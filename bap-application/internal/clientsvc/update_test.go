package clientsvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/discoverwait"
)

// testPool opens a pool against the local dev Postgres these tests need for
// their DB-level assertions (no test-DB harness exists in this repo yet --
// see the Update-flow edge-case task's "remaining gaps" notes). Skips
// instead of failing when it's not reachable, so `go test ./...` stays
// green in environments without the docker-compose stack running.
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

// seedConfirmedTransaction inserts a transaction row directly, bypassing
// the normal Select->Init->Confirm lifecycle, so each test can start from
// whatever status it needs to exercise.
func seedTransaction(t *testing.T, pool *pgxpool.Pool, status dbsqlc.TransactionStatus) uuid.UUID {
	t.Helper()
	txnID := uuid.New()
	q := dbsqlc.New(pool)
	bppID, bppURI := "test-bpp", "http://bpp.test/bpp/receiver"
	if err := q.UpsertTransaction(context.Background(), dbsqlc.UpsertTransactionParams{
		TransactionID: txnID,
		BapID:         "onix-bap-x7m2wmy7nq-et.a.run.app",
		NetworkID:     strPtrTest("test-network"),
		BppID:         &bppID,
		BppUri:        &bppURI,
		Status:        status,
	}); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM contract_snapshots WHERE transaction_id = $1`, txnID) //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM beckn_message_log WHERE transaction_id = $1`, txnID)  //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM transactions WHERE transaction_id = $1`, txnID)       //nolint:errcheck
	})
	return txnID
}

// seedTransactionWithSnapshot is seedTransaction plus a contract snapshot --
// Cancel() requires one to build its outbound payload (unlike Update(),
// which just forwards the caller's own contract), and in real usage any
// transaction that's reached CONFIRMED or beyond (UPDATE_SENT, etc.) always
// has one from Confirm's own on_confirm snapshot write.
func seedTransactionWithSnapshot(t *testing.T, pool *pgxpool.Pool, status dbsqlc.TransactionStatus) uuid.UUID {
	t.Helper()
	txnID := seedTransaction(t, pool, status)
	q := dbsqlc.New(pool)
	if err := q.UpsertContractSnapshot(context.Background(), dbsqlc.UpsertContractSnapshotParams{
		TransactionID: txnID,
		Action:        dbsqlc.BecknActionOnConfirm,
		Contract:      []byte(`{"id":"c1"}`),
	}); err != nil {
		t.Fatalf("seed contract snapshot: %v", err)
	}
	return txnID
}

func seedConfirmedTransactionWithSnapshot(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	return seedTransactionWithSnapshot(t, pool, dbsqlc.TransactionStatusCONFIRMED)
}

func newTestService(pool *pgxpool.Pool) *ClientService {
	cfg := &config.Config{
		BapID:        "onix-bap-x7m2wmy7nq-et.a.run.app",
		NetworkID:    "test-network",
		BppID:        "test-bpp",
		BppURI:       "http://bpp.test/bpp/receiver",
		BapCallerURL: "http://127.0.0.1:1/unreachable", // deliberately unroutable: exercises sendToBPP failure paths
	}
	lh := logharbour.NewLogger(logharbour.NewLoggerContext(logharbour.Debug2), "test", os.Stdout)
	return NewClientService(pool, cfg, lh, discoverwait.NewRegistry())
}

// seedTransactionWithBuyer is seedTransaction but also sets buyer_subject --
// UpsertTransaction's own ON CONFLICT clause deliberately excludes
// buyer_subject from later upserts (see its own comment), so it can only be
// set on the row's initial insert, same as the real Select() call path.
func seedTransactionWithBuyer(t *testing.T, pool *pgxpool.Pool, status dbsqlc.TransactionStatus, buyerSubject string) uuid.UUID {
	t.Helper()
	txnID := uuid.New()
	bppID, bppURI := "test-bpp", "http://bpp.test/bpp/receiver"
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO transactions (transaction_id, bap_id, network_id, bpp_id, bpp_uri, status, buyer_subject)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING transaction_id
	`, txnID, "onix-bap-x7m2wmy7nq-et.a.run.app", "test-network", bppID, bppURI, status, buyerSubject).Scan(&txnID); err != nil {
		t.Fatalf("seed transaction with buyer: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM contract_snapshots WHERE transaction_id = $1`, txnID) //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM beckn_message_log WHERE transaction_id = $1`, txnID)  //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM transactions WHERE transaction_id = $1`, txnID)       //nolint:errcheck
	})
	return txnID
}

func newTestServiceWithCallerURL(pool *pgxpool.Pool, callerURL string) *ClientService {
	cfg := &config.Config{
		BapID:        "onix-bap-x7m2wmy7nq-et.a.run.app",
		NetworkID:    "test-network",
		BppID:        "test-bpp",
		BppURI:       "http://bpp.test/bpp/receiver",
		BapCallerURL: callerURL,
	}
	lh := logharbour.NewLogger(logharbour.NewLoggerContext(logharbour.Debug2), "test", os.Stdout)
	return NewClientService(pool, cfg, lh, discoverwait.NewRegistry())
}

func strPtrTest(s string) *string { return &s }

func TestUpdate_InvalidTransactionID(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)

	err := svc.Update(context.Background(), &ClientUpdateRequest{TransactionID: "not-a-uuid", Contract: []byte(`{}`)}, nil)
	if !errors.Is(err, ErrInvalidTransactionID) {
		t.Fatalf("expected ErrInvalidTransactionID, got %v", err)
	}
}

// TestUpdate_RejectsNonOwningBuyer verifies the IDOR fix: a buyer who
// doesn't own the transaction gets the same ErrTransactionNotFound as a
// truly nonexistent one -- never a distinguishable "forbidden" response
// that would confirm the transaction's existence to a non-owner.
func TestUpdate_RejectsNonOwningBuyer(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	txnID := seedTransactionWithBuyer(t, pool, dbsqlc.TransactionStatusCONFIRMED, "buyer-A")

	attacker := "buyer-B"
	err := svc.Update(context.Background(), &ClientUpdateRequest{TransactionID: txnID.String(), Contract: []byte(`{}`)}, &attacker)
	if !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("expected ErrTransactionNotFound for a non-owning buyer, got %v", err)
	}
}

// TestUpdate_AllowsOwningBuyer confirms the fix doesn't also block the
// legitimate owner.
func TestUpdate_AllowsOwningBuyer(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	txnID := seedTransactionWithBuyer(t, pool, dbsqlc.TransactionStatusCONFIRMED, "buyer-A")

	owner := "buyer-A"
	err := svc.Update(context.Background(), &ClientUpdateRequest{TransactionID: txnID.String(), Contract: []byte(`{}`)}, &owner)
	if errors.Is(err, ErrTransactionNotFound) || errors.Is(err, ErrInvalidUpdateState) {
		t.Fatalf("expected the owning buyer's update to pass ownership/state checks, got %v", err)
	}
}

func TestUpdate_TransactionNotFound(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)

	err := svc.Update(context.Background(), &ClientUpdateRequest{TransactionID: uuid.New().String(), Contract: []byte(`{}`)}, nil)
	if !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("expected ErrTransactionNotFound, got %v", err)
	}
}

func TestUpdate_RejectsNonConfirmedState(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)

	// A transaction that never reached CONFIRMED (still awaiting on_select)
	// must not accept an update -- update requires a prior confirmed
	// contract per spec.
	txnID := seedTransaction(t, pool, dbsqlc.TransactionStatusSELECTSENT)

	err := svc.Update(context.Background(), &ClientUpdateRequest{TransactionID: txnID.String(), Contract: []byte(`{}`)}, nil)
	if !errors.Is(err, ErrInvalidUpdateState) {
		t.Fatalf("expected ErrInvalidUpdateState, got %v", err)
	}
}

func TestUpdate_RejectsTerminalState(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)

	txnID := seedTransaction(t, pool, dbsqlc.TransactionStatusCANCELLED)

	err := svc.Update(context.Background(), &ClientUpdateRequest{TransactionID: txnID.String(), Contract: []byte(`{}`)}, nil)
	if !errors.Is(err, ErrInvalidUpdateState) {
		t.Fatalf("expected ErrInvalidUpdateState for a CANCELLED transaction, got %v", err)
	}
}

func TestUpdate_RollsBackStatusOnSendFailure(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool) // BapCallerURL is deliberately unroutable

	txnID := seedTransaction(t, pool, dbsqlc.TransactionStatusCONFIRMED)

	err := svc.Update(context.Background(), &ClientUpdateRequest{TransactionID: txnID.String(), Contract: []byte(`{}`)}, nil)
	if err == nil {
		t.Fatal("expected an error from the unreachable BapCallerURL, got nil")
	}
	if errors.Is(err, ErrInvalidUpdateState) || errors.Is(err, ErrTransactionNotFound) || errors.Is(err, ErrInvalidTransactionID) {
		t.Fatalf("expected a delivery error, got sentinel: %v", err)
	}

	q := dbsqlc.New(pool)
	txn, getErr := q.GetTransaction(context.Background(), txnID)
	if getErr != nil {
		t.Fatalf("re-fetch transaction: %v", getErr)
	}
	if txn.Status != dbsqlc.TransactionStatusCONFIRMED {
		t.Fatalf("expected status rolled back to CONFIRMED after failed delivery, got %s", txn.Status)
	}
}

// TestMarkUpdateSentIfConfirmed_AtomicCAS exercises the compare-and-swap
// primitive Update() relies on for concurrency safety directly: a second
// attempt to move an already-UPDATE_SENT transaction must affect 0 rows,
// simulating what happens when two Update calls for the same transaction
// race each other.
func TestMarkUpdateSentIfConfirmed_AtomicCAS(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	txnID := seedTransaction(t, pool, dbsqlc.TransactionStatusCONFIRMED)
	q := dbsqlc.New(pool)

	first, err := q.MarkUpdateSentIfConfirmed(context.Background(), txnID)
	if err != nil {
		t.Fatalf("first transition: %v", err)
	}
	if first != 1 {
		t.Fatalf("expected the first CONFIRMED->UPDATE_SENT transition to affect 1 row, got %d", first)
	}

	second, err := q.MarkUpdateSentIfConfirmed(context.Background(), txnID)
	if err != nil {
		t.Fatalf("second transition: %v", err)
	}
	if second != 0 {
		t.Fatalf("expected a concurrent/duplicate transition attempt to affect 0 rows, got %d", second)
	}
}

// TestUpdate_TrueConcurrency fires two Update() calls for the same
// transaction from separate goroutines at the same time (not just
// sequentially) -- exactly the "concurrent updates for the same
// transaction" scenario the task calls out. Exactly one must win (proceed
// past the CAS to attempt delivery); the other must be rejected with
// ErrInvalidUpdateState. Both would otherwise race past a naive
// read-then-write check -- MarkUpdateSentIfConfirmed's WHERE-guarded
// UPDATE is what Postgres itself serializes.
//
// Deliberately points BapCallerURL at a slow server, not an unroutable one:
// an instantly-failing delivery lets the winner's rollback-to-CONFIRMED
// (see Update's own comment on why it rolls back on send failure) race
// ahead of the loser's CAS attempt, which can let both calls through the
// CAS and produce a false pass/fail depending on scheduling -- the slow
// server holds the winner "in flight" long enough that the loser's CAS
// attempt reliably lands while the transaction is still UPDATE_SENT.
func TestUpdate_TrueConcurrency(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()
	svc := newTestServiceWithCallerURL(pool, slow.URL)
	txnID := seedTransaction(t, pool, dbsqlc.TransactionStatusCONFIRMED)

	results := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- svc.Update(context.Background(), &ClientUpdateRequest{TransactionID: txnID.String(), Contract: []byte(`{}`)}, nil)
		}()
	}
	close(start)

	var rejected, other int
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case errors.Is(err, ErrInvalidUpdateState):
			rejected++
		default:
			// The winner proceeds to sendToBPP, which fails against the
			// deliberately unroutable BapCallerURL -- that's a delivery
			// error, not a state-conflict one, and is the expected
			// outcome for whichever goroutine won the CAS.
			other++
		}
	}
	if rejected != 1 || other != 1 {
		t.Fatalf("expected exactly one ErrInvalidUpdateState rejection and one non-conflict outcome, got %d rejected / %d other", rejected, other)
	}
}

func TestCancel_RejectsNonConfirmedState(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	txnID := seedTransactionWithSnapshot(t, pool, dbsqlc.TransactionStatusSELECTSENT)

	err := svc.Cancel(context.Background(), &ClientCancelRequest{TransactionID: txnID.String()})
	if !errors.Is(err, ErrInvalidUpdateState) {
		t.Fatalf("expected ErrInvalidUpdateState, got %v", err)
	}
}

// TestCancel_RejectsWhileUpdateInFlight is Phase 5's core assertion: a
// cancel against a transaction whose own update is still UPDATE_SENT must
// be rejected, not race it.
func TestCancel_RejectsWhileUpdateInFlight(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	txnID := seedTransactionWithSnapshot(t, pool, dbsqlc.TransactionStatusUPDATESENT)

	svc := newTestService(pool)
	err := svc.Cancel(context.Background(), &ClientCancelRequest{TransactionID: txnID.String()})
	if !errors.Is(err, ErrInvalidUpdateState) {
		t.Fatalf("expected ErrInvalidUpdateState while an update is in flight, got %v", err)
	}
}

func TestCancel_RollsBackStatusOnSendFailure(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool) // unroutable BapCallerURL
	txnID := seedConfirmedTransactionWithSnapshot(t, pool)

	err := svc.Cancel(context.Background(), &ClientCancelRequest{TransactionID: txnID.String()})
	if err == nil {
		t.Fatal("expected an error from the unreachable BapCallerURL, got nil")
	}

	q := dbsqlc.New(pool)
	txn, getErr := q.GetTransaction(context.Background(), txnID)
	if getErr != nil {
		t.Fatalf("re-fetch transaction: %v", getErr)
	}
	if txn.Status != dbsqlc.TransactionStatusCONFIRMED {
		t.Fatalf("expected status rolled back to CONFIRMED after failed cancel delivery, got %s", txn.Status)
	}
}

// TestUpdateVsCancel_TrueConcurrency is the actual cross-action guard this
// phase adds: an Update and a Cancel fired concurrently for the same
// transaction must not both succeed -- whichever's CAS loses gets
// ErrInvalidUpdateState, exactly like two concurrent Updates would.
func TestUpdateVsCancel_TrueConcurrency(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()
	svc := newTestServiceWithCallerURL(pool, slow.URL)
	txnID := seedConfirmedTransactionWithSnapshot(t, pool)

	results := make(chan error, 2)
	start := make(chan struct{})
	go func() {
		<-start
		results <- svc.Update(context.Background(), &ClientUpdateRequest{TransactionID: txnID.String(), Contract: []byte(`{}`)}, nil)
	}()
	go func() {
		<-start
		results <- svc.Cancel(context.Background(), &ClientCancelRequest{TransactionID: txnID.String()})
	}()
	close(start)

	var rejected, other int
	for i := 0; i < 2; i++ {
		if err := <-results; errors.Is(err, ErrInvalidUpdateState) {
			rejected++
		} else {
			other++
		}
	}
	if rejected != 1 || other != 1 {
		t.Fatalf("expected exactly one action rejected as a state conflict and one to proceed, got %d rejected / %d other", rejected, other)
	}
}

func TestMain(m *testing.M) {
	// Sanity-check message so `go test -v` output makes clear why tests
	// skip in an environment with no local Postgres, instead of just
	// silently reporting SKIP with no context.
	fmt.Println("clientsvc tests require a local Postgres (see testPool) -- they skip individually if unreachable")
	os.Exit(m.Run())
}

// seedTransactionWithDelivery seeds a CONFIRMED transaction whose snapshot
// carries a real delivery block, which is what the no-op guard compares
// against. Built on the existing snapshot helper rather than a new one.
func seedTransactionWithDelivery(t *testing.T, pool *pgxpool.Pool, deliveryDetails string) uuid.UUID {
	t.Helper()
	txnID := seedTransaction(t, pool, dbsqlc.TransactionStatusCONFIRMED)
	q := dbsqlc.New(pool)
	if err := q.UpsertContractSnapshot(context.Background(), dbsqlc.UpsertContractSnapshotParams{
		TransactionID: txnID,
		Action:        dbsqlc.BecknActionOnConfirm,
		Contract:      []byte(contractWithDelivery(deliveryDetails)),
	}); err != nil {
		t.Fatalf("seed contract snapshot: %v", err)
	}
	return txnID
}

func contractWithDelivery(deliveryDetails string) string {
	return `{"id":"c1","commitments":[{"id":"x"}],"performance":[{"performanceAttributes":` +
		`{"@type":"ion:IONTradePerformance","deliveryDetails":` + deliveryDetails + `}}]}`
}

const testDelivery = `{"address":{"streetAddress":"42 MG Road","addressLocality":"Bengaluru","postalCode":"560034"},"contact":{"name":"Arjun","phone":"+911"}}`

// A buyer who opens the change form and submits without editing anything
// must be refused: the seller would otherwise receive a blank request and
// have to spend a decision on it.
func TestUpdate_RejectsRequestThatChangesNothing(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	txnID := seedTransactionWithDelivery(t, pool, testDelivery)

	err := svc.Update(context.Background(), &ClientUpdateRequest{
		TransactionID: txnID.String(),
		Contract:      []byte(contractWithDelivery(testDelivery)),
	}, nil)

	if !errors.Is(err, ErrNoChangeRequested) {
		t.Fatalf("expected ErrNoChangeRequested, got %v", err)
	}

	// Refused before any state changed: the order must still be usable.
	q := dbsqlc.New(pool)
	txn, gErr := q.GetTransaction(context.Background(), txnID)
	if gErr != nil {
		t.Fatalf("re-fetch transaction: %v", gErr)
	}
	if txn.Status != dbsqlc.TransactionStatusCONFIRMED {
		t.Errorf("status should be untouched, got %s", txn.Status)
	}
	// And no allowance spent.
	if n, cErr := q.CountUpdateRequests(context.Background(), txnID); cErr == nil && n != 0 {
		t.Errorf("a refused no-op must not consume quota, got %d requests", n)
	}
}

// Reordered keys are the same request, not a change.
func TestUpdate_RejectsReorderedButIdenticalDetails(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	txnID := seedTransactionWithDelivery(t, pool, testDelivery)

	reordered := `{"contact":{"phone":"+911","name":"Arjun"},"address":{"postalCode":"560034","addressLocality":"Bengaluru","streetAddress":"42 MG Road"}}`
	err := svc.Update(context.Background(), &ClientUpdateRequest{
		TransactionID: txnID.String(),
		Contract:      []byte(contractWithDelivery(reordered)),
	}, nil)
	if !errors.Is(err, ErrNoChangeRequested) {
		t.Fatalf("expected ErrNoChangeRequested for reordered-but-identical details, got %v", err)
	}
}

// A genuine edit must still get through the guard. Delivery to the BPP
// fails in tests (no adapter), so this asserts only that it was NOT
// refused as a no-op.
func TestUpdate_AllowsRealChange(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	txnID := seedTransactionWithDelivery(t, pool, testDelivery)

	edited := `{"address":{"streetAddress":"42 MG Road","addressLocality":"Pune","postalCode":"411001"},"contact":{"name":"Arjun","phone":"+911"}}`
	err := svc.Update(context.Background(), &ClientUpdateRequest{
		TransactionID: txnID.String(),
		Contract:      []byte(contractWithDelivery(edited)),
	}, nil)
	if errors.Is(err, ErrNoChangeRequested) {
		t.Fatal("a real edit must not be refused as a no-op")
	}
}

// A contract with no delivery block at all cannot be proven a no-op, so it
// must be allowed through rather than wrongly refused.
func TestUpdate_AllowsWhenNoDeliveryBlockToCompare(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	txnID := seedConfirmedTransactionWithSnapshot(t, pool) // snapshot is {"id":"c1"}

	err := svc.Update(context.Background(), &ClientUpdateRequest{
		TransactionID: txnID.String(),
		Contract:      []byte(`{"id":"c1"}`),
	}, nil)
	if errors.Is(err, ErrNoChangeRequested) {
		t.Fatal("must not refuse when there is no delivery block to compare")
	}
}
