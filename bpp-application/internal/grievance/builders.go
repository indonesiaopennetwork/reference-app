package grievance

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// timeLayout is occurredAt's format: RFC 3339 with milliseconds, in WIB like
// the pack's examples.
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

var wib = time.FixedZone("WIB", 7*60*60)

// FormatTime formats a time for occurredAt, openedAt, decidedAt, ...
func FormatTime(t time.Time) string { return t.In(wib).Format(timeLayout) }

// NewID returns a grievance or proposal identifier, e.g. GRV-2026-3F9A1C2B.
func NewID(prefix string, now time.Time) string {
	return fmt.Sprintf("%s-%d-%s", prefix, now.In(wib).Year(), strings.ToUpper(uuid.NewString()[:8]))
}

// Opening is what OPENED says about a new grievance.
type Opening struct {
	GrievanceID string
	// Side is SideBuyer (BAP opens with /support) or SideSeller (BPP opens
	// with an unsolicited /on_support).
	Side         string
	NpID         string // the originating participant
	AssignedNpID string // the participant expected to respond
	Against      Subject
	Category     string
	Description  string
	Affected     []AffectedLine
	PolicyRef    string
	Related      []RelatedReference
}

// Open builds the OPENED event.
func Open(o Opening, now time.Time) *Grievance {
	at := FormatTime(now)
	policy := o.PolicyRef
	if policy == "" {
		policy = DefaultPolicyRef
	}
	against := o.Against
	return &Grievance{
		Context: Context, Type: Type,
		GrievanceID: o.GrievanceID, EventType: Opened, CaseStatus: StatusOpen,
		OccurredAt: at, OpenedAt: at, OriginatingNpID: o.NpID,
		RaisedBy:      &Party{Side: o.Side, NpID: o.NpID},
		RaisedAgainst: &against, AssignedNpID: o.AssignedNpID,
		Category: o.Category, Description: o.Description, Affected: o.Affected,
		PolicyRef: policy, ChannelMode: "ASYNC", RelatedReferences: o.Related,
	}
}

// Next builds the next event of the grievance with its base fields and the
// caseStatus it leaves the grievance in. Events about the active resolution
// repeat it in full; RESOLVED also carries the decision and every execution.
// The caller adds what is specific to the event: Resolution for
// RESOLUTION_PROPOSED, Decision, Execution, Explanation.
func (st *State) Next(eventType string, now time.Time) *Grievance {
	g := &Grievance{
		Context: Context, Type: Type,
		GrievanceID: st.GrievanceID, EventType: eventType, CaseStatus: st.CaseStatus,
		OccurredAt: FormatTime(now), OriginatingNpID: st.OriginatingNpID,
	}
	switch eventType {
	case ResolutionProposed, ResolutionDecided, ActionCompleted:
		g.CaseStatus = StatusInProgress
	case Resolved:
		g.CaseStatus = StatusResolved
	case Objection:
		g.CaseStatus = StatusOpen
	case Closed:
		g.CaseStatus = StatusClosed
	}
	if st.Resolution != nil && (eventType == ResolutionDecided || eventType == ActionCompleted || eventType == Resolved) {
		r := *st.Resolution
		g.Resolution = &r
	}
	if eventType == Resolved {
		if st.Decision != nil {
			d := *st.Decision
			g.Decision = &d
		}
		if st.Resolution != nil {
			var ex []ActionExecution
			for _, a := range st.Resolution.Actions {
				if e, ok := st.Executions[a.ActionID]; ok {
					ex = append(ex, e)
				}
			}
			if len(ex) > 0 {
				g.Execution = &Execution{ActionExecutions: ex}
			}
		}
	}
	return g
}

// Decide builds RESOLUTION_DECIDED on the active version.
func (st *State) Decide(npID string, accept bool, reason string, now time.Time) *Grievance {
	g := st.Next(ResolutionDecided, now)
	result := Rejected
	if accept {
		result = Accepted
	}
	g.Decision = &Decision{
		ProposalID: st.Resolution.ProposalID, Version: st.Resolution.Version,
		DecidedByNpID: npID, Result: result, DecidedAt: g.OccurredAt, Reason: reason,
	}
	return g
}

// Propose builds RESOLUTION_PROPOSED for actions: version 1 of a new
// proposal, or the next version of the active one.
func (st *State) Propose(npID, authorization string, actions []Action, now time.Time) *Grievance {
	g := st.Next(ResolutionProposed, now)
	r := &Resolution{
		ProposalID: NewID("RES", now), Version: 1, ProposedByNpID: npID,
		AuthorizationType: authorization, Actions: actions,
	}
	if st.Resolution != nil {
		r.ProposalID, r.Version = st.Resolution.ProposalID, st.Resolution.Version+1
	}
	if authorization == Unilateral {
		r.FundingNpID = npID
	}
	g.Resolution = r
	return g
}
