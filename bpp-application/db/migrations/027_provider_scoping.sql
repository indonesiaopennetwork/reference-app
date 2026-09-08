-- Denormalizes provider_id onto resources and contracts so seller-dashboard
-- queries can filter "my data" with a plain WHERE clause, matching how
-- bpp_id is already denormalized onto every one of these tables. Kept in
-- sync going forward by triggers (mirroring the resources.search_vector /
-- *.updated_at trigger pattern in 021_triggers.sql), not maintained by
-- application code.
--
-- resource_stock is deliberately NOT given a provider_id column: it's keyed
-- by (resource_id, bpp_id) only and, per its own migration comment (023),
-- is an intentionally SHARED counter "across all catalogs that reference
-- the same resource ID" — a single resource_id can be shared by resources
-- belonging to different providers. Adding one owning provider_id here
-- would be actively wrong, not just imprecise. Seller-scoped inventory
-- reads join resource_stock through resources (which does carry an
-- unambiguous provider_id per catalog) instead.

-- ---------------------------------------------------------------------------
-- resources.provider_id — unambiguous: one resource row belongs to exactly
-- one catalog, and one catalog belongs to exactly one provider.
-- ---------------------------------------------------------------------------

ALTER TABLE resources ADD COLUMN provider_id TEXT;

UPDATE resources r
SET provider_id = c.provider_id
FROM catalogs c
WHERE r.catalog_id = c.id AND r.bpp_id = c.bpp_id;

ALTER TABLE resources ALTER COLUMN provider_id SET NOT NULL;

CREATE INDEX idx_resources_provider_id ON resources (provider_id, bpp_id);

CREATE OR REPLACE FUNCTION fn_resources_set_provider_id()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    SELECT provider_id INTO NEW.provider_id
    FROM catalogs
    WHERE id = NEW.catalog_id AND bpp_id = NEW.bpp_id;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_resources_set_provider_id
    BEFORE INSERT OR UPDATE OF catalog_id, bpp_id ON resources
    FOR EACH ROW EXECUTE FUNCTION fn_resources_set_provider_id();

-- ---------------------------------------------------------------------------
-- contracts.provider_id — best-effort: a contract only gains resource
-- linkage once its first commitment_resources row is inserted (contracts
-- themselves carry no resource reference). Resolved from whichever
-- resource is committed first; nullable until that happens, and left
-- unresolved if commitment_resources.resource_id doesn't match any
-- resources row for the contract's bpp_id (pre-existing looseness in that
-- table already — see 009_commitments.sql, resource_id has no FK there).
-- ---------------------------------------------------------------------------

ALTER TABLE contracts ADD COLUMN provider_id TEXT;

CREATE INDEX idx_contracts_provider_id ON contracts (provider_id, bpp_id);

CREATE OR REPLACE FUNCTION fn_contracts_set_provider_id_from_commitment()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    contract_bpp_id      TEXT;
    resolved_provider_id TEXT;
BEGIN
    SELECT bpp_id INTO contract_bpp_id FROM contracts WHERE id = NEW.contract_id;

    SELECT provider_id INTO resolved_provider_id
    FROM resources
    WHERE id = NEW.resource_id AND bpp_id = contract_bpp_id
    LIMIT 1;

    IF resolved_provider_id IS NOT NULL THEN
        UPDATE contracts
        SET provider_id = resolved_provider_id
        WHERE id = NEW.contract_id AND provider_id IS NULL;
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_contracts_set_provider_id
    AFTER INSERT ON commitment_resources
    FOR EACH ROW EXECUTE FUNCTION fn_contracts_set_provider_id_from_commitment();

-- Backfill existing contracts from their existing commitment_resources rows
-- (first-committed resource per contract, by insertion order).
UPDATE contracts c
SET provider_id = sub.provider_id
FROM (
    SELECT DISTINCT ON (cr.contract_id) cr.contract_id, r.provider_id
    FROM commitment_resources cr
    JOIN contracts ct ON ct.id = cr.contract_id
    JOIN resources r ON r.id = cr.resource_id AND r.bpp_id = ct.bpp_id
    ORDER BY cr.contract_id, cr.created_at
) sub
WHERE c.id = sub.contract_id;

---- create above / drop below ----

DROP TRIGGER IF EXISTS trg_contracts_set_provider_id ON commitment_resources;
DROP FUNCTION IF EXISTS fn_contracts_set_provider_id_from_commitment();
ALTER TABLE contracts DROP COLUMN IF EXISTS provider_id;

DROP TRIGGER IF EXISTS trg_resources_set_provider_id ON resources;
DROP FUNCTION IF EXISTS fn_resources_set_provider_id();
ALTER TABLE resources DROP COLUMN IF EXISTS provider_id;
