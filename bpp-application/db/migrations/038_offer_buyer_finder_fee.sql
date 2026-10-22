-- The buyer finder fee moves from the offer's policies to its consideration
-- (ion-specs PR #24): offer_considerations.consideration_attributes
-- .buyerFinderFee {feeType, feeValue, appliesTo}, one fee per offer, instead
-- of offer_attributes.policies.commission (named entries, migration 036). An
-- offer's first policies.commission entry becomes its fee; a consideration
-- that still carries a commission (from before 036) has it renamed. Field
-- names stored the earlier way (type, value) become feeType, feeValue.

UPDATE offer_considerations c
   SET consideration_attributes = (c.consideration_attributes - 'commission') || jsonb_build_object('buyerFinderFee',
         jsonb_strip_nulls(jsonb_build_object(
           'feeType',   COALESCE(f.entry->>'feeType', f.entry->>'type'),
           'feeValue',  COALESCE(f.entry->'feeValue', f.entry->'value'),
           'appliesTo', CASE WHEN COALESCE(f.entry->>'feeType', f.entry->>'type') = 'PERCENT'
                             THEN COALESCE(f.entry->>'appliesTo', 'ITEM_TOTAL') END)))
  FROM (SELECT o.id, o.bpp_id, o.offer_attributes->'policies'->'commission'->0 AS entry
          FROM offers o
         WHERE jsonb_typeof(o.offer_attributes->'policies'->'commission') = 'array'
           AND jsonb_array_length(o.offer_attributes->'policies'->'commission') > 0) f
 WHERE c.offer_id = f.id AND c.bpp_id = f.bpp_id
   AND NOT (c.consideration_attributes ? 'buyerFinderFee');

UPDATE offer_considerations
   SET consideration_attributes = (consideration_attributes - 'commission') || jsonb_build_object('buyerFinderFee',
         jsonb_strip_nulls(jsonb_build_object(
           'feeType',   COALESCE(consideration_attributes->'commission'->>'feeType', consideration_attributes->'commission'->>'type'),
           'feeValue',  COALESCE(consideration_attributes->'commission'->'feeValue', consideration_attributes->'commission'->'value'),
           'appliesTo', CASE WHEN COALESCE(consideration_attributes->'commission'->>'feeType', consideration_attributes->'commission'->>'type') = 'PERCENT'
                             THEN COALESCE(consideration_attributes->'commission'->>'appliesTo', 'ITEM_TOTAL') END)))
 WHERE jsonb_typeof(consideration_attributes->'commission') = 'object'
   AND NOT (consideration_attributes ? 'buyerFinderFee');

UPDATE offers
   SET offer_attributes = offer_attributes #- '{policies,commission}'
 WHERE offer_attributes->'policies' ? 'commission';

---- create above / drop below ----

-- The fee stays on the offers' considerations: nothing to undo.
