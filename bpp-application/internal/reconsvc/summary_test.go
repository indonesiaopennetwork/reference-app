package reconsvc

import "testing"

func answer(accord bool, net float64) map[string]any {
	return map[string]any{"settlements": []any{map[string]any{"settlementAttributes": map[string]any{
		"accord": accord, "netPayable": map[string]any{"value": net}}}}}
}

func TestSummaryCountsAgreedAndDisputedContracts(t *testing.T) {
	sm := summaryOf("bap", "bpp", "2026-10-13", []any{answer(true, 100), answer(false, 50), answer(true, 25)})
	if sm.ContractCount != 3 || sm.AgreedCount == nil || *sm.AgreedCount != 2 || *sm.DisputedCount != 1 || sm.TotalNetPayable != 175 {
		t.Errorf("summary = %+v", sm)
	}
}

func TestSummaryOfAReconcileHasNoCounts(t *testing.T) {
	c := answer(true, 100)
	delete(c["settlements"].([]any)[0].(map[string]any)["settlementAttributes"].(map[string]any), "accord")
	if sm := summaryOf("bap", "bpp", "2026-10-13", []any{c}); sm.AgreedCount != nil || sm.DisputedCount != nil {
		t.Errorf("a /reconcile has no accord to count: %+v", sm)
	}
}
