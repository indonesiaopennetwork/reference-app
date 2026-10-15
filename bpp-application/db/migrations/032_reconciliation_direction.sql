-- Reconciliation per ION Release 1 core/reconcile/v1: either participant may
-- send /reconcile. direction says who sent the batch: OUTBOUND (this BPP sent
-- /reconcile; response_payload is the BAP's /on_reconcile) or INBOUND (a BAP
-- sent it; response_payload is this BPP's /on_reconcile). status is SENDING |
-- SENT | FAILED (outbound delivery) or AGREED | PARTIALLY_AGREED | DISPUTED.

ALTER TABLE reconciliations ADD COLUMN IF NOT EXISTS direction TEXT NOT NULL DEFAULT 'OUTBOUND';

---- create above / drop below ----

ALTER TABLE reconciliations DROP COLUMN IF EXISTS direction;
