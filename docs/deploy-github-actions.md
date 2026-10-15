# Deploying from GitHub Actions

`.github/workflows/deploy.yml` deploys to Cloud Run whenever something is
pushed or merged to `main`. It redeploys only what changed, the same way
`task redeploy:<service>` does:

| Change under | What happens |
|---|---|
| `bap-application/`, `bpp-application/`, `bap-frontend/`, `bpp-frontend/`, `onix-bap/`, `onix-bpp/`, `onix-catalog-publish/`, `dedi-static-server/`, `landing-page/`, `discovery-service/` | That service is rebuilt, pushed and rolled out |
| `otel-collector/` | The collector sidecar is updated in `beckn-onix-bap` and `beckn-onix-bpp` |
| `bap-application/db/migrations/`, `bpp-application/db/migrations/` | `migrate-bap` / `migrate-bpp` runs first; services roll out only if it succeeds |
| `ion-landing-page/Dockerfile.cloud`, `nginx.conf.template`, `env.sh` | The `ion-landing-page` image is rolled out |
| `ion-landing-page/index.html` | Uploaded to the `<project>-ion-landing-page-static` bucket |

Images are tagged with the short commit SHA. You can also run the workflow by
hand from the **Actions** tab, with **Redeploy every service** and **Run both
migration jobs**.

It deploys application changes only. Infrastructure changes (`terraform/`)
are still applied with Terraform, as in `terraform/README.md`.

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
