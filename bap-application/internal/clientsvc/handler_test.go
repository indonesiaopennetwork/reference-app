package clientsvc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/auth"
)

// newTestRouter wires just the /update route through the real gin/OptionalAuth
// middleware stack (nil verifier -- skips real token verification, same as
// this app's own RegisterRoutes when Zitadel is unreachable at startup) so
// these tests exercise the actual HTTP boundary (status codes, JSON body
// shape), not just the service layer already covered by update_test.go.
func newTestRouter(svc *ClientService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &ClientHandler{svc: svc, lh: svc.lh}
	v1 := r.Group("/api/v1")
	v1.Use(auth.OptionalAuth(nil, nil))
	v1.POST("/update", h.HandleUpdate)
	return r
}

func doUpdate(t *testing.T, r *gin.Engine, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/update", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestHandleUpdate_MalformedTransactionID_Returns400(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	r := newTestRouter(newTestService(pool))

	rec := doUpdate(t, r, map[string]any{"transaction_id": "not-a-uuid", "contract": map[string]any{}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Fatalf("expected an 'error' field in the response body, got: %v", body)
	}
}

func TestHandleUpdate_UnknownTransaction_Returns404(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	r := newTestRouter(newTestService(pool))

	rec := doUpdate(t, r, map[string]any{"transaction_id": "00000000-0000-0000-0000-000000000000", "contract": map[string]any{}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdate_InvalidState_Returns409(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	r := newTestRouter(newTestService(pool))
	txnID := seedTransaction(t, pool, dbsqlc.TransactionStatusSELECTSENT)

	rec := doUpdate(t, r, map[string]any{"transaction_id": txnID.String(), "contract": map[string]any{}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdate_MissingBody_Returns400(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	r := newTestRouter(newTestService(pool))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/update", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed request body, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdate_HappyPath_Returns200(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()
	r := newTestRouter(newTestServiceWithCallerURL(pool, slow.URL))
	txnID := seedTransaction(t, pool, dbsqlc.TransactionStatusCONFIRMED)

	rec := doUpdate(t, r, map[string]any{"transaction_id": txnID.String(), "contract": map[string]any{"id": "c1"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if body["status"] != "UPDATE_SENT" {
		t.Fatalf("expected status=UPDATE_SENT, got %v", body)
	}
}
