# Public static-file storage for the two SPAs, replacing the old
# bap-frontend / bpp-frontend Cloud Run services:
#   cn/  bap-frontend's `npm run build` output (buyer app)
#   pn/  bpp-frontend's `npm run build` output (seller app)
# Served at <ion_landing_page_domain>/tsp/remiges/cn/ and /pn/ by the
# ion-landing-page nginx, which proxies to this bucket's public URL
# (https://storage.googleapis.com/<bucket>/cn/...) and also forwards each
# app's /api/ calls straight to bap / bpp -- see
# ion-landing-page/nginx.conf.template.
#
# Content is uploaded by the deploy workflow's frontend-files job (or `task
# deploy-frontend-files`) -- nothing writes to it at runtime. env-config.js
# is NOT stored here: ion-landing-page generates it at startup (env.sh) so
# runtime URLs stay owned by Terraform.

resource "google_storage_bucket" "frontends_static" {
  name                        = "${var.project_id}-frontends-static"
  project                     = var.project_id
  location                    = var.region
  force_destroy               = false
  uniform_bucket_level_access = true

  # Required for the allUsers IAM binding below to actually take effect -- a
  # project/org with the "Public access prevention" org policy enforced will
  # still reject it regardless of this setting.
  public_access_prevention = "inherited"
}

resource "google_storage_bucket_iam_member" "frontends_public_read" {
  bucket = google_storage_bucket.frontends_static.name
  role   = "roles/storage.objectViewer"
  member = "allUsers"
}
