package dokusvc

import (
	"net/http"
	"testing"
)

// TestSignRoundTrips checks sign/verify agree with each other -- the actual
// values aren't checked against a DOKU-published fixture (their docs give
// an example HMAC but not the secret key that produced it), so this
// confirms internal consistency: what Client signs for an outbound request,
// VerifyNotification (the same algorithm, used on an inbound one) accepts.
func TestSignRoundTrips(t *testing.T) {
	const secret = "sandbox-secret-key"
	components := signatureComponents("MCH-0001", "req-1", "2026-08-11T08:45:42Z", "/checkout/v1/payment", digest([]byte(`{"order":{"amount":1}}`)))
	sig := sign(secret, components)

	if !verify(secret, components, sig) {
		t.Fatal("verify rejected a signature sign just produced")
	}
	if verify("wrong-secret", components, sig) {
		t.Fatal("verify accepted a signature signed with a different secret")
	}
	if verify(secret, components+"tampered", sig) {
		t.Fatal("verify accepted a signature for a different component string")
	}
}

func TestVerifyNotificationRejectsTamperedBody(t *testing.T) {
	c := NewClient("https://api-sandbox.doku.com", "MCH-0001", "sandbox-secret-key")
	body := []byte(`{"order":{"invoice_number":"inv-1","amount":20000},"transaction":{"status":"SUCCESS"}}`)

	header := http.Header{}
	header.Set("Client-Id", "MCH-0001")
	header.Set("Request-Id", "notif-1")
	header.Set("Request-Timestamp", "2026-08-11T08:45:42Z")
	components := signatureComponents("MCH-0001", "notif-1", "2026-08-11T08:45:42Z", "/api/webhook/doku-notification", digest(body))
	header.Set("Signature", sign("sandbox-secret-key", components))

	if err := c.VerifyNotification(header, "/api/webhook/doku-notification", body); err != nil {
		t.Fatalf("expected a correctly signed notification to verify, got: %v", err)
	}

	tampered := []byte(`{"order":{"invoice_number":"inv-1","amount":999999999},"transaction":{"status":"SUCCESS"}}`)
	if err := c.VerifyNotification(header, "/api/webhook/doku-notification", tampered); err == nil {
		t.Fatal("expected a tampered body to fail verification")
	}
}

func TestVerifyNotificationRejectsMissingHeaders(t *testing.T) {
	c := NewClient("https://api-sandbox.doku.com", "MCH-0001", "sandbox-secret-key")
	if err := c.VerifyNotification(http.Header{}, "/api/webhook/doku-notification", []byte("{}")); err == nil {
		t.Fatal("expected missing signature headers to fail verification")
	}
}

func TestMethodFor(t *testing.T) {
	cases := map[string]string{
		"VIRTUAL_ACCOUNT_BCA":  "VIRTUAL_ACCOUNT",
		"QRIS":                 "QRIS",
		"CREDIT_CARD":          "CREDIT_CARD",
		"EMONEY_OVO":           "EWALLET",
		"PEER_TO_PEER_AKULAKU": "BNPL",
		"SOMETHING_UNKNOWN":    "EWALLET",
	}
	for in, want := range cases {
		if got := MethodFor(in); got != want {
			t.Errorf("MethodFor(%q) = %q, want %q", in, got, want)
		}
	}
}
