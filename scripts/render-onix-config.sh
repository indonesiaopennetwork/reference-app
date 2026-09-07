#!/usr/bin/env bash
# Renders config/local-simple-bap.yaml, config/local-simple-bpp.yaml, and
# config/catalog-publish.yaml from their .example templates, substituting
# every keyManager field from the repo root .env. Re-run any time .env's
# keys change -- this is what keeps the app-level signing key and the onix
# keyManager identity guaranteed identical (see INFRA_SETUP.md's Gotcha #1
# reference) instead of relying on careful hand-copying.
#
# Usage: ./scripts/render-onix-config.sh

set -euo pipefail

cd "$(dirname "$0")/.."

if [ ! -f .env ]; then
  echo "ERROR: .env not found. Copy .env.example to .env and fill in your registered keys first." >&2
  exit 1
fi

# Parsed line-by-line rather than `source`d -- .env values (e.g. BAP_KEY_ID's
# "subscriberId|keyId|ed25519") contain shell metacharacters like "|" that
# `source`/`.` would interpret as a pipe instead of a literal character.
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    ''|'#'*) continue ;;
  esac
  export "${line%%=*}=${line#*=}"
done < .env

# BAP_KEY_ID/BPP_KEY_ID are "<subscriberId>|<keyId>|ed25519" -- the Go apps
# need that combined string as-is for the HTTP signature header. The onix
# yaml configs need subscriberId (networkParticipant) as its own field;
# derive that here instead of duplicating it as its own .env var. keyId and
# signingPrivateKey, by contrast, come from their own dedicated
# BAP_ONIX_KEY_ID/BAP_ONIX_SIGNING_PRIVATE_KEY .env vars (see .env.example)
# rather than being derived, matching how scripts/seed-secrets.sh already
# provides them to the cloud deployment.
BAP_NETWORK_PARTICIPANT="${BAP_KEY_ID%%|*}"
BPP_NETWORK_PARTICIPANT="${BPP_KEY_ID%%|*}"
export BAP_NETWORK_PARTICIPANT BPP_NETWORK_PARTICIPANT

require() {
  local var_name="$1" value="${!1:-}"
  if [ -z "$value" ] || [[ "$value" == REPLACE_WITH_* ]]; then
    echo "ERROR: ${var_name} is empty or still a REPLACE_WITH_* placeholder in .env." >&2
    exit 1
  fi
}

for v in BAP_NETWORK_PARTICIPANT BAP_ONIX_KEY_ID BAP_ONIX_SIGNING_PRIVATE_KEY \
         BAP_ONIX_SIGNING_PUBLIC_KEY BAP_ONIX_ENCR_PRIVATE_KEY BAP_ONIX_ENCR_PUBLIC_KEY \
         BPP_NETWORK_PARTICIPANT BPP_ONIX_KEY_ID BPP_ONIX_SIGNING_PRIVATE_KEY \
         BPP_ONIX_SIGNING_PUBLIC_KEY BPP_ONIX_ENCR_PRIVATE_KEY BPP_ONIX_ENCR_PUBLIC_KEY; do
  require "$v"
done

BAP_VARS='$BAP_NETWORK_PARTICIPANT $BAP_ONIX_KEY_ID $BAP_ONIX_SIGNING_PRIVATE_KEY $BAP_ONIX_SIGNING_PUBLIC_KEY $BAP_ONIX_ENCR_PRIVATE_KEY $BAP_ONIX_ENCR_PUBLIC_KEY'
BPP_VARS='$BPP_NETWORK_PARTICIPANT $BPP_ONIX_KEY_ID $BPP_ONIX_SIGNING_PRIVATE_KEY $BPP_ONIX_SIGNING_PUBLIC_KEY $BPP_ONIX_ENCR_PRIVATE_KEY $BPP_ONIX_ENCR_PUBLIC_KEY'

envsubst "$BAP_VARS" < config/local-simple-bap.yaml.example > config/local-simple-bap.yaml
echo "wrote config/local-simple-bap.yaml"

envsubst "$BPP_VARS" < config/local-simple-bpp.yaml.example > config/local-simple-bpp.yaml
echo "wrote config/local-simple-bpp.yaml"

envsubst "$BPP_VARS" < config/catalog-publish.yaml.example > config/catalog-publish.yaml
echo "wrote config/catalog-publish.yaml"
