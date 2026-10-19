package reconcilesvc

import (
	"encoding/json"
	"testing"
	"time"
)

// testContract is the BAP's copy of the order the BPP tests use (bpp
// reconsvc/due_test.go): 99,900 paid into the PJP, 3% finder fee on the item
// total, one of two units cancelled with 44,950 refunded from the held funds.
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

func TestBuyerAppComputesTheSameFigures(t *testing.T) {
	f, err := contractFigures(testContract(t), defaultTerms)
	if err != nil {
		t.Fatal(err)
	}
	// The BPP's test expects exactly these for the same contract.
	if f.OrderAmount != 99900 || f.Refund != 44950 || f.Commission != 1350 || f.NetPayable != 53600 {
		t.Errorf("order=%v refund=%v commission=%v net=%v", f.OrderAmount, f.Refund, f.Commission, f.NetPayable)
	}
}

func due(t *testing.T, c map[string]any) DueContract {
	return DueContract{own: stored{id: "c-1", contract: c}}
}

func TestDueAfterDeliveryPlusWindow(t *testing.T) {
	delivered := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	d := due(t, testContract(t))
	if !assess(&d, &delivered, delivered, delivered.Add(time.Minute)) || d.Due {
		t.Fatalf("1 min after delivery: due=%v", d.Due)
	}
	d = due(t, testContract(t))
	assess(&d, &delivered, delivered, delivered.Add(3*time.Minute))
	if !d.Due || d.BasisEvent != "delivered" {
		t.Errorf("3 min after delivery with PT2M: due=%v event=%s", d.Due, d.BasisEvent)
	}
}

func TestCancelledCountsFromTheCancellation(t *testing.T) {
	c := testContract(t)
	c["status"] = map[string]any{"code": "CANCELLED"}
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	d := due(t, c)
	assess(&d, nil, at, at.Add(5*time.Minute))
	if !d.Due || d.BasisEvent != "cancelled" {
		t.Errorf("cancelled: due=%v event=%s", d.Due, d.BasisEvent)
	}
}

func TestReturnWindowBasisIsLeftToTheSeller(t *testing.T) {
	c := testContract(t)
	asMap(c["contractAttributes"])["settlementBasis"] = "AFTER_RETURN_WINDOW"
	delivered := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	d := due(t, c)
	assess(&d, &delivered, delivered, delivered.Add(48*time.Hour))
	if d.Due || d.BasisEvent != "awaiting return window" {
		t.Errorf("due=%v event=%s", d.Due, d.BasisEvent)
	}
}

func TestPendingAndAgreedAreSkippedDisputedIsResent(t *testing.T) {
	for status, listed := range map[string]bool{"PENDING": false, "AGREED": false, "DISPUTED": true} {
		c := testContract(t)
		pa := asMap(asMap(slice(c["settlements"])[0])["settlementAttributes"])
		pa["reconciliationId"], pa["reconciliationStatus"] = "recon-1", status
		d := due(t, c)
		if got := assess(&d, nil, time.Now(), time.Now()); got != listed {
			t.Errorf("%s: listed=%v, want %v", status, got, listed)
		}
	}
}

func TestReplyReplacesTheSettlement(t *testing.T) {
	c := map[string]any{"id": "c-1", "commitments": []any{"x"}, "settlements": []any{map[string]any{"id": "s"}}}
	r := reply(c, "s", "price-1", map[string]any{"accord": true})
	st := asMap(slice(r["settlements"])[0])
	if str(st["considerationId"]) != "price-1" || asMap(st["settlementAttributes"])["accord"] != true || len(slice(r["commitments"])) != 1 {
		t.Errorf("reply = %v", r)
	}
	if asMap(slice(c["settlements"])[0])["settlementAttributes"] != nil {
		t.Error("the received contract must not be modified")
	}
}

func TestCheckCommission(t *testing.T) {
	// An order placed before the commission moved to the offer: its finderFee.
	c := testContract(t)
	if _, _, ok := CheckCommission(c); ok {
		t.Error("no feeAmount stated: nothing to check")
	}
	fee := asMap(asMap(c["contractAttributes"])["finderFee"])
	fee["feeAmount"] = 1350.0 // 3% of the 45,000 still ordered
	if stated, computed, ok := CheckCommission(c); !ok || stated != computed {
		t.Errorf("stated=%v computed=%v ok=%v, want a match", stated, computed, ok)
	}
	fee["feeAmount"] = 2700.0
	if stated, computed, _ := CheckCommission(c); stated == computed || computed != 1350 {
		t.Errorf("stated=%v computed=%v, want a mismatch against 1350", stated, computed)
	}

	// The seller's commission on the offer: 5% of one unit at 45,000.
	c = testContract(t)
	delete(c, "contractAttributes")
	cm := asMap(slice(c["commitments"])[0])
	cm["commitmentAttributes"] = map[string]any{"price": map[string]any{"value": 45000.0}}
	asMap(cm["offer"])["considerations"] = []any{map[string]any{"id": "p", "considerationAttributes": map[string]any{
		"commission": map[string]any{"type": "PERCENT", "value": 5.0, "appliesTo": "ITEM_TOTAL"}}}}
	price := asMap(asMap(slice(c["consideration"])[1])["considerationAttributes"])
	price["commission"] = map[string]any{"type": "PERCENT", "value": 5.0, "appliesTo": "ITEM_TOTAL", "amount": 2250.0}
	if stated, computed, ok := CheckCommission(c); !ok || stated != 2250 || computed != 2250 {
		t.Errorf("stated=%v computed=%v ok=%v, want 2250 both", stated, computed, ok)
	}
	asMap(price["commission"])["amount"] = 9000.0
	if stated, computed, _ := CheckCommission(c); stated == computed {
		t.Errorf("stated=%v computed=%v, want a mismatch", stated, computed)
	}
}
