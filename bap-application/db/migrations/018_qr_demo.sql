-- Demo scenarios for the QR scan simulator (internal/qrsvc/demo.go). When
-- QR_DEMO_ENABLED is on, a scan can ask for a scenario (SINGLE, MULTI,
-- QUOTE_CHANGED): this BAP then plays the merchants itself instead of calling
-- discover / select / init / confirm, so the whole buyer flow can be tried
-- without a working Beckn network. NULL = a real checkout.

ALTER TABLE qr_candidates ADD COLUMN IF NOT EXISTS demo_scenario TEXT;
ALTER TABLE qr_orders     ADD COLUMN IF NOT EXISTS demo_scenario TEXT;

---- create above / drop below ----

ALTER TABLE qr_orders     DROP COLUMN IF EXISTS demo_scenario;
ALTER TABLE qr_candidates DROP COLUMN IF EXISTS demo_scenario;
