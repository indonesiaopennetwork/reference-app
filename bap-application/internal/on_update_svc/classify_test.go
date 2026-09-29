package onupdatesvc

import (
	"encoding/json"
	"testing"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
)

// The whole approved-vs-rejected readout the buyer sees rests on this
// function, because on_update's wire shape carries no decision field --
// so each branch is pinned here explicitly, including the ones where the
// honest answer is "can't tell" (MODIFIED).
func TestClassifyOutcome(t *testing.T) {
	const (
		prior    = `{"id":"c1","performance":[{"performanceAttributes":{"deliveryDetails":{"contact":{"phone":"111"}}}}]}`
		proposed = `{"id":"c1","performance":[{"performanceAttributes":{"deliveryDetails":{"contact":{"phone":"222"}}}}]}`
		counter  = `{"id":"c1","performance":[{"performanceAttributes":{"deliveryDetails":{"contact":{"phone":"333"}}}}]}`
	)

	tests := []struct {
		name                      string
		proposed, prior, received string
		want                      dbsqlc.UpdateOutcome
	}{
		{
			name:     "seller applied the buyer's change verbatim",
			proposed: proposed, prior: prior, received: proposed,
			want: dbsqlc.UpdateOutcomeAPPLIED,
		},
		{
			name:     "seller rejected: contract comes back unchanged",
			proposed: proposed, prior: prior, received: prior,
			want: dbsqlc.UpdateOutcomeDECLINED,
		},
		{
			name:     "seller counter-offered: matches neither side",
			proposed: proposed, prior: prior, received: counter,
			want: dbsqlc.UpdateOutcomeMODIFIED,
		},
		{
			// Key order and whitespace must never be mistaken for a
			// seller decision -- anything on the path (onix, a proxy, a
			// re-serialisation) may reorder JSON object keys freely.
			name:     "reordered keys and whitespace still read as APPLIED",
			proposed: `{"a":1,"b":{"x":true,"y":[1,2]}}`,
			prior:    prior,
			received: "{\n  \"b\": { \"y\": [1, 2], \"x\": true },\n  \"a\": 1\n}",
			want:     dbsqlc.UpdateOutcomeAPPLIED,
		},
		{
			// A missing pre-update snapshot makes DECLINED structurally
			// undetectable. It must fall through to MODIFIED rather than
			// guessing -- the service logs prior_snapshot_missing when
			// this happens.
			name:     "no prior snapshot: an unchanged contract reads MODIFIED, not DECLINED",
			proposed: proposed, prior: "", received: prior,
			want: dbsqlc.UpdateOutcomeMODIFIED,
		},
		{
			name:     "empty received contract is never a match",
			proposed: proposed, prior: prior, received: "",
			want: dbsqlc.UpdateOutcomeMODIFIED,
		},
		{
			// Two unparseable documents must not compare equal to each
			// other and be reported as a definite decision.
			name:     "malformed JSON on both sides does not compare equal",
			proposed: `{"broken":`, prior: prior, received: `{"broken":`,
			want: dbsqlc.UpdateOutcomeMODIFIED,
		},
		{
			// JSON numbers all decode to float64, so 1 and 1.0 are the
			// same value -- this pins that as intentional, not incidental.
			name:     "numeric formatting differences are semantically equal",
			proposed: `{"qty":1}`, prior: prior, received: `{"qty":1.0}`,
			want: dbsqlc.UpdateOutcomeAPPLIED,
		},
		{
			// APPLIED is checked before DECLINED, so a no-op update that
			// the seller approved reads as APPLIED rather than DECLINED.
			// Both describe the same end state; APPLIED is the one that
			// matches what actually happened.
			name:     "no-op update approved by the seller reads APPLIED",
			proposed: prior, prior: prior, received: prior,
			want: dbsqlc.UpdateOutcomeAPPLIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyOutcome(
				json.RawMessage(tt.proposed),
				json.RawMessage(tt.prior),
				json.RawMessage(tt.received),
			)
			if got != tt.want {
				t.Fatalf("classifyOutcome() = %s, want %s", got, tt.want)
			}
		})
	}
}
