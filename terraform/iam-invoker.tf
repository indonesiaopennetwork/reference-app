# All services are publicly reachable (--allow-unauthenticated equivalent) —
# matches the reference deployment's documented demo-app pattern. IAM-based
# service-to-service auth (minting an ID token on every outbound call) would
# be more secure but requires app code changes; left as a documented
# future-hardening step, not implemented here.
#
# Each service gets its OWN standalone google_cloud_run_v2_service_iam_member
# resource (matching dedi_static_public_invoker below), rather than one
# resource for_each over a shared map. Tried the shared-map version first and
# hit two real bugs from it:
#   1. A map built from nine services' .name attributes makes EVERY instance
#      (even a single -target'd one, e.g. public_invoker["zitadel"])
#      transitively depend on all nine, since Terraform's dependency graph is
#      per resource *block*, not per for_each key -- a targeted apply for one
#      service's public access would pull in and fail on any OTHER unhealthy
#      service in the map, unrelated to the one being targeted.
#   2. "Fixing" that by making the map's values plain string literals removed
#      ALL dependency edges, including the (correct, needed) one between each
#      binding and its OWN service -- so Terraform could try to set IAM
#      policy on a service before/without it existing yet ("Resource 'bap' of
#      kind 'SERVICE' ... does not exist" mid-apply).
# Standalone resources give each binding an accurate, narrow dependency on
# exactly its own service and nothing else -- no shared map to entangle them.

resource "google_cloud_run_v2_service_iam_member" "bap_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.bap.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_service_iam_member" "bpp_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.bpp.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_service_iam_member" "bap_frontend_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.bap_frontend.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_service_iam_member" "bpp_frontend_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.bpp_frontend.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_service_iam_member" "onix_bap_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.onix_bap.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_service_iam_member" "onix_bpp_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.onix_bpp.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_service_iam_member" "onix_catalog_publish_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.onix_catalog_publish.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_service_iam_member" "landing_page_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.landing_page.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_service_iam_member" "zitadel_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.zitadel.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_service_iam_member" "discovery_service_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.discovery_service.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

resource "google_cloud_run_v2_service_iam_member" "ion_landing_page_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.ion_landing_page.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# dedi_static lives in a different region (asia-southeast1, hardcoded in
# cloud-run-dedi-static.tf) than var.region — it can't share the single
# `location = var.region` blocks above, which assume every entry is in the
# same region.
resource "google_cloud_run_v2_service_iam_member" "dedi_static_public_invoker" {
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.dedi_static.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# State migration from the earlier shared-for_each design (see comment
# above) -- these seven bindings already existed and succeeded under the
# old addresses; move them instead of destroying/recreating (which would
# briefly revoke public access on already-working services). bap and
# landing_page never successfully existed in state under the old design
# (their creation raced with/depended on their own service, which is
# exactly bug #2 above), so they have nothing to move -- they're just
# created fresh under the new addresses.
moved {
  from = google_cloud_run_v2_service_iam_member.public_invoker["bap_frontend"]
  to   = google_cloud_run_v2_service_iam_member.bap_frontend_public_invoker
}

moved {
  from = google_cloud_run_v2_service_iam_member.public_invoker["bpp"]
  to   = google_cloud_run_v2_service_iam_member.bpp_public_invoker
}

moved {
  from = google_cloud_run_v2_service_iam_member.public_invoker["bpp_frontend"]
  to   = google_cloud_run_v2_service_iam_member.bpp_frontend_public_invoker
}

moved {
  from = google_cloud_run_v2_service_iam_member.public_invoker["onix_bap"]
  to   = google_cloud_run_v2_service_iam_member.onix_bap_public_invoker
}

moved {
  from = google_cloud_run_v2_service_iam_member.public_invoker["onix_bpp"]
  to   = google_cloud_run_v2_service_iam_member.onix_bpp_public_invoker
}

moved {
  from = google_cloud_run_v2_service_iam_member.public_invoker["onix_catalog_publish"]
  to   = google_cloud_run_v2_service_iam_member.onix_catalog_publish_public_invoker
}

moved {
  from = google_cloud_run_v2_service_iam_member.public_invoker["zitadel"]
  to   = google_cloud_run_v2_service_iam_member.zitadel_public_invoker
}
