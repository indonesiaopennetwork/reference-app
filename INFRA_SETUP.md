# Infra setup — from scratch

This is the master checklist for standing up beckn-app's infrastructure,
local and cloud, from a completely fresh clone. It's split into two
independent tracks:

- **[Local development](#1-local-development-docker-compose)** — everything
  runs in Docker Compose on your machine. Start here; it's the fastest way to
  get a working end-to-end system and to generate/verify the signing keys
  you'll reuse in the cloud track.
- **[Cloud deployment](#2-cloud-deployment-cloud-run)** — deploys the same
  six services to Google Cloud Run. Full detail lives in
  [`terraform/README.md`](terraform/README.md); this section is the
  condensed version plus how it fits together with the local track.

## The six services

| Service | What it is | Local port | Local dev source |
|---|---|---|---|
| `bap` | Buyer-side app (Go) | 8083 | `bap-application/` |
| `bpp` | Seller-side app (Go) | 8080 | `bpp-application/` |
| `bap-frontend` | Buyer web UI | 3000 | `bap-frontend/` |
| `bpp-frontend` | Seller/admin web UI | 3001 | `bpp-frontend/` |
| `onix-bap` | Beckn network adapter for bap | 8081 | `onix-bap/` |
| `onix-bpp` | Beckn network adapter for bpp | 8082 | `onix-bpp/` |

Plus Postgres and two Redis instances, all defined in `docker-compose.yml`
for local dev (in the cloud, these are a dedicated Cloud SQL instance and a
dedicated Memorystore instance created in the same project — see the cloud
section).

Two more services exist **local-dev only** (not yet part of the Cloud Run
deployment) for seller login:

| Service | What it is | Local port | Local dev source |
|---|---|---|---|
| `landing-page` | Entry point + seller login/registration (React SPA) | 3002 | `landing-page/` |
| `zitadel` | Identity provider issuing seller login sessions | 8090 | n/a (upstream image) |

`bpp-frontend`'s dashboard now requires a logged-in seller session (see §1f
below) — every `/api/v1/*` route except catalog/webhook ones is gated by it
and scoped to that seller's own data.

## 1. Local development (Docker Compose)

### 1a. Prerequisites

- Docker + Docker Compose.
- `envsubst` (from the `gettext`/`gettext-base` package — `apt install
  gettext-base` on Debian/Ubuntu, `brew install gettext` on macOS). Used by
  `scripts/render-onix-config.sh` in step 1b below to generate the onix
  config files from `.env`.
- `curl` and `jq`. Used by `scripts/setup-zitadel.sh` (§1f) to drive
  Zitadel's Management API — no manual console setup needed for seller
  login.
- A registered Beckn network identity for both `bap` and `bpp` — a
  subscriber ID, a `keyId`, and an Ed25519 keypair for signing (`signingPrivateKey`/
  `signingPublicKey`) plus an encryption keypair (`encrPrivateKey`/`encrPublicKey`),
  each registered on the network registry you're testing against
  (`fabric.nfh.global` in this codebase's current setup). **You cannot
  generate these yourself and expect them to work** — they have to actually
  be registered so other participants can verify your signatures. Ask
  whoever administers the network registry for a subscriber slot.

### 1b. Config files you must create (git-ignored, never committed)

1. **`.env`** (repo root) — copy `.env.example`, fill in your registered
   keys. This file is the single source for every `keyManager` field the
   onix config files need — step 2 below generates them from it, nothing
   is hand-copied:
   ```
   NETWORK_ID=<the Beckn network both bap and bpp are registered on>

   BAP_PRIVATE_KEY=<your bap signingPrivateKey>
   BAP_KEY_ID=<bap subscriber id>|<bap keyId>|ed25519
   BAP_ID=<bap subscriber id, same value as the subscriber-id segment of BAP_KEY_ID>
   BAP_ONIX_KEY_ID=<bap keyId, same value as the keyId segment of BAP_KEY_ID>
   BAP_ONIX_SIGNING_PRIVATE_KEY=<your bap signingPrivateKey, same value as BAP_PRIVATE_KEY>
   BAP_ONIX_SIGNING_PUBLIC_KEY=<your bap signingPublicKey>
   BAP_ONIX_ENCR_PRIVATE_KEY=<your bap encrPrivateKey>
   BAP_ONIX_ENCR_PUBLIC_KEY=<your bap encrPublicKey>

   BPP_PRIVATE_KEY=<your bpp signingPrivateKey>
   BPP_KEY_ID=<bpp subscriber id>|<bpp keyId>|ed25519
   BPP_ID=<bpp subscriber id, same value as the subscriber-id segment of BPP_KEY_ID>
   BPP_ONIX_KEY_ID=<bpp keyId, same value as the keyId segment of BPP_KEY_ID>
   BPP_ONIX_SIGNING_PRIVATE_KEY=<your bpp signingPrivateKey, same value as BPP_PRIVATE_KEY>
   BPP_ONIX_SIGNING_PUBLIC_KEY=<your bpp signingPublicKey>
   BPP_ONIX_ENCR_PRIVATE_KEY=<your bpp encrPrivateKey>
   BPP_ONIX_ENCR_PUBLIC_KEY=<your bpp encrPublicKey>

   # Seller login (Zitadel) — see §1f below for the full setup/test flow.
   ZITADEL_MASTERKEY=<any 32-character string, e.g. `openssl rand -base64 24 | cut -c1-32`>
   ZITADEL_ADMIN_USERNAME=admin
   ZITADEL_ADMIN_EMAIL=admin@reference-app.local
   ZITADEL_ADMIN_PASSWORD=<a password meeting Zitadel's default complexity policy>
   ZITADEL_CLIENT_ID=SET_BY_SCRIPTS_SETUP_ZITADEL_SH
   ```
   The last line is not something you fill in — leave it as-is;
   `scripts/setup-zitadel.sh` (§1f) overwrites it with a real value.
   Only `BAP_PRIVATE_KEY`/`BAP_KEY_ID` (and their bpp equivalents) are read
   directly by the Go apps — the `*_ONIX_*` fields exist here purely so
   step 2's script has one place to read every value from.
   **Important:** `BAP_PRIVATE_KEY`/`BAP_KEY_ID` must be the *same keypair
   and subscriber identity* as `BAP_ONIX_SIGNING_PRIVATE_KEY`/
   `BAP_ONIX_KEY_ID` — there's only one registered identity per side, used
   for both the onix adapter's own signing and the app's direct CDS calls;
   they're separate `.env` fields only because the Go app and the onix
   yaml consume the identity in different shapes. Same rule for bpp. (See
   `terraform/README.md`'s Gotcha #1 if you want the full "why.")

2. **Generate the onix config files** — run:
   ```sh
   ./scripts/render-onix-config.sh
   ```
   This renders `config/local-simple-bap.yaml`, `config/local-simple-bpp.yaml`,
   and `config/catalog-publish.yaml` from their `.example` templates,
   substituting every `keyManager` field (`networkParticipant`/`subscriberId`
   — derived from `BAP_KEY_ID`/`BPP_KEY_ID` — plus `keyId`,
   `signingPrivateKey`, `signingPublicKey`, `encrPrivateKey`,
   `encrPublicKey` from their dedicated `*_ONIX_*` vars) from the `.env`
   you just filled in — including both the receiver and caller modules in
   the bap/bpp files, and
   `catalog-publish.yaml`'s single module, which reuses the **same** bpp
   identity (it's the same operator/key as `onix-bpp`, just named
   `subscriberId` instead of `networkParticipant` — if the container still
   fails config validation, check `docker logs onix-catalog-publish`; the
   example file's own comment notes this name may need to change depending
   on the deployed adapter build). `config/catalog-publish.yaml` is
   required by `docker-compose.yml`'s `onix-catalog-publish` service —
   without it that container fails to start.

   The script fails loudly (before you ever get to `docker compose up`) if
   `.env` is missing or still has unfilled `REPLACE_WITH_*` placeholders.
   Re-run it any time `.env`'s keys change — that's what keeps the
   app-level signing key and the onix `keyManager` identity guaranteed
   identical (Gotcha #1, above) instead of relying on careful hand-copying.

3. **`docker-compose.yml`**'s `NETWORK_ID`/`BAP_ID`/`BPP_ID` env vars —
   these are read straight from the `.env` fields above (Docker Compose
   substitutes `${VAR}` in `docker-compose.yml` itself directly from
   `.env`, no script needed). Nothing to hand-edit in `docker-compose.yml`
   — just make sure `.env`'s `BAP_ID`/`BPP_ID` equal the subscriber ID in
   `BAP_KEY_ID`/`BPP_KEY_ID` (the part before the first `|`).

### 1c. Bring it up

Do these in order — `landing-page` and `bpp` both read
`ZITADEL_CLIENT_ID`/`ZITADEL_ISSUER` from `.env` at container *start*, so
Zitadel has to be up and set up (step 2) *before* step 3 builds them:

1. Start Postgres and Zitadel on their own first:
   ```sh
   docker compose up -d postgres zitadel
   ```
   Wait for Zitadel to report healthy: `docker compose ps zitadel`.

2. Run the one-time Zitadel setup script:
   ```sh
   ./scripts/setup-zitadel.sh
   ```
   This waits for Zitadel to finish its first-boot bootstrap, then uses its
   own Management API (via an auto-created machine-user token — no manual
   console step, no credentials to copy by hand) to create a Project and a
   public/PKCE OIDC Application for `landing-page`, writing the result into
   `.env` as `ZITADEL_CLIENT_ID`. Safe to re-run — it finds the existing
   Project/Application by name instead of creating duplicates.

3. Bring up everything else:
   ```sh
   docker compose up -d --build
   ```
   This builds and starts the original 6 app services, `landing-page`, and
   the 2 Redis instances (`postgres`/`zitadel` are already up from step 1).

4. First run also needs the database schema created — migrations aren't
   run automatically. Requires the `tern` CLI
   (`go install github.com/jackc/tern/v2@latest`); each `tern.conf` already
   defaults to `docker-compose.yml`'s exposed Postgres port (`5435`), so no
   extra config is needed:
   ```sh
   cd bap-application/db/migrations && tern migrate && cd ../../..
   cd bpp-application/db/migrations && tern migrate && cd ../../..
   ```

**If you ran step 3 before step 2** (e.g. you brought everything up in one
shot): `landing-page`/`bpp` will have started with an empty
`ZITADEL_CLIENT_ID`. Just run step 2 now, then
`docker compose restart landing-page bpp` — no rebuild needed, `.env` is
read at container start, not baked into the image.

### 1d. Verify

```sh
curl http://localhost:8083/health   # bap
curl http://localhost:8080/health   # bpp
curl "http://localhost:3000/api/v1/discover?textSearch=coffee"   # full discover flow through the frontend proxy
```

A `200` with real catalog data back means signing, routing, and the CDS
connection are all working.

```sh
curl http://localhost:8090/debug/healthz          # zitadel — expect "ok"
curl -o /dev/null -w "%{http_code}\n" http://localhost:8080/api/v1/orders   # expect 401 — dashboard API rejects unauthenticated requests
```

The `401` is the point, not a failure — it confirms `bpp`'s seller-auth
middleware is actually wired in. See §1f for the real (logged-in) check.

### 1e. Known local-dev gotcha

If you rebuild/recreate `bap`, `bpp`, `onix-bap`, or `onix-bpp` without also
restarting `frontend`/`bpp-frontend`, the frontends' nginx will keep talking
to the *old* container IP and you'll get `502 Bad Gateway`. Either
`docker compose up -d` the whole stack together, or explicitly
`docker compose restart frontend bpp-frontend` after rebuilding a backend.

### 1f. Testing the seller login flow

`bpp-frontend` requires a logged-in seller session — there's no way to see
the dashboard without going through this once. This assumes §1c is
already fully done (Zitadel is up *and* set up via
`scripts/setup-zitadel.sh`).

1. Open `http://localhost:3002` (`landing-page`) in a browser. With no
   session yet, it redirects straight to Zitadel's hosted login page —
   this is expected (see the note below on why there's no "Register"
   button to click yet).
2. Log in with the admin identity from `.env`:
   `ZITADEL_ADMIN_EMAIL` / `ZITADEL_ADMIN_PASSWORD`. This account's email
   is pre-verified (`ZITADEL_FIRSTINSTANCE_ORG_HUMAN_EMAIL_VERIFIED=true`
   in `docker-compose.yml`), so it logs in directly with no email-OTP
   step.
3. Zitadel redirects back to `landing-page`. Since this is the first
   login for this identity, you'll see a one-time "set up your seller
   profile" form (business name) — this is what actually creates the
   `providers`/`users` rows in `bpp`'s database
   (`POST /api/v1/auth/bootstrap`, `bpp-application/internal/authsvc/`).
4. You'll land on the buyer/seller chooser. Click **Enter as Seller** —
   it carries your session token to `bpp-frontend`
   (`http://localhost:3001`), which should load the full dashboard
   (Overview, Orders, Inventory, Catalogs, …), scoped to the seller
   profile you just created.
5. **Enter as Buyer** stays unauthenticated, as before — only the seller
   side requires login.

**Why there's no self-service "Register" flow yet:** Zitadel's own hosted
registration form requires verifying the new account's email via a
one-time code, and this local setup has no SMTP configured to actually
deliver that email — registering a brand-new (non-admin) identity here
will get stuck at that step. Use the admin identity above as your test
seller for now; adding a local SMTP catcher (e.g. Mailpit) so real
self-registration can be tested end-to-end is a known follow-up, not yet
done.

**If something goes wrong:** `docker logs zitadel` for first-boot/bootstrap
issues; `docker logs beckn-app-bpp-1` for `"Zitadel token verifier ready"`
(confirms `bpp` reached Zitadel successfully at startup) or a `Crit`-level
error if it couldn't. A `403` with `"code":"NO_PROVIDER"` from any
`/api/v1/*` call means the bearer token is valid but step 3 above
(the bootstrap call) never completed — check the browser console on
`landing-page` for the actual error.

### 1g. Observability (OTel)

`onix-bap`/`onix-bpp` each ship telemetry to their own companion collector
sidecar — `otel-collector-bap`/`otel-collector-bpp` in `docker-compose.yml`,
both built from `otel-collector/` (same image/config as the cloud deploy's
sidecar). The collector filters onix's raw telemetry down to only
network-relevant signals (the `onix_http_request_count` metric; spans
carrying `sender.id`) and forwards OTLP/HTTP to the network-level collector
at `https://netcol.iontest.in` — nothing this repo deploys itself.

**How it's wired**: `config/local-simple-{bap,bpp}.yaml`'s top-level
`plugins.otelsetup` block sets `otlpEndpoint` to the sidecar's docker-compose
service name (`otel-collector-bap:4317`/`otel-collector-bpp:4317`, OTLP/gRPC).
`config/audit-fields.yaml` (shared by both) controls PII masking in audit
logs — see its own comments for the masking model. **Known gap**: this onix
version doesn't appear to pick up the `auditFieldsConfig`/`producer`/
`producerType` fields from this config shape — its own debug log
(`Telemetry config mapped: ...`) shows them empty even when set, while
`serviceName`/`otlpEndpoint`/`enableMetrics`/`enableTracing`/`enableLogs`/
`timeInterval` all map correctly. Metrics/traces/logs shipping is confirmed
working (see verification below); audit-field masking and producer
attribution are not — don't rely on them without checking upstream.

**To verify it's actually working:**

```sh
docker compose up -d postgres redis-bap redis-bpp otel-collector-bap otel-collector-bpp onix-bap onix-bpp
docker logs onix-bap --tail 300 | grep -i otel
```
You should see `Loaded plugin: otelsetup`, then `OpenTelemetry metrics
initialized ... (OTLP endpoint=otel-collector-bap:4317)` and the tracing
equivalent — confirms the plugin loaded and the sidecar address resolved
(same check works for `onix-bpp` / `otel-collector-bpp:4317`). Then hit an
onix endpoint directly to generate real telemetry and watch the collector
try to export it:
```sh
curl -X POST http://localhost:8081/bap/receiver/ -H "Content-Type: application/json" -d '{"context":{"action":"on_search"},"message":{}}'
sleep 12   # otelsetup's timeInterval is 5s
docker logs otel-collector-bap --tail 40
```

**What this actually proved, verified 2026-09-02**: the full local pipeline
— onix's otelsetup plugin → `otel-collector-bap` sidecar → a real HTTPS POST
to `netcol.iontest.in` — works end-to-end on our side. But
`netcol.iontest.in` itself currently returns **HTTP 404** for both
`/v1/traces` and `/v1/metrics`:
```
error exporting items, request to https://netcol.iontest.in/v1/traces responded with HTTP Status Code 404
error exporting items, request to https://netcol.iontest.in/v1/metrics responded with HTTP Status Code 404
```
Confirmed on both the bap and bpp sidecars, and independently by probing
`netcol.iontest.in` directly with `curl` outside the collector. **This means
nothing will actually show up on `observe.iontest.in`'s dashboard yet** —
not because of anything wrong in this repo's wiring, but because the
network-level collector hasn't got working OTLP/HTTP ingestion routes at
those paths (or they're not the right paths/protocol — worth confirming with
whoever operates that endpoint, the same "word of mouth" caveat as before).
Re-run the verification above after that's fixed upstream to confirm data
actually flows through.

## 2. Cloud deployment (Cloud Run)

Out of scope for this file — this file only covers local development. See
[README.md](README.md#deploy-to-your-fresh-gcp-project) for the full
fresh-deploy walkthrough (Task commands, step by step), and
[terraform/README.md](terraform/README.md) for the gotchas actually hit
building this, verification, redeploying, and inspecting Cloud SQL.

## 3. Keeping local and cloud in sync

Whenever the registered network identity or signing keys change (e.g. a new
subscriber registration, a key rotation), the same values need updating in
**four** places, or the two environments will drift and start failing with
subscriber-identity-mismatch errors:

| What | Local dev | Cloud |
|---|---|---|
| App-level signing key (`BAP_PRIVATE_KEY`/`BAP_KEY_ID`, `BPP_PRIVATE_KEY`/`BPP_KEY_ID`) | root `.env` | `bap-private-key`/`bap-key-id`/`bpp-private-key`/`bpp-key-id` secrets in Secret Manager |
| Onix keyManager identity (`networkParticipant`, `keyId`, signing/encr keys) | `config/local-simple-{bap,bpp}.yaml` | `bap-onix-*`/`bpp-onix-*` secrets in Secret Manager |
| Subscriber ID used in `context.bapId`/`bppId` | `docker-compose.yml`'s `BAP_ID`/`BPP_ID` | `terraform/terraform.tfvars`'s `bap_id`/`bpp_id` |
| Network ID | `docker-compose.yml`'s `NETWORK_ID` (+ both apps' `cmd/server/.env`) | `terraform/terraform.tfvars`'s `network_id` |

After any cloud secret update, the affected Cloud Run service needs a forced
new revision to actually pick up the change — see `terraform/README.md`'s
Gotcha #9.

Note: by default this deployment's cloud identity is a *separate* registered
participant from local dev's (see §2 above), not the same one — so "keeping
in sync" here mainly matters within the cloud track itself (e.g. after
rotating this deployment's own keys), not necessarily between local and
cloud.

## 4. Resolved: registered-URL / receiver-path mismatch

Used to be a problem: the registered Subscriber URL needs to include the
onix adapter's actual receiver path (`/bpp/receiver/`, `/bap/receiver/`), or
another participant looking it up and appending an action name directly
(`<url>/select`) would 404. Now fixed on both sides that mattered:
- `context.bapUri`/`bppUri` on our own outbound messages already include the
  receiver path (`terraform/cloud-run-wiring.tf`) — confirmed working via a
  real `/select` → `/on_select` transaction.
- The actual registered entry in
  `dedi-onboarding-files/ion-scratch-registry.json` already has the path
  baked in (`"url": "https://.../bpp/receiver/"`), not a bare URL.

See `terraform/README.md`'s Gotcha #7 for the fuller writeup.
