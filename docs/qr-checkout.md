# QR packet checkout

A buyer scans a QR code stuck on a physical spot (a kos, an apartment lobby, a
warung) and orders a ready-made basket from the nearest merchant, with no login
and no browsing. The QR codes and their baskets ("packets") come from the
`qr-service` in the [ion-qr-code](https://github.com/indonesiaopennetwork/ion-qr-code)
repo. Everything after the scan is a normal Beckn order placed by this BAP.

It's part of the BAP, not a separate app. The checkout is a buyer journey: it
reuses the BAP's discover, select, init and confirm, its transactions and its
on_* webhooks. The BPP needs no changes. To a seller, a QR order is an ordinary
order.

## Built on Beckn

Every exchange between network participants goes over Beckn through the ONIX adapter:

| Exchange | Beckn |
|---|---|
| Finding merchants (resolve), and qr-service's coverage check before activating a packet | `discover` → `on_discover` through onix-bap (`QR_DISCOVER_METHOD=POST`, the default) |
| Quote | `select` → `on_select` |
| Checkout | `init` → `on_init` |
| Place order | `confirm` → `on_confirm` (same settlement as the shop's Pay & Confirm) |
| Order page: check status, cancel, rate, get help | `status`, `cancel`, `rate`, `support` and their `on_*` answers |

qr-service has no Beckn identity: it asks the BAP (`POST /api/v1/qr/coverage`), which runs the discover. Calls inside the BAP (browser → BAP, BAP → its qr-service) are not Beckn exchanges. Demo scenarios send no Beckn messages (the BAP plays the merchant). The console's simulator shows each order's Beckn messages (**Beckn messages** on a Live activity row).

## Flow

```
phone camera ──► qr-service GET /p/{id} ──302──► bap-frontend /?packet_id=…&scan_id=…
                 (records the scan)                │
                                                   ▼
bap-application /api/v1/qr ─ resolve ─► qr-service GET /api/v1/packets/{id}  (X-Service-Key)
                            │          discover around the packet's spot (S_DWITHIN radius)
                            │          → merchants stocking every item, nearest first
                            │          (the buyer picks a merchant, then which items and how many)
                            ├ quote ───► Beckn select of the chosen items → wait for on_select (the binding price)
                            ├ checkout ► Beckn init (name, phone, spot + room as address) → on_init
                            └ place ───► Beckn confirm → on_confirm
```

| Step | Endpoint | Notes |
|---|---|---|
| Show packet | `GET /api/v1/qr/packets/:id` | Public view; the page shows it while resolve runs |
| Resolve | `POST /api/v1/qr/resolve` | `FAST_CHECKOUT` (one merchant: straight to choosing items), `SELECT_MERCHANT` (ranked list) or `NO_MERCHANT` (with the reason). Each merchant's lines are priced for one unit. Returns the guest token |
| Quote | `POST /api/v1/qr/quote` | Body: `candidate_id` and `items` (`packet_item_id`, `quantity`): what the buyer chose. Their subtotal must reach the packet's minimum basket (`422 BELOW_MIN_BASKET`). Select + wait for on_select. An item the seller marks `CLOSED` gives `409 QUOTE_UNAVAILABLE` |
| Checkout | `POST /api/v1/qr/checkout` | Init + wait for on_init. A different total gives `409 QUOTE_CHANGED`, which the buyer must accept |
| Place order | `POST /api/v1/qr/orders/:id/place` | Body `expected_amount` (the on_init total the buyer agreed to; else `409 QUOTE_CHANGED`). Sends confirm. No payment is collected yet |
| Track | `GET /api/v1/qr/orders/:transaction_id` | State and the seller's fulfilment status |
| After the order | `POST /api/v1/qr/orders/:id/status`, `/cancel` (`reason_code`), `/rate` (`rating` 1–5, `feedback`), `/support` (`description`) | Beckn `status` / `cancel` / `rate` / `support`. Cancel follows the seller's cancellation policy (`409 CANCEL_NOT_ALLOWED`) |
| Beckn trail | `GET /api/v1/qr/orders/:id/beckn` | The order's logged Beckn messages (login required) |
| Basket product search (console) | `GET /api/v1/qr/products?q=` | Beckn discover across the network (the console sends no location: the radius only matters when a buyer scans; optional `lat`/`lon`/`radius_meters` narrow it); each result carries the item it becomes: barcode (GTIN) when the catalog states one, else the main words of its name (TEXT_QUERY). Login required |
| Coverage for qr-service | `POST /api/v1/qr/coverage` | `X-Service-Key: QR_COVERAGE_KEY`; Beckn discover + the same matching as resolve |
| Console | `/api/v1/qr/admin/packets/*`, `GET /api/v1/qr/packet-orders/:packet_id` | Login required; forwarded to qr-service's admin API |

Every call after resolve needs the `X-QR-Guest-Token` header, so only the phone
that scanned can move that checkout on. A logged-in buyer's orders are also
recorded against them and appear in the shop's order history.

The swag annotations on `internal/qrsvc/handler.go` hold the full contract
(`task docs:api`).

## Pages (bap-frontend)

| Path | What |
|---|---|
| `/?packet_id=…&scan_id=…` or `/qr/{packet_id}` | Buyer checkout, mobile-first, no login. A reload resumes the same checkout (sessionStorage) |
| `/qr-admin` | Packet console: create, edit, validate, pause, retire, delete (qr-service `DELETE /api/v1/packets/:id`; a live packet is paused first); QR PNG/SVG; printable A4 poster; each packet's orders. |
| `/qr-admin/{id}/simulate` | Scan simulator: the real buyer page in a phone frame, opened by direct link or through the QR redirect, with "New scan", live order activity and the QR to scan with a real phone. |
| | The console A view of the shop itself: the **QR Packets** button in the shop header switches to it without a reload, under the same header, login, cart and orders |

## Data (bap DB, migrations 017–019)

- `qr_candidates`: each merchant option resolve found, with the select items ready to send and a copy of the packet's spot.
- `qr_orders`: one per checkout, keyed by the Beckn `transaction_id`. States: `QUOTED → INIT_SENT → AWAITING_CONFIRM → CONFIRM_SENT → CONFIRMED`, or `FAILED`. `lines` holds what the buyer chose.

## Matching rules

- `TEXT_QUERY`: every word appears in the product's name or descriptions. Discover also matches provider names, but a packet item has to name the product itself.
- `GTIN`: the resource id or descriptor code is the barcode, or any string value in `resourceAttributes` equals it.
- `ION_SKU`: the resource id, descriptor code or offer id.
- Each merchant uses its cheapest priced offer per item. Merchants must stock every item of the packet and sit within the packet's radius. The minimum basket applies to what the buyer chooses, at quote.
- Ranking: merchants with a location come first, then nearest, then cheapest.
- Prices are in IDR; offers in another currency are ignored.

## Configuration (bap-application)

| Env | Default (compose) | |
|---|---|---|
| `QR_SERVICE_URL` | `http://host.docker.internal:8088` | Empty turns the QR routes off (503 `QR_NOT_CONFIGURED`) |
| `QR_SERVICE_KEY` | `dev-service-key-bap-application-0001` | One of qr-service's `SERVICE_API_KEYS` |
| `QR_ADMIN_TOKEN` | `dev-admin-token` | Empty forwards the user's own login, which then needs qr-service's `ion_admin` role |
| `QR_ALLOW_UNLOCATED_MERCHANTS` | `true` | Lets merchants with no `availableAt` location serve, ranked last |
| `QR_DISCOVER_METHOD` | `POST` | `POST`: Beckn discover through onix-bap. `GET`: the direct, adapter-less shortcut (local testing only) |
| `QR_COVERAGE_KEY` | `dev-service-key-qr-service-0001` (compose) | qr-service's key for `/api/v1/qr/coverage` (its `COVERAGE_BAP_KEY`); empty turns the endpoint off |
| `QR_DEMO_ENABLED` | `true` (compose); off by default | Scan-simulator demo scenarios (`SINGLE` fast checkout, `MULTI` merchant picker, `QUOTE_CHANGED` total changes at checkout): the BAP plays the merchants itself, with no Beckn calls. Order states and live activity stay real; demo orders are tagged DEMO and never reach a seller. Keep it off in production |

## Run it locally

1. Start qr-service from the ion-qr-code repo: `task up` (port 8088). Its `TENANT_WEB_BASE_URL` default `http://localhost:3000` is this frontend, so a scan lands here.
2. Start this stack as usual. The compose defaults already point bap at qr-service.
3. Open <http://localhost:3000/qr-admin>, log in, create a packet whose items match products in discovery, then **Validate against discovery**.
4. Use **Open buyer page**, or scan the printed QR (for a phone, set qr-service's `PUBLIC_BASE_URL` / `TENANT_WEB_BASE_URL` to your PC's LAN IP).

## Not done yet

- **Payment.** The QR flow collects no payment: **Place order** sends confirm straight after on_init. A payment step (e.g. QRIS through a PJP gateway) belongs between checkout and place order, and is being built separately.
- **Seller locations.** bpp-frontend's catalog publish sends no `availableAt` geo, so distances show as unknown and the radius can't exclude those merchants (see `QR_ALLOW_UNLOCATED_MERCHANTS`).
- **Self pickup** goes to the seller as a delivery whose address reads "Self pickup at …". The select message only offers the `DELIVERY` performance mode.
- **Timing.** Quote and checkout wait for on_select / on_init for up to 60s inside one request, which fits nginx's 90s proxy timeout only when the adapters are warm.
- `DISCOVER_METHOD=POST` uses the async discover through onix-bap, once per packet item.
