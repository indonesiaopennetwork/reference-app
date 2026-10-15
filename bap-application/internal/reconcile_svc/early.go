package reconcilesvc

import (
	"fmt"
	"time"
)

// CodeBeforeWindow is the dispute code for a contract sent to /reconcile
// before its settlement window has passed. The answer has accord false and
// the contract is not part of the batch's outcome: it stays to be reconciled
// once it is due.
const CodeBeforeWindow = "SETTLEMENT_WINDOW_NOT_REACHED"

// beforeWindow says whether d -- this BAP's view of a contract -- is not due
// yet, and why. A contract whose due time this BAP can't tell (one paid out
// after the seller's return window) is left to the seller.
func beforeWindow(d DueContract) (reason string, early bool) {
	switch {
	case d.windowDue:
		return "", false
	case d.DueAt != nil:
		return fmt.Sprintf("sent before its settlement window: %s + %s, due at %s",
			d.BasisEvent, d.Window, d.DueAt.UTC().Format(time.RFC3339)), true
	case d.BasisEvent == "awaiting delivery":
		return fmt.Sprintf("sent before its settlement window: not delivered yet, so the %s + %s window has not started",
			d.Basis, d.Window), true
	}
	return "", false
}
