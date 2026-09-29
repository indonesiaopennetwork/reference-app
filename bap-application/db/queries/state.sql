-- Queries for persisting contract state received in on_* callbacks.

-- name: UpsertTransaction :exec
-- Creates or updates the transaction tracking row. buyer_subject is
-- deliberately excluded from the ON CONFLICT update, same as bap_id --
-- it's set once at creation (by Select, the only call site that has a
-- buyer identity to give it) and never clobbered by later lifecycle
-- upserts that don't.
INSERT INTO transactions (transaction_id, bap_id, network_id, bpp_id, bpp_uri, status, buyer_subject)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (transaction_id) DO UPDATE SET
    bpp_id     = EXCLUDED.bpp_id,
    bpp_uri    = EXCLUDED.bpp_uri,
    status     = EXCLUDED.status,
    updated_at = NOW();

-- name: UpsertContractSnapshot :exec
-- Stores (or replaces) the full contract JSON for a given (transaction_id, action).
INSERT INTO contract_snapshots (transaction_id, action, contract_id, contract)
VALUES ($1, $2, $3, $4);

-- name: GetTransaction :one
-- Returns the transaction row for the given transaction UUID.
SELECT transaction_id, bap_id, network_id, bpp_id, bpp_uri, status, created_at, updated_at
FROM transactions
WHERE transaction_id = $1;

-- name: GetTransactionForBuyer :one
-- Same as GetTransaction, but scoped to the given buyer_subject -- returns
-- no rows if the transaction belongs to someone else (or has no owner).
SELECT transaction_id, bap_id, network_id, bpp_id, bpp_uri, status, created_at, updated_at
FROM transactions
WHERE transaction_id = $1
  AND buyer_subject = $2;

-- name: GetContractSnapshot :one
-- Returns the most recent contract snapshot for a (transaction_id, action) pair.
SELECT id, transaction_id, action, contract_id, contract, created_at, updated_at
FROM contract_snapshots
WHERE transaction_id = $1
  AND action = $2;

-- name: GetLatestContractSnapshot :one
-- Returns the latest snapshot for a transaction (highest priority action).
SELECT id, transaction_id, action, contract_id, contract, created_at, updated_at
FROM contract_snapshots
WHERE transaction_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: ListTransactions :many
-- Returns paginated transactions newest-first, with their latest contract snapshot.
SELECT
    t.transaction_id,
    t.bpp_id,
    t.bpp_uri,
    t.status,
    t.created_at,
    t.updated_at,
    cs.contract,
    cs.action AS latest_action
FROM transactions t
LEFT JOIN LATERAL (
    SELECT contract, action
    FROM contract_snapshots
    WHERE transaction_id = t.transaction_id
    ORDER BY created_at DESC
    LIMIT 1
) cs ON true
ORDER BY t.created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountTransactions :one
SELECT COUNT(*) FROM transactions;

-- name: MarkUpdateSentIfConfirmed :execrows
-- Atomically transitions CONFIRMED -> UPDATE_SENT. The WHERE status =
-- 'CONFIRMED' predicate doubles as a compare-and-swap: it returns 0 rows
-- affected (not an error) if the transaction is already mid-update
-- (UPDATE_SENT), terminal (CANCELLED/COMPLETE), or never confirmed at all --
-- giving Update() a single atomic check for both "invalid state" and
-- "another update already in flight" without a separate lock.
UPDATE transactions
SET status = 'UPDATE_SENT', updated_at = NOW()
WHERE transaction_id = $1
  AND status = 'CONFIRMED';

-- name: MarkCancelSentIfConfirmed :execrows
-- Same compare-and-swap technique as MarkUpdateSentIfConfirmed, applied to
-- Cancel: only a CONFIRMED transaction can be cancelled, which as a side
-- effect also blocks cancelling one with an update currently in flight
-- (UPDATE_SENT) -- Cancel and Update share one mutual-exclusion window via
-- the same status column, rather than needing a separate lock per pair of
-- actions.
UPDATE transactions
SET status = 'CANCEL_SENT', updated_at = NOW()
WHERE transaction_id = $1
  AND status = 'CONFIRMED';

-- name: MarkUpdateReceivedIfPending :execrows
-- Atomically transitions UPDATE_SENT -> CONFIRMED on a received on_update.
-- 0 rows affected means this callback doesn't correspond to an update this
-- transaction is currently waiting on (a duplicate/replayed/stale
-- on_update, or an unsolicited one) -- the caller uses that to skip
-- overwriting the committed contract snapshot with it.
UPDATE transactions
SET status = 'CONFIRMED', updated_at = NOW()
WHERE transaction_id = $1
  AND status = 'UPDATE_SENT';
