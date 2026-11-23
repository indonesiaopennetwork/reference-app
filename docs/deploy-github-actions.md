# Deploying from GitHub Actions

`.github/workflows/deploy.yml` deploys to Cloud Run whenever something is
pushed or merged to `main`. It redeploys only what changed, the same way
`task redeploy:<service>` does:

| Change under | What happens |
|---|---|
| `bap-application/`, `bpp-application/`, `onix-bap/`, `onix-bpp/`, `onix-catalog-publish/`, `dedi-static-server/`, `landing-page/`, `discovery-service/` | That service is rebuilt, pushed and rolled out |
| `bap-frontend/`, `bpp-frontend/` | That app is built (`npm run build`) and uploaded to the `<project>-frontends-static` bucket (`cn/` / `pn/`); no Cloud Run service, ion-landing-page serves it |
| `otel-collector/` | The collector sidecar is updated in `beckn-onix-bap` and `beckn-onix-bpp` |
| `bap-application/db/migrations/`, `bpp-application/db/migrations/` | `migrate-bap` / `migrate-bpp` runs first; services roll out only if it succeeds |
| `ion-landing-page/Dockerfile.cloud`, `nginx.conf.template`, `env.sh` | The `ion-landing-page` image is rolled out |
| `ion-landing-page/index.html` | Uploaded to the `<project>-ion-landing-page-static` bucket |

Images are tagged with the short commit SHA. You can also run the workflow by
hand from the **Actions** tab, with **Redeploy every service**, **Run both
migration jobs** and **Apply app settings only** (see [App settings](#app-settings)).

It deploys application changes and two app settings only. Infrastructure
changes (`terraform/`) are still applied with Terraform, as in
`terraform/README.md`.

## One-time setup

The workflow signs in to Google Cloud with Workload Identity Federation, so
no service-account key is stored in GitHub. Run these once, with
`PROJECT_ID` and `REGION` set to the `project_id` and `region` in
`terraform/terraform.tfvars`.

```bash
PROJECT_ID=<your project>
REGION=<region from terraform.tfvars>   # e.g. asia-southeast2 (the default) or asia-southeast1
GITHUB_REPO=indonesiaopennetwork/reference-app
PROJECT_NUMBER=$(gcloud projects describe "$PROJECT_ID" --format='value(projectNumber)')
DEPLOY_SA="github-deploy@$PROJECT_ID.iam.gserviceaccount.com"

# 1. The service account the workflow acts as.
gcloud iam service-accounts create github-deploy --project="$PROJECT_ID" \
  --display-name="GitHub Actions deploy"

# 2. What it may do: push images, update services and jobs, run jobs,
#    deploy revisions that run as the app's runtime account, and upload the
#    landing page.
gcloud artifacts repositories add-iam-policy-binding beckn-app \
  --project="$PROJECT_ID" --location="$REGION" \
  --member="serviceAccount:$DEPLOY_SA" --role=roles/artifactregistry.writer
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:$DEPLOY_SA" --role=roles/run.developer
gcloud iam service-accounts add-iam-policy-binding \
  "beckn-app-run-sa@$PROJECT_ID.iam.gserviceaccount.com" --project="$PROJECT_ID" \
  --member="serviceAccount:$DEPLOY_SA" --role=roles/iam.serviceAccountUser
gcloud storage buckets add-iam-policy-binding "gs://$PROJECT_ID-ion-landing-page-static" \
  --member="serviceAccount:$DEPLOY_SA" --role=roles/storage.objectAdmin
# The buyer (cn/) and seller (pn/) apps' built files (frontend-files job).
gcloud storage buckets add-iam-policy-binding "gs://$PROJECT_ID-frontends-static" \
  --member="serviceAccount:$DEPLOY_SA" --role=roles/storage.objectAdmin

# 3. Let GitHub Actions runs from this repository's main branch use it.
gcloud iam workload-identity-pools create github --project="$PROJECT_ID" \
  --location=global --display-name="GitHub Actions"
gcloud iam workload-identity-pools providers create-oidc reference-app \
  --project="$PROJECT_ID" --location=global --workload-identity-pool=github \
  --issuer-uri="https://token.actions.githubusercontent.com" \
  --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.ref=assertion.ref" \
  --attribute-condition="assertion.repository == '$GITHUB_REPO' && assertion.ref == 'refs/heads/main'"
gcloud iam service-accounts add-iam-policy-binding "$DEPLOY_SA" --project="$PROJECT_ID" \
  --role=roles/iam.workloadIdentityUser \
  --member="principalSet://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/github/attribute.repository/$GITHUB_REPO"

echo "GCP_WORKLOAD_IDENTITY_PROVIDER=projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/github/providers/reference-app"
echo "GCP_DEPLOY_SERVICE_ACCOUNT=$DEPLOY_SA"
```

Then add these as repository **variables**, not secrets. Either use the
repository page (Settings → Secrets and variables → Actions → **Variables**
tab → **New repository variable**), or the `gh` CLI, which needs admin access
to the repository:

```bash
gh variable set GCP_PROJECT_ID -R "$GITHUB_REPO" -b "$PROJECT_ID"
gh variable set GCP_REGION -R "$GITHUB_REPO" -b "$REGION"
gh variable set GCP_WORKLOAD_IDENTITY_PROVIDER -R "$GITHUB_REPO" \
  -b "projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/github/providers/reference-app"
gh variable set GCP_DEPLOY_SERVICE_ACCOUNT -R "$GITHUB_REPO" -b "$DEPLOY_SA"
```

| Variable | Value |
|---|---|
| `GCP_PROJECT_ID` | the project ID |
| `GCP_WORKLOAD_IDENTITY_PROVIDER` | printed above |
| `GCP_DEPLOY_SERVICE_ACCOUNT` | printed above |
| `GCP_REGION` | the `region` in `terraform.tfvars`; required unless it is the default `asia-southeast2` |
| `GCP_ARTIFACT_REPO` | optional, default `beckn-app` |

Until they are set, the workflow stops at its first step with a message
naming the missing variables.

## App settings

The **App settings** job sets two environment variables on the Cloud Run
services from GitHub repository variables, so they can be changed without
access to the Google Cloud project and without rebuilding images:

| Repository variable | Cloud Run service (container) | Allowed values |
|---|---|---|
| `SETTLEMENT_WINDOW` | `bpp` (`bpp`) | ISO 8601 duration, e.g. `PT2M`, `P1D` |
| `RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW` | `bap` (`bap`) and `bpp` (`bpp`) | `true` or `false` |

Both are required. The job signs in the same way as the rest of the workflow
(Workload Identity Federation, the repository variables from
[One-time setup](#one-time-setup)); the deploy service account's
`roles/run.developer` covers it.

### Setting the variables

On the repository page: Settings → Secrets and variables → Actions →
**Variables** tab → **New repository variable** (or edit an existing one).
Or with the `gh` CLI:

```bash
gh variable set SETTLEMENT_WINDOW --body 'PT2M'
gh variable set RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW --body 'false'
```

Changing a variable doesn't start a run; the new value is applied by the next
run (see below).

### When it runs

- **Push to `main`:** after the deploy, as the last job. It runs whether or
  not anything was rebuilt, and not after a failed migration or rollout.
- **By hand, with "Apply app settings only":** Actions → **Deploy** →
  **Run workflow**, tick **Apply app settings only** (input
  `apply_app_settings_only`). Only this job runs: nothing is built, pushed,
  migrated or deployed, whatever the other options are. From the CLI:
  `gh workflow run deploy.yml -f apply_app_settings_only=true`.
- **By hand, without it:** the usual deploy, then this job.

### What it does

1. Checks both variables (set, and in the allowed form) before signing in; if
   one is missing or invalid the job fails with a message naming it, and
   neither service is changed.
2. Reads the `bpp` and `bap` services. If either can't be read, or its app
   container is missing, the job fails before changing either.
3. For each service whose values differ, runs
   `gcloud run services update <service> --container=<container> --update-env-vars=...`
   with only the differing variables. `--update-env-vars` changes those names
   and keeps every other variable (including those from Secret Manager); the
   `cloudsql-proxy` sidecar isn't touched. Each update creates one new
   revision, which takes all traffic. A service already up to date is left
   alone, with no new revision.

The job summary lists what was set on each service. Only these two values are
printed, never the services' other variables. If an update fails the job
fails; `bpp` is updated before `bap`, so a failed `bap` update can leave
`bpp` already updated -- rerun the job once the cause is fixed.

### Terraform

Terraform (`terraform/cloud-run-bap.tf`, `cloud-run-bpp.tf`) declares the
same variables on the same services, from `var.settlement_window` (default
`PT2M`) and `var.reconcile_wait_for_settlement_window` (default `false`).
`terraform apply` sets the services to Terraform's values, so keep the two in
step: if you change a repository variable, change the Terraform default (or
the value in `terraform.tfvars`, which takes precedence) too, or the next
apply puts the old value back. The repository variables are the values for
day-to-day changes; Terraform is what a full apply restores.

### Checking and troubleshooting

- See the values on the services (needs access to the project):
  `gcloud run services describe bpp --region=<region> --format=json | jq '.spec.template.spec.containers[] | select(.name=="bpp") | .env'`
- "Repository variable ... is not set" / "... is not an ISO 8601 duration" /
  "... must be true or false": set or fix the variable, then run the workflow
  with **Apply app settings only**.
- "Could not read Cloud Run service ...": check `GCP_PROJECT_ID` and
  `GCP_REGION`, and that the service exists there.
- "Updating ... failed": the `gcloud` output above it gives the reason, often
  missing permissions for the deploy service account.
- The job didn't run on a push: it is skipped when a migration or a service
  rollout failed; fix that and rerun.

