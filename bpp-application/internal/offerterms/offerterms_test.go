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

func TestFillAttributesAddsTheDefaultCommission(t *testing.T) {
	got := decode(t, FillAttributes(json.RawMessage(`{"policies": {}}`), d))["policies"].(map[string]any)["commission"].([]any)
	want := map[string]any{"feeName": "Finder fee", "feeType": "PERCENT", "feeValue": 3.0, "appliesTo": "ITEM_TOTAL"}
	if len(got) != 1 || !equal(got[0].(map[string]any), want) {
		t.Errorf("commission = %v, want [%v]", got, want)
	}
	own := `{"policies": {"commission": [{"feeName": "Platform fee", "feeType": "AMOUNT", "feeValue": 500}],
	  "settlement": {"settlementBasis": "AFTER_DELIVERY", "settlementWindow": "P1D"}}}`
	if string(FillAttributes(json.RawMessage(own), d)) != own {
		t.Error("a seller's own commission must be kept")
	}
	earlier := `{"policies": {"commission": [{"name": "Platform fee", "type": "AMOUNT", "value": 500}],
	  "settlement": {"settlementBasis": "AFTER_DELIVERY", "settlementWindow": "P1D"}}}`
	renamed := decode(t, FillAttributes(json.RawMessage(earlier), d))["policies"].(map[string]any)["commission"].([]any)
	if !equal(renamed[0].(map[string]any), map[string]any{"feeName": "Platform fee", "feeType": "AMOUNT", "feeValue": 500.0}) {
		t.Errorf("commission stored under the earlier names = %v, want it renamed", renamed)
	}
	if amount := (Defaults{CommissionType: "AMOUNT", CommissionValue: 500}).Commission()[0].(map[string]any); amount["appliesTo"] != nil {
		t.Errorf("an AMOUNT commission has no appliesTo: %v", amount)
	}
}

func TestStripCommission(t *testing.T) {
	got := decode(t, StripCommission(json.RawMessage(`{"totalAmount": 1, "commission": {"type": "PERCENT", "value": 3}}`)))
	if _, ok := got["commission"]; ok || got["totalAmount"] != 1.0 {
		t.Errorf("attrs = %v, want the commission gone and the rest kept", got)
	}
	plain := json.RawMessage(`{"totalAmount": 1}`)
	if string(StripCommission(plain)) != string(plain) {
		t.Error("attributes without a commission must be unchanged")
	}
}

func equal(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
