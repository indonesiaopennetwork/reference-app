-- Stale-pending-review alerting. See internal/sweep's own package doc.

-- name: ListStalePendingUpdateRequests :many
-- Read-only: unlike the BAP-side sweep, this never mutates
-- contract_update_requests -- a PENDING request genuinely needs a seller's
-- decision, not an automatic one, so this only surfaces which ones have
-- been waiting too long.
SELECT ur.id, ur.contract_id, ur.created_at
FROM contract_update_requests ur
WHERE ur.status = 'PENDING'
  AND ur.created_at < NOW() - make_interval(mins => sqlc.arg(stale_after_minutes)::int);
