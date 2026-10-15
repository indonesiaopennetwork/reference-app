package confirmsvc

import (
	"encoding/json"
	"testing"
)

func settlementAttrs(t *testing.T, c ConfirmContract) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(c.Settlements[0].SettlementAttributes, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWithSettlementTermsKeepsAgreedTerms(t *testing.T) {
	c := ConfirmContract{Settlements: []Settlement{{ID: "s", SettlementAttributes: json.RawMessage(
		`{"@type": "ion:IONSettlementReconciliation", "settlementBasis": "ON_CONFIRMATION", "settlementWindow": "PT1H"}`)}}}
	withSettlementTerms(&c, "AFTER_DELIVERY", "P1D")
	if got := settlementAttrs(t, c); got["settlementBasis"] != "ON_CONFIRMATION" || got["settlementWindow"] != "PT1H" {
		t.Errorf("agreed terms changed: %v", got)
	}
}

func TestWithSettlementTermsFillsInMissingTerms(t *testing.T) {
	c := ConfirmContract{Settlements: []Settlement{{ID: "s", SettlementAttributes: json.RawMessage(
		`{"@type": "ion:IONPayment", "status": "PAID", "amount": 100}`)}}}
	withSettlementTerms(&c, "AFTER_DELIVERY", "P1D")
	got := settlementAttrs(t, c)
	if got["settlementBasis"] != "AFTER_DELIVERY" || got["settlementWindow"] != "P1D" || got["@type"] != "ion:IONSettlementReconciliation" {
		t.Errorf("missing terms not filled in: %v", got)
	}
}
