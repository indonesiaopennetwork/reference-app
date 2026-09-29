#!/usr/bin/env bash
# One-time (re-runnable) setup for the zitadel service in docker-compose.yml:
# waits for Zitadel to come up, reads the automation machine user's PAT
# (auto-created at first boot -- see docker-compose.yml's zitadel service),
# and uses Zitadel's own Management API to create a Project + a public/SPA
# OIDC Application for landing-page, writing the resulting client ID into
# .env as ZITADEL_CLIENT_ID. No manual step in Zitadel's console needed.
#
# Usage: ./scripts/setup-zitadel.sh
# Safe to re-run: looks up an existing project/application by name before
# creating either, so re-running never creates duplicates.

set -euo pipefail

cd "$(dirname "$0")/.."

BASE_URL="${ZITADEL_ISSUER:-http://localhost:8090}"
PAT_FILE="./.zitadel-machinekey/pat.txt"
PROJECT_NAME="reference-app"
APP_NAME="landing-page"
REDIRECT_URI="http://localhost:3002/callback"
POST_LOGOUT_REDIRECT_URI="http://localhost:3002"

for cmd in curl jq; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "ERROR: $cmd is required." >&2; exit 1; }
done

echo "Waiting for Zitadel at ${BASE_URL} to be ready..."
for i in $(seq 1 60); do
  if curl -sf "${BASE_URL}/.well-known/openid-configuration" >/dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "ERROR: Zitadel at ${BASE_URL} never became ready. Is 'docker compose up -d zitadel' running?" >&2
    exit 1
  fi
  sleep 2
done

echo "Waiting for the automation machine user's PAT at ${PAT_FILE}..."
for i in $(seq 1 60); do
  if [ -s "$PAT_FILE" ]; then
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "ERROR: ${PAT_FILE} never appeared. Check 'docker logs zitadel' for first-instance bootstrap errors." >&2
    exit 1
  fi
  sleep 2
done
PAT="$(cat "$PAT_FILE")"

api() {
  # api <path> <json-body>
  curl -sf -X POST "${BASE_URL}${1}" \
    -H "Authorization: Bearer ${PAT}" \
    -H "Content-Type: application/json" \
    -H "Connect-Protocol-Version: 1" \
    -d "$2"
}

echo "Looking up organization..."
ORG_ID="$(api /v2/organizations/_search '{}' | jq -r '.result[0].id')"
if [ -z "$ORG_ID" ] || [ "$ORG_ID" = "null" ]; then
  echo "ERROR: could not find any organization via /v2/organizations/_search." >&2
  exit 1
fi

echo "Looking up project '${PROJECT_NAME}'..."
PROJECT_ID="$(api /zitadel.project.v2.ProjectService/ListProjects '{}' \
  | jq -r --arg name "$PROJECT_NAME" '.projects[]? | select(.name == $name) | .projectId' | head -n1)"

if [ -z "${PROJECT_ID:-}" ] || [ "$PROJECT_ID" = "null" ]; then
  echo "Creating project '${PROJECT_NAME}'..."
  PROJECT_ID="$(api /zitadel.project.v2.ProjectService/CreateProject \
    "$(jq -n --arg org "$ORG_ID" --arg name "$PROJECT_NAME" '{organizationId: $org, name: $name}')" \
    | jq -r '.projectId')"
else
  echo "Found existing project ${PROJECT_ID}."
fi

echo "Looking up application '${APP_NAME}' in project ${PROJECT_ID}..."
CLIENT_ID="$(api /zitadel.application.v2.ApplicationService/ListApplications '{}' \
  | jq -r --arg pid "$PROJECT_ID" --arg name "$APP_NAME" \
    '.applications[]? | select(.projectId == $pid and .name == $name) | .oidcConfiguration.clientId' | head -n1)"

if [ -z "${CLIENT_ID:-}" ] || [ "$CLIENT_ID" = "null" ]; then
  echo "Creating OIDC application '${APP_NAME}' (public SPA client, PKCE)..."
  CREATE_APP_BODY="$(jq -n \
    --arg pid "$PROJECT_ID" \
    --arg name "$APP_NAME" \
    --arg redirect "$REDIRECT_URI" \
    --arg logout "$POST_LOGOUT_REDIRECT_URI" \
    '{
      projectId: $pid,
      name: $name,
      oidcConfiguration: {
        redirectUris: [$redirect],
        responseTypes: ["OIDC_RESPONSE_TYPE_CODE"],
        grantTypes: ["OIDC_GRANT_TYPE_AUTHORIZATION_CODE"],
        applicationType: "OIDC_APP_TYPE_USER_AGENT",
        authMethodType: "OIDC_AUTH_METHOD_TYPE_NONE",
        postLogoutRedirectUris: [$logout],
        developmentMode: true,
        accessTokenType: "OIDC_TOKEN_TYPE_JWT"
      }
    }')"
  CLIENT_ID="$(api /zitadel.application.v2.ApplicationService/CreateApplication "$CREATE_APP_BODY" \
    | jq -r '.oidcConfiguration.clientId')"
else
  echo "Found existing application, client ID ${CLIENT_ID}."
fi

if [ -z "${CLIENT_ID:-}" ] || [ "$CLIENT_ID" = "null" ]; then
  echo "ERROR: failed to obtain a client ID." >&2
  exit 1
fi

# SMTP: configured here (not via ZITADEL_DEFAULTINSTANCE_SMTPCONFIGURATION_*
# in docker-compose.yml) because a config seeded that way -- or one whose
# password was set afterwards via PUT .../password -- fails every send with
# CRYPT-mNsQwe / "535 Authentication failed". Only a config created in one
# call with its password works, so any existing one is replaced.
env_val() { grep "^$1=" .env | head -n1 | cut -d= -f2-; }
SMTP_HOST="$(env_val ZITADEL_SMTP_HOST)"
if [ -n "$SMTP_HOST" ]; then
  echo "Configuring SMTP (${SMTP_HOST})..."
  for OLD_ID in $(api /admin/v1/smtp/_search '{}' | jq -r '.result[]?.id'); do
    api "/admin/v1/smtp/${OLD_ID}/_deactivate" '{}' >/dev/null 2>&1 || true
    curl -sf -X DELETE "${BASE_URL}/admin/v1/smtp/${OLD_ID}" \
      -H "Authorization: Bearer ${PAT}" >/dev/null
  done
  SMTP_BODY="$(jq -n \
    --arg host "$SMTP_HOST" \
    --arg user "$(env_val ZITADEL_SMTP_USER)" \
    --arg password "$(env_val ZITADEL_SMTP_PASSWORD)" \
    --arg from "$(env_val ZITADEL_SMTP_FROM)" \
    --arg name "$(env_val ZITADEL_SMTP_FROM_NAME)" \
    --arg tls "$(env_val ZITADEL_SMTP_TLS)" \
    '{host: $host, user: $user, password: $password, senderAddress: $from, senderName: $name, tls: ($tls == "true")}')"
  SMTP_ID="$(api /admin/v1/smtp "$SMTP_BODY" | jq -r '.id')"
  api "/admin/v1/smtp/${SMTP_ID}/_activate" '{}' >/dev/null
  echo "SMTP config ${SMTP_ID} created and activated."
fi

if grep -q '^ZITADEL_CLIENT_ID=' .env; then
  sed -i.bak "s|^ZITADEL_CLIENT_ID=.*|ZITADEL_CLIENT_ID=${CLIENT_ID}|" .env && rm -f .env.bak
else
  echo "ZITADEL_CLIENT_ID=${CLIENT_ID}" >> .env
fi

echo "Done. ZITADEL_CLIENT_ID=${CLIENT_ID} written to .env."
