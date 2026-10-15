package dashboardsvc

import (
	"testing"
	"time"
)

func TestCompleteAtFollowsTheSettlementBasis(t *testing.T) {
	delivered := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	contract := func(basis string) map[string]any {
		return map[string]any{
			"commitments": []any{map[string]any{"offer": map[string]any{"offerAttributes": map[string]any{
				"policies": map[string]any{"returns": map[string]any{"allowed": true, "window": "P7D"}}}}}},
			"settlements": []any{map[string]any{"id": "settlement-1", "settlementAttributes": map[string]any{
				"@type": "ion:IONSettlementReconciliation", "settlementBasis": basis}}},
		}
	}
	for basis, want := range map[string]time.Time{
		"AFTER_DELIVERY":       delivered,
		"DELIVERY":             delivered, // the older name
		"AFTER_SHIPMENT":       delivered,
		"ON_CONFIRMATION":      delivered,
		"AFTER_RETURN_WINDOW":  delivered.Add(7 * 24 * time.Hour),
		"RETURN_WINDOW_EXPIRY": delivered.Add(7 * 24 * time.Hour),
	} {
		if got := completeAt(contract(basis), "AFTER_DELIVERY", delivered); !got.Equal(want) {
			t.Errorf("%s: complete at %v, want %v", basis, got, want)
		}
	}
	if got := completeAt(contract(""), "AFTER_RETURN_WINDOW", delivered); !got.Equal(delivered.Add(7 * 24 * time.Hour)) {
		t.Errorf("no basis stated: complete at %v, want the default basis's time", got)
	}
}
