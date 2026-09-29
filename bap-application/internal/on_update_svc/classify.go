package onupdatesvc

import (
	"encoding/json"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/contractx"
)

// classifyOutcome works out what the seller actually decided about a
// buyer's update request, given the contract that came back in on_update.
//
// This exists because Beckn v2's on_update carries a contract and nothing
// else -- there is no "approved"/"rejected" field, and no place to put a
// rejection reason. A BPP that rejects an update is expected to reply with
// the contract unchanged, which on the wire looks exactly like a BPP that
// approved a change. The only way to tell them apart without inventing an
// unpublished ion:* extension (which onix's Extended-Schema validation
// would reject) is to compare what came back against the two contracts the
// BAP already knows:
//
//	received == proposed  -> the seller applied the buyer's change: APPLIED
//	received == prior     -> the seller left the contract alone:    DECLINED
//	neither               -> MODIFIED
//
// MODIFIED is deliberately its own outcome rather than being folded into
// one of the other two. It legitimately covers a seller counter-offer
// (approving a change but adjusting it), but it also covers an
// intermediary that re-serialised or stripped a field in transit -- and in
// both cases claiming a definite "approved" or "rejected" would tell the
// buyer about a decision the seller never made. Surfacing it as "the
// seller responded with different terms, review them" is honest about what
// the protocol actually lets the BAP know.
//
// Comparison is semantic, not bytewise: both sides are parsed to Go values
// first, so JSON key ordering, whitespace and number formatting differences
// introduced anywhere along the path never masquerade as a seller decision.
func classifyOutcome(proposed, prior, received json.RawMessage) dbsqlc.UpdateOutcome {
	if contractx.JSONEqual(received, proposed) {
		return dbsqlc.UpdateOutcomeAPPLIED
	}
	if contractx.JSONEqual(received, prior) {
		return dbsqlc.UpdateOutcomeDECLINED
	}
	return dbsqlc.UpdateOutcomeMODIFIED
}
