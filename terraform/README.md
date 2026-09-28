# GCP deployment (Cloud Run, scale-to-zero)

Deploys beckn-app's six services (bap, bpp, bap-frontend, bpp-frontend,
onix-bap, onix-bpp) to Cloud Run in a single GCP project (`ion-sandbox-001`
by default), along with a dedicated Cloud SQL Postgres instance and a
dedicated Memorystore Redis instance created inside that same project.
Everything lives in one project — no Shared VPC, no cross-project reuse.

The onix-bap/onix-bpp Cloud Run services are named `beckn-onix-bap`/
`beckn-onix-bpp` (not the bare `onix-bap`/`onix-bpp`) — a prefix added
specifically to avoid colliding with unrelated pre-existing services that
happened to already exist under those exact names in one project this was
deployed to. If you're deploying into a genuinely fresh project you could
safely rename them back, but there's no need to — the prefix is harmless.

This directory replaces `docker-compose.yml`'s Docker-network hostnames
(`http://bap:8083`, `redis-bap:6379`, ...) with Cloud Run's per-service HTTPS
URLs and the new instances' addresses.

For local development instead of the cloud, see [INFRA_SETUP.md](../INFRA_SETUP.md).
For the stack/architecture/request-flow overview, see [TECHNICAL_ARTIFACT.md](../TECHNICAL_ARTIFACT.md).

## Architecture notes

- **Single project, owned resources**: `network.tf` creates a VPC + subnet,
  `sql.tf` creates a new Cloud SQL Postgres instance, `redis.tf` creates a
  new Memorystore Redis instance — all in `var.project_id`. Cloud Run reaches
  both via Direct VPC Egress into the owned subnet.
- **Private Services Access for Cloud SQL**: Cloud SQL's private IP requires
  a reserved peering range (`google_compute_global_address` with
  `purpose = "VPC_PEERING"`) and a `google_service_networking_connection`
  peering the VPC with `servicenetworking.googleapis.com`, created before the
  instance itself. See `network.tf`.
- **Redis via Direct Peering**: Memorystore uses its own peering mechanism
  (`connect_mode = "DIRECT_PEERING"`, a separate reserved CIDR from the PSA
  range) — simpler than Cloud SQL's setup, no `google_service_networking_connection`
  needed for it. See `redis.tf`.
- **Dedicated Redis, no cross-tenant risk**: unlike a shared/reused instance,
  this Redis is created solely for this deployment — no risk of another
  tenant's cached routing decision colliding with this app's.
- **Custom onix images**: `onix-bap/` and `onix-bpp/` wrap the vendor's
  `fidedocker/onix-adapter` image with an `entrypoint.sh` that runs
  `envsubst` over `.yaml.tmpl` templates (the real `keyManager`/`router`/
  `schemaValidator` config) at container startup, substituting only the
  small set of env vars Cloud Run actually varies at runtime (Redis address,
  cross-service URLs, signing keys). Everything else is baked into the image
  at build time.
- **OTel sidecar**: onix-bap/onix-bpp each carry a second container,
  `otel-collector` (`otel-collector/`, image pinned at
  `otel/opentelemetry-collector-contrib:0.159.0`), reachable at
  `localhost:4317` since Cloud Run v2 sidecars share a network namespace —
  same pattern as the `cloudsql-proxy` sidecar below. Each onix config's
  top-level `plugins.otelsetup` block (added to `local-simple-{bap,bpp}.yaml.tmpl`)
  ships metrics/traces/logs to it over OTLP/gRPC; `otel-collector/config.yaml`
  filters that down to only network-relevant signals (`onix_http_request_count`,
  and spans carrying `sender.id`) and re-exports OTLP/HTTP to
  `https://netcol.iontest.in`, the network-level collector operated outside
  this deployment — not something this module deploys itself.
  `auditFieldsConfig` (`onix-bap/templates/audit-fields.yaml` and the bpp
  equivalent, byte-identical) controls PII masking in audit logs; both are
  static, non-templated files copied in by `entrypoint.sh`'s generic
  (non-`.tmpl`) copy loop.
- **Cloud SQL Auth Proxy sidecar**: bap/bpp/the migrate jobs reach Postgres
  via a `cloudsql-proxy` sidecar container on `127.0.0.1:5432`, not the
  native `/cloudsql` socket — zero app-code changes needed (`DB_HOST` is
  just `127.0.0.1`).
- **Circular URL dependencies**: bap↔onix-bap and bpp↔onix-bpp each
  reference the other's Cloud Run URL. Terraform can't express a cycle, so
  those specific env vars are set to a bootstrap placeholder in the initial
  resource declarations and patched to the real value by
  `cloud-run-wiring.tf` (a `null_resource` + `gcloud run services update`)
  once every service's URL is known. This step re-runs on every
  `terraform apply` (deliberately — see Gotchas).
- **Public access**: every service (including `zitadel` and
  `onix-catalog-publish`) grants `roles/run.invoker` to `allUsers` — no auth.
  Documented tradeoff, not an oversight; see `iam-invoker.tf`.
- **Network identity**: `BAP_ID`/`BPP_ID` (context.bapId/bppId in every
  outbound message) and the onix keyManager's `NETWORK_PARTICIPANT` share
  the *same* Terraform variable (`var.bap_id`/`var.bpp_id` — see
  `variables.tf`). These two uses must always be the same value, and
  `BAP_PRIVATE_KEY`/`BAP_KEY_ID` (the app's own key for signing direct CDS
  calls) must also be the *same keypair* as the onix keyManager's signing
  key — there is only one registered identity per side, not two. See the
  first Gotcha below for what happens if this drifts. This deployment uses
  its own separate registered identity (not the same one as local dev or any
  other deployment) — `bap_id`/`bpp_id`/etc. have no default in
  `variables.tf` on purpose, so `terraform plan` fails loudly until
  `terraform.tfvars` supplies real values.

## Prerequisites

- A GCP project with a **billing account linked**. A brand-new project has
  none by default, and `google_project_service.apis` (the first bootstrap
  step) fails immediately without one.
- `gcloud` authenticated with Owner (or equivalent) on that project, plus
  Application Default Credentials configured (separate credential store from
  `gcloud auth login`):
  ```sh
  gcloud auth application-default login
  gcloud auth application-default set-quota-project <YOUR_PROJECT_ID>
  ```
  The wiring step also shells out to `gcloud run services update` during
  `terraform apply` — make sure `gcloud` is authenticated against the target
  project on the machine running `terraform apply`.
- `terraform >= 1.5` and `docker` installed locally.
- **A registered Beckn network identity for both `bap` and `bpp`** — a
  subscriber ID, a `keyId`, an Ed25519 signing keypair, and an encryption
  keypair for each side, registered against the Beckn network registry you're
  deploying against. **You cannot generate these yourself** — they must
  actually be registered so other participants can verify your signatures.
  Ask whoever administers that network registry for a subscriber slot. This
  is why five Terraform variables below have no default: `terraform plan`
  will refuse to run until you supply real values.

## Deploy to your fresh GCP project

All commands below run from the repo root unless noted, and `<YOUR_PROJECT_ID>`
/ `<YOUR_REGION>` are placeholders — pick real values and use them consistently.

### Minimum-steps deployment

True one-click isn't achievable — three things are externally gated and
nothing here can automate them: a registered Beckn network identity, DNS
records at your registrar, and Google Search Console domain verification.
But everything *between* those inputs and a finished deployment can be
chained, with exactly one unavoidable pause in the middle (Zitadel's domain
has to actually be live — DNS propagated + managed SSL cert issued, up to
~60 min — before `bap`/`bpp` will start; `bpp` treats an unreachable
Zitadel as fatal). The minimum-steps path:

1. `task deploy:state-bucket PROJECT_ID=<YOUR_PROJECT_ID> REGION=<YOUR_REGION>`
   (one-time — see step 0/1 below first, editing `backend.tf`).
2. Fill in `terraform/terraform.tfvars` (all variables, including all seven
   domain variables — see step 2 below), `scripts/seed-secrets.env` (see
   step 4 below), and `dedi-onboarding-files/.env.local` (see "Custom
   domain for DeDi discovery files" below for the signing keys), then run
   `task sign-dedi-files`. Pure input-gathering and local signing, nothing
   cloud yet.
3. `task deploy:phase1 PROJECT_ID=<YOUR_PROJECT_ID> REGION=<YOUR_REGION>` —
   runs unattended through bootstrap, staging the already-signed DeDi files
   in their bucket, secrets, images, infra, and bringing up Zitadel, then
   prints the one DNS record to create and stops.
4. Create and verify that DNS record yourself at your registrar.
5. `task deploy:phase2 PROJECT_ID=<YOUR_PROJECT_ID> REGION=<YOUR_REGION>` —
   confirms Zitadel's domain is actually live (polls up to ~60 min, since
   the managed SSL cert can lag even after DNS resolves), then continues on
   its own through Zitadel's OIDC setup, migrations, the final apply, and
   bringing up `dedi-static-server`, then prints the DNS records for the 6
   remaining domains (`bap`, `bap-frontend`, `bpp-frontend`,
   `landing-page`, plus the DeDi pair) — **create these at your registrar
   too**; none of them block the deployment itself, but without them those
   URLs won't resolve. If `phase2` is interrupted during the wait, just
   re-run it — every step both phases chain is idempotent, aside from
   `deploy:state-bucket`, which already ran in step 1.

The rest of this section is the detailed step-by-step `deploy:phase1`/
`deploy:phase2` actually run under the hood — read it if you want manual
control, need to resume from a specific point, or are debugging a failure
in one phase.

### 0. Point Terraform state at your project

`terraform/backend.tf` currently hardcodes a state bucket name for the
project this repo was built against. Edit it before running `terraform init`:

```hcl
# terraform/backend.tf
terraform {
  backend "gcs" {
    bucket = "<YOUR_PROJECT_ID>-tfstate"   # was: ion-sandbox-001-tfstate
    prefix = "beckn-app"
  }
}
```

### 1. Create the state bucket

Chicken-and-egg: Terraform can't create the bucket it stores its own state
in, so this is a one-time manual step.

```sh
task deploy:state-bucket PROJECT_ID=<YOUR_PROJECT_ID> REGION=<YOUR_REGION>
```

### 2. Create `terraform/terraform.tfvars`

Not committed (already git-ignored). Override `project_id` and supply the
identity variables that have no default, the Zitadel admin account,
**and all seven domain variables** — set everything now, not later.
`cds_discover_url` is optional — by default `bap`'s `CDS_DISCOVER_URL`
points at the `discovery-service` Cloud Run service this module deploys (a
same-project, same-operator service, not an external registry endpoint),
computed the same way `bpp`'s `CDS_PUBLISH_URL` already is. This same
variable also drives `onix-bap`'s own `discover` routing rule
(`DISCOVERY_SERVICE_URL` in `cloud-run-onix-bap.tf`, used when
`DISCOVER_METHOD=POST` routes discover through onix-bap instead of
`bap`'s direct `GET`) — the `/discover` suffix is stripped automatically
for that one, since onix-bap's router appends the action name itself.
Only set `cds_discover_url` if you want both of these to use a different,
externally-hosted discovery service instead.

`discovery_service_subscriber_id`/`discovery_service_subscriber_uri` are
also optional — left unset, discovery-service signs its `on_discover`
callbacks as bap's own onix-bap identity (a borrowed-identity pattern
that works because that identity is already registered; see the comment
on these variables in `variables.tf`). The actual signing key material
lives in the `discovery-service-subscriber-key-id`/
`discovery-service-signing-private-key` secrets (`secrets.tf`) — reseed
those (via `scripts/seed-secrets.env.example`) once discovery-service has
its own real, independently-registered identity; these two variables
only need to change alongside that reseed, not before:

```hcl
# terraform/terraform.tfvars
project_id             = "<YOUR_PROJECT_ID>"
region                 = "<YOUR_REGION>"
bap_id                 = "<your registered onix-bap identity>"
bpp_id                 = "<your registered onix-bpp identity>"
network_id             = "<your registry's network id>"
zitadel_admin_username = "admin"
zitadel_admin_email    = "<your email>"

# Domain mapping is mandatory, not an add-on for later — see "Custom
# domains" below for what each one is for and the Search Console
# verification each domain needs before its mapping resource will succeed.
bpp_domain             = "<your bpp/dedi-discovery domain>"
bpp_domain_www         = "<the www variant of the domain above>"
bap_domain             = "<your bap backend domain>"
bap_frontend_domain    = "<your buyer-app domain>"
bpp_frontend_domain    = "<your seller-app domain>"
landing_page_domain    = "<your landing-page domain>"
zitadel_domain         = "<your zitadel domain>"
```

**Why all seven, right now, even though the "Custom domains" sections
appear much later in this doc:** none of the seven has a default in
`variables.tf`, and Terraform resolves every declared root-module variable
before it prunes the resource graph by `-target` — so even step 3 below
(`deploy:bootstrap-apis`, a `-target`'d plan that doesn't touch any domain
resource) fails immediately with "no value for required variable" if any of
these seven are still unset. There's no step in this sequence where you can
defer them — they have to be real values you own from the very first
`terraform plan`.

There's no `cds_publish_url` to supply — `bpp`'s `CDS_PUBLISH_URL` points at
the `onix-catalog-publish` Cloud Run service this module deploys (a
same-project, same-operator service, not an external registry endpoint), and
Terraform computes that URL itself.

### 3. Bootstrap APIs, Artifact Registry, and empty secret containers

```sh
task deploy:bootstrap-apis
```

### 4. Seed secrets

14 secrets are seeded outside Terraform (`postgres-password` is the only one
Terraform generates itself). Use values for the identity you registered in
the prerequisites — `BAP_PRIVATE_KEY`/`BAP_KEY_ID` **must** be the same
keypair/identity as `BAP_ONIX_SIGNING_PRIVATE_KEY`/`BAP_ONIX_KEY_ID` (same
rule for bpp) — there's exactly one registered identity per side.

Copy the example file and fill in your real values:
```sh
cp scripts/seed-secrets.env.example scripts/seed-secrets.env
```
Open `scripts/seed-secrets.env` and set all 14 values. This file is
gitignored — never commit it with real values filled in. Then run:
```sh
task deploy:seed-secrets PROJECT_ID=<YOUR_PROJECT_ID>
```

### 5. Build and push all 12 images

```sh
task deploy:build-all PROJECT_ID=<YOUR_PROJECT_ID> REGION=<YOUR_REGION>
```

### 6. Create the network, Cloud SQL, and Redis

Cloud SQL private-IP creation is slow (budget 5–10 minutes), which is why
it's sequenced as its own targeted apply:

```sh
task deploy:infra
```

### 6b. Bootstrap Zitadel

`bap`/`bpp` both require a reachable Zitadel to start (see
[cloud-run-zitadel.tf](cloud-run-zitadel.tf) — `bpp`
treats an unreachable Zitadel as fatal, `bap` degrades gracefully but buyer
login won't work), so this has to happen before step 7. `zitadel_domain`
needs the same Search Console verification as the other custom domains —
see "Custom domains" below.

**1. Apply Zitadel — all three targets together.** The IAM invoker binding
is easy to miss (it's a separate resource, not a dependency of the service
or the domain mapping) but Zitadel is unreachable without it:
```sh
cd terraform
terraform apply \
  -target=google_cloud_run_v2_service.zitadel \
  -target=google_cloud_run_domain_mapping.zitadel \
  -target='google_cloud_run_v2_service_iam_member.public_invoker["zitadel"]'
cd ..
```

**2. Wait for DNS + the managed TLS cert to be ready** (can take up to ~60
min, same as the DeDi custom domain below). Poll until this prints `OK`:
```sh
curl -sf https://<your zitadel_domain>/.well-known/openid-configuration && echo OK
```

**3. Run the setup script**, passing `ZITADEL_ISSUER`/`LANDING_PAGE_URL`
explicitly rather than letting it fall back to `terraform output` — targeted
applies like step 1 above don't reliably populate every output (`terraform
output zitadel_domain_url` can come back "not found" even after a
successful apply), so passing these directly avoids a confusing detour:
```sh
PROJECT_ID=<YOUR_PROJECT_ID> \
  ZITADEL_ISSUER=https://<your zitadel_domain> \
  LANDING_PAGE_URL=https://<your landing_page_domain> \
  ./scripts/setup-zitadel-cloud.sh
```

**4.** The script prints a `zitadel_client_id = "..."` line — put that in
`terraform/terraform.tfvars` (it defaults to a non-working placeholder, so
`landing-page`'s login link is broken until you do this).

**Troubleshooting:**
- *Setup script times out "waiting for Zitadel to be ready"* — almost
  always the IAM invoker binding from step 1 wasn't actually applied.
  Confirm directly with the `curl` command from step 2; a `403 Forbidden`
  page from `Google Frontend` (not from Zitadel itself) confirms it, then
  re-run step 1.
- *Zitadel's own logs show `password authentication failed for user
  "trade"`* — `google_sql_user.trade`'s password has drifted from what's in
  the `postgres-password` secret (this can happen if `google_sql_user.trade`
  was never applied before `random_password.postgres` got generated in a
  targeted apply). Fix: `terraform apply -target=google_sql_user.trade` to
  resync, then retry step 1.

### 7. First full apply — expect it to fail on `bap`/`bpp`

The databases have no schema yet, and both apps exit if a required startup
query fails. This is normal for a first deploy:

```sh
task deploy:full-apply
```

### 8. Run migrations

```sh
task deploy:migrate
```

### 9. Re-apply

```sh
task deploy:full-apply
```

`bap`/`bpp` should now start successfully against the real schema, and
`landing-page` picks up the real `zitadel_client_id` from step 6b.

**Next:** once `bap`/`bpp` are confirmed up, do the
["Custom domain for DeDi discovery files"](#custom-domain-for-dedi-discovery-files-required-for-real-network-participation)
section below — it's required for actually participating on the network,
not optional, even though it has to come after this step (not before) for a
real dependency reason explained there.

### Verify the deployment

```sh
terraform output bap_frontend_url bpp_frontend_url
```

Open both URLs and confirm the SPA loads. Then seed some demo catalog data
via [catalog-seed/](../catalog-seed/) against the new `bpp_url` and try a
discover flow end-to-end. See "Verification" further below for deeper
checks (logs, migrate job status, Cloud SQL/Redis connectivity).

## Custom domain for DeDi discovery files (required for real network participation)

`onix-catalog-publish` writes signed catalog files (`index/becknCatalogs.index.json`,
`catalogs/*.json.gz`) to `outputRoot` on every "Publish to Network" click.
`gcs-dedi-static.tf` and `cloud-run-dedi-static.tf` back
that `outputRoot` with a public GCS bucket and a small nginx service
(`dedi-static-server`), and `domain-mapping.tf` maps it onto a
custom domain (`var.bpp_domain`/`var.bpp_domain_www`) so the discovery
chain — `.well-known/dedi.index.json` → `dedi/ion-scratch-registry.json` →
`index/becknCatalogs.index.json` → the catalog files themselves — is
publicly reachable. This is genuinely required, not optional, if you intend
to actually transact on the network: other participants (and your own onix
adapters' `dediregistry` plugin) resolve your registered identity through
this self-hosted discovery chain, matching the "registered Beckn network
identity" prerequisite at the top of this doc. The Terraform **resources
are also not conditional** either way — there's no flag to skip creating
them, so:

**Why this section comes *after* step 9, not before it, even though it's a
prerequisite conceptually:** `dedi-static-server`'s own config
(`cloud-run-dedi-static.tf`) references `bap`/`bpp`/`onix-bap`/
`onix-bpp`'s live Cloud Run URLs directly (`google_cloud_run_v2_service.bap.uri`
etc.), so Terraform cannot create it until all four of those already exist
with a real URL — which itself requires the DB schema to exist (step 8) and
`bap`/`bpp` to have successfully started (step 9). This is a hard
dependency in the Terraform graph, not a documentation-ordering choice —
attempting this section before step 9 succeeds will just fail trying to
resolve URLs for services that don't exist yet.

**`bpp_domain`/`bpp_domain_www` have no default** — same treatment as
`bap_id`/`bpp_id`/`network_id` above, and for the same
reason: a hardcoded default pointing at *some* domain is exactly how you'd
end up silently deploying against a domain you don't control. `terraform
plan` fails immediately and loudly ("no value for required variable") until
you set both in `terraform.tfvars` to a domain you actually own and can
verify — before that, there's no `apply`-time surprise to plan around, this
is caught at `plan` time.

**Prerequisite:** the domain must be a **verified domain** in
[Search Console](https://search.google.com/search-console) under the same
Google account your `gcloud`/Terraform credentials use —
`google_cloud_run_domain_mapping` fails until that's done.

**Deploy sequence:**

The bucket (`deploy:dedi-bucket`) and the signing/upload steps have **no
dependency on bap/bpp/onix-bap/onix-bpp** — only `dedi-static-server`
itself (`deploy:dedi-server`) does, since its config references those four
services' live Cloud Run URLs directly. So steps 1-4 below can happen
early (well before step 9 of the main walkthrough — this is exactly what
`deploy:phase1` does), while step 5 (`deploy:dedi-server`) genuinely can't
run until after `deploy:full-apply-and-migrate` has succeeded, the same
hard Terraform-graph dependency as before.

1. Set `bpp_domain`/`bpp_domain_www` in `terraform.tfvars` — required,
   `terraform plan` fails without them.
2. **Before signing anything**, set up your DeDi signing identity — copy
   `dedi-onboarding-files/.env.local.example` to
   `dedi-onboarding-files/.env.local` and fill in `DEDI_PUBLIC_KEY`/
   `DEDI_PRIVATE_KEY` (a registered Ed25519 keypair for this discovery
   chain). This is not optional: `Taskfile.yaml` auto-loads
   `dedi-onboarding-files/.env.local` (`dotenv:` at the top of the file) and
   `dedi-file-signer` needs both values to sign anything — without them,
   `task sign-dedi-files` fails immediately rather than signing with a
   placeholder. Put `dedi.index.json` and `ion-scratch-registry.json` (the
   two files that rarely change and aren't generated by anything in this
   repo) in `dedi-onboarding-files/`, then sign them:
   ```sh
   task sign-dedi-files
   ```
3. Create the bucket:
   ```sh
   task deploy:dedi-bucket
   ```
4. Upload the signed files — **requires step 3's bucket to already exist**,
   `task deploy-dedi-files` reads the bucket name straight from
   `terraform output -raw dedi_static_bucket`, which doesn't exist before
   that:
   ```sh
   task deploy-dedi-files
   ```
5. Once `bap`/`bpp`/`onix-bap`/`onix-bpp` all exist with real URLs (i.e.
   after `deploy:full-apply-and-migrate` has succeeded), build and push the
   `dedi-static-server` image — included in the main walkthrough's step 5
   if you're deploying fresh; if you're adding this to an
   **already-running** deployment, build and push it explicitly first,
   since there's no "build all images" checkpoint to fall back on at that
   point:
   ```sh
   gcloud auth configure-docker <YOUR_REGION>-docker.pkg.dev --quiet
   docker build -t <YOUR_REGION>-docker.pkg.dev/<YOUR_PROJECT_ID>/beckn-app/dedi-static-server:latest dedi-static-server/
   docker push <YOUR_REGION>-docker.pkg.dev/<YOUR_PROJECT_ID>/beckn-app/dedi-static-server:latest
   ```
   Then create `dedi-static-server` and its two domain mappings:
   ```sh
   task deploy:dedi-server
   ```
   If `google_cloud_run_domain_mapping` fails with a region/availability
   error, see the comment at the top of `domain-mapping.tf` — Cloud Run
   domain mapping has historically had self-service limited to a subset of
   regions.
6. Get the DNS records and create them at your registrar (the one step I
   can't automate):
   ```sh
   terraform output bpp_domain_dns_records
   terraform output bpp_domain_www_dns_records
   ```
7. Wait for the managed SSL cert (up to ~60 min after DNS propagates), then
   `curl https://<your domain>/.well-known/dedi.index.json`.

## Routing BPP/BAP receiver calls through the custom domain

**Not optional — this is unconditional in the current Terraform, not a
toggle you can skip.** There's no `count`/`for_each`/feature-flag guarding
any of it: `cloud-run-wiring.tf` always sets
`BAP_URI=https://${var.bap_domain}/bap/receiver/` and
`BPP_URI=https://${var.bpp_domain}/bpp/receiver/` on every apply, and
`dedi-static-server`'s nginx config always proxies `/bpp/receiver/*` →
`onix-bpp` and `/bap/receiver/*` → `onix-bap`. Builds on the section above
— requires the custom domain already mapped to `dedi-static-server` — so a
subscriber's registered URL is `https://<your domain>/bpp/receiver/` (or
`/bap/receiver/`) rather than `onix-bpp`'s/`onix-bap`'s raw `*.run.app` URL,
on every deployment of this repo, not just some of them.

This also sidesteps the path mismatch in
[Gotcha #7 below](#gotchas-actually-hit-doing-this-not-theoretical--each-one-broke-a-real-deploy):
other participants append `/<action>` directly to whatever URL is
registered, and `onix`'s receiver modules default to listening at
`/bpp/receiver/`/`/bap/receiver/` — baking that same suffix into the
*registered* URL itself makes `<registered-url>/<action>` land correctly,
without needing to change either receiver module's own listening path.

**How it's wired:**
- `dedi-static-server/nginx.conf.template` — the two `location` blocks doing
  the proxying, targeting `${ONIX_BPP_HOST}`/`${ONIX_BAP_HOST}`.
- `dedi-static-server/Dockerfile` — `COPY`s that template into
  `/etc/nginx/templates/default.conf.template` rather than directly into
  `/etc/nginx/conf.d/`, so `nginx:alpine`'s built-in entrypoint does the
  `envsubst` substitution from real environment variables at container
  startup. This has to happen at *startup*, not image build time, because
  the target host is only known once `onix-bpp`/`onix-bap` already exist.
- `cloud-run-dedi-static.tf` — sets `ONIX_BPP_HOST`/`ONIX_BAP_HOST`
  on the container from `google_cloud_run_v2_service.onix_bpp.uri` /
  `google_cloud_run_v2_service.onix_bap.uri` (scheme stripped).
- Redeploying after touching any of the above is the same as any other
  Cloud Run image change — see "Redeploying after a code change" below.

**Do not extend this to `/bpp/caller/` or `/bap/caller/`.** Unlike the
receiver modules (`validateSign` is their first processing step), the caller
modules have no inbound signature validation — they trust whoever calls them
and sign+forward outbound messages on the participant's behalf. They're
meant to stay reachable only internally: `BPP_CALLER_URL`/`ADAPTER_URL`
point directly at `onix-bpp`'s/`onix-bap`'s raw Cloud Run URL, set by
`cloud-run-wiring.tf`. Proxying either publicly would let anyone
get that adapter to sign and send forged outbound messages impersonating
this participant.

**Update:** `BAP_URI`/`BPP_URI` (the in-band `context.bapUri`/`context.bppUri`
fields on outbound messages) now *do* point at this same domain+path —
`cloud-run-wiring.tf` sets them to `https://${var.bpp_domain}/bap/receiver/`
/ `.../bpp/receiver/`. This is required, not just cosmetic: other network
participants POST callbacks to `{bapUri}/<action>` directly, and
`bapTxnReceiver`/`bppTxnReceiver` only listen at `/bap/receiver/`/
`/bpp/receiver/` (see `../config/local-simple-bap.yaml`/`local-simple-bpp.yaml`)
— a bare `bapUri` with no path would misroute every inbound callback.

## Custom domains for bap, bap-frontend, bpp-frontend, landing page, and Zitadel (required)

`domain-mapping.tf` also maps `bap` (`var.bap_domain`),
`bap-frontend` (`var.bap_frontend_domain`), `bpp-frontend`
(`var.bpp_frontend_domain`), `landing-page` (`var.landing_page_domain`), and
`zitadel` (`var.zitadel_domain`) to their own subdomains — same
`google_cloud_run_domain_mapping` pattern, Search Console verification
prerequisite, and **no-default treatment** as `bpp_domain` above: all five
are required, `terraform plan` fails without them (unlike the other four,
`zitadel_domain` isn't really "optional" in practice — see step 6b above,
`bpp` won't start without a working Zitadel). Set all five in
`terraform.tfvars` to subdomains you control before applying.

`bpp` intentionally does **not** get its own separate domain — it shares
`dedi-static-server`'s domain instead, via one more `location` block in
`dedi-static-server/nginx.conf.template` (`/api/v1/` → `bpp`'s dashboard/
provider-facing API, using the same envsubst'd-hostname proxy pattern as the
`/bpp/receiver/`/`/bap/receiver/` blocks above). `bpp`'s other route group,
`/api/webhook/*`, is reached internally by `onix-bpp` via `bpp`'s raw Cloud
Run URL (`BPP_URL`, set by `cloud-run-wiring.tf`) and is deliberately not
exposed on any public domain.

`bap_frontend_domain`/`bpp_frontend_domain` do double duty beyond domain
mapping: `cloud-run-bap-frontend.tf` also injects
`bap_frontend_domain` into `bap-frontend`'s own container as `BPP_ADMIN_URL`,
and `cloud-run-bpp-frontend.tf` injects `bpp_frontend_domain` into
`bpp-frontend`'s container as `BAP_URL` — these back the "BPP APP" /
"Go to BAP APP" cross-navigation buttons in each frontend's UI (locally,
the same values come from `VITE_BPP_ADMIN_URL`/`VITE_BAP_URL` in `.env`).
Note that `BAP_URL` as a container env var name is overloaded: on
`bap-frontend` it's a *different* var pointing at the bap backend (nginx's
reverse-proxy target, `var.bap_domain`), while on `bpp-frontend` it points at
bap-frontend's own URL for the button — same name, different meaning
depending on which service reads it.

After applying, get the DNS records for the five new domains the same way as
`bpp_domain`:
```sh
terraform output bap_domain_dns_records
terraform output bap_frontend_domain_dns_records
terraform output bpp_frontend_domain_dns_records
terraform output landing_page_domain_dns_records
terraform output zitadel_domain_dns_records
```

## Redeploying after a code change

### A new service added to the Terraform module

If you add a brand-new `google_cloud_run_v2_service` to an
**already-running** deployment (not a fresh deploy — see step 5 above,
which already covers this case), Cloud Run refuses to create the service
until its image exists in Artifact Registry: `Image '...' not found`. Build
and push that one image before applying:
```sh
docker build -t <YOUR_REGION>-docker.pkg.dev/<YOUR_PROJECT_ID>/beckn-app/<new-service>:latest <new-service-dir>/
docker push <YOUR_REGION>-docker.pkg.dev/<YOUR_PROJECT_ID>/beckn-app/<new-service>:latest
```
then `terraform apply` as usual.

### Code change in any service

Cloud Run pins each service to a specific image digest at deploy time, and
every Cloud Run resource in this module has a `lifecycle { ignore_changes =
[...] }` block on its image — so pushing a new image under the same
`:latest` tag does nothing to an already-running service. **A plain
`terraform apply`, even with a new `image_tag`, does not force a redeploy
either** — `ignore_changes` blocks that field regardless of where the new
value comes from. What actually forces a new revision is
`gcloud run services update --image=...` directly, bypassing Terraform for
that one field — which is exactly what the per-service Task commands below
do, so you never need to touch `image_tag` at all:

```sh
task redeploy:bap-frontend   # only rebuilds/redeploys bap-frontend
task redeploy:bpp            # only rebuilds/redeploys bpp
task redeploy:all            # rebuilds/redeploys every service (not the migrate jobs)
```

Every service has its own `redeploy:<service>` task (`task --list` shows
them all) — see `../Taskfile.yaml` for the full set, or run the equivalent
commands by hand:
```sh
docker build -t <YOUR_REGION>-docker.pkg.dev/<YOUR_PROJECT_ID>/beckn-app/<service>:$(git rev-parse --short HEAD) <service-dir>/
docker push <YOUR_REGION>-docker.pkg.dev/<YOUR_PROJECT_ID>/beckn-app/<service>:$(git rev-parse --short HEAD)
gcloud run services update <service> --project=<YOUR_PROJECT_ID> --region=<YOUR_REGION> \
  --container=<container-name> --image=<YOUR_REGION>-docker.pkg.dev/<YOUR_PROJECT_ID>/beckn-app/<service>:$(git rev-parse --short HEAD)
```

### Database migration added

The migrate *jobs* have the same `ignore_changes` lifecycle, so
`redeploy:all` above doesn't touch them — update and re-run each job
explicitly:

```sh
task redeploy:migrate-bap
task redeploy:migrate-bpp
```

### Terraform-only change (no new image)

Just re-run Terraform — no `image_tag` needed:

```sh
cd terraform
terraform plan -out=redeploy.tfplan
terraform apply redeploy.tfplan
```

### A secret value changed (key rotation, etc.)

Update the value in `../scripts/seed-secrets.env` and reseed via
`task deploy:seed-secrets PROJECT_ID=<YOUR_PROJECT_ID>`, then force a new
revision on whatever service reads that secret — Secret Manager's `latest`
doesn't propagate to an already-running instance. See
[Gotcha #9 below](#gotchas-actually-hit-doing-this-not-theoretical--each-one-broke-a-real-deploy).

## Keeping local and cloud in sync

If you rotate keys or re-register an identity, the same values need updating
in local dev's `.env`/`config/local-simple-*.yaml` and the cloud secrets
above, or the two environments will drift — see
[INFRA_SETUP.md §3](../INFRA_SETUP.md#3-keeping-local-and-cloud-in-sync) for the
full mapping.

## Gotchas actually hit doing this (not theoretical — each one broke a real deploy)

1. **`BAP_ID`/`NETWORK_PARTICIPANT`/`BAP_KEY_ID` must all agree.** onix signs
   a message using the keyset registered for whatever `subscriber_id` the
   message *itself* claims to be from (`context.bapId`), not just whatever
   identity the onix pod happens to be configured as. If `BAP_ID` says one
   subscriber but the onix keyManager only has keys for a different one:
   `"Keyset not found for keyID: ..."` → 500. Same rule applies to
   `BAP_PRIVATE_KEY`/`BAP_KEY_ID` (used for direct-to-CDS signing, bypassing
   onix) — the CDS itself will reject with `"subscriber identity mismatch"`
   if that keypair's embedded subscriber doesn't match `context.bapId`
   either. There is exactly one registered identity per side; every signing
   path must use it. Especially easy to hit here since this deployment's
   identity is deliberately separate from local dev's — don't let any value
   fall back to the wrong identity.
2. **`sqladmin.googleapis.com` must be enabled on the project.** The Cloud
   SQL Auth Proxy sidecar calls the Cloud SQL Admin API using this project's
   own service account/quota context. Symptom: `cloudsql-proxy` container
   fails with `"Cloud SQL Admin API has not been used in project <number>
   before or it is disabled."`
3. **`tern.conf` renders as a Go template — literal values are never read
   from the environment.** `jackc/tern` parses `tern.conf` through
   `text/template` with Masterminds/sprig functions. A plain
   `host = localhost` line is never substituted; it takes `{{env "PGHOST" |
   default "localhost"}}` syntax to actually pick up `PGHOST` at runtime.
   Without this, the migrate job silently connects to whatever
   host/port/user/db is hardcoded in the file, not the Cloud SQL Auth Proxy
   sidecar — and fails with a plain connection-refused error that doesn't
   obviously point at the real cause.
4. **`gcloud run services update`/`jobs update`'s container-scoped flags
   (`--update-env-vars`, `--image`, etc.) need an explicit `--container=NAME`
   once a service/job has more than one container** (e.g. the
   `cloudsql-proxy` sidecar). Omitting it is either ambiguous or targets the
   wrong container depending on gcloud version.
5. **Cloud Run rejects `memory < 512Mi` when CPU is "always allocated"**
   (the default). The frontend nginx containers hit this at 256Mi even
   though nginx barely needs it — bumped to 512Mi.
6. **First `terraform apply` will fail on `bap`/`bpp` even with everything
   else correct**, because the databases have no schema yet and both apps
   `os.Exit(1)` if a required startup query fails against a table that
   doesn't exist. The migrate jobs (Cloud Run *jobs*, not services) get
   created fine in that same apply since jobs don't block on a health
   check — run them, then re-apply. This is why the fresh-deploy
   sequence above has an apply → migrate → apply-again shape instead of one apply.
7. **Resolved: the registered Subscriber URL and the onix module's
   listening path must agree.** Used to be a problem: if a participant's
   registry entry were a bare URL (e.g. `https://onix-bpp-xxx.a.run.app`, no
   path), other participants doing a registry-based lookup would call
   `<that-bare-url>/<action-name>` directly — e.g. `.../select` — not
   `<url>/bpp/receiver/select`, 404ing against the onix adapter's actual
   receiver path. Now fixed on both fronts that mattered: `context.bapUri`/
   `bppUri` on our own outbound messages already include the receiver path
   (`cloud-run-wiring.tf`, confirmed working via a real `/select` →
   `/on_select` transaction), and the actual registered entry in
   `../dedi-onboarding-files/ion-scratch-registry.json` already has the path
   baked in (`"url": "https://.../bpp/receiver/"`), not a bare URL.
8. **Cloud Run pins an image tag to a specific digest at deploy time and
   never re-resolves it.** Pushing a new image under the same `:latest` tag
   does nothing to an already-running service/job — `lifecycle {
   ignore_changes = [...] }` on every Cloud Run resource means Terraform
   won't touch it either. Force it with `gcloud run services update
   --container=<name> --image=<new-ref>` (services) or `gcloud run jobs
   update --container=<name> --image=<new-ref>` (jobs).
9. **Secret Manager's `version = "latest"` doesn't propagate to already-running
   instances.** Adding a new secret version doesn't do anything until a
   *new revision* starts (secrets are resolved at container start, not
   live). Any change to a secret value needs a forced redeploy of whatever
   service/job reads it — a `terraform apply` that touches an unrelated
   field on that resource is enough, since it creates a new revision.
10. **Cloud SQL private-IP instance creation is slow.** Budget 5-10 minutes
    for step 6 above — it's sequenced as its own targeted apply rather than
    bundled into the full apply, so a failure there is cheap to retry
    without churning any Cloud Run resource.
11. **Subnet CIDR, the PSA reserved range, and Redis's reserved IP range must
    not overlap.** The defaults in `variables.tf` (`10.10.0.0/24` for the
    subnet, an auto-selected `/20` for PSA, `10.20.0.0/29` for Redis) don't
    overlap each other — if you customize any of them, check for overlap
    before applying.
12. **`roles/compute.networkUser` for Direct VPC Egress is granted to the
    Cloud Run Service Agent, not the runtime SA, as a precaution.** Google's
    docs tie this requirement specifically to Shared VPC; in a single
    project it may not be strictly necessary. Kept anyway since it's a
    harmless, idempotent grant — worth checking empirically on first apply
    whether it can be removed.
13. **Adding a new `google_cloud_run_v2_service` to an already-running
    deployment fails with `Image '...' not found`** unless its image was
    built and pushed first. On a fresh deploy this never bites, since
    step 5 above builds every image before the first full apply — it only shows up
    when a new service is added later, one at a time, with no "build all
    images" checkpoint to fall back on. Build and push that one image
    (`docker build`/`docker push` directly, or a full `build-and-push.sh`
    run) before applying it. Hit this adding `onix-catalog-publish` and
    `dedi-static-server`.

## Zitadel-specific notes (design assumptions — verify at first apply, not yet confirmed in practice)

Unlike the gotchas above, these are things `cloud-run-zitadel.tf` assumes rather than things already proven working end-to-end on a real deploy — call them out explicitly if you hit something different:

- **Reuses the `trade` Postgres user/password for Zitadel's database** (both the regular *and* admin connection — see `ZITADEL_DATABASE_POSTGRES_USER_*`/`_ADMIN_*` env vars), rather than a dedicated user. Assumption: Cloud SQL Postgres users get the instance-wide `cloudsqlsuperuser` role, so `trade` should have sufficient DDL rights on the new `zitadel` database the same way it already does on `trade_bap`/`trade_bpp`. If Zitadel's own setup fails with a permissions error, run `ALTER DATABASE zitadel OWNER TO trade;` against the instance (Cloud SQL Studio, see "Inspecting the Cloud SQL instance" below) and re-apply.
- **Discrete `ZITADEL_DATABASE_POSTGRES_*` env vars, not the single `ZITADEL_DATABASE_POSTGRES_DSN` docker-compose uses** — confirmed against Zitadel's own config key paths (`Database.postgres.Host`/`.User.Username`/`.User.Password`/`.User.SSL.Mode`, and the `.Admin.*` equivalents), necessary because Secret Manager injects one value per env var and Cloud Run can't compose a literal prefix + a secret reference into a single string.
- **`--masterkeyFromEnv` instead of docker-compose's `--masterkey <value>` flag** — confirmed supported by running `--help` against the pinned image (`ghcr.io/zitadel/zitadel:v4.17.3` at the time this was written); reads `ZITADEL_MASTERKEY` as an env var instead, since Cloud Run can't inject a secret into a literal `args` string.
- **No `ZITADEL_INTERNAL_URL` on `bap`/`bpp`** (unlike docker-compose) — every Cloud Run service in this deployment uses `INGRESS_TRAFFIC_ALL` and calls each other via their normal public `.uri`, so there's no docker-network-style "can't reach my own host-published port" problem to work around. `ZITADEL_ISSUER` alone is enough; both apps' `config.go` already default the internal URL to equal it when unset.
- **The Cloud Run `startup_probe` on the zitadel container is a plain TCP check on port 8080**, not an HTTP health-endpoint check — simpler and safer than guessing at Zitadel's exact health-check path, but slower to detect a container that's listening but not actually healthy. Tighten this to an HTTP probe against Zitadel's readiness endpoint if that turns out to matter in practice.

## Verification

- `terraform output bap_frontend_url bpp_frontend_url` — open both, confirm the SPA loads.
- `gcloud run jobs executions list --job=migrate-bap --project=ion-sandbox-001` — confirm `Succeeded` (same for migrate-bpp).
- Repeat the discover → select flow against the new `bap_url`, watching
  `gcloud logging read 'resource.type="cloud_run_revision" AND resource.labels.service_name="bap"' --project=ion-sandbox-001 --freshness=5m`
  (swap the service name for `beckn-onix-bap`/`bpp`/`beckn-onix-bpp` as needed) for
  signing errors or connectivity failures.
- A cloudsql-proxy sidecar failing its startup probe means the
  `roles/cloudsql.client` grant isn't working; onix logs timing out reaching
  Redis means the `compute.networkUser` subnet grant isn't working, or the
  Redis instance isn't in `READY` state yet.
- Seed a catalog via `../catalog-seed/` against the new `bpp_url`.

## Inspecting the Cloud SQL instance

It has no public IP and isn't reachable by `cloud-sql-proxy` run outside a
VPC-attached environment (your laptop won't reach the private IP even with
`--private-ip` — only Cloud Run's Direct VPC Egress can). Use **Cloud SQL
Studio** in the console instead — no network path needed, goes through the
Cloud SQL Admin API:
```
https://console.cloud.google.com/sql/instances/beckn-app-postgres/studio?project=ion-sandbox-001
```
Log in inside Studio with the `trade` user and the `postgres-password`
secret's value (`gcloud secrets versions access latest --secret=postgres-password --project=ion-sandbox-001`).
