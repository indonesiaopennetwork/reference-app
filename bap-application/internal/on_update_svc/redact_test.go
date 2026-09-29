package onupdatesvc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactForLog_MasksDeliveryDetails(t *testing.T) {
	in := json.RawMessage(`{
		"context": {"transactionId": "t1"},
		"message": {"contract": {
			"id": "c1",
			"performance": [{
				"performanceAttributes": {
					"deliveryDetails": {
						"address": {"streetAddress": "221B Baker St", "postalCode": "560102"},
						"contact": {"name": "Sherlock Holmes", "phone": "+911234567890"}
					}
				}
			}]
		}}
	}`)

	out := redactForLog(in)
	s := string(out)

	if strings.Contains(s, "Baker St") || strings.Contains(s, "Sherlock") || strings.Contains(s, "1234567890") {
		t.Fatalf("expected PII fields to be redacted, got: %s", s)
	}
	if !strings.Contains(s, redactedPlaceholder) {
		t.Fatalf("expected redaction placeholder present, got: %s", s)
	}
	// Non-PII fields must survive untouched.
	if !strings.Contains(s, `"id":"c1"`) && !strings.Contains(s, `"id": "c1"`) {
		t.Fatalf("expected non-PII field 'id' to survive redaction, got: %s", s)
	}
}

func TestRedactForLog_PassesThroughWhenNoDeliveryDetails(t *testing.T) {
	in := json.RawMessage(`{"context":{"transactionId":"t1"},"message":{"contract":{"id":"c1"}}}`)
	out := redactForLog(in)
	if string(out) != `{"context":{"transactionId":"t1"},"message":{"contract":{"id":"c1"}}}` {
		t.Fatalf("expected payload without deliveryDetails to pass through unchanged (modulo key order), got: %s", out)
	}
}

func TestRedactForLog_HandlesEmptyAndInvalidInput(t *testing.T) {
	if out := redactForLog(nil); out != nil {
		t.Fatalf("expected nil in -> nil out, got %v", out)
	}
	invalid := json.RawMessage(`not json`)
	if out := redactForLog(invalid); string(out) != string(invalid) {
		t.Fatalf("expected invalid JSON to pass through unchanged, got %s", out)
	}
}
