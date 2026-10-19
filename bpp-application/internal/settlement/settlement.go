// Package settlement is an order's settlement terms and figures.
//
// The terms are the seller's, published on each offer and carried on the
// order's commitments:
//
//   - the commission (finder fee) the seller pays the buyer app:
//     offer.considerations[].considerationAttributes.commission (ION
//     IONTradeConsideration) -- PERCENT of the offer's item value
//     (ITEM_TOTAL, excluding tax) or of its total (ORDER_TOTAL), or a fixed
//     AMOUNT per unit sold;
//   - the payout terms: offer.offerAttributes.policies.settlement (ION
//     IONTradeOffer) -- settlementBasis and settlementWindow. With several
//     offers, the term giving the latest due date applies.
//
// The contract's price consideration carries the commission the order comes
// to (commission.amount), set by the BPP in on_select / on_init and
// recomputed after cancellations and returns (Refresh).
//
// The settlement object is ION Release 1 IONSettlementReconciliation
// (common/Settlement/v1), the settlementAttributes of the contract's payment
// settlement from init to reconciliation: both participants' payout
// accounts, the payment, and the figures:
//
//	init       BAP   its payout account, the payment method, status PENDING,
//	                 estimated figures
//	on_init    BPP   its payout account, the figures on its final price
//	confirm    BAP   status PAID, paidAt, gatewayRef; orderAmount is the
//	                 amount paid
//	on_cancel, on_update
//	           BPP   the figures follow cancellations and returns
//	reconcile, on_reconcile
//	           both  each participant's own figures for the payout
//
// Orders placed before the terms moved to the offer carry them on the
// settlement object (finderFee, settlementBasis, settlementWindow) or in
// contractAttributes; those are still read (LegacyTerms).
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

// termFields are the settlement terms an order placed before they moved to
// the offer carries on the settlement object.
var termFields = []string{"settlementBasis", "settlementWindow", "finderFee"}

// Term is a payout term: the payout falls due Window (an ISO 8601 duration)
// after the Basis event (ON_CONFIRMATION, AFTER_SHIPMENT, AFTER_DELIVERY,
// AFTER_RETURN_WINDOW).
type Term struct{ Basis, Window string }

// OfferTerm is an offer's payout term, its policies.settlement; ok is false
// when the offer has none.
func OfferTerm(offer map[string]any) (t Term, ok bool) {
	p := asMap(asMap(asMap(offer["offerAttributes"])["policies"])["settlement"])
	t = Term{Basis: str(p["settlementBasis"]), Window: str(p["settlementWindow"])}
	return t, t.Basis != "" || t.Window != ""
}

// OfferCommission is an offer's commission terms (type, value, appliesTo),
// from the first of its considerations that states one; nil when none does.
func OfferCommission(offer map[string]any) map[string]any {
	for _, c := range slice(offer["considerations"]) {
		if m := asMap(asMap(asMap(c)["considerationAttributes"])["commission"]); m != nil {
			return m
		}
	}
	return nil
}

// PayoutTerms are the contract's payout terms: those of the offers still
// ordered (of every offer once all are cancelled), or -- for an order placed
// before the terms moved to the offer -- the one on its settlement object.
// Several terms: the payout falls due at the latest of their due dates.
func PayoutTerms(contract map[string]any) []Term {
	commitments := activeCommitments(contract)
	if len(commitments) == 0 {
		commitments = allCommitments(contract)
	}
	var terms []Term
	seen := map[Term]bool{}
	for _, cm := range commitments {
		if t, ok := OfferTerm(asMap(cm["offer"])); ok && !seen[t] {
			seen[t] = true
			terms = append(terms, t)
		}
	}
	if len(terms) == 0 {
		if basis, window, _ := LegacyTerms(contract); basis != "" || window != "" {
			terms = append(terms, Term{Basis: basis, Window: window})
		}
	}
	return terms
}

// OfferCommissionOf works out the commission the contract's offers come to,
// as the contract consideration's commission object: the offers' own terms
// when every offer still ordered has the same PERCENT terms, otherwise type
// AMOUNT with value the order's total; amount is the total either way. Each
// offer's fee is on its own items: PERCENT of the unit price (ITEM_TOTAL) or
// of the unit total with tax (ORDER_TOTAL), or the AMOUNT, times the
// quantity still ordered. ok is false when no offer states a commission (an
// order placed before the commission moved to the offer).
func OfferCommissionOf(contract map[string]any) (commission map[string]any, ok bool) {
	for _, cm := range allCommitments(contract) {
		if OfferCommission(asMap(cm["offer"])) != nil {
			ok = true
		}
	}
	if !ok {
		return nil, false
	}
	var total float64
	var shared map[string]any
	same := true
	for _, cm := range activeCommitments(contract) {
		offer := asMap(cm["offer"])
		terms := OfferCommission(offer)
		if terms == nil {
			same = false
			continue
		}
		qty := commitmentQty(cm)
		value := num(terms["value"])
		if str(terms["type"]) == "AMOUNT" {
			total += value * qty
		} else if str(terms["appliesTo"]) == "ORDER_TOTAL" {
			total += unitTotal(offer) * qty * value / 100
		} else {
			total += unitPrice(cm) * qty * value / 100
		}
		if str(terms["type"]) == "AMOUNT" {
			same = false
		} else if shared == nil {
			shared = terms
		} else if num(shared["value"]) != value || appliesTo(shared) != appliesTo(terms) {
			same = false
		}
	}
	total = round2(total)
	if same && shared != nil {
		return map[string]any{"type": "PERCENT", "value": num(shared["value"]), "appliesTo": appliesTo(shared), "amount": total}, true
	}
	return map[string]any{"type": "AMOUNT", "value": total, "amount": total}, true
}

// StatedCommission is the commission.amount on the contract's price
// consideration in force; ok is false when none states one.
func StatedCommission(contract map[string]any) (amount float64, ok bool) {
	for _, c := range priceConsiderations(contract) {
		if v, ok := asMap(asMap(c["considerationAttributes"])["commission"])["amount"].(float64); ok {
			return v, true
		}
	}
	return 0, false
}

func appliesTo(terms map[string]any) string {
	if str(terms["appliesTo"]) == "ORDER_TOTAL" {
		return "ORDER_TOTAL"
	}
	return "ITEM_TOTAL"
}

// unitPrice is a commitment's pre-tax unit price: commitmentAttributes.price,
// or its offer's BASE_PRICE.
func unitPrice(cm map[string]any) float64 {
	if v, ok := asMap(asMap(cm["commitmentAttributes"])["price"])["value"].(float64); ok {
		return v
	}
	for _, c := range slice(asMap(cm["offer"])["considerations"]) {
		for _, line := range slice(asMap(asMap(c)["considerationAttributes"])["breakup"]) {
			if l := asMap(line); str(l["type"]) == "BASE_PRICE" {
				return num(l["amount"])
			}
		}
	}
	return 0
}

// unitTotal is an offer's unit price with tax: its first consideration's
// totalAmount.
func unitTotal(offer map[string]any) float64 {
	for _, c := range slice(offer["considerations"]) {
		if v, ok := asMap(asMap(c)["considerationAttributes"])["totalAmount"].(float64); ok {
			return v
		}
	}
	return 0
}

// commitmentQty is a commitment's quantity: resources[0].quantity, then
// commitmentAttributes.quantity, then 1.
func commitmentQty(cm map[string]any) float64 {
	if rs := slice(cm["resources"]); len(rs) > 0 {
		if q := num(asMap(asMap(rs[0])["quantity"])["unitQuantity"]); q > 0 {
			return q
		}
	}
	if q := num(asMap(asMap(cm["commitmentAttributes"])["quantity"])["unitQuantity"]); q > 0 {
		return q
	}
	return 1
}

func allCommitments(contract map[string]any) []map[string]any {
	var out []map[string]any
	for _, raw := range slice(contract["commitments"]) {
		if m := asMap(raw); m != nil {
			out = append(out, m)
		}
	}
	return out
}

// activeCommitments are the commitments still ordered (not CLOSED by a
// cancellation or return).
func activeCommitments(contract map[string]any) []map[string]any {
	var out []map[string]any
	for _, cm := range allCommitments(contract) {
		s := asMap(cm["status"])
		code := str(asMap(s["descriptor"])["code"])
		if code == "" {
			code = str(s["code"])
		}
		if code != "CLOSED" {
			out = append(out, cm)
		}
	}
	return out
}

// priceConsiderations are the contract's price considerations in force: not
// CANCELLED, and not a cancellation / restocking fee.
func priceConsiderations(contract map[string]any) []map[string]any {
	var out []map[string]any
	for _, raw := range slice(contract["consideration"]) {
		c := asMap(raw)
		if c == nil || isFee(str(c["id"])) || str(asMap(c["status"])["code"]) == "CANCELLED" {
			continue
		}
		out = append(out, c)
	}
	return out
}

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

// LegacyTerms returns the settlement terms of an order placed before they
// moved to the offer: from the settlement object, or from contractAttributes
// (IONTradeContract) on a contract agreed before they moved there.
func LegacyTerms(contract map[string]any) (basis, window string, finderFee map[string]any) {
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
// the one stated on the price consideration (commission.amount), or -- with
// recomputeFee, or when none is stated -- what the offers' commission terms
// come to on the items still ordered. An order placed before the commission
// moved to the offer uses its finderFee the same way. It never exceeds what
// is left to pay out.
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
	if commission, ok := OfferCommissionOf(contract); ok {
		if stated, ok := StatedCommission(contract); ok && !recomputeFee {
			a.Commission = stated
		} else {
			a.Commission = num(commission["amount"])
		}
	} else {
		_, _, fee := LegacyTerms(contract)
		if stated, ok := fee["feeAmount"].(float64); ok && !recomputeFee {
			a.Commission = stated
		} else {
			a.Commission = FeeAmount(fee, ItemTotal(contract), a.Order)
		}
	}
	if max := a.Order - a.Refund - a.Withholding; a.Commission > max {
		a.Commission = math.Max(0, max)
	}
	a.Order, a.Refund, a.Withholding = round2(a.Order), round2(a.Refund), round2(a.Withholding)
	a.Commission, a.RefundedAfterPayout = round2(a.Commission), round2(a.RefundedAfterPayout)
	a.Net = round2(a.Order - a.Commission - a.Withholding - a.Refund)
	return a
}

// Refresh recomputes the contract's commission and figures as it stands and
// returns the figures: the commission on its price considerations in force,
// and orderAmount, commission, withholdingAmount, refundAmount, netPayable on
// its settlement object. An order placed before the commission moved to the
// offer keeps its finderFee, with feeAmount recomputed. A contract without a
// settlement only gets the commission.
func Refresh(contract map[string]any) Amounts {
	attrs := Normalize(contract)
	a := Compute(contract, true)
	if commission, ok := OfferCommissionOf(contract); ok {
		commission["amount"] = a.Commission
		if str(commission["type"]) == "AMOUNT" {
			commission["value"] = a.Commission
		}
		for _, c := range priceConsiderations(contract) {
			ca := asMap(c["considerationAttributes"])
			if ca == nil {
				ca = map[string]any{}
				c["considerationAttributes"] = ca
			}
			ca["commission"] = copyMap(commission)
		}
		for _, k := range termFields {
			delete(attrs, k) // the terms are the offers' now
		}
	} else if _, _, fee := LegacyTerms(contract); fee != nil && attrs != nil {
		fee["feeAmount"] = FeeAmount(fee, ItemTotal(contract), a.Order)
		attrs["finderFee"] = fee
	}
	if attrs == nil {
		return a
	}
	SetFigures(attrs, a)
	return a
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
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
	for _, c := range priceConsiderations(contract) {
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
	for _, c := range priceConsiderations(contract) {
		total += num(asMap(c["considerationAttributes"])["totalAmount"])
	}
	return total
}

// FeeAmount applies an earlier order's finderFee terms: a percentage of the item total
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
