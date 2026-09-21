# New Cloud SQL Postgres instance, owned by this project — see variables.tf
# for sizing (sql_tier etc, sandbox-appropriate defaults).

resource "google_sql_database_instance" "postgres" {
  name                = var.sql_instance_name
  project             = var.project_id
  region              = var.region
  database_version    = "POSTGRES_15"
  deletion_protection = false

  settings {
    tier              = var.sql_tier
    availability_type = var.sql_availability_type
    disk_type         = var.sql_disk_type
    disk_size         = var.sql_disk_size_gb

    ip_configuration {
      ipv4_enabled                                  = false
      private_network                               = google_compute_network.vpc.id
      enable_private_path_for_google_cloud_services = true
    }

    backup_configuration {
      enabled = var.sql_backup_enabled
    }
  }

  depends_on = [google_service_networking_connection.psa]
}

resource "google_sql_database" "trade_bap" {
  name     = var.db_name_bap
  project  = var.project_id
  instance = google_sql_database_instance.postgres.name
}

resource "google_sql_database" "trade_bpp" {
  name     = var.db_name_bpp
  project  = var.project_id
  instance = google_sql_database_instance.postgres.name
}

# Zitadel gets its own database on the same instance, but reuses the trade
# user below rather than a dedicated one — bap/bpp already prove that user
# has sufficient DDL rights on this instance (their own tern migrations
# create full schemas as trade), and Cloud SQL Postgres users are granted
# the instance-wide cloudsqlsuperuser role, so this should extend cleanly to
# a third database. Verify at first apply (see cloud-run-zitadel.tf); if
# Zitadel's own schema setup fails with a permissions error, the fallback is
# `ALTER DATABASE zitadel OWNER TO trade;` via Cloud SQL Studio.
resource "google_sql_database" "zitadel" {
  name     = var.db_name_zitadel
  project  = var.project_id
  instance = google_sql_database_instance.postgres.name
}

resource "random_password" "postgres" {
  length  = 32
  special = false
}

resource "google_sql_user" "trade" {
  name     = var.db_user
  project  = var.project_id
  instance = google_sql_database_instance.postgres.name
  password = random_password.postgres.result
}

# Self-generated, like postgres above — see secrets.tf's
# google_secret_manager_secret_version resources for where these land.
resource "random_password" "zitadel_masterkey" {
  length  = 32
  special = false
}

# Unlike postgres/masterkey above, this one IS validated -- Zitadel's
# default password complexity policy rejects
# ZITADEL_FIRSTINSTANCE_ORG_HUMAN_PASSWORD unless it has at least one of
# each character class. min_* guarantees that deterministically rather
# than leaving it to chance across 32 random characters.
resource "random_password" "zitadel_admin_password" {
  length      = 32
  special     = true
  min_lower   = 1
  min_upper   = 1
  min_numeric = 1
  min_special = 1
}
