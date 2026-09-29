package updatesvc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newTestWebhookRouter(svc *Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{svc: svc, lh: svc.lh}
	r.POST("/api/webhook/update", h.Handle)
	return r
}

func postUpdate(t *testing.T, r *gin.Engine, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/update", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestHandle_MissingBapURI_Returns400NACK(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	r := newTestWebhookRouter(newTestService(pool))

	rec := postUpdate(t, r, map[string]any{
		"context": map[string]any{"transactionId": "00000000-0000-0000-0000-000000000000"},
		"message":  map[string]any{"contract": map[string]any{"id": "x"}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	assertNACK(t, rec.Body.Bytes())
}

func TestHandle_MissingTransactionID_Returns400NACK(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	r := newTestWebhookRouter(newTestService(pool))

	rec := postUpdate(t, r, map[string]any{
		"context": map[string]any{"bapUri": "http://bap.test/bap/receiver"},
		"message":  map[string]any{"contract": map[string]any{"id": "x"}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	assertNACK(t, rec.Body.Bytes())
}

func TestHandle_MissingContract_Returns400NACK(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	r := newTestWebhookRouter(newTestService(pool))

	rec := postUpdate(t, r, map[string]any{
		"context": map[string]any{
			"bapUri":        "http://bap.test/bap/receiver",
			"transactionId": "00000000-0000-0000-0000-000000000000",
		},
		"message": map[string]any{},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	assertNACK(t, rec.Body.Bytes())
}

func TestHandle_ValidRequest_ReturnsACKAndQueuesForReview(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	svc := newTestService(pool)
	r := newTestWebhookRouter(svc)
	contractID, txnID := seedContract(t, pool, "ACTIVE")

	rec := postUpdate(t, r, map[string]any{
		"context": map[string]any{
			"bapUri":        "http://bap.test/bap/receiver",
			"bapId":         "test-bap",
			"transactionId": txnID.String(),
		},
		"message": map[string]any{"contract": map[string]any{"id": contractID.String(), "note": "http-test"}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 ACK, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	msg, _ := body["message"].(map[string]any)
	ack, _ := msg["ack"].(map[string]any)
	if ack["status"] != "ACK" {
		t.Fatalf("expected ACK, got %v", body)
	}

	// Process() runs in a goroutine after the ACK -- give it a moment, then
	// confirm the request was actually queued (not applied) rather than
	// just trusting the synchronous ACK.
	time.Sleep(200 * time.Millisecond)

	var count int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM contract_update_requests WHERE transaction_id = $1 AND status = 'PENDING'`, txnID).Scan(&count); err != nil {
		t.Fatalf("query contract_update_requests: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 PENDING update request to be queued, got %d", count)
	}
}

func assertNACK(t *testing.T, body []byte) {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	msg, _ := v["message"].(map[string]any)
	ack, _ := msg["ack"].(map[string]any)
	if ack["status"] != "NACK" {
		t.Fatalf("expected NACK, got %v", v)
	}
}
