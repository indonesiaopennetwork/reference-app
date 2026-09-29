package dashboardsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

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

// seedContractWithProvider seeds a contract owned by a specific providerID,
// plus one PENDING update request against it, for testing provider-scoped
// approve/reject/list access.
func seedContractWithProvider(t *testing.T, pool *pgxpool.Pool, providerID string) (contractID, txnID, requestID uuid.UUID) {
	t.Helper()
	contractID = uuid.New()
	txnID = uuid.New()
	requestID = uuid.New()
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO contracts (id, bpp_id, bap_id, bap_uri, transaction_id, status, contract_attributes, provider_id)
		VALUES ($1, $2, 'test-bap', 'http://bap.test/bap/receiver', $3, 'ACTIVE'::contract_status, '{"id":"orig"}'::jsonb, $4)
	`, contractID, testBppID, txnID, providerID); err != nil {
		t.Fatalf("seed contract: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contract_update_requests (id, contract_id, transaction_id, bap_id, bap_uri, requested_attributes)
		VALUES ($1, $2, $3, 'test-bap', 'http://bap.test/bap/receiver', $4)
	`, requestID, contractID, txnID, json.RawMessage(`{"id":"`+contractID.String()+`","note":"proposed"}`)); err != nil {
		t.Fatalf("seed update request: %v", err)
	}

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM beckn_message_log WHERE transaction_id = $1`, txnID)        //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM contract_update_requests WHERE transaction_id = $1`, txnID)  //nolint:errcheck
		pool.Exec(context.Background(), `DELETE FROM contracts WHERE id = $1`, contractID)                        //nolint:errcheck
	})
	return contractID, txnID, requestID
}

// withProvider injects the provider_id auth.RequireSeller would normally set,
// bypassing real Zitadel token verification for these handler-level tests --
// same string key auth.ProviderIDFromContext reads ("auth.provider_id").
func withProvider(providerID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("auth.provider_id", providerID)
		c.Next()
	}
}

func newTestDashboardRouter(pool *pgxpool.Pool, providerID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	lh := logharbour.NewLogger(logharbour.NewLoggerContext(logharbour.Debug2), "test", os.Stdout)
	h := NewHandler(pool, &config.Config{BppID: testBppID, BppURI: "http://bpp.test/bpp/receiver"}, lh)

	dash := r.Group("/api/v1")
	dash.Use(withProvider(providerID))
	dash.GET("/update-requests", h.HandleListUpdateRequests)
	dash.POST("/update-requests/:id/approve", h.HandleApproveUpdateRequest)
	dash.POST("/update-requests/:id/reject", h.HandleRejectUpdateRequest)
	return r
}

func TestHandleApproveUpdateRequest_OwningProvider_Succeeds(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	_, _, requestID := seedContractWithProvider(t, pool, "provider-A")
	r := newTestDashboardRouter(pool, "provider-A")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/update-requests/"+requestID.String()+"/approve", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleApproveUpdateRequest_WrongProvider_Rejected(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	_, _, requestID := seedContractWithProvider(t, pool, "provider-A")
	r := newTestDashboardRouter(pool, "provider-B") // different seller

	req := httptest.NewRequest(http.MethodPost, "/api/v1/update-requests/"+requestID.String()+"/approve", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("expected a non-2xx response for a different seller's request, got 200: %s", rec.Body.String())
	}
}

func TestHandleRejectUpdateRequest_RecordsReason(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	_, _, requestID := seedContractWithProvider(t, pool, "provider-A")
	r := newTestDashboardRouter(pool, "provider-A")

	body, _ := json.Marshal(map[string]string{"reason": "out of stock"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/update-requests/"+requestID.String()+"/reject", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var reason *string
	if err := pool.QueryRow(context.Background(), `SELECT reject_reason FROM contract_update_requests WHERE id = $1`, requestID).Scan(&reason); err != nil {
		t.Fatalf("re-fetch reject reason: %v", err)
	}
	if reason == nil || *reason != "out of stock" {
		t.Fatalf("expected reject reason to be recorded, got %v", reason)
	}
}

// Asserts presence/absence rather than an exact total count -- this is a
// shared, non-transactional dev database that other test runs also write
// to, so an exact row-count assertion would be flaky against leftover data
// from unrelated tests. What actually matters for provider scoping is that
// A's own request is visible and B's is not.
func TestHandleListUpdateRequests_ScopedToProvider(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	_, _, requestA := seedContractWithProvider(t, pool, "provider-A")
	_, _, requestB := seedContractWithProvider(t, pool, "provider-B")

	r := newTestDashboardRouter(pool, "provider-A")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/update-requests?limit=100", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	seenA, seenB := false, false
	for _, item := range body.Items {
		switch item["id"] {
		case requestA.String():
			seenA = true
		case requestB.String():
			seenB = true
		}
	}
	if !seenA {
		t.Fatal("expected provider-A's own update request to be visible")
	}
	if seenB {
		t.Fatal("expected provider-B's update request to NOT be visible to provider-A")
	}
}
