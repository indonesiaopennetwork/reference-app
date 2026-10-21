-- An offer's commission moves from its price consideration
-- (consideration_attributes.commission, one object) to its policies
-- (offer_attributes.policies.commission, named entries that add up; ION
-- IONTradeOffer). An offer saved with a commission on its price gets it as
-- one "Finder fee" entry, unless its policies already state one; the price
-- then carries none.

UPDATE offers o
   SET offer_attributes = jsonb_set(
         COALESCE(o.offer_attributes, '{}'::jsonb), '{policies,commission}',
         jsonb_build_array(jsonb_build_object('name', 'Finder fee') || c.commission), true)
  FROM (SELECT DISTINCT ON (offer_id, bpp_id) offer_id, bpp_id, consideration_attributes->'commission' AS commission
          FROM offer_considerations
         WHERE consideration_attributes ? 'commission'
         ORDER BY offer_id, bpp_id, consideration_id) c
 WHERE o.id = c.offer_id AND o.bpp_id = c.bpp_id
   AND jsonb_typeof(c.commission) = 'object'
   AND jsonb_array_length(COALESCE(o.offer_attributes->'policies'->'commission', '[]'::jsonb)) = 0;

UPDATE offer_considerations
   SET consideration_attributes = consideration_attributes - 'commission'
 WHERE consideration_attributes ? 'commission';

---- create above / drop below ----

-- The commission stays in the offers' policies: nothing to undo.
