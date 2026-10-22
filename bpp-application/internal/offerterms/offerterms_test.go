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

func TestFillAttributesDropsPoliciesCommission(t *testing.T) {
	got := decode(t, FillAttributes(json.RawMessage(`{"policies": {"commission": [{"feeName": "Finder fee", "feeType": "PERCENT", "feeValue": 5}],
	  "settlement": {"settlementBasis": "AFTER_DELIVERY", "settlementWindow": "P1D"}}}`), d))
	if _, ok := got["policies"].(map[string]any)["commission"]; ok {
		t.Errorf("policies.commission must go (the fee is on the consideration): %v", got)
	}
}

func TestFillFee(t *testing.T) {
	fee := func(raw, offer string) map[string]any {
		return decode(t, FillFee(json.RawMessage(raw), json.RawMessage(offer), d))["buyerFinderFee"].(map[string]any)
	}
	want := map[string]any{"feeType": "PERCENT", "feeValue": 3.0, "appliesTo": "ITEM_TOTAL"}
	if got := fee(`{"totalAmount": 1}`, `{}`); !equal(got, want) {
		t.Errorf("default fee = %v, want %v", got, want)
	}
	own := `{"totalAmount": 1, "buyerFinderFee": {"feeType": "AMOUNT", "feeValue": 500}}`
	if string(FillFee(json.RawMessage(own), nil, d)) != own {
		t.Error("a seller's own fee must be kept unchanged")
	}
	// Stored the earlier ways: as the consideration's commission, or as the
	// offer's policies.commission entries (the first is the fee).
	if got := fee(`{"totalAmount": 1, "commission": {"type": "AMOUNT", "value": 700}}`, `{}`); !equal(got, map[string]any{"feeType": "AMOUNT", "feeValue": 700.0}) {
		t.Errorf("fee from the consideration's commission = %v", got)
	}
	got := decode(t, FillFee(json.RawMessage(`{"totalAmount": 1, "commission": {"type": "AMOUNT", "value": 700}}`), nil, d))
	if _, ok := got["commission"]; ok {
		t.Error("the consideration's commission must go")
	}
	offer := `{"policies": {"commission": [{"feeName": "Finder fee", "feeType": "PERCENT", "feeValue": 4}, {"feeName": "Platform fee", "feeType": "AMOUNT", "feeValue": 250}]}}`
	if got := fee(`{"totalAmount": 1}`, offer); !equal(got, map[string]any{"feeType": "PERCENT", "feeValue": 4.0, "appliesTo": "ITEM_TOTAL"}) {
		t.Errorf("fee from policies.commission = %v", got)
	}
	if amount := (Defaults{CommissionType: "AMOUNT", CommissionValue: 500}).Fee(); amount["appliesTo"] != nil {
		t.Errorf("an AMOUNT fee has no appliesTo: %v", amount)
	}
}

func equal(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
