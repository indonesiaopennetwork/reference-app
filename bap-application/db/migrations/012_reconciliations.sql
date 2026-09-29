-- Reconciliation batches received from BPPs (Beckn extension /reconcile, see
-- docs/specs/reconcile.yaml): what the BPP claimed, what this BAP computed
-- from its own records, the outcome, and the payout (the payment gateway's
-- release of the held funds) when agreed.

ALTER TYPE beckn_action ADD VALUE IF NOT EXISTS 'reconcile';
ALTER TYPE beckn_action ADD VALUE IF NOT EXISTS 'on_reconcile';

CREATE TABLE IF NOT EXISTS reconciliations (
    id                  TEXT        PRIMARY KEY,           -- reconciliationId
    bpp_id              TEXT        NOT NULL,
    settlement_date     DATE        NOT NULL,
    status              TEXT        NOT NULL,              -- AGREED | DISPUTED
    claimed_summary     JSONB       NOT NULL,              -- the BPP's settlementSummary
    computed_summary    JSONB       NOT NULL,              -- this BAP's own computation
    contracts           JSONB       NOT NULL,              -- per-contract verdicts
    payout              JSONB,                             -- gateway release, when AGREED
    request_payload     JSONB       NOT NULL,
    response_payload    JSONB,                             -- the on_reconcile sent
    error_message       TEXT,                              -- on_reconcile delivery failure
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

---- create above / drop below ----

DROP TABLE IF EXISTS reconciliations;
-- enum values added to beckn_action are left in place (Postgres cannot drop them).
