-- The catalog's ION Catalogue Resource Category (CRC, e.g. TRC-fashion; see
-- ion-specs docs/ION_Resource_Categories.md), chosen when the catalog is
-- created. The seller app shows that category's attributes for its products
-- and publishes each product with resourceAttributes.crc set to it.

ALTER TABLE catalogs ADD COLUMN IF NOT EXISTS crc TEXT;

---- create above / drop below ----

ALTER TABLE catalogs DROP COLUMN IF EXISTS crc;
