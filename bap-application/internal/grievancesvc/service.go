// Package grievancesvc is the BAP side of order-level grievances (ION
// Grievance v1, see internal/grievance): the buyer opens one about an
// order, decides on the seller's offers, objects or closes; the seller's
// events arrive on /api/webhook/on_support. Every event goes out as
// /support through onix-bap.
package grievancesvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/grievance"
	"github.com/ion/winroom/bap/internal/httpclient"
)

// Service runs the BAP's grievances.
type Service struct {
	pool   *pgxpool.Pool
	cfg    *config.Config
	lh     *logharbour.Logger
	store  *grievance.Store
	client *http.Client
	now    func() time.Time
}

// NewService returns a Service.
func NewService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Service {
	return &Service{
		pool: pool, cfg: cfg, lh: lh, store: &grievance.Store{Pool: pool},
		client: httpclient.New(90 * time.Second), now: time.Now,
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

// orderInfo is an order a grievance is about.
type orderInfo struct {
	header       grievance.Header
	order        grievance.Order
	buyerSubject *string
}

// loadOrder reads the transaction and its latest contract.
func (s *Service) loadOrder(ctx context.Context, txnID uuid.UUID) (*orderInfo, error) {
	var bppID, bppURI, subject *string
	err := s.pool.QueryRow(ctx, `SELECT bpp_id, bpp_uri, buyer_subject FROM transactions WHERE transaction_id = $1`, txnID).
		Scan(&bppID, &bppURI, &subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var contractJSON []byte
	if err := s.pool.QueryRow(ctx, `
		SELECT contract FROM contract_snapshots WHERE transaction_id = $1
		ORDER BY created_at DESC LIMIT 1`, txnID).Scan(&contractJSON); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var contract map[string]any
	_ = json.Unmarshal(contractJSON, &contract)
	if inner, ok := contract["message"].(map[string]any); ok {
		if c, ok := inner["contract"].(map[string]any); ok {
			contract = c
		}
	}
	o := &orderInfo{order: grievance.ReadOrder(contract), buyerSubject: subject}
	h := grievance.Header{
		TransactionID: txnID, OrderID: o.order.ContractID,
		BapID: s.cfg.BapID, BapURI: s.cfg.BapURI, BppID: s.cfg.BppID, BppURI: s.cfg.BppURI, NetworkID: s.cfg.NetworkID,
	}
	if bppID != nil && *bppID != "" {
		h.BppID = *bppID
	}
	if bppURI != nil && *bppURI != "" {
		h.BppURI = *bppURI
	}
	if h.OrderID == "" {
		h.OrderID = txnID.String()
	}
	o.header = h
	return o, nil
}

// owns reports whether the buyer may see the transaction's grievances.
func owns(subject *string, buyer string) bool {
	return buyer != "" && subject != nil && *subject == buyer
}

// Categories the buyer can choose. Return and refund categories link the
// return or refund they are about.
var buyerCategories = map[string]bool{
	grievance.DamagedItem: true, grievance.WrongItem: true, grievance.MissingItem: true,
	grievance.LateDelivery: true, grievance.ReturnRejected: true, grievance.ReturnPickupFailed: true,
	grievance.ReturnQCDisputed: true, grievance.RefundNotReceived: true, grievance.Other: true,
}

// OpenRequest is a buyer grievance.
type OpenRequest struct {
	TransactionID string                       `json:"transactionId" binding:"required"`
	Category      string                       `json:"category" binding:"required"`
	Description   string                       `json:"description" binding:"required"`
	Affected      []grievance.AffectedLine     `json:"affected"`
	Related       []grievance.RelatedReference `json:"related"`
}

// Open opens a buyer grievance: OPENED over /support.
func (s *Service) Open(ctx context.Context, buyer string, req OpenRequest) (*grievance.View, error) {
	txnID, err := uuid.Parse(req.TransactionID)
	if err != nil {
		return nil, ErrNotFound
	}
	o, err := s.loadOrder(ctx, txnID)
	if err != nil {
		return nil, err
	}
	if !owns(o.buyerSubject, buyer) {
		return nil, ErrNotFound
	}
	if !buyerCategories[req.Category] {
		return nil, badRequest("unknown category %q", req.Category)
	}
	desc := strings.TrimSpace(req.Description)
	if desc == "" || len(desc) > 1500 {
		return nil, badRequest("description is required, at most 1500 characters")
	}
	affected := make([]grievance.AffectedLine, 0, len(req.Affected))
	for _, a := range req.Affected {
		l := o.order.Line(a.CommitmentID)
		if l == nil {
			return nil, badRequest("unknown order line %q", a.CommitmentID)
		}
		if a.Quantity < 0 || float64(a.Quantity) > l.Quantity {
			return nil, badRequest("quantity for %s is more than was ordered", l.Name)
		}
		affected = append(affected, a)
	}
	related := req.Related
	if grievance.LinkedCategories[req.Category] && len(related) == 0 {
		related = autoLink(req.Category, o.order)
	}

	now := s.now()
	g := grievance.Open(grievance.Opening{
		GrievanceID: grievance.NewID("GRV", now), Side: grievance.SideBuyer,
		NpID: s.cfg.BapID, AssignedNpID: o.header.BppID,
		Against:  grievance.Subject{Role: "SELLER", PartyID: o.order.ProviderID},
		Category: req.Category, Description: desc, Affected: affected,
		PolicyRef: o.order.PolicyRef, Related: related,
	}, now)
	h := o.header
	if o.buyerSubject != nil {
		h.Extra = map[string]any{"buyer_subject": *o.buyerSubject}
	}
	if err := s.send(ctx, h, g, uuid.New()); err != nil {
		return nil, err
	}
	return s.View(ctx, buyer, g.GrievanceID)
}

// autoLink picks the record a return or refund grievance is about: the
// latest return, the latest refund, or else the contract.
func autoLink(category string, o grievance.Order) []grievance.RelatedReference {
	var refs []grievance.RelatedReference
	if len(o.Returns) > 0 {
		refs = append(refs, grievance.RelatedReference{RefType: "PERFORMANCE", RefID: o.Returns[len(o.Returns)-1].PerformanceID})
	}
	if category == grievance.RefundNotReceived && len(o.Refunds) > 0 {
		refs = append(refs, grievance.RelatedReference{RefType: "SETTLEMENT", RefID: o.Refunds[len(o.Refunds)-1].SettlementID})
	}
	if len(refs) == 0 && o.ContractID != "" {
		refs = append(refs, grievance.RelatedReference{RefType: "CONTRACT", RefID: o.ContractID})
	}
	return refs
}

// load returns a grievance the buyer may act on, with its state.
func (s *Service) load(ctx context.Context, buyer, id string) (*grievance.Record, []grievance.EventRecord, *grievance.State, error) {
	rec, events, err := s.store.Get(ctx, id)
	if errors.Is(err, grievance.ErrNotFound) {
		return nil, nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, nil, err
	}
	var subject *string
	if err := s.pool.QueryRow(ctx, `SELECT buyer_subject FROM grievances WHERE grievance_id = $1`, id).Scan(&subject); err != nil {
		return nil, nil, nil, err
	}
	if !owns(subject, buyer) {
		return nil, nil, nil, ErrNotFound
	}
	return rec, events, grievance.Replay(events), nil
}

func headerOf(rec *grievance.Record) grievance.Header { return rec.Header }

// Decide accepts or rejects the seller's active offer.
func (s *Service) Decide(ctx context.Context, buyer, id string, accept bool, reason string) (*grievance.View, error) {
	rec, _, st, err := s.load(ctx, buyer, id)
	if err != nil {
		return nil, err
	}
	if st.Resolution == nil {
		return nil, badRequest("there is no offer to decide on")
	}
	if err := s.send(ctx, headerOf(rec), st.Decide(s.cfg.BapID, accept, strings.TrimSpace(reason), s.now()), uuid.New()); err != nil {
		return nil, err
	}
	return s.View(ctx, buyer, id)
}

// Object reopens a RESOLVED grievance within its objection period.
func (s *Service) Object(ctx context.Context, buyer, id, explanation string) (*grievance.View, error) {
	rec, _, st, err := s.load(ctx, buyer, id)
	if err != nil {
		return nil, err
	}
	explanation = strings.TrimSpace(explanation)
	if explanation == "" {
		return nil, badRequest("say what is still wrong")
	}
	g := st.Next(grievance.Objection, s.now())
	g.Explanation = explanation
	if err := s.send(ctx, headerOf(rec), g, uuid.New()); err != nil {
		return nil, err
	}
	return s.View(ctx, buyer, id)
}

// Close closes a grievance the buyer opened.
func (s *Service) Close(ctx context.Context, buyer, id string) (*grievance.View, error) {
	rec, _, st, err := s.load(ctx, buyer, id)
	if err != nil {
		return nil, err
	}
	if err := s.send(ctx, headerOf(rec), st.Next(grievance.Closed, s.now()), uuid.New()); err != nil {
		return nil, err
	}
	return s.View(ctx, buyer, id)
}

// RespondRequest is the buyer's answer to a seller grievance.
type RespondRequest struct {
	Description string `json:"description" binding:"required"`
}

// Respond answers a seller grievance with an explanation, for the seller
// to accept or reject.
func (s *Service) Respond(ctx context.Context, buyer, id string, req RespondRequest) (*grievance.View, error) {
	rec, _, st, err := s.load(ctx, buyer, id)
	if err != nil {
		return nil, err
	}
	desc := strings.TrimSpace(req.Description)
	if desc == "" || len(desc) > 1500 {
		return nil, badRequest("an answer is required, at most 1500 characters")
	}
	if st.OriginSide == grievance.SideBAP {
		return nil, badRequest("only a seller's grievance is answered this way")
	}
	g := st.Propose(s.cfg.BapID, grievance.ExplicitAgreement, []grievance.Action{{
		ActionID: "ACT-001", Type: "INFORMATION_PROVIDED", Description: desc,
		Affected: rec.Affected, ExecutionMethod: grievance.MethodNone,
	}}, s.now())
	if err := s.send(ctx, headerOf(rec), g, uuid.New()); err != nil {
		return nil, err
	}
	return s.View(ctx, buyer, id)
}

// GoodwillRequest is a refund the buyer app pays itself.
type GoodwillRequest struct {
	Amount      float64 `json:"amount" binding:"required"`
	Description string  `json:"description"`
}

// GoodwillRefund resolves a buyer grievance with a refund funded by the BAP
// (UNILATERAL): proposed, executed and resolved by the BAP, with no
// liability for the seller. It is an operator action, not a buyer one.
func (s *Service) GoodwillRefund(ctx context.Context, id string, req GoodwillRequest) (*grievance.View, error) {
	rec, events, err := s.store.Get(ctx, id)
	if errors.Is(err, grievance.ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	st := grievance.Replay(events)
	if req.Amount <= 0 {
		return nil, badRequest("amount must be positive")
	}
	desc := strings.TrimSpace(req.Description)
	if desc == "" {
		desc = "Goodwill refund from the buyer app"
	}
	now := s.now()
	h := headerOf(rec)
	g := st.Propose(s.cfg.BapID, grievance.Unilateral, []grievance.Action{{
		ActionID: "ACT-GW-1", Type: "REFUND", Description: desc, Affected: rec.Affected, ExecutionMethod: "SETTLEMENT",
		FinancialEffect: &grievance.FinancialEffect{
			EffectType: "REFUND", Amount: req.Amount, Currency: grievance.Currency, AllocationBasis: "UNILATERAL",
			LiablePartyID: s.cfg.BapID, AllocationReason: "Buyer app goodwill; no liability for the seller app.",
		},
	}}, now)
	if err := s.send(ctx, h, g, uuid.New()); err != nil {
		return nil, err
	}
	st = grievance.Apply(st, g, grievance.SideBAP)
	// The buyer app pays it at once through its own (mock) gateway.
	done := st.Next(grievance.ActionCompleted, now)
	done.Execution = &grievance.Execution{ActionExecutions: []grievance.ActionExecution{{
		ActionID: "ACT-GW-1", Status: "COMPLETED", CompletedAt: grievance.FormatTime(now),
		ExecutedByNpID: s.cfg.BapID, ExecutionReference: "GW-" + strings.ToUpper(uuid.NewString()[:12]),
	}}}
	if err := s.send(ctx, h, done, uuid.New()); err != nil {
		return nil, err
	}
	st = grievance.Apply(st, done, grievance.SideBAP)
	if err := s.send(ctx, h, st.Next(grievance.Resolved, now), uuid.New()); err != nil {
		return nil, err
	}
	v := s.view(ctx, id)
	return v, nil
}

// send delivers g as /support and records it.
func (s *Service) send(ctx context.Context, h grievance.Header, g *grievance.Grievance, msgID uuid.UUID) error {
	from := grievance.Sender{Endpoint: grievance.EndpointSupport, BapID: h.BapID, BppID: h.BppID}
	err := s.store.Send(ctx, h, from, msgID, g, s.now(), func(ctx context.Context) error {
		return s.post(ctx, h, msgID, g)
	})
	var rule *grievance.RuleError
	if errors.As(err, &rule) {
		return fmt.Errorf("%w: %s", ErrBadRequest, rule.Msg)
	}
	return err
}

// post sends /support through onix-bap and logs it.
func (s *Service) post(ctx context.Context, h grievance.Header, msgID uuid.UUID, g *grievance.Grievance) error {
	msg, err := grievance.NewMessage(grievance.EndpointSupport, h, msgID.String(), g, titleOf(g), nil, s.now())
	if err != nil {
		return err
	}
	body, _ := json.Marshal(msg)
	url := strings.TrimRight(s.cfg.BapCallerURL, "/") + "/support"
	start := time.Now()
	resp, sendErr := grievance.Post(ctx, s.client, url, body)
	s.logMessage(context.WithoutCancel(ctx), h, msgID, dbsqlc.BecknActionSupport, dbsqlc.MessageDirectionOUTBOUND, url, body, resp, sendErr, start)
	return sendErr
}

// titleOf is Support.descriptor.name: the category in words.
func titleOf(g *grievance.Grievance) string {
	if g.Category != "" {
		return strings.ReplaceAll(strings.ToLower(g.Category), "_", " ")
	}
	return "grievance " + g.GrievanceID
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
		TransactionID: pgtype.UUID{Bytes: h.TransactionID, Valid: true}, MessageID: msgID,
		Action: action, Direction: dir, Url: &url,
		BapID: &h.BapID, BapUri: &h.BapURI, BppID: &h.BppID, BppUri: &h.BppURI, NetworkID: &h.NetworkID,
		AckStatus: ack, RequestPayload: req, ResponsePayload: respJSON, ErrorMessage: errMsg, ProcessingDurationMs: &ms,
	}); err != nil {
		s.lh.WithModule("grievancesvc").Warn().Error(err).Log("message log insert failed")
	}
}

// Receive handles an inbound /on_support: checks the grievance event
// against the grievance's state and stores it. A *grievance.RuleError (or
// any error) is answered with NACK.
func (s *Service) Receive(ctx context.Context, msg *grievance.Message) error {
	c := msg.Context
	if c.BapID != s.cfg.BapID {
		return &grievance.RuleError{Msg: "context.bapId is not this BAP"}
	}
	g, ok, err := msg.Message.Support.Parse()
	if err != nil {
		return &grievance.RuleError{Msg: "supportAttributes is not an IONGrievance: " + err.Error()}
	}
	if !ok {
		// A plain Beckn on_support (channels only): nothing to track.
		return nil
	}
	txnID, err := uuid.Parse(c.TransactionID)
	if err != nil {
		return &grievance.RuleError{Msg: "context.transactionId is not the order's transaction"}
	}
	msgID, err := uuid.Parse(c.MessageID)
	if err != nil {
		return &grievance.RuleError{Msg: "context.messageId is not a UUID"}
	}
	h := grievance.Header{
		TransactionID: txnID, OrderID: msg.Message.Support.OrderID,
		BapID: c.BapID, BapURI: c.BapURI, BppID: c.BppID, BppURI: c.BppURI, NetworkID: c.NetworkID,
	}
	if g.EventType == grievance.Opened {
		o, err := s.loadOrder(ctx, txnID)
		if err != nil {
			return &grievance.RuleError{Msg: "unknown order"}
		}
		if o.header.BppID != c.BppID {
			return &grievance.RuleError{Msg: "context.bppId is not the order's BPP"}
		}
		if o.buyerSubject != nil {
			h.Extra = map[string]any{"buyer_subject": *o.buyerSubject}
		}
		if h.OrderID == "" {
			h.OrderID = o.header.OrderID
		}
	}
	from := grievance.Sender{Endpoint: grievance.EndpointOnSupport, BapID: c.BapID, BppID: c.BppID}
	return s.store.Receive(ctx, h, from, msgID, g, msg.Message.Support.Channels, s.now())
}

// React does what the BAP does on its own after accepting an event: it
// acknowledges a seller grievance, and resolves a seller grievance once
// the seller accepted its answer (an explanation needs no execution).
func (s *Service) React(ctx context.Context, msg *grievance.Message) {
	g, ok, _ := msg.Message.Support.Parse()
	if !ok || g == nil {
		return
	}
	rec, events, err := s.store.Get(ctx, g.GrievanceID)
	if err != nil {
		return
	}
	st := grievance.Replay(events)
	var next *grievance.Grievance
	switch {
	case g.EventType == grievance.Opened:
		next = st.Next(grievance.Acknowledged, s.now())
	case g.EventType == grievance.ResolutionDecided && st.Authorized() && st.ProposerSide == grievance.SideBAP && len(st.Pending()) == 0:
		next = st.Next(grievance.Resolved, s.now())
	}
	if next == nil {
		return
	}
	if err := s.send(ctx, headerOf(rec), next, uuid.New()); err != nil {
		s.lh.WithModule("grievancesvc").Warn().Error(err).Log(fmt.Sprintf("automatic %s for %s failed", next.EventType, g.GrievanceID))
	}
}

// View returns a grievance as the buyer app shows it, and marks it seen.
func (s *Service) View(ctx context.Context, buyer, id string) (*grievance.View, error) {
	if _, _, _, err := s.load(ctx, buyer, id); err != nil {
		return nil, err
	}
	return s.view(ctx, id), nil
}

func (s *Service) view(ctx context.Context, id string) *grievance.View {
	rec, events, err := s.store.Get(ctx, id)
	if err != nil {
		return nil
	}
	v := grievance.BuildView(rec, events, grievance.SideBAP, s.now())
	return &v
}

// Detail is a grievance with its order, for the detail page.
type Detail struct {
	grievance.View
	Order grievance.Order `json:"order"`
}

// Get returns the detail page of a grievance and marks it seen.
func (s *Service) Get(ctx context.Context, buyer, id string) (*Detail, error) {
	rec, _, _, err := s.load(ctx, buyer, id)
	if err != nil {
		return nil, err
	}
	_ = s.store.MarkSeen(ctx, id)
	o, err := s.loadOrder(ctx, rec.TransactionID)
	if err != nil {
		return nil, err
	}
	return &Detail{View: *s.view(ctx, id), Order: o.order}, nil
}

// List returns the buyer's grievances, open ones first; with txn, only
// that order's.
func (s *Service) List(ctx context.Context, buyer, txn string) ([]grievance.View, error) {
	out := []grievance.View{}
	if buyer == "" {
		return out, nil
	}
	q := `SELECT grievance_id FROM grievances WHERE buyer_subject = $1`
	args := []any{buyer}
	if txn != "" {
		id, err := uuid.Parse(txn)
		if err != nil {
			return out, nil
		}
		q += ` AND transaction_id = $2`
		args = append(args, id)
	}
	rows, err := s.pool.Query(ctx, q+` ORDER BY (case_status IN ('RESOLVED', 'CLOSED')), last_event_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if v := s.view(ctx, id); v != nil {
			out = append(out, *v)
		}
	}
	return out, nil
}

// Options is what the "raise a problem" form needs about an order.
type Options struct {
	Order  grievance.Order `json:"order"`
	Policy PolicyView      `json:"policy"`
	// Open lists the order's grievances that are not closed.
	Open []grievance.View `json:"open"`
}

// PolicyView is the order's grievance SLA policy in figures.
type PolicyView struct {
	Ref                string  `json:"ref"`
	FirstResponseHours float64 `json:"firstResponseHours"`
	ResolutionHours    float64 `json:"resolutionHours"`
	ObjectionDays      float64 `json:"objectionDays"`
}

// GrievanceOptions returns the order's lines, returns, refunds and policy.
func (s *Service) GrievanceOptions(ctx context.Context, buyer, txn string) (*Options, error) {
	txnID, err := uuid.Parse(txn)
	if err != nil {
		return nil, ErrNotFound
	}
	o, err := s.loadOrder(ctx, txnID)
	if err != nil {
		return nil, err
	}
	if !owns(o.buyerSubject, buyer) {
		return nil, ErrNotFound
	}
	p := grievance.Policy(o.order.PolicyRef)
	list, err := s.List(ctx, buyer, txn)
	if err != nil {
		return nil, err
	}
	open := []grievance.View{}
	for _, v := range list {
		if v.CaseStatus != grievance.StatusClosed && !v.DeemedClosed {
			open = append(open, v)
		}
	}
	return &Options{
		Order: o.order,
		Policy: PolicyView{
			Ref: o.order.PolicyRef, FirstResponseHours: p.FirstResponse.Hours(),
			ResolutionHours: p.Resolution.Hours(), ObjectionDays: p.Objection.Hours() / 24,
		},
		Open: open,
	}, nil
}

// NeedsAction counts the buyer's grievances waiting on them.
func (s *Service) NeedsAction(ctx context.Context, buyer string) (int, error) {
	list, err := s.List(ctx, buyer, "")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, v := range list {
		if v.NeedsAction {
			n++
		}
	}
	return n, nil
}
