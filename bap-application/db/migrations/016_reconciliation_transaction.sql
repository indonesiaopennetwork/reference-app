-- ION reconciliation without reconciliationId (ion-specs PR #25): an
-- exchange is identified on the wire by context.transactionId, each round by
-- context.messageId. reconciliations.id stays this BAP's own name for the
-- exchange; transaction_id is the exchange's transactionId, by which a
-- resend and an answer find it. status of an answered batch is the
-- answer's summary (AGREED | DISPUTED | PENDING); each settlement's own
-- outcome is on the settlement.

ALTER TABLE reconciliations ADD COLUMN IF NOT EXISTS transaction_id TEXT;
CREATE INDEX IF NOT EXISTS idx_reconciliations_transaction ON reconciliations (direction, transaction_id);
CREATE INDEX IF NOT EXISTS idx_reconciliations_request_message ON reconciliations (request_message_id);

---- create above / drop below ----

DROP INDEX IF EXISTS idx_reconciliations_request_message;
DROP INDEX IF EXISTS idx_reconciliations_transaction;
ALTER TABLE reconciliations DROP COLUMN IF EXISTS transaction_id;
