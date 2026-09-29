// Package orderchange turns a buyer's /update proposal into something a
// human seller can actually read and act on.
//
// Why this package exists rather than a generic JSON diff:
//
// A buyer's /update carries a FULL replacement contract (PUT semantics),
// built from the BAP's own contract snapshot. The seller, meanwhile, stores
// the live order NORMALISED across contract_performances / commitments /
// considerations -- contracts.contract_attributes is only a small JSON-LD
// extension bag and is routinely null. So the two sides are not the same
// document in two versions; they are two different representations of the
// same order.
//
// Diffing them wholesale -- in Go or with a Postgres jsonb comparison --
// therefore produces hundreds of rows of "this field is new", because
// almost nothing on the proposal side has a counterpart to compare
// against. That is exactly the unreadable seller screen this package
// replaces, and no amount of better diffing fixes it: the input is wrong,
// not the algorithm.
//
// Instead this compares ONLY the small, explicit set of fields a buyer is
// actually allowed to change, reading each side from its own authoritative
// location. That buys four things a generic diff cannot:
//
//  1. Protocol noise (@context, @type, policies, serviceability,
//     operatingHours) can never appear, because it is not on the list.
//  2. Every field has a human label -- "Delivery city", not
//     "commitments.0.offer.offerAttributes.policies.cancellation.window".
//  3. It is stable when either side's JSON shape changes.
//  4. It is unit-testable with no database.
//
// To support a new buyer-changeable field, add one entry to
// deliveryFields. Nothing else here needs to change.
package orderchange

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Change is one field the buyer wants changed, ready to render as a
// before/after row. Before is empty when the seller holds no current value.
type Change struct {
	Label  string `json:"label"`
	Group  string `json:"group"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// field describes one buyer-changeable leaf: where to find it inside a
// deliveryDetails object, and what to call it in front of a human.
type field struct {
	label string
	group string
	path  []string
}

// deliveryFields is the whitelist. Order matters -- it's the order the
// seller sees. Add a field here to support changing it; that is the only
// edit needed.
var deliveryFields = []field{
	{"Contact name", "Contact", []string{"contact", "name"}},
	{"Phone", "Contact", []string{"contact", "phone"}},
	{"Street address", "Address", []string{"address", "streetAddress"}},
	{"City", "Address", []string{"address", "addressLocality"}},
	{"State / region", "Address", []string{"address", "addressRegion"}},
	{"Postal code", "Address", []string{"address", "postalCode"}},
	{"Country", "Address", []string{"address", "addressCountry"}},
}

// Summarize compares the seller's current performance attributes against
// the buyer's proposed contract and returns only the whitelisted fields
// that actually differ.
//
// currentPerformance is a contract_performances.performance_attributes
// object (an ion:IONTradePerformance). proposedContract is the buyer's
// requested_attributes contract. Either may be nil/empty -- a missing
// current side yields changes with an empty Before, which the UI shows as
// "not set" rather than pretending the buyer changed nothing.
func Summarize(currentPerformance, proposedContract json.RawMessage) []Change {
	current := deliveryDetailsFromPerformance(currentPerformance)
	proposed := deliveryDetailsFromContract(proposedContract)
	if proposed == nil {
		// Nothing the seller can act on -- the proposal carries no
		// delivery details at all. Deliberately not reported as "no
		// changes": see Headline, which distinguishes the two.
		return nil
	}

	// An absent proposed value is not a request to blank the field -- the
	// buyer UI submits the whole object, so a missing leaf means "not
	// provided", not "delete this". diffDelivery enforces that.
	return diffDelivery(current, proposed)
}

// Headline is the one-line summary the seller reads in the list, without
// expanding anything: "Delivery address and phone" or similar. hasProposal
// distinguishes "the buyer changed nothing we support" from "this proposal
// carried no delivery details at all", which are different problems.
func Headline(changes []Change, hasProposal bool) string {
	if !hasProposal {
		return "No delivery details in this request"
	}
	if len(changes) == 0 {
		return "No changes to delivery details"
	}

	// Group into the coarse things a human thinks in ("address", "phone"),
	// not the individual leaves -- a buyer correcting four address lines
	// has made one change as far as the seller is concerned.
	var groups []string
	seen := map[string]bool{}
	for _, c := range changes {
		g := strings.ToLower(c.Group)
		if g == "contact" {
			g = strings.ToLower(c.Label)
		} else {
			g = "delivery " + g
		}
		if !seen[g] {
			seen[g] = true
			groups = append(groups, g)
		}
	}

	switch len(groups) {
	case 1:
		return upperFirst(groups[0])
	case 2:
		return upperFirst(groups[0] + " and " + groups[1])
	default:
		return upperFirst(strings.Join(groups[:len(groups)-1], ", ") + " and " + groups[len(groups)-1])
	}
}

// HasDeliveryProposal reports whether the proposal carried any delivery
// details at all -- the hasProposal argument to Headline.
func HasDeliveryProposal(proposedContract json.RawMessage) bool {
	return deliveryDetailsFromContract(proposedContract) != nil
}

// DeliveryDetailsFromPerformanceJSON returns the deliveryDetails object
// inside a performance-attributes bag as raw JSON, or nil when there is
// none. This is the one place that knows where delivery details live on the
// seller's side -- callers needing to store or forward that block should
// use it rather than re-deriving the path.
func DeliveryDetailsFromPerformanceJSON(raw json.RawMessage) json.RawMessage {
	dd := deliveryDetailsFromPerformance(raw)
	if len(dd) == 0 {
		return nil
	}
	b, err := json.Marshal(dd)
	if err != nil {
		return nil
	}
	return b
}

// deliveryDetailsFromPerformance pulls deliveryDetails out of a single
// performance-attributes object (the seller's stored side).
func deliveryDetailsFromPerformance(raw json.RawMessage) map[string]any {
	obj := decodeObject(raw)
	if obj == nil {
		return nil
	}
	dd, _ := obj["deliveryDetails"].(map[string]any)
	return dd
}

// deliveryDetailsFromContract pulls deliveryDetails out of the buyer's
// proposed contract, which nests it under performance[].performanceAttributes
// -- the same place bap-frontend's Request Change form writes it. The first
// performance entry that has it wins; this app only ever creates one.
func deliveryDetailsFromContract(raw json.RawMessage) map[string]any {
	obj := decodeObject(raw)
	if obj == nil {
		return nil
	}
	perfs, _ := obj["performance"].([]any)
	for _, p := range perfs {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		attrs, ok := pm["performanceAttributes"].(map[string]any)
		if !ok {
			continue
		}
		if dd, ok := attrs["deliveryDetails"].(map[string]any); ok {
			return dd
		}
	}
	return nil
}

func decodeObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	return obj
}

// stringAtPath walks nested maps and renders the leaf as a display string.
// Anything that isn't a scalar yields "" -- the whitelist only ever points
// at scalars, so a non-scalar means the payload is shaped unexpectedly and
// is better shown as absent than as Go's map formatting.
func stringAtPath(obj map[string]any, path []string) string {
	cur := any(obj)
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[key]
		if !ok {
			return ""
		}
	}
	switch v := cur.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case bool:
		return fmt.Sprintf("%t", v)
	case float64:
		// JSON numbers decode to float64; render whole numbers without a
		// trailing .0 so a postal code reads 560034, not 560034.0.
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", v), "0"), ".")
	default:
		return ""
	}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// DeliveryDetailsJSON returns the proposal's deliveryDetails object as raw
// JSON, ready to be written straight into
// contract_performances.performance_attributes. Returns nil when the
// proposal carries no delivery details, so callers can skip the write
// rather than blanking a live address with an empty object.
func DeliveryDetailsJSON(proposedContract json.RawMessage) json.RawMessage {
	dd := deliveryDetailsFromContract(proposedContract)
	if len(dd) == 0 {
		return nil
	}
	b, err := json.Marshal(dd)
	if err != nil {
		return nil
	}
	return b
}

// SummarizeFromDeliveryDetails is Summarize given the "before" delivery
// details directly, rather than the performance bag they live inside.
//
// This is the form the seller's screen should use, because the before state
// must come from the snapshot taken when the request was QUEUED, not from
// the live contract. Diffing against the live contract silently breaks once
// a request is approved: the contract then holds the requested values, so
// current == requested and the change list collapses to empty -- leaving no
// record of what was approved. See migration 030.
func SummarizeFromDeliveryDetails(beforeDeliveryDetails, proposedContract json.RawMessage) []Change {
	before := decodeObject(beforeDeliveryDetails)
	proposed := deliveryDetailsFromContract(proposedContract)
	if proposed == nil {
		return nil
	}
	return diffDelivery(before, proposed)
}

// diffDelivery is the shared field-by-field comparison behind both
// Summarize and SummarizeFromDeliveryDetails.
func diffDelivery(before, proposed map[string]any) []Change {
	changes := make([]Change, 0, len(deliveryFields))
	for _, f := range deliveryFields {
		b := stringAtPath(before, f.path)
		a := stringAtPath(proposed, f.path)
		if a == "" || b == a {
			continue
		}
		changes = append(changes, Change{Label: f.label, Group: f.group, Before: b, After: a})
	}
	return changes
}
