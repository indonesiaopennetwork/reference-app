# NOTE: onix-bap <-> bap is a mutual reference — see cloud-run-wiring.tf,
# which patches BAP_URL (used by onix-bap's receiver-webhook routing
# template) once every service's URL is known.

resource "google_cloud_run_v2_service" "onix_bap" {
  name                = "beckn-onix-bap"
  project             = var.project_id
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false
  depends_on          = [google_project_service.apis, google_compute_subnetwork_iam_member.network_user, google_secret_manager_secret_iam_member.accessor, google_redis_instance.redis]

  template {
    service_account = google_service_account.cloud_run_sa.email

    scaling {
      min_instance_count = 0
      max_instance_count = 3
    }

    vpc_access {
      network_interfaces {
        network    = google_compute_network.vpc.id
        subnetwork = google_compute_subnetwork.subnet.id
      }
      egress = "PRIVATE_RANGES_ONLY"
    }

    containers {
      name  = "onix-bap"
      image = local.images.onix_bap

      depends_on = ["otel-collector"]

      ports {
        container_port = 8081
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
        # CPU stays allocated between requests, so the otel-collector
        # sidecar can flush its batches (up to 10s after a request) instead
        # of being throttled until the next request arrives.
        cpu_idle = false
      }

      env {
        name  = "REDIS_ADDR"
        value = "${google_redis_instance.redis.host}:${google_redis_instance.redis.port}"
      }
      # The otel-collector sidecar's OTLP receiver is plaintext-only (same
      # instance, localhost-only traffic) -- without this, the OTel SDK
      # defaults to TLS and every export fails the handshake.
      env {
        name  = "OTEL_EXPORTER_OTLP_INSECURE"
        value = "true"
      }
      env {
        name  = "NETWORK_PARTICIPANT"
        value = var.bap_id
      }
      env {
        name = "BAP_ONIX_KEY_ID"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["bap-onix-key-id"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "BAP_ONIX_SIGNING_PRIVATE_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["bap-onix-signing-private-key"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "BAP_ONIX_SIGNING_PUBLIC_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["bap-onix-signing-public-key"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "BAP_ONIX_ENCR_PRIVATE_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["bap-onix-encr-private-key"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "BAP_ONIX_ENCR_PUBLIC_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["bap-onix-encr-public-key"].secret_id
            version = "latest"
          }
        }
      }
      # Bootstrap placeholder, patched to the real value post-apply by
      # cloud-run-wiring.tf. NOT blank: if left empty, entrypoint.sh's
      # envsubst would render local-simple-routing-BAPReceiver.yaml's target
      # url as the scheme-less, host-less string "/api/webhook" — whether the
      # vendor onix binary eagerly validates/rejects that at startup (versus
      # only when routing a request) is unverified from outside the image,
      # so use a syntactically-valid placeholder rather than risk it.
      env {
        name  = "BAP_URL"
        value = "https://placeholder.invalid"
      }
      env {
        # Base URL only (no /discover suffix) -- onix-bap's own
        # local-simple-routing-BAPCaller.yaml.tmpl appends the action name
        # itself when routing a "url"-targeted action. Drives the same
        # cds_discover_url override bap-application's own CDS_DISCOVER_URL
        # uses (see cloud-run-bap.tf), just trimmed to a base when that
        # override is a full .../discover URL.
        name  = "DISCOVERY_SERVICE_URL"
        value = var.cds_discover_url != "" ? trimsuffix(var.cds_discover_url, "/discover") : google_cloud_run_v2_service.discovery_service.uri
      }
    }

    # otel-collector sidecar — onix-bap's otelsetup plugin ships telemetry to
    # it over localhost (Cloud Run v2 sidecars share a network namespace),
    # which then filters and forwards network-relevant signals to
    # var.network_collector_endpoint (netcol.staging.ion.id by default). See
    # ../otel-collector/config.yaml.
    containers {
      name  = "otel-collector"
      image = local.images.otel_collector

      env {
        name  = "NETWORK_COLLECTOR_ENDPOINT"
        value = var.network_collector_endpoint
      }

      # The collector's health_check extension (otel-collector/config.yaml).
      startup_probe {
        http_get {
          path = "/"
          port = 13133
        }
        period_seconds    = 5
        timeout_seconds   = 3
        failure_threshold = 20
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
        cpu_idle = false
      }
    }
  }

  lifecycle {
    ignore_changes = [template[0].containers[0].image]
  }
}
