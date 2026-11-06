package grievance

import "time"

// DefaultPolicyRef is the grievance SLA policy used when the order's offers
// name none (offerAttributes.policies.grievanceSla.policyRef).
const DefaultPolicyRef = "ion://policy/grievance-sla.consumer.standard"

// SLAPolicy holds the targets of a grievance SLA policy.
type SLAPolicy struct {
	// FirstResponse is the target for ACKNOWLEDGED after OPENED.
	FirstResponse time.Duration
	// Resolution is the target for RESOLVED after OPENED.
	Resolution time.Duration
	// Objection is how long after RESOLVED the originator may object;
	// afterwards the grievance is deemed closed.
	Objection time.Duration
}

// policies are the figures of the registered grievance SLA policies, kept
// here until the ION policy registry (registry.ion.id/policies) publishes
// them in a machine-readable form.
var policies = map[string]SLAPolicy{
	DefaultPolicyRef: {FirstResponse: 2 * time.Hour, Resolution: 72 * time.Hour, Objection: 7 * 24 * time.Hour},
}

// Policy returns the SLA policy ref names, or the default one.
func Policy(ref string) SLAPolicy {
	if p, ok := policies[ref]; ok {
		return p
	}
	return policies[DefaultPolicyRef]
}

// State is what a grievance's events add up to.
type State struct {
	GrievanceID     string
	OriginatingNpID string
	OriginatingSide string // BUYER | SELLER
	// OriginSide is the side that opened it (SideBAP for a buyer grievance).
	OriginSide     string
	Category       string
	PolicyRef      string
	CaseStatus     string
	OpenedAt       time.Time
	AcknowledgedAt time.Time
	ResolvedAt     time.Time
	ClosedAt       time.Time
	LastEvent      string
	LastEventAt    time.Time

	// Resolution is the active version; Versions every version in order,
	// with the side that proposed each in ProposerSides.
	Resolution    *Resolution
	Versions      []Resolution
	ProposerSides []string
	// ProposerSide proposed the active version.
	ProposerSide string
	// Decisions holds every decision; Decision is the one on the active
	// version (nil while undecided).
	Decisions []Decision
	Decision  *Decision
	// Executions are the executed actions of the active version, by actionId.
	Executions map[string]ActionExecution
	Related    []RelatedReference
	// Objected is an OBJECTION not yet answered with a new version.
	Objected bool
}

// Apply returns the state after event g (already checked), sent by side.
func Apply(st *State, g *Grievance, side string) *State {
	at, _ := ParseTime(g.OccurredAt)
	if st == nil {
		opened, _ := ParseTime(g.OpenedAt)
		raisedBy := ""
		if g.RaisedBy != nil {
			raisedBy = g.RaisedBy.Side
		}
		st = &State{
			GrievanceID: g.GrievanceID, OriginatingNpID: g.OriginatingNpID, OriginatingSide: raisedBy,
			OriginSide: side, Category: g.Category, PolicyRef: g.PolicyRef, OpenedAt: opened,
			Executions: map[string]ActionExecution{},
		}
		if st.PolicyRef == "" {
			st.PolicyRef = DefaultPolicyRef
		}
	}
	next := *st
	next.CaseStatus = g.CaseStatus
	next.LastEvent = g.EventType
	next.LastEventAt = at
	next.Related = appendRefs(st.Related, g.RelatedReferences)

	switch g.EventType {
	case Acknowledged:
		if next.AcknowledgedAt.IsZero() {
			next.AcknowledgedAt = at
		}
	case ResolutionProposed:
		r := *g.Resolution
		next.Resolution = &r
		next.Versions = append(append([]Resolution{}, st.Versions...), r)
		next.ProposerSides = append(append([]string{}, st.ProposerSides...), side)
		next.ProposerSide = side
		next.Decision = nil
		next.Executions = map[string]ActionExecution{}
		next.Objected = false
	case ResolutionDecided:
		d := *g.Decision
		next.Decision = &d
		next.Decisions = append(append([]Decision{}, st.Decisions...), d)
	case Objection:
		next.ResolvedAt = time.Time{}
		next.Objected = true
	case Closed:
		next.ClosedAt = at
	}
	if g.Execution != nil {
		ex := make(map[string]ActionExecution, len(st.Executions))
		for k, v := range st.Executions {
			ex[k] = v
		}
		for _, e := range g.Execution.ActionExecutions {
			ex[e.ActionID] = e
		}
		next.Executions = ex
	}
	if g.EventType == Resolved {
		next.ResolvedAt = at
	}
	return &next
}

// Event is an event and the side that sent it.
type Event struct {
	Side      string
	Grievance *Grievance
}

// Fold replays a grievance's events in order.
func Fold(events []Event) *State {
	var st *State
	for _, e := range events {
		st = Apply(st, e.Grievance, e.Side)
	}
	return st
}

// Authorized reports whether the active resolution may be executed:
// accepted, or allowed by policy or by its funder without a decision.
func (st *State) Authorized() bool {
	if st.Resolution == nil {
		return false
	}
	if st.Resolution.AuthorizationType == ExplicitAgreement {
		return st.Decision != nil && st.Decision.Result == Accepted
	}
	return true
}

// Action returns the active resolution's action actionID, or nil.
func (r *Resolution) Action(actionID string) *Action {
	for i := range r.Actions {
		if r.Actions[i].ActionID == actionID {
			return &r.Actions[i]
		}
	}
	return nil
}

// Pending lists the actions of the active resolution that still need
// executing (any method but NONE, with no execution yet).
func (st *State) Pending() []Action {
	if st.Resolution == nil {
		return nil
	}
	var out []Action
	for _, a := range st.Resolution.Actions {
		if a.ExecutionMethod != MethodNone {
			if _, done := st.Executions[a.ActionID]; !done {
				out = append(out, a)
			}
		}
	}
	return out
}

// executedWith returns executionReference by actionId, counting both the
// recorded executions and the ones in ex.
func (st *State) executedWith(ex *Execution) map[string]string {
	done := map[string]string{}
	for id, e := range st.Executions {
		done[id] = e.ExecutionReference
	}
	if ex != nil {
		for _, e := range ex.ActionExecutions {
			done[e.ActionID] = e.ExecutionReference
		}
	}
	return done
}

// ObjectionDeadline is when a RESOLVED grievance is deemed closed.
func (st *State) ObjectionDeadline() time.Time {
	if st.ResolvedAt.IsZero() {
		return time.Time{}
	}
	return st.ResolvedAt.Add(Policy(st.PolicyRef).Objection)
}

// DeemedClosed reports a RESOLVED grievance past its objection period: both
// participants treat it as closed without another message.
func (st *State) DeemedClosed(now time.Time) bool {
	return st.CaseStatus == StatusResolved && !st.ResolvedAt.IsZero() && now.After(st.ObjectionDeadline())
}

func appendRefs(have, add []RelatedReference) []RelatedReference {
	out := append([]RelatedReference{}, have...)
	for _, r := range add {
		dup := false
		for _, h := range out {
			dup = dup || h == r
		}
		if !dup {
			out = append(out, r)
		}
	}
	return out
}

// Next-step actions.
const (
	StepAcknowledge = "ACKNOWLEDGE" // the responder acknowledges
	StepPropose     = "PROPOSE"     // a (new) resolution version is due
	StepDecide      = "DECIDE"      // the counterparty accepts or rejects the version
	StepExecute     = "EXECUTE"     // the proposer carries out the actions
	StepResolve     = "RESOLVE"     // every action is done: the proposer marks it RESOLVED
	StepConfirm     = "CONFIRM"     // RESOLVED: the originator may object or close
	StepNone        = "NONE"        // closed, or deemed closed
)

// Step is what the grievance waits for and from which side.
type Step struct {
	Action string `json:"action"`
	// Side is SideBAP or SideBPP ("" when closed).
	Side string `json:"side,omitempty"`
}

// NextStep says what happens next and which side it waits on.
func (st *State) NextStep(now time.Time) Step {
	responder := OtherSide(st.OriginSide)
	switch {
	case st.CaseStatus == StatusClosed || st.DeemedClosed(now):
		return Step{Action: StepNone}
	case st.CaseStatus == StatusResolved:
		return Step{Action: StepConfirm, Side: st.OriginSide}
	case st.Resolution == nil || st.Objected:
		if st.AcknowledgedAt.IsZero() && st.Resolution == nil {
			return Step{Action: StepAcknowledge, Side: responder}
		}
		return Step{Action: StepPropose, Side: responder}
	}
	proposer := st.ProposerSide
	switch {
	case st.Resolution.AuthorizationType == ExplicitAgreement && st.Decision == nil:
		return Step{Action: StepDecide, Side: OtherSide(proposer)}
	case st.Decision != nil && st.Decision.Result == Rejected:
		return Step{Action: StepPropose, Side: proposer}
	case len(st.Pending()) > 0:
		return Step{Action: StepExecute, Side: proposer}
	}
	return Step{Action: StepResolve, Side: proposer}
}
