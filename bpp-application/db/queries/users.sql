-- Seller login accounts (see 026_users.sql). One row per Zitadel identity,
-- 1:1 with the provider it owns.

-- name: GetUserByZitadelSubject :one
SELECT id, zitadel_subject, email, provider_id, bpp_id, created_at
FROM users
WHERE zitadel_subject = $1;

-- name: CreateUser :one
-- Called by POST /api/v1/auth/bootstrap the first time a Zitadel identity
-- is seen; provider_id/bpp_id must already exist (the provider is created
-- in the same request, immediately before this call).
INSERT INTO users (zitadel_subject, email, provider_id, bpp_id)
VALUES ($1, $2, $3, $4)
RETURNING id, zitadel_subject, email, provider_id, bpp_id, created_at;
