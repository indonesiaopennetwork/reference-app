# The update flow (`/update` → `/on_update`)

How a buyer changes a confirmed order, and how the seller decides on it.

Living document, same convention as `discovery-service/CONTEXT.md`: append
or mark superseded, don't delete history.

## 1. What it does

A buyer who has already confirmed an order can ask the seller to change its
delivery/contact details. The seller reviews the request in their dashboard
and approves or rejects it. The buyer is then told which of those happened.

Two properties are worth stating up front, because both are deliberate
departures from how the other actions in this repo behave:

1. **An update is never applied automatically.** Unlike `confirm` or
   `cancel`, which `bpp-application` processes unattended, an inbound
   `/update` is queued as a `PENDING` row for a human seller to decide on.
   An update changes committed order terms after the fact; this app treats
   that as a decision, not a formality.
2. **`on_update` is sent only once a seller has decided** — not on receipt.
   The inbound `/update` gets a transport-level ACK and nothing more, so a
   buyer's "awaiting review" state can legitimately last hours.

## 2. End-to-end path

```
bap-frontend  "Request Change"
     │  POST /api/v1/update {transaction_id, contract}
     ▼
bap clientsvc.Update
     │  · scopes the transaction to the logged-in buyer
     │  · CONFIRMED ──CAS──▶ UPDATE_SENT   (MarkUpdateSentIfConfirmed)
     │  · records the proposed contract    (update_requests, PENDING)
     ▼  POST {BAP_CALLER_URL}/update  →  onix-bap  →  network  →  onix-bpp
bpp updatesvc.Process
     │  · validates: parseable txn id, contract present, contract ACTIVE
     │  · INSERT contract_update_requests (PENDING)      ← no mutation yet
     │  · ACK/NACK audited to beckn_message_log (PII redacted)
     ▼
     ⏸  waits for a human
bpp-frontend  seller approves / rejects
     │  POST /api/v1/update-requests/:id/{approve,reject}
     ▼
bpp updatesvc.ApproveRequest / RejectRequest
     │  · PENDING ──CAS──▶ APPROVED|REJECTED  (DecideUpdateRequestIfPending)
     │  · approve only: contracts.contract_attributes ← requested_attributes
     │  · sends on_update carrying the contract as it now stands
     ▼  POST {BPP_CALLER_URL}/on_update  →  onix-bpp  →  network  →  onix-bap
bap on_update_svc.Process
     │  · UPDATE_SENT ──CAS──▶ CONFIRMED   (MarkUpdateReceivedIfPending)
     │  · classifies the seller's decision (§3) and resolves update_requests
     │  · overwrites the contract snapshot
     ▼
bap-frontend  order detail shows "Change approved" / "Change declined" / …
```

## 3. How the buyer learns the decision

**This is the part of the flow with no protocol support, so read this
before changing anything here.**

Beckn v2's `on_update` carries a contract and nothing else. There is no
`approved`/`rejected` field, and nowhere to put a rejection reason. A BPP
that rejects an update replies with the contract **unchanged** — which on
the wire is indistinguishable from a BPP that approved a change that
happened to change nothing. A BAP receiving `on_update` alone can only
tell the buyer "your order is back to CONFIRMED".

The decision is therefore **recovered by comparison, not read off a
field**. `bap-application` stores the contract the buyer proposed
(`update_requests.proposed_contract`, migration `012`) and, when
`on_update` arrives, compares the returned contract against both that
proposal and the pre-update snapshot
(`internal/on_update_svc/classify.go`):

| Returned contract matches | Outcome | Shown to the buyer |
|---|---|---|
| the buyer's proposal | `APPLIED` | Change approved |
| the pre-update contract | `DECLINED` | Change declined |
| neither | `MODIFIED` | Seller responded with different details |
| — (no `on_update` ever arrived) | `ABANDONED` | Change request timed out |
| — (still in flight) | `PENDING` | Change requested, awaiting review |

Comparison is **semantic, not bytewise**: both sides are parsed to Go
values first, so JSON key ordering, whitespace and number formatting
introduced anywhere along the path can never masquerade as a seller
decision.

`MODIFIED` is deliberately its own outcome rather than being folded into
`APPLIED` or `DECLINED`. It covers a genuine seller counter-offer, but it
also covers an intermediary that re-serialised or stripped a field in
transit — and in both cases claiming a definite "approved" or "rejected"
would report a decision the seller never made.

### Why not add a field to the wire?

Because an unpublished `ion:*` extension is rejected by onix's Extended
Schema validation, which is exactly the failure mode this repo has already
hit (see the `schema.beckn.io` / `RetailConsideration` enum incident). Any
richer channel here needs a real schema published through whatever process
publishes the existing `ion:*` extensions — not improvised inline. This is
the same call the Support flow made for the same reason.

### Known limitation: the rejection reason doesn't reach the buyer

The seller's reason is captured in
`contract_update_requests.reject_reason` and shown in the seller's own
dashboard. It has nowhere to travel on the `on_update` wire, so the
buyer sees "Change declined" without a reason and is pointed at the
existing support channel. Closing this properly requires the published
schema above.

## 3a. What the seller sees, and why it isn't a diff

The seller's review screen shows a **one-line headline** per request
("Buyer wants to change: delivery address and phone") and, on expanding,
**only the fields that actually differ**, with human labels and the
seller's current value alongside.

It deliberately does **not** diff the two contracts. It used to, and the
result was unusable: hundreds of rows of dotted leaf paths like
`commitments.0.offer.offerAttributes.policies.cancellation.window`, every
one of them showing `CURRENT —`.

Two reasons a diff cannot work here, no matter how it's implemented (in Go,
or with a Postgres `jsonb` comparison):

1. **There is usually nothing to compare against.**
   `contracts.contract_attributes` is a small JSON-LD extension bag and is
   routinely `NULL`. Diffing a full proposal against `NULL` reports every
   field as new.
2. **The two sides aren't the same document.** The buyer sends a full
   replacement contract built from the *BAP's* snapshot; the seller stores
   the live order **normalised** across `contract_performances`,
   `commitments` and `considerations`. They're two different
   representations of one order, not two versions of one document.

So `internal/orderchange` compares only an explicit **whitelist of
buyer-changeable fields**, reading each side from its own authoritative
location — the seller's from
`contract_performances.performance_attributes.deliveryDetails`, the
buyer's from `performance[].performanceAttributes.deliveryDetails`.

That buys four things a generic diff can't: protocol noise (`@context`,
`@type`, `policies`, `serviceability`) can never appear because it isn't on
the list; every field gets a human label; the output is stable when either
side's JSON shape shifts; and it's unit-testable with no database.

**To support a new changeable field, add one entry to `deliveryFields` in
`internal/orderchange/changeset.go`.** Nothing else changes.

### Approval writes where fulfilment reads

Approving applies the whitelisted delivery block to
`contract_performances` via `ApplyPerformanceDeliveryDetails`. This fixed a
real bug: approval used to write the proposed contract into
`contracts.contract_attributes` only, which nothing consults for delivery —
so a seller could approve an address change and the parcel would still go
to the old address. If the apply fails, the error is loud, because the
decision is already recorded as `APPROVED` and the order is now
inconsistent with what the seller agreed to.

### Fulfilment-phase warnings: an extension point, not a feature

The screen has a place for "this order already shipped, so the address
can't change" — but **fulfilment phase is not tracked yet**.
`contract_performances.status_code` is written once (`PENDING`) at init,
nothing advances it, there's no seller endpoint to move it, and
`contract_tracking` is unused.

So the warning machinery exists and is wired end to end, and reports
nothing while the phase is unknown. To switch it on, implement
`orderchange.PhaseReporter` and replace the single `NoPhaseTracking{}` call
site in `dashboardsvc`. Per the product decision, a warning **never blocks
approval** — a seller can sometimes redirect a parcel, so the system
advises and the human decides. `feasibility.go`'s header has the full
instructions, and a test proves warnings appear as soon as a reporter says
the order has left the seller.

### Rejection reasons

Optional, with one-tap presets ("Already dispatched", "Outside delivery
area", …) plus free text. The reason is the seller's own record — it has
nowhere to travel on `on_update`, so the buyer sees only that the change
was declined (see §3's known limitation).

## 3b. Buyer-side polling cadence

The checkout screen polls `/status/:id`, which carries
`update_request.outcome` alongside the transaction status so the buyer sees
the verdict rather than the order silently reverting to `CONFIRMED`.

The interval scales with what's being waited on: **2s** for
select/init/confirm, which resolve in seconds, and **20s** for
`UPDATE_SENT`, which waits on a human and can sit for hours. At 2s that
state was issuing roughly 1,800 pointless requests an hour. The effect also
depends on `transaction.id`/`transaction.status` rather than the whole
`transaction` object — each poll produces a fresh object, so depending on
the object rebuilt the interval on every tick.

## 3c. The seller's reason reaches the buyer

**Correction to an earlier statement in this document's history:** the
rejection reason *can* travel. The distinction that matters:

- Inventing a **new** `ion:*` `@type` fails onix's Extended Schema
  validation, because the schema isn't published at `schema.ion.id`.
- Adding a property **inside an already-published type** does not.
  `IONTradeContract` — the type that attaches to
  `Contract.contractAttributes` — declares `additionalProperties: true`.

So on each decision the BPP attaches:

```json
"contractAttributes": {
  "@type": "ion:IONTradeContract",
  "updateDecision": {
    "outcome": "REJECTED",
    "reason": { "code": "ALREADY_DISPATCHED",
                "name": "Already dispatched",
                "longDesc": "<seller's note>" }
  }
}
```

The shape follows ION's own convention for an outcome reason: TradeContract
models `cancellation.reason` as a beckn `Descriptor` whose `code` is the
machine-comparable value. This mirrors that rather than inventing a
different reason shape. Reason **codes** (not free text) are what the
seller picks, so the buyer sees consistent wording whichever seller
declined — which is the point: a buyer who understands *why* stops
re-sending the same request, and the seller stops re-evaluating it.

`internal/on_update_svc/decision.go` treats a stated decision as
**authoritative**, and falls back to the inference in §3 when it is absent.
That keeps this working against any ION peer that doesn't send the field.

**Known limit:** `updateDecision` is not in the published TradeContract
schema. Other participants will ignore it (they won't error). Making it
genuinely interoperable means proposing it upstream.

## 3d. Limits and history

| Constant | Value | Where |
|---|---|---|
| `MaxUpdateRequestsPerOrder` | 5 | `clientsvc/errors.go` |
| `BuyerUpdateHistoryLimit` | 5 | `clientsvc/errors.go` |
| `ORDER_DETAIL_REFRESH_MS` | 1 hour | `bap-frontend/src/App.jsx` |

The cap counts **all** outcomes, not just pending ones: it exists so a
buyer can't make a seller re-evaluate one order indefinitely, and a
declined attempt cost the seller attention just as an approved one did. It
is checked *before* the `CONFIRMED → UPDATE_SENT` compare-and-swap, so a
refused request never consumes the transition. Exceeding it returns **409
Conflict** — the request is well-formed, the order has simply spent its
allowance. Raising the limit is a one-line change.

`GET /orders/:id` returns `update_requests[]` (newest first, capped) and
`update_quota {used, max}` alongside the existing singular
`update_request`. The seller's dashboard filters across all statuses, which
is its full history.

Buyers can raise a change from **two** places: the live checkout panel, and
the order detail view reached from order history — which is where it
actually matters, since a wrong phone number is usually noticed hours after
checkout. Both are gated on `CONFIRMED` (so never while one is in flight)
and on remaining quota.

## 3e. Two bugs worth not reintroducing

**Outbound messages must carry the FULL contract, never a stub.** beckn's
`Contract` requires `commitments` and is `additionalProperties: false`, so a
hand-built `{"id": "..."}` is rejected by onix with
`SCH_SCHEMA_VALIDATION_FAILED / property "commitments" is missing`. That is
why every `/request-status` call failed while `/cancel` worked — cancel
rebuilt the contract from the snapshot, status sent a stub. Both now share
`clientsvc.contractFromSnapshot`, which also unwraps the two shapes
snapshots are stored in (raw contract, or a `{"message":{"contract":…}}`
envelope). Use it for any new outbound action rather than assembling a
contract by hand.

**The seller's "what changed" must diff against a stored snapshot, not the
live contract.** Comparing live-vs-requested reads correctly while a request
is PENDING, but the instant it is APPROVED the live contract *holds* the
requested values — so the diff collapses to zero rows and the screen showed
"No changes" for a change it had just applied, with no record of what was
approved. `contract_update_requests.previous_delivery_details` (migration
030) captures the before state when the request is **queued**, and
`orderchange.SummarizeFromDeliveryDetails` compares against that. A
regression test pins both halves, including the old collapsing behaviour as
an explicit precondition.

The seller's table headers follow the decision, so a decided row reads as a
record rather than an open question: `Currently / Buyer asked for` while
PENDING, `Was / Changed to` once approved, `Kept as / They asked for` once
rejected.

## 3f. Where the buyer acts, and the trapdoor that blocked them

A buyer can act on an order from **two** places: the live checkout panel,
and the order detail view in order history — which is where it normally
happens, since a wrong phone number is noticed days later. Order detail has
one button per flow: **Check status**, **Cancel order**, **Request a
change** (with the remaining-quota count beside it). When an order isn't
actionable the reason is stated rather than the buttons silently
disappearing.

**A status query must never change the order's state.** `RequestStatus`
used to write `STATUS_SENT`, and that was a one-way trapdoor: nothing ever
moved a transaction back out of it — `on_status` doesn't touch the status,
and the sweep only recovers `UPDATE_SENT` — while Update *and* Cancel both
gate on `CONFIRMED`. So one tap on "Check Status" permanently blocked every
remaining action on that order. Five orders were stuck this way. Asking
"where is my order?" is a read; it no longer writes anything. The
`STATUS_SENT` enum value is kept for compatibility, but nothing should
write it again.

**Outbound calls need a cold-cache-sized timeout.** The BAP's client to
onix was 10s. onix validates every outbound action against the ION
Extended Schemas, fetching a ~19-document `$ref` graph from
`schema.ion.id`; measured cold cost for a full contract is 9–22s, warm is
single-digit milliseconds. So the first call after any onix restart failed
and looked like a broken endpoint rather than a slow one. Raised to 60s —
onix caches for 24h, so the ceiling is reached at most once.

## 3g. Callbacks must not run on the caller's request context

`on_update` delivery runs in a goroutine on `context.Background()`, not on
the seller's HTTP request context.

This was a real bug: delivering on the request context tied the callback's
lifetime to the seller's browser. onix validates the outbound contract
against the ION Extended Schemas (~9-22s on a cold cache), so the seller's
request routinely ended first and the POST died with `context canceled` —
the decision was recorded, the seller saw success, and **the buyer was
never told anything**, staying on "Waiting for the seller" forever. The
`beckn_message_log` write failed for the same reason, so there wasn't even
a NACK row to show what happened.

`confirmsvc` and `cancelsvc` already delivered their callbacks on
`context.Background()` for exactly this reason; the decision path was the
one that didn't. Any new outbound callback should follow the same pattern.

A related BAP-side bug: a stale `PENDING` row (from an earlier request
whose `on_update` never arrived) blocked the partial unique index, so a new
request inserted nothing and a later `on_update` resolved the **old** row —
showing the buyer the wrong proposal. `Update` now retires stale rows first,
which is safe because `MarkUpdateSentIfConfirmed` only succeeds when the
transaction is `CONFIRMED`, i.e. nothing is actually in flight.

## 3h. A request that changes nothing is refused

Submitting the change form without editing anything used to send a request,
which reached the seller as a blank "No changes to delivery details" row
and cost them a decision for nothing. Guarded at three layers:

| Layer | Behaviour |
|---|---|
| Buyer UI | Submit disabled, with "Nothing changed yet — edit a detail above" |
| BAP (`clientsvc.Update`) | `ErrNoChangeRequested` → **400**, before any state changes |
| BPP (`queueUpdateRequest`) | Refuses to queue; audited as a NACK |

The BAP is the **primary** guard, not merely an optimisation: the BPP's
handler ACKs an inbound `/update` *before* `Process` runs in a goroutine, so
a seller-side refusal cannot reach the caller as an error. A no-op rejected
only at the BPP would leave the transaction at `UPDATE_SENT` with no
`on_update`, stuck until the 48h sweep. Refusing at the BAP also avoids
spending one of the order's five allowed requests on a request that asks for
nothing — verified: status and quota are untouched by a refusal.

Comparison is semantic (`contractx.JSONEqual`), so key reordering and
number formatting are not "changes". If either side has no delivery block
the request is **allowed through**: an unprovable case must not be refused,
since refusing a legitimate request is worse than forwarding a redundant one
the seller-side check still catches. The BPP reuses
`orderchange.SummarizeFromDeliveryDetails` — the same comparison its review
screen renders from — so "queued" and "has something to show" cannot
disagree.

### A minimal on_update must not shadow the contract snapshot

Found while verifying the above. `on_update` may legitimately carry little
more than an id and a decision (this app's own rejection path does exactly
that when the contract has no attributes). Storing it made it the **newest**
snapshot, shadowing the complete `on_confirm` one — after which everything
that builds an outbound contract from "the latest snapshot" failed with
`property "commitments" is missing`: `/request-status`, `/cancel`, and the
no-op detection above, which could no longer find a delivery block to
compare.

`on_update_svc` now audits such a callback and advances the status as
before, but refuses to store it as the snapshot
(`contractx.HasOrderCore`). The rule: never replace good order state with
worse.

## 4. Concurrency and failure handling

Every state transition in this flow is a compare-and-swap, not a
read-then-write, so concurrent or replayed messages can't corrupt state:

- **`MarkUpdateSentIfConfirmed`** (`CONFIRMED → UPDATE_SENT`) is what
  enforces "one update in flight per transaction". A second `/update`
  affects 0 rows and is refused.
- **`MarkCancelSentIfConfirmed`** (`CONFIRMED → CANCEL_SENT`) gates on the
  same column, so cancel and update are mutually exclusive without needing
  a separate lock — a transaction mid-update cannot be cancelled underneath
  itself.
- **`MarkUpdateReceivedIfPending`** (`UPDATE_SENT → CONFIRMED`) affects 0
  rows for a duplicate, replayed or unsolicited `on_update`. Those are
  still audited to `beckn_message_log`, but are never allowed to overwrite
  the committed contract snapshot.
- **`DecideUpdateRequestIfPending`** (BPP) means two sellers clicking
  approve and reject at the same time produce exactly one decision.
- **`ResolveUpdateRequest`** (BAP) is likewise `PENDING`-predicated, so a
  duplicate callback can't rewrite a decision already recorded.
- A partial unique index on `update_requests (transaction_id) WHERE
  outcome = 'PENDING'` is a database-level backstop for the same invariant.

Failure paths:

- **Delivery to the BPP fails outright** — `Update` rolls the transaction
  back to `CONFIRMED` and marks the tracking row `ABANDONED`, rather than
  stranding it at `UPDATE_SENT` where every future update would be refused.
- **Delivery succeeded but no `on_update` ever came back** — not detectable
  synchronously, so `bap-application/internal/sweep` handles it: a
  transaction at `UPDATE_SENT` past `DefaultStaleAfter` (**48h**) is rolled
  back to `CONFIRMED` and its request marked `ABANDONED`. The threshold is
  deliberately in hours, not the seconds/minutes of a delivery timeout,
  because a slow *human* review is expected and must never be mistaken for
  a stuck transaction.
- **A request sits unreviewed** — `bpp-application/internal/sweep` logs
  anything `PENDING` past **24h**. It is deliberately read-only: a pending
  request needs a human decision, so the only safe automatic action is
  making the backlog visible.

Both sweeps are a single in-process background goroutine started in
`cmd/server/main.go` and stopped on shutdown — no queue, no cron service.

## 5. Update semantics: PUT, not PATCH

`/update` carries a **full replacement contract**, not a merge patch.
`bpp-application` stores and applies `requested_attributes` wholesale. The
buyer UI honours this by fetching the current contract, patching only the
delivery/contact fields into it, and submitting the whole thing back.

A merge patch would need a documented merge algorithm both sides agree on;
the spec doesn't define one, so a wholesale replacement is what's
implemented. Don't partially apply a subset of the fields.

## 6. Data model

**BAP** (`bap-application/db/migrations/012_update_requests.sql`)

`update_requests` — the buyer's own record of what it asked for and what
came back. Never authoritative for contract state; `contract_snapshots`
is. Holds `proposed_contract`, `outcome` (`update_outcome` enum), and the
outbound `message_id` for audit correlation.

**BPP** (`bpp-application/db/migrations/029_contract_update_requests.sql`)

`contract_update_requests` — the queue of buyer proposals awaiting seller
decision. Holds `requested_attributes`, `status` (`update_request_status`
enum), and `reject_reason`. Only `APPROVED` rows are ever applied to
`contracts.contract_attributes`.

Deliberately a separate table rather than a new `contracts.status` value:
"this ACTIVE contract has a pending proposed change" is an orthogonal axis
to the contract's own `DRAFT`/`ACTIVE`/`CANCELLED`/`COMPLETE` lifecycle.

## 7. Privacy

An update payload carries delivery address and contact details. Both sides
redact PII from everything they log — `beckn_message_log` rows and stdout
alike (`internal/{updatesvc,on_update_svc}/redact.go`). The unredacted
payload is persisted only in the update-request tables, reachable solely
through the authenticated dashboard/order views.

## 8. Endpoints

| Side | Method | Path | Purpose |
|---|---|---|---|
| BAP | `POST` | `/api/v1/update` | Buyer submits a change request |
| BAP | `GET` | `/api/v1/orders/:id` | Returns `update_request.outcome` |
| BAP | `POST` | `/api/webhook/on_update` | Seller's decision arrives |
| BPP | `POST` | `/api/webhook/update` | Buyer's request arrives |
| BPP | `GET` | `/api/v1/update-requests` | Seller's review queue |
| BPP | `POST` | `/api/v1/update-requests/:id/approve` | Approve |
| BPP | `POST` | `/api/v1/update-requests/:id/reject` | Reject (optional `{reason}`) |

Buyer-facing BAP endpoints are scoped to the logged-in buyer: a
transaction belonging to someone else is reported as not found, so buyers
can't probe transaction IDs. Seller-facing BPP endpoints are scoped by
`ProviderIDFromContext`, the same convention as the rest of `dashboardsvc`.

## 9. Known constraints

- **`on_update` does not echo the request's `messageId`.** Beckn v2 says a
  CN correlates a callback by the `messageId` from the request context.
  Every `on_*` service in this repo — select, init, confirm, cancel,
  support, status and update alike — mints a fresh `messageId` instead, and
  correlates on `transactionId` plus the state compare-and-swap. The update
  flow is consistent with the rest of the codebase here rather than
  correct against the spec; fixing it is a repo-wide change, not an
  update-flow one.
- **Not yet testable end-to-end against the real network.** `on_update`
  is delivered through `onix-bap`'s `bapTxnReceiver`, which verifies the
  signer against the live DeDi registry — and the BPP identity is not
  registered there, so the callback fails signature validation exactly as
  `select`/`init`/`confirm` do. See `INFRA_SETUP.md` §4. Until that's
  resolved, the seller-decision half of this flow is exercised by unit
  tests and locally with onix auth disabled, not against `fabric.nfh.global`.
