# ION network extension: additional endpoints

The reconciliation endpoints, `/reconcile` and `/on_reconcile`, are ION
Release 1 `core/api/v2.0.0/ion-additional.yaml` in
[ion-specs](https://github.com/indonesiaopennetwork/ion-specs): every ION
endpoint that Beckn v2.0.0 does not define. Their settlement object,
`IONSettlementReconciliation`, is the ion-specs `common/Settlement/v1`
attribute pack.

Both ONIX adapters load the Beckn core contract and these endpoints from the
published Release 1 (`location`, and `auxiliaryTypes: "url"` /
`auxiliaryLocations`, in the ONIX configs):

```text
https://schema.ion.id/release1/schema/vendored/beckn/api/v2.0.0/beckn.yaml
https://schema.ion.id/release1/schema/core/api/v2.0.0/ion-additional.yaml
```

`ion-additional.yaml` references the first (ion-specs' vendored copy of
Beckn core `v2.0.0-lts`, unchanged). The adapters fetch the settlement pack,
like every other ION `@context` type, from schema.ion.id. A change to either belongs in ion-specs; restart the adapters
to pick it up.

**For now (local compose only)** the adapters validate against ion-specs PR #25
(`feat/recon-per-settlement`, which includes PR #24), not yet published: a
bundled `schemas/ion-additional.yaml` (`auxiliaryTypes: "file"`) and bundled
`IONSettlementReconciliation`, `IONTradeOffer` and `IONTradeConsideration`
packs in `schemas/` -- see `schemas/README.md`. Switch back once both PRs are
merged and published.

## Money model: the PJP holds the payment

The buyer pays into the PJP (Penyelenggara Jasa Pembayaran), which **holds**
the funds; neither the BAP nor the BPP receives them. Once both participants
agree a contract's figures, SEAM (Settlement Assurance Mechanism) has the PJP
pay it out:

| Recipient | Receives |
|---|---|
| BPP | `netPayable` = `orderAmount − commission − withholdingAmount − refundAmount` |
| BAP | `commission` — the commission (finder fee) the seller pays it |
| Buyer | `refundAmount` — refunds made before the payout |
| Stays at the PJP | `withholdingAmount` (0 in this app) |

No tax is withheld from payouts. SEAM reads the agreement from the message
log; payout status comes back through the PJP's webhook, outside this API.

## Settlement terms are the seller's, on each offer

The seller publishes its terms on every offer (ion-specs
`feat/offer-settlement-policy`):

- the **buyer finder fee** it pays the buyer app, on the offer's
  consideration: `considerationAttributes.buyerFinderFee`
  (IONTradeConsideration), one per offer -- `feeType` `PERCENT` of the offer's
  item value without tax (`appliesTo: ITEM_TOTAL`) or with tax
  (`ORDER_TOTAL`), or a fixed `AMOUNT` per unit sold, of `feeValue`;
- the **payout term**, `offerAttributes.policies.settlement` (IONTradeOffer)
  -- `settlementBasis` and `settlementWindow`.

The seller sets them per offer on the Add Offer form (under Policies); an
offer without them gets the BPP's defaults (env `COMMISSION_TYPE`, `COMMISSION_VALUE`,
`COMMISSION_APPLIES_TO`, `SETTLEMENT_BASIS`, `SETTLEMENT_WINDOW`) when it is
saved, published or ordered. At `select`, `init` and `confirm` the BPP puts the
terms stored for each offer on the order's commitments, so the buyer app can't
change them. The buyer app takes the seller's fee as it is.

The contract's price consideration carries the order's total fee
(`buyerFinderFee.feeAmount`): each offer's fee on its own items still ordered,
summed; the settlement's `commission` equals it. With several offers, the
payout term giving the latest due date applies. Offers and orders stored the
earlier ways -- the fee as `policies.commission` entries or as a
consideration's `commission`, under the field names `name`, `type`, `value`,
`amount` -- are still read; migration 038 moves stored offers' fees onto their
considerations, and an offer is rewritten when it is next saved or
published.

## One settlement object, from init to reconcile

The order's settlement is one `IONSettlementReconciliation` object, the
`settlementAttributes` of the contract's payment settlement. It is built up
through the order and sent as it stands in `/reconcile`:

| Step | Who | Adds or changes |
|---|---|---|
| `on_select` | BPP | the offers' terms, and the commission on the quoted price |
| `init` | BAP | its `settlementAccounts` entry (`holderRole: BAP`), `method` / `paymentRail` QRIS, `collectedBy: BAP`, `timing`, `status: PENDING`, figures estimated on the quoted price |
| `on_init` | BPP | the offers' terms as stored, the commission on its final price, its account (`holderRole: BPP`), the figures; the BAP checks the commission and logs a mismatch |
| `confirm` | BAP | `status: PAID`, `paidAt`, `gatewayRef`; `orderAmount` is the amount paid |
| `on_cancel`, `on_update` | BPP | the commission (on the replacement price), `commission`, `refundAmount`, `netPayable` follow cancellations and returns |
| `/reconcile`, `/on_reconcile` | both | each participant's own figures, `orderDate`, `dueDate`, `collections`, a refund `breakup` and its own account's allocation, with `accord` in the answer |

Until reconciliation the figures are estimates. Both apps keep this logic in
one package, `internal/settlement` (the two copies are kept identical). Refund
settlements stay ION TradeSettlement records.

The settlement object no longer carries the terms (`finderFee`,
`settlementBasis`, `settlementWindow`). An order placed before they moved to
the offer still has them there (or in `contractAttributes`), and both apps
keep using them for it: its commission, due date and completion are worked
out as before.

Both apps still accept a contract that declares an `IONPayment` (from before
the settlement object) and convert it.

## When a contract is due

A contract is due at `basis event + settlementWindow` -- with several offers,
at the latest of their terms' due dates: delivery for
`AFTER_DELIVERY` / `AFTER_SHIPMENT` (shipment isn't recorded separately),
confirmation for `ON_CONFIRMATION`, the close of the longest return window for
`AFTER_RETURN_WINDOW`. A contract cancelled before delivery is due at the
cancellation plus the window.

A contract sent before it is due on the answering side is answered
`accord: false` with the dispute code `SETTLEMENT_WINDOW_NOT_REACHED` and the
reason (when it falls due, or that the order isn't delivered yet): it is
**pending**, not disputed, and is sent again once due. The other contracts of
the batch are not affected.
The BAP always checks this against the real settlement window;
`RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW=false` only lets a participant send
early.

Each contract is reconciled **once**, through its payment settlement, together
with the refunds made before the payout. A refund the BPP makes after the
payout (`refund.source: PROVIDER`) is outside reconciliation in ION Release 1:
the BPP pays the buyer itself and the dashboards report it. Sellers of
returnable goods can avoid this with `AFTER_RETURN_WINDOW`.

## Messages

Either participant sends `/reconcile`; the other answers `/on_reconcile`.
`context.bapId` / `bppId` are always the pair; `senderId` / `receiverId` say who
sent it. There is no reconciliation ID on the wire: `context.transactionId`
is the exchange (created by whichever participant starts it) and
`context.messageId` each `/reconcile` and its answer. Each app keeps its own
name for an exchange (`reconciliations.id`, `recon-<date>T<HHMMSS>Z-<senderId>`
of the participant that started it) and finds it by `transaction_id`.

`message.contracts[]` are Beckn Contract snapshots: the contract `id`, the
minimal `commitments` Beckn requires, and the payment settlement (its `id`)
whose `settlementAttributes` is an `IONSettlementReconciliation`:

| Field | |
|---|---|
| `collectedBy`, `currency`, `status`, `paidAt`, `gatewayRef`, ... | the payment |
| `orderDate`, `dueDate` | the order's creation date (Asia/Jakarta) and the payout due date |
| `orderAmount`, `commission`, `withholdingAmount`, `refundAmount`, `netPayable` | `{value}`, plus `diffValue` (answer minus request) in a disagreeing answer |
| `collections[]` | who collected the buyer's money: `PREPAID` into the PJP, or `COD` by the `collectedBy` party |
| `breakup[]` | the refund, itemized per refund settlement: `CANCELLATION`, or `RETURN` once delivered |
| `settlementAccounts[]` | the BPP's always; the BAP's when `commission` > 0. Each participant marks its own with `holderId`, `allocationFor` and `allocatedAmount` (its share: the BPP keeps all of `netPayable`, as it has no seller accounts to split it among) |
| `accord` | answer only: `true` agrees, `false` disputes or leaves it pending |
| `dueDate` / `dispute` | answer: when `accord` is `true` / `false` (`code`, `field`, `description`) |

Agreement is **per settlement**. An agreed settlement is `AGREED` and goes on
to SEAM whatever happens to the others; a disputed one (`COMMISSION_MISMATCH`,
`REFUND_MISMATCH`, `MISSING_CONTRACT`, ...) is `DISPUTED`; a pending one
(`SETTLEMENT_WINDOW_NOT_REACHED`, `AWAITING_INFORMATION`) keeps no status and is
sent again later. The answer's `reconciliationStatus` only summarizes:
`DISPUTED` when any is disputed, otherwise `PENDING` when any is pending,
otherwise `AGREED`. Its `settlementSummary` counts contracts and settlements
by outcome and totals the agreed and unreconciled commission and net payable;
the sender's adds `orderDayCounts` (its orders per order day, and how many are
not yet reconciled). A settlement agreed before and sent again is agreed
again, never paid twice.

On the reconciliation pages the operator ticks what the next `/reconcile`
carries -- any contracts that are due or disputed, for one counterparty at a
time, filtered by the contracts' creation date -- and they go out as a new
exchange; a disputed contract moves to it from its old one. (Called without a
selection, Send resends the disputed and now-due pending settlements of an
exchange the participant started -- only those, in the same exchange: same
`transactionId`, new `messageId` -- or else sends everything due.) Each
batch's settlements open to show both sides' attributes. Escalation to
`/raise` comes later.

The commission is computed from the contract: each offer's buyer finder fee on
its own items still ordered -- a `PERCENT` fee on the unit price (`ITEM_TOTAL`)
or unit total with tax (`ORDER_TOTAL`), an `AMOUNT` per unit -- summed; a fully
cancelled order has none. The result is the price consideration's
`buyerFinderFee.feeAmount`, and a settlement's `commission` equals it, so the two apps compare the commission each copy of the contract
states. An order placed before the commission moved to the offer uses its
`finderFee` by the same rule.

## Examples

In the shape the apps build, validated against ion-specs PR #25's
`ion-additional.yaml` and the vendored Beckn `beckn.yaml`:
`examples/reconcile.json` (the BPP's batch for 2026-09-30, with its own
account's allocation and the order-day counts) and `examples/on_reconcile.json`
(the BAP agrees with every settlement). Each order paid 90,000 (80,000 of items plus PPN) with a 3% finder
fee on the item total:

| Contract | Paid | Refund | Finder fee (BAP) | Net payable (BPP) |
|---|---|---|---|---|
| no changes | 90,000 | 0 | 2,400 | 87,600 |
| 1 of 2 units cancelled | 90,000 | 45,000 | 1,200 | 43,800 |
| returned in full (Rp 200 restocking fee) | 90,000 | 89,800 | 0 | 200 |
| **Summary** | **270,000** | **134,800** | **3,600** | **131,600** |
