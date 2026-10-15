package reconcilesvc

import (
	"context"
	"encoding/json"
)

// Result is how a contract fared in the disputed batch it was last in: in an
// all-or-nothing batch most contracts may match, and only those with accord
// false are the dispute.
type Result struct {
	Accord bool   `json:"accord"`
	Code   string `json:"code,omitempty"`
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
		r := &Result{Accord: accord, Code: str(dispute["code"]), Reason: str(dispute["description"])}
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
		  FROM reconciliations WHERE id = ANY($1)`, ids)
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
			due[i].LastResult = byBatch[due[i].ReconciliationID][due[i].ContractID]
		}
	}
}
