package reconsvc

import "github.com/ion/winroom/bpp/internal/settlement"

// Summary is the settlementSummary of a /reconcile or /on_reconcile: totals
// over the settlements in the message, from the sender's own figures. The
// outcome counts and the agreed / unreconciled totals are an /on_reconcile
// answer's only.
type Summary struct {
	BapID            string  `json:"bapId"`
	BppID            string  `json:"bppId"`
	Currency         string  `json:"currency" example:"IDR"`
	SettlementDate   string  `json:"settlementDate" format:"date"`
	ContractCount    int     `json:"contractCount"`
	SettlementCount  int     `json:"settlementCount"`
	TotalOrderAmount float64 `json:"totalOrderAmount"`
	TotalCommission  float64 `json:"totalCommission"`
	TotalWithholding float64 `json:"totalWithholding"`
	TotalRefund      float64 `json:"totalRefund"`
	TotalNetPayable  float64 `json:"totalNetPayable"`

	// Contracts by outcome: disputed when any of its settlements is,
	// otherwise pending when any is, otherwise agreed.
	AgreedCount   *int `json:"agreedCount,omitempty"`
	DisputedCount *int `json:"disputedCount,omitempty"`
	PendingCount  *int `json:"pendingCount,omitempty"`
	// The same per settlement.
	AgreedSettlementCount   *int `json:"agreedSettlementCount,omitempty"`
	DisputedSettlementCount *int `json:"disputedSettlementCount,omitempty"`
	PendingSettlementCount  *int `json:"pendingSettlementCount,omitempty"`
	// The BAP's (commission) and the BPP's (netPayable) entitlements over the
	// settlements with accord true, and the answerer's own claim over the
	// others -- not an agreed amount.
	TotalAgreedCommission       *float64 `json:"totalAgreedCommission,omitempty"`
	TotalUnreconciledCommission *float64 `json:"totalUnreconciledCommission,omitempty"`
	TotalAgreedNetPayable       *float64 `json:"totalAgreedNetPayable,omitempty"`
	TotalUnreconciledNetPayable *float64 `json:"totalUnreconciledNetPayable,omitempty"`

	// OrderDayCounts: for each order day in the message, the sender's own
	// count of the pair's orders created that day and of those not yet
	// reconciled.
	OrderDayCounts []OrderDayCount `json:"orderDayCounts,omitempty"`
}

// OrderDayCount is one order day's counts, from this participant's records.
type OrderDayCount struct {
	OrderDate           string `json:"orderDate"`
	ReportedOrderCount  int    `json:"reportedOrderCount"`
	RemainingOrderCount int    `json:"remainingOrderCount"`
}

// summaryOf totals the settlement bags of a message's contracts. When they
// carry accord (an answer), it also counts the outcomes and totals the agreed
// and unreconciled entitlements.
func summaryOf(bapID, bppID, date string, contracts []any) Summary {
	sm := Summary{BapID: bapID, BppID: bppID, SettlementDate: date, Currency: "IDR"}
	answered := false
	var contractsBy, settlementsBy [3]int // agreed, disputed, pending
	var agreedCom, otherCom, agreedNet, otherNet float64
	for _, raw := range contracts {
		sm.ContractCount++
		worst := 0
		for _, st := range slice(asMap(raw)["settlements"]) {
			b := asMap(asMap(st)["settlementAttributes"])
			sm.SettlementCount++
			if c := str(b["currency"]); c != "" {
				sm.Currency = c
			}
			com, net := num(asMap(b["commission"])["value"]), num(asMap(b["netPayable"])["value"])
			sm.TotalOrderAmount += num(asMap(b["orderAmount"])["value"])
			sm.TotalCommission += com
			sm.TotalWithholding += num(asMap(b["withholdingAmount"])["value"])
			sm.TotalRefund += num(asMap(b["refundAmount"])["value"])
			sm.TotalNetPayable += net
			accord, ok := b["accord"].(bool)
			if !ok {
				continue
			}
			answered = true
			outcome := 0
			switch {
			case accord:
				agreedCom, agreedNet = agreedCom+com, agreedNet+net
			case settlement.IsPendingCode(str(asMap(b["dispute"])["code"])):
				outcome = 2
				otherCom, otherNet = otherCom+com, otherNet+net
			default:
				outcome = 1
				otherCom, otherNet = otherCom+com, otherNet+net
			}
			settlementsBy[outcome]++
			if outcome == 1 || (outcome == 2 && worst == 0) {
				worst = outcome
			}
		}
		contractsBy[worst]++
	}
	sm.TotalOrderAmount = round2(sm.TotalOrderAmount)
	sm.TotalCommission = round2(sm.TotalCommission)
	sm.TotalWithholding = round2(sm.TotalWithholding)
	sm.TotalRefund = round2(sm.TotalRefund)
	sm.TotalNetPayable = round2(sm.TotalNetPayable)
	if answered {
		i := func(n int) *int { return &n }
		f := func(v float64) *float64 { v = round2(v); return &v }
		sm.AgreedCount, sm.DisputedCount, sm.PendingCount = i(contractsBy[0]), i(contractsBy[1]), i(contractsBy[2])
		sm.AgreedSettlementCount, sm.DisputedSettlementCount, sm.PendingSettlementCount = i(settlementsBy[0]), i(settlementsBy[1]), i(settlementsBy[2])
		sm.TotalAgreedCommission, sm.TotalUnreconciledCommission = f(agreedCom), f(otherCom)
		sm.TotalAgreedNetPayable, sm.TotalUnreconciledNetPayable = f(agreedNet), f(otherNet)
	}
	return sm
}

// status is the answer's reconciliationStatus (answerStatus) from its counts.
func (sm Summary) status() string {
	if sm.DisputedSettlementCount == nil {
		return ""
	}
	return answerStatus(*sm.AgreedSettlementCount, *sm.DisputedSettlementCount, *sm.PendingSettlementCount)
}
