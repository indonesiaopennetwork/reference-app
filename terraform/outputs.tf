output "project_id" {
  value       = var.project_id
  description = "Bare passthrough of var.project_id — lets Taskfile.yaml's redeploy:* tasks self-derive it via terraform output, no manual entry needed."
}

output "region" {
  value       = var.region
  description = "Bare passthrough of var.region — same reason as project_id above."
}

output "bap_frontend_url" {
  value       = google_cloud_run_v2_service.bap_frontend.uri
  description = "Public URL — open this to use the BAP (buyer) app."
}

output "bpp_frontend_url" {
  value       = google_cloud_run_v2_service.bpp_frontend.uri
  description = "Public URL — open this to use the BPP (seller/admin) app."
}

output "bap_url" {
  value = google_cloud_run_v2_service.bap.uri
}

output "bpp_url" {
  value = google_cloud_run_v2_service.bpp.uri
}

output "onix_bap_url" {
  value = google_cloud_run_v2_service.onix_bap.uri
}

output "onix_bpp_url" {
  value = google_cloud_run_v2_service.onix_bpp.uri
}

output "onix_catalog_publish_url" {
  value = google_cloud_run_v2_service.onix_catalog_publish.uri
}

output "dedi_static_bucket" {
  value       = google_storage_bucket.dedi_static.name
  description = "Upload the rarely-changing bootstrap files here directly, e.g.: gsutil cp dedi.index.json gs://<this>/.well-known/dedi.index.json"
}

output "dedi_static_server_url" {
  value = google_cloud_run_v2_service.dedi_static.uri
}

output "landing_page_url" {
  value       = google_cloud_run_v2_service.landing_page.uri
  description = "Public URL — the entry-point page with buttons to the buyer and seller apps."
}

output "zitadel_url" {
  value = google_cloud_run_v2_service.zitadel.uri
}

output "bpp_domain_url" {
  value       = "https://${var.bpp_domain}"
  description = "Custom domain URL for the DeDi discovery chain / dedi-static-server (also serves bpp's /api/v1/* — see README)."
}

output "bpp_domain_www_url" {
  value = "https://${var.bpp_domain_www}"
}

output "bap_domain_url" {
  value       = "https://${var.bap_domain}"
  description = "Custom domain URL for the bap backend."
}

output "bap_frontend_domain_url" {
  value       = "https://${var.bap_frontend_domain}"
  description = "Custom domain URL for the bap-frontend (buyer) app."
}

output "bpp_frontend_domain_url" {
  value       = "https://${var.bpp_frontend_domain}"
  description = "Custom domain URL for the bpp-frontend (seller) app."
}

output "landing_page_domain_url" {
  value       = "https://${var.landing_page_domain}"
  description = "Custom domain URL for the landing page."
}

output "zitadel_domain_url" {
  value       = "https://${var.zitadel_domain}"
  description = "Custom domain URL for Zitadel."
}

output "bpp_domain_dns_records" {
  value       = google_cloud_run_domain_mapping.dedi_apex.status[0].resource_records
  description = "DNS records to create at your registrar for var.bpp_domain."
}

output "bpp_domain_www_dns_records" {
  value       = google_cloud_run_domain_mapping.dedi_www.status[0].resource_records
  description = "DNS records to create at your registrar for var.bpp_domain_www."
}

output "bap_domain_dns_records" {
  value       = google_cloud_run_domain_mapping.bap.status[0].resource_records
  description = "DNS records to create at your registrar for var.bap_domain."
}

output "bap_frontend_domain_dns_records" {
  value       = google_cloud_run_domain_mapping.bap_frontend.status[0].resource_records
  description = "DNS records to create at your registrar for var.bap_frontend_domain."
}

output "bpp_frontend_domain_dns_records" {
  value       = google_cloud_run_domain_mapping.bpp_frontend.status[0].resource_records
  description = "DNS records to create at your registrar for var.bpp_frontend_domain."
}

output "landing_page_domain_dns_records" {
  value       = google_cloud_run_domain_mapping.landing_page.status[0].resource_records
  description = "DNS records to create at your registrar for var.landing_page_domain."
}

output "zitadel_domain_dns_records" {
  value       = google_cloud_run_domain_mapping.zitadel.status[0].resource_records
  description = "DNS records to create at your registrar for var.zitadel_domain."
}

output "zitadel_machinekey_bucket" {
  value       = google_storage_bucket.zitadel_machinekey.name
  description = "Read the first-boot automation PAT from here, e.g.: gsutil cat gs://<this>/pat.txt -- used by scripts/setup-zitadel-cloud.sh."
}

output "artifact_registry_repo" {
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${var.artifact_registry_repo_id}"
  description = "Push images here — see scripts/build-and-push.sh."
}

output "cloud_sql_connection_name" {
  value = google_sql_database_instance.postgres.connection_name
}

output "redis_host" {
  value = google_redis_instance.redis.host
}

output "vpc_name" {
  value = google_compute_network.vpc.name
}

output "subnet_name" {
  value = google_compute_subnetwork.subnet.name
}
