# Maps var.bpp_domain / var.bpp_domain_www to dedi-static-server.
#
# PREREQUISITE (outside Terraform, cannot be automated here): the domain must
# be a VERIFIED domain in Search Console
# (https://search.google.com/search-console) under the same account/org that
# owns this GCP project — `terraform apply` fails on these resources until
# that's done.
#
# CAVEAT: Cloud Run domain mapping has historically had self-service
# availability limited to a subset of regions (us-central1, us-east1,
# europe-west1, asia-east1 in older docs) for domains outside those. If this
# resource fails with a region/availability error even after domain
# verification, either move dedi-static-server to one of those regions, or
# run `gcloud beta run domain-mappings create --service=dedi-static-server
# --domain=<domain> --region=<region>` directly for a clearer error message.
#
# After a successful apply, read the DNS records to create at your registrar
# from the `bpp_domain_dns_records` / `bpp_domain_www_dns_records` outputs.

resource "google_cloud_run_domain_mapping" "dedi_apex" {
  name     = var.bpp_domain
  location = var.region
  project  = var.project_id

  metadata {
    namespace = var.project_id
  }

  spec {
    route_name = google_cloud_run_v2_service.dedi_static.name
  }
}

resource "google_cloud_run_domain_mapping" "dedi_www" {
  name     = var.bpp_domain_www
  location = var.region
  project  = var.project_id

  metadata {
    namespace = var.project_id
  }

  spec {
    route_name = google_cloud_run_v2_service.dedi_static.name
  }
}

# bap_domain is a hub, not a direct 1:1 mapping — it routes to
# dedi-static-server, whose nginx (server_name-differentiated, see
# nginx.conf.template) sends /bap/receiver/ to onix-bap (validated) and
# everything else straight to bap. Mirrors how bpp_domain already works
# for bpp/onix-bpp.
resource "google_cloud_run_domain_mapping" "bap" {
  name     = var.bap_domain
  location = var.region
  project  = var.project_id

  metadata {
    namespace = var.project_id
  }

  spec {
    route_name = google_cloud_run_v2_service.dedi_static.name
  }
}

resource "google_cloud_run_domain_mapping" "landing_page" {
  name     = var.landing_page_domain
  location = var.region
  project  = var.project_id

  metadata {
    namespace = var.project_id
  }

  spec {
    route_name = google_cloud_run_v2_service.landing_page.name
  }
}

resource "google_cloud_run_domain_mapping" "ion_landing_page" {
  name     = var.ion_landing_page_domain
  location = var.region
  project  = var.project_id

  metadata {
    namespace = var.project_id
  }

  spec {
    route_name = google_cloud_run_v2_service.ion_landing_page.name
  }
}

resource "google_cloud_run_domain_mapping" "zitadel" {
  name     = var.zitadel_domain
  location = var.region
  project  = var.project_id

  metadata {
    namespace = var.project_id
  }

  spec {
    route_name = google_cloud_run_v2_service.zitadel.name
  }
}
