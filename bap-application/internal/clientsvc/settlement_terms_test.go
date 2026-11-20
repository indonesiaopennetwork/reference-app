package clientsvc

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/config"
)

// paidSession is a stand-in for the PAID payment_sessions row attachSettlement
// expects -- what dbsqlc.GetLatestPaidPaymentSession would return after a real
// DOKU notification.
func paidSession() *dbsqlc.PaymentSession {
	gatewayRef := "ref-1"
	method := "QRIS"
	return &dbsqlc.PaymentSession{
		InvoiceNumber: "inv-1",
		GatewayRef:    &gatewayRef,
		Method:        &method,
		PaidAt:        pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
}

func termsConfig() *config.Config {
	return &config.Config{SettlementBankCode: "MANDIRI", SettlementAccountNumber: "1", SettlementAccountName: "bap"}
}

// pricedContract is a quote (on_select) for one unit of an offer whose
// seller pays a 3% buyer finder fee on the item total.
func pricedContract() map[string]interface{} {
	return map[string]interface{}{
		"commitments": []interface{}{map[string]interface{}{"id": "c1",
			"commitmentAttributes": map[string]interface{}{"price": map[string]interface{}{"value": 90000.0}},
			"offer": map[string]interface{}{"id": "o1", "considerations": []interface{}{map[string]interface{}{"id": "p",
				"considerationAttributes": map[string]interface{}{"totalAmount": 99900.0,
					"buyerFinderFee": map[string]interface{}{"feeType": "PERCENT", "feeValue": 3.0, "appliesTo": "ITEM_TOTAL"}}}}}}},
		"consideration": []interface{}{map[string]interface{}{"id": "price-1",
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
	if _, ok := sa["finderFee"]; ok {
		t.Errorf("the buyer app declares no finder fee: %v", sa["finderFee"])
	}
	// Estimated on the quoted price: the seller's 3% of the 90,000 item total.
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
	// What the BPP answered in on_init: its account, and the commission its
	// final price comes to.
	sa := asObj(asObj(asSlice(c["settlements"])[0])["settlementAttributes"])
	sa["settlementAccounts"] = append(asSlice(sa["settlementAccounts"]), map[string]interface{}{"holderRole": "BPP", "bankCode": "BCA", "accountNumber": "2"})
	price := asObj(asObj(asSlice(c["consideration"])[0])["considerationAttributes"])
	asObj(price["buyerFinderFee"])["feeAmount"] = 2500.0
	declaredID := asObj(asSlice(c["settlements"])[0])["id"]
	if err := attachSettlement(c, termsConfig(), paidSession()); err != nil {
		t.Fatal(err)
	}
	st := asObj(asSlice(c["settlements"])[0])
	paid := asObj(st["settlementAttributes"])
	if st["id"] != declaredID || st["status"] != "COMMITTED" || paid["status"] != "PAID" || paid["paidAt"] == nil || paid["gatewayRef"] == nil {
		t.Errorf("paid settlement = %v", st)
	}
	if amount(paid, "orderAmount") != 99900.0 || amount(paid, "commission") != 2500.0 || amount(paid, "netPayable") != 97400.0 {
		t.Errorf("figures must use the commission the BPP stated: %v", paid)
	}
	if len(asSlice(paid["settlementAccounts"])) != 2 {
		t.Errorf("accounts agreed in init/on_init must be kept: %v", paid)
	}
}
