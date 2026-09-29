package sweep

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "host=localhost port=5435 dbname=bap user=postgres password=postgres sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("skipping: could not open test database pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skipping: test database not reachable: %v", err)
	}
	return pool
}

// seedUpdateSentTransaction seeds a transaction at UPDATE_SENT with a
// backdated updated_at, since the sweep's own eligibility check
// (RollBackStaleUpdateSent) is purely age-based.
func seedUpdateSentTransaction(t *testing.T, pool *pgxpool.Pool, age time.Duration) uuid.UUID {
	t.Helper()
	txnID := uuid.New()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO transactions (transaction_id, bap_id, network_id, bpp_id, bpp_uri, status, updated_at)
		VALUES ($1, $2, 'test-network', 'test-bpp', 'http://bpp.test/bpp/receiver', 'UPDATE_SENT', NOW() - $3::interval)
	`, txnID, "onix-bap-x7m2wmy7nq-et.a.run.app", age.String()); err != nil {
		t.Fatalf("seed stale transaction: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM transactions WHERE transaction_id = $1`, txnID) //nolint:errcheck
	})
	return txnID
}

func TestRunOnce_RollsBackTrulyStaleTransactions(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	lh := logharbour.NewLogger(logharbour.NewLoggerContext(logharbour.Debug2), "test", os.Stdout)

	stale := seedUpdateSentTransaction(t, pool, 72*time.Hour)

	n, err := RunOnce(context.Background(), pool, lh, 48*time.Hour)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected at least 1 transaction rolled back, got %d", n)
	}

	q := dbsqlc.New(pool)
	txn, err := q.GetTransaction(context.Background(), stale)
	if err != nil {
		t.Fatalf("re-fetch transaction: %v", err)
	}
	if txn.Status != dbsqlc.TransactionStatusCONFIRMED {
		t.Fatalf("expected the stale transaction to be rolled back to CONFIRMED, got %s", txn.Status)
	}
}

// TestRunOnce_LeavesRecentUpdateSentAlone is the core of this phase's
// design: a transaction that's only recently gone to UPDATE_SENT --
// plausibly still awaiting seller review -- must NOT be touched by a run
// using the same (long) production threshold.
func TestRunOnce_LeavesRecentUpdateSentAlone(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	lh := logharbour.NewLogger(logharbour.NewLoggerContext(logharbour.Debug2), "test", os.Stdout)

	recent := seedUpdateSentTransaction(t, pool, 1*time.Hour)

	if _, err := RunOnce(context.Background(), pool, lh, DefaultStaleAfter); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	q := dbsqlc.New(pool)
	txn, err := q.GetTransaction(context.Background(), recent)
	if err != nil {
		t.Fatalf("re-fetch transaction: %v", err)
	}
	if txn.Status != dbsqlc.TransactionStatusUPDATESENT {
		t.Fatalf("expected a recently-UPDATE_SENT transaction to be left alone (still awaiting review), got %s", txn.Status)
	}
}
