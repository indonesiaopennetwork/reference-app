# Static entry-point page with buttons to bap-frontend and bpp-frontend, and
# the OIDC login flow itself (see landing-page/src/oidc.js). Plain nginx, no
# DB, no volume mount — every env var below is read by the BROWSER (this is
# a client-side SPA), so they must all be host-published, browser-reachable
# addresses, not internal hostnames — envsubst'd into index.html.template by
# entrypoint.sh at container startup (mirrors onix-bap/entrypoint.sh's
# templating pattern).

# Canonical public URLs of landing-page, bap-frontend and bpp-frontend:
# mounted under ion-landing-page's domain via its nginx (see
# ion-landing-page/nginx.conf.template). bap-frontend and bpp-frontend have
# no other URL at all -- their files live in the frontends bucket
# (gcs-frontends.tf). landing-page's own custom domain stays mapped, but
# every link and the bpp CORS origin point here -- login tokens live in
# this origin's localStorage, so links must all agree on one URL or logout
# won't clear them.
locals {
  landing_page_url    = "https://${var.ion_landing_page_domain}/tsp/remiges"
  landing_page_origin = "https://${var.ion_landing_page_domain}"
  bap_frontend_url    = "${local.landing_page_url}/cn"
  bpp_frontend_url    = "${local.landing_page_url}/pn"
}

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
      # 0: scale to zero when idle, so nothing is billed without traffic.
      # Trade-off: the first request after an idle period pays a cold start
      # (container boot plus any in-process cache warm-up -- measured at
      # 10-35s+ for the onix modules), which a flaky client network can abort
      # (net::ERR_NETWORK_CHANGED). Raise to 1 to keep an instance warm.
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
        cpu_idle = true # request-based billing
      }

      env {
        name  = "BAP_URL"
        value = local.bap_frontend_url
      }
      env {
        name  = "BPP_URL"
        value = local.bpp_frontend_url
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
