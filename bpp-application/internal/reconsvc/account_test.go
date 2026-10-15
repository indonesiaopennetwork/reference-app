package reconsvc

import "testing"

func TestOwnAccountIsAddedWhenMissing(t *testing.T) {
	own := map[string]any{"holderRole": "BPP", "bankCode": "BCA", "accountNumber": "8730000000001", "accountName": ""}
	bap := map[string]any{"holderRole": "BAP", "bankCode": "MANDIRI", "accountNumber": "1370000000001"}

	// An order placed before payout accounts: no accounts at all.
	contract := map[string]any{"id": "c1", "settlements": []any{map[string]any{"id": "settlement-1",
		"settlementAttributes": map[string]any{"@type": "ion:IONTradeSettlement", "settledAmount": 1000.0}}}}
	f, err := contractFigures(contract, Terms{Account: own})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.bag()["settlementAccounts"].([]any); len(got) != 1 || asMap(got[0])["accountNumber"] != "8730000000001" {
		t.Errorf("no accounts: got %v, want this participant's own account", got)
	}

	// The other participant's account is kept, and this one's added.
	if got := withAccount([]any{bap}, own); len(got) != 2 || asMap(got[0])["holderRole"] != "BAP" || asMap(got[1])["holderRole"] != "BPP" {
		t.Errorf("BAP account only: got %v, want both", got)
	}
	// An account already declared for the role is left as it is.
	declared := map[string]any{"holderRole": "BPP", "bankCode": "BRI", "accountNumber": "999"}
	if got := withAccount([]any{bap, declared}, own); len(got) != 2 || asMap(got[1])["accountNumber"] != "999" {
		t.Errorf("declared BPP account: got %v, want it unchanged", got)
	}
	// No account configured: nothing added.
	if got := withAccount(nil, nil); len(got) != 0 {
		t.Errorf("no own account: got %v, want none", got)
	}
	if got := withAccount(nil, map[string]any{"holderRole": "BPP"}); len(got) != 0 {
		t.Errorf("own account without a number: got %v, want none", got)
	}
}
