# Self-hosted Zitadel (OIDC provider for seller login on bpp and buyer login
# on bap — see bpp-application/internal/auth and bap-application/internal/auth).
# Mirrors docker-compose.yml's zitadel service as closely as Cloud Run
# allows; see inline notes below for where Cloud Run genuinely differs.
#
# No "internal URL" distinct from the public issuer, unlike docker-compose:
# every Cloud Run service in this deployment uses INGRESS_TRAFFIC_ALL and
# calls each other via their normal public .uri (see e.g. bpp calling
# onix_catalog_publish.uri directly) — there's no docker-network-style
# "container can't reach its own host-published port" problem here. bap/bpp
# just get ZITADEL_ISSUER = https://${var.zitadel_domain}; their config.go
# already defaults ZitadelInternalURL to equal that when unset.
#
# Postgres connection uses discrete ZITADEL_DATABASE_POSTGRES_* fields, not
# docker-compose's single ZITADEL_DATABASE_POSTGRES_DSN — Secret Manager
# injects one value per env var, and Cloud Run can't compose a literal
# prefix + a secret reference into one string within a single env block.
# Reuses the trade user for both the regular and admin connections (see the
# comment on google_sql_database.zitadel in sql.tf for why that should work
# and the fallback if it doesn't) — there's no separate Postgres superuser
# handed out on Cloud SQL the way local dev's raw `postgres` role is.
#
# The --masterkey CLI flag docker-compose uses can't carry a Secret-Manager
# value (Cloud Run doesn't support secret injection into `args`), so this
# uses --masterkeyFromEnv instead, reading ZITADEL_MASTERKEY as an env var —
# confirmed supported by running `--help` against the pinned image.
#
# The first-boot automation machine user's PAT (ZITADEL_FIRSTINSTANCE_PATPATH)
# needs somewhere persistent to land, since Cloud Run containers are
# otherwise stateless — mounted via a GCS volume (gen2 execution
# environment), same mechanism cloud-run-onix-catalog-publish.tf already
# uses for its own GCS mount. scripts/setup-zitadel-cloud.sh reads it from
# there afterward.

resource "google_cloud_run_v2_service" "zitadel" {
  name                = "zitadel"
  project             = var.project_id
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false
  depends_on = [
    google_project_service.apis,
    google_compute_subnetwork_iam_member.network_user,
    google_secret_manager_secret_iam_member.accessor,
    google_sql_database.zitadel,
    google_storage_bucket_iam_member.zitadel_machinekey_readwrite,
    # The env blocks below reference the secret CONTAINER
    # (google_secret_manager_secret.this[...]), not these VERSION
    # resources, so Terraform can't infer this ordering on its own --
    # without it, a targeted apply (see README step 6b) can create this
    # service before any of these three secrets has a value, and Cloud Run
    # rejects it ("secret .../versions/latest was not found").
    google_secret_manager_secret_version.postgres_password,
    google_secret_manager_secret_version.zitadel_masterkey,
    google_secret_manager_secret_version.zitadel_admin_password,
    # Zitadel connects to Postgres AS this user (ZITADEL_DATABASE_POSTGRES_
    # USER_USERNAME/ADMIN_USERNAME below are both var.db_user) -- without
    # this, a targeted apply can create zitadel before google_sql_user.trade
    # has ever been applied at all, or before its password matches what's
    # in the postgres-password secret, and Postgres rejects the connection
    # ("password authentication failed for user \"trade\"").
    google_sql_user.trade,
  ]

  template {
    service_account       = google_service_account.cloud_run_sa.email
    execution_environment = "EXECUTION_ENVIRONMENT_GEN2"

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

    volumes {
      name = "machinekey"
      gcs {
        bucket    = google_storage_bucket.zitadel_machinekey.name
        read_only = false
      }
    }

    containers {
      name  = "zitadel"
      image = local.zitadel_image
      args  = ["start-from-init", "--masterkeyFromEnv"]

      depends_on = ["cloudsql-proxy"]

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "1Gi"
        }
      }

      volume_mounts {
        name       = "machinekey"
        mount_path = "/machinekey"
      }

      # start-from-init does real first-boot work (initializing the full
      # event-store schema and every projection table in Postgres, over
      # the cloudsql-proxy sidecar) before it ever opens the listening
      # port -- this can genuinely take several minutes on a fresh
      # database. 150s (the previous 5s*30 budget) was too tight and Cloud
      # Run killed the revision as "failed to start" before it got there.
      startup_probe {
        tcp_socket {
          port = 8080
        }
        period_seconds    = 10
        timeout_seconds   = 3
        failure_threshold = 60
      }

      env {
        name  = "ZITADEL_PORT"
        value = "8080"
      }
      env {
        name  = "ZITADEL_EXTERNALDOMAIN"
        value = var.zitadel_domain
      }
      env {
        name  = "ZITADEL_EXTERNALPORT"
        value = "443"
      }
      env {
        name  = "ZITADEL_EXTERNALSECURE"
        value = "true"
      }
      env {
        name  = "ZITADEL_TLS_ENABLED"
        value = "false"
      }
      env {
        name  = "ZITADEL_DEFAULTINSTANCE_FEATURES_LOGINV2_REQUIRED"
        value = "false"
      }
      env {
        name = "ZITADEL_MASTERKEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["zitadel-masterkey"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "ZITADEL_DATABASE_POSTGRES_HOST"
        value = "127.0.0.1"
      }
      env {
        name  = "ZITADEL_DATABASE_POSTGRES_PORT"
        value = "5432"
      }
      env {
        name  = "ZITADEL_DATABASE_POSTGRES_DATABASE"
        value = var.db_name_zitadel
      }
      env {
        name  = "ZITADEL_DATABASE_POSTGRES_USER_USERNAME"
        value = var.db_user
      }
      env {
        name = "ZITADEL_DATABASE_POSTGRES_USER_PASSWORD"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["postgres-password"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "ZITADEL_DATABASE_POSTGRES_USER_SSL_MODE"
        value = "disable"
      }
      env {
        name  = "ZITADEL_DATABASE_POSTGRES_ADMIN_USERNAME"
        value = var.db_user
      }
      env {
        name = "ZITADEL_DATABASE_POSTGRES_ADMIN_PASSWORD"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["postgres-password"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "ZITADEL_DATABASE_POSTGRES_ADMIN_SSL_MODE"
        value = "disable"
      }
      env {
        name  = "ZITADEL_FIRSTINSTANCE_ORG_NAME"
        value = var.zitadel_org_name
      }
      env {
        name  = "ZITADEL_FIRSTINSTANCE_ORG_HUMAN_USERNAME"
        value = var.zitadel_admin_username
      }
      env {
        name  = "ZITADEL_FIRSTINSTANCE_ORG_HUMAN_EMAIL_ADDRESS"
        value = var.zitadel_admin_email
      }
      env {
        name  = "ZITADEL_FIRSTINSTANCE_ORG_HUMAN_EMAIL_VERIFIED"
        value = "true"
      }
      env {
        name = "ZITADEL_FIRSTINSTANCE_ORG_HUMAN_PASSWORD"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.this["zitadel-admin-password"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "ZITADEL_FIRSTINSTANCE_ORG_HUMAN_PASSWORDCHANGEREQUIRED"
        value = "false"
      }
      env {
        name  = "ZITADEL_FIRSTINSTANCE_ORG_MACHINE_MACHINE_USERNAME"
        value = "automation"
      }
      env {
        name  = "ZITADEL_FIRSTINSTANCE_ORG_MACHINE_MACHINE_NAME"
        value = "Reference App Automation"
      }
      env {
        name  = "ZITADEL_FIRSTINSTANCE_ORG_MACHINE_PAT_EXPIRATIONDATE"
        value = "2100-01-01T00:00:00Z"
      }
      env {
        name  = "ZITADEL_FIRSTINSTANCE_PATPATH"
        value = "/machinekey/pat.txt"
      }
    }

    containers {
      name  = "cloudsql-proxy"
      image = local.cloudsql_proxy_image
      args  = ["--structured-logs", "--address=0.0.0.0", "--port=5432", "--private-ip", google_sql_database_instance.postgres.connection_name]

      startup_probe {
        tcp_socket {
          port = 5432
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
      }
    }
  }

  # Deliberately NOT ignore_changes-ing the image here, unlike every other
  # service: Zitadel's image is a manually-bumped pinned tag (images.tf),
  # not something a task redeploy:* manages out-of-band, so a plain
  # `terraform apply` after bumping the pin SHOULD redeploy it.
}
