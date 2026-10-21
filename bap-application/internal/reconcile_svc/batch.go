package reconcilesvc

import (
	"strings"

	"github.com/ion/winroom/bap/internal/settlement"
)

// Agreement is per settlement (ION Release 1): a settlement the counterparty
// agrees with (accord true) is AGREED and goes on to SEAM whatever happens to
// the others in its batch. One with accord false is DISPUTED, or -- for the
// pending codes SETTLEMENT_WINDOW_NOT_REACHED and AWAITING_INFORMATION --
// pending: it keeps only the batch id and is sent again later. A batch's
// reconciliationStatus only summarizes its answer (answerStatus). The
// sender sends the disputed and pending settlements again in a new
// /reconcile of the same exchange (same context.transactionId, new
// messageId).

// settlementOutcome is a settlement's reconciliation status from the answer:
// AGREED, DISPUTED, or "" when it is pending.
func settlementOutcome(accord bool, code string) string {
	switch {
	case accord:
		return "AGREED"
	case settlement.IsPendingCode(code):
		return ""
	}
	return "DISPUTED"
}

// answerStatus is an answer's reconciliationStatus: DISPUTED when any
// settlement is disputed, otherwise PENDING when any is pending, otherwise
// AGREED.
func answerStatus(agreed, disputed, pending int) string {
	switch {
	case disputed > 0:
		return "DISPUTED"
	case pending > 0:
		return "PENDING"
	}
	return "AGREED"
}

// unresolved reports whether d was left unresolved by a batch: disputed, or
// pending and due by now.
func unresolved(d DueContract) bool {
	return d.ReconciliationStatus == "DISPUTED" || (d.ReconciliationStatus == "" && d.ReconciliationID != "" && d.Due)
}

// nextBatch picks what this participant (selfID) sends next from its due
// contracts. While an exchange it started has unresolved settlements
// (disputed, or pending and now due), those go again -- only those -- in that
// exchange (resendID), and contracts that fell due since wait for it.
// Disputed contracts of an exchange the counterparty started are left to the
// counterparty to send again.
func nextBatch(due []DueContract, selfID string) (resendID string, batch []DueContract) {
	for _, d := range due {
		if unresolved(d) && sentBy(d.ReconciliationID, selfID) &&
			(resendID == "" || d.ReconciliationID < resendID) {
			resendID = d.ReconciliationID
		}
	}
	for _, d := range due {
		switch {
		case resendID != "":
			if d.ReconciliationID == resendID && unresolved(d) {
				batch = append(batch, d)
			}
		case d.ReconciliationStatus == "DISPUTED" && !sentBy(d.ReconciliationID, selfID):
			// the counterparty's exchange: it sends it again
		default:
			batch = append(batch, d)
		}
	}
	return resendID, batch
}

// sentBy reports whether batch id was started by participant: a batch id
// (recon-<date>T<time>Z-<senderId>, kept by each participant for an
// exchange) ends with the subscriber ID of the participant that started it.
func sentBy(id, participant string) bool {
	return id != "" && strings.HasSuffix(id, "-"+participant)
}

// choose is the batch Send sends. With no selection it is nextBatch's (the
// unresolved settlements of an exchange this participant started, again in
// that exchange, or else everything due). Otherwise it is the selected
// contracts that are due or disputed -- in any mix, as a new exchange; a
// disputed contract moves to it from its old one.
func choose(due []DueContract, selfID string, selected []string) (resendID string, batch []DueContract) {
	if len(selected) == 0 {
		return nextBatch(due, selfID)
	}
	keep := map[string]bool{}
	for _, id := range selected {
		keep[id] = true
	}
	for _, d := range due {
		if keep[d.ContractID] && selectable(d) {
			batch = append(batch, d)
		}
	}
	return "", batch
}

// selectable reports whether the operator can put d in a /reconcile: it is
// due, or disputed (in either participant's batch).
func selectable(d DueContract) bool {
	return d.Due || d.ReconciliationStatus == "DISPUTED"
}
