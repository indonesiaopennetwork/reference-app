-- When the contract's order was delivered: the start of the return window
-- (offer policies.returns.window). Set by the seller dashboard when the
-- fulfillment status becomes Delivered.
ALTER TABLE contracts ADD COLUMN IF NOT EXISTS delivered_at TIMESTAMPTZ;

---- create above / drop below ----

ALTER TABLE contracts DROP COLUMN IF EXISTS delivered_at;
