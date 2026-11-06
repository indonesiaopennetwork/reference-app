// Package grievancesvc is the BPP side of order-level grievances (ION
// Grievance v1, see internal/grievance). A buyer grievance arrives on
// /api/webhook/support and is acknowledged with its direct /on_support
// callback; the seller then proposes a resolution, executes it through the
// order's own flows (a refund settlement or a redelivery performance, sent
// to the BAP with an unsolicited on_update) and resolves it. The seller can
// also raise a grievance itself (an unsolicited on_support), e.g. about a
// wrong item returned. Every event goes out as /on_support through onix-bpp.
package grievancesvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/cancellation"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/grievance"
	"github.com/ion/winroom/bpp/internal/httpclient"
)

// defaultReturnPolicyRef is the returns policy a POLICY_AUTHORIZED refund
// cites when the order's offers name none (policies.returns.policyRef);
// their window and method are still the ones the offers state.
const defaultReturnPolicyRef = "ion://policy/return.standard"

// qcMethod is how a return was verified for a POLICY_AUTHORIZED refund.
const qcMethod = "QUALITY_CHECK_OF_RETURNED_ITEM"

// Service runs the BPP's grievances.
type Service struct {
	pool    *pgxpool.Pool
	cfg     *config.Config
	lh      *logharbour.Logger
	store   *grievance.Store
	client  *http.Client
	now     func() time.Time
	autoAck bool
}

// NewService returns a Service. Buyer grievances are acknowledged as soon
// as they arrive unless GRIEVANCE_AUTO_ACKNOWLEDGE is false; then the
// seller acknowledges from the dashboard.
func NewService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Service {
	return &Service{
		pool: pool, cfg: cfg, lh: lh, store: &grievance.Store{Pool: pool},
		client: httpclient.New(30 * time.Second), now: time.Now, autoAck: cfg.GrievanceAutoAcknowledge,
	}
}

// Errors the handlers map to HTTP statuses.
var (
	ErrNotFound   = errors.New("not found")
	ErrBadRequest = errors.New("bad request")
)

func badRequest(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadRequest, fmt.Sprintf(format, a...))
}

// orderInfo is the order a grievance is about.
type orderInfo struct {
	contractID uuid.UUID
	providerID string
	header     grievance.Header
	contract   map[string]any
	order      grievance.Order
}

func (s *Service) loadOrderBy(ctx context.Context, where string, arg any) (*orderInfo, error) {
	var (
		o        orderInfo
		txID     uuid.UUID
		provider *string
		network  *string
		attrs    []byte
		bapID    string
		bapURI   string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, transaction_id, provider_id, bap_id, bap_uri, network_id, contract_attributes
		FROM contracts WHERE `+where+` AND bpp_id = $2
		ORDER BY created_at DESC LIMIT 1`, arg, s.cfg.BppID).
		Scan(&o.contractID, &txID, &provider, &bapID, &bapURI, &network, &attrs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if provider != nil {
		o.providerID = *provider
	}
	o.contract, err = cancellation.LatestContract(ctx, s.pool, txID, attrs)
	if err != nil {
		return nil, err
	}
	o.order = grievance.ReadOrder(o.contract)
	if o.order.ContractID == "" {
		o.order.ContractID = o.contractID.String()
	}
	o.header = grievance.Header{
		TransactionID: txID, OrderID: o.order.ContractID,
		BapID: bapID, BapURI: bapURI, BppID: s.cfg.BppID, BppURI: s.cfg.BppURI,
		Extra: map[string]any{"contract_id": o.contractID, "provider_id": provider},
	}
	if network != nil {
		o.header.NetworkID = *network
	}
	return &o, nil
}

func (s *Service) orderByTxn(ctx context.Context, txID uuid.UUID) (*orderInfo, error) {
	return s.loadOrderBy(ctx, "transaction_id = $1", txID)
}

func (s *Service) orderByContract(ctx context.Context, contractID uuid.UUID) (*orderInfo, error) {
	return s.loadOrderBy(ctx, "id = $1", contractID)
}

// load returns a grievance on one of the seller's orders, with its state.
func (s *Service) load(ctx context.Context, provider, id string) (*grievance.Record, []grievance.EventRecord, *grievance.State, error) {
	var owner *string
	err := s.pool.QueryRow(ctx, `SELECT provider_id FROM grievances WHERE grievance_id = $1`, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (owner == nil || *owner != provider)) {
		return nil, nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, nil, err
	}
	rec, events, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	return rec, events, grievance.Replay(events), nil
}

// Receive handles an inbound /support: checks the grievance event against
// the grievance's state and stores it. Any error is answered with NACK.
// ok is false for a plain Beckn support request (no supportAttributes).
func (s *Service) Receive(ctx context.Context, msg *grievance.Message) (ok bool, err error) {
	c := msg.Context
	if c.BppID != s.cfg.BppID {
		return true, &grievance.RuleError{Msg: "context.bppId is not this BPP"}
	}
	g, ok, err := msg.Message.Support.Parse()
	if err != nil {
		return true, &grievance.RuleError{Msg: "supportAttributes is not an IONGrievance: " + err.Error()}
	}
	if !ok {
		return false, nil
	}
	txID, err := uuid.Parse(c.TransactionID)
	if err != nil {
		return true, &grievance.RuleError{Msg: "context.transactionId is not the order's transaction"}
	}
	msgID, err := uuid.Parse(c.MessageID)
	if err != nil {
		return true, &grievance.RuleError{Msg: "context.messageId is not a UUID"}
	}
	h := grievance.Header{
		TransactionID: txID, OrderID: msg.Message.Support.OrderID,
		BapID: c.BapID, BapURI: c.BapURI, BppID: c.BppID, BppURI: c.BppURI, NetworkID: c.NetworkID,
	}
	if g.EventType == grievance.Opened {
		o, err := s.orderByTxn(ctx, txID)
		if err != nil {
			return true, &grievance.RuleError{Msg: "unknown order"}
		}
		if o.header.BapID != c.BapID {
			return true, &grievance.RuleError{Msg: "context.bapId is not the order's BAP"}
		}
		h.Extra = o.header.Extra
		if h.OrderID == "" {
			h.OrderID = o.header.OrderID
		}
		if g.Category == grievance.WrongReturn {
			return true, &grievance.RuleError{Msg: "WRONG_RETURN is a seller grievance"}
		}
	}
	from := grievance.Sender{Endpoint: grievance.EndpointSupport, BapID: c.BapID, BppID: c.BppID}
	return true, s.store.Receive(ctx, h, from, msgID, g, nil, s.now())
}

// React does what the BPP does on its own after accepting an event: with
// auto-acknowledge on, a buyer grievance is acknowledged at once with the
// direct callback of its OPENED (same messageId).
func (s *Service) React(ctx context.Context, msg *grievance.Message) {
	g, ok, _ := msg.Message.Support.Parse()
	if !ok || g == nil || g.EventType != grievance.Opened || !s.autoAck {
		return
	}
	rec, events, err := s.store.Get(ctx, g.GrievanceID)
	if err != nil {
		return
	}
	st := grievance.Replay(events)
	if err := s.send(ctx, rec.Header, st.Next(grievance.Acknowledged, s.now()), grievance.OpeningMessageID(events)); err != nil {
		s.lh.WithModule("grievancesvc").Warn().Error(err).Log("automatic acknowledgement of " + g.GrievanceID + " failed")
	}
}

// Acknowledge acknowledges a buyer grievance (the direct callback of its
// OPENED, so it repeats that messageId).
func (s *Service) Acknowledge(ctx context.Context, provider, id string) error {
	rec, events, st, err := s.load(ctx, provider, id)
	if err != nil {
		return err
	}
	return s.send(ctx, rec.Header, st.Next(grievance.Acknowledged, s.now()), grievance.OpeningMessageID(events))
}

// ProposalRequest is the seller's resolution.
type ProposalRequest struct {
	// Route is who allows it: EXPLICIT (the buyer accepts), POLICY (the
	// order's return policy, with the QC of a returned item) or UNILATERAL
	// (the seller bears it without asking).
	Route string `json:"route" binding:"required" enums:"EXPLICIT,POLICY,UNILATERAL"`
	// QCReference is the approved return whose QC a POLICY route rests on.
	QCReference string           `json:"qcReference"`
	Actions     []ProposalAction `json:"actions" binding:"required"`
}

// ProposalAction is one remedy.
type ProposalAction struct {
	Kind         string  `json:"kind" binding:"required" enums:"REFUND_FULL,REFUND_PARTIAL,REPLACEMENT,REDELIVERY,INFORMATION,NO_ACTION"`
	CommitmentID string  `json:"commitmentId"`
	Quantity     int     `json:"quantity"`
	Amount       float64 `json:"amount"`
	Description  string  `json:"description"`
}

// routes map a ProposalRequest.Route to its authorizationType and the
// allocationBasis its money effects carry (they must match, rule 6).
var routes = map[string]struct{ auth, basis string }{
	"EXPLICIT":   {grievance.ExplicitAgreement, "EXPLICIT"},
	"POLICY":     {grievance.PolicyAuthorized, "POLICY"},
	"UNILATERAL": {grievance.Unilateral, "UNILATERAL"},
}

var kinds = map[string]struct{ typ, method, desc string }{
	"REFUND_FULL":    {"REFUND", "SETTLEMENT", "Full refund"},
	"REFUND_PARTIAL": {"REFUND", "SETTLEMENT", "Partial refund"},
	"REPLACEMENT":    {"REPLACEMENT", "PERFORMANCE", "Replacement item"},
	"REDELIVERY":     {"REDELIVERY", "PERFORMANCE", "Send the item again"},
	"INFORMATION":    {"INFORMATION_PROVIDED", grievance.MethodNone, "Explanation"},
	"NO_ACTION":      {"NO_ACTION", grievance.MethodNone, "No action"},
}

// Propose sends a resolution version: version 1, or the next one after a
// rejection or objection.
func (s *Service) Propose(ctx context.Context, provider, id string, req ProposalRequest) error {
	rec, _, st, err := s.load(ctx, provider, id)
	if err != nil {
		return err
	}
	o, err := s.orderByTxn(ctx, rec.TransactionID)
	if err != nil {
		return err
	}
	route, ok := routes[req.Route]
	if !ok {
		return badRequest("route is EXPLICIT, POLICY or UNILATERAL")
	}
	auth, basis := route.auth, route.basis
	if len(req.Actions) == 0 {
		return badRequest("add at least one action")
	}
	returnPolicy := o.order.ReturnPolicyRef
	if returnPolicy == "" {
		returnPolicy = defaultReturnPolicyRef
	}
	linked := ""
	for _, r := range rec.Related {
		if r.RefType == "SETTLEMENT" {
			linked = r.RefID
		}
	}
	now := s.now()
	suffix := strings.ToLower(id[strings.LastIndex(id, "-")+1:])
	version := 1
	if st.Resolution != nil {
		version = st.Resolution.Version + 1
	}
	var actions []grievance.Action
	for i, a := range req.Actions {
		k, ok := kinds[a.Kind]
		if !ok {
			return badRequest("unknown action %q", a.Kind)
		}
		desc := strings.TrimSpace(a.Description)
		if desc == "" {
			desc = k.desc
		}
		act := grievance.Action{
			ActionID: fmt.Sprintf("ACT-%03d", i+1), Type: k.typ, Description: desc, ExecutionMethod: k.method,
		}
		if a.CommitmentID != "" {
			l := o.order.Line(a.CommitmentID)
			if l == nil {
				return badRequest("unknown order line %q", a.CommitmentID)
			}
			qty := a.Quantity
			if qty <= 0 || float64(qty) > l.Quantity {
				qty = int(l.Quantity)
			}
			act.Affected = []grievance.AffectedLine{{CommitmentID: a.CommitmentID, Quantity: qty}}
		}
		if k.typ == "REFUND" {
			if a.CommitmentID == "" {
				return badRequest("a refund names the order line it is for")
			}
			paid := o.order.Paid(a.CommitmentID, act.Affected[0].Quantity)
			amount := a.Amount
			if a.Kind == "REFUND_FULL" {
				amount = paid
			}
			if amount <= 0 {
				return badRequest("a refund needs an amount above zero")
			}
			if paid > 0 && amount > paid+0.005 {
				return badRequest("the refund (%.0f) is more than the buyer paid for these items (%.0f)", amount, paid)
			}
			// A refund agreed in the order flow that never arrived is paid
			// out again on its own settlement; any other refund gets one.
			ref := fmt.Sprintf("refund-grv-%s-v%d-%d", suffix, version, i+1)
			if linked != "" && rec.Category == grievance.RefundNotReceived {
				ref = linked
			}
			act.FinancialEffect = &grievance.FinancialEffect{
				EffectType: "REFUND", Amount: math.Round(amount*100) / 100, Currency: grievance.Currency,
				AllocationBasis: basis, LiablePartyID: s.cfg.BppID, SettlementRef: ref,
			}
			if basis == "POLICY" {
				act.FinancialEffect.PolicyRef = returnPolicy
			}
		}
		actions = append(actions, act)
	}
	g := st.Propose(s.cfg.BppID, auth, actions, now)
	if auth == grievance.PolicyAuthorized {
		qc := findReturn(o.order, req.QCReference)
		if qc == nil {
			return badRequest("pick the approved return whose quality check this refund rests on")
		}
		g.Resolution.PolicyRef = returnPolicy
		g.Resolution.Verification = &grievance.Verification{
			VerifiedByNpID: s.cfg.BppID, VerificationReference: qc.PerformanceID,
			VerifiedAt: grievance.FormatTime(now), Method: qcMethod,
		}
	}
	return s.send(ctx, rec.Header, g, uuid.New())
}

// findReturn returns the approved (QC passed) return ref, or nil.
func findReturn(o grievance.Order, ref string) *grievance.Return {
	for i, r := range o.Returns {
		if r.PerformanceID == ref && (r.Status == "Return_Approved" || r.Status == "Return_Received") {
			return &o.Returns[i]
		}
	}
	return nil
}

// QCRecords are the order's returns that passed their check, for a
// POLICY_AUTHORIZED refund.
func QCRecords(o grievance.Order) []grievance.Return {
	out := []grievance.Return{}
	for _, r := range o.Returns {
		if r.Status == "Return_Approved" || r.Status == "Return_Received" {
			out = append(out, r)
		}
	}
	return out
}

// Execute carries out the active resolution's pending actions through the
// order's own flows -- a refund settlement, a redelivery or replacement
// performance -- sends the updated contract to the BAP with an unsolicited
// on_update, and sends ACTION_COMPLETED with each action's reference.
func (s *Service) Execute(ctx context.Context, provider, id string) error {
	rec, _, st, err := s.load(ctx, provider, id)
	if err != nil {
		return err
	}
	if !st.Authorized() {
		return badRequest("the buyer has not accepted the offer yet")
	}
	pending := st.Pending()
	if len(pending) == 0 {
		return badRequest("nothing left to carry out")
	}
	o, err := s.orderByTxn(ctx, rec.TransactionID)
	if err != nil {
		return err
	}
	now := s.now()
	var done []grievance.ActionExecution
	changed := false
	for _, a := range pending {
		key := id + "/" + a.ActionID
		ref := ""
		switch a.ExecutionMethod {
		case "SETTLEMENT":
			if a.FinancialEffect == nil {
				return badRequest("action %s moves no money", a.ActionID)
			}
			ref = cancellation.GrievanceRefund(o.contract, a.FinancialEffect.SettlementRef, key, a.FinancialEffect.Amount, now)
			changed = true
		case "PERFORMANCE":
			var lines []string
			for _, l := range a.Affected {
				if l.CommitmentID != "" {
					lines = append(lines, l.CommitmentID)
				}
			}
			kind := a.Type
			ref = cancellation.GrievancePerformance(o.contract, strings.ToLower(kind)+"-"+strings.ToLower(strings.ReplaceAll(key, "/", "-")), kind, lines)
			changed = true
		default:
			// Done outside the order's flows (CONTRACT_UPDATE, OTHER).
			ref = "MANUAL-" + strings.ToUpper(strings.ReplaceAll(key, "/", "-"))
		}
		done = append(done, grievance.ActionExecution{
			ActionID: a.ActionID, Status: "COMPLETED", CompletedAt: grievance.FormatTime(now),
			ExecutedByNpID: s.cfg.BppID, ExecutionReference: ref,
		})
	}
	if changed {
		body, _ := json.Marshal(o.contract)
		if _, err := s.pool.Exec(ctx, `UPDATE contracts SET contract_attributes = $1, updated_at = NOW() WHERE id = $2 AND bpp_id = $3`,
			body, o.contractID, s.cfg.BppID); err != nil {
			return err
		}
		if err := s.sendOnUpdate(ctx, o); err != nil {
			s.lh.WithModule("grievancesvc").Warn().Error(err).Log("unsolicited on_update for grievance " + id + " failed")
		}
	}
	g := st.Next(grievance.ActionCompleted, now)
	g.Execution = &grievance.Execution{ActionExecutions: done}
	return s.send(ctx, rec.Header, g, uuid.New())
}

// Resolve marks the grievance RESOLVED once every action is carried out.
func (s *Service) Resolve(ctx context.Context, provider, id string) error {
	rec, _, st, err := s.load(ctx, provider, id)
	if err != nil {
		return err
	}
	if st.Resolution == nil {
		return badRequest("propose a solution first")
	}
	return s.send(ctx, rec.Header, st.Next(grievance.Resolved, s.now()), uuid.New())
}

// Decide accepts or rejects the buyer's answer to a seller grievance.
func (s *Service) Decide(ctx context.Context, provider, id string, accept bool, reason string) error {
	rec, _, st, err := s.load(ctx, provider, id)
	if err != nil {
		return err
	}
	if st.Resolution == nil {
		return badRequest("there is no answer to decide on")
	}
	return s.send(ctx, rec.Header, st.Decide(s.cfg.BppID, accept, strings.TrimSpace(reason), s.now()), uuid.New())
}

// Object reopens a RESOLVED seller grievance within its objection period.
func (s *Service) Object(ctx context.Context, provider, id, explanation string) error {
	rec, _, st, err := s.load(ctx, provider, id)
	if err != nil {
		return err
	}
	explanation = strings.TrimSpace(explanation)
	if explanation == "" {
		return badRequest("say what is still wrong")
	}
	g := st.Next(grievance.Objection, s.now())
	g.Explanation = explanation
	return s.send(ctx, rec.Header, g, uuid.New())
}

// Close closes a seller grievance.
func (s *Service) Close(ctx context.Context, provider, id string) error {
	rec, _, st, err := s.load(ctx, provider, id)
	if err != nil {
		return err
	}
	return s.send(ctx, rec.Header, st.Next(grievance.Closed, s.now()), uuid.New())
}

// RaiseRequest is a seller grievance about an order.
type RaiseRequest struct {
	Category    string                   `json:"category" binding:"required" enums:"WRONG_RETURN,RETURN_QC_DISPUTED,OTHER"`
	Description string                   `json:"description" binding:"required"`
	Affected    []grievance.AffectedLine `json:"affected"`
	// ReturnID is the return it is about (its performance id), if any.
	ReturnID string `json:"returnId"`
}

var sellerCategories = map[string]bool{grievance.WrongReturn: true, grievance.ReturnQCDisputed: true, grievance.Other: true}

// Raise opens a seller grievance about one of the seller's orders: an
// unsolicited on_support with raisedBy.side SELLER, against the buyer.
func (s *Service) Raise(ctx context.Context, provider string, contractID uuid.UUID, req RaiseRequest) (string, error) {
	o, err := s.orderByContract(ctx, contractID)
	if err != nil {
		return "", err
	}
	if o.providerID != provider {
		return "", ErrNotFound
	}
	if !sellerCategories[req.Category] {
		return "", badRequest("unknown category %q", req.Category)
	}
	desc := strings.TrimSpace(req.Description)
	if desc == "" || len(desc) > 1500 {
		return "", badRequest("description is required, at most 1500 characters")
	}
	var related []grievance.RelatedReference
	affected := req.Affected
	if req.ReturnID != "" {
		r := findAnyReturn(o.order, req.ReturnID)
		if r == nil {
			return "", badRequest("unknown return")
		}
		related = append(related, grievance.RelatedReference{RefType: "PERFORMANCE", RefID: r.PerformanceID})
		if len(affected) == 0 {
			for _, cid := range r.CommitmentIDs {
				affected = append(affected, grievance.AffectedLine{CommitmentID: cid, PerformanceID: r.PerformanceID, Quantity: int(lineQty(o.order, cid))})
			}
		}
		if len(affected) == 0 {
			// A return recorded without its lines: name the return itself.
			affected = []grievance.AffectedLine{{PerformanceID: r.PerformanceID}}
		}
	}
	if grievance.LinkedCategories[req.Category] && len(related) == 0 {
		related = append(related, grievance.RelatedReference{RefType: "CONTRACT", RefID: o.order.ContractID})
	}
	for _, a := range affected {
		if a.CommitmentID != "" && o.order.Line(a.CommitmentID) == nil {
			return "", badRequest("unknown order line %q", a.CommitmentID)
		}
	}
	now := s.now()
	g := grievance.Open(grievance.Opening{
		GrievanceID: grievance.NewID("GRV", now), Side: grievance.SideSeller,
		NpID: s.cfg.BppID, AssignedNpID: o.header.BapID,
		Against:  grievance.Subject{Role: "BUYER"},
		Category: req.Category, Description: desc, Affected: affected,
		PolicyRef: o.order.PolicyRef, Related: related,
	}, now)
	return g.GrievanceID, s.send(ctx, o.header, g, uuid.New())
}

func findAnyReturn(o grievance.Order, id string) *grievance.Return {
	for i := range o.Returns {
		if o.Returns[i].PerformanceID == id {
			return &o.Returns[i]
		}
	}
	return nil
}

func lineQty(o grievance.Order, id string) float64 {
	if l := o.Line(id); l != nil {
		return l.Quantity
	}
	return 0
}

// send delivers g as /on_support and records it.
func (s *Service) send(ctx context.Context, h grievance.Header, g *grievance.Grievance, msgID uuid.UUID) error {
	from := grievance.Sender{Endpoint: grievance.EndpointOnSupport, BapID: h.BapID, BppID: h.BppID}
	err := s.store.Send(ctx, h, from, msgID, g, s.now(), func(ctx context.Context) error {
		msg, err := grievance.NewMessage(grievance.EndpointOnSupport, h, msgID.String(), g, titleOf(g), nil, s.now())
		if err != nil {
			return err
		}
		return s.post(ctx, h, msgID, "on_support", dbsqlc.BecknActionOnSupport, msg)
	})
	var rule *grievance.RuleError
	if errors.As(err, &rule) {
		return fmt.Errorf("%w: %s", ErrBadRequest, rule.Msg)
	}
	return err
}

func titleOf(g *grievance.Grievance) string {
	if g.Category != "" {
		return strings.ReplaceAll(strings.ToLower(g.Category), "_", " ")
	}
	return "grievance " + g.GrievanceID
}

// sendOnUpdate sends the order's updated contract to the BAP.
func (s *Service) sendOnUpdate(ctx context.Context, o *orderInfo) error {
	msgID := uuid.New()
	h := o.header
	msg := map[string]any{
		"context": map[string]any{
			"version": "2.0.0", "action": "on_update", "timestamp": s.now().UTC().Format(time.RFC3339),
			"messageId": msgID.String(), "transactionId": h.TransactionID.String(),
			"bapId": h.BapID, "bapUri": h.BapURI, "bppId": h.BppID, "bppUri": h.BppURI, "networkId": h.NetworkID,
		},
		"message": map[string]any{"contract": o.contract},
	}
	return s.post(ctx, h, msgID, "on_update", dbsqlc.BecknActionOnUpdate, msg)
}

// post sends a message through onix-bpp's caller and logs it.
func (s *Service) post(ctx context.Context, h grievance.Header, msgID uuid.UUID, action string, logAction dbsqlc.BecknAction, msg any) error {
	base := strings.TrimRight(strings.TrimSpace(s.cfg.BppCallerURL), "/")
	if base == "" {
		base = strings.TrimSuffix(strings.TrimRight(s.cfg.BppURI, "/"), "/bpp/receiver") + "/bpp/caller"
	}
	url := base + "/" + action
	body, _ := json.Marshal(msg)
	start := time.Now()
	resp, err := grievance.Post(ctx, s.client, url, body)
	s.logMessage(context.WithoutCancel(ctx), h, msgID, logAction, dbsqlc.MessageDirectionOUTBOUND, url, body, resp, err, start)
	return err
}

func (s *Service) logMessage(ctx context.Context, h grievance.Header, msgID uuid.UUID, action dbsqlc.BecknAction,
	dir dbsqlc.MessageDirection, url string, req, resp []byte, sendErr error, start time.Time) {
	ack := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
	var errMsg *string
	if sendErr != nil {
		ack.AckStatus = dbsqlc.AckStatusNACK
		m := sendErr.Error()
		errMsg = &m
	}
	var respJSON json.RawMessage
	if json.Valid(resp) {
		respJSON = resp
	}
	ms := int32(time.Since(start).Milliseconds())
	if err := dbsqlc.New(s.pool).InsertMessageLog(ctx, dbsqlc.InsertMessageLogParams{
		TransactionID: pgtype.UUID{Bytes: h.TransactionID, Valid: h.TransactionID != uuid.Nil}, MessageID: msgID,
		Action: action, Direction: dir, Url: &url,
		BapID: &h.BapID, BapUri: &h.BapURI, BppID: &h.BppID, BppUri: &h.BppURI, NetworkID: &h.NetworkID,
		AckStatus: ack, RequestPayload: req, ResponsePayload: respJSON, ErrorMessage: errMsg, ProcessingDurationMs: &ms,
	}); err != nil {
		s.lh.WithModule("grievancesvc").Warn().Error(err).Log("message log insert failed")
	}
}

// ListFilter narrows the inbox.
type ListFilter struct {
	Status   string // OPEN | IN_PROGRESS | RESOLVED | CLOSED
	Category string
	// Only: "action" (waiting on the seller) or "sla" (a target within 2
	// hours or past).
	Only string
}

// Item is an inbox row: the view plus its order's short summary.
type Item struct {
	grievance.View
	OrderTotal float64 `json:"orderTotal"`
	OrderItems string  `json:"orderItems"`
	// DueAt is the nearest open target (first response or resolution).
	DueAt *time.Time `json:"dueAt"`
}

// List returns the seller's grievances, most urgent first: waiting on the
// seller, then by the nearest target.
func (s *Service) List(ctx context.Context, provider string, f ListFilter) ([]Item, int, error) {
	rows, err := s.pool.Query(ctx, `SELECT grievance_id FROM grievances WHERE provider_id = $1 ORDER BY last_event_at DESC LIMIT 500`, provider)
	if err != nil {
		return nil, 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	now := s.now()
	out := []Item{}
	needs := 0
	for _, id := range ids {
		rec, events, err := s.store.Get(ctx, id)
		if err != nil {
			continue
		}
		v := grievance.BuildView(rec, events, grievance.SideBPP, now)
		if v.NeedsAction {
			needs++
		}
		it := Item{View: v, DueAt: nearest(v.FirstResponseDue, v.ResolutionDue)}
		status := v.CaseStatus
		if v.DeemedClosed {
			status = grievance.StatusClosed
		}
		if f.Status != "" && status != f.Status {
			continue
		}
		if f.Category != "" && v.Category != f.Category {
			continue
		}
		if f.Only == "action" && !v.NeedsAction {
			continue
		}
		if f.Only == "sla" && (it.DueAt == nil || it.DueAt.After(now.Add(2*time.Hour))) {
			continue
		}
		if o, err := s.orderByTxn(ctx, rec.TransactionID); err == nil {
			names := make([]string, 0, len(o.order.Lines))
			for _, l := range o.order.Lines {
				names = append(names, fmt.Sprintf("%g× %s", l.Quantity, l.Name))
				it.OrderTotal += l.UnitPrice * l.Quantity
			}
			it.OrderItems = strings.Join(names, ", ")
		}
		out = append(out, it)
	}
	sortByUrgency(out)
	return out, needs, nil
}

func nearest(ts ...*time.Time) *time.Time {
	var best *time.Time
	for _, t := range ts {
		if t != nil && (best == nil || t.Before(*best)) {
			best = t
		}
	}
	return best
}

func sortByUrgency(items []Item) {
	rank := func(it Item) int {
		switch {
		case it.NeedsAction:
			return 0
		case it.CaseStatus == grievance.StatusOpen || it.CaseStatus == grievance.StatusInProgress:
			return 1
		case it.CaseStatus == grievance.StatusResolved && !it.DeemedClosed:
			return 2
		}
		return 3
	}
	less := func(a, b Item) bool {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra < rb
		}
		if a.DueAt != nil && b.DueAt != nil && !a.DueAt.Equal(*b.DueAt) {
			return a.DueAt.Before(*b.DueAt)
		}
		if (a.DueAt == nil) != (b.DueAt == nil) {
			return a.DueAt != nil
		}
		return a.LastEventAt.After(b.LastEventAt)
	}
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && less(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// Detail is a grievance with its order, for the detail page.
type Detail struct {
	grievance.View
	Order      grievance.Order `json:"order"`
	ContractID string          `json:"contractId"`
	// QCRecords are the order's returns that passed their check, for a
	// refund under the return policy.
	QCRecords []grievance.Return `json:"qcRecords"`
}

// Get returns a grievance's detail page and marks it seen.
func (s *Service) Get(ctx context.Context, provider, id string) (*Detail, error) {
	rec, events, _, err := s.load(ctx, provider, id)
	if err != nil {
		return nil, err
	}
	_ = s.store.MarkSeen(ctx, id)
	o, err := s.orderByTxn(ctx, rec.TransactionID)
	if err != nil {
		return nil, err
	}
	return &Detail{
		View: grievance.BuildView(rec, events, grievance.SideBPP, s.now()), Order: o.order,
		ContractID: o.contractID.String(), QCRecords: QCRecords(o.order),
	}, nil
}

// NeedsAction counts the seller's grievances waiting on the seller.
func (s *Service) NeedsAction(ctx context.Context, provider string) (int, error) {
	_, n, err := s.List(ctx, provider, ListFilter{})
	return n, err
}

// OrderGrievances lists the grievances of one of the seller's orders.
func (s *Service) OrderGrievances(ctx context.Context, provider string, contractID uuid.UUID) ([]grievance.View, error) {
	out := []grievance.View{}
	rows, err := s.pool.Query(ctx, `SELECT grievance_id FROM grievances WHERE contract_id = $1 AND provider_id = $2 ORDER BY last_event_at DESC`, contractID, provider)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if rec, events, err := s.store.Get(ctx, id); err == nil {
			out = append(out, grievance.BuildView(rec, events, grievance.SideBPP, s.now()))
		}
	}
	return out, nil
}
