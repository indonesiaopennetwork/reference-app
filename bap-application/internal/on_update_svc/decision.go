package onupdatesvc

import (
	"encoding/json"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
)

// ---------------------------------------------------------------------------
// Reading the seller's decision off on_update.
//
// classify.go infers the outcome by comparing the returned contract against
// what the buyer proposed. That works, but an inference can never recover
// the seller's REASON -- and a buyer who is told only "declined" re-sends
// the same request, making the seller evaluate it again.
//
// A seller running this app now states the decision explicitly at
// contract.contractAttributes.updateDecision (see
// bpp-application/internal/orderchange/decision.go for why that slot
// validates). When that field is present it is AUTHORITATIVE -- the seller
// saying what they did beats us guessing from a diff. When it is absent,
// which is what any other ION participant will send, we fall back to
// classify.go and simply have no reason to show. Neither side breaks.
// ---------------------------------------------------------------------------

// statedDecision is the wire shape written by
// orderchange.UpdateDecision/Descriptor.
type statedDecision struct {
	Outcome string `json:"outcome"`
	Reason  *struct {
		Code     string `json:"code"`
		Name     string `json:"name"`
		LongDesc string `json:"longDesc"`
	} `json:"reason"`
}

// decisionFromContract extracts an explicitly stated decision, returning
// ok=false when the callback carries none.
func decisionFromContract(contract json.RawMessage) (outcome dbsqlc.UpdateOutcome, reasonCode, reasonText *string, ok bool) {
	if len(contract) == 0 {
		return "", nil, nil, false
	}
	var c struct {
		ContractAttributes struct {
			UpdateDecision *statedDecision `json:"updateDecision"`
		} `json:"contractAttributes"`
	}
	if err := json.Unmarshal(contract, &c); err != nil || c.ContractAttributes.UpdateDecision == nil {
		return "", nil, nil, false
	}
	d := c.ContractAttributes.UpdateDecision

	switch d.Outcome {
	case "APPROVED":
		outcome = dbsqlc.UpdateOutcomeAPPLIED
	case "REJECTED":
		outcome = dbsqlc.UpdateOutcomeDECLINED
	default:
		// An outcome we don't recognise is not trusted into a definite
		// state -- fall back to inference rather than inventing a meaning.
		return "", nil, nil, false
	}

	if d.Reason != nil {
		if d.Reason.Code != "" {
			code := d.Reason.Code
			reasonCode = &code
		}
		// Prefer the seller's own prose; fall back to the code's label so
		// the buyer always gets something readable when a reason was sent.
		text := d.Reason.LongDesc
		if text == "" {
			text = d.Reason.Name
		}
		if text != "" {
			reasonText = &text
		}
	}
	return outcome, reasonCode, reasonText, true
}
