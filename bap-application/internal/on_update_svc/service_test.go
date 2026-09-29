package onupdatesvc

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/contractx"
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

func seedTransaction(t *testing.T, pool *pgxpool.Pool, status dbsqlc.TransactionStatus) uuid.UUID {
	t.Helper()
	txnID := uuid.New()
	q := dbsqlc.New(pool)
	bppID, bppURI := "test-bpp", "http://bpp.test/bpp/receiver"
	if err := q.UpsertTransaction(context.Background(), dbsqlc.UpsertTransactionParams{
		TransactionID: txnID,
		BapID:         "onix-bap-x7m2wmy7nq-et.a.run.app",
		NetworkID:     ptr("test-network"),
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

func ptr(s string) *string { return &s }

func newTestService(pool *pgxpool.Pool) *Service {
	lh := logharbour.NewLogger(logharbour.NewLoggerContext(logharbour.Debug2), "test", os.Stdout)
	return NewService(pool, &config.Config{}, lh)
}

// TestProcess_AcceptsWhenPending: the ordinary, expected path -- a
// transaction currently UPDATE_SENT receives its solicited on_update, so
// the snapshot is written and the transaction returns to CONFIRMED.
func TestProcess_AcceptsWhenPending(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	txnID := seedTransaction(t, pool, dbsqlc.TransactionStatusUPDATESENT)
	msgID := uuid.New()

	svc.Process(context.Background(), &OnUpdateRequest{
		Context: BecknContext{
			TransactionID: txnID.String(),
			MessageID:     msgID.String(),
			BapID:         "onix-bap-x7m2wmy7nq-et.a.run.app",
			BppID:         "test-bpp",
		},
		// A realistic contract: beckn's Contract requires commitments, and
		// on_update_svc deliberately refuses to store one without them
		// (it would shadow the complete on_confirm snapshot -- see
		// contractx.HasOrderCore).
		Message: OnUpdateMessage{Contract: json.RawMessage(`{"id":"contract-1","commitments":[{"id":"line-1"}]}`)},
	})

	q := dbsqlc.New(pool)
	txn, err := q.GetTransaction(context.Background(), txnID)
	if err != nil {
		t.Fatalf("re-fetch transaction: %v", err)
	}
	if txn.Status != dbsqlc.TransactionStatusCONFIRMED {
		t.Fatalf("expected status CONFIRMED after accepted on_update, got %s", txn.Status)
	}

	snap, err := q.GetContractSnapshot(context.Background(), dbsqlc.GetContractSnapshotParams{
		TransactionID: txnID,
		Action:        dbsqlc.BecknActionOnUpdate,
	})
	if err != nil {
		t.Fatalf("expected a contract snapshot to be written, got error: %v", err)
	}
	if snap.ContractID == nil || *snap.ContractID != "contract-1" {
		t.Fatalf("expected snapshot contract id 'contract-1', got %v", snap.ContractID)
	}
}

// TestProcess_IgnoresStaleWhenNotPending: a duplicate/replayed/unsolicited
// on_update arriving when the transaction isn't UPDATE_SENT (e.g. it
// already completed a prior update round trip) must not flip the status
// again or overwrite the committed snapshot -- see MarkUpdateReceivedIfPending's
// own doc comment.
func TestProcess_IgnoresStaleWhenNotPending(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	txnID := seedTransaction(t, pool, dbsqlc.TransactionStatusCONFIRMED)

	svc.Process(context.Background(), &OnUpdateRequest{
		Context: BecknContext{
			TransactionID: txnID.String(),
			MessageID:     uuid.New().String(),
			BapID:         "onix-bap-x7m2wmy7nq-et.a.run.app",
			BppID:         "test-bpp",
		},
		Message: OnUpdateMessage{Contract: json.RawMessage(`{"id":"stale-contract"}`)},
	})

	q := dbsqlc.New(pool)
	txn, err := q.GetTransaction(context.Background(), txnID)
	if err != nil {
		t.Fatalf("re-fetch transaction: %v", err)
	}
	if txn.Status != dbsqlc.TransactionStatusCONFIRMED {
		t.Fatalf("expected status to remain CONFIRMED (unaffected by a stale on_update), got %s", txn.Status)
	}

	if _, err := q.GetContractSnapshot(context.Background(), dbsqlc.GetContractSnapshotParams{
		TransactionID: txnID,
		Action:        dbsqlc.BecknActionOnUpdate,
	}); err == nil {
		t.Fatal("expected no contract snapshot to be written for a stale/unsolicited on_update")
	}
}

// TestProcess_MalformedTransactionID: a callback with a garbled
// transactionId must be dropped safely (no panic, no attempt to touch the
// database with a zero-value UUID).
func TestProcess_MalformedTransactionID(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)

	svc.Process(context.Background(), &OnUpdateRequest{
		Context: BecknContext{TransactionID: "not-a-uuid", MessageID: uuid.New().String()},
		Message: OnUpdateMessage{Contract: json.RawMessage(`{"id":"x"}`)},
	})
	// No assertion beyond "did not panic" -- there's no transaction row to
	// check since the whole point is that this is dropped before any DB write.
}

// A minimal on_update contract (id plus a decision, no commitments) must be
// accepted and audited, and must flip the status -- but must NOT become the
// contract snapshot. Storing it shadowed the complete on_confirm snapshot,
// after which every outbound action built from "the latest snapshot" failed
// with `property "commitments" is missing`.
func TestProcess_DoesNotLetMinimalContractShadowTheSnapshot(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	txnID := seedTransaction(t, pool, dbsqlc.TransactionStatusUPDATESENT)

	q := dbsqlc.New(pool)
	const full = `{"id":"contract-1","commitments":[{"id":"line-1"}],"performance":[{"performanceAttributes":{"deliveryDetails":{"contact":{"phone":"+911"}}}}]}`
	if err := q.UpsertContractSnapshot(context.Background(), dbsqlc.UpsertContractSnapshotParams{
		TransactionID: txnID,
		Action:        dbsqlc.BecknActionOnConfirm,
		Contract:      []byte(full),
	}); err != nil {
		t.Fatalf("seed on_confirm snapshot: %v", err)
	}

	svc.Process(context.Background(), &OnUpdateRequest{
		Context: BecknContext{
			TransactionID: txnID.String(),
			MessageID:     uuid.New().String(),
			BapID:         "onix-bap-x7m2wmy7nq-et.a.run.app",
			BppID:         "test-bpp",
		},
		Message: OnUpdateMessage{Contract: json.RawMessage(
			`{"id":"contract-1","contractAttributes":{"@type":"ion:IONTradeContract","updateDecision":{"outcome":"REJECTED"}}}`)},
	})

	// The callback was still accepted: status advanced.
	txn, err := q.GetTransaction(context.Background(), txnID)
	if err != nil {
		t.Fatalf("re-fetch transaction: %v", err)
	}
	if txn.Status != dbsqlc.TransactionStatusCONFIRMED {
		t.Errorf("expected CONFIRMED after an accepted on_update, got %s", txn.Status)
	}

	// But it did not become the snapshot.
	if _, err := q.GetContractSnapshot(context.Background(), dbsqlc.GetContractSnapshotParams{
		TransactionID: txnID,
		Action:        dbsqlc.BecknActionOnUpdate,
	}); err == nil {
		t.Error("a contract with no commitments must not be stored as the on_update snapshot")
	}

	// And the complete snapshot still wins, so outbound calls keep working.
	latest, err := q.GetLatestContractSnapshot(context.Background(), txnID)
	if err != nil {
		t.Fatalf("latest snapshot: %v", err)
	}
	if !contractx.HasOrderCore(latest.Contract) {
		t.Errorf("latest snapshot lost its commitments: %s", latest.Contract)
	}
}
