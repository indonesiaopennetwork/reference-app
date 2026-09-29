-- What the order looked like BEFORE the buyer's requested change.
--
-- Fixes a real bug: the seller's review screen computed "current vs
-- requested" against the LIVE contract. That reads correctly while the
-- request is PENDING, but the moment it is APPROVED the live contract
-- becomes the requested value -- so current == requested, the diff
-- collapses to zero rows, and the screen showed "No changes to delivery
-- details" for a change it had just applied. There was then no way to tell
-- what had been approved.
--
-- Captured once, when the request is queued, so it is a stable historical
-- record rather than something recomputed from mutable state. That also
-- makes the PENDING view immune to the contract changing underneath it
-- between queueing and review.
--
-- Stores the deliveryDetails object only (not the whole performance bag):
-- it is the sole part of the order this flow can change, and keeping it
-- narrow avoids copying PII that has no bearing on the decision.
ALTER TABLE contract_update_requests
    ADD COLUMN previous_delivery_details JSONB;

COMMENT ON COLUMN contract_update_requests.previous_delivery_details IS
    'Snapshot of contract_performances.performance_attributes.deliveryDetails taken when this request was queued. NULL for rows created before this column existed.';

---- create above / drop below ----

ALTER TABLE contract_update_requests
    DROP COLUMN previous_delivery_details;
