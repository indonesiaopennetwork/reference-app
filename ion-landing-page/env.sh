#!/bin/sh
# Runs automatically on every nginx container start via /docker-entrypoint.d/.
# Exposes runtime config to the browser as window.__env__ so the same built
# page works locally and in the cloud without rebuilding (see index.html's
# launchPartnerDemo()).
cat > /usr/share/nginx/html/env-config.js <<EOF
window.__env__ = {
  LANDING_PAGE_URL: "${LANDING_PAGE_URL:-}"
};
EOF
