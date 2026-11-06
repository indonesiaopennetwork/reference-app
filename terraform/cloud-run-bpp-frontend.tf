resource "google_cloud_run_v2_service" "bpp_frontend" {
  name                = "bpp-frontend"
  project             = var.project_id
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false
  depends_on          = [google_project_service.apis]

  template {
    service_account = google_service_account.cloud_run_sa.email

    scaling {
      # 1, not 0: see the scaling comment on bap/onix-bap -- keeps the
      # first page load after idle from paying a cold start too.
      min_instance_count = 1
      max_instance_count = 3
    }

    containers {
      name  = "bpp-frontend"
      image = local.images.bpp_frontend

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
        name  = "BPP_URL"
        value = "https://${var.bpp_domain}"
      }
      env {
        name  = "NGINX_RESOLVER"
        value = "8.8.8.8 8.8.4.4"
      }
      env {
        name  = "BAP_URL"
        value = local.bap_frontend_url
      }
      env {
        name  = "LANDING_PAGE_URL"
        value = local.landing_page_url
      }
    }
  }

  lifecycle {
    ignore_changes = [template[0].containers[0].image]
  }
}
