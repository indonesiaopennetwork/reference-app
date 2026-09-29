package orderchange

// ---------------------------------------------------------------------------
// Fulfilment-phase feasibility -- EXTENSION POINT
//
// READ THIS FIRST IF YOU ARE ADDING ORDER TRACKING.
//
// A seller deciding on a change request needs to know how far the order has
// already progressed: changing a delivery address is fine before packing
// and usually impossible once the parcel has been handed to a courier.
//
// This app does NOT track fulfilment phase yet. Today
// contract_performances.status_code is written once ('PENDING') at init and
// nothing ever advances it, there is no seller endpoint to move it, and
// contract_tracking is unused. Rather than invent a fake phase or leave the
// seller UI with no place to put one, the warning machinery is built here
// and wired all the way through to the screen -- it simply reports nothing
// while the phase is unknown.
//
// To switch it on, you need exactly two things:
//
//  1. Implement PhaseReporter against wherever you end up storing phase
//     (most likely contract_performances.status_code, advanced by a new
//     seller endpoint). Return the phase plus whether the parcel has left
//     the seller -- that single boolean is what the warnings below turn on.
//
//  2. Pass your implementation into the dashboard handler in place of
//     NoPhaseTracking{}. Grep for NoPhaseTracking -- there is one call
//     site.
//
// Nothing in changeset.go, the HTTP response shape, or the seller UI needs
// to change: the UI already renders Warning values and already shows the
// phase label it is given. Per the product decision on this feature, a
// warning NEVER blocks approval -- a seller can sometimes redirect a
// parcel, so the system advises and the human decides.
// ---------------------------------------------------------------------------

import "context"

// Phase is how far along an order is, in the only terms the warnings need.
type Phase struct {
	// Code is the machine value, e.g. "DISPATCHED". Empty when unknown.
	Code string `json:"code"`
	// Label is what the seller reads, e.g. "Dispatched". Implementations
	// should return something human here; "Not tracked yet" is what the
	// no-op reports.
	Label string `json:"label"`
	// Tracked is false when the system genuinely does not know the phase.
	// The UI uses this to say "not tracked" rather than implying the order
	// is sitting at the earliest phase.
	Tracked bool `json:"tracked"`
	// LeftSeller is true once the goods are no longer in the seller's
	// hands (dispatched, in transit, delivered). This is the single signal
	// the delivery-address warning depends on -- deliberately a boolean
	// rather than an ordered enum, so adding phases later cannot silently
	// change which ones count as "too late".
	LeftSeller bool `json:"leftSeller"`
}

// PhaseReporter resolves the fulfilment phase of one contract. Implement
// this when order tracking is added; see the header comment.
type PhaseReporter interface {
	PhaseFor(ctx context.Context, contractID string) (Phase, error)
}

// NoPhaseTracking is the current implementation: it always reports an
// untracked phase, so no feasibility warnings are produced. It exists so
// the rest of the code can depend on the interface today and be switched
// over by changing one line later.
type NoPhaseTracking struct{}

// PhaseFor always reports "not tracked" and never errors.
func (NoPhaseTracking) PhaseFor(context.Context, string) (Phase, error) {
	return Phase{Label: "Not tracked yet", Tracked: false}, nil
}

// Warning is advice for the seller about a change request. It never
// prevents a decision -- see the header comment.
type Warning struct {
	// Severity is "danger" for "this almost certainly cannot be honoured"
	// and "caution" for "check before agreeing". The UI colours on this.
	Severity string `json:"severity"`
	Text     string `json:"text"`
}

// Warnings returns what the seller should be told before deciding, given
// how far the order has progressed and what the buyer wants changed.
//
// Returns nothing while the phase is untracked -- silence is correct there,
// because an invented warning would be worse than none. Once PhaseReporter
// is implemented this starts producing real advice with no other change.
func Warnings(phase Phase, changes []Change) []Warning {
	if !phase.Tracked || len(changes) == 0 {
		return nil
	}

	var out []Warning
	if phase.LeftSeller && changesAffectAddress(changes) {
		out = append(out, Warning{
			Severity: "danger",
			Text: "This order has already left the seller (" + phase.Label +
				"), so the delivery address usually cannot be changed. Reject with a reason unless you can redirect the parcel.",
		})
	}
	return out
}

func changesAffectAddress(changes []Change) bool {
	for _, c := range changes {
		if c.Group == "Address" {
			return true
		}
	}
	return false
}
