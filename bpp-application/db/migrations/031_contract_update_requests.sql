-- Buyer-requested contract updates (Beckn /update), held for seller
-- approval rather than applied automatically -- see updatesvc.applyUpdate's
-- own comment. A row here is the proposed change; contracts.contract_attributes
-- is only mutated once a seller approves it (see ApproveUpdateRequest).
-- Deliberately a separate table rather than a new contracts.status value --
-- "this ACTIVE contract has a pending proposed change" is an orthogonal axis
-- to the contract's own DRAFT/ACTIVE/CANCELLED/COMPLETE lifecycle, not a
-- replacement for it.

CREATE TYPE update_request_status AS ENUM (
    'PENDING',
    'APPROVED',
    'REJECTED'
);

CREATE TABLE contract_update_requests (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    contract_id          UUID        NOT NULL REFERENCES contracts (id),
    transaction_id       UUID        NOT NULL,
    bap_id               TEXT        NOT NULL,
    bap_uri              TEXT        NOT NULL,

    -- The buyer's proposed full contract (PUT semantics -- see
    -- applyUpdate's own doc comment on why this is a full replacement,
    -- not a merge patch).
    requested_attributes JSONB       NOT NULL,

    status               update_request_status NOT NULL DEFAULT 'PENDING',
    reject_reason        TEXT,

    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at           TIMESTAMPTZ
);

COMMENT ON TABLE contract_update_requests IS
    'Buyer-proposed contract mutations awaiting seller approval/rejection. '
    'Only APPROVED requests are ever applied to contracts.contract_attributes.';

CREATE INDEX idx_update_requests_pending ON contract_update_requests (contract_id, status)
    WHERE status = 'PENDING';

---- create above / drop below ----

DROP TABLE IF EXISTS contract_update_requests;
DROP TYPE IF EXISTS update_request_status;
