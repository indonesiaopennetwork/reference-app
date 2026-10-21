package reconsvc

import (
	"testing"

	"github.com/google/uuid"
)

const self, other = "me.example.id", "them.example.id"

func dueContract(id, status, reconID string) DueContract {
	return DueContract{ContractID: uuid.MustParse(id), ReconciliationStatus: status, ReconciliationID: reconID, Due: true}
}

func ids(batch []DueContract) []string {
	out := []string{}
	for _, d := range batch {
		out = append(out, d.ContractID.String()[len(d.ContractID.String())-1:])
	}
	return out
}

func TestEachSettlementHasItsOwnOutcome(t *testing.T) {
	if settlementOutcome(true, "") != "AGREED" || settlementOutcome(false, "COMMISSION_MISMATCH") != "DISPUTED" {
		t.Error("accord true is AGREED, a mismatch DISPUTED")
	}
	if settlementOutcome(false, "SETTLEMENT_WINDOW_NOT_REACHED") != "" || settlementOutcome(false, "AWAITING_INFORMATION") != "" {
		t.Error("a pending code leaves the settlement pending")
	}
}

func TestOnlyTheUnresolvedSettlementsAreSentAgain(t *testing.T) {
	mine := "recon-2026-10-13T100000Z-" + self
	due := []DueContract{
		dueContract("00000000-0000-0000-0000-000000000001", "DISPUTED", mine),
		dueContract("00000000-0000-0000-0000-000000000002", "", ""),   // fell due since: waits
		dueContract("00000000-0000-0000-0000-000000000003", "", mine), // pending in it, due now
	}
	id, batch := nextBatch(due, self)
	if id != mine || len(batch) != 2 || ids(batch)[0] != "1" || ids(batch)[1] != "3" {
		t.Errorf("resend %q with %v", id, ids(batch))
	}
}

func TestANewBatchLeavesTheCounterpartysDisputedOnes(t *testing.T) {
	due := []DueContract{
		dueContract("00000000-0000-0000-0000-000000000001", "DISPUTED", "recon-2026-10-13T100000Z-"+other),
		dueContract("00000000-0000-0000-0000-000000000002", "", ""),
		dueContract("00000000-0000-0000-0000-000000000003", "FAILED", ""),
	}
	id, batch := nextBatch(due, self)
	if id != "" || len(batch) != 2 || ids(batch)[0] != "2" || ids(batch)[1] != "3" {
		t.Errorf("new batch %q with %v", id, ids(batch))
	}
}

func TestChooseSelected(t *testing.T) {
	mine := "recon-2026-10-13T100000Z-" + self
	theirs := "recon-2026-10-13T110000Z-" + other
	due := []DueContract{
		dueContract("00000000-0000-0000-0000-000000000001", "DISPUTED", mine),
		dueContract("00000000-0000-0000-0000-000000000002", "", ""),
		dueContract("00000000-0000-0000-0000-000000000003", "DISPUTED", mine),
		dueContract("00000000-0000-0000-0000-000000000004", "", ""),
		dueContract("00000000-0000-0000-0000-000000000005", "DISPUTED", theirs),
	}
	due[3].Due = false // not due yet
	sel := func(n ...string) []string {
		var out []string
		for _, x := range n {
			out = append(out, "00000000-0000-0000-0000-00000000000"+x)
		}
		return out
	}

	if id, batch := choose(due, self, nil); id != mine || len(batch) != 2 {
		t.Errorf("no selection: %q %v, want the disputed batch", id, ids(batch))
	}
	if id, batch := choose(due, self, sel("1", "2", "5")); id != "" || len(batch) != 3 {
		t.Errorf("a mix: %q %v, want a new batch of 1, 2, 5", id, ids(batch))
	}
	if id, batch := choose(due, self, sel("4")); id != "" || len(batch) != 0 {
		t.Errorf("not due: %q %v, want nothing", id, ids(batch))
	}
	if !selectable(due[0]) || !selectable(due[4]) || selectable(due[3]) {
		t.Error("selectable: due or disputed")
	}
}

func TestTheAnswerStatusSummarizes(t *testing.T) {
	for _, c := range []struct {
		agreed, disputed, pending int
		want                      string
	}{
		{3, 0, 0, "AGREED"},
		{2, 1, 0, "DISPUTED"},
		{2, 0, 1, "PENDING"},
		{1, 1, 1, "DISPUTED"},
		{0, 0, 3, "PENDING"}, // all sent before their window
	} {
		if got := answerStatus(c.agreed, c.disputed, c.pending); got != c.want {
			t.Errorf("answerStatus(%d, %d, %d) = %s, want %s", c.agreed, c.disputed, c.pending, got, c.want)
		}
	}
}
