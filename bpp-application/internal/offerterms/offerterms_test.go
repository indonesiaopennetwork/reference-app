package offerterms

import (
	"encoding/json"
	"testing"
)

var d = Defaults{CommissionType: "PERCENT", CommissionValue: 3, CommissionAppliesTo: "ITEM_TOTAL", Basis: "AFTER_DELIVERY", Window: "P1D"}

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestFillAttributesAddsTheDefaultPolicy(t *testing.T) {
	got := decode(t, FillAttributes(json.RawMessage(`{"policies": {"returns": {"allowed": true}}}`), d))
	policies := got["policies"].(map[string]any)
	if policies["returns"] == nil {
		t.Error("existing policies must be kept")
	}
	if p := policies["settlement"].(map[string]any); p["settlementBasis"] != "AFTER_DELIVERY" || p["settlementWindow"] != "P1D" {
		t.Errorf("settlement = %v", p)
	}
}

func TestFillAttributesKeepsTheSellersPolicy(t *testing.T) {
	raw := json.RawMessage(`{"policies": {"settlement": {"settlementBasis": "ON_CONFIRMATION"}}}`)
	p := decode(t, FillAttributes(raw, d))["policies"].(map[string]any)["settlement"].(map[string]any)
	if p["settlementBasis"] != "ON_CONFIRMATION" || p["settlementWindow"] != "P1D" {
		t.Errorf("settlement = %v, want the seller's basis and the default window", p)
	}
}

func TestFillConsideration(t *testing.T) {
	got := decode(t, FillConsideration(json.RawMessage(`{"totalAmount": 1}`), d))["commission"].(map[string]any)
	if got["type"] != "PERCENT" || got["value"] != 3.0 || got["appliesTo"] != "ITEM_TOTAL" {
		t.Errorf("commission = %v", got)
	}
	own := json.RawMessage(`{"commission": {"type": "AMOUNT", "value": 500}}`)
	if string(FillConsideration(own, d)) != string(own) {
		t.Error("a seller's own commission must be kept")
	}
	if amount := (Defaults{CommissionType: "AMOUNT", CommissionValue: 500}).Commission(); amount["appliesTo"] != nil {
		t.Errorf("an AMOUNT commission has no appliesTo: %v", amount)
	}
}
