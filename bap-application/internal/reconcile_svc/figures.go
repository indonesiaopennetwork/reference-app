package reconcilesvc

import (
	"fmt"
	"strings"

	"github.com/ion/winroom/bap/internal/settlement"
)

// ION Release 1 common/Settlement/v1: the settlement object (package
// settlement), which /reconcile and /on_reconcile carry with each
// participant's figures.
const (
	reconcileContext = settlement.Context
	reconcileType    = settlement.Type
)

// Terms are the seller's default payout terms, used for contracts agreed
// before the terms were carried with the settlement.
type Terms struct {
	Basis, Window string
	// Account is this participant's own payout account (holderRole BAP or
	// BPP). It is added to a contract whose settlement doesn't carry one --
	// an order placed before payout accounts were declared -- so the
	// reconcile figures always name it.
	Account map[string]any
}

// Figures are one contract's reconciliation figures. The buyer's payment is
// held at the PJP; on agreement the PJP pays NetPayable to the BPP and
// Commission to the BAP, and returns Refund to the buyer:
// OrderAmount = NetPayable + Commission + Withholding + Refund.
type Figures struct {
	SettlementID    string         `json:"settlement_id"`
	ConsiderationID string         `json:"consideration_id"`
	CollectedBy     string         `json:"collected_by,omitempty"`
	Currency        string         `json:"currency"`
	Basis           string         `json:"settlement_basis,omitempty"`
	Window          string         `json:"settlement_window,omitempty"`
	FinderFee       map[string]any `json:"finder_fee,omitempty"`

	OrderAmount float64 `json:"order_amount"`
	Commission  float64 `json:"commission"`
	Withholding float64 `json:"withholding"`
	Refund      float64 `json:"refund"`
	NetPayable  float64 `json:"net_payable"`

	Accounts []any `json:"-"`
	// Payment is the settlement object's payment details (status, paidAt,
	// gatewayRef, ...), sent on with the figures.
	Payment map[string]any `json:"-"`

	// RefundSettlementIDs are the pre-payout refunds counted in Refund.
	RefundSettlementIDs []string `json:"refund_settlement_ids"`
	// RefundedAfterPayout is refunded by the BPP itself after the payout; it
	// is outside reconciliation (ION Release 1) and only reported.
	RefundedAfterPayout float64 `json:"refunded_after_payout"`
}

// withAccount returns accounts with account added when none of them has its
// holderRole; unchanged when account is nil or has no number.
func withAccount(accounts []any, account map[string]any) []any {
	if account == nil || str(account["accountNumber"]) == "" {
		return accounts
	}
	for _, raw := range accounts {
		if asMap(raw)["holderRole"] == account["holderRole"] {
			return accounts
		}
	}
	out := append([]any{}, accounts...)
	return append(out, account)
}

// contractFigures computes a contract's figures from its full state. Both the
// BPP and the BAP apply this same rule to their own copy of the contract, so
// matching copies give matching figures.
func contractFigures(contract map[string]any, defaults Terms) (*Figures, error) {
	payment, pa := settlement.Of(contract)
	if payment == nil {
		return nil, fmt.Errorf("contract %s has no payment settlement", str(contract["id"]))
	}
	a := settlement.Compute(contract, false)
	f := &Figures{
		SettlementID:        str(payment["id"]),
		ConsiderationID:     str(payment["considerationId"]),
		CollectedBy:         str(pa["collectedBy"]),
		Currency:            str(pa["currency"]),
		Accounts:            withAccount(slice(pa["settlementAccounts"]), defaults.Account),
		Payment:             settlement.PaymentDetails(pa),
		OrderAmount:         a.Order,
		Commission:          a.Commission,
		Withholding:         a.Withholding,
		Refund:              a.Refund,
		NetPayable:          a.Net,
		RefundSettlementIDs: a.RefundIDs,
		RefundedAfterPayout: a.RefundedAfterPayout,
	}
	if f.Currency == "" {
		f.Currency = "IDR"
	}
	if f.CollectedBy == "" {
		f.CollectedBy = "BAP"
	}
	f.Basis, f.Window, f.FinderFee = settlement.Terms(contract)
	if f.Basis == "" {
		f.Basis = defaults.Basis
	}
	if f.Window == "" {
		f.Window = defaults.Window
	}
	f.Basis = normalizeBasis(f.Basis)
	return f, nil
}

// paymentOf is the contract's payment settlement: the first one that is not
// a refund.
func paymentOf(contract map[string]any) map[string]any {
	st, _ := settlement.Of(contract)
	return st
}

// bag renders f as the IONSettlementReconciliation settlementAttributes.
func (f *Figures) bag() map[string]any {
	b := map[string]any{}
	for k, v := range f.Payment {
		b[k] = v
	}
	for k, v := range map[string]any{
		"@context":          reconcileContext,
		"@type":             reconcileType,
		"collectedBy":       f.CollectedBy,
		"currency":          f.Currency,
		"orderAmount":       map[string]any{"value": f.OrderAmount},
		"commission":        map[string]any{"value": f.Commission},
		"withholdingAmount": map[string]any{"value": f.Withholding},
		"refundAmount":      map[string]any{"value": f.Refund},
		"netPayable":        map[string]any{"value": f.NetPayable},
		"settlementAccounts": func() []any {
			if f.Accounts == nil {
				return []any{}
			}
			return f.Accounts
		}(),
	} {
		b[k] = v
	}
	if f.Basis != "" {
		b["settlementBasis"] = f.Basis
	}
	if f.Window != "" {
		b["settlementWindow"] = f.Window
	}
	if f.FinderFee != nil {
		fee := map[string]any{}
		for k, v := range f.FinderFee {
			fee[k] = v
		}
		fee["feeAmount"] = f.Commission
		b["finderFee"] = fee
	}
	return b
}

// snapshot is the contract as /reconcile and /on_reconcile carry it: its id,
// the minimal commitments Beckn requires, and its payment settlement with
// the figures. It is not a contract update.
func snapshot(contract map[string]any, f *Figures, bag map[string]any) map[string]any {
	st := map[string]any{"id": f.SettlementID, "settlementAttributes": bag}
	if f.ConsiderationID != "" {
		st["considerationId"] = f.ConsiderationID
	}
	return map[string]any{
		"id":          str(contract["id"]),
		"commitments": minimalCommitments(contract),
		"settlements": []any{st},
	}
}

// minimalCommitments keeps what Beckn requires of each commitment: id,
// status, resources (id, quantity) and offer (id, resourceIds).
func minimalCommitments(contract map[string]any) []any {
	out := []any{}
	for _, raw := range slice(contract["commitments"]) {
		c := asMap(raw)
		var resources, ids []any
		for _, r := range slice(c["resources"]) {
			rm := asMap(r)
			qty := asMap(rm["quantity"])
			if qty == nil {
				qty = asMap(asMap(rm["resourceAttributes"])["quantity"])
			}
			if qty == nil {
				qty = map[string]any{"count": 1}
			}
			resources = append(resources, map[string]any{"id": str(rm["id"]), "quantity": qty})
			ids = append(ids, str(rm["id"]))
		}
		status := asMap(c["status"])
		if status == nil {
			status = map[string]any{"descriptor": map[string]any{"code": "ACTIVE"}}
		}
		out = append(out, map[string]any{
			"id":        str(c["id"]),
			"status":    status,
			"resources": resources,
			"offer":     map[string]any{"id": str(asMap(c["offer"])["id"]), "resourceIds": ids},
		})
	}
	return out
}

// reconcileComponents are the amounts compared between the two copies.
var reconcileComponents = []struct{ key, code string }{
	{"orderAmount", "ORDER_AMOUNT_MISMATCH"},
	{"commission", "COMMISSION_MISMATCH"},
	{"withholdingAmount", "WITHHOLDING_MISMATCH"},
	{"refundAmount", "REFUND_MISMATCH"},
	{"netPayable", "NET_PAYABLE_MISMATCH"},
}

// compare answers a received bag with this participant's own figures: the
// reply bag (own figures, with diffValue on each component that differs),
// accord, and the dispute when they disagree.
func compare(received map[string]any, own *Figures) (reply map[string]any, accord bool, dispute map[string]any) {
	reply = own.bag()
	accord = true
	var reasons []string
	for _, c := range reconcileComponents {
		theirs := num(asMap(received[c.key])["value"])
		mine := num(asMap(reply[c.key])["value"])
		if diff := round2(mine - theirs); diff != 0 {
			asMap(reply[c.key])["diffValue"] = diff
			if accord {
				dispute = map[string]any{"code": c.code}
			}
			accord = false
			reasons = append(reasons, fmt.Sprintf("%s %v here, %v received", c.key, mine, theirs))
		}
	}
	if !accord {
		dispute["description"] = strings.Join(reasons, "; ")
	}
	return reply, accord, dispute
}

// disagree is the reply for a contract or settlement this participant cannot
// match: the received figures, accord false, and why.
func disagree(received map[string]any, code, description string) map[string]any {
	reply := map[string]any{}
	for k, v := range received {
		reply[k] = v
	}
	reply["accord"] = false
	reply["dispute"] = map[string]any{"code": code, "description": description}
	delete(reply, "dueDate")
	return reply
}

// normalizeBasis returns ION's name for a settlement basis stored under a
// pre-ION name.
func normalizeBasis(basis string) string {
	switch basis {
	case "DELIVERY":
		return "AFTER_DELIVERY"
	case "SHIPMENT":
		return "AFTER_SHIPMENT"
	case "RETURN_WINDOW_EXPIRY":
		return "AFTER_RETURN_WINDOW"
	}
	return basis
}

// CheckFinderFee compares the finderFee.feeAmount a BPP states (in on_init,
// on_cancel or on_update) with what this BAP computes from the same contract's
// terms and prices. ok is false when the contract states no fee amount.
func CheckFinderFee(contract map[string]any) (stated, computed float64, ok bool) {
	_, _, fee := settlement.Terms(contract)
	stated, ok = fee["feeAmount"].(float64)
	if !ok {
		return 0, 0, false
	}
	return stated, settlement.FeeAmount(fee, settlement.ItemTotal(contract), settlement.OrderTotal(contract)), true
}
