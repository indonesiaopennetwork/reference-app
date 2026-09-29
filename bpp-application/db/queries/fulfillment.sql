-- Queries for fulfillment actions: status, cancel, update, rate, support.

-- ============================================================
-- STATUS — look up full contract for on_status callback
-- ============================================================

-- name: GetActiveContractByTxnID :one
-- Returns core contract fields needed to build an on_status payload.
SELECT
    c.id,
    c.bap_id,
    c.bap_uri,
    c.status,
    c.transaction_id,
    c.network_id,
    c.domain,
    c.contract_attributes,
    c.created_at,
    c.updated_at
FROM contracts c
WHERE c.transaction_id = $1
  AND c.bpp_id = $2
  AND c.deleted_at IS NULL
LIMIT 1;

-- name: GetActiveContractByID :one
-- Returns core contract fields needed to build an on_status payload, using ID.
SELECT
    c.id,
    c.bap_id,
    c.bap_uri,
    c.status,
    c.transaction_id,
    c.network_id,
    c.domain,
    c.contract_attributes,
    c.created_at,
    c.updated_at
FROM contracts c
WHERE c.id = $1
  AND c.bpp_id = $2
  AND c.deleted_at IS NULL
LIMIT 1;

-- ============================================================
-- UPDATE FULFILLMENT STATUS
-- ============================================================

-- name: UpdatePerformanceStatus :exec
-- Updates the status of contract performances for a contract.
UPDATE contract_performances SET
    status_code = $1,
    updated_at = NOW()
WHERE contract_id = $2;

-- ============================================================
-- CANCEL — transition ACTIVE → CANCELLED
-- ============================================================

-- name: CancelContract :one
-- Transitions an ACTIVE contract to CANCELLED.
-- Returns bap_uri and bap_id so the service can call on_cancel.
UPDATE contracts SET
    status     = 'CANCELLED'::contract_status,
    updated_at = NOW()
WHERE transaction_id = $1
  AND bpp_id = $2
  AND status = 'ACTIVE'::contract_status
  AND deleted_at IS NULL
RETURNING id, bap_id, bap_uri;

-- ============================================================
-- UPDATE — mutate contract_attributes on an ACTIVE contract
-- ============================================================

-- name: UpdateContractIfActive :one
-- Mirrors CancelContract's status-gated pattern: only mutates an ACTIVE
-- contract, so an update targeting an already-CANCELLED/COMPLETE contract
-- affects 0 rows (surfaced as pgx.ErrNoRows) instead of silently
-- succeeding against a terminal contract. Returns bap_id/bap_uri so the
-- service can call on_update without a separate lookup.
UPDATE contracts SET
    contract_attributes = $1,
    updated_at = NOW()
WHERE transaction_id = $2
  AND bpp_id = $3
  AND status = 'ACTIVE'::contract_status
  AND deleted_at IS NULL
RETURNING id, bap_id, bap_uri;

-- ============================================================
-- UPDATE REQUESTS — buyer-proposed contract mutations awaiting seller review
-- ============================================================

-- name: InsertUpdateRequest :one
-- previous_delivery_details is the order's delivery block as it stands RIGHT
-- NOW, captured so the seller can still see what changed after the request
-- is decided (see migration 030 -- diffing against the live contract breaks
-- the moment an approval is applied).
INSERT INTO contract_update_requests (
    contract_id, transaction_id, bap_id, bap_uri, requested_attributes,
    previous_delivery_details
)
VALUES ($1, $2, $3, $4, $5, sqlc.narg('previous_delivery_details'))
RETURNING id;

-- name: ListUpdateRequests :many
-- provider_id: see ListSupportTickets's own comment -- NULL bypasses for internal/ops use.
SELECT
    ur.id,
    ur.contract_id,
    ur.transaction_id,
    ur.bap_id,
    ur.requested_attributes,
    ur.status,
    ur.reject_reason,
    ur.created_at,
    ur.decided_at,
    c.contract_attributes AS current_attributes
FROM contract_update_requests ur
JOIN contracts c ON c.id = ur.contract_id
WHERE c.bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR c.provider_id = sqlc.narg('provider_id'))
  AND (sqlc.narg('status')::update_request_status IS NULL OR ur.status = sqlc.narg('status'))
ORDER BY ur.created_at DESC
LIMIT  $2
OFFSET $3;

-- name: CountUpdateRequests :one
SELECT COUNT(*) FROM contract_update_requests ur
JOIN contracts c ON c.id = ur.contract_id
WHERE c.bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR c.provider_id = sqlc.narg('provider_id'))
  AND (sqlc.narg('status')::update_request_status IS NULL OR ur.status = sqlc.narg('status'));

-- name: GetUpdateRequestForDecision :one
-- Scoped by provider_id (nullable, NULL bypasses for internal/ops use) so a
-- seller can only see/decide requests against their own contracts, same
-- scoping convention as ListUpdateRequests.
SELECT ur.id, ur.contract_id, ur.transaction_id, ur.bap_id, ur.bap_uri,
       ur.requested_attributes, ur.status
FROM contract_update_requests ur
JOIN contracts c ON c.id = ur.contract_id
WHERE ur.id = $1
  AND c.bpp_id = $2
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR c.provider_id = sqlc.narg('provider_id'));

-- name: DecideUpdateRequestIfPending :execrows
-- Atomic compare-and-swap, same technique as MarkUpdateSentIfConfirmed on
-- the BAP side: only a currently-PENDING request can be decided, so two
-- concurrent approve/reject clicks (or a double-submit) can't both succeed.
UPDATE contract_update_requests
SET status = $2, reject_reason = $3, decided_at = NOW()
WHERE id = $1
  AND status = 'PENDING';

-- name: GetCommitmentResourceIDsByContractID :many
-- Returns all committed resource IDs for a contract (used to restore stock on cancel).
SELECT DISTINCT cr.resource_id
FROM commitment_resources cr
JOIN commitments cm ON cm.id = cr.commitment_id AND cm.contract_id = cr.contract_id
WHERE cm.contract_id = $1;

-- ============================================================
-- RATE — persist rating inputs
-- ============================================================

-- name: InsertRating :exec
INSERT INTO ratings (
    contract_id,
    bap_id,
    bap_uri,
    target_id,
    target_descriptor,
    range,
    feedback_form_submission,
    is_preview
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListRatings :many
-- provider_id: see dashboard.sql's ListContracts -- NULL bypasses for internal/ops use.
SELECT
    r.id,
    r.contract_id,
    c.transaction_id,
    r.bap_id,
    r.target_id,
    r.target_descriptor,
    r.range,
    r.feedback_form_submission,
    r.is_preview,
    r.created_at
FROM ratings r
JOIN contracts c ON c.id = r.contract_id
WHERE c.bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR c.provider_id = sqlc.narg('provider_id'))
ORDER BY r.created_at DESC
LIMIT  $2
OFFSET $3;

-- name: CountRatings :one
SELECT COUNT(*) FROM ratings r
JOIN contracts c ON c.id = r.contract_id
WHERE c.bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR c.provider_id = sqlc.narg('provider_id'));

-- ============================================================
-- SUPPORT — persist support tickets
-- ============================================================

-- name: InsertSupportTicket :one
INSERT INTO support_tickets (
    contract_id,
    bap_id,
    bap_uri,
    descriptor_name,
    descriptor_short_desc,
    channels,
    is_preview
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: ListSupportTickets :many
-- provider_id NULL (internal/ops use): unchanged behavior, includes standalone
-- tickets with no contract_id. provider_id set (seller-scoped): standalone
-- tickets are excluded -- there's no contract to resolve a provider from, so
-- they can't be safely attributed to any one seller (see 015_support_tickets.sql).
SELECT
    st.id,
    st.contract_id,
    c.transaction_id,
    st.bap_id,
    st.descriptor_name,
    st.descriptor_short_desc,
    st.channels,
    st.status,
    st.is_preview,
    st.created_at,
    st.resolved_at
FROM support_tickets st
LEFT JOIN contracts c ON c.id = st.contract_id
WHERE
    CASE
        WHEN sqlc.narg('provider_id')::TEXT IS NOT NULL THEN c.provider_id = sqlc.narg('provider_id')
        ELSE (c.bpp_id = $1 OR st.contract_id IS NULL)
    END
ORDER BY st.created_at DESC
LIMIT  $2
OFFSET $3;

-- name: CountSupportTickets :one
SELECT COUNT(*) FROM support_tickets st
LEFT JOIN contracts c ON c.id = st.contract_id
WHERE
    CASE
        WHEN sqlc.narg('provider_id')::TEXT IS NOT NULL THEN c.provider_id = sqlc.narg('provider_id')
        ELSE (c.bpp_id = $1 OR st.contract_id IS NULL)
    END;

-- name: ListUpdateRequestsDetailed :many
-- Same scoping as ListUpdateRequests, but joins the order context a human
-- seller needs to decide: when the order was placed, its contract status,
-- the delivery details the seller currently holds, and the fulfilment
-- phase column.
--
-- current_performance_attributes -- NOT contracts.contract_attributes --
-- is the seller's authoritative delivery address. contract_attributes is a
-- small JSON-LD extension bag and is routinely NULL; comparing against it
-- is what made the old seller screen show every field as "new". See
-- internal/orderchange.
--
-- performance_status_code is selected for the fulfilment-phase extension
-- point (internal/orderchange.PhaseReporter). It is 'PENDING' for every
-- row today because nothing advances it yet.
SELECT
    ur.id,
    ur.contract_id,
    ur.transaction_id,
    ur.bap_id,
    ur.requested_attributes,
    ur.status,
    ur.reject_reason,
    ur.created_at,
    ur.decided_at,
    c.created_at      AS order_placed_at,
    c.status          AS contract_status,
    c.descriptor_name AS order_name,
    cp.status_code    AS performance_status_code,
    cp.performance_attributes AS current_performance_attributes,
    ur.previous_delivery_details,
    COALESCE(
        (SELECT jsonb_agg(cm.commitment_attributes ORDER BY cm.id)
         FROM commitments cm
         WHERE cm.contract_id = ur.contract_id
           AND cm.commitment_attributes IS NOT NULL),
        '[]'::jsonb
    ) AS commitment_attributes
FROM contract_update_requests ur
JOIN contracts c ON c.id = ur.contract_id
LEFT JOIN LATERAL (
    SELECT status_code, performance_attributes
    FROM contract_performances
    WHERE contract_id = ur.contract_id
    ORDER BY created_at ASC
    LIMIT 1
) cp ON TRUE
WHERE c.bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR c.provider_id = sqlc.narg('provider_id'))
  AND (sqlc.narg('status')::update_request_status IS NULL OR ur.status = sqlc.narg('status'))
ORDER BY ur.created_at DESC
LIMIT  $2
OFFSET $3;

-- name: GetCurrentPerformanceForContract :one
-- The seller's live delivery details, used both to show "before" values and
-- as the target ApplyPerformanceDeliveryDetails writes into.
SELECT id, status_code, performance_attributes
FROM contract_performances
WHERE contract_id = $1
ORDER BY created_at ASC
LIMIT 1;

-- name: ApplyPerformanceDeliveryDetails :execrows
-- Writes an approved delivery-details change to where fulfilment actually
-- reads it.
--
-- This is the fix for a real bug: approving a change used to write the
-- buyer's whole proposed contract into contracts.contract_attributes,
-- which nothing reads for fulfilment -- so an approved address change was
-- recorded but the parcel still went to the old address. The delivery
-- address lives in contract_performances.performance_attributes, and this
-- is the only statement that should change it.
UPDATE contract_performances
SET performance_attributes = jsonb_set(
        COALESCE(performance_attributes, '{}'::jsonb),
        '{deliveryDetails}',
        sqlc.arg(delivery_details)::jsonb,
        true
    ),
    updated_at = NOW()
WHERE contract_id = sqlc.arg(contract_id);
