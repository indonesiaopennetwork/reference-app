package clientsvc

import (
	"testing"

	"github.com/ion/winroom/bap/internal/config"
)

func termsConfig() *config.Config {
	return &config.Config{FinderFeeType: "PERCENT", FinderFeeValue: 3, FinderFeeAppliesTo: "ITEM_TOTAL",
		SettlementBankCode: "MANDIRI", SettlementAccountNumber: "1", SettlementAccountName: "bap"}
}

func pricedContract() map[string]interface{} {
	return map[string]interface{}{"consideration": []interface{}{map[string]interface{}{"id": "price-1",
		"considerationAttributes": map[string]interface{}{"totalAmount": 99900.0, "currency": "IDR",
			"breakup": []interface{}{map[string]interface{}{"type": "BASE_PRICE", "amount": 90000.0}}}}}}
}

func amount(attrs map[string]interface{}, key string) interface{} { return asObj(attrs[key])["value"] }

func TestInitDeclaresTheSettlement(t *testing.T) {
	c := pricedContract()
	if err := declareSettlement(c, termsConfig()); err != nil {
		t.Fatal(err)
	}
	st := asObj(asSlice(c["settlements"])[0])
	sa := asObj(st["settlementAttributes"])
	if sa["@type"] != "ion:IONSettlementReconciliation" || sa["collectedBy"] != "BAP" || sa["status"] != "PENDING" || st["considerationId"] != "price-1" {
		t.Errorf("settlement = %v", st)
	}
	if fee := asObj(sa["finderFee"]); fee["feeType"] != "PERCENT" || fee["feeValue"] != 3.0 || fee["appliesTo"] != "ITEM_TOTAL" {
		t.Errorf("finderFee = %v", fee)
	}
	// Estimated on the quoted price: 3% of the 90,000 item total.
	if amount(sa, "orderAmount") != 99900.0 || amount(sa, "commission") != 2700.0 || amount(sa, "netPayable") != 97200.0 {
		t.Errorf("figures = %v", sa)
	}
	if acc := asObj(asSlice(sa["settlementAccounts"])[0]); acc["holderRole"] != "BAP" || acc["accountNumber"] != "1" {
		t.Errorf("account = %v", acc)
	}
	if c["contractAttributes"] != nil {
		t.Errorf("terms belong on the settlement, not contractAttributes: %v", c["contractAttributes"])
	}
}

func TestConfirmPaysTheSettlementKeepingTheAgreedTerms(t *testing.T) {
	c := pricedContract()
	_ = declareSettlement(c, termsConfig())
	// What the BPP answered in on_init.
	sa := asObj(asObj(asSlice(c["settlements"])[0])["settlementAttributes"])
	sa["settlementAccounts"] = append(asSlice(sa["settlementAccounts"]), map[string]interface{}{"holderRole": "BPP", "bankCode": "BCA", "accountNumber": "2"})
	sa["settlementBasis"], sa["settlementWindow"] = "AFTER_DELIVERY", "P1D"
	asObj(sa["finderFee"])["feeAmount"] = 2500.0
	declaredID := asObj(asSlice(c["settlements"])[0])["id"]
	if err := attachSettlement(c, termsConfig()); err != nil {
		t.Fatal(err)
	}
	st := asObj(asSlice(c["settlements"])[0])
	paid := asObj(st["settlementAttributes"])
	if st["id"] != declaredID || st["status"] != "COMMITTED" || paid["status"] != "PAID" || paid["paidAt"] == nil || paid["gatewayRef"] == nil {
		t.Errorf("paid settlement = %v", st)
	}
	if amount(paid, "orderAmount") != 99900.0 || amount(paid, "commission") != 2500.0 || amount(paid, "netPayable") != 97400.0 {
		t.Errorf("figures must use the fee the BPP stated: %v", paid)
	}
	if len(asSlice(paid["settlementAccounts"])) != 2 || paid["settlementBasis"] != "AFTER_DELIVERY" {
		t.Errorf("terms and accounts agreed in init/on_init must be kept: %v", paid)
	}
}
