#!/bin/sh
# Runs automatically on every nginx container start via /docker-entrypoint.d/.
# Exposes runtime config to the browser as window.__env__ so the same built
# page works locally and in the cloud without rebuilding (see index.html's
# launchPartnerDemo()).
#
# Written OUTSIDE /usr/share/nginx/html on purpose: in the cloud that path
# is a read-only GCS bucket mount (see terraform/cloud-run-ion-landing-page.tf),
# so writing there would fail. nginx.conf serves /env-config.js from this
# separate path instead.
mkdir -p /usr/share/nginx/env-config-out
cat > /usr/share/nginx/env-config-out/env-config.js <<EOF
window.__env__ = {
  LANDING_PAGE_URL: "${LANDING_PAGE_URL:-}"
};
EOF
