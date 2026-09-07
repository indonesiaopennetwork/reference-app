#!/bin/sh
cat > /usr/share/nginx/html/env-config.js <<EOF
window.__env__ = {
  API_BASE: "${API_BASE:-/api/v1}",
  BAP_URL: "${BAP_URL}"
};
EOF
