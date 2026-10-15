-- Reconciliation per ION Release 1 core/reconcile/v1: either participant may
-- send /reconcile, and the payout is made by SEAM through the PJP rather than
-- recorded here. direction says who sent the batch: INBOUND (a BPP sent
-- /reconcile; response_payload is this BAP's /on_reconcile) or OUTBOUND (this
-- BAP sent it; response_payload is the BPP's /on_reconcile).
-- claimed_summary is the sender's settlementSummary, computed_summary the
-- answerer's. status is SENDING | SENT | FAILED (outbound delivery) or
-- AGREED | PARTIALLY_AGREED | DISPUTED. payout is no longer written.

ALTER TABLE reconciliations ADD COLUMN IF NOT EXISTS direction TEXT NOT NULL DEFAULT 'INBOUND';
ALTER TABLE reconciliations ADD COLUMN IF NOT EXISTS request_message_id UUID;
ALTER TABLE reconciliations ALTER COLUMN computed_summary DROP NOT NULL;
ALTER TABLE reconciliations ALTER COLUMN contracts DROP NOT NULL;

---- create above / drop below ----

ALTER TABLE reconciliations DROP COLUMN IF EXISTS request_message_id;
ALTER TABLE reconciliations DROP COLUMN IF EXISTS direction;
