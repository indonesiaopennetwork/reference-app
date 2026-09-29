package contractx

import (
	"encoding/json"
	"testing"
)

func contract(deliveryDetails string) json.RawMessage {
	return json.RawMessage(`{
      "id": "c1",
      "commitments": [{"id": "x"}],
      "performance": [{"performanceAttributes": {
        "@type": "ion:IONTradePerformance",
        "deliveryDetails": ` + deliveryDetails + `
      }}]
    }`)
}

const dd = `{"address":{"streetAddress":"42 MG Road","addressLocality":"Bengaluru","postalCode":"560034"},"contact":{"name":"Arjun","phone":"+911"}}`

func TestDeliveryDetails(t *testing.T) {
	got := DeliveryDetails(contract(dd))
	if len(got) == 0 {
		t.Fatal("expected the delivery block to be found")
	}
	if !JSONEqual(got, json.RawMessage(dd)) {
		t.Errorf("extracted block differs: %s", got)
	}
}

func TestDeliveryDetails_AbsentOrMalformed(t *testing.T) {
	for _, in := range []string{
		`{"id":"c1"}`,                    // no performance
		`{"id":"c1","performance":[]}`,   // empty performance
		`{"id":"c1","performance":[{}]}`, // no attributes
		`{"id":"c1","performance":[{"performanceAttributes":{}}]}`, // no delivery block
		`{"broken":`, // unparseable
		``,           // empty
	} {
		if got := DeliveryDetails(json.RawMessage(in)); got != nil {
			t.Errorf("DeliveryDetails(%q) = %s, want nil", in, got)
		}
	}
}

func TestJSONEqual_SemanticNotBytewise(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", `{"a":1}`, `{"a":1}`, true},
		{"key order and whitespace ignored", `{"a":1,"b":2}`, "{\n \"b\": 2,\n \"a\": 1\n}", true},
		{"nested key order ignored", `{"o":{"x":1,"y":[1,2]}}`, `{"o":{"y":[1,2],"x":1}}`, true},
		{"array order is significant", `{"a":[1,2]}`, `{"a":[2,1]}`, false},
		{"number formatting ignored", `{"n":1}`, `{"n":1.0}`, true},
		{"different values", `{"a":1}`, `{"a":2}`, false},
		// Absent/unparseable input must never compare equal -- including to
		// another unparseable input -- so a malformed payload is treated as
		// "cannot prove these are the same", not as a match.
		{"empty vs empty", ``, ``, false},
		{"empty vs value", ``, `{"a":1}`, false},
		{"malformed both sides", `{"broken":`, `{"broken":`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := JSONEqual(json.RawMessage(tt.a), json.RawMessage(tt.b)); got != tt.want {
				t.Fatalf("JSONEqual(%q,%q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// The behaviour the /update guard depends on: a proposal that re-submits the
// current values must be detectable as a no-op, and one real edit must not.
func TestDeliveryDetailsUnchanged(t *testing.T) {
	current := contract(dd)

	if !DeliveryDetailsUnchanged(current, contract(dd)) {
		t.Error("re-submitting identical delivery details must be reported as unchanged")
	}

	reordered := `{"contact":{"phone":"+911","name":"Arjun"},"address":{"postalCode":"560034","addressLocality":"Bengaluru","streetAddress":"42 MG Road"}}`
	if !DeliveryDetailsUnchanged(current, contract(reordered)) {
		t.Error("key reordering is not a change")
	}

	edited := `{"address":{"streetAddress":"42 MG Road","addressLocality":"Pune","postalCode":"560034"},"contact":{"name":"Arjun","phone":"+911"}}`
	if DeliveryDetailsUnchanged(current, contract(edited)) {
		t.Error("a real edit must NOT be reported as unchanged")
	}
}

// When either side's delivery block is missing we cannot prove the request is
// a no-op, so it must be allowed through rather than wrongly refused.
func TestDeliveryDetailsUnchanged_UnknownIsNotUnchanged(t *testing.T) {
	if DeliveryDetailsUnchanged(json.RawMessage(`{"id":"c1"}`), contract(dd)) {
		t.Error("no current delivery block: must not claim unchanged")
	}
	if DeliveryDetailsUnchanged(contract(dd), json.RawMessage(`{"id":"c1"}`)) {
		t.Error("no proposed delivery block: must not claim unchanged")
	}
}

// A contract is only usable as stored order state if it carries the core
// beckn Contract requires. Anything less must not be allowed to replace a
// complete snapshot.
func TestHasOrderCore(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"full contract", string(contract(dd)), true},
		{"commitments only", `{"id":"c1","commitments":[{"id":"x"}]}`, true},
		{"id only", `{"id":"c1"}`, false},
		{"empty commitments array", `{"id":"c1","commitments":[]}`, false},
		{"null commitments", `{"id":"c1","commitments":null}`, false},
		{"decision-only contract", `{"id":"c1","contractAttributes":{"updateDecision":{"outcome":"APPROVED"}}}`, false},
		{"malformed", `{"broken":`, false},
		{"empty", ``, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasOrderCore(json.RawMessage(tt.in)); got != tt.want {
				t.Fatalf("HasOrderCore(%.40q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
