package clientsvc

import "errors"

// Sentinel errors for Update's and Cancel's edge-case handling (see
// service.go's Update/Cancel and handler.go's HandleUpdate) -- wrapped with
// fmt.Errorf's %w so errors.Is still matches through the added context,
// letting HTTP handlers map each to the right protocol-level status code
// instead of a blanket 500.
var (
	ErrInvalidTransactionID = errors.New("invalid transaction id")
	ErrTransactionNotFound  = errors.New("transaction not found")
	// ErrInvalidUpdateState covers both Update and Cancel: both gate on the
	// transaction being CONFIRMED via the same compare-and-swap technique
	// (MarkUpdateSentIfConfirmed / MarkCancelSentIfConfirmed), so "wrong
	// state" and "a sibling action is already in flight" look identical to
	// callers of either -- see Cancel's own doc comment for why that's
	// deliberate, not an accident of reusing one error.
	ErrInvalidUpdateState = errors.New("transaction is not in a state that accepts this action")
)

// MaxUpdateRequestsPerOrder caps how many change requests one order may
// ever carry. Raising it is a one-line change here, deliberately -- it is a
// policy number, not a structural limit.
//
// Counted across ALL outcomes, not just pending ones: the cap exists so a
// buyer cannot make a seller re-evaluate the same order indefinitely, and a
// declined or abandoned attempt cost the seller attention exactly as an
// approved one did. The separate "one in flight at a time" rule is enforced
// independently by the CONFIRMED -> UPDATE_SENT compare-and-swap.
const MaxUpdateRequestsPerOrder = 5

// BuyerUpdateHistoryLimit is how many past change requests the buyer's
// order view shows, newest first. Short on purpose: the buyer needs to see
// what they already asked and how it went, not an audit log. The seller's
// dashboard can list the full set.
const BuyerUpdateHistoryLimit = 5

// ErrTooManyUpdateRequests is returned once MaxUpdateRequestsPerOrder is
// reached. Surfaced as 409 Conflict, not 400 -- the request is well-formed,
// the order has simply exhausted its allowance.
var ErrTooManyUpdateRequests = errors.New("change request limit reached for this order")

// ErrNoChangeRequested is returned when a buyer submits a change request
// that asks for exactly the delivery details the order already has.
//
// Refused at the BAP rather than forwarded because the BPP ACKs an inbound
// /update BEFORE validating it (see its handler), so a seller-side refusal
// cannot reach us as an error -- the transaction would sit at UPDATE_SENT
// until the 48h sweep. Refusing here also avoids spending one of the
// order's MaxUpdateRequestsPerOrder allowance on a request that asks for
// nothing, and avoids putting a blank request in front of a seller.
var ErrNoChangeRequested = errors.New("no change requested: the details submitted match the order's current details")
