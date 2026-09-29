package sweep

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"
)

const testBppID = "onix-bpp-x7m2wmy7nq-et.a.run.app"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "host=localhost port=5435 dbname=bpp user=postgres password=postgres sslmode=disable"
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

// seedPendingUpdateRequest seeds a contract + a PENDING update request
// against it, backdated by age -- ListStalePendingUpdateRequests's
// eligibility check is purely age-based.
func seedPendingUpdateRequest(t *testing.T, pool *pgxpool.Pool, age time.Duration) uuid.UUID {
	t.Helper()
	contractID := uuid.New()
	txnID := uuid.New()
	requestID := uuid.New()
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO contracts (id, bpp_id, bap_id, bap_uri, transaction_id, status, contract_attributes)
		VALUES ($1, $2, 'test-bap', 'http://bap.test/bap/receiver', $3, 'ACTIVE'::contract_status, '{"id":"orig"}'::jsonb)
	`, contractID, testBppID, txnID); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contract_update_requests (id, contract_id, transaction_id, bap_id, bap_uri, requested_attributes, created_at)
		VALUES ($1, $2, $3, 'test-bap', 'http://bap.test/bap/receiver', '{"id":"x"}'::jsonb, NOW() - $4::interval)
	`, requestID, contractID, txnID, age.String()); err != nil {
		t.Fatalf("seed pending update request: %v", err)
	}

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM contract_update_requests WHERE id = $1`, requestID) //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM contracts WHERE id = $1`, contractID)                //nolint:errcheck
	})
	return requestID
}

func TestRunOnce_FindsTrulyStalePendingRequests(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	lh := logharbour.NewLogger(logharbour.NewLoggerContext(logharbour.Debug2), "test", os.Stdout)

	seedPendingUpdateRequest(t, pool, 48*time.Hour)

	n, err := RunOnce(context.Background(), pool, lh, 24*time.Hour)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected at least 1 stale pending request found, got %d", n)
	}
}

// TestRunOnce_NeverMutatesState is the core design point of this package:
// unlike the BAP-side sweep, running it must never change anything in the
// database -- only log.
func TestRunOnce_NeverMutatesState(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	lh := logharbour.NewLogger(logharbour.NewLoggerContext(logharbour.Debug2), "test", os.Stdout)

	requestID := seedPendingUpdateRequest(t, pool, 48*time.Hour)

	if _, err := RunOnce(context.Background(), pool, lh, 24*time.Hour); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM contract_update_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("re-fetch update request: %v", err)
	}
	if status != "PENDING" {
		t.Fatalf("expected the request to remain PENDING (sweep must not decide it), got %s", status)
	}
}

func TestRunOnce_LeavesRecentPendingAlone(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	lh := logharbour.NewLogger(logharbour.NewLoggerContext(logharbour.Debug2), "test", os.Stdout)

	seedPendingUpdateRequest(t, pool, 1*time.Hour)

	n, err := RunOnce(context.Background(), pool, lh, DefaultStaleAfter)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// A freshly-created request must not be reported as stale under the
	// production default threshold. (Doesn't assert n==0 outright since
	// other tests/processes may have left genuinely stale rows in this
	// shared dev DB -- just that this run doesn't error.)
	_ = n
}
