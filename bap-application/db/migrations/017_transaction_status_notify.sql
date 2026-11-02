-- Push transaction status changes to bap-frontend instead of having it poll
-- GET /api/v1/status/:id. Every write to a transaction's row or a new
-- contract snapshot sends NOTIFY bap_transaction_status '<transaction_id>';
-- bap-application LISTENs on that channel (internal/statusstream) and
-- forwards it to the open GET /api/v1/status/:id/stream connections for
-- that transaction. Doing it here rather than in Go covers every writer of
-- these tables (the on_* callbacks and the client actions alike), and a
-- NOTIFY reaches every bap instance, not just the one that took the
-- callback. NOTIFY is delivered on commit, so a listener that re-reads the
-- row always sees the new state.

CREATE OR REPLACE FUNCTION notify_transaction_status() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('bap_transaction_status', NEW.transaction_id::text);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION notify_transaction_status() IS
    'Sends NOTIFY bap_transaction_status with the row''s transaction_id; drives GET /api/v1/status/:id/stream.';

CREATE TRIGGER transactions_notify_status
    AFTER INSERT OR UPDATE ON transactions
    FOR EACH ROW EXECUTE FUNCTION notify_transaction_status();

-- on_* callbacks store the contract snapshot after updating the status, so
-- the snapshot needs its own notification for the stream to carry it.
CREATE TRIGGER contract_snapshots_notify_status
    AFTER INSERT ON contract_snapshots
    FOR EACH ROW EXECUTE FUNCTION notify_transaction_status();

---- create above / drop below ----

DROP TRIGGER IF EXISTS contract_snapshots_notify_status ON contract_snapshots;
DROP TRIGGER IF EXISTS transactions_notify_status ON transactions;
DROP FUNCTION IF EXISTS notify_transaction_status();
