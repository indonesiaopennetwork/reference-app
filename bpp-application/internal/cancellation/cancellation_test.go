package cancellation

import (
	"encoding/json"
	"errors"
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

// delivered is the test contract delivered, with a 7-day return policy and a
// 10% restocking fee, plus the buyer's RETURN performance.
func delivered(t *testing.T) (map[string]any, map[string]any) {
	c := load(t)
	asMap(slice(c["performance"])[0])["status"] = map[string]any{"code": "Delivered"}
	pol := asMap(asMap(asMap(asMap(slice(c["commitments"])[0])["offer"])["offerAttributes"])["policies"])
	pol["returns"] = map[string]any{"allowed": true, "window": "P7D", "restockingFee": map[string]any{"type": "PERCENTAGE", "value": 10.0}}
	perf := map[string]any{"id": "return-1", "status": map[string]any{"code": "Return_Initiated"},
		"performanceAttributes": map[string]any{"type": "RETURN"}}
	return c, perf
}

func TestReturnWithinWindow(t *testing.T) {
	c, perf := delivered(t)
	now := time.Now()
	res, err := Apply(c, []Item{{CommitmentID: "commitment-cake", Quantity: 1}},
		Request{Return: true, DeliveredAt: now.Add(-48 * time.Hour), ReturnPerformance: perf}, now)
	if err != nil {
		t.Fatal(err)
	}
	// 1 unit = 49,950 paid; 10% restocking fee = 4,995; refund 44,955.
	if res.Fee != 4995 || res.Refund != 44955 {
		t.Errorf("fee=%v refund=%v, want 4995 / 44955", res.Fee, res.Refund)
	}
	if res.Full || str(asMap(res.Contract["status"])["code"]) == "COMPLETE" {
		t.Error("returning 1 of 2 must leave the contract open")
	}
	old := byID(t, res.Contract["commitments"], "commitment-cake")
	if commitmentCode(old) != "CLOSED" || str(asMap(asMap(old["status"])["descriptor"])["name"]) != "Returned" {
		t.Errorf("returned commitment status = %v, want CLOSED Returned", old["status"])
	}
	fee := asMap(byID(t, res.Contract["consideration"], "restocking-105ad328-c1")["considerationAttributes"])
	if str(asMap(slice(fee["breakup"])[0])["type"]) != "RESTOCKING_FEE" {
		t.Errorf("fee consideration = %v", fee)
	}
	rp := byID(t, res.Contract["performance"], "return-1")
	if str(asMap(rp["status"])["code"]) != "Return_Approved" || len(slice(rp["commitmentIds"])) != 1 {
		t.Errorf("return performance = %v, want Return_Approved listing the returned commitment", rp)
	}
	if res.RestoredUnits["item-cake"] != 1 {
		t.Errorf("restored = %v, want 1", res.RestoredUnits)
	}
}

func TestFullReturnCompletesContract(t *testing.T) {
	c, perf := delivered(t)
	now := time.Now()
	res, err := Apply(c, nil, Request{Return: true, DeliveredAt: now.Add(-time.Hour), ReturnPerformance: perf}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Full || str(asMap(res.Contract["status"])["code"]) != "COMPLETE" {
		t.Errorf("full return: full=%v status=%v, want COMPLETE", res.Full, res.Contract["status"])
	}
}

func TestReturnRejected(t *testing.T) {
	now := time.Now()
	req := func(p map[string]any, at time.Time) Request {
		return Request{Return: true, DeliveredAt: at, ReturnPerformance: p}
	}

	c, perf := delivered(t)
	if _, err := Apply(c, nil, req(perf, now.Add(-8*24*time.Hour)), now); !errors.Is(err, ErrReturnRejected) {
		t.Errorf("window closed: err = %v, want ErrReturnRejected", err)
	}

	c, perf = delivered(t)
	asMap(asMap(asMap(asMap(asMap(slice(c["commitments"])[0])["offer"])["offerAttributes"])["policies"])["returns"])["allowed"] = false
	if _, err := Apply(c, nil, req(perf, now), now); !errors.Is(err, ErrReturnRejected) {
		t.Errorf("returns not allowed: err = %v, want ErrReturnRejected", err)
	}

	c, perf = delivered(t)
	asMap(slice(c["performance"])[0])["status"] = map[string]any{"code": "Shipped"}
	if _, err := Apply(c, nil, req(perf, time.Time{}), now); !errors.Is(err, ErrReturnRejected) {
		t.Errorf("not delivered: err = %v, want ErrReturnRejected", err)
	}
}

func TestReturnRequestDetection(t *testing.T) {
	stored, perf := delivered(t)
	requested, _ := deepCopy(stored)
	requested["performance"] = append(slice(requested["performance"]), perf)
	setCommitmentQty(asMap(slice(requested["commitments"])[0]), 1)
	p, items := ReturnRequest(stored, requested)
	if p == nil || len(items) != 1 || items[0].Quantity != 1 {
		t.Errorf("perf=%v items=%v, want the RETURN performance and 1 unit", p, items)
	}
	if p, _ := ReturnRequest(stored, stored); p != nil {
		t.Error("an update without a new RETURN performance is not a return request")
	}
}

func TestParseISODuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"P7D": 7 * 24 * time.Hour, "PT2H": 2 * time.Hour, "P1W": 7 * 24 * time.Hour,
		"P1DT12H": 36 * time.Hour, "PT30M": 30 * time.Minute,
	} {
		if got, err := ParseISODuration(in); err != nil || got != want {
			t.Errorf("ParseISODuration(%s) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseISODuration("7 days"); err == nil {
		t.Error("expected an error for a non-ISO duration")
	}
}

func TestRefundSource(t *testing.T) {
	refundSource := func(res *Result) string {
		return str(asMap(asMap(res.RefundSettlement["settlementAttributes"])["refund"])["source"])
	}

	// Not reconciled yet: the gateway still holds the funds and refunds them.
	res, err := Apply(load(t), []Item{{CommitmentID: "commitment-cake", Quantity: 1}}, reason, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := refundSource(res); got != "PAYMENT_GATEWAY" {
		t.Errorf("unreconciled: source = %s, want PAYMENT_GATEWAY", got)
	}

	// Payment released by an agreed reconciliation: the provider refunds.
	c, perf := delivered(t)
	pay := asMap(asMap(slice(c["settlements"])[0])["settlementAttributes"])
	pay["reconciliationId"], pay["reconciliationStatus"] = "recon-2026-09-30", "AGREED"
	now := time.Now()
	res, err = Apply(c, []Item{{CommitmentID: "commitment-cake", Quantity: 1}},
		Request{Return: true, DeliveredAt: now.Add(-time.Hour), ReturnPerformance: perf}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := refundSource(res); got != "PROVIDER" {
		t.Errorf("reconciled: source = %s, want PROVIDER", got)
	}

	// Still PENDING in a reconciliation: not released yet.
	pay["reconciliationStatus"] = "PENDING"
	if got := RefundSource(asMap(slice(c["settlements"])[0])); got != "PAYMENT_GATEWAY" {
		t.Errorf("pending reconciliation: source = %s, want PAYMENT_GATEWAY", got)
	}
}
