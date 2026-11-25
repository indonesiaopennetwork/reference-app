-- QR packet checkout (internal/qrsvc). A buyer scans a packet QR printed by
-- ion-qr-code's qr-service; this BAP finds the merchants near the packet's
-- node that stock its items, opens a normal Beckn transaction with the one
-- chosen. (Collecting payment is not part of this flow yet.)
--
-- The packet itself stays in qr-service: rows here copy only its id/version
-- and the fields checkout needs, so an edited packet never changes an order
-- already under way.

-- One merchant offer for a scanned packet, as resolve found it. The buyer
-- picks one (or resolve picks the only one) and quote turns it into a
-- transaction. lines are the select items, ready to send.
CREATE TABLE qr_candidates (
    id                 UUID PRIMARY KEY,
    packet_id          TEXT        NOT NULL,
    packet_version     INTEGER     NOT NULL,
    scan_id            UUID,
    -- sha256 hex of the guest token handed to the browser at resolve; every
    -- later call of the same checkout must present the token.
    guest_token_hash   TEXT        NOT NULL,
    buyer_subject      TEXT,
    bpp_id             TEXT        NOT NULL,
    bpp_uri            TEXT        NOT NULL,
    provider_id        TEXT        NOT NULL,
    provider_name      TEXT        NOT NULL,
    -- NULL when the merchant's catalog states no location.
    distance_meters    INTEGER,
    lines              JSONB       NOT NULL,
    estimated_subtotal NUMERIC(14,2) NOT NULL,
    delivery_fee       NUMERIC(14,2),
    currency           TEXT        NOT NULL,
    -- The packet's node and fulfilment rules, copied for checkout.
    node               JSONB       NOT NULL,
    -- The candidate as resolve showed it to the buyer.
    view               JSONB       NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at         TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_qr_candidates_packet ON qr_candidates (packet_id, created_at DESC);

-- A QR checkout: one Beckn transaction opened from a candidate.
-- state: QUOTED (on_select in) → INIT_SENT → AWAITING_CONFIRM (on_init in)
--        → CONFIRM_SENT → CONFIRMED; FAILED on any dead end.
CREATE TABLE qr_orders (
    transaction_id    UUID PRIMARY KEY REFERENCES transactions (transaction_id),
    candidate_id      UUID        NOT NULL REFERENCES qr_candidates (id),
    packet_id         TEXT        NOT NULL,
    packet_version    INTEGER     NOT NULL,
    scan_id           UUID,
    guest_token_hash  TEXT        NOT NULL,
    buyer_subject     TEXT,
    provider_name     TEXT        NOT NULL,
    state             TEXT        NOT NULL DEFAULT 'QUOTED',
    -- The seller's on_select total, then its on_init total once known.
    quoted_total      NUMERIC(14,2),
    currency          TEXT        NOT NULL,
    fulfillment_mode  TEXT,
    unit_label        TEXT,
    buyer_name        TEXT,
    buyer_phone       TEXT,
    last_error        TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_qr_orders_packet ON qr_orders (packet_id, created_at DESC);

---- create above / drop below ----

DROP TABLE IF EXISTS qr_orders;
DROP TABLE IF EXISTS qr_candidates;
