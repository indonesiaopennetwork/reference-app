// Package contractx holds the small set of read-only operations on a Beckn
// Contract that more than one bap-application package needs.
//
// It exists to keep a single implementation of two things that were about
// to be written twice: semantic JSON comparison (used both to classify an
// incoming on_update and to reject a no-op /update) and locating the
// delivery block inside a contract.
//
// Read-only by design: nothing here mutates a contract. Building an
// outbound contract stays with the caller that owns that wire format.
package contractx

import (
	"encoding/json"
	"reflect"
)

// JSONEqual reports whether two JSON documents are semantically equal.
//
// Semantic, not bytewise: both sides are parsed first, so object key order,
// whitespace and number formatting (1 vs 1.0) never register as a
// difference. Array order IS significant, because element order is
// meaningful in a contract.
//
// Absent or unparseable input is never equal to anything -- including
// another unparseable input. Callers therefore read false as "cannot prove
// these are the same", which is the safe reading for both callers: an
// on_update that cannot be matched falls through to MODIFIED rather than
// claiming a decision, and an /update that cannot be proven a no-op is
// allowed through rather than wrongly refused.
func JSONEqual(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// DeliveryDetails returns the deliveryDetails object from a contract, or nil
// when the contract carries none.
//
// Path: performance[].performanceAttributes.deliveryDetails -- the same
// place bap-frontend's change form writes and the seller's review screen
// reads. The first performance entry that has it wins; this app only ever
// creates one.
func DeliveryDetails(contract json.RawMessage) json.RawMessage {
	if len(contract) == 0 {
		return nil
	}
	var c struct {
		Performance []struct {
			PerformanceAttributes struct {
				DeliveryDetails json.RawMessage `json:"deliveryDetails"`
			} `json:"performanceAttributes"`
		} `json:"performance"`
	}
	if err := json.Unmarshal(contract, &c); err != nil {
		return nil
	}
	for _, p := range c.Performance {
		if dd := p.PerformanceAttributes.DeliveryDetails; len(dd) > 0 {
			return dd
		}
	}
	return nil
}

// DeliveryDetailsUnchanged reports whether proposed asks for exactly the
// delivery details current already holds -- i.e. the request would change
// nothing a seller could act on.
//
// Returns false whenever either side's delivery block is missing: an
// unprovable case must not be refused as a no-op, since refusing a
// legitimate request is worse than forwarding a redundant one (which the
// seller-side check still catches).
func DeliveryDetailsUnchanged(current, proposed json.RawMessage) bool {
	cur := DeliveryDetails(current)
	prop := DeliveryDetails(proposed)
	if len(cur) == 0 || len(prop) == 0 {
		return false
	}
	return JSONEqual(cur, prop)
}

// HasOrderCore reports whether a contract carries enough to serve as the
// stored state of an order -- specifically at least one commitment.
//
// beckn's Contract schema requires `commitments` (minItems 1) and is
// `additionalProperties: false`, so a contract without it cannot be sent
// back out: the adapter rejects it with
// SCH_REQUIRED_FIELD_MISSING / `property "commitments" is missing`.
//
// This exists to stop a minimal inbound contract from overwriting a
// complete snapshot. An on_update may legitimately carry only an id plus a
// decision (our own rejection path does exactly that when the contract has
// no attributes), and letting such a payload become the latest snapshot
// silently breaks every later action that builds an outbound contract from
// it -- status, cancel, and the no-op detection in Update.
func HasOrderCore(contract json.RawMessage) bool {
	if len(contract) == 0 {
		return false
	}
	var c struct {
		Commitments []json.RawMessage `json:"commitments"`
	}
	if err := json.Unmarshal(contract, &c); err != nil {
		return false
	}
	return len(c.Commitments) > 0
}
