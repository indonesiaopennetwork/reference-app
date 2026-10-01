-- The seller's reason for their decision, as received over the wire.
--
-- Until now the buyer could only be told THAT a change was declined, never
-- why -- so nothing stopped them re-sending the same request, which costs
-- the seller a fresh evaluation each time. The reason now travels on
-- on_update inside contract.contractAttributes.updateDecision (see
-- internal/on_update_svc/decision.go for why that is schema-legal), and
-- these columns are where it lands.
--
-- Two fields, following ION's own convention for an outcome reason
-- (TradeContract's cancellation.reason is a beckn Descriptor, where
-- Descriptor.code is the machine-comparable code and the prose is human
-- text). Keeping them apart means the UI can show a stable label per code
-- while still surfacing whatever the seller typed.
ALTER TABLE update_requests
    ADD COLUMN decision_reason_code TEXT,
    ADD COLUMN decision_reason_text TEXT;

COMMENT ON COLUMN update_requests.decision_reason_code IS
    'Descriptor.code from the seller''s updateDecision.reason, e.g. ALREADY_DISPATCHED. NULL when the seller''s app sent no reason.';
COMMENT ON COLUMN update_requests.decision_reason_text IS
    'Human-readable reason text from the seller, shown to the buyer as-is.';

---- create above / drop below ----

ALTER TABLE update_requests
    DROP COLUMN decision_reason_code,
    DROP COLUMN decision_reason_text;
