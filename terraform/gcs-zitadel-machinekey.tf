# Private bucket Zitadel's container mounts at /machinekey (see
# cloud-run-zitadel.tf) so the automation machine user's PAT
# (ZITADEL_FIRSTINSTANCE_PATPATH), written on first boot, survives revision
# restarts and is readable afterward by scripts/setup-zitadel-cloud.sh. This
# is the Cloud Run equivalent of docker-compose's
# ./.zitadel-machinekey:/machinekey bind mount — unlike dedi_static
# (gcs-dedi-static.tf), this bucket is never public.

resource "google_storage_bucket" "zitadel_machinekey" {
  name                        = "${var.project_id}-zitadel-machinekey"
  project                     = var.project_id
  location                    = var.region
  force_destroy               = false
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
}

resource "google_storage_bucket_iam_member" "zitadel_machinekey_readwrite" {
  bucket = google_storage_bucket.zitadel_machinekey.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.cloud_run_sa.email}"
}
