# Public static-file storage for ion-landing-page (the front page), served
# on a custom domain via the ion-landing-page Cloud Run service
# (cloud-run-ion-landing-page.tf), which mounts this bucket read-only.
# Content is uploaded here directly (e.g. via `task
# deploy-ion-landing-page-files`) -- nothing writes to it programmatically,
# unlike dedi_static (gcs-dedi-static.tf), which onix-catalog-publish writes
# into.

resource "google_storage_bucket" "ion_landing_page_static" {
  name                        = "${var.project_id}-ion-landing-page-static"
  project                     = var.project_id
  location                    = var.region
  force_destroy               = false
  uniform_bucket_level_access = true

  # Required for the allUsers IAM binding below to actually take effect — a
  # project/org with the "Public access prevention" org policy enforced will
  # still reject it regardless of this setting; that's an external policy
  # check outside Terraform's control.
  public_access_prevention = "inherited"
}

resource "google_storage_bucket_iam_member" "ion_landing_page_public_read" {
  bucket = google_storage_bucket.ion_landing_page_static.name
  role   = "roles/storage.objectViewer"
  member = "allUsers"
}
