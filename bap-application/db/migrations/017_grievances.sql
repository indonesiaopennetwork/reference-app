-- Order-level grievances (ion-specs Grievance/v1): every /support and
-- /on_support carries one IONGrievance event in
-- message.support.supportAttributes. See internal/grievance.
-- buyer_subject scopes a grievance to the buyer who owns the order.

CREATE TABLE IF NOT EXISTS grievances (
    grievance_id        TEXT        PRIMARY KEY,          -- IONGrievance.grievanceId
    transaction_id      UUID        NOT NULL,             -- the order's transaction
    order_id            TEXT        NOT NULL,             -- Support.orderId
    bap_id              TEXT        NOT NULL,
    bap_uri             TEXT        NOT NULL,
    bpp_id              TEXT        NOT NULL,
    bpp_uri             TEXT        NOT NULL,
    network_id          TEXT,
    originating_np_id   TEXT        NOT NULL,             -- BAP: buyer grievance, BPP: seller grievance
    originating_side    TEXT        NOT NULL,             -- BUYER | SELLER (raisedBy.side)
    category            TEXT        NOT NULL,             -- DAMAGED_ITEM, WRONG_RETURN, REFUND_NOT_RECEIVED, ...
    description         TEXT        NOT NULL,
    affected            JSONB       NOT NULL DEFAULT '[]', -- [{commitmentId, performanceId, quantity}]
    related_references  JSONB       NOT NULL DEFAULT '[]', -- [{refType, refId}]
    policy_ref          TEXT        NOT NULL,             -- grievance SLA policy of the order
    case_status         TEXT        NOT NULL,             -- OPEN | IN_PROGRESS | RESOLVED | CLOSED
    last_event_type     TEXT        NOT NULL,
    last_event_at       TIMESTAMPTZ NOT NULL,
    opened_at           TIMESTAMPTZ NOT NULL,
    acknowledged_at     TIMESTAMPTZ,
    resolved_at         TIMESTAMPTZ,
    closed_at           TIMESTAMPTZ,
    channels            JSONB,                            -- latest Support.channels received
    seen_at             TIMESTAMPTZ,                      -- last opened by the user (unread marker)
    buyer_subject       TEXT,                             -- transactions.buyer_subject of the order
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_grievances_transaction ON grievances (transaction_id);
CREATE INDEX IF NOT EXISTS idx_grievances_buyer ON grievances (buyer_subject, last_event_at DESC);

-- Every event of a grievance, received or sent: the record the other three
-- tables are rebuilt from (internal/grievance.Store).
CREATE TABLE IF NOT EXISTS grievance_events (
    id                  UUID        PRIMARY KEY,
    seq                 BIGINT      GENERATED ALWAYS AS IDENTITY,
    grievance_id        TEXT        NOT NULL REFERENCES grievances (grievance_id) ON DELETE CASCADE,
    message_id          UUID        NOT NULL,             -- context.messageId
    action              TEXT        NOT NULL CHECK (action IN ('support', 'on_support')),
    direction           TEXT        NOT NULL CHECK (direction IN ('INBOUND', 'OUTBOUND')),
    event_type          TEXT        NOT NULL,
    -- SENDING (stored before the POST) | ACK | NACK | FAILED. Only SENDING
    -- and ACK count towards the grievance's state.
    delivery            TEXT        NOT NULL CHECK (delivery IN ('SENDING', 'ACK', 'NACK', 'FAILED')),
    attributes          JSONB       NOT NULL,             -- the IONGrievance as sent or received
    error               TEXT,
    occurred_at         TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_grievance_events_grievance ON grievance_events (grievance_id, seq);

-- Every resolution version proposed, with the decision on it.
CREATE TABLE IF NOT EXISTS grievance_resolutions (
    grievance_id        TEXT        NOT NULL REFERENCES grievances (grievance_id) ON DELETE CASCADE,
    proposal_id         TEXT        NOT NULL,
    version             INT         NOT NULL,
    proposed_by_np_id   TEXT        NOT NULL,
    authorization_type  TEXT        NOT NULL,             -- EXPLICIT_AGREEMENT | POLICY_AUTHORIZED | UNILATERAL
    resolution          JSONB       NOT NULL,
    decision_result     TEXT,                             -- ACCEPTED | REJECTED
    decided_by_np_id    TEXT,
    decided_at          TIMESTAMPTZ,
    decision_reason     TEXT,
    PRIMARY KEY (grievance_id, proposal_id, version)
);

-- Executed actions of the active resolution version.
CREATE TABLE IF NOT EXISTS grievance_executions (
    grievance_id        TEXT        NOT NULL REFERENCES grievances (grievance_id) ON DELETE CASCADE,
    proposal_id         TEXT        NOT NULL,
    version             INT         NOT NULL,
    action_id           TEXT        NOT NULL,
    executed_by_np_id   TEXT        NOT NULL,
    execution_reference TEXT,
    completed_at        TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (grievance_id, proposal_id, version, action_id)
);

COMMENT ON TABLE grievances IS
    'Order-level grievances (ION Grievance v1, /support and /on_support): current state, rebuilt from grievance_events.';
COMMENT ON TABLE grievance_events IS
    'Every grievance event (IONGrievance) received or sent; the record grievances, grievance_resolutions and grievance_executions are rebuilt from.';
COMMENT ON TABLE grievance_resolutions IS
    'Resolution versions proposed for a grievance, each with the decision on it.';
COMMENT ON TABLE grievance_executions IS
    'Executed resolution actions, with their execution references.';

---- create above / drop below ----

DROP TABLE IF EXISTS grievance_executions;
DROP TABLE IF EXISTS grievance_resolutions;
DROP TABLE IF EXISTS grievance_events;
DROP TABLE IF EXISTS grievances;
