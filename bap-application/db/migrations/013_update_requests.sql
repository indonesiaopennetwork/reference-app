-- The BAP side's own record of every /update the buyer sent, and what the
-- seller ultimately decided about it.
--
-- Why this is needed at all: on_update's wire shape carries a contract and
-- nothing else -- there is no field anywhere in the Beckn v2 callback for
-- "approved" / "rejected, because X". A BPP that rejects an update replies
-- with the contract UNCHANGED, which on the wire is indistinguishable from
-- a BPP that approved an update that happened to change nothing. Without
-- remembering what the buyer actually proposed, the BAP can only tell the
-- buyer "your order is back to CONFIRMED" and nothing more.
--
-- So the decision is recovered by comparison, not by trusting a new field:
-- keep the proposed contract here, and when on_update arrives, compare it
-- against both the proposal and the pre-update snapshot (see
-- internal/on_update_svc.classifyOutcome). That deliberately adds NO new
-- wire extension -- same reasoning as the Support flow's "no ion:* schema
-- for v1" decision, and it avoids the class of onix Extended-Schema
-- validation failure an unpublished new field would guarantee.

CREATE TYPE update_outcome AS ENUM (
    -- Sent, no on_update back yet.
    'PENDING',
    -- on_update returned exactly what the buyer proposed: seller approved.
    'APPLIED',
    -- on_update returned the pre-update contract unchanged: seller rejected.
    'DECLINED',
    -- on_update returned something matching neither: the seller responded
    -- with a modified contract (a counter-offer), or an intermediary
    -- normalised the payload in transit. Deliberately NOT collapsed into
    -- APPLIED or DECLINED -- guessing here would show the buyer a decision
    -- the seller never made.
    'MODIFIED',
    -- No on_update ever arrived and the transaction was swept back to
    -- CONFIRMED (see internal/sweep), or delivery failed outright.
    'ABANDONED'
);

CREATE TABLE update_requests (
    id                UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id    UUID           NOT NULL REFERENCES transactions (transaction_id),

    -- The messageId of the outbound /update this row represents. Stored
    -- for audit correlation against beckn_message_log; it is NOT what the
    -- on_update callback is matched on, because every on_* service in this
    -- codebase mints a fresh messageId for its callback rather than
    -- echoing the request's (correlation here is by transaction_id plus
    -- the UPDATE_SENT compare-and-swap, same as the rest of the app).
    message_id        UUID           NOT NULL,

    -- The full contract the buyer proposed, exactly as sent on the wire.
    proposed_contract JSONB          NOT NULL,

    outcome           update_outcome NOT NULL DEFAULT 'PENDING',

    created_at        TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    decided_at        TIMESTAMPTZ
);

COMMENT ON TABLE update_requests IS
    'Buyer-side record of outbound /update requests and the seller decision '
    'inferred from the matching on_update callback. Never authoritative for '
    'contract state -- contract_snapshots is.';

-- One transaction has at most one update in flight at a time (enforced by
-- the CONFIRMED -> UPDATE_SENT compare-and-swap in MarkUpdateSentIfConfirmed),
-- so a partial unique index is both a lookup path and a safety net against
-- a second PENDING row ever appearing.
CREATE UNIQUE INDEX idx_update_requests_pending ON update_requests (transaction_id)
    WHERE outcome = 'PENDING';

CREATE INDEX idx_update_requests_txn ON update_requests (transaction_id, created_at DESC);

---- create above / drop below ----

DROP TABLE IF EXISTS update_requests;
DROP TYPE IF EXISTS update_outcome;
