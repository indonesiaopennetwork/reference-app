locals {
  registry = "${var.region}-docker.pkg.dev/${var.project_id}/${var.artifact_registry_repo_id}"

  images = {
    bap                  = "${local.registry}/bap-application:${var.image_tag}"
    bap_migrate          = "${local.registry}/bap-application-migrate:${var.image_tag}"
    bpp                  = "${local.registry}/bpp-application:${var.image_tag}"
    bpp_migrate          = "${local.registry}/bpp-application-migrate:${var.image_tag}"
    onix_bap             = "${local.registry}/onix-bap:${var.image_tag}"
    onix_bpp             = "${local.registry}/onix-bpp:${var.image_tag}"
    onix_catalog_publish = "${local.registry}/onix-catalog-publish:${var.image_tag}"
    dedi_static_server   = "${local.registry}/dedi-static-server:${var.image_tag}"
    discovery_service    = "${local.registry}/discovery-service:${var.image_tag}"
    landing_page         = "${local.registry}/landing-page:${var.image_tag}"
    ion_landing_page     = "${local.registry}/ion-landing-page:${var.image_tag}"
    otel_collector       = "${local.registry}/otel-collector:${var.image_tag}"
  }

  # Mirrored into this project's own Artifact Registry (local.registry),
  # not pulled directly from gcr.io/ghcr.io — Cloud Run rejects updating a
  # multi-container service (this one: zitadel + cloudsql-proxy) whose
  # containers' images come from different external registries ("Mixing
  # ghcr.io container images with images from other registries, no-build,
  # or Cloud Build is not supported"). The original `terraform apply` that
  # created this service got away with it once; every update since has
  # failed this check. Re-mirror and bump the tag here when upgrading:
  #   docker pull gcr.io/cloud-sql-connectors/cloud-sql-proxy:<tag>
  #   docker tag  gcr.io/cloud-sql-connectors/cloud-sql-proxy:<tag> ${local.registry}/cloudsql-proxy:<tag>
  #   docker push ${local.registry}/cloudsql-proxy:<tag>
  cloudsql_proxy_image = "${local.registry}/cloudsql-proxy:2.14.2"

  # Off-the-shelf upstream image, mirrored into Artifact Registry for the
  # same reason as cloudsql_proxy_image above — not built/pushed by this
  # repo (see scripts/build-and-push.sh, which has no zitadel entry), so
  # it's pinned here rather than in the registry map. Upgrading means
  # re-mirroring the new tag the same way (see cloudsql_proxy_image's
  # comment) before bumping this and re-applying.
  zitadel_image = "${local.registry}/zitadel:v4.17.3"
}
