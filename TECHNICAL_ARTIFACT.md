# Technical artifact

Stack, architecture, and request flow for the ION Reference App. For "what
this app is and who it's for," see [README.md](README.md). For local dev
setup, see [INFRA_SETUP.md](INFRA_SETUP.md). For cloud deployment, see
[terraform/README.md](terraform/README.md).

## Services and stack

| Service | Role | Stack | Source |
|---|---|---|---|
| `bap` | Buyer-side backend | Go, [Gin](https://github.com/gin-gonic/gin), [sqlc](https://sqlc.dev/)-generated Postgres access, [tern](https://github.com/jackc/tern) migrations | `bap-application/` |
| `bpp` | Seller-side backend | Go, Gin, sqlc, tern | `bpp-application/` |
| `bap-frontend` | Buyer web UI | React (Vite), served via nginx | `bap-frontend/` |
| `bpp-frontend` | Seller/admin dashboard | React (Vite), served via nginx | `bpp-frontend/` |
| `landing-page` | Entry point + seller registration/login | React (Vite) | `landing-page/` |
| `onix-bap` | Beckn network adapter for `bap` | [beckn-onix](https://github.com/beckn/beckn-onix) adapter (`fidedocker/onix-adapter` image) + this repo's own config/entrypoint | `onix-bap/` |
| `onix-bpp` | Beckn network adapter for `bpp` | Same onix adapter, bpp-side config | `onix-bpp/` |
| `onix-catalog-publish` | Publishes `bpp`'s catalog to the network's discovery layer | Same onix adapter, catalog-publish config | `onix-catalog-publish/` |
| `zitadel` | Identity provider (OIDC) for network-participant login | [Zitadel](https://zitadel.com/) | Upstream image, self-hosted |

Plus PostgreSQL (one database per app) and Redis (one instance per onix
adapter, used for routing/caching). Locally these run via Docker Compose;
in the cloud, a dedicated Cloud SQL instance and a dedicated Memorystore
instance in the same GCP project — see `terraform/README.md`.

## Two distinct auth layers — don't conflate them

1. **Network-participant login (Zitadel/OIDC)** — gates the human using
   `bpp-frontend`'s dashboard (seller) and, optionally, `bap-frontend`
   (buyer). Standard OIDC/PKCE flow via `landing-page`; `bap`/`bpp` verify
   the bearer token against Zitadel (`internal/auth` in both Go modules).
2. **Beckn message signing (Ed25519 HTTP signatures)** — authenticates
   *messages on the network itself*, independent of any human session. Every
   outbound Beckn action is signed with the participant's registered
   keypair (`becknAuthHeader` in `bap-application`; the onix adapter's own
   `keyManager` plugin for everything routed through `onix-bap`/`onix-bpp`).
   This is what other network participants and the registry actually check
   — it has nothing to do with who's logged into the UI.

## Request flow: buyer search (discover)

```
bap-frontend (React)
   │  GET /api/v1/discover?textSearch=...
   ▼
bap (Go) — internal/clientsvc.Discover()
   │  builds a signed Beckn v2 "discover" request (Ed25519 HTTP signature)
   │  GET, with the search intent as the request body
   ▼
external CDS / network registry discover endpoint
   │  returns matching catalog/offer data directly in the response
   ▼
bap-frontend renders the results
```

`discover` intentionally calls the network's discover endpoint directly
(`CDSDiscoverURL`), not through `onix-bap` — the only synchronous, non-onix-routed
call in the whole flow.

## Request flow: order lifecycle (select → init → confirm)

```
bap-frontend                bap (Go)              onix-bap            onix-bpp            bpp (Go)
     │  POST /select            │                      │                   │                   │
     ├─────────────────────────►│                      │                   │                   │
     │                          │  POST (signed)        │                   │                   │
     │                          ├─────────────────────►│                   │                   │
     │                          │                      │  routes + forwards on the network      │
     │                          │                      ├──────────────────►│                   │
     │                          │                      │                   │  POST /select      │
     │                          │                      │                   ├──────────────────►│
     │                          │                      │                   │                   │ processes,
     │                          │                      │                   │                   │ persists to
     │                          │                      │                   │                   │ Postgres
     │                          │  POST /on_select (async callback)                             │
     │                          │◄─────────────────────────────────────────────────────────────┤
     │                          │  internal/on_select_svc persists the result                    │
```

Every action (`select`/`init`/`confirm`/`status`/`cancel`/`update`/`rate`/
`support`) follows this same async request/callback shape:
`bap` POSTs through `onix-bap` → the network → `onix-bpp` → `bpp`, and the
result comes back later as a separate `POST /on_<action>` to `bap`'s own
webhook route (`internal/on_<action>_svc/{handler,service,types}.go`),
matched back to the original request by `transactionId`/`messageId`. The
frontend polls/reads the persisted transaction state rather than blocking on
a single synchronous HTTP round-trip.

`onix-bap`/`onix-bpp` are what actually sign, validate, and route these
messages on the network — `bap`/`bpp` themselves only build the Beckn
message payload and talk to their own local onix adapter.

## Seller catalog publish flow

```
bpp-frontend (dashboard)
   │  publish catalog
   ▼
bpp (Go) — internal/catalog
   │  writes catalog/provider/resource/offer rows to Postgres
   │  signs + sends the publish payload
   ▼
onix-catalog-publish
   │  writes signed catalog files (index + catalog JSON) to a public bucket
   ▼
dedi-static-server (or equivalent DeDi discovery endpoint)
   │  publicly serves .well-known/dedi.index.json → registry entry → catalog files
   ▼
other network participants discover this seller's catalog
```

## Data model (high level)

Each backend owns its own Postgres database (`sqlc`-generated queries,
`tern` migrations under `db/migrations/`). Core tables, by purpose:

- **`transactions`** (`bap`) — one row per order-lifecycle transaction,
  tracking status (`SELECT_SENT`, `INIT_SENT`, ...), the counterpart
  BPP identity, and (if a buyer is logged in) the owning buyer's subject —
  used to scope order history to the logged-in buyer.
- **`beckn_message_log`** — every outbound request and inbound callback,
  request/response payloads, ACK/NACK status, latency — the audit trail
  for every Beckn action either side sends or receives.
- **`providers` / `catalogs` / `resources` / `offers`** (`bpp`) — a
  seller's published inventory: one provider per seller identity, catalogs
  grouping resources/offers, scoped to the logged-in seller's own
  `provider_id`.
- **`users`** (`bpp`) — maps a Zitadel subject to a seller's `provider_id`,
  created on first login via the bootstrap flow (`internal/authsvc`).

## Deployment model

Every service is a Docker container. Locally, `docker-compose.yml` runs all
of them on one machine with container-network hostnames. In the cloud,
Terraform (`terraform/`) deploys the same containers to Cloud Run
(scale-to-zero), backed by a dedicated Cloud SQL Postgres instance and a
dedicated Memorystore Redis instance in one GCP project — see
[terraform/README.md](terraform/README.md) for the full walkthrough,
architecture notes, and gotchas.
