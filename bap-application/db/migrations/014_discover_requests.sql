-- Who made each discover (search) call. A discover has its own transactionId
-- but no row in transactions, so this is what lets the buyer's message log
-- show their own searches (discover / on_discover) and no one else's.

CREATE TABLE IF NOT EXISTS discover_requests (
    transaction_id UUID        PRIMARY KEY,
    buyer_subject  TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS discover_requests_buyer_idx ON discover_requests (buyer_subject, created_at DESC);

---- create above / drop below ----

DROP TABLE IF EXISTS discover_requests;
