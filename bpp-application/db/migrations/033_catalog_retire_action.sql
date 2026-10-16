-- Audit-log action for retiring a catalog (DELETE /catalogs/:id), the
-- one-way NFH-014 retiredAt mechanism -- distinct from 'catalog_publish'.

ALTER TYPE beckn_action ADD VALUE IF NOT EXISTS 'catalog_retire';

---- create above / drop below ----

-- enum values added to beckn_action are left in place (Postgres cannot drop them).
