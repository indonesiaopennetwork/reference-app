package orderchange

import (
	"encoding/json"
	"time"
)

// ---------------------------------------------------------------------------
// Carrying the seller's decision (and their reason) to the buyer.
//
// Beckn v2's on_update carries a contract and nothing else -- there is no
// "approved"/"rejected" field and no obvious slot for a rejection reason.
// The BAP can infer the outcome by comparing what came back against what it
// proposed, but an inference can never recover the REASON, and without a
// reason a buyer just re-sends the same request and the seller pays to
// evaluate it again.
//
// This is schema-legal, and the distinction matters:
//
//   - Inventing a NEW ion:* @type would fail onix's Extended Schema
//     validation, because the schema is not published at schema.ion.id.
//   - Adding a property INSIDE an already-published type does not.
//     IONTradeContract (the type that attaches to Contract.contractAttributes)
//     declares `additionalProperties: true`, so an extra key validates.
//
// The shape follows ION's own convention for an outcome reason: TradeContract
// models cancellation.reason as a beckn Descriptor, where Descriptor.code is
// the machine-comparable reason code and the prose is human text. This
// mirrors that exactly rather than inventing a different reason shape.
//
// Caveat to keep in mind: `updateDecision` is NOT in the published
// TradeContract schema. Other ION participants will ignore it (they will not
// error). Making it genuinely interoperable means proposing it upstream --
// until then the BAP treats it as authoritative when present and falls back
// to inference when absent, so nothing breaks against a peer that omits it.
// ---------------------------------------------------------------------------

// Descriptor is the beckn Descriptor subset used for a decision reason.
type Descriptor struct {
	Code     string `json:"code,omitempty"`
	Name     string `json:"name,omitempty"`
	LongDesc string `json:"longDesc,omitempty"`
}

// UpdateDecision is what the seller decided about one change request.
type UpdateDecision struct {
	// Outcome is APPROVED or REJECTED -- the seller's actual decision, not
	// the BAP's guess at it.
	Outcome   string      `json:"outcome"`
	Reason    *Descriptor `json:"reason,omitempty"`
	DecidedAt string      `json:"decidedAt,omitempty"`
}

// Decision outcome values carried on the wire.
const (
	OutcomeApproved = "APPROVED"
	OutcomeRejected = "REJECTED"
)

// RejectReason maps a seller-chosen code to its stable human label. The
// seller UI offers exactly these codes; a free-text note rides along in
// Descriptor.longDesc.
//
// Codes exist so the buyer sees consistent wording no matter which seller
// declined, and so a buyer who reads "already dispatched" does not re-send
// the same request -- which is the whole point of putting the reason on the
// wire. Add a code here and to REJECT_PRESETS in bpp-frontend together.
var RejectReason = map[string]string{
	"ALREADY_DISPATCHED":    "Already dispatched",
	"OUTSIDE_DELIVERY_AREA": "New address is outside the delivery area",
	"ALREADY_PACKED":        "Order is already packed",
	"DETAILS_UNVERIFIED":    "Could not verify the new contact details",
	"OTHER":                 "Declined by the seller",
}

// BuildDecisionAttributes returns the contractAttributes object to send on
// on_update: the contract's existing attributes with updateDecision added.
//
// current may be nil or JSON null -- which is the normal case here, since
// contracts.contract_attributes is routinely unset -- in which case a
// minimal, correctly-typed IONTradeContract object is created so the
// decision has somewhere valid to live rather than being dropped.
func BuildDecisionAttributes(current json.RawMessage, d UpdateDecision) json.RawMessage {
	attrs := decodeObject(current)
	if attrs == nil {
		attrs = map[string]any{}
	}
	// Ensure the JSON-LD type is present, otherwise onix has no schema to
	// validate the object against and the whole callback is rejected.
	if _, ok := attrs["@type"]; !ok {
		attrs["@type"] = "ion:IONTradeContract"
	}
	if _, ok := attrs["@context"]; !ok {
		attrs["@context"] = "https://schema.ion.id/release1/schema/extension/trade/TradeContract/v1/context.jsonld"
	}
	if d.DecidedAt == "" {
		d.DecidedAt = time.Now().UTC().Format(time.RFC3339)
	}
	attrs["updateDecision"] = d

	out, err := json.Marshal(attrs)
	if err != nil {
		// Losing the decision field is better than sending a malformed
		// contract that fails validation and tells the buyer nothing at all.
		return current
	}
	return out
}

// RejectDecision builds the decision for a rejection. code may be empty or
// unrecognised, in which case it degrades to OTHER rather than sending a
// code the buyer's app cannot label.
func RejectDecision(code, note string) UpdateDecision {
	label, ok := RejectReason[code]
	if !ok {
		code, label = "OTHER", RejectReason["OTHER"]
	}
	return UpdateDecision{
		Outcome: OutcomeRejected,
		Reason:  &Descriptor{Code: code, Name: label, LongDesc: note},
	}
}

// ApproveDecision builds the decision for an approval. No reason is
// attached: "we did what you asked" needs no explanation.
func ApproveDecision() UpdateDecision {
	return UpdateDecision{Outcome: OutcomeApproved}
}

// AttachDecision returns the contract with the seller's decision recorded
// at contract.contractAttributes.updateDecision -- the slot
// IONTradeContract declares itself attachable to.
//
// Operates on the whole Contract rather than the bare attributes bag
// because that is what on_update actually carries, and because the two
// decision paths hand us different things: an approval sends the buyer's
// full proposed contract, while a rejection sends the contract as it
// stands. Both are Contract objects here, so both get the decision in the
// same place.
func AttachDecision(contract json.RawMessage, d UpdateDecision) json.RawMessage {
	obj := decodeObject(contract)
	if obj == nil {
		obj = map[string]any{}
	}

	var existing json.RawMessage
	if raw, ok := obj["contractAttributes"]; ok {
		if b, err := json.Marshal(raw); err == nil {
			existing = b
		}
	}

	attrs := BuildDecisionAttributes(existing, d)
	var attrsObj map[string]any
	if err := json.Unmarshal(attrs, &attrsObj); err != nil {
		return contract
	}
	obj["contractAttributes"] = attrsObj

	out, err := json.Marshal(obj)
	if err != nil {
		// Better to send the contract without the decision than to send
		// something malformed that fails validation outright.
		return contract
	}
	return out
}
