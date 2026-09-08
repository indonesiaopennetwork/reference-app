-- Fixes 027_provider_scoping.sql's contracts.provider_id trigger: it
-- watched commitment_resources, but nothing in this codebase ever writes
-- to that table -- the /init handler stores committed resource IDs on
-- commitment_offers.resource_ids instead (see UpsertCommitmentOffer in
-- db/queries/init.sql, called from internal/initsvc/init_service.go).
-- Result: contracts.provider_id stayed NULL for every order ever placed,
-- making every seller's own orders (and, transitively, their ratings and
-- support tickets, both scoped via contracts.provider_id) invisible in
-- their own dashboard.

DROP TRIGGER IF EXISTS trg_contracts_set_provider_id ON commitment_resources;

CREATE OR REPLACE FUNCTION fn_contracts_set_provider_id_from_commitment()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    contract_bpp_id      TEXT;
    resolved_provider_id TEXT;
BEGIN
    IF NEW.resource_ids IS NULL OR array_length(NEW.resource_ids, 1) IS NULL THEN
        RETURN NEW;
    END IF;

    SELECT bpp_id INTO contract_bpp_id FROM contracts WHERE id = NEW.contract_id;

    SELECT provider_id INTO resolved_provider_id
    FROM resources
    WHERE id = NEW.resource_ids[1] AND bpp_id = contract_bpp_id
    LIMIT 1;

    IF resolved_provider_id IS NOT NULL THEN
        UPDATE contracts SET provider_id = resolved_provider_id WHERE id = NEW.contract_id;
    END IF;

    RETURN NEW;
END;
$$;

-- Fires on both a first-time insert and the ON CONFLICT DO UPDATE path
-- UpsertCommitmentOffer actually uses, so a later /init revision to the
-- same commitment/contract keeps provider_id current too. Always
-- recomputes (not conditioned on provider_id IS NULL) for the same reason.
CREATE TRIGGER trg_contracts_set_provider_id
    AFTER INSERT OR UPDATE OF resource_ids ON commitment_offers
    FOR EACH ROW EXECUTE FUNCTION fn_contracts_set_provider_id_from_commitment();

-- Backfill every existing contract -- this is what actually fixes orders
-- placed before this migration.
UPDATE contracts c
SET provider_id = sub.provider_id
FROM (
    SELECT DISTINCT ON (co.contract_id) co.contract_id, r.provider_id
    FROM commitment_offers co
    JOIN contracts ct ON ct.id = co.contract_id
    JOIN resources r ON r.id = co.resource_ids[1] AND r.bpp_id = ct.bpp_id
    WHERE co.resource_ids IS NOT NULL AND array_length(co.resource_ids, 1) > 0
    ORDER BY co.contract_id, co.created_at
) sub
WHERE c.id = sub.contract_id;

---- create above / drop below ----

DROP TRIGGER IF EXISTS trg_contracts_set_provider_id ON commitment_offers;
DROP FUNCTION IF EXISTS fn_contracts_set_provider_id_from_commitment();

-- Restore 027's original (harmless-but-inert) trigger definition so the
-- down migration leaves schema state matching what 027 alone produced.
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
