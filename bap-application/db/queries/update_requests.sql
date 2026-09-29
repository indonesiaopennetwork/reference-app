-- Buyer-side tracking of outbound /update requests and the seller decision
-- recovered from the matching on_update. See migration
-- 012_update_requests.sql for why the decision has to be inferred rather
-- than read off a field.

-- name: InsertUpdateRequest :one
-- Records a PENDING request at /update send time. Returns the row id.
-- The partial unique index on (transaction_id) WHERE outcome = 'PENDING'
-- makes a concurrent second insert fail loudly rather than silently
-- creating two in-flight requests for one transaction.
INSERT INTO update_requests (transaction_id, message_id, proposed_contract)
VALUES ($1, $2, $3)
RETURNING id;

-- name: GetPendingUpdateRequest :one
-- The one in-flight request for a transaction, if any.
SELECT id, transaction_id, message_id, proposed_contract, outcome, created_at, decided_at
FROM update_requests
WHERE transaction_id = $1
  AND outcome = 'PENDING';

-- name: GetLatestUpdateRequest :one
-- The most recent request for a transaction regardless of outcome -- what
-- the order detail view shows the buyer ("your change was applied" /
-- "declined" / "still awaiting review").
SELECT id, transaction_id, message_id, proposed_contract, outcome, created_at, decided_at
FROM update_requests
WHERE transaction_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: ResolveUpdateRequest :execrows
-- Moves a PENDING request to its final outcome. Predicated on outcome =
-- 'PENDING' so a duplicate/replayed on_update can't rewrite a decision
-- that was already recorded -- the same compare-and-swap discipline
-- MarkUpdateReceivedIfPending applies to the transaction status itself.
UPDATE update_requests
SET outcome = $2, decided_at = NOW()
WHERE id = $1
  AND outcome = 'PENDING';

-- name: AbandonPendingUpdateRequests :execrows
-- Marks every still-PENDING request for the given transactions ABANDONED.
-- Used both by Update()'s synchronous rollback (delivery never happened)
-- and by internal/sweep (delivery happened, no on_update ever came back),
-- so a buyer is never left looking at "awaiting seller review" for a
-- request that is no longer in flight.
UPDATE update_requests
SET outcome = 'ABANDONED', decided_at = NOW()
WHERE transaction_id = ANY(sqlc.arg(transaction_ids)::uuid[])
  AND outcome = 'PENDING';

-- name: CountUpdateRequests :one
-- How many change requests this transaction has ever had, used to enforce
-- clientsvc.MaxUpdateRequestsPerOrder. Counts every request regardless of
-- outcome: the cap exists so a buyer cannot make a seller re-evaluate the
-- same order indefinitely, and an abandoned or declined attempt cost the
-- seller attention just as an approved one did.
SELECT COUNT(*) FROM update_requests WHERE transaction_id = $1;

-- name: ListUpdateRequestsForTransaction :many
-- Newest-first history for one transaction, capped by the caller. The buyer
-- sees a short window (clientsvc.BuyerUpdateHistoryLimit) so they can see
-- what they already asked and how it went, without an unbounded list.
SELECT id, transaction_id, message_id, proposed_contract, outcome,
       decision_reason_code, decision_reason_text, created_at, decided_at
FROM update_requests
WHERE transaction_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: ResolveUpdateRequestWithReason :execrows
-- Same compare-and-swap as ResolveUpdateRequest, but also records the
-- seller's stated reason. Used when on_update carried an explicit
-- updateDecision; ResolveUpdateRequest remains for the inferred path.
UPDATE update_requests
SET outcome = $2,
    decision_reason_code = sqlc.narg('decision_reason_code'),
    decision_reason_text = sqlc.narg('decision_reason_text'),
    decided_at = NOW()
WHERE id = $1
  AND outcome = 'PENDING';
