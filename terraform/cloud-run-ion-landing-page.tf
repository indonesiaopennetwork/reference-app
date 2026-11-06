# Public static front-end for ion-landing-page. Proxies to the public URL
# of the GCS bucket from gcs-ion-landing-page.tf
# (https://storage.googleapis.com/<bucket>/...) -- see
# ion-landing-page/nginx.conf.template. Domain-mapped to the custom domain
# -- see domain-mapping.tf.

resource "google_cloud_run_v2_service" "ion_landing_page" {
  name                = "ion-landing-page"
  project             = var.project_id
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false
  depends_on          = [google_project_service.apis, google_storage_bucket_iam_member.ion_landing_page_public_read]

  template {
    service_account       = google_service_account.cloud_run_sa.email
    execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

    scaling {
      # 1, not 0: this is the outer proxy every request goes through
      # (demo.ion.id/tsp/remiges/...) -- see the scaling comment on
      # bap/onix-bap for why 0 is risky here too.
      min_instance_count = 1
      max_instance_count = 3
    }

    containers {
      name  = "ion-landing-page"
      image = local.images.ion_landing_page

      # Read by index.html's launchPartnerDemo() via env.sh/env-config.js.
      env {
        name  = "LANDING_PAGE_URL"
        value = local.landing_page_url
      }

      # nginx.conf.template proxies /tsp/remiges/, /tsp/remiges/cn/ and
      # /tsp/remiges/pn/ to these hosts -- see local.landing_page_url.
      env {
        name  = "LANDING_PAGE_UPSTREAM"
        value = trimprefix(google_cloud_run_v2_service.landing_page.uri, "https://")
      }
      env {
        name  = "BAP_FRONTEND_UPSTREAM"
        value = trimprefix(google_cloud_run_v2_service.bap_frontend.uri, "https://")
      }
      env {
        name  = "BPP_FRONTEND_UPSTREAM"
        value = trimprefix(google_cloud_run_v2_service.bpp_frontend.uri, "https://")
      }

      # nginx.conf.template proxy_pass's to this bucket's public URL
      # (https://storage.googleapis.com/<bucket>/...) instead of reading it
      # off a mounted volume -- see ion-landing-page/nginx.conf.template.
      env {
        name  = "ION_LANDING_PAGE_BUCKET"
        value = google_storage_bucket.ion_landing_page_static.name
      }

      ports {
        container_port = 80
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [template[0].containers[0].image]
  }
}
