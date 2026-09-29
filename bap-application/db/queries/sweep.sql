-- Stuck-transaction recovery. See internal/sweep's own package doc for why
-- this exists and why the threshold here is measured in hours, not minutes
-- like a typical delivery-timeout retry.

-- name: RollBackStaleUpdateSent :many
-- Atomically finds and resets every transaction that's been sitting at
-- UPDATE_SENT for longer than staleAfterMinutes, back to CONFIRMED --
-- exactly the same target state Update()'s own synchronous
-- rollback-on-send-failure already uses, just for the case that isn't
-- detectable synchronously (delivery succeeded, but no on_update ever
-- arrived). Returns the transaction IDs it recovered, for logging.
UPDATE transactions
SET status = 'CONFIRMED', updated_at = NOW()
WHERE status = 'UPDATE_SENT'
  AND updated_at < NOW() - make_interval(mins => sqlc.arg(stale_after_minutes)::int)
RETURNING transaction_id;
