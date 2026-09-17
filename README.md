# beckn-app

A reference implementation of a buyer (BAP) and seller (BPP) application on
the Beckn protocol, plus the Beckn network adapters (onix) that sign, route,
and validate their traffic.

| Service | What it is | Local port | Source |
|---|---|---|---|
| `bap` | Buyer-side app (Go) | 8083 | `bap-application/` |
| `bpp` | Seller-side app (Go) | 8080 | `bpp-application/` |
| `bap-frontend` | Buyer web UI | 3000 | `bap-frontend/` |
| `bpp-frontend` | Seller/admin web UI | 3001 | `bpp-frontend/` |
| `onix-bap` | Beckn network adapter for bap | 8081 | `onix-bap/` |
| `onix-bpp` | Beckn network adapter for bpp | 8082 | `onix-bpp/` |

Plus Postgres and two Redis instances (Docker Compose locally; a dedicated
Cloud SQL instance and a dedicated Memorystore instance in the cloud).

## Docs map

- **Local development** (Docker Compose, fastest way to get a working
  end-to-end system) → [INFRA_SETUP.md](INFRA_SETUP.md#1-local-development-docker-compose)
- **Deploy to your own fresh GCP project** → this README, below
- **Cloud deploy reference** (every gotcha hit building this, verification,
  redeploys, inspecting Cloud SQL) → [terraform/README.md](terraform/README.md)

## Prerequisites

Before touching Terraform, make sure you have:

- A GCP project with a **billing account linked**. A brand-new project has
  none by default, and the first bootstrap step fails immediately without one.
- `gcloud` authenticated with Owner (or equivalent) on that project, plus
  Application Default Credentials configured (separate credential store from
  `gcloud auth login`):
  ```sh
  gcloud auth application-default login
  gcloud auth application-default set-quota-project <YOUR_PROJECT_ID>
  ```
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

Not committed (already git-ignored). At minimum, override `project_id` and
supply the five identity variables that have no default, plus the Zitadel
admin account (the seven domain variables, including `zitadel_domain`, are
covered separately below — see "Custom domains"):

```hcl
# terraform/terraform.tfvars
project_id             = "<YOUR_PROJECT_ID>"
region                 = "<YOUR_REGION>"
bap_id                 = "<your registered onix-bap identity>"
bpp_id                 = "<your registered onix-bpp identity>"
network_id             = "<your registry's network id>"
cds_discover_url       = "<your registry's discover URL>"
zitadel_admin_username = "admin"
zitadel_admin_email    = "<your email>"
```

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
[terraform/cloud-run-zitadel.tf](terraform/cloud-run-zitadel.tf) — `bpp`
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

For the full list of gotchas hit while building this deploy (secret
propagation, image pinning, subnet sizing, and more), see
[terraform/README.md](terraform/README.md#gotchas-actually-hit-doing-this-not-theoretical--each-one-broke-a-real-deploy).

## Verify the deployment

```sh
terraform output bap_frontend_url bpp_frontend_url
```

Open both URLs and confirm the SPA loads. Then seed some demo catalog data
via [catalog-seed/](catalog-seed/) against the new `bpp_url` and try a
discover flow end-to-end.

## Custom domain for DeDi discovery files (required for real network participation)

`onix-catalog-publish` writes signed catalog files (`index/becknCatalogs.index.json`,
`catalogs/*.json.gz`) to `outputRoot` on every "Publish to Network" click.
`terraform/gcs-dedi-static.tf` and `terraform/cloud-run-dedi-static.tf` back
that `outputRoot` with a public GCS bucket and a small nginx service
(`dedi-static-server`), and `terraform/domain-mapping.tf` maps it onto a
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
(`terraform/cloud-run-dedi-static.tf`) references `bap`/`bpp`/`onix-bap`/
`onix-bpp`'s live Cloud Run URLs directly (`google_cloud_run_v2_service.bap.uri`
etc.), so Terraform cannot create it until all four of those already exist
with a real URL — which itself requires the DB schema to exist (step 8) and
`bap`/`bpp` to have successfully started (step 9). This is a hard
dependency in the Terraform graph, not a documentation-ordering choice —
attempting this section before step 9 succeeds will just fail trying to
resolve URLs for services that don't exist yet.

**`bpp_domain`/`bpp_domain_www` have no default** — same treatment as
`bap_id`/`bpp_id`/`network_id`/`cds_discover_url` above, and for the same
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

1. Set `bpp_domain`/`bpp_domain_www` in `terraform.tfvars` — required,
   `terraform plan` fails without them.
2. Build and push the `dedi-static-server` image — it's included in step 5
   above if you're deploying fresh. If you're adding this to an
   **already-running** deployment, build and push it explicitly before
   applying, since there's no "build all images" checkpoint to fall back on
   at that point:
   ```sh
   gcloud auth configure-docker <YOUR_REGION>-docker.pkg.dev --quiet
   docker build -t <YOUR_REGION>-docker.pkg.dev/<YOUR_PROJECT_ID>/beckn-app/dedi-static-server:latest dedi-static-server/
   docker push <YOUR_REGION>-docker.pkg.dev/<YOUR_PROJECT_ID>/beckn-app/dedi-static-server:latest
   ```
3. Create the bucket, `dedi-static-server`, and its two domain mappings:
   ```sh
   task deploy:dedi-static
   ```
   If `google_cloud_run_domain_mapping` fails with a region/availability
   error, see the comment at the top of `terraform/domain-mapping.tf` —
   Cloud Run domain mapping has historically had self-service limited to a
   subset of regions.
4. Get the DNS records and create them at your registrar (the one step I
   can't automate):
   ```sh
   terraform output bpp_domain_dns_records
   terraform output bpp_domain_www_dns_records
   ```
5. Upload the two files that rarely change and aren't generated by anything
   in this repo. Put `dedi.index.json` and `ion-scratch-registry.json` in
   `dedi-onboarding-files/` (see `dedi-onboarding-files/.env.local.example`
   for the signing keys), then re-sign and upload them:
   ```sh
   task sign-dedi-files
   task deploy-dedi-files
   ```
6. Wait for the managed SSL cert (up to ~60 min after DNS propagates), then
   `curl https://<your domain>/.well-known/dedi.index.json`.

## Routing BPP/BAP receiver calls through the custom domain (optional)

Builds on the section above — requires the custom domain already mapped to
`dedi-static-server`. `dedi-static-server`'s nginx also reverse-proxies
`/bpp/receiver/*` → `onix-bpp` and `/bap/receiver/*` → `onix-bap`, so a
subscriber's registered URL can be `https://<your domain>/bpp/receiver/` (or
`/bap/receiver/`) instead of `onix-bpp`'s/`onix-bap`'s raw `*.run.app` URL.

This also sidesteps the path mismatch in
[terraform/README.md's Gotcha #7](terraform/README.md#gotchas-actually-hit-doing-this-not-theoretical--each-one-broke-a-real-deploy):
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
- `terraform/cloud-run-dedi-static.tf` — sets `ONIX_BPP_HOST`/`ONIX_BAP_HOST`
  on the container from `google_cloud_run_v2_service.onix_bpp.uri` /
  `google_cloud_run_v2_service.onix_bap.uri` (scheme stripped).
- Redeploying after touching any of the above is the same as any other
  Cloud Run image change — see "Redeploy after a change" → "Code change in
  any service" below.

**Do not extend this to `/bpp/caller/` or `/bap/caller/`.** Unlike the
receiver modules (`validateSign` is their first processing step), the caller
modules have no inbound signature validation — they trust whoever calls them
and sign+forward outbound messages on the participant's behalf. They're
meant to stay reachable only internally: `BPP_CALLER_URL`/`ADAPTER_URL`
point directly at `onix-bpp`'s/`onix-bap`'s raw Cloud Run URL, set by
`terraform/cloud-run-wiring.tf`. Proxying either publicly would let anyone
get that adapter to sign and send forged outbound messages impersonating
this participant.

**Update:** `BAP_URI`/`BPP_URI` (the in-band `context.bapUri`/`context.bppUri`
fields on outbound messages) now *do* point at this same domain+path —
`terraform/cloud-run-wiring.tf` sets them to `https://${var.bpp_domain}/bap/receiver/`
/ `.../bpp/receiver/`. This is required, not just cosmetic: other network
participants POST callbacks to `{bapUri}/<action>` directly, and
`bapTxnReceiver`/`bppTxnReceiver` only listen at `/bap/receiver/`/
`/bpp/receiver/` (see `config/local-simple-bap.yaml`/`local-simple-bpp.yaml`)
— a bare `bapUri` with no path would misroute every inbound callback.

## Custom domains for bap, bap-frontend, bpp-frontend, landing page, and Zitadel (required)

`terraform/domain-mapping.tf` also maps `bap` (`var.bap_domain`),
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
mapping: `terraform/cloud-run-bap-frontend.tf` also injects
`bap_frontend_domain` into `bap-frontend`'s own container as `BPP_ADMIN_URL`,
and `terraform/cloud-run-bpp-frontend.tf` injects `bpp_frontend_domain` into
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

## Redeploy after a change

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
them all) — see `Taskfile.yaml` for the full set, or run the equivalent
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

Update the value in `scripts/seed-secrets.env` and reseed via
`task deploy:seed-secrets PROJECT_ID=<YOUR_PROJECT_ID>`, then force a new
revision on whatever service reads that secret — Secret Manager's `latest`
doesn't propagate to an already-running instance. See
[terraform/README.md's Gotcha #9](terraform/README.md#gotchas-actually-hit-doing-this-not-theoretical--each-one-broke-a-real-deploy).

## Keeping local and cloud in sync

If you rotate keys or re-register an identity, the same values need updating
in local dev's `.env`/`config/local-simple-*.yaml` and the cloud secrets
above, or the two environments will drift — see
[INFRA_SETUP.md §3](INFRA_SETUP.md#3-keeping-local-and-cloud-in-sync) for the
full mapping.
