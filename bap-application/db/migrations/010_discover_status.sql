-- Add new transaction lifecycle statuses for discover actions.

ALTER TYPE transaction_status ADD VALUE IF NOT EXISTS 'DISCOVER_SENT';
ALTER TYPE transaction_status ADD VALUE IF NOT EXISTS 'DISCOVER_RECEIVED';

---- create above / drop below ----

-- Enum values cannot be dropped in PostgreSQL; migration is intentionally irreversible.
