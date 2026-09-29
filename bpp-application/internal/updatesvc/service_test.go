package updatesvc

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/config"
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

// seedContract inserts a minimal contract row directly (bypassing the
// normal init->confirm lifecycle) in the given status, for tests that need
// to start from a specific state.
func seedContract(t *testing.T, pool *pgxpool.Pool, status string) (contractID, txnID uuid.UUID) {
	t.Helper()
	contractID = uuid.New()
	txnID = uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO contracts (id, bpp_id, bap_id, bap_uri, transaction_id, status, contract_attributes)
		VALUES ($1, $2, 'test-bap', 'http://bap.test/bap/receiver', $3, $4::contract_status, '{"id":"orig"}'::jsonb)
	`, contractID, testBppID, txnID, status)
	if err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM beckn_message_log WHERE transaction_id = $1`, txnID)        //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM contract_update_requests WHERE transaction_id = $1`, txnID) //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM contracts WHERE id = $1`, contractID)                       //nolint:errcheck
	})
	return contractID, txnID
}

func newTestService(pool *pgxpool.Pool) *Service {
	lh := logharbour.NewLogger(logharbour.NewLoggerContext(logharbour.Debug2), "test", os.Stdout)
	return NewService(pool, &config.Config{BppID: testBppID, BppURI: "http://bpp.test/bpp/receiver", NetworkID: "test-network"}, lh)
}

func req(txnID uuid.UUID, contract json.RawMessage) *UpdateRequest {
	return &UpdateRequest{
		Context: BecknContext{TransactionID: txnID.String(), BapURI: "http://bap.test/bap/receiver", BapID: "test-bap"},
		Message: UpdateMessage{Contract: contract},
	}
}

func TestQueueUpdateRequest_SuccessOnActiveContract(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	contractID, txnID := seedContract(t, pool, "ACTIVE")

	requestID, err := svc.queueUpdateRequest(context.Background(), req(txnID, json.RawMessage(`{"id":"`+contractID.String()+`","note":"proposed"}`)))
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if requestID == uuid.Nil {
		t.Fatal("expected a non-nil request id")
	}

	q := dbsqlc.New(pool)
	row, err := q.GetUpdateRequestForDecision(context.Background(), dbsqlc.GetUpdateRequestForDecisionParams{ID: requestID, BppID: testBppID})
	if err != nil {
		t.Fatalf("re-fetch update request: %v", err)
	}
	if row.Status != dbsqlc.UpdateRequestStatusPENDING {
		t.Fatalf("expected PENDING, got %s", row.Status)
	}

	// The contract itself must NOT be mutated yet -- only queued.
	var stored map[string]any
	var raw []byte
	if err := pool.QueryRow(context.Background(), `SELECT contract_attributes FROM contracts WHERE id = $1`, contractID).Scan(&raw); err != nil {
		t.Fatalf("re-fetch contract: %v", err)
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stored["id"] != "orig" {
		t.Fatalf("expected contract to remain unchanged pending approval, got %v", stored)
	}
}

func TestQueueUpdateRequest_RejectsCancelledContract(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	_, txnID := seedContract(t, pool, "CANCELLED")

	if _, err := svc.queueUpdateRequest(context.Background(), req(txnID, json.RawMessage(`{"id":"x"}`))); err == nil {
		t.Fatal("expected an error queueing an update against a CANCELLED contract, got nil")
	}
}

func TestQueueUpdateRequest_RejectsCompletedContract(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	_, txnID := seedContract(t, pool, "COMPLETE")

	if _, err := svc.queueUpdateRequest(context.Background(), req(txnID, json.RawMessage(`{"id":"x"}`))); err == nil {
		t.Fatal("expected an error queueing an update against a COMPLETE contract, got nil")
	}
}

func TestQueueUpdateRequest_RejectsUnknownTransaction(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)

	if _, err := svc.queueUpdateRequest(context.Background(), req(uuid.New(), json.RawMessage(`{"id":"x"}`))); err == nil {
		t.Fatal("expected an error for a transaction with no matching contract, got nil")
	}
}

func TestQueueUpdateRequest_RejectsMissingContractPayload(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	_, txnID := seedContract(t, pool, "ACTIVE")

	if _, err := svc.queueUpdateRequest(context.Background(), req(txnID, nil)); err == nil {
		t.Fatal("expected an error for a missing contract payload, got nil")
	}
}

func TestQueueUpdateRequest_RejectsMalformedTransactionID(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)

	bad := &UpdateRequest{
		Context: BecknContext{TransactionID: "not-a-uuid", BapURI: "http://bap.test/bap/receiver"},
		Message: UpdateMessage{Contract: json.RawMessage(`{"id":"x"}`)},
	}
	if _, err := svc.queueUpdateRequest(context.Background(), bad); err == nil {
		t.Fatal("expected an error for a malformed transactionId, got nil")
	}
}

func TestApproveRequest_AppliesAttributesAndDecides(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	contractID, txnID := seedContract(t, pool, "ACTIVE")

	requestID, err := svc.queueUpdateRequest(context.Background(), req(txnID, json.RawMessage(`{"id":"`+contractID.String()+`","note":"approved-value"}`)))
	if err != nil {
		t.Fatalf("queue: %v", err)
	}

	// ApproveRequest also attempts to deliver on_update over HTTP, which
	// will fail here (no real BAP listening) -- that's fine, the decision
	// and contract mutation must still have happened before delivery is
	// even attempted.
	_ = svc.ApproveRequest(context.Background(), requestID, nil)

	q := dbsqlc.New(pool)
	row, err := q.GetUpdateRequestForDecision(context.Background(), dbsqlc.GetUpdateRequestForDecisionParams{ID: requestID, BppID: testBppID})
	if err != nil {
		t.Fatalf("re-fetch update request: %v", err)
	}
	if row.Status != dbsqlc.UpdateRequestStatusAPPROVED {
		t.Fatalf("expected APPROVED, got %s", row.Status)
	}

	var raw []byte
	if err := pool.QueryRow(context.Background(), `SELECT contract_attributes FROM contracts WHERE id = $1`, contractID).Scan(&raw); err != nil {
		t.Fatalf("re-fetch contract: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stored["note"] != "approved-value" {
		t.Fatalf("expected the approved attributes to be applied, got %v", stored)
	}
}

func TestRejectRequest_LeavesContractUnchangedAndRecordsReason(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	contractID, txnID := seedContract(t, pool, "ACTIVE")

	requestID, err := svc.queueUpdateRequest(context.Background(), req(txnID, json.RawMessage(`{"id":"`+contractID.String()+`","note":"rejected-value"}`)))
	if err != nil {
		t.Fatalf("queue: %v", err)
	}

	_ = svc.RejectRequest(context.Background(), requestID, nil, "ALREADY_PACKED", "quantity no longer available")

	q := dbsqlc.New(pool)
	row, err := q.GetUpdateRequestForDecision(context.Background(), dbsqlc.GetUpdateRequestForDecisionParams{ID: requestID, BppID: testBppID})
	if err != nil {
		t.Fatalf("re-fetch update request: %v", err)
	}
	if row.Status != dbsqlc.UpdateRequestStatusREJECTED {
		t.Fatalf("expected REJECTED, got %s", row.Status)
	}

	var raw []byte
	if err := pool.QueryRow(context.Background(), `SELECT contract_attributes FROM contracts WHERE id = $1`, contractID).Scan(&raw); err != nil {
		t.Fatalf("re-fetch contract: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if stored["id"] != "orig" {
		t.Fatalf("expected contract to remain unchanged after rejection, got %v", stored)
	}

	var reason *string
	if err := pool.QueryRow(context.Background(), `SELECT reject_reason FROM contract_update_requests WHERE id = $1`, requestID).Scan(&reason); err != nil {
		t.Fatalf("re-fetch reject reason: %v", err)
	}
	if reason == nil || *reason != "quantity no longer available" {
		t.Fatalf("expected reject reason to be recorded, got %v", reason)
	}
}

func TestDecideUpdateRequestIfPending_AtomicCAS(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	contractID, txnID := seedContract(t, pool, "ACTIVE")

	requestID, err := svc.queueUpdateRequest(context.Background(), req(txnID, json.RawMessage(`{"id":"`+contractID.String()+`"}`)))
	if err != nil {
		t.Fatalf("queue: %v", err)
	}

	q := dbsqlc.New(pool)
	first, err := q.DecideUpdateRequestIfPending(context.Background(), dbsqlc.DecideUpdateRequestIfPendingParams{ID: requestID, Status: dbsqlc.UpdateRequestStatusAPPROVED})
	if err != nil {
		t.Fatalf("first decide: %v", err)
	}
	if first != 1 {
		t.Fatalf("expected first decision to affect 1 row, got %d", first)
	}

	// A second decision attempt (e.g. a double-click, or approve+reject
	// racing) on an already-decided request must be a no-op.
	second, err := q.DecideUpdateRequestIfPending(context.Background(), dbsqlc.DecideUpdateRequestIfPendingParams{ID: requestID, Status: dbsqlc.UpdateRequestStatusREJECTED})
	if err != nil {
		t.Fatalf("second decide: %v", err)
	}
	if second != 0 {
		t.Fatalf("expected a second decision attempt to affect 0 rows, got %d", second)
	}
}

// A request that asks for exactly the delivery details the contract already
// holds must not be queued: the seller's review screen would render it as a
// blank "No changes to delivery details" row and cost them a decision for
// nothing.
func TestQueueUpdateRequest_RejectsNoOpChange(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	contractID, txnID := seedContract(t, pool, "ACTIVE")

	const dd = `{"address":{"streetAddress":"42 MG Road","addressLocality":"Bengaluru"},"contact":{"phone":"+911"}}`
	seedPerformanceDelivery(t, pool, contractID, dd)

	proposed := `{"id":"` + contractID.String() + `","performance":[{"performanceAttributes":{"@type":"ion:IONTradePerformance","deliveryDetails":` + dd + `}}]}`
	_, err := svc.queueUpdateRequest(context.Background(), req(txnID, json.RawMessage(proposed)))
	if err == nil {
		t.Fatal("expected a no-op update to be refused")
	}
	if !strings.Contains(err.Error(), "changes nothing") {
		t.Fatalf("expected a 'changes nothing' refusal, got %v", err)
	}

	var queued int
	if qErr := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM contract_update_requests WHERE transaction_id=$1`, txnID).Scan(&queued); qErr != nil {
		t.Fatalf("count queued: %v", qErr)
	}
	if queued != 0 {
		t.Errorf("a refused no-op must not be queued, found %d rows", queued)
	}
}

// A genuine edit against the same seeded contract must still queue.
func TestQueueUpdateRequest_AllowsRealChange(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	contractID, txnID := seedContract(t, pool, "ACTIVE")

	seedPerformanceDelivery(t, pool, contractID,
		`{"address":{"addressLocality":"Bengaluru"},"contact":{"phone":"+911"}}`)

	proposed := `{"id":"` + contractID.String() + `","performance":[{"performanceAttributes":{"@type":"ion:IONTradePerformance","deliveryDetails":{"address":{"addressLocality":"Pune"},"contact":{"phone":"+911"}}}}]}`
	if _, err := svc.queueUpdateRequest(context.Background(), req(txnID, json.RawMessage(proposed))); err != nil {
		t.Fatalf("a real change must queue, got %v", err)
	}
}

// seedPerformanceDelivery gives a seeded contract a performance row holding
// the given delivery block -- the "before" state the no-op check and the
// seller's review screen both compare against.
func seedPerformanceDelivery(t *testing.T, pool *pgxpool.Pool, contractID uuid.UUID, deliveryDetails string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO contract_performances (id, contract_id, status_code, performance_attributes)
		 VALUES ($1, $2, 'PENDING', $3::jsonb)`,
		"p-"+contractID.String()[:8], contractID,
		`{"@type":"ion:IONTradePerformance","deliveryDetails":`+deliveryDetails+`}`)
	if err != nil {
		t.Fatalf("seed performance: %v", err)
	}
}
