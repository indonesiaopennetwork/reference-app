package config

import "testing"

func TestNormalizeSettlementBasis(t *testing.T) {
	for in, want := range map[string]string{
		"DELIVERY":             "AFTER_DELIVERY",
		"SHIPMENT":             "AFTER_SHIPMENT",
		"RETURN_WINDOW_EXPIRY": "AFTER_RETURN_WINDOW",
		"AFTER_DELIVERY":       "AFTER_DELIVERY",
		"ON_CONFIRMATION":      "ON_CONFIRMATION",
	} {
		if got := NormalizeSettlementBasis(in); got != want {
			t.Errorf("NormalizeSettlementBasis(%q) = %q, want %q", in, got, want)
		}
	}
}
