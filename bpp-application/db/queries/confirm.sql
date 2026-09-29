-- Queries for the /beckn/confirm handler.
-- Confirm transitions the contract from DRAFT → ACTIVE.
-- The contract MUST already exist (created during init).

-- name: ConfirmContract :one
-- Transitions a DRAFT contract to ACTIVE status.
-- Returns the contract id on success; errors if no DRAFT contract is found.
UPDATE contracts SET
    status     = 'ACTIVE'::contract_status,
    updated_at = NOW()
WHERE transaction_id = $1
  AND bpp_id = $2
  AND status = 'DRAFT'::contract_status
  AND deleted_at IS NULL
RETURNING id;

-- name: UpsertSettlement :exec
-- Records the settlement (payment proof) the BAP supplies on confirm.
INSERT INTO settlements (
    id, contract_id, consideration_id, status,
    settlement_attributes, settlement_attributes_context, settlement_attributes_type
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT (id, contract_id) DO UPDATE SET
    consideration_id              = EXCLUDED.consideration_id,
    status                        = EXCLUDED.status,
    settlement_attributes         = EXCLUDED.settlement_attributes,
    settlement_attributes_context = EXCLUDED.settlement_attributes_context,
    settlement_attributes_type    = EXCLUDED.settlement_attributes_type,
    updated_at                    = NOW();
