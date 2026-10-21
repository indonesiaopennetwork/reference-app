package settlement

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func contract(t *testing.T, s string) map[string]any {
	t.Helper()
	var c map[string]any
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func value(attrs map[string]any, key string) any { return asMap(attrs[key])["value"] }

// An order as the BAP declares it in init: terms, its account, PENDING.
const declared = `{
  "consideration": [{"id": "price-1", "considerationAttributes": {"totalAmount": 98900,
    "breakup": [{"type": "BASE_PRICE", "amount": 89000}, {"type": "TAX", "amount": 9900}]}}],
  "settlements": [{"id": "settlement-1", "settlementAttributes": {
    "@context": "https://schema.ion.id/release1/schema/common/Settlement/v1/context.jsonld",
    "@type": "ion:IONSettlementReconciliation",
    "status": "PENDING", "currency": "IDR",
    "finderFee": {"feeType": "PERCENT", "feeValue": 3, "appliesTo": "ITEM_TOTAL"},
    "settlementAccounts": [{"holderRole": "BAP", "bankCode": "MANDIRI"}]}}]}`

func TestRefreshEstimatesTheFiguresBeforePayment(t *testing.T) {
	c := contract(t, declared)
	a := Refresh(c)
	if a.Order != 98900 || a.Commission != 2670 || a.Net != 96230 {
		t.Fatalf("figures = %+v", a)
	}
	_, attrs := Of(c)
	if value(attrs, "netPayable") != 96230.0 || asMap(attrs["finderFee"])["feeAmount"] != 2670.0 {
		t.Errorf("settlement object = %v", attrs)
	}
}

func TestRefreshFollowsAPaymentAndARefund(t *testing.T) {
	c := contract(t, declared)
	_, attrs := Of(c)
	attrs["status"], attrs["paidAt"] = "PAID", "2026-10-13T10:00:00Z"
	attrs["orderAmount"] = map[string]any{"value": 98900.0}
	// Half the items cancelled: the price consideration is replaced and the
	// buyer gets a refund before payout.
	c["consideration"] = []any{
		map[string]any{"id": "price-1", "status": map[string]any{"code": "CANCELLED"}},
		map[string]any{"id": "price-1a", "considerationAttributes": map[string]any{"totalAmount": 49450.0,
			"breakup": []any{map[string]any{"type": "BASE_PRICE", "amount": 44500.0}}}},
	}
	c["settlements"] = append(slice(c["settlements"]), map[string]any{"id": "refund-1",
		"settlementAttributes": map[string]any{"refund": map[string]any{"amount": 49450.0}}})
	a := Refresh(c)
	if a.Order != 98900 || a.Refund != 49450 || a.Commission != 1335 || a.Net != 48115 {
		t.Errorf("figures = %+v", a)
	}
	if len(a.RefundIDs) != 1 || a.RefundIDs[0] != "refund-1" {
		t.Errorf("refund ids = %v", a.RefundIDs)
	}
}

func TestComputeKeepsTheStatedFee(t *testing.T) {
	c := contract(t, declared)
	_, attrs := Of(c)
	asMap(attrs["finderFee"])["feeAmount"] = 2000.0
	if a := Compute(c, false); a.Commission != 2000 {
		t.Errorf("commission = %v, want the stated 2000", a.Commission)
	}
	if a := Compute(c, true); a.Commission != 2670 {
		t.Errorf("recomputed commission = %v", a.Commission)
	}
}

func TestNormalizeConvertsAnEarlierPayment(t *testing.T) {
	c := contract(t, `{
	  "contractAttributes": {"settlementBasis": "AFTER_DELIVERY", "settlementWindow": "P1D",
	    "finderFee": {"feeType": "AMOUNT", "feeValue": 5000}},
	  "settlements": [{"id": "s", "settlementAttributes": {"@type": "ion:IONPayment",
	    "status": "PAID", "amount": 98900, "paidAt": "2026-10-13T10:00:00Z"}}]}`)
	attrs := Normalize(c)
	if attrs["@type"] != Type || value(attrs, "orderAmount") != 98900.0 || attrs["amount"] != nil {
		t.Errorf("normalized = %v", attrs)
	}
	if basis, window, fee := LegacyTerms(c); basis != "AFTER_DELIVERY" || window != "P1D" || fee["feeValue"] != 5000.0 {
		t.Errorf("terms = %v %v %v", basis, window, fee)
	}
	if v, ok := PaidAmount(attrs); !ok || v != 98900 {
		t.Errorf("paid = %v %v", v, ok)
	}
}

func TestTermsFallBackToContractAttributes(t *testing.T) {
	c := contract(t, `{"contractAttributes": {"settlementBasis": "AFTER_SHIPMENT"},
	  "settlements": [{"settlementAttributes": {"amount": 1}}]}`)
	if basis, _, _ := LegacyTerms(c); basis != "AFTER_SHIPMENT" {
		t.Errorf("basis = %q", basis)
	}
}

func TestPaymentDetailsDropsPaidWithoutPaidAt(t *testing.T) {
	d := PaymentDetails(map[string]any{"status": "PAID", "paymentRail": "QRIS", "orderAmount": 1})
	if d["status"] != nil || d["paymentRail"] != "QRIS" || d["orderAmount"] != nil {
		t.Errorf("details = %v", d)
	}
}

func TestFeeAmount(t *testing.T) {
	if got := FeeAmount(map[string]any{"feeType": "PERCENT", "feeValue": 2.0, "appliesTo": "ORDER_TOTAL"}, 89000, 98900); got != 1978 {
		t.Errorf("2%% of 98,900 = %v", got)
	}
	if got := FeeAmount(map[string]any{"feeType": "AMOUNT", "feeValue": 5000.0}, 0, 0); got != 5000 {
		t.Errorf("fixed fee = %v", got)
	}
	if got := FeeAmount(nil, 89000, 98900); got != 0 {
		t.Errorf("no fee = %v", got)
	}
}

func TestCancelledPricesAndFeesDoNotCount(t *testing.T) {
	c := contract(t, `{"consideration": [
	  {"id": "price-1", "status": {"code": "CANCELLED"}, "considerationAttributes": {"totalAmount": 99900, "breakup": [{"type": "BASE_PRICE", "amount": 90000}]}},
	  {"id": "price-1a", "status": {"code": "ACTIVE"}, "considerationAttributes": {"totalAmount": 49950, "breakup": [{"type": "BASE_PRICE", "amount": 45000}]}},
	  {"id": "cancellation-x", "status": {"code": "ACTIVE"}, "considerationAttributes": {"totalAmount": 500, "breakup": [{"type": "BASE_PRICE", "amount": 500}]}}]}`)
	if ItemTotal(c) != 45000 || OrderTotal(c) != 49950 {
		t.Errorf("items=%v order=%v", ItemTotal(c), OrderTotal(c))
	}
}

func TestAnUnknownMethodIsDropped(t *testing.T) {
	contract := map[string]any{"settlements": []any{map[string]any{"id": "settlement-1",
		"settlementAttributes": map[string]any{"@type": "ion:IONTradeSettlement", "method": "ONLINE", "settledAmount": 1000.0}}}}
	if a := Normalize(contract); a["method"] != nil {
		t.Errorf("Normalize kept method %v", a["method"])
	}
	if p := PaymentDetails(map[string]any{"method": "ONLINE", "paymentRail": "QRIS"}); p["method"] != nil || p["paymentRail"] != "QRIS" {
		t.Errorf("PaymentDetails = %v, want no method", p)
	}
	if p := PaymentDetails(map[string]any{"method": "QRIS"}); p["method"] != "QRIS" {
		t.Errorf("PaymentDetails dropped QRIS: %v", p)
	}
}

func TestALegacyContextIsReplaced(t *testing.T) {
	legacy := "https://schema.ion.id/release1/schema/core/reconcile/v1/context.jsonld"
	contract := map[string]any{"settlements": []any{
		map[string]any{"id": "settlement-1", "settlementAttributes": map[string]any{"@context": legacy, "@type": Type}},
		map[string]any{"id": "refund-1", "settlementAttributes": map[string]any{
			"@context": "https://schema.ion.id/release1/schema/extension/trade/TradeSettlement/v1/context.jsonld",
			"@type":    "ion:IONTradeSettlement", "refund": map[string]any{"amount": 10.0}}},
	}}
	CurrentContext(contract)
	if got := asMap(asMap(slice(contract["settlements"])[0])["settlementAttributes"])["@context"]; got != Context {
		t.Errorf("payment settlement @context = %v, want %v", got, Context)
	}
	if got := asMap(asMap(slice(contract["settlements"])[1])["settlementAttributes"])["@context"]; got == Context {
		t.Error("a refund settlement's own @context must be kept")
	}
	// Normalize does it too for an object that is already the current type.
	contract = map[string]any{"settlements": []any{map[string]any{"id": "s", "settlementAttributes": map[string]any{"@context": legacy, "@type": Type}}}}
	if a := Normalize(contract); a["@context"] != Context {
		t.Errorf("Normalize kept %v", a["@context"])
	}
}

func TestOrderDateIsTheJakartaDate(t *testing.T) {
	// 18:30 UTC is 01:30 the next day in Jakarta.
	if got := OrderDate(time.Date(2026, 10, 18, 18, 30, 0, 0, time.UTC)); got != "2026-10-19" {
		t.Errorf("orderDate = %s, want 2026-10-19", got)
	}
}

func TestPendingCodes(t *testing.T) {
	if !IsPendingCode("SETTLEMENT_WINDOW_NOT_REACHED") || !IsPendingCode("AWAITING_INFORMATION") || IsPendingCode("COMMISSION_MISMATCH") {
		t.Error("only SETTLEMENT_WINDOW_NOT_REACHED and AWAITING_INFORMATION are pending")
	}
}

func TestCollections(t *testing.T) {
	prepaid := Collections(map[string]any{"method": "QRIS", "status": "PAID", "gatewayRef": "GW-1"}, 98900)
	want := map[string]any{"method": "PREPAID", "status": "COLLECTED", "collectorRole": "PJP", "reportedCollectedAmount": 98900.0, "referenceId": "GW-1"}
	if len(prepaid) != 1 || !reflect.DeepEqual(prepaid[0], want) {
		t.Errorf("prepaid = %v, want %v", prepaid, want)
	}
	cod := Collections(map[string]any{"method": "COD", "status": "PENDING", "collectedBy": "LSP"}, 50000)
	want = map[string]any{"method": "COD", "status": "NOT_COLLECTED", "collectorRole": "LOGISTICS"}
	if len(cod) != 1 || !reflect.DeepEqual(cod[0], want) {
		t.Errorf("cod = %v, want %v", cod, want)
	}
	if Collections(map[string]any{}, 1) != nil {
		t.Error("no payment status means no collections")
	}
}

func TestRefundBreakupAndAllocate(t *testing.T) {
	c := map[string]any{"performance": []any{map[string]any{"status": map[string]any{"code": "DELIVERED"}}}, "settlements": []any{
		map[string]any{"id": "pay", "settlementAttributes": map[string]any{}},
		map[string]any{"id": "refund-1", "settlementAttributes": map[string]any{"refund": map[string]any{"amount": 45000.0}}},
		map[string]any{"id": "refund-2", "settlementAttributes": map[string]any{"refund": map[string]any{"amount": 10.0, "source": "PROVIDER"}}},
	}}
	got := RefundBreakup(c)
	want := []any{map[string]any{"code": "RETURN", "includedIn": "REFUND", "amount": 45000.0, "referenceId": "refund-1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("breakup = %v, want %v", got, want)
	}
	accounts := []any{map[string]any{"holderRole": "BAP"}, map[string]any{"holderRole": "BPP"}}
	out := Allocate(accounts, "BPP", "bpp.example", 96230)
	if b := out[1].(map[string]any); b["allocationFor"] != "BPP" || b["allocatedAmount"] != 96230.0 || b["holderId"] != "bpp.example" {
		t.Errorf("BPP account = %v", b)
	}
	if a := out[0].(map[string]any); a["allocationFor"] != nil || accounts[1].(map[string]any)["allocationFor"] != nil {
		t.Error("only the BPP's own account is marked, on a copy")
	}
}
