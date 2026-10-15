package reconcilesvc

import "strings"

// A reconciliation batch is agreed as a whole or not at all: only when every
// contract in it has accord true is the batch AGREED and its contracts paid
// out. Otherwise the batch is DISPUTED, none of its contracts is agreed, and
// its sender sends all of them again -- same reconciliationId, figures worked
// out afresh -- until the counterparty agrees with every one.

// batchStatus is a batch's outcome: AGREED when every one of its total
// contracts has accord true, otherwise DISPUTED.
func batchStatus(agreed, total int) string {
	if total > 0 && agreed == total {
		return "AGREED"
	}
	return "DISPUTED"
}

// batchOutcome is a batch's reconciliationStatus. Contracts sent before
// their settlement window (early) don't count: the batch is AGREED when
// every other contract is agreed, DISPUTED when one isn't, and PENDING when
// every contract was early.
func batchOutcome(agreed, counted, early int) string {
	if counted == 0 && early > 0 {
		return "PENDING"
	}
	return batchStatus(agreed, counted)
}

// contractOutcome is a contract's reconciliation status once its batch is
// answered: AGREED only when the whole batch is.
func contractOutcome(batch string) string {
	if batch == "AGREED" {
		return "AGREED"
	}
	return "DISPUTED"
}

// nextBatch picks what this participant (selfID) sends next from its due
// contracts. While a batch it sent is disputed, that batch goes again in
// full, under its reconciliationId (resendID), and contracts that fell due
// since wait for it. Contracts in a batch the counterparty sent are left to
// the counterparty to send again.
func nextBatch(due []DueContract, selfID string) (resendID string, batch []DueContract) {
	for _, d := range due {
		if d.ReconciliationStatus == "DISPUTED" && sentBy(d.ReconciliationID, selfID) &&
			(resendID == "" || d.ReconciliationID < resendID) {
			resendID = d.ReconciliationID
		}
	}
	for _, d := range due {
		disputed := d.ReconciliationStatus == "DISPUTED"
		switch {
		case resendID != "":
			if disputed && d.ReconciliationID == resendID {
				batch = append(batch, d)
			}
		case disputed && !sentBy(d.ReconciliationID, selfID):
			// the counterparty's batch: it sends it again
		default:
			batch = append(batch, d)
		}
	}
	return resendID, batch
}

// sentBy reports whether batch id was sent by participant: a
// reconciliationId ends with its sender's subscriber ID.
func sentBy(id, participant string) bool {
	return id != "" && strings.HasSuffix(id, "-"+participant)
}

// choose is the batch Send sends. With no selection it is nextBatch's (the
// disputed batch this participant sent, again in full under its
// reconciliationId, or else everything due). Otherwise it is the selected
// contracts that are due or disputed -- in any mix, as a new batch; a
// disputed contract moves to it from its old batch.
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
