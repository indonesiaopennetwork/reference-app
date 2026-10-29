variable "project_id" {
  description = "GCP project ID to deploy everything into (Cloud Run, Artifact Registry, Secret Manager, VPC, Cloud SQL, Redis)."
  type        = string
  default     = "ion-sandbox-001"
}

variable "region" {
  description = "GCP region for all resources."
  type        = string
  default     = "asia-southeast2"
}

variable "image_tag" {
  description = "Tag applied to all images by scripts/build-and-push.sh (e.g. a git SHA). Terraform ignores image changes after first create — see lifecycle blocks on each Cloud Run resource."
  type        = string
  default     = "latest"
}

variable "artifact_registry_repo_id" {
  description = "Artifact Registry Docker repository ID."
  type        = string
  default     = "beckn-app"
}

# --- New VPC/subnet for Direct VPC Egress (bap/bpp/onix-bap/onix-bpp/migrate jobs) ---

variable "vpc_name" {
  description = "Name of the VPC network this module creates."
  type        = string
  default     = "beckn-app-vpc"
}

variable "subnet_name" {
  description = "Name of the subnetwork this module creates (within vpc_name)."
  type        = string
  default     = "beckn-app-subnet"
}

variable "subnet_cidr" {
  description = "CIDR range for the subnetwork. Must not overlap psa_prefix_length's auto-selected range or redis_reserved_ip_range."
  type        = string
  default     = "10.10.0.0/24"
}

variable "psa_prefix_length" {
  description = "Prefix length of the IP range reserved for Private Services Access (Cloud SQL private IP peering). Google auto-selects a non-overlapping range of this size within the VPC."
  type        = number
  default     = 20
}

# --- New Cloud SQL instance ---

variable "sql_instance_name" {
  description = "Name of the Cloud SQL Postgres instance this module creates."
  type        = string
  default     = "beckn-app-postgres"
}

variable "sql_tier" {
  description = "Cloud SQL machine tier. db-f1-micro/db-g1-small are MySQL-only and rejected for Postgres — db-custom-1-3840 (1 vCPU/3.75GB) is the cheapest valid Postgres tier."
  type        = string
  default     = "db-custom-1-3840"
}

variable "sql_disk_size_gb" {
  type    = number
  default = 10
}

variable "sql_disk_type" {
  description = "PD_HDD is cheaper than PD_SSD; fine for a sandbox workload."
  type        = string
  default     = "PD_HDD"
}

variable "sql_availability_type" {
  description = "ZONAL (no HA, cheaper) or REGIONAL. Sandbox default is ZONAL."
  type        = string
  default     = "ZONAL"
}

variable "sql_backup_enabled" {
  description = "Whether automated backups are enabled. Off by default for a sandbox — flip on if you care about not losing data."
  type        = bool
  default     = false
}

# --- New Memorystore Redis instance ---

variable "redis_instance_name" {
  description = "Name of the Memorystore Redis instance this module creates."
  type        = string
  default     = "beckn-app-redis"
}

variable "redis_tier" {
  description = "BASIC (no HA/failover, cheapest) or STANDARD_HA."
  type        = string
  default     = "BASIC"
}

variable "redis_memory_size_gb" {
  type    = number
  default = 1
}

variable "redis_reserved_ip_range" {
  description = "CIDR reserved for Memorystore's direct peering. Must not overlap subnet_cidr or the PSA range."
  type        = string
  default     = "10.20.0.0/29"
}

# --- New databases/user on the new Cloud SQL instance ---

variable "db_name_bap" {
  type    = string
  default = "trade_bap"
}

variable "db_name_bpp" {
  type    = string
  default = "trade_bpp"
}

variable "db_user" {
  type    = string
  default = "trade"
}

variable "db_name_zitadel" {
  type    = string
  default = "zitadel"
}

# --- Beckn network identity ---
# Single source of truth per side: this value appears BOTH in context.bapId/
# bppId (every outbound message) AND as onix-bap/onix-bpp's own keyManager
# networkParticipant. These two uses MUST always match — onix signs a
# message using the keyset registered for whatever subscriber_id the message
# itself claims to be from, so any mismatch here fails signing with "keyset
# not found". No default on purpose: this deployment uses a separate
# registered identity from local dev / other deployments, and leaving a
# hardcoded default around is exactly how that kind of drift happens
# silently. terraform plan fails loudly until terraform.tfvars supplies a
# real value.

variable "bap_id" {
  type = string
}

variable "bpp_id" {
  type = string
}

variable "network_id" {
  type = string
}

# cds_discover_url is an OPTIONAL override, unlike bpp's CDS_PUBLISH_URL
# (which has no equivalent variable at all). By default (unset/empty),
# bap's CDS_DISCOVER_URL is computed directly from the discovery-service
# Cloud Run service this module deploys (see cloud-run-discovery-service.tf
# and cloud-run-bap.tf) — a same-project, same-operator service. Set this
# to point bap at a different, externally-hosted discovery service instead
# (e.g. if you later decide to run discovery-service somewhere else, or use
# someone else's).
variable "cds_discover_url" {
  type    = string
  default = ""
}

# Where the otel-collector sidecars on onix-bap/onix-bpp forward filtered
# onix telemetry (OTLP/HTTP) -- the same NETWORK_COLLECTOR_ENDPOINT
# docker-compose.yml passes to otel-collector-bap/otel-collector-bpp. See
# ../otel-collector/config.yaml.
variable "network_collector_endpoint" {
  type    = string
  default = "https://netcol.iontest.in"
}

variable "discover_method" {
  description = "bap-application's DISCOVER_METHOD: \"GET\" (default) calls discovery-service directly via CDS_DISCOVER_URL; \"POST\" routes discover through onix-bap as a real async Beckn action (requires discovery-service to have a real registered network identity, not the ephemeral-key default)."
  type        = string
  default     = "GET"
}

# discovery_service_subscriber_id/uri are optional overrides for
# discovery-service's own outbound on_discover signing identity (its
# SUBSCRIBER_ID/SUBSCRIBER_URI). Left unset (the default), it borrows
# bap's own onix-bap identity (var.bap_id / onix-bap's Cloud Run URL) --
# the same borrowed-identity pattern local dev's .env already uses
# (DS_ID/DS_URI), which works because onix-bap's registry lookup resolves
# that subscriber_id to a genuinely registered key. Set both once
# discovery-service has its own real, independently-registered network
# identity -- the actual key material (discovery-service-subscriber-key-id/
# discovery-service-signing-private-key secrets, see secrets.tf) is
# reseeded separately via scripts/seed-secrets.sh, not through these vars.
variable "discovery_service_subscriber_id" {
  description = "Optional override for discovery-service's own outbound-signing SUBSCRIBER_ID. Leave unset to default to bap's own onix-bap identity (var.bap_id)."
  type        = string
  default     = ""
}

variable "discovery_service_subscriber_uri" {
  description = "Optional override for discovery-service's own SUBSCRIBER_URI, paired with discovery_service_subscriber_id. Leave unset to default to bap's own public domain (var.bap_domain)."
  type        = string
  default     = ""
}

# --- bpp / DeDi static-file custom domain ---
# The domain must already be verified in Search Console under the account
# that owns this project before google_cloud_run_domain_mapping will apply
# successfully — see domain-mapping.tf. bpp has no domain of its own (see
# cloud-run-dedi-static.tf's nginx routing) — this is effectively bpp's
# public domain, which also happens to serve the DeDi discovery chain.

variable "bpp_domain" {
  description = "Apex domain bpp's public API is served on, and the DeDi discovery chain alongside it (e.g. ayushmatha.in) — see dedi-static-server's nginx routing."
  type        = string
}

variable "bpp_domain_www" {
  description = "www subdomain variant, mapped to the same dedi-static-server service."
  type        = string
}

variable "bap_domain" {
  description = "Custom domain for the bap backend."
  type        = string
}

variable "bap_frontend_domain" {
  description = "Custom domain for the bap-frontend (buyer) app."
  type        = string
}

variable "bpp_frontend_domain" {
  description = "Custom domain for the bpp-frontend (seller) app."
  type        = string
}

variable "landing_page_domain" {
  description = "Custom domain for the landing page."
  type        = string
}

variable "ion_landing_page_domain" {
  description = "Custom domain for ion-landing-page (the front page)."
  type        = string
}

variable "zitadel_domain" {
  description = "Custom domain for the self-hosted Zitadel instance (see cloud-run-zitadel.tf) — this becomes ZITADEL_EXTERNALDOMAIN and the issuer bap/bpp/landing-page verify tokens against. Same Search Console verification requirement as the other domains above."
  type        = string
}

# --- Zitadel (self-hosted login, see cloud-run-zitadel.tf) ---
# Admin username/email are required with no default, same reasoning as
# bap_id/bpp_id above: this is a real account an operator should choose, not
# something safe to leave defaulted. The masterkey and admin password are
# NOT variables — they're self-generated by Terraform (random_password in
# sql.tf), same treatment as the Postgres password.

variable "zitadel_admin_username" {
  description = "Username for the Zitadel admin account created on first boot."
  type        = string
}

variable "zitadel_admin_email" {
  description = "Email for the Zitadel admin account created on first boot."
  type        = string
}

variable "zitadel_org_name" {
  description = "Name of the Zitadel organization created on first boot."
  type        = string
  default     = "reference-app"
}

# landing-page's OIDC client ID. Genuinely unknown until AFTER Zitadel is up
# and scripts/setup-zitadel-cloud.sh has created the Project+Application
# against it — same bootstrap-order problem as bap_uri/bpp_uri
# (cloud-run-wiring.tf), but one-directional and simpler: rather than a
# null_resource patch step, this just follows the same "apply twice" pattern
# already documented for DB migrations (README steps 7-9). Placeholder
# default lets the FIRST apply succeed (landing-page's login link just won't
# work yet); set the real value in terraform.tfvars after running the setup
# script, then apply again.
variable "zitadel_client_id" {
  description = "landing-page's Zitadel OIDC client ID. Placeholder until scripts/setup-zitadel-cloud.sh has run — see its output for the real value."
  type        = string
  default     = "placeholder"
}

# --- Secrets ---
# No variables here on purpose. Every secret value (Postgres password aside,
# which Terraform generates itself) is created as an empty Secret Manager
# container by Terraform, then seeded once with its real value via
# scripts/seed-secrets.sh — see terraform/README.md. This keeps plaintext
# secrets out of every Terraform-visible file, including .tfvars and state.

# --- Settlement and reconciliation ---
# The defaults are the apps' own (config.go), except settlement_window and
# reconcile_wait_for_settlement_window, which match the local .env testing
# setup (a 2-minute window, sending before it). Local docker-compose takes
# the same settings from .env.

variable "settlement_basis" {
  description = "bpp-application's SETTLEMENT_BASIS, the default payout basis for an offer published without its own policies.settlement: the event the payout clock starts from -- AFTER_DELIVERY, AFTER_SHIPMENT, ON_CONFIRMATION or AFTER_RETURN_WINDOW. Also decides when a delivered contract becomes COMPLETE."
  type        = string
  default     = "AFTER_DELIVERY"
}

variable "settlement_window" {
  description = "bpp-application's SETTLEMENT_WINDOW, the default for an offer without its own: ISO 8601 duration after the basis event when a contract falls due for reconciliation. PT2M (testing) like the local .env; the app's own default, for production, is P1D."
  type        = string
  default     = "PT2M"
}

variable "reconcile_wait_for_settlement_window" {
  description = "RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW for bap and bpp: true sends a contract to /reconcile only once its settlement window has passed; false (testing, like the local .env) lets every paid order be sent right away. The BAP rejects early contracts either way. Set true for production."
  type        = bool
  default     = false
}

variable "commission_type" {
  description = "bpp-application's COMMISSION_TYPE, the default commission (finder fee) the seller pays the buyer app for an offer published without its own: PERCENT or AMOUNT (per unit sold)."
  type        = string
  default     = "PERCENT"
}

variable "commission_value" {
  description = "bpp-application's COMMISSION_VALUE: the percentage (PERCENT) or fixed amount per unit in IDR (AMOUNT)."
  type        = number
  default     = 3
}

variable "commission_applies_to" {
  description = "bpp-application's COMMISSION_APPLIES_TO for a PERCENT commission: ITEM_TOTAL (without tax) or ORDER_TOTAL (with tax)."
  type        = string
  default     = "ITEM_TOTAL"
}

# Payout accounts (settlementAccounts): where the PJP pays the BAP the commission
# sellers pay it and the BPP its order proceeds. The defaults are demo accounts.
variable "bap_settlement_bank_code" {
  type    = string
  default = "MANDIRI"
}

variable "bap_settlement_account_number" {
  type    = string
  default = "1370000000001"
}

variable "bap_settlement_account_name" {
  type    = string
  default = ""
}

variable "bpp_settlement_bank_code" {
  type    = string
  default = "BCA"
}

variable "bpp_settlement_account_number" {
  type    = string
  default = "8730000000001"
}

variable "bpp_settlement_account_name" {
  type    = string
  default = ""
}
