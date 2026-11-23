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

# The buyer (cn/) and seller (pn/) apps' own env-config.js -- the same
# content their old per-app containers' env.sh wrote (see
# bap-frontend/env.sh, bpp-frontend/env.sh, still used locally by
# docker-compose). Their static files come from the frontends GCS bucket,
# but this file stays local so runtime URLs remain owned by Terraform (see
# terraform/cloud-run-ion-landing-page.tf) rather than baked into an upload.
# Served by nginx.conf.template's /tsp/remiges/{cn,pn}/env-config.js blocks.
mkdir -p /usr/share/nginx/env-config-out/cn /usr/share/nginx/env-config-out/pn
cat > /usr/share/nginx/env-config-out/cn/env-config.js <<EOF
window.__env__ = {
  API_BASE: "${API_BASE:-/api/v1}",
  BPP_ADMIN_URL: "${BPP_FRONTEND_URL:-}",
  LANDING_PAGE_URL: "${LANDING_PAGE_URL:-}"
};
EOF
cat > /usr/share/nginx/env-config-out/pn/env-config.js <<EOF
window.__env__ = {
  API_BASE: "${API_BASE:-/api/v1}",
  BAP_URL: "${BAP_FRONTEND_URL:-}",
  LANDING_PAGE_URL: "${LANDING_PAGE_URL:-}"
};
EOF
