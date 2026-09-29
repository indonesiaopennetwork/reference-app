package cancellation

import (
	"encoding/json"
	"testing"
	"time"
)

// contractJSON is a confirmed order: 2 × Tax Test Cake at Rp 45,000 + PPN 12%
// on an 11/12 base (Rp 4,950/unit), paid Rp 99,900.
const contractJSON = `{
  "id": "c-1", "status": {"code": "ACTIVE"},
  "commitments": [{
    "id": "commitment-cake", "status": {"descriptor": {"code": "ACTIVE"}},
    "resources": [{"id": "item-cake", "quantity": {"unitCode": "EA", "unitQuantity": 2}}],
    "offer": {"id": "offer-cake", "descriptor": {"name": "Tax Test Cake"},
      "offerAttributes": {"policies": {"cancellation": {"allowed": true, "fee": {"type": "FIXED", "value": 5000}}}},
      "considerations": [{"id": "offer-cake-price", "considerationAttributes": {"currency": "IDR", "totalAmount": 49950,
        "breakup": [{"type": "BASE_PRICE", "title": "Tax Test Cake", "amount": 45000},
                    {"type": "TAX", "title": "PPN 12%", "amount": 4950, "taxDetail": {"rate": 0.12, "taxableBase": 41250, "taxRegime": "PPN"}}]}}]},
    "commitmentAttributes": {"quantity": {"unitCode": "EA", "unitQuantity": 2}}
  }],
  "performance": [{"id": "f1", "commitmentIds": ["commitment-cake"]}],
  "consideration": [{"id": "consideration-105ad328", "status": {"code": "QUOTED"},
    "considerationAttributes": {"@type": "ion:IONTradeConsideration", "currency": "IDR", "totalAmount": 99900,
      "breakup": [{"type": "BASE_PRICE", "title": "Tax Test Cake", "amount": 90000},
                  {"type": "TAX", "title": "PPN 12%", "amount": 9900}]}}],
  "settlements": [{"id": "settlement-8477dc8d", "considerationId": "consideration-105ad328", "status": "COMPLETE",
    "settlementAttributes": {"method": "ONLINE", "settledAmount": 99900, "currency": "IDR"}}]
}`

var reason = Request{ReasonCode: "CHANGE_OF_MIND"}

func load(t *testing.T) map[string]any {
	t.Helper()
	var c map[string]any
	if err := json.Unmarshal([]byte(contractJSON), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func byID(t *testing.T, list any, id string) map[string]any {
	t.Helper()
	for _, v := range slice(list) {
		if str(asMap(v)["id"]) == id {
			return asMap(v)
		}
	}
	t.Fatalf("no entry %q in %v", id, list)
	return nil
}

func TestPartialCancelOneOfTwoUnits(t *testing.T) {
	orig := load(t)
	res, err := Apply(orig, []Item{{CommitmentID: "commitment-cake", Quantity: 1}}, reason, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c := res.Contract
	if res.Full || str(asMap(c["status"])["code"]) != "ACTIVE" {
		t.Fatalf("partial cancel must leave the contract ACTIVE, got full=%v status=%v", res.Full, c["status"])
	}

	old := byID(t, c["commitments"], "commitment-cake")
	if commitmentCode(old) != "CLOSED" || commitmentQty(old) != 2 {
		t.Errorf("old commitment: code=%s qty=%v, want CLOSED with original qty 2", commitmentCode(old), commitmentQty(old))
	}
	repl := byID(t, c["commitments"], "commitment-cake-a1")
	if commitmentCode(repl) != "ACTIVE" || commitmentQty(repl) != 1 {
		t.Errorf("replacement: code=%s qty=%v, want ACTIVE qty 1", commitmentCode(repl), commitmentQty(repl))
	}
	if ids := slice(asMap(slice(c["performance"])[0])["commitmentIds"]); len(ids) != 2 {
		t.Errorf("performance commitmentIds = %v, want old + replacement", ids)
	}

	if code := str(asMap(byID(t, c["consideration"], "consideration-105ad328")["status"])["code"]); code != "CANCELLED" {
		t.Errorf("original consideration status = %s, want CANCELLED", code)
	}
	active := asMap(byID(t, c["consideration"], "consideration-105ad328-a1")["considerationAttributes"])
	if num(active["totalAmount"]) != 49950 {
		t.Errorf("active consideration total = %v, want 49950", active["totalAmount"])
	}
	fee := asMap(byID(t, c["consideration"], "cancellation-105ad328-c1")["considerationAttributes"])
	if num(fee["totalAmount"]) != 5000 {
		t.Errorf("fee consideration total = %v, want 5000", fee["totalAmount"])
	}

	if res.Refund != 44950 || res.Fee != 5000 {
		t.Errorf("refund=%v fee=%v, want 44950 / 5000", res.Refund, res.Fee)
	}
	if str(byID(t, c["settlements"], "settlement-8477dc8d")["status"]) != "COMPLETE" {
		t.Error("original payment settlement must stay COMPLETE")
	}
	refund := byID(t, c["settlements"], "refund-8477dc8d-c1")
	sa := asMap(refund["settlementAttributes"])
	if str(refund["status"]) != "COMMITTED" || num(asMap(sa["refund"])["amount"]) != 44950 {
		t.Errorf("refund settlement = %v", refund)
	}
	// 99,900 paid − 44,950 refunded = 54,950 still settled (49,950 owed + 5,000 fee).
	if num(sa["settledAmount"]) != 54950 {
		t.Errorf("settledAmount = %v, want 54950", sa["settledAmount"])
	}
	if str(refund["considerationId"]) != "consideration-105ad328-a1" {
		t.Errorf("refund settlement discharges %v, want the ACTIVE consideration", refund["considerationId"])
	}
	if res.RestoredUnits["item-cake"] != 1 {
		t.Errorf("restored units = %v, want 1 × item-cake", res.RestoredUnits)
	}
	if commitmentQty(asMap(slice(orig["commitments"])[0])) != 2 || len(slice(orig["commitments"])) != 1 {
		t.Error("Apply mutated its input contract")
	}
}

func TestFullCancel(t *testing.T) {
	res, err := Apply(load(t), nil, reason, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c := res.Contract
	if !res.Full || str(asMap(c["status"])["code"]) != "CANCELLED" {
		t.Fatalf("full cancel: full=%v status=%v", res.Full, c["status"])
	}
	if len(slice(c["commitments"])) != 1 || commitmentCode(asMap(slice(c["commitments"])[0])) != "CLOSED" {
		t.Errorf("commitments = %v, want the single one CLOSED with no replacement", c["commitments"])
	}
	for _, cons := range slice(c["consideration"]) {
		if str(asMap(cons)["id"]) == "consideration-105ad328-a1" {
			t.Error("full cancel must not issue a replacement price consideration")
		}
	}
	if res.Fee != 5000 || res.Refund != 94900 || res.RestoredUnits["item-cake"] != 2 {
		t.Errorf("fee=%v refund=%v restored=%v, want 5000 / 94900 / 2", res.Fee, res.Refund, res.RestoredUnits)
	}
}

func TestSecondPartialCancelChainsReplacements(t *testing.T) {
	first, err := Apply(load(t), []Item{{CommitmentID: "commitment-cake", Quantity: 1}}, reason, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Apply(first.Contract, []Item{{CommitmentID: "commitment-cake-a1"}}, reason, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Full {
		t.Fatal("cancelling the last remaining unit must cancel the contract")
	}
	if code := str(asMap(byID(t, second.Contract["consideration"], "consideration-105ad328-a1")["status"])["code"]); code != "CANCELLED" {
		t.Errorf("replacement consideration status = %s, want CANCELLED", code)
	}
	byID(t, second.Contract["consideration"], "cancellation-105ad328-c2")
	r2 := byID(t, second.Contract["settlements"], "refund-8477dc8d-c2")
	if second.Refund != 44950 {
		t.Errorf("second refund = %v, want 49950 − 5000 = 44950", second.Refund)
	}
	// 54,950 settled − 44,950 refunded = 10,000: the two retained fees.
	if got := num(asMap(r2["settlementAttributes"])["settledAmount"]); got != 10000 {
		t.Errorf("settledAmount after second cancel = %v, want 10000", got)
	}
	if str(r2["considerationId"]) != "consideration-105ad328-a1" {
		t.Errorf("full cancel: considerationId = %v, want the last price consideration", r2["considerationId"])
	}
}

func TestPercentageFeeOnPaidAmount(t *testing.T) {
	c := load(t)
	pol := asMap(asMap(asMap(asMap(asMap(slice(c["commitments"])[0])["offer"])["offerAttributes"])["policies"])["cancellation"])
	pol["fee"] = map[string]any{"type": "PERCENTAGE", "value": 10.0}
	res, err := Apply(c, []Item{{CommitmentID: "commitment-cake", Quantity: 1}}, reason, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Fee != 4995 || res.Refund != 44955 {
		t.Errorf("fee=%v refund=%v, want 10%% of 49950 = 4995 / 44955", res.Fee, res.Refund)
	}
}

func TestCancellationNotAllowed(t *testing.T) {
	c := load(t)
	asMap(asMap(asMap(asMap(asMap(slice(c["commitments"])[0])["offer"])["offerAttributes"])["policies"])["cancellation"])["allowed"] = false
	if _, err := Apply(c, nil, reason, time.Now()); err == nil {
		t.Fatal("expected ErrNotAllowed")
	}
}

func TestItemsFromUpdate(t *testing.T) {
	stored := load(t)
	reduced := load(t)
	setCommitmentQty(asMap(slice(reduced["commitments"])[0]), 1)
	if items := ItemsFromUpdate(stored, reduced); len(items) != 1 || items[0].Quantity != 1 {
		t.Errorf("reduced quantity: items = %v, want cancel 1", items)
	}

	closed := load(t)
	setCommitmentStatus(asMap(slice(closed["commitments"])[0]), "CLOSED", "")
	if items := ItemsFromUpdate(stored, closed); len(items) != 1 || items[0].Quantity != 2 {
		t.Errorf("closed commitment: items = %v, want cancel 2", items)
	}

	// A return adds a RETURN performance and leaves commitments untouched.
	ret := load(t)
	ret["performance"] = append(slice(ret["performance"]), map[string]any{"id": "return-1"})
	if items := ItemsFromUpdate(stored, ret); items != nil {
		t.Errorf("return update: items = %v, want none", items)
	}
}

func TestReasonRequiredAndRecorded(t *testing.T) {
	if _, err := Apply(load(t), nil, Request{}, time.Now()); err == nil {
		t.Fatal("expected an error when no reason is given")
	}
	res, err := Apply(load(t), []Item{{CommitmentID: "commitment-cake", Quantity: 1}}, reason, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	canc := asMap(asMap(res.Contract["contractAttributes"])["cancellation"])
	if str(asMap(canc["reason"])["code"]) != "CHANGE_OF_MIND" {
		t.Errorf("recorded reason = %v", canc["reason"])
	}
	if ids := slice(canc["commitmentIds"]); len(ids) != 1 || str(ids[0]) != "commitment-cake" {
		t.Errorf("recorded commitmentIds = %v", ids)
	}
}

func TestReasonMustBeAllowedByOffer(t *testing.T) {
	c := load(t)
	pol := asMap(asMap(asMap(asMap(asMap(slice(c["commitments"])[0])["offer"])["offerAttributes"])["policies"])["cancellation"])
	pol["allowedReasons"] = []any{"WRONG_ITEM_ORDERED"}
	if _, err := Apply(c, nil, reason, time.Now()); err == nil {
		t.Fatal("expected CHANGE_OF_MIND to be rejected")
	}
	if _, err := Apply(c, nil, Request{ReasonCode: "WRONG_ITEM_ORDERED"}, time.Now()); err != nil {
		t.Fatalf("allowed reason rejected: %v", err)
	}
}

func TestItemsFromUpdateUsesDeclaredCommitmentIDs(t *testing.T) {
	requested := load(t)
	requested["contractAttributes"] = map[string]any{"cancellation": map[string]any{
		"reason": map[string]any{"code": "CHANGE_OF_MIND"}, "commitmentIds": []any{"commitment-cake"},
	}}
	if items := ItemsFromUpdate(load(t), requested); len(items) != 1 || items[0].Quantity != 2 {
		t.Errorf("items = %v, want the declared commitment cancelled in full", items)
	}
}

func TestSettledAmountWithoutFee(t *testing.T) {
	// The reported case: 77,700 paid, cancel Rp 49,950 of it, no fee.
	c := load(t)
	pol := asMap(asMap(asMap(asMap(asMap(slice(c["commitments"])[0])["offer"])["offerAttributes"])["policies"])["cancellation"])
	delete(pol, "fee")
	res, err := Apply(c, []Item{{CommitmentID: "commitment-cake", Quantity: 1}}, reason, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Refund != 49950 || res.SettledAfter != 49950 {
		t.Errorf("refund=%v settledAfter=%v, want 49950 / 49950 (99,900 − 49,950)", res.Refund, res.SettledAfter)
	}
}

func TestCutoffBeforePacking(t *testing.T) {
	c := load(t)
	pol := asMap(asMap(asMap(asMap(asMap(slice(c["commitments"])[0])["offer"])["offerAttributes"])["policies"])["cancellation"])
	pol["cutoffEvent"] = "BEFORE_PACKING"

	if _, err := Apply(c, nil, reason, time.Now()); err != nil {
		t.Fatalf("not yet packed: cancellation must be allowed, got %v", err)
	}

	asMap(slice(c["performance"])[0])["status"] = map[string]any{"code": "Packed"}
	if _, err := Apply(c, nil, reason, time.Now()); err == nil {
		t.Fatal("packed order: cancellation must be refused")
	}

	// The BPP's own recorded status counts even when the contract lags behind.
	c2 := load(t)
	asMap(asMap(asMap(asMap(asMap(slice(c2["commitments"])[0])["offer"])["offerAttributes"])["policies"])["cancellation"])["cutoffEvent"] = "BEFORE_PACKING"
	if _, err := Apply(c2, nil, Request{ReasonCode: "CHANGE_OF_MIND", FulfillmentStatus: "Shipped"}, time.Now()); err == nil {
		t.Fatal("shipped per BPP records: cancellation must be refused")
	}
}

func TestCutoffPassed(t *testing.T) {
	cases := []struct {
		cutoff, status string
		want           bool
	}{
		{"BEFORE_PACKING", "PENDING", false},
		{"BEFORE_PACKING", "Packed", true},
		{"BEFORE_PACKING", "Delivered", true},
		{"BEFORE_DISPATCH", "Packed", false},
		{"BEFORE_DISPATCH", "Shipped", true},
		{"BEFORE_SHIPMENT", "Packed", false},
		{"BEFORE_CONFIRMATION", "PENDING", true},
		{"", "Delivered", false},
	}
	for _, tc := range cases {
		if got := CutoffPassed(tc.cutoff, StageRank(tc.status)); got != tc.want {
			t.Errorf("CutoffPassed(%s, %s) = %v, want %v", tc.cutoff, tc.status, got, tc.want)
		}
	}
}

func TestDeliveredCannotBeCancelled(t *testing.T) {
	c := load(t)
	asMap(slice(c["performance"])[0])["status"] = map[string]any{"code": "Delivered"}
	if _, err := Apply(c, nil, reason, time.Now()); err == nil {
		t.Fatal("delivered order: cancellation must be refused (it is a return)")
	}
}
