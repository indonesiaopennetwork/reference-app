#!/usr/bin/env bash
# Brands Zitadel's hosted login page (/ui/login) to match the BAP / BPP apps:
# dark navy background, cyan buttons, the ION logo and icon, DM Sans, and no
# "Powered by ZITADEL". It sets the instance's default label policy, so it
# applies to every organization, then activates it.
#
# Called by scripts/setup-zitadel.sh (local) and setup-zitadel-cloud.sh
# (cloud); can also be run on its own:
#   BASE_URL=http://localhost:8090 PAT="$(cat .zitadel-machinekey/pat.txt)" ./scripts/zitadel-branding.sh
# Safe to re-run: every call replaces what was set before.
#
# The images and font are in scripts/zitadel-branding/ (DM Sans is under the
# SIL Open Font License, see OFL.txt there).

set -euo pipefail

: "${BASE_URL:?set BASE_URL to the Zitadel URL}"
: "${PAT:?set PAT to a token with IAM_OWNER}"
ASSETS="$(cd "$(dirname "$0")" && pwd)/zitadel-branding"

# The apps' palette: background #080d1e, accent #00b8e6, errors rose-500,
# text slate-200. The login page is always dark, like the apps, so the
# light-theme colours are set to the same values.
POLICY='{
  "primaryColor": "#00b8e6",      "primaryColorDark": "#00b8e6",
  "backgroundColor": "#080d1e",   "backgroundColorDark": "#080d1e",
  "warnColor": "#f43f5e",         "warnColorDark": "#f43f5e",
  "fontColor": "#e2e8f0",         "fontColorDark": "#e2e8f0",
  "hideLoginNameSuffix": true,
  "disableWatermark": true,
  "themeMode": "THEME_MODE_DARK"
}'

echo "Branding the login page..."
# Zitadel answers 400 "has not been changed" when the policy already has
# these values (a re-run); that counts as done.
RESP="$(curl -sS -X PUT "${BASE_URL}/admin/v1/policies/label" \
  -H "Authorization: Bearer ${PAT}" -H "Content-Type: application/json" \
  -d "$POLICY" -w '\n%{http_code}')"
CODE="${RESP##*$'\n'}"
if [ "${CODE:0:1}" != "2" ] && ! grep -q "has not been changed" <<<"$RESP"; then
  echo "ERROR: updating the label policy failed (HTTP ${CODE}): ${RESP%$'\n'*}" >&2
  exit 1
fi

upload() {
  # upload <asset path> <file>
  curl -sf -X POST "${BASE_URL}/assets/v1/instance/policy/label/$1" \
    -H "Authorization: Bearer ${PAT}" -F "file=@${ASSETS}/$2" >/dev/null
}
upload logo/dark logo-dark.png
upload logo logo-light.png
upload icon/dark icon.png
upload icon icon.png
upload font DMSans.ttf

# Changes to the label policy stay in preview until activated.
curl -sf -X POST "${BASE_URL}/admin/v1/policies/label/_activate" \
  -H "Authorization: Bearer ${PAT}" -H "Content-Type: application/json" -d '{}' >/dev/null
echo "Login page branded and activated."
