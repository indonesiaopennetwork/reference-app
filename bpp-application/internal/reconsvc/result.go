package reconsvc

import (
	"context"
	"encoding/json"

	"github.com/ion/winroom/bpp/internal/settlement"
)

// Result is how a contract fared in the batch it was last in, when that left
// it unresolved: disputed, or pending (sent before its settlement window, or
// awaiting information).
type Result struct {
	Accord bool `json:"accord"`
	// Pending: accord false with a pending code; the contract is sent again
	// later rather than disputed.
	Pending bool   `json:"pending,omitempty"`
	Code    string `json:"code,omitempty"`
	// Field is the figure the dispute names (dispute.field), when it does.
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Diffs are the figures the two participants differ on.
	Diffs []Diff `json:"diffs,omitempty"`
}

// Diff is one figure that differs: this participant's, the counterparty's,
// and Diff = Theirs - Ours.
type Diff struct {
	Field  string  `json:"field"`
	Ours   float64 `json:"ours"`
	Theirs float64 `json:"theirs"`
	Diff   float64 `json:"diff"`
}

// batchResults reads each contract's result from a batch's /reconcile
// (request) and /on_reconcile (response). outbound: this participant sent the
// /reconcile, so the request holds its figures and the response the
// counterparty's; otherwise the other way round.
func batchResults(request, response json.RawMessage, outbound bool) map[string]*Result {
	figures := func(raw json.RawMessage) map[string]map[string]any {
		var msg struct {
			Message struct {
				Contracts []map[string]any `json:"contracts"`
			} `json:"message"`
		}
		out := map[string]map[string]any{}
		if json.Unmarshal(raw, &msg) != nil {
			return out
		}
		for _, c := range msg.Message.Contracts {
			out[str(c["id"])] = asMap(asMap(firstOf(slice(c["settlements"])))["settlementAttributes"])
		}
		return out
	}
	sent, answered := figures(request), figures(response)
	results := map[string]*Result{}
	for id, ans := range answered {
		accord, _ := ans["accord"].(bool)
		dispute := asMap(ans["dispute"])
		r := &Result{Accord: accord, Code: str(dispute["code"]), Field: str(dispute["field"]), Reason: str(dispute["description"])}
		r.Pending = !accord && settlement.IsPendingCode(r.Code)
		ours, theirs := sent[id], ans
		if !outbound {
			ours, theirs = ans, sent[id]
		}
		for _, c := range reconcileComponents {
			o, t := num(asMap(ours[c.key])["value"]), num(asMap(theirs[c.key])["value"])
			if d := round2(t - o); d != 0 {
				r.Diffs = append(r.Diffs, Diff{Field: c.key, Ours: o, Theirs: t, Diff: d})
			}
		}
		results[id] = r
	}
	return results
}

// attachResults sets LastResult on the contracts in due that a batch didn't
// reconcile -- disputed, or sent before their settlement window (no status,
// a batch id) -- from the batches they were last in.
func (s *Service) attachResults(ctx context.Context, due []DueContract) {
	var ids []string
	for _, d := range due {
		if d.ReconciliationID != "" {
			ids = append(ids, d.ReconciliationID)
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, direction, COALESCE(request_payload, 'null'), COALESCE(response_payload, 'null')
		  FROM reconciliations WHERE bpp_id = $1 AND id = ANY($2)`, s.cfg.BppID, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	byBatch := map[string]map[string]*Result{}
	for rows.Next() {
		var id, direction string
		var req, resp json.RawMessage
		if rows.Scan(&id, &direction, &req, &resp) == nil {
			byBatch[id] = batchResults(req, resp, direction == directionOutbound)
		}
	}
	for i := range due {
		if due[i].ReconciliationID != "" {
			due[i].LastResult = byBatch[due[i].ReconciliationID][due[i].ContractID.String()]
		}
	}
}
