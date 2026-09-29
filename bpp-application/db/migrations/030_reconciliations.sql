-- Reconciliation batches (Beckn extension /reconcile, see
-- docs/specs/reconcile.yaml): one row per /reconcile the BPP sends to a BAP,
-- with the BAP's /on_reconcile outcome.

ALTER TYPE beckn_action ADD VALUE IF NOT EXISTS 'reconcile';
ALTER TYPE beckn_action ADD VALUE IF NOT EXISTS 'on_reconcile';

CREATE TABLE IF NOT EXISTS reconciliations (
    id                  TEXT        PRIMARY KEY,          -- reconciliationId
    bpp_id              TEXT        NOT NULL,
    bap_id              TEXT        NOT NULL,
    provider_id         TEXT,
    settlement_date     DATE        NOT NULL,
    -- SENT (ACKed, awaiting on_reconcile) | FAILED (not delivered) |
    -- PENDING | AGREED | DISPUTED | ADJUSTED (from on_reconcile)
    status              TEXT        NOT NULL,
    contract_ids        UUID[]      NOT NULL,
    summary             JSONB       NOT NULL,             -- settlementSummary sent
    request_message_id  UUID        NOT NULL,
    request_payload     JSONB,
    response_payload    JSONB,                            -- latest on_reconcile
    error_message       TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_reconciliations_bpp_created ON reconciliations (bpp_id, created_at DESC);

---- create above / drop below ----

DROP TABLE IF EXISTS reconciliations;
-- enum values added to beckn_action are left in place (Postgres cannot drop them).
