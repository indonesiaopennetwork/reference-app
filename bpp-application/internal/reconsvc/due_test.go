package reconsvc

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ion/winroom/bpp/internal/config"
)

func testService() *Service {
	return &Service{cfg: &config.Config{BppID: "bpp-1", SettlementBasis: "AFTER_DELIVERY", SettlementWindow: "P1D"}}
}

// testContract: 2 units at 45,000 (item total 90,000) plus 9,900 tax, paid
// 99,900 into the PJP (IONPayment), with a 3% finder fee on the item total
// and AFTER_DELIVERY / PT2M terms. One unit was then cancelled: its 45,000
// was refunded from the held funds, leaving an ACTIVE price of 45,000.
func testContract(t *testing.T) map[string]any {
	t.Helper()
	var c map[string]any
	err := json.Unmarshal([]byte(`{
	  "id": "c-1",
	  "contractAttributes": {"settlementBasis": "AFTER_DELIVERY", "settlementWindow": "PT2M",
	    "finderFee": {"feeType": "PERCENT", "feeValue": 3, "appliesTo": "ITEM_TOTAL"}},
	  "commitments": [{"id": "L1", "status": {"descriptor": {"code": "ACTIVE"}},
	    "resources": [{"id": "R1", "resourceAttributes": {"quantity": {"count": 1}}}], "offer": {"id": "O1"}}],
	  "consideration": [
	    {"id": "price-1", "status": {"code": "CANCELLED"}, "considerationAttributes": {"breakup": [
	      {"type": "BASE_PRICE", "amount": 90000}, {"type": "TAX", "amount": 9900}]}},
	    {"id": "price-1a", "status": {"code": "ACTIVE"}, "considerationAttributes": {"breakup": [
	      {"type": "BASE_PRICE", "amount": 45000}, {"type": "TAX", "amount": 4950}]}}],
	  "settlements": [
	    {"id": "settlement-a", "considerationId": "price-1", "status": "COMMITTED", "settlementAttributes": {
	      "@type": "ion:IONPayment", "collectedBy": "BAP", "currency": "IDR", "amount": 99900,
	      "paidAt": "2026-09-29T10:00:00Z",
	      "settlementAccounts": [{"holderRole": "BAP", "bankCode": "MANDIRI", "accountNumber": "1"},
	                             {"holderRole": "BPP", "bankCode": "BCA", "accountNumber": "2"}]}},
	    {"id": "refund-a-c1", "status": "COMMITTED", "settlementAttributes": {"currency": "IDR", "settledAmount": 54950,
	      "settledAt": "2026-09-29T10:05:00Z", "refund": {"amount": 44950, "source": "PAYMENT_GATEWAY"}}}
	  ]}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFiguresSplitTheHeldPayment(t *testing.T) {
	f, err := contractFigures(testContract(t), Terms{})
	if err != nil {
		t.Fatal(err)
	}
	// Commission: 3% of the 45,000 still ordered. BPP: 99,900 - 1,350 - 44,950.
	if f.OrderAmount != 99900 || f.Refund != 44950 || f.Commission != 1350 || f.NetPayable != 53600 {
		t.Errorf("order=%v refund=%v commission=%v net=%v", f.OrderAmount, f.Refund, f.Commission, f.NetPayable)
	}
	if f.OrderAmount != f.NetPayable+f.Commission+f.Withholding+f.Refund {
		t.Error("figures must split the order amount")
	}
	if f.SettlementID != "settlement-a" || len(f.RefundSettlementIDs) != 1 || f.Basis != "AFTER_DELIVERY" || f.Window != "PT2M" {
		t.Errorf("settlement=%s refunds=%v basis=%s window=%s", f.SettlementID, f.RefundSettlementIDs, f.Basis, f.Window)
	}
}

func TestAgreedFeeAmountIsTheCommission(t *testing.T) {
	c := testContract(t)
	asMap(asMap(c["contractAttributes"])["finderFee"])["feeAmount"] = 1500.0
	f, _ := contractFigures(c, Terms{})
	if f.Commission != 1500 || f.NetPayable != 99900-44950-1500 {
		t.Errorf("commission=%v net=%v, want the agreed 1500", f.Commission, f.NetPayable)
	}
	// The reconcile bag states the commission; the terms are not part of it.
	bag := f.bag()
	if got := asMap(bag["commission"])["value"]; got != 1500.0 {
		t.Errorf("bag commission = %v", got)
	}
	for _, k := range []string{"finderFee", "settlementBasis", "settlementWindow"} {
		if _, has := bag[k]; has {
			t.Errorf("bag carries %s", k)
		}
	}
	// Without feeAmount (agreed before it existed) the fee is computed.
	delete(asMap(asMap(c["contractAttributes"])["finderFee"]), "feeAmount")
	f, _ = contractFigures(c, Terms{})
	if f.Commission != 1350 || asMap(f.bag()["commission"])["value"] != 1350.0 {
		t.Errorf("computed commission=%v bag=%v", f.Commission, f.bag()["commission"])
	}
	if _, has := asMap(asMap(c["contractAttributes"])["finderFee"])["feeAmount"]; has {
		t.Error("building the bag must not change the stored contract")
	}
}

func TestFinderFeeVariants(t *testing.T) {
	cases := []struct {
		fee  map[string]any
		want float64
	}{
		{nil, 0},
		{map[string]any{"feeType": "AMOUNT", "feeValue": 2000.0}, 2000},
		{map[string]any{"feeType": "PERCENT", "feeValue": 2.0, "appliesTo": "ORDER_TOTAL"}, 1998},
	}
	for _, tc := range cases {
		c := testContract(t)
		asMap(c["contractAttributes"])["finderFee"] = tc.fee
		if tc.fee == nil {
			delete(asMap(c["contractAttributes"]), "finderFee")
		}
		f, _ := contractFigures(c, Terms{})
		if f.Commission != tc.want || f.NetPayable != round2(99900-44950-tc.want) {
			t.Errorf("fee %v: commission=%v net=%v", tc.fee, f.Commission, f.NetPayable)
		}
	}
}

func TestRefundAfterPayoutIsOutsideReconciliation(t *testing.T) {
	c := testContract(t)
	refund := asMap(asMap(asMap(slice(c["settlements"])[1])["settlementAttributes"])["refund"])
	refund["source"] = "PROVIDER"
	f, _ := contractFigures(c, Terms{})
	if f.Refund != 0 || f.RefundedAfterPayout != 44950 || len(f.RefundSettlementIDs) != 0 {
		t.Errorf("refund=%v afterPayout=%v ids=%v", f.Refund, f.RefundedAfterPayout, f.RefundSettlementIDs)
	}
}

func TestLegacyPaymentAndTerms(t *testing.T) {
	c := testContract(t)
	delete(c, "contractAttributes")
	pa := asMap(asMap(slice(c["settlements"])[0])["settlementAttributes"])
	delete(pa, "amount")
	pa["settledAmount"], pa["settlementBasis"] = 99900.0, "DELIVERY"
	f, _ := contractFigures(c, Terms{Basis: "ON_CONFIRMATION", Window: "P3D"})
	if f.OrderAmount != 99900 || f.Basis != "AFTER_DELIVERY" || f.Window != "P3D" || f.Commission != 0 {
		t.Errorf("order=%v basis=%s window=%s commission=%v", f.OrderAmount, f.Basis, f.Window, f.Commission)
	}
}

func TestDueAfterDeliveryPlusWindow(t *testing.T) {
	s := testService()
	delivered := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	d := DueContract{contract: testContract(t)}
	if !s.assess(&d, &delivered, nil, delivered.Add(time.Minute)) {
		t.Fatal("contract not yet reconciled must be listed")
	}
	if d.Due || d.DueAt == nil || !d.DueAt.Equal(delivered.Add(2*time.Minute)) {
		t.Fatalf("1 min after delivery: due=%v dueAt=%v, want not due, due at delivery+2m", d.Due, d.DueAt)
	}
	d = DueContract{contract: testContract(t)}
	s.assess(&d, &delivered, nil, delivered.Add(3*time.Minute))
	if !d.Due {
		t.Error("3 min after delivery with a PT2M window: must be due")
	}
}

func TestNotDueBeforeDelivery(t *testing.T) {
	d := DueContract{contract: testContract(t)}
	testService().assess(&d, nil, nil, time.Now())
	if d.Due || d.DueAt != nil || d.BasisEvent != "awaiting delivery" {
		t.Errorf("undelivered: due=%v dueAt=%v event=%s", d.Due, d.DueAt, d.BasisEvent)
	}
}

func TestCancelledBeforeDeliveryCountsFromCancellation(t *testing.T) {
	cancelled := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	d := DueContract{contract: testContract(t)}
	testService().assess(&d, nil, &cancelled, cancelled.Add(5*time.Minute))
	if !d.Due || d.BasisEvent != "cancelled" {
		t.Errorf("cancelled: due=%v event=%s, want due from the cancellation", d.Due, d.BasisEvent)
	}
}

func TestPendingAndAgreedAreSkippedDisputedIsResent(t *testing.T) {
	for status, listed := range map[string]bool{"PENDING": false, "AGREED": false, "DISPUTED": true} {
		c := testContract(t)
		pa := asMap(asMap(slice(c["settlements"])[0])["settlementAttributes"])
		pa["reconciliationId"], pa["reconciliationStatus"] = "recon-1", status
		d := DueContract{contract: c}
		if got := testService().assess(&d, nil, nil, time.Now()); got != listed {
			t.Errorf("%s: listed=%v, want %v", status, got, listed)
		}
	}
}

func TestCompareAgreesOrReportsDifferences(t *testing.T) {
	own, _ := contractFigures(testContract(t), Terms{})
	if _, accord, _ := compare(own.bag(), own); !accord {
		t.Error("identical figures must agree")
	}

	theirs := own.bag()
	theirs["commission"] = map[string]any{"value": 1000.0}
	theirs["netPayable"] = map[string]any{"value": 53950.0}
	reply, accord, dispute := compare(theirs, own)
	if accord || str(dispute["code"]) != "COMMISSION_MISMATCH" {
		t.Fatalf("accord=%v dispute=%v", accord, dispute)
	}
	if num(asMap(reply["commission"])["diffValue"]) != 350 || num(asMap(reply["netPayable"])["diffValue"]) != -350 {
		t.Errorf("diffs: commission=%v net=%v", reply["commission"], reply["netPayable"])
	}
	if _, ok := asMap(reply["orderAmount"])["diffValue"]; ok {
		t.Error("matching components carry no diffValue")
	}
}

func TestSnapshotCarriesMinimalCommitments(t *testing.T) {
	c := testContract(t)
	f, _ := contractFigures(c, Terms{})
	snap := snapshot(c, f, f.bag())
	cm := asMap(slice(snap["commitments"])[0])
	res := asMap(slice(cm["resources"])[0])
	if str(res["id"]) != "R1" || num(asMap(res["quantity"])["count"]) != 1 {
		t.Errorf("resource = %v", res)
	}
	if ids := slice(asMap(cm["offer"])["resourceIds"]); len(ids) != 1 || str(ids[0]) != "R1" {
		t.Errorf("offer = %v", cm["offer"])
	}
	st := asMap(slice(snap["settlements"])[0])
	if str(st["id"]) != "settlement-a" || str(asMap(st["settlementAttributes"])["@type"]) != reconcileType {
		t.Errorf("settlement = %v", st)
	}
}

// offerTermsContract is testContract with its terms on two offers instead:
// O1 pays out PT2M after delivery, O2 P1D after confirmation (payment).
func offerTermsContract(t *testing.T) map[string]any {
	t.Helper()
	c := testContract(t)
	delete(c, "contractAttributes")
	policy := func(basis, window string) map[string]any {
		return map[string]any{"policies": map[string]any{"settlement": map[string]any{"settlementBasis": basis, "settlementWindow": window}}}
	}
	cms := c["commitments"].([]any)
	asMap(asMap(cms[0])["offer"])["offerAttributes"] = policy("AFTER_DELIVERY", "PT2M")
	second := map[string]any{"id": "L2", "status": map[string]any{"descriptor": map[string]any{"code": "ACTIVE"}},
		"resources": []any{map[string]any{"id": "R2"}},
		"offer":     map[string]any{"id": "O2", "offerAttributes": policy("ON_CONFIRMATION", "P1D")}}
	c["commitments"] = append(cms, second)
	return c
}

func TestTheLatestOfferTermDecidesTheDueDate(t *testing.T) {
	s := testService()
	paid := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	delivered := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	d := DueContract{contract: offerTermsContract(t)}
	s.assess(&d, &delivered, nil, delivered.Add(time.Hour))
	// O1 is due at delivery + 2m, O2 at payment + 1d: the later one applies.
	if d.DueAt == nil || !d.DueAt.Equal(paid.Add(24*time.Hour)) || d.Basis != "ON_CONFIRMATION" || d.Window != "P1D" || d.Due {
		t.Errorf("dueAt=%v basis=%s window=%s due=%v, want payment + P1D, not yet due", d.DueAt, d.Basis, d.Window, d.Due)
	}

	// Before delivery O1's due date isn't known, so neither is the contract's.
	d = DueContract{contract: offerTermsContract(t)}
	s.assess(&d, nil, nil, paid.Add(48*time.Hour))
	if d.Due || d.DueAt != nil || d.BasisEvent != "awaiting delivery" {
		t.Errorf("undelivered: due=%v dueAt=%v event=%s", d.Due, d.DueAt, d.BasisEvent)
	}
}
