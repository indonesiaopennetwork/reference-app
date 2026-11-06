package grievance

import (
	"fmt"
	"strings"
	"time"
)

// Endpoints. /support is always sent by the BAP and /on_support by the BPP,
// whichever side raised the grievance (rule 1).
const (
	EndpointSupport   = "support"
	EndpointOnSupport = "on_support"
)

// Sides of an order. A side is known from the endpoint alone (rule 1), even
// when one participant is both the order's BAP and its BPP (bapId ==
// bppId), so the rules tell the two parties apart by side, and check NP ids
// on top where they differ.
const (
	SideBAP = "BAP"
	SideBPP = "BPP"
)

// SideOf is the side that sends on endpoint.
func SideOf(endpoint string) string {
	if endpoint == EndpointOnSupport {
		return SideBPP
	}
	return SideBAP
}

// OtherSide is the other side.
func OtherSide(side string) string {
	if side == SideBAP {
		return SideBPP
	}
	return SideBAP
}

// Sender is who sent an event: the endpoint it came on and the order's BAP
// and BPP (context.bapId / context.bppId).
type Sender struct {
	Endpoint string
	BapID    string
	BppID    string
}

// NpID is the participant that sent the message.
func (s Sender) NpID() string {
	if s.Endpoint == EndpointOnSupport {
		return s.BppID
	}
	return s.BapID
}

// Side is the side that sent the message.
func (s Sender) Side() string { return SideOf(s.Endpoint) }

// RuleError is an event that breaks the pack's rules; it is answered with a
// NACK (inbound) or refused before sending (outbound).
type RuleError struct{ Msg string }

func (e *RuleError) Error() string { return e.Msg }

func fail(format string, a ...any) error { return &RuleError{Msg: fmt.Sprintf(format, a...)} }

// Check validates event g, sent by from, against the grievance's state st
// (nil for a grievance not seen yet). It covers what JSON Schema checks per
// event and the rules the pack README lists under "Rules enforced by ONIX".
func Check(g *Grievance, st *State, from Sender, now time.Time) error {
	if err := checkShape(g); err != nil {
		return err
	}
	sender := from.NpID()

	if g.EventType == Opened {
		if st != nil {
			return fail("grievance %s is already open", g.GrievanceID)
		}
		if g.OriginatingNpID != sender || g.RaisedBy.NpID != sender {
			return fail("OPENED must be sent by the originating participant (%s)", sender)
		}
		switch from.Endpoint {
		case EndpointSupport:
			if g.RaisedBy.Side != SideBuyer {
				return fail("a grievance opened with /support is a buyer grievance (raisedBy.side BUYER)")
			}
		case EndpointOnSupport:
			if g.RaisedBy.Side != SideSeller {
				return fail("a grievance opened with an unsolicited /on_support is a seller grievance (raisedBy.side SELLER)")
			}
		}
		return checkResolutionLinks(g, g.RelatedReferences)
	}

	if st == nil {
		return fail("unknown grievance %s", g.GrievanceID)
	}
	if g.OriginatingNpID != st.OriginatingNpID {
		return fail("originatingNpId never changes (%s)", st.OriginatingNpID)
	}
	if st.CaseStatus == StatusClosed {
		return fail("grievance %s is closed", g.GrievanceID)
	}
	originator := from.Side() == st.OriginSide

	switch g.EventType {
	case Acknowledged:
		if originator {
			return fail("only the responding participant acknowledges a grievance")
		}
	case Objection, Closed:
		// Rule 10.
		if !originator {
			return fail("only the originating participant (%s) sends %s", st.OriginatingNpID, g.EventType)
		}
		if g.EventType == Objection {
			if st.CaseStatus != StatusResolved {
				return fail("an objection follows a RESOLVED grievance")
			}
			if st.DeemedClosed(now) {
				return fail("the objection period of %s is over", st.PolicyRef)
			}
		}
	case ResolutionProposed:
		r := g.Resolution
		if r.ProposedByNpID != sender {
			return fail("resolution.proposedByNpId must be the sender (%s)", sender)
		}
		if st.CaseStatus == StatusResolved {
			return fail("a RESOLVED grievance takes a new proposal only after an objection")
		}
		if last := st.Resolution; last != nil {
			if r.ProposalID != last.ProposalID {
				return fail("a new version replaces proposal %s: proposalId must stay the same", last.ProposalID)
			}
			if r.Version <= last.Version {
				return fail("version %d is not newer than the active version %d", r.Version, last.Version)
			}
		}
	case ResolutionDecided:
		if err := checkLatest(g.Resolution, st); err != nil {
			return err
		}
		d := g.Decision
		// Rule 4.
		if d.ProposalID != st.Resolution.ProposalID || d.Version != st.Resolution.Version {
			return fail("decision on %s v%d, but the latest version is %s v%d",
				d.ProposalID, d.Version, st.Resolution.ProposalID, st.Resolution.Version)
		}
		// Rule 5.
		if from.Side() == st.ProposerSide || (from.BapID != from.BppID && d.DecidedByNpID == st.Resolution.ProposedByNpID) {
			return fail("the proposer cannot decide on its own proposal")
		}
		if d.DecidedByNpID != sender {
			return fail("decision.decidedByNpId must be the sender (%s)", sender)
		}
		if st.Resolution.AuthorizationType != ExplicitAgreement {
			return fail("only an EXPLICIT_AGREEMENT resolution is decided")
		}
		if st.Decision != nil {
			return fail("version %d is already decided (%s)", st.Decision.Version, st.Decision.Result)
		}
	case ActionCompleted:
		if err := checkLatest(g.Resolution, st); err != nil {
			return err
		}
		if !st.Authorized() {
			return fail("the resolution is not accepted or authorized yet")
		}
		for _, ex := range g.Execution.ActionExecutions {
			// Rule 7.
			a := st.Resolution.Action(ex.ActionID)
			if a == nil {
				return fail("execution of unknown action %s", ex.ActionID)
			}
			if a.ExecutionMethod != MethodNone && ex.ExecutionReference == "" {
				return fail("action %s needs an executionReference", ex.ActionID)
			}
		}
	case Resolved:
		if err := checkLatest(g.Resolution, st); err != nil {
			return err
		}
		// Rule 8.
		if st.Resolution.AuthorizationType == ExplicitAgreement && (st.Decision == nil || st.Decision.Result != Accepted) {
			return fail("an EXPLICIT_AGREEMENT resolution must be ACCEPTED before RESOLVED")
		}
		done := st.executedWith(g.Execution)
		for _, a := range st.Resolution.Actions {
			if a.ExecutionMethod != MethodNone && done[a.ActionID] == "" {
				return fail("RESOLVED without an executionReference for action %s", a.ActionID)
			}
		}
	case StatusChanged:
	}

	if g.Resolution != nil {
		return checkResolutionLinks(g, append(append([]RelatedReference{}, st.Related...), g.RelatedReferences...))
	}
	return nil
}

// checkShape covers what the pack's JSON Schema checks on one event.
func checkShape(g *Grievance) error {
	if g.Context != Context || g.Type != Type {
		return fail("supportAttributes must be @type %s with @context %s", Type, Context)
	}
	if g.GrievanceID == "" || g.OriginatingNpID == "" || g.OccurredAt == "" {
		return fail("grievanceId, occurredAt and originatingNpId are required")
	}
	if !oneOf(g.CaseStatus, StatusOpen, StatusInProgress, StatusResolved, StatusClosed) {
		return fail("unknown caseStatus %q", g.CaseStatus)
	}
	if g.ChannelMode != "" && !oneOf(g.ChannelMode, "LIVE", "URGENT", "ASYNC", "EMERGENCY") {
		return fail("unknown channelMode %q", g.ChannelMode)
	}
	if len(g.Description) > 1500 || len(g.Explanation) > 2000 {
		return fail("description is at most 1500 characters, explanation 2000")
	}
	for _, a := range g.Affected {
		if a.CommitmentID == "" && a.PerformanceID == "" {
			return fail("an affected line names a commitmentId or performanceId")
		}
	}
	for _, r := range g.RelatedReferences {
		if !oneOf(r.RefType, "CONTRACT", "PERFORMANCE", "SETTLEMENT", "RECONCILIATION", "DISPUTE", "EVIDENCE", "OTHER") || r.RefID == "" {
			return fail("relatedReferences entry %q/%q is invalid", r.RefType, r.RefID)
		}
	}
	switch g.EventType {
	case Opened:
		if g.OpenedAt == "" || g.RaisedBy == nil || g.RaisedAgainst == nil || g.Category == "" || g.Description == "" {
			return fail("OPENED needs openedAt, raisedBy, raisedAgainst, category and description")
		}
		if g.CaseStatus != StatusOpen {
			return fail("OPENED leaves the grievance OPEN")
		}
		if !oneOf(g.RaisedBy.Side, SideBuyer, SideSeller) || g.RaisedBy.NpID == "" {
			return fail("raisedBy needs side (BUYER or SELLER) and npId")
		}
		if !oneOf(g.RaisedAgainst.Role, "BUYER", "SELLER", "BAP", "BPP", "LOGISTICS", "PAYMENT", "OTHER") {
			return fail("unknown raisedAgainst.role %q", g.RaisedAgainst.Role)
		}
		if LinkedCategories[g.Category] && !links(g.RelatedReferences) {
			return fail("category %s must link the contract, return performance or refund settlement in relatedReferences", g.Category)
		}
	case ResolutionProposed, ResolutionDecided, ActionCompleted, Resolved:
		if g.Resolution == nil {
			return fail("%s needs resolution", g.EventType)
		}
		if err := checkResolution(g.Resolution); err != nil {
			return err
		}
		if g.EventType == ResolutionDecided {
			d := g.Decision
			if d == nil || d.ProposalID == "" || d.Version < 1 || d.DecidedByNpID == "" || d.DecidedAt == "" || !oneOf(d.Result, Accepted, Rejected) {
				return fail("RESOLUTION_DECIDED needs a decision (proposalId, version, decidedByNpId, result, decidedAt)")
			}
		}
		if g.EventType == ActionCompleted && (g.Execution == nil || len(g.Execution.ActionExecutions) == 0) {
			return fail("ACTION_COMPLETED needs execution")
		}
		if g.EventType == Resolved && g.CaseStatus != StatusResolved {
			return fail("RESOLVED leaves the grievance RESOLVED")
		}
	case Objection:
		if g.Explanation == "" || !oneOf(g.CaseStatus, StatusOpen, StatusInProgress) {
			return fail("OBJECTION needs an explanation and reopens the grievance (OPEN or IN_PROGRESS)")
		}
	case Closed:
		if g.CaseStatus != StatusClosed {
			return fail("CLOSED leaves the grievance CLOSED")
		}
	case Acknowledged, StatusChanged:
	default:
		return fail("unknown eventType %q", g.EventType)
	}
	if g.Execution != nil {
		for _, ex := range g.Execution.ActionExecutions {
			if ex.ActionID == "" || ex.Status != "COMPLETED" || ex.CompletedAt == "" || ex.ExecutedByNpID == "" {
				return fail("an action execution needs actionId, status COMPLETED, completedAt and executedByNpId")
			}
		}
	}
	return nil
}

func checkResolution(r *Resolution) error {
	if r.ProposalID == "" || r.Version < 1 || r.ProposedByNpID == "" || len(r.Actions) == 0 {
		return fail("resolution needs proposalId, version, proposedByNpId and actions")
	}
	basis := map[string]string{ExplicitAgreement: "EXPLICIT", PolicyAuthorized: "POLICY", Unilateral: "UNILATERAL"}[r.AuthorizationType]
	switch r.AuthorizationType {
	case PolicyAuthorized:
		v := r.Verification
		if r.PolicyRef == "" || v == nil || v.VerifiedByNpID == "" || v.VerificationReference == "" || v.VerifiedAt == "" || v.Method == "" {
			return fail("a POLICY_AUTHORIZED resolution needs policyRef and verification")
		}
	case Unilateral:
		if r.FundingNpID == "" {
			return fail("a UNILATERAL resolution needs fundingNpId")
		}
		if r.FundingNpID != r.ProposedByNpID {
			return fail("a UNILATERAL resolution is funded by its proposer")
		}
	case ExplicitAgreement:
	default:
		return fail("unknown authorizationType %q", r.AuthorizationType)
	}
	ids := map[string]bool{}
	for _, a := range r.Actions {
		if a.ActionID == "" || a.Description == "" || ids[a.ActionID] {
			return fail("every action needs a unique actionId and a description")
		}
		ids[a.ActionID] = true
		if !oneOf(a.Type, "REFUND", "REPLACEMENT", "REDELIVERY", "CANCELLATION", "ADJUSTMENT", "INFORMATION_PROVIDED", "NO_ACTION", "WITHDRAWN", "OTHER") {
			return fail("unknown action type %q", a.Type)
		}
		if !oneOf(a.ExecutionMethod, "CONTRACT_UPDATE", "SETTLEMENT", "PERFORMANCE", MethodNone, "OTHER") {
			return fail("unknown executionMethod %q", a.ExecutionMethod)
		}
		f := a.FinancialEffect
		if f == nil {
			continue
		}
		if !oneOf(f.EffectType, "REFUND", "ADJUSTMENT", "EXTRA_CHARGE", "CREDIT") || f.Amount < 0 || f.Currency != Currency {
			return fail("action %s: financialEffect needs effectType, a non-negative amount and currency IDR", a.ActionID)
		}
		// Rule 6.
		if f.AllocationBasis != basis {
			return fail("action %s: allocationBasis %s does not match %s (%s)", a.ActionID, f.AllocationBasis, r.AuthorizationType, basis)
		}
		if f.AllocationBasis == "POLICY" && f.PolicyRef == "" {
			return fail("action %s: a POLICY allocation needs policyRef", a.ActionID)
		}
		if f.AllocationBasis == "UNILATERAL" && f.LiablePartyID != r.FundingNpID {
			return fail("action %s: a unilateral remedy is borne by its funder (%s), never the counterparty", a.ActionID, r.FundingNpID)
		}
	}
	return nil
}

// checkResolutionLinks is rule 9: with a SETTLEMENT in relatedReferences,
// every REFUND effect states settlementRef.
func checkResolutionLinks(g *Grievance, related []RelatedReference) error {
	if g.Resolution == nil {
		return nil
	}
	hasSettlement := false
	for _, r := range related {
		hasSettlement = hasSettlement || r.RefType == "SETTLEMENT"
	}
	if !hasSettlement {
		return nil
	}
	for _, a := range g.Resolution.Actions {
		if f := a.FinancialEffect; f != nil && f.EffectType == "REFUND" && f.SettlementRef == "" {
			return fail("action %s: a refund on a linked settlement needs settlementRef", a.ActionID)
		}
	}
	return nil
}

// checkLatest: decisions, executions and RESOLVED are about the active
// version, which the message repeats in full.
func checkLatest(r *Resolution, st *State) error {
	if st.Resolution == nil {
		return fail("no resolution has been proposed")
	}
	if r.ProposalID != st.Resolution.ProposalID || r.Version != st.Resolution.Version {
		return fail("resolution %s v%d is not the active version (%s v%d)", r.ProposalID, r.Version, st.Resolution.ProposalID, st.Resolution.Version)
	}
	return nil
}

func links(refs []RelatedReference) bool {
	for _, r := range refs {
		if oneOf(r.RefType, "CONTRACT", "PERFORMANCE", "SETTLEMENT") {
			return true
		}
	}
	return false
}

func oneOf(s string, set ...string) bool {
	for _, v := range set {
		if s == v {
			return true
		}
	}
	return false
}

// ParseTime reads an occurredAt-style date-time.
func ParseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
}
