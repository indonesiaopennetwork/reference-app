-- Adds the buyer's Zitadel subject to transactions, so order history can be
-- scoped to the buyer who placed it (see bap-application/internal/auth).
-- Nullable: existing rows, and any transaction created without a resolvable
-- buyer identity, simply have no owner and won't match any buyer's scoped
-- query.

ALTER TABLE transactions ADD COLUMN buyer_subject TEXT;

CREATE INDEX idx_transactions_buyer_subject ON transactions(buyer_subject) WHERE buyer_subject IS NOT NULL;

---- create above / drop below ----

DROP INDEX IF EXISTS idx_transactions_buyer_subject;
ALTER TABLE transactions DROP COLUMN buyer_subject;
