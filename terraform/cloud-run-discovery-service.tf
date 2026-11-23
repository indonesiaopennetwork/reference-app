# A standalone Beckn v2 Discovery Service (DS): serves POST/GET /discover,
# crawling CATALOG_INDEX_URLS for real catalog data. Stateless and
# in-memory only (see discovery-service/README.md) — no DB, no VPC egress,
# no sidecar needed, unlike bap/bpp.
#
# TRUSTED_CALLER_KEY_ID/TRUSTED_CALLER_PUBLIC_KEY_BASE64 reuse bap's own
# PUBLIC identifiers (safe to share — see discovery-service/.env.example),
# not a separate secret.
#
# SUBSCRIBER_ID/SUBSCRIBER_URI default to bap's own onix-bap identity
# (var.bap_id / onix-bap's Cloud Run URL) unless overridden by
# discovery_service_subscriber_id/uri — see the comment on those
# variables in variables.tf for why. SUBSCRIBER_KEY_ID/
# SIGNING_PRIVATE_KEY_BASE64 come from their own dedicated secrets
# (discovery-service-subscriber-key-id/discovery-service-signing-private-key,
# see secrets.tf) so rotating to a real, independently-registered
# identity later is just a reseed of those two secrets — no Terraform
# change needed.

resource "google_cloud_run_v2_service" "discovery_service" {
  name                = "discovery-service"
  project             = var.project_id
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false
  depends_on          = [google_project_service.apis]

  template {
    service_account = google_service_account.cloud_run_sa.email

    scaling {
      # 0: scale to zero when idle, so nothing is billed without traffic.
      # Trade-off: the first request after an idle period pays a cold start
      # (container boot plus any in-process cache warm-up -- measured at
      # 10-35s+ for the onix modules), which a flaky client network can abort
      # (net::ERR_NETWORK_CHANGED). Raise to 1 to keep an instance warm.
      min_instance_count = 0
      max_instance_count = 3
    }

    containers {
      name  = "discovery-service"
      image = local.images.discovery_service

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
        cpu_idle = true # request-based billing
      }

      env {
        name = "TRUSTED_CALLER_KEY_ID"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["bap-key-id"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "TRUSTED_CALLER_PUBLIC_KEY_BASE64"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["bap-onix-signing-public-key"].secret_id
            version = "latest"
          }
        }
      }
      env {
        # Our own bpp's index first, then any other Provider Nodes' indexes
        # (var.extra_catalog_index_urls), comma-separated.
        name = "CATALOG_INDEX_URLS"
        value = join(",", concat(
          ["https://${var.bpp_domain}/index/becknCatalogs.index.json"],
          var.extra_catalog_index_urls,
        ))
      }
      env {
        name  = "SUBSCRIBER_ID"
        value = var.discovery_service_subscriber_id != "" ? var.discovery_service_subscriber_id : var.bap_id
      }
      env {
        # Not google_cloud_run_v2_service.onix_bap.uri -- that would make
        # this a circular dependency (onix-bap's own DISCOVERY_SERVICE_URL
        # already points back at this service). Also fine on its own
        # terms: SUBSCRIBER_URI only ever ends up as the informational
        # context.bppUri field on outbound on_discover responses
        # (discovery-service/internal/service/response.go), never used in
        # signature verification, so it doesn't need to be onix-bap's
        # exact URL -- bap's own public domain identifies the same
        # borrowed identity just as well.
        name  = "SUBSCRIBER_URI"
        value = var.discovery_service_subscriber_uri != "" ? var.discovery_service_subscriber_uri : "https://${var.bap_domain}"
      }
      env {
        name = "SUBSCRIBER_KEY_ID"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["discovery-service-subscriber-key-id"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "SIGNING_PRIVATE_KEY_BASE64"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["discovery-service-signing-private-key"].secret_id
            version = "latest"
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [template[0].containers[0].image]
  }
}
