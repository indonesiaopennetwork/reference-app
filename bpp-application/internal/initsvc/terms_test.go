package initsvc

import (
	"encoding/json"
	"testing"

	"github.com/ion/winroom/bpp/internal/config"
)

func TestApplySellerTermsAnswersTheSettlement(t *testing.T) {
	c := InitContract{
		Consideration: []Consideration{{ID: "price-1", ConsiderationAttributes: json.RawMessage(
			`{"totalAmount": 98900, "breakup": [{"type": "BASE_PRICE", "amount": 89000}, {"type": "TAX", "amount": 9900}]}`)}},
		Settlements: []json.RawMessage{json.RawMessage(`{"id": "settlement-1", "settlementAttributes": {
		  "@type": "ion:IONSettlementReconciliation", "status": "PENDING",
		  "finderFee": {"feeType": "PERCENT", "feeValue": 3, "appliesTo": "ITEM_TOTAL"},
		  "settlementAccounts": [{"holderRole": "BAP", "bankCode": "MANDIRI", "accountNumber": "1"}]}}`)},
	}
	cfg := &config.Config{SettlementBasis: "AFTER_DELIVERY", SettlementWindow: "P1D",
		SettlementBankCode: "BCA", SettlementAccountNumber: "2", SettlementAccountName: "bpp"}
	for i := 0; i < 2; i++ { // a resent init must not add the account twice
		if err := applySellerTerms(&c, cfg); err != nil {
			t.Fatal(err)
		}
	}
	var st map[string]any
	_ = json.Unmarshal(c.Settlements[0], &st)
	attrs := st["settlementAttributes"].(map[string]any)
	if attrs["settlementBasis"] != "AFTER_DELIVERY" || attrs["settlementWindow"] != "P1D" {
		t.Errorf("terms = %v", attrs)
	}
	if fee := attrs["finderFee"].(map[string]any); fee["feeAmount"] != 2670.0 {
		t.Errorf("feeAmount = %v, want 2670 (3%% of the 89,000 item total)", fee["feeAmount"])
	}
	if net := attrs["netPayable"].(map[string]any)["value"]; net != 96230.0 {
		t.Errorf("netPayable = %v, want 98,900 - 2,670", net)
	}
	accounts := asSlice(attrs["settlementAccounts"])
	if len(accounts) != 2 || accounts[1].(map[string]any)["holderRole"] != "BPP" || accounts[1].(map[string]any)["accountNumber"] != "2" {
		t.Errorf("settlementAccounts = %v", accounts)
	}
}

func TestApplySellerTermsConvertsAnIONPayment(t *testing.T) {
	c := InitContract{
		ContractAttributes: json.RawMessage(`{"finderFee": {"feeType": "AMOUNT", "feeValue": 1000}}`),
		Consideration:      []Consideration{{ID: "price-1", ConsiderationAttributes: json.RawMessage(`{"totalAmount": 5000}`)}},
		Settlements:        []json.RawMessage{json.RawMessage(`{"id": "s", "settlementAttributes": {"@type": "ion:IONPayment", "settlementAccounts": []}}`)},
	}
	if err := applySellerTerms(&c, &config.Config{SettlementBasis: "AFTER_DELIVERY"}); err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	_ = json.Unmarshal(c.Settlements[0], &st)
	attrs := st["settlementAttributes"].(map[string]any)
	if attrs["@type"] != "ion:IONSettlementReconciliation" || attrs["commission"].(map[string]any)["value"] != 1000.0 {
		t.Errorf("settlement = %v", attrs)
	}
}
