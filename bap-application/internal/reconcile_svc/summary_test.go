package reconcilesvc

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

func TestSummaryCountsPendingAndTotalsAgreedAndUnreconciled(t *testing.T) {
	pending := answer(false, 40)
	bag := pending["settlements"].([]any)[0].(map[string]any)["settlementAttributes"].(map[string]any)
	bag["dispute"] = map[string]any{"code": "SETTLEMENT_WINDOW_NOT_REACHED"}
	bag["commission"] = map[string]any{"value": 4.0}
	disputed := answer(false, 50)
	sm := summaryOf("bap", "bpp", "2026-10-13", []any{answer(true, 100), disputed, pending})
	if *sm.AgreedCount != 1 || *sm.DisputedCount != 1 || *sm.PendingCount != 1 || *sm.PendingSettlementCount != 1 {
		t.Errorf("counts = %+v", sm)
	}
	if *sm.TotalAgreedNetPayable != 100 || *sm.TotalUnreconciledNetPayable != 90 || *sm.TotalUnreconciledCommission != 4 {
		t.Errorf("totals = agreed %v, unreconciled %v / %v", *sm.TotalAgreedNetPayable, *sm.TotalUnreconciledNetPayable, *sm.TotalUnreconciledCommission)
	}
	if sm.status() != "DISPUTED" || summaryOf("bap", "bpp", "", []any{answer(true, 1), pending}).status() != "PENDING" {
		t.Error("status: DISPUTED when any is disputed, PENDING when only pending remain")
	}
}
