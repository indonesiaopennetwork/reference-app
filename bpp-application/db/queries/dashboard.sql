-- Dashboard queries for the BPP admin panel.

-- ============================================================
-- STATS
-- ============================================================

-- name: GetDashboardStats :one
-- provider_id: see ListContracts (below) -- NULL bypasses for internal/ops use.
SELECT
    COUNT(*) FILTER (WHERE status = 'ACTIVE')                           AS active_orders,
    COUNT(*) FILTER (WHERE status = 'DRAFT')                            AS pending_orders,
    COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '24 hours')    AS today_orders
FROM contracts
WHERE bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR provider_id = sqlc.narg('provider_id'))
  AND deleted_at IS NULL;

-- name: GetInventoryStats :one
-- provider_id: see ListContracts -- NULL bypasses for internal/ops use.
SELECT
    (SELECT COUNT(*) FROM resources r2 WHERE r2.bpp_id = $1
        AND (sqlc.narg('provider_id')::TEXT IS NULL OR r2.provider_id = sqlc.narg('provider_id'))
        AND r2.deleted_at IS NULL) AS resource_count,
    (SELECT COUNT(*) FROM offers o2
     JOIN catalogs cat2 ON cat2.id = o2.catalog_id AND cat2.bpp_id = o2.bpp_id
     WHERE o2.bpp_id = $1
        AND (sqlc.narg('provider_id')::TEXT IS NULL OR COALESCE(o2.provider_id, cat2.provider_id) = sqlc.narg('provider_id'))
        AND o2.deleted_at IS NULL) AS offer_count;

-- name: GetMessageFunnel :many
SELECT
    action,
    COUNT(*) AS message_count
FROM beckn_message_log
WHERE bpp_id = $1
  AND action IN ('select', 'init', 'confirm')
  AND direction = 'INBOUND'
GROUP BY action
ORDER BY action;

-- ============================================================
-- CONTRACTS / ORDERS
-- ============================================================

-- name: ListContracts :many
-- provider_id: NULL lists across all sellers (used by internal/ops tooling);
-- a non-NULL value scopes to that seller's own orders only (the dashboard
-- API always passes one, resolved from the authenticated session).
SELECT
    c.id,
    c.transaction_id,
    c.status,
    c.bap_id,
    c.created_at,
    COALESCE(co.offer_descriptor, '{}') AS offer_descriptor
FROM contracts c
LEFT JOIN LATERAL (
    SELECT co2.offer_descriptor
    FROM commitment_offers co2
    JOIN commitments cm ON cm.id = co2.commitment_id AND cm.contract_id = co2.contract_id
    WHERE cm.contract_id = c.id
    LIMIT 1
) co ON TRUE
WHERE c.bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR c.provider_id = sqlc.narg('provider_id'))
  AND c.deleted_at IS NULL
ORDER BY c.created_at DESC
LIMIT  $2
OFFSET $3;

-- name: CountContracts :one
SELECT COUNT(*) FROM contracts
WHERE bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR provider_id = sqlc.narg('provider_id'))
  AND deleted_at IS NULL;

-- name: GetContractDetail :one
-- provider_id: see ListContracts -- NULL bypasses the check for internal/ops use.
SELECT
    c.id,
    c.transaction_id,
    c.status,
    c.bap_id,
    c.bap_uri,
    c.domain,
    c.contract_attributes,
    c.created_at,
    c.updated_at
FROM contracts c
WHERE c.id = $1
  AND c.bpp_id = $2
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR c.provider_id = sqlc.narg('provider_id'));

-- name: ListContractCommitments :many
SELECT
    cm.id,
    cm.status,
    cm.commitment_attributes,
    co.offer_id,
    co.offer_descriptor,
    co.resource_ids
FROM commitments cm
LEFT JOIN commitment_offers co ON co.commitment_id = cm.id AND co.contract_id = cm.contract_id
WHERE cm.contract_id = $1
ORDER BY cm.created_at;

-- name: ListContractConsiderations :many
SELECT
    id,
    status_code,
    status_name,
    consideration_attributes
FROM considerations
WHERE contract_id = $1;

-- ============================================================
-- RESOURCES
-- ============================================================

-- name: ListResources :many
-- provider_id: see ListContracts -- NULL bypasses for internal/ops use.
SELECT
    r.id,
    r.catalog_id,
    r.descriptor_name,
    r.descriptor_short_desc,
    r.resource_attributes,
    r.created_at
FROM resources r
WHERE r.bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR r.provider_id = sqlc.narg('provider_id'))
  AND r.deleted_at IS NULL
ORDER BY r.created_at DESC
LIMIT  $2
OFFSET $3;

-- name: CountResources :one
SELECT COUNT(*) FROM resources
WHERE bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR provider_id = sqlc.narg('provider_id'))
  AND deleted_at IS NULL;

-- ============================================================
-- OFFERS
-- ============================================================

-- name: ListOffers :many
-- provider_id: see ListContracts -- NULL bypasses for internal/ops use.
-- offers.provider_id is nullable (optional override of the catalog's own
-- provider, per 007_offers.sql) so this falls back to the catalog's
-- provider_id when unset, same as any other offer on that catalog.
SELECT
    o.id,
    o.catalog_id,
    o.descriptor_name,
    o.descriptor_short_desc,
    o.resource_ids,
    o.validity_start,
    o.validity_end,
    o.created_at
FROM offers o
JOIN catalogs cat ON cat.id = o.catalog_id AND cat.bpp_id = o.bpp_id
WHERE o.bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR COALESCE(o.provider_id, cat.provider_id) = sqlc.narg('provider_id'))
  AND o.deleted_at IS NULL
ORDER BY o.created_at DESC
LIMIT  $2
OFFSET $3;

-- name: CountOffers :one
SELECT COUNT(*) FROM offers o
JOIN catalogs cat ON cat.id = o.catalog_id AND cat.bpp_id = o.bpp_id
WHERE o.bpp_id = $1
  AND (sqlc.narg('provider_id')::TEXT IS NULL OR COALESCE(o.provider_id, cat.provider_id) = sqlc.narg('provider_id'))
  AND o.deleted_at IS NULL;

-- name: ListOfferConsiderations :many
SELECT
    consideration_id,
    status_code,
    status_name,
    consideration_attributes
FROM offer_considerations
WHERE offer_id = $1
  AND bpp_id   = $2;

-- ============================================================
-- MESSAGE LOG
-- ============================================================

-- name: ListRecentMessages :many
SELECT
    id,
    message_id,
    action,
    direction,
    ack_status,
    transaction_id,
    url,
    error_message,
    request_payload,
    response_payload,
    created_at
FROM beckn_message_log
WHERE bpp_id = $1
ORDER BY created_at DESC
LIMIT $2;
