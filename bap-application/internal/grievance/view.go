package grievance

import (
	"encoding/json"
	"fmt"
	"time"
)

// Parties in a View, from the viewing participant's side.
const (
	Me   = "ME"
	Them = "THEM"
)

// View is a grievance as the apps' frontends show it, from one
// participant's side: who did what is ME or THEM, and Next says what the
// grievance waits for and from whom.
type View struct {
	ID            string             `json:"id"`
	TransactionID string             `json:"transactionId"`
	OrderID       string             `json:"orderId"`
	RaisedBy      string             `json:"raisedBy"` // BUYER | SELLER
	OpenedBy      string             `json:"openedBy"` // ME | THEM
	Category      string             `json:"category"`
	Description   string             `json:"description"`
	Affected      []AffectedLine     `json:"affected"`
	Related       []RelatedReference `json:"related"`
	PolicyRef     string             `json:"policyRef"`
	// CaseStatus is the protocol status; DeemedClosed marks a RESOLVED one
	// past its objection period.
	CaseStatus   string `json:"caseStatus"`
	DeemedClosed bool   `json:"deemedClosed"`
	Next         Next   `json:"next"`
	NeedsAction  bool   `json:"needsAction"`
	Unread       bool   `json:"unread"`
	// Offer is the active resolution version; History the earlier ones,
	// newest first.
	Offer    *OfferView        `json:"offer"`
	History  []OfferView       `json:"history"`
	Timeline []TimelineEntry   `json:"timeline"`
	Channels []json.RawMessage `json:"channels" swaggertype:"array,object"`
	// LastSent is the latest event this participant sent, so the UI can
	// tell "sent" (ACK) from "answered".
	LastSent *TimelineEntry `json:"lastSent"`

	OpenedAt          time.Time  `json:"openedAt"`
	LastEventAt       time.Time  `json:"lastEventAt"`
	AcknowledgedAt    *time.Time `json:"acknowledgedAt"`
	ResolvedAt        *time.Time `json:"resolvedAt"`
	ClosedAt          *time.Time `json:"closedAt"`
	FirstResponseDue  *time.Time `json:"firstResponseDue"`
	ResolutionDue     *time.Time `json:"resolutionDue"`
	ObjectionDeadline *time.Time `json:"objectionDeadline"`
}

// Next is what the grievance waits for.
type Next struct {
	Action    string `json:"action"`    // Step* constants
	WaitingOn string `json:"waitingOn"` // ME | THEM | "" (closed)
}

// OfferView is a resolution version.
type OfferView struct {
	ProposalID    string        `json:"proposalId"`
	Version       int           `json:"version"`
	Authorization string        `json:"authorization"` // EXPLICIT_AGREEMENT | POLICY_AUTHORIZED | UNILATERAL
	ProposedBy    string        `json:"proposedBy"`    // ME | THEM
	FundedBy      string        `json:"fundedBy,omitempty"`
	PolicyRef     string        `json:"policyRef,omitempty"`
	Verification  *Verification `json:"verification,omitempty"`
	Actions       []ActionView  `json:"actions"`
	Total         float64       `json:"total"`
	Decision      *DecisionView `json:"decision"`
	ProposedAt    time.Time     `json:"proposedAt"`
}

// ActionView is a remedy action with its execution.
type ActionView struct {
	ActionID           string         `json:"actionId"`
	Type               string         `json:"type"`
	Description        string         `json:"description"`
	ExecutionMethod    string         `json:"executionMethod"`
	Affected           []AffectedLine `json:"affected"`
	Amount             float64        `json:"amount"`
	Currency           string         `json:"currency,omitempty"`
	EffectType         string         `json:"effectType,omitempty"`
	Liable             string         `json:"liable,omitempty"` // ME | THEM
	SettlementRef      string         `json:"settlementRef,omitempty"`
	Executed           bool           `json:"executed"`
	ExecutionReference string         `json:"executionReference,omitempty"`
	CompletedAt        string         `json:"completedAt,omitempty"`
}

// DecisionView is the decision on a version.
type DecisionView struct {
	Result string `json:"result"` // ACCEPTED | REJECTED
	By     string `json:"by"`     // ME | THEM
	Reason string `json:"reason,omitempty"`
	At     string `json:"at"`
}

// TimelineEntry is one event.
type TimelineEntry struct {
	At        time.Time `json:"at"`
	EventType string    `json:"eventType"`
	By        string    `json:"by"`       // ME | THEM
	Delivery  string    `json:"delivery"` // SENDING | ACK | NACK | FAILED
	Error     string    `json:"error,omitempty"`
	Note      string    `json:"note,omitempty"`
	Version   int       `json:"version,omitempty"`
	Result    string    `json:"result,omitempty"`
}

// BuildView builds rec's view for side (SideBAP or SideBPP).
func BuildView(rec *Record, events []EventRecord, side string, now time.Time) View {
	who := func(s string) string {
		if s == side {
			return Me
		}
		return Them
	}
	origin := SideBAP
	if rec.OriginatingSide == SideSeller {
		origin = SideBPP
	}
	v := View{
		ID: rec.GrievanceID, TransactionID: rec.TransactionID.String(), OrderID: rec.OrderID,
		RaisedBy: rec.OriginatingSide, OpenedBy: who(origin),
		Category: rec.Category, Description: rec.Description,
		Affected: nonNil(rec.Affected), Related: nonNilRefs(rec.Related), PolicyRef: rec.PolicyRef,
		CaseStatus: rec.CaseStatus, Channels: []json.RawMessage{}, History: []OfferView{}, Timeline: []TimelineEntry{},
		OpenedAt: rec.OpenedAt, LastEventAt: rec.LastEventAt,
		AcknowledgedAt: rec.AcknowledgedAt, ResolvedAt: rec.ResolvedAt, ClosedAt: rec.ClosedAt,
	}
	if len(rec.Channels) > 0 {
		_ = json.Unmarshal(rec.Channels, &v.Channels)
	}
	st := Replay(events)
	if st == nil {
		return v
	}
	v.DeemedClosed = st.DeemedClosed(now)
	step := st.NextStep(now)
	v.Next = Next{Action: step.Action}
	if step.Side != "" {
		v.Next.WaitingOn = who(step.Side)
	}
	v.NeedsAction = v.Next.WaitingOn == Me && step.Action != StepConfirm && step.Action != StepNone
	v.Unread = rec.SeenAt == nil || rec.LastEventAt.After(*rec.SeenAt)

	policy := Policy(rec.PolicyRef)
	if rec.AcknowledgedAt == nil && rec.CaseStatus == StatusOpen {
		t := rec.OpenedAt.Add(policy.FirstResponse)
		v.FirstResponseDue = &t
	}
	if rec.CaseStatus == StatusOpen || rec.CaseStatus == StatusInProgress {
		t := rec.OpenedAt.Add(policy.Resolution)
		v.ResolutionDue = &t
	}
	if d := st.ObjectionDeadline(); !d.IsZero() && rec.CaseStatus == StatusResolved {
		v.ObjectionDeadline = &d
	}

	proposedAt := map[string]time.Time{}
	for _, e := range events {
		entry := TimelineEntry{
			At: e.OccurredAt, EventType: e.EventType, By: Them, Delivery: e.Delivery, Error: e.Error,
		}
		if e.Direction == "OUTBOUND" {
			entry.By = Me
		}
		g := e.Event
		switch {
		case g.EventType == Opened:
			entry.Note = g.Description
		case g.Decision != nil && g.EventType == ResolutionDecided:
			entry.Note, entry.Result, entry.Version = g.Decision.Reason, g.Decision.Result, g.Decision.Version
		case g.Resolution != nil && g.EventType == ResolutionProposed:
			entry.Version = g.Resolution.Version
			if e.Delivery == DeliverySending || e.Delivery == DeliveryAck {
				proposedAt[versionKey(g.Resolution)] = e.OccurredAt
			}
		case g.Explanation != "":
			entry.Note = g.Explanation
		}
		v.Timeline = append(v.Timeline, entry)
		if e.Direction == "OUTBOUND" {
			last := entry
			v.LastSent = &last
		}
	}

	// partyOf is the side of an NP id named in a resolution; when one
	// participant is both BAP and BPP the id cannot tell, and fallback (the
	// proposer, who bears its own remedies here) is used.
	partyOf := func(np, fallback string) string {
		switch {
		case rec.BapID == rec.BppID:
			return fallback
		case np == rec.BapID:
			return SideBAP
		case np == rec.BppID:
			return SideBPP
		}
		return fallback
	}
	offer := func(r Resolution, proposer string, active bool) OfferView {
		o := OfferView{
			ProposalID: r.ProposalID, Version: r.Version, Authorization: r.AuthorizationType,
			ProposedBy: who(proposer), PolicyRef: r.PolicyRef, Verification: r.Verification,
			Actions: []ActionView{}, ProposedAt: proposedAt[versionKey(&r)],
		}
		if r.FundingNpID != "" {
			o.FundedBy = who(proposer)
		}
		for _, d := range st.Decisions {
			if d.ProposalID == r.ProposalID && d.Version == r.Version {
				o.Decision = &DecisionView{Result: d.Result, By: who(OtherSide(proposer)), Reason: d.Reason, At: d.DecidedAt}
			}
		}
		for _, a := range r.Actions {
			av := ActionView{
				ActionID: a.ActionID, Type: a.Type, Description: a.Description,
				ExecutionMethod: a.ExecutionMethod, Affected: nonNil(a.Affected),
			}
			if f := a.FinancialEffect; f != nil {
				av.Amount, av.Currency, av.EffectType, av.SettlementRef = f.Amount, f.Currency, f.EffectType, f.SettlementRef
				if f.LiablePartyID != "" {
					av.Liable = who(partyOf(f.LiablePartyID, proposer))
				}
				o.Total += f.Amount
			}
			if active {
				if ex, ok := st.Executions[a.ActionID]; ok {
					av.Executed, av.ExecutionReference, av.CompletedAt = true, ex.ExecutionReference, ex.CompletedAt
				}
			}
			o.Actions = append(o.Actions, av)
		}
		return o
	}
	for i := len(st.Versions) - 1; i >= 0; i-- {
		r := st.Versions[i]
		proposer := st.ProposerSides[i]
		if st.Resolution != nil && r.ProposalID == st.Resolution.ProposalID && r.Version == st.Resolution.Version {
			o := offer(r, proposer, true)
			v.Offer = &o
			continue
		}
		v.History = append(v.History, offer(r, proposer, false))
	}
	return v
}

func versionKey(r *Resolution) string { return fmt.Sprintf("%s#%d", r.ProposalID, r.Version) }
