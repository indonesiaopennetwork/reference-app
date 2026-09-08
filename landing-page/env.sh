#!/bin/sh
# Runs automatically on every nginx container start via /docker-entrypoint.d/.
# Exposes runtime config to the browser as window.__env__ so the same built
# bundle works locally and in the cloud without rebuilding (see src/config.js).
cat > /usr/share/nginx/html/env-config.js <<EOF
window.__env__ = {
  BAP_URL: "${BAP_URL:-}",
  BPP_URL: "${BPP_URL:-}",
  BPP_API_URL: "${BPP_API_URL:-}",
  ZITADEL_ISSUER: "${ZITADEL_ISSUER:-}",
  ZITADEL_CLIENT_ID: "${ZITADEL_CLIENT_ID:-}"
};
EOF
