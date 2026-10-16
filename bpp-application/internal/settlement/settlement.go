// Package settlement is an order's settlement object: ION Release 1
// IONSettlementReconciliation (common/Settlement/v1), carried as the
// settlementAttributes of the contract's payment settlement from init to
// reconciliation. It holds the settlement terms, both participants' payout
// accounts, the payment, and the figures:
//
//	init       BAP   finderFee terms, its payout account, the payment method,
//	                 status PENDING, estimated figures
//	on_init    BPP   settlementBasis / settlementWindow, finderFee.feeAmount,
//	                 its payout account, the figures on its final price
//	confirm    BAP   status PAID, paidAt, gatewayRef; orderAmount is the
//	                 amount paid
//	on_cancel, on_update
//	           BPP   the figures follow cancellations and returns
//	reconcile, on_reconcile
//	           both  each participant's own figures for the payout
//
// Until reconciliation the figures are estimates. Refunds are separate
// settlements (ION TradeSettlement with a refund), not part of this object.
//
// Shared with bap-application/internal/settlement: keep the two copies in step.
package settlement

import (
	"math"
	"strings"
)

const (
	Context = "https://schema.ion.id/release1/schema/common/Settlement/v1/context.jsonld"
	Type    = "ion:IONSettlementReconciliation"
)

// legacyContexts are earlier @context URLs of the settlement object, no
// longer served (ion-specs moved the pack from core/reconcile/v1 to
// common/Settlement/v1). A validator that fetches one fails the message.
var legacyContexts = map[string]bool{
	"https://schema.ion.id/release1/schema/core/reconcile/v1/context.jsonld": true,
}

// CurrentContext points every settlement object of the contract that still
// carries a legacy @context at Context, so an order stored before the move
// validates when it is sent again. It returns the contract.
func CurrentContext(contract map[string]any) map[string]any {
	for _, raw := range slice(contract["settlements"]) {
		if a := asMap(asMap(raw)["settlementAttributes"]); a != nil && legacyContexts[str(a["@context"])] {
			a["@context"] = Context
		}
	}
	return contract
}

// paymentFields are the payment details the object carries (the IONPayment
// definitions).
var paymentFields = []string{"method", "paymentRail", "paymentGatewayProvider", "methodDetail", "timing", "status", "paidAt", "gatewayRef"}

// methods are the payment methods the object's method may hold (IONPayment).
var methods = map[string]bool{
	"QRIS": true, "VIRTUAL_ACCOUNT": true, "EWALLET": true, "COD": true, "BANK_TRANSFER": true,
	"BI_FAST": true, "RTGS": true, "SKN": true, "BNPL": true, "CREDIT_CARD": true, "DEBIT_CARD": true,
	"DIRECT_DEBIT": true, "CREDIT_FACILITY": true, "ESCROW": true,
}

// termFields are the settlement terms the object carries.
var termFields = []string{"settlementBasis", "settlementWindow", "finderFee"}

// Of returns the contract's payment settlement -- the first settlement that
// is not a refund -- and its settlementAttributes; nil when there is none.
func Of(contract map[string]any) (st, attrs map[string]any) {
	for _, raw := range slice(contract["settlements"]) {
		s := asMap(raw)
		if s == nil {
			continue
		}
		a := asMap(s["settlementAttributes"])
		if asMap(a["refund"]) != nil {
			continue
		}
		return s, a
	}
	return nil, nil
}

// Normalize returns the contract's settlement object, first converting a
// payment declared the earlier way (IONPayment, with the terms in
// contractAttributes) in place. nil when the contract has no settlement.
func Normalize(contract map[string]any) map[string]any {
	st, a := Of(contract)
	if st == nil {
		return nil
	}
	if a == nil {
		a = map[string]any{}
		st["settlementAttributes"] = a
	}
	if str(a["@type"]) == Type {
		CurrentContext(contract)
		return a
	}
	a["@context"], a["@type"] = Context, Type
	ca := asMap(contract["contractAttributes"])
	for _, k := range termFields {
		if a[k] == nil && ca[k] != nil {
			a[k] = ca[k]
		}
	}
	if v, ok := a["amount"].(float64); ok {
		a["orderAmount"] = map[string]any{"value": v}
	}
	delete(a, "amount")
	if m, ok := a["method"].(string); ok && !methods[m] {
		delete(a, "method") // an earlier settlement's method (ONLINE, ...)
	}
	return a
}

// Terms returns the settlement terms: from the settlement object, or from
// contractAttributes (IONTradeContract) on a contract agreed before they
// moved there.
func Terms(contract map[string]any) (basis, window string, finderFee map[string]any) {
	_, a := Of(contract)
	ca := asMap(contract["contractAttributes"])
	pick := func(k string) any {
		if v := a[k]; v != nil {
			return v
		}
		return ca[k]
	}
	return str(pick("settlementBasis")), str(pick("settlementWindow")), asMap(pick("finderFee"))
}

// PaymentDetails copies the payment fields of a settlement object, for
// sending them on in /reconcile. A PAID status without paidAt (a payment
// recorded before paidAt was kept) and a method IONPayment doesn't know are
// left out.
func PaymentDetails(attrs map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range paymentFields {
		if v, ok := attrs[k]; ok && v != nil {
			out[k] = v
		}
	}
	if out["status"] == "PAID" && out["paidAt"] == nil {
		delete(out, "status")
	}
	if m, ok := out["method"].(string); ok && !methods[m] {
		delete(out, "method") // one an earlier settlement carried (ONLINE, ...)
	}
	return out
}

// PaidAmount is what the buyer paid: orderAmount once the payment is PAID,
// or the amount / settledAmount of a payment recorded the earlier way.
// ok is false before payment.
func PaidAmount(attrs map[string]any) (amount float64, ok bool) {
	if v, ok := attrs["amount"].(float64); ok {
		return v, true
	}
	if v, ok := attrs["settledAmount"].(float64); ok {
		return v, true
	}
	if str(attrs["@type"]) == Type && str(attrs["status"]) == "PAID" {
		if v, ok := asMap(attrs["orderAmount"])["value"].(float64); ok {
			return v, true
		}
	}
	return 0, false
}

// Amounts are a contract's settlement figures:
// Order = Net + Commission + Withholding + Refund.
type Amounts struct {
	Order, Commission, Withholding, Refund, Net float64
	// RefundIDs are the pre-payout refund settlements counted in Refund.
	RefundIDs []string
	// RefundedAfterPayout is refunded by the BPP itself after the payout; it
	// is outside reconciliation (ION Release 1) and only reported.
	RefundedAfterPayout float64
}

// Compute works out the contract's figures as it stands. The commission is
// the agreed finderFee.feeAmount, or -- with recomputeFee, or when no
// feeAmount was stated -- the fee its terms come to on the items still
// ordered. It never exceeds what is left to pay out.
func Compute(contract map[string]any, recomputeFee bool) Amounts {
	var a Amounts
	_, attrs := Of(contract)
	a.Order = OrderTotal(contract)
	if w, ok := asMap(attrs["withholdingAmount"])["value"].(float64); ok {
		a.Withholding = w
	}
	for _, raw := range slice(contract["settlements"]) {
		st := asMap(raw)
		refund := asMap(asMap(st["settlementAttributes"])["refund"])
		if refund == nil {
			continue
		}
		if str(refund["source"]) == "PROVIDER" {
			a.RefundedAfterPayout += num(refund["amount"])
			continue
		}
		a.Refund += num(refund["amount"])
		a.RefundIDs = append(a.RefundIDs, str(st["id"]))
	}
	_, _, fee := Terms(contract)
	if stated, ok := fee["feeAmount"].(float64); ok && !recomputeFee {
		a.Commission = stated
	} else {
		a.Commission = FeeAmount(fee, ItemTotal(contract), a.Order)
	}
	if max := a.Order - a.Refund - a.Withholding; a.Commission > max {
		a.Commission = math.Max(0, max)
	}
	a.Order, a.Refund, a.Withholding = round2(a.Order), round2(a.Refund), round2(a.Withholding)
	a.Commission, a.RefundedAfterPayout = round2(a.Commission), round2(a.RefundedAfterPayout)
	a.Net = round2(a.Order - a.Commission - a.Withholding - a.Refund)
	return a
}

// Refresh recomputes the figures on the contract's settlement object --
// finderFee.feeAmount, orderAmount, commission, withholdingAmount,
// refundAmount, netPayable -- from the contract as it stands, and returns
// them. A contract without a settlement is left unchanged.
func Refresh(contract map[string]any) Amounts {
	attrs := Normalize(contract)
	a := Compute(contract, true)
	if attrs == nil {
		return a
	}
	_, _, fee := Terms(contract)
	if fee != nil {
		fee["feeAmount"] = FeeAmount(fee, ItemTotal(contract), a.Order)
		attrs["finderFee"] = fee
	}
	SetFigures(attrs, a)
	return a
}

// SetFigures writes a's figures onto a settlement object.
func SetFigures(attrs map[string]any, a Amounts) {
	attrs["orderAmount"] = map[string]any{"value": a.Order}
	attrs["commission"] = map[string]any{"value": a.Commission}
	attrs["withholdingAmount"] = map[string]any{"value": a.Withholding}
	attrs["refundAmount"] = map[string]any{"value": a.Refund}
	attrs["netPayable"] = map[string]any{"value": a.Net}
}

// SetAccount puts account (holderRole BAP or BPP) in the object's
// settlementAccounts, replacing that role's earlier one.
func SetAccount(attrs, account map[string]any) {
	var accounts []any
	for _, raw := range slice(attrs["settlementAccounts"]) {
		if m := asMap(raw); m != nil && m["holderRole"] != account["holderRole"] {
			accounts = append(accounts, m)
		}
	}
	attrs["settlementAccounts"] = append(accounts, account)
}

// ItemTotal is the value of the items still ordered: the BASE_PRICE lines of
// the price considerations in force (not CANCELLED, and not a cancellation /
// restocking fee).
func ItemTotal(contract map[string]any) float64 {
	var total float64
	for _, raw := range slice(contract["consideration"]) {
		c := asMap(raw)
		if isFee(str(c["id"])) || str(asMap(c["status"])["code"]) == "CANCELLED" {
			continue
		}
		for _, line := range slice(asMap(c["considerationAttributes"])["breakup"]) {
			if l := asMap(line); str(l["type"]) == "BASE_PRICE" {
				total += num(l["amount"])
			}
		}
	}
	return total
}

// OrderTotal is what the buyer pays for the order: the amount paid once
// paid, otherwise the total of the price considerations in force.
func OrderTotal(contract map[string]any) float64 {
	if _, attrs := Of(contract); attrs != nil {
		if v, ok := PaidAmount(attrs); ok {
			return v
		}
	}
	var total float64
	for _, raw := range slice(contract["consideration"]) {
		c := asMap(raw)
		if isFee(str(c["id"])) || str(asMap(c["status"])["code"]) == "CANCELLED" {
			continue
		}
		total += num(asMap(c["considerationAttributes"])["totalAmount"])
	}
	return total
}

// FeeAmount applies a finderFee's terms: a percentage of the item total
// (ITEM_TOTAL, the default) or of the order total (ORDER_TOTAL), or a fixed
// AMOUNT. No finder fee agreed means no fee.
func FeeAmount(fee map[string]any, items, order float64) float64 {
	if fee == nil {
		return 0
	}
	value := num(fee["feeValue"])
	if str(fee["feeType"]) == "AMOUNT" {
		return round2(value)
	}
	base := items
	if str(fee["appliesTo"]) == "ORDER_TOTAL" {
		base = order
	}
	return round2(base * value / 100)
}

func isFee(id string) bool {
	return strings.HasPrefix(id, "cancellation-") || strings.HasPrefix(id, "restocking-")
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func slice(v any) []any          { s, _ := v.([]any); return s }
func str(v any) string           { s, _ := v.(string); return s }
func num(v any) float64          { f, _ := v.(float64); return f }
func round2(f float64) float64   { return math.Round(f*100) / 100 }
