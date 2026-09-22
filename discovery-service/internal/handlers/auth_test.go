package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/remiges-tushar/discovery-service/internal/beckn"
	"github.com/remiges-tushar/discovery-service/internal/constants"
)

// stubVerifier lets tests control Verify's outcome without a real key pair.
type stubVerifier struct {
	err        error
	gotAuthHdr string
	gotBody    []byte
	calls      int
}

func (v *stubVerifier) Verify(authHeader string, body []byte) error {
	v.calls++
	v.gotAuthHdr = authHeader
	v.gotBody = body
	return v.err
}

func TestVerifyMiddleware_NilVerifierPassesThrough(t *testing.T) {
	var buf bytes.Buffer
	logger := newCapturingLogger(&buf)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	wrapped := VerifyMiddleware(nil, 1<<20, logger)(inner)

	req := httptest.NewRequest(http.MethodPost, "/discover", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if !called {
		t.Fatal("inner handler was not called — nil verifier should pass every request through unchecked")
	}
}

func TestVerifyMiddleware_RejectsMissingAuthorizationHeader(t *testing.T) {
	var buf bytes.Buffer
	logger := newCapturingLogger(&buf)
	verifier := &stubVerifier{}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	wrapped := VerifyMiddleware(verifier, 1<<20, logger)(inner)

	req := httptest.NewRequest(http.MethodPost, "/discover", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if called {
		t.Fatal("inner handler was called despite a missing Authorization header")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if verifier.calls != 0 {
		t.Errorf("Verify was called %d times, want 0 (should short-circuit before verifying)", verifier.calls)
	}

	var ack beckn.Ack
	if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if ack.Status != constants.StatusNACK {
		t.Errorf("Ack.Status = %q, want %q", ack.Status, constants.StatusNACK)
	}
	if ack.Error == nil || ack.Error.ErrorCode != constants.ErrSignatureMissing {
		t.Errorf("Ack.Error = %+v, want ErrorCode %q", ack.Error, constants.ErrSignatureMissing)
	}
}

func TestVerifyMiddleware_RejectsAFailedVerification(t *testing.T) {
	var buf bytes.Buffer
	logger := newCapturingLogger(&buf)
	verifier := &stubVerifier{err: errors.New("signature does not match")}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	wrapped := VerifyMiddleware(verifier, 1<<20, logger)(inner)

	req := httptest.NewRequest(http.MethodPost, "/discover", bytes.NewBufferString(`{"context":{}}`))
	req.Header.Set("Authorization", `Signature keyId="a|b|ed25519",algorithm="ed25519",created="1",expires="2",headers="h",signature="c2ln"`)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if called {
		t.Fatal("inner handler was called despite a failed signature verification")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var ack beckn.Ack
	if err := json.Unmarshal(rec.Body.Bytes(), &ack); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if ack.Error == nil || ack.Error.ErrorCode != constants.ErrSignatureInvalid {
		t.Errorf("Ack.Error = %+v, want ErrorCode %q", ack.Error, constants.ErrSignatureInvalid)
	}
}

func TestVerifyMiddleware_PassesBodyUnchangedToTheNextHandlerOnSuccess(t *testing.T) {
	var buf bytes.Buffer
	logger := newCapturingLogger(&buf)
	verifier := &stubVerifier{}

	wantBody := `{"context":{"action":"discover"},"message":{}}`
	var gotBody []byte
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
	})
	wrapped := VerifyMiddleware(verifier, 1<<20, logger)(inner)

	req := httptest.NewRequest(http.MethodPost, "/discover", bytes.NewBufferString(wantBody))
	req.Header.Set("Authorization", `Signature keyId="a|b|ed25519",algorithm="ed25519",created="1",expires="2",headers="h",signature="c2ln"`)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if string(gotBody) != wantBody {
		t.Errorf("body seen by next handler = %q, want %q (must be restored unchanged after verification reads it)", gotBody, wantBody)
	}
	if string(verifier.gotBody) != wantBody {
		t.Errorf("body passed to Verify = %q, want %q", verifier.gotBody, wantBody)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (inner handler never wrote a status)", rec.Code, http.StatusOK)
	}
}
