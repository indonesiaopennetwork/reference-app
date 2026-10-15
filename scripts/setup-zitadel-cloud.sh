#!/usr/bin/env bash
# Cloud counterpart of scripts/setup-zitadel.sh: run this once, after
# `terraform apply` has brought up the zitadel Cloud Run service (see
# terraform/cloud-run-zitadel.tf), to create the same Project + public/SPA
# OIDC Application for landing-page that the local script creates against
# docker-compose's zitadel. Prints the resulting client ID -- it does NOT
# write it anywhere itself, since this script has no access to Terraform
# state/tfvars. Put the printed value into terraform.tfvars as
# zitadel_client_id and `terraform apply` again (see variables.tf's comment
# on that variable for why a second apply is needed at all).
#
# Usage: ./scripts/setup-zitadel-cloud.sh
# Safe to re-run: looks up an existing project/application by name before
# creating either, so re-running never creates duplicates.
#
# Requires: gcloud (authenticated against the target project), curl, jq.

set -euo pipefail

cd "$(dirname "$0")/.."

if [ -z "${PROJECT_ID:-}" ]; then
  echo "ERROR: set PROJECT_ID, e.g. PROJECT_ID=your-project ./scripts/setup-zitadel-cloud.sh" >&2
  exit 1
fi

for cmd in gcloud curl jq; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "ERROR: $cmd is required." >&2; exit 1; }
done

cd terraform
BASE_URL="${ZITADEL_ISSUER:-$(terraform output -raw zitadel_domain_url)}"
LANDING_PAGE_URL="${LANDING_PAGE_URL:-$(terraform output -raw landing_page_public_url)}"
MACHINEKEY_BUCKET="$(terraform output -raw zitadel_machinekey_bucket)"
cd ..

PROJECT_NAME="reference-app"
APP_NAME="landing-page"
REDIRECT_URI="${LANDING_PAGE_URL}/callback"
# Trailing slash required -- landing-page/src/oidc.js's logout() builds this
# as `${window.location.origin}${basePath}`, and basePath always has a
# trailing slash (see config.js) even at a domain root. Omitting it here
# causes Zitadel to reject logout with "post_logout_redirect_uri invalid".
POST_LOGOUT_REDIRECT_URI="${LANDING_PAGE_URL}/"

echo "Waiting for Zitadel at ${BASE_URL} to be ready..."
for i in $(seq 1 60); do
  if curl -sf "${BASE_URL}/.well-known/openid-configuration" >/dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "ERROR: Zitadel at ${BASE_URL} never became ready. Check 'gcloud run services describe zitadel --project=${PROJECT_ID}' and its logs." >&2
    exit 1
  fi
  sleep 5
done

echo "Reading the automation machine user's PAT from gs://${MACHINEKEY_BUCKET}/pat.txt..."
for i in $(seq 1 60); do
  if gsutil -q stat "gs://${MACHINEKEY_BUCKET}/pat.txt" 2>/dev/null; then
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "ERROR: pat.txt never appeared in gs://${MACHINEKEY_BUCKET}/. Check 'gcloud run services logs read zitadel --project=${PROJECT_ID}' for first-instance bootstrap errors." >&2
    exit 1
  fi
  sleep 5
done
PAT="$(gsutil cat "gs://${MACHINEKEY_BUCKET}/pat.txt")"

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
PROJECT_ID_ZITADEL="$(api /zitadel.project.v2.ProjectService/ListProjects '{}' \
  | jq -r --arg name "$PROJECT_NAME" '.projects[]? | select(.name == $name) | .projectId' | head -n1)"

if [ -z "${PROJECT_ID_ZITADEL:-}" ] || [ "$PROJECT_ID_ZITADEL" = "null" ]; then
  echo "Creating project '${PROJECT_NAME}'..."
  PROJECT_ID_ZITADEL="$(api /zitadel.project.v2.ProjectService/CreateProject \
    "$(jq -n --arg org "$ORG_ID" --arg name "$PROJECT_NAME" '{organizationId: $org, name: $name}')" \
    | jq -r '.projectId')"
else
  echo "Found existing project ${PROJECT_ID_ZITADEL}."
fi

echo "Looking up application '${APP_NAME}' in project ${PROJECT_ID_ZITADEL}..."
CLIENT_ID="$(api /zitadel.application.v2.ApplicationService/ListApplications '{}' \
  | jq -r --arg pid "$PROJECT_ID_ZITADEL" --arg name "$APP_NAME" \
    '.applications[]? | select(.projectId == $pid and .name == $name) | .oidcConfiguration.clientId' | head -n1)"

# idTokenUserinfoAssertion: without this, the id_token carries only
# sub/iss/aud/etc -- no name/email -- even with scope=openid profile email
# requested. bap-frontend/bpp-frontend's UserProfileMenu decodes the
# id_token client-side (no userinfo call) specifically relying on this.
# Yes, this is a deliberate, documented departure from strict OIDC
# practice (Zitadel's own proto comment: "this violates the OIDC
# specification... to prevent leaking of personal information in the id
# token") -- accepted here since this id_token is already forwarded
# between apps via URL params regardless.
if [ -z "${CLIENT_ID:-}" ] || [ "$CLIENT_ID" = "null" ]; then
  echo "Creating OIDC application '${APP_NAME}' (public SPA client, PKCE)..."
  # developmentMode: false here (unlike the local script) -- this app is
  # reached over real HTTPS custom domains, so the relaxed redirect-URI
  # checks development mode allows (e.g. plain http) aren't needed.
  CREATE_APP_BODY="$(jq -n \
    --arg pid "$PROJECT_ID_ZITADEL" \
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
        # landing-page logs out to its base URL with a trailing slash
        # (origin + basePath); Zitadel matches exactly, so register both.
        postLogoutRedirectUris: ([($logout | rtrimstr("/")), ($logout | rtrimstr("/")) + "/"]),
        developmentMode: false,
        accessTokenType: "OIDC_TOKEN_TYPE_JWT",
        idTokenUserinfoAssertion: true
      }
    }')"
  CLIENT_ID="$(api /zitadel.application.v2.ApplicationService/CreateApplication "$CREATE_APP_BODY" \
    | jq -r '.oidcConfiguration.clientId')"
else
  echo "Found existing application, client ID ${CLIENT_ID}. Ensuring idTokenUserinfoAssertion and postLogoutRedirectUris are up to date..."
  APP_ID="$(api /zitadel.application.v2.ApplicationService/ListApplications '{}' \
    | jq -r --arg pid "$PROJECT_ID_ZITADEL" --arg name "$APP_NAME" \
      '.applications[]? | select(.projectId == $pid and .name == $name) | .applicationId' | head -n1)"
  api /zitadel.application.v2.ApplicationService/UpdateApplication \
    "$(jq -n --arg aid "$APP_ID" --arg pid "$PROJECT_ID_ZITADEL" --arg logout "$POST_LOGOUT_REDIRECT_URI" \
      '{applicationId: $aid, projectId: $pid, oidcConfiguration: {idTokenUserinfoAssertion: true, postLogoutRedirectUris: [$logout]}}')" \
    >/dev/null
fi

if [ -z "${CLIENT_ID:-}" ] || [ "$CLIENT_ID" = "null" ]; then
  echo "ERROR: failed to obtain a client ID." >&2
  exit 1
fi

# Login page look, matching the BAP / BPP apps.
BASE_URL="$BASE_URL" PAT="$PAT" ./scripts/zitadel-branding.sh

echo ""
echo "Done. Set this in terraform/terraform.tfvars and run 'terraform apply' again:"
echo ""
echo "  zitadel_client_id = \"${CLIENT_ID}\""
echo ""
