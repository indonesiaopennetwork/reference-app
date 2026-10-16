-- Availability replaces the stock counter. A resource publishes only its
-- availability signal, resourceAttributes.availability.status (IN_STOCK,
-- LOW_STOCK, OUT_OF_STOCK, PREORDER, DISCONTINUED -- IONTradeResource), never
-- a stock count, so the BPP keeps no per-resource stock any more.
--
-- 1. Every resource without an availability gets status IN_STOCK. The old
--    counter can't tell sold-out from untracked (a blank stock was stored as
--    0, meaning unlimited), so it isn't used to derive one. A resource with
--    no attributes at all also gets the IONTradeResource @context and @type.
-- 2. resource_stock (on-hand and sold units, migration 023) is dropped. Its
--    data is not kept; the down migration recreates the table empty.

UPDATE resources
   SET resource_attributes = jsonb_set(
           COALESCE(resource_attributes, jsonb_build_object(
               '@context', 'https://schema.ion.id/release1/schema/extension/trade/TradeResource/v1/context.jsonld',
               '@type', 'ion:IONTradeResource')),
           '{availability}', jsonb_build_object('status', 'IN_STOCK'), true),
       resource_attributes_context = COALESCE(resource_attributes_context,
           'https://schema.ion.id/release1/schema/extension/trade/TradeResource/v1/context.jsonld'),
       resource_attributes_type = COALESCE(resource_attributes_type, 'ion:IONTradeResource')
 WHERE resource_attributes IS NULL
    OR jsonb_typeof(resource_attributes) <> 'object'
    OR NOT (resource_attributes ? 'availability');

DROP TABLE IF EXISTS resource_stock;

---- create above / drop below ----

CREATE TABLE IF NOT EXISTS resource_stock (
    resource_id   TEXT        NOT NULL,
    bpp_id        TEXT        NOT NULL,
    quantity      INTEGER     NOT NULL DEFAULT 0,
    sold          INTEGER     NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (resource_id, bpp_id)
);

COMMENT ON TABLE resource_stock IS
    'Per-resource inventory counter. quantity = remaining stock; sold = cumulative confirmed units.';
-- The resources' availability is left in place.
