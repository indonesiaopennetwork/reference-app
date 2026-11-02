# API documentation

Swagger (OpenAPI 2.0) specs for the two backend services, **generated from the code** with [swag](https://github.com/swaggo/swag):

| File | Service | Endpoints |
|---|---|---|
| [bap.yaml](bap.yaml) | `bap-application` — buyer backend | 31 |
| [bpp.yaml](bpp.yaml) | `bpp-application` — seller backend | 44 |

Don't edit these files by hand — they are rebuilt from the `// @...` annotations above each handler by `task docs:api` (see [Keeping the specs current](#keeping-the-specs-current)). They describe what the code does today, including some behaviour that may be worth changing (see [Things to know](#things-to-know)). For how the services fit together, see [../architecture.md](../architecture.md).

## Viewing the specs

- **Browser:** open [editor.swagger.io](https://editor.swagger.io) and paste the file in (File → Import file). You get the interactive Swagger UI on the right.
- **VS Code:** install the *OpenAPI (Swagger) Editor* extension and open the `.yaml`; use its preview.
- **Try a call:** with the stack running (`task local:up`), *Try it out* in Swagger UI calls the API on the spec's host — set it to `localhost:8083` (BAP) or `localhost:8080` (BPP).

## How a Swagger file is organised

Every spec has the same parts:

| Part | What it holds | Comes from |
|---|---|---|
| `info`, `basePath` | Title, version, a description of the whole API | The `@title` / `@description` block above `main()` in `cmd/server/main.go` |
| `securityDefinitions` | `BearerAuth` — the Zitadel token in the `Authorization` header | Same block |
| `paths` | Every endpoint: method, `tags`, `parameters` (path, query and the `body`), one entry per status code under `responses` | The annotations above each handler (`@Router`, `@Param`, `@Success`, `@Failure`, …) |
| `definitions` | The data shapes, referenced with `$ref` | The Go structs named in the annotations — named `<package>.<Type>`, e.g. `clientsvc.ClientSelectRequest` |

Reading an endpoint: `paths` → URL → method → the `in: body` parameter (what you send) and `responses` (one entry per status code, each with a schema). When you see `$ref: '#/definitions/X'`, look up `X` under `definitions`. Response types that only exist for the docs (because the handler builds its JSON inline) live in each package's `apidoc.go`.

---

## BAP API walkthrough (`bap.yaml`)

### The three kinds of endpoints

| Group | Who calls it | Auth | Purpose |
|---|---|---|---|
| **Client API** `/api/v1/*` | `bap-frontend` (the buyer shop) | Optional bearer token | Search, place and manage orders, read history |
| **Beckn webhooks** `/api/webhook/*` | `onix-bap` (the Beckn adapter) | None — the adapter already verified the Beckn signature | Receive the seller's answers (`on_select`, `on_confirm`, …) |
| **Reconciliation** `/api/v1/reconciliation*` | The frontend's reconciliation page | None | Settlement overview and sending `/reconcile` |

Plus `GET /health` for liveness.

### The one pattern to understand: request now, answer later

Beckn is asynchronous. Every order action works in two halves:

1. **The client call** (e.g. `POST /api/v1/select`) builds a Beckn message, saves the new status (e.g. `SELECT_SENT`), sends it to the seller through `onix-bap`, and returns as soon as the adapter ACKs. **The response contains no seller data** — just a `transaction_id` or the new status.
2. **The seller's answer** arrives later as a separate request on a webhook (e.g. `POST /api/webhook/on_select`). The BAP ACKs immediately, then in the background updates the status (e.g. `QUOTE_RECEIVED`) and saves the contract as a snapshot.
3. **The frontend is told** over `GET /api/v1/status/{id}/stream` (server-sent events) the moment the change is saved. It falls back to polling `GET /api/v1/status/{id}` only while that stream is down.

That's why every client action in the spec says "the result arrives later via `/api/webhook/on_…`".

### Placing an order, end to end

| Step | Call | Body | Returns | Status after |
|---|---|---|---|---|
| 1. Search | `GET /api/v1/discover?q=coffee` | — | catalogs (`clientsvc.DiscoverResponse`) | — |
| 2. Choose items | `POST /api/v1/select` | `clientsvc.ClientSelectRequest` — `items[]` from the search result | `{transaction_id}` (`clientsvc.SelectResponse`) | `SELECT_SENT` → `QUOTE_RECEIVED` |
| 3. Buyer details | `POST /api/v1/init` | `clientsvc.ClientInitRequest` — `transaction_id` + `billing` | `{status: INIT_SENT}` | → `INIT_RECEIVED` |
| 4. Pay and confirm | `POST /api/v1/confirm` | `clientsvc.ClientConfirmRequest` — `transaction_id` | `{status: CONFIRM_SENT}` | → `CONFIRMED` |
| Check progress | `GET /api/v1/status/{id}` | — | `clientsvc.ClientStatusResponse` — status + latest contract | — |

Payment in step 4 is a **mock**: the BAP marks the settlement `PAID` with a generated reference; there is no payment gateway.

### After the order

| Call | Beckn action sent | Status set | Answer arrives on |
|---|---|---|---|
| `POST /request-status` | `status` | `STATUS_SENT` | `on_status` → `CONFIRMED` / `CANCELLED` / `COMPLETE` |
| `POST /cancel` | `cancel` | `CANCEL_SENT` | `on_cancel` → `CANCELLED` |
| `POST /cancel-items` | `cancel` (all items) or `update` (some) | `CANCEL_SENT` or `UPDATE_SENT` | `on_cancel` / `on_update` |
| `POST /return-items` | `update` | `UPDATE_SENT` | `on_update` |
| `POST /update` | `update` (raw contract) | `UPDATE_SENT` | `on_update` |
| `POST /rate` | `rate` | `RATE_SENT` | `on_rate` (logged only) |
| `POST /support` | `support` | `SUPPORT_SENT` | `on_support` (logged only) |

The seller can also send `on_status` **without being asked** (e.g. when the order ships or is delivered); the same webhook handles it.

### Reading data

| Call | Returns | Scoped to the logged-in buyer? |
|---|---|---|
| `GET /orders?page=&limit=` | Paginated order history (`clientsvc.OrderListResponse`) | Yes — no token gives an empty page |
| `GET /orders/{id}` | One order + every contract snapshot (`clientsvc.OrderDetailResponse`) | Yes — not yours or no token gives 404 |
| `GET /messages?limit=&action=` | The buyer's Beckn message log | Yes — no token gives 401 |
| `GET /status/{id}` | Status + latest contract | **No** — any transaction id works |
| `GET /catalog` | Five hard-coded demo products | — |

### Auth

Defined once as `securityDefinitions.BearerAuth` (a Zitadel JWT) and listed on each `/api/v1` client operation. On the BAP it is **optional** (Swagger 2.0 can't mark that, so each description says so): no token → served anonymously; valid token → the buyer is identified (scopes orders and messages); invalid or expired token → `401`. Webhooks, reconciliation and health list no security — they take **no auth**.

### Errors

- Client API errors are always `{"error": "<message>"}` (`clientsvc.ErrorResponse`).
- `400` = bad or missing body; `500` = transaction/snapshot not found, database error, or the adapter call failed; `409` on `/init` = the quote is no longer available.
- Webhooks reply with a Beckn ACK/NACK (`server.BecknAckResponse`): `{"context": {...}, "message": {"ack": {"status": "ACK"}}}`. NACK only when the body can't be parsed. `reconcile` / `on_reconcile` use their own shapes (`server.ReconcileAckResponse`, `server.ReconcileNackResponse`).

### Key data shapes (`definitions`)

| Definition | What it is |
|---|---|
| `clientsvc.ClientStatusResponse` | An order's status and latest contract. `status` lists every order status: `SELECT_SENT`, `QUOTE_RECEIVED`, `INIT_SENT`, `INIT_RECEIVED`, `CONFIRM_SENT`, `CONFIRMED`, `CANCELLED`, `COMPLETE`, and the `*_SENT` statuses of post-order actions |
| `contract` fields (`type: object`) | The Beckn/ION contract JSON from the seller, stored as-is — left open because its shape is defined by the ION schemas, not by this service |
| `clientsvc.SelectItem`, `clientsvc.Billing`, `clientsvc.CancelItem`, `clientsvc.RatingInput` | What the frontend sends for select, init, cancel/return and rate |
| `clientsvc.BecknContext`, `server.BecknContractRequest` | The Beckn envelope the webhooks receive: `context` (ids, URIs, action) + `message` |
| `reconcilesvc.ReconciliationOverviewResponse`, `reconcilesvc.ReconciliationBatch`, `reconcilesvc.DueContract` | The reconciliation page's data |

---

## BPP API in brief (`bpp.yaml`)

Same structure; the seller side:

| Group | Who calls it | Auth |
|---|---|---|
| `POST /api/v1/auth/bootstrap` | `landing-page` after login | Bearer token required; creates the seller profile on first login |
| Dashboard `/api/v1/*` — stats, orders, inventory, catalogs, products, offers, publish, catalog index and latest published file, messages, support tickets, ratings, reconciliation | `bpp-frontend` | Bearer token required, and the user must have a seller profile (else `403 NO_PROVIDER`); every query is limited to that seller's own data |
| `POST /api/v1/catalog/publish` | Scripts / provider integrations | None |
| Beckn webhooks `/api/webhook/*` — select, init, confirm, status, update, cancel, rate, support, reconcile, on_reconcile, track, catalog/on_publish | `onix-bpp` | None (signature verified by the adapter) |

The webhooks are the mirror of the BAP's: the BPP ACKs at once, processes in the background (e.g. `init` creates the DRAFT contract, `confirm` makes it ACTIVE), and sends its `on_*` answer back through `onix-bpp`.

---

## Things to know

The specs record current behaviour, including these points worth discussing:

- **Unprotected:** BPP `POST /api/v1/catalog/publish` has no auth, so anyone can overwrite a catalog. BAP `GET /api/v1/status/{id}` returns any order's status and contract to anyone with its id.
- **Ignored input:** BAP `/rate` accepts `ratingCategory` but doesn't send it.
- **Error codes:** BAP `/cancel-items` and `/return-items` report every failure as `400`, including server errors; a missing transaction gives `500` rather than `404` on the order actions.
- **Silent failures:** if the BPP fails while processing `confirm`, `cancel` or `update`, no `on_*` answer is sent — the failure is only logged.
- **Stubs:** BPP `track` and `catalog/on_publish` return success without doing anything.

## Keeping the specs current

The specs are generated — never edit `bap.yaml` / `bpp.yaml` directly. When you add or change an endpoint:

1. Update the swag annotations above the handler (`@Summary`, `@Param`, `@Success`, `@Failure`, `@Router`, …). New endpoint → copy the block from a similar handler. If the handler writes JSON inline (`gin.H{...}`), describe the shape with a struct in that package's `apidoc.go`.
2. Regenerate both specs (needs only Docker):

   ```sh
   task docs:api
   ```

3. Commit the updated `docs/api/*.yaml` with the code change.

Annotation syntax: [swag declarative comments](https://github.com/swaggo/swag#declarative-comments-format). To lint the output:

```sh
docker run --rm -v "$PWD/docs/api:/spec" redocly/cli:1.25.0 lint /spec/bap.yaml /spec/bpp.yaml
```
