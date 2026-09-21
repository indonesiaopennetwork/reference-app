# Static entry-point page with buttons to bap-frontend and bpp-frontend, and
# the OIDC login flow itself (see landing-page/src/oidc.js). Plain nginx, no
# DB, no volume mount — every env var below is read by the BROWSER (this is
# a client-side SPA), so they must all be host-published, browser-reachable
# addresses, not internal hostnames — envsubst'd into index.html.template by
# entrypoint.sh at container startup (mirrors onix-bap/entrypoint.sh's
# templating pattern).

resource "google_cloud_run_v2_service" "landing_page" {
  name                = "landing-page"
  project             = var.project_id
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false
  depends_on          = [google_project_service.apis]

  template {
    service_account = google_service_account.cloud_run_sa.email

    scaling {
      min_instance_count = 0
      max_instance_count = 3
    }

    containers {
      name  = "landing-page"
      image = local.images.landing_page

      ports {
        container_port = 80
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      env {
        name  = "BAP_URL"
        value = "https://${var.bap_frontend_domain}"
      }
      env {
        name  = "BPP_URL"
        value = "https://${var.bpp_frontend_domain}"
      }
      env {
        # Public bpp API URL -- landing-page's bootstrap() call hits
        # ${config.bppApiUrl}/api/v1/auth/bootstrap directly (see
        # landing-page/src/App.jsx), not through bpp-frontend's nginx proxy.
        name  = "BPP_API_URL"
        value = google_cloud_run_v2_service.bpp.uri
      }
      env {
        name  = "ZITADEL_ISSUER"
        value = "https://${var.zitadel_domain}"
      }
      env {
        # Placeholder until scripts/setup-zitadel-cloud.sh has run against
        # the deployed Zitadel -- see the comment on var.zitadel_client_id.
        name  = "ZITADEL_CLIENT_ID"
        value = var.zitadel_client_id
      }
    }
  }

  lifecycle {
    ignore_changes = [template[0].containers[0].image]
  }
}
