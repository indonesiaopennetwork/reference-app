-- DOKU Checkout payment sessions: one row per attempt to create a DOKU
-- hosted-checkout session for a transaction. invoice_number is what this BAP
-- sends DOKU as order.invoice_number and gets back on the async notification
-- webhook, so it's the join key between "we asked DOKU to collect payment"
-- and "DOKU told us what happened". status PENDING -> PAID/FAILED/EXPIRED is
-- driven entirely by the notification webhook (internal/dokunotifysvc), not
-- by the browser's return redirect.

CREATE TYPE payment_session_status AS ENUM (
    'PENDING',  -- checkout session created, DOKU hasn't notified us yet
    'PAID',     -- DOKU notification reported transaction.status SUCCESS
    'FAILED',   -- DOKU notification reported a failed/voided transaction
    'EXPIRED'   -- payment_due_date passed with no notification
);

ALTER TYPE transaction_status ADD VALUE IF NOT EXISTS 'AWAITING_PAYMENT';

CREATE TABLE payment_sessions (
    id                UUID                    NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    transaction_id    UUID                    NOT NULL REFERENCES transactions(transaction_id),
    invoice_number    TEXT                    NOT NULL UNIQUE,
    doku_token_id     TEXT,
    status            payment_session_status  NOT NULL DEFAULT 'PENDING',
    gateway_ref       TEXT,
    method            TEXT,
    amount            NUMERIC(14, 2)          NOT NULL,
    paid_at           TIMESTAMPTZ,
    raw_notification  JSONB,
    created_at        TIMESTAMPTZ             NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ             NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payment_sessions_transaction ON payment_sessions (transaction_id);

COMMENT ON TABLE payment_sessions IS
    'One row per DOKU Checkout session created for a transaction. '
    'invoice_number is the key DOKU''s async notification webhook reports back.';

---- create above / drop below ----

DROP TABLE IF EXISTS payment_sessions;

-- Enum values cannot be dropped in PostgreSQL; AWAITING_PAYMENT stays.
DROP TYPE IF EXISTS payment_session_status;
