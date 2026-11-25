package qrsvc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bap/internal/clientsvc"
	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/discoverwait"
)

const (
	// candidateTTL bounds how long a resolve result can be turned into a quote.
	candidateTTL = 30 * time.Minute
	// callbackWait bounds how long quote/checkout wait for the seller's
	// on_select / on_init after the adapter ACKed the request. Kept under
	// bap-frontend nginx's 90s proxy_read_timeout together with the
	// request itself.
	callbackWait = 60 * time.Second
	// maxDiscoverCalls caps parallel discover calls per resolve.
	maxDiscoverCalls = 4
)

// apiError is an error with an HTTP status and machine code for the client.
type apiError struct {
	status int
	code   string
	msg    string
	data   any
}

func (e *apiError) Error() string { return e.code + ": " + e.msg }

func newErr(status int, code, msg string) *apiError {
	return &apiError{status: status, code: code, msg: msg}
}

// Service implements the QR checkout.
type Service struct {
	pool    *pgxpool.Pool
	cfg     *config.Config
	lh      *logharbour.Logger
	qr      *qrClient
	beckn   *clientsvc.ClientService
	confirm sync.Mutex
}

// NewService builds the QR checkout service.
func NewService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger, registry *discoverwait.Registry) *Service {
	return &Service{
		pool:  pool,
		cfg:   cfg,
		lh:    lh,
		qr:    newQRClient(cfg.QRServiceURL, cfg.QRServiceKey),
		beckn: clientsvc.NewClientService(pool, cfg, lh, registry),
	}
}

func (s *Service) log() *logharbour.Logger { return s.lh.WithModule("qr_checkout") }

// ---------------------------------------------------------------------------
// Packet
// ---------------------------------------------------------------------------

func (s *Service) packet(ctx context.Context, id string) (*Packet, error) {
	p, err := s.qr.getPacket(ctx, strings.ToLower(strings.TrimSpace(id)))
	switch {
	case errors.Is(err, errQRNotConfigured):
		return nil, newErr(503, "QR_NOT_CONFIGURED", "QR checkout is not configured on this BAP (QR_SERVICE_URL)")
	case errors.Is(err, errPacketNotFound):
		return nil, newErr(404, "PACKET_NOT_FOUND", "This QR code doesn't point to a known packet")
	case err != nil:
		s.log().Warn().Error(err).Log("qr-service call failed")
		return nil, newErr(502, "QR_SERVICE_UNAVAILABLE", "The QR service is unavailable, try again shortly")
	}
	return p, nil
}

// Packet returns the public view of a packet (any status).
func (s *Service) Packet(ctx context.Context, id string) (*PacketView, error) {
	p, err := s.packet(ctx, id)
	if err != nil {
		return nil, err
	}
	v := p.view()
	return &v, nil
}

// ---------------------------------------------------------------------------
// Resolve
// ---------------------------------------------------------------------------

// Resolve finds the merchants that can serve a scanned packet.
func (s *Service) Resolve(ctx context.Context, req ResolveRequest, buyerSubject *string, userAgent, clientIP string) (*ResolveResponse, error) {
	p, err := s.packet(ctx, req.PacketID)
	if err != nil {
		return nil, err
	}
	if p.Status != "ACTIVE" {
		e := newErr(409, "PACKET_NOT_ACTIVE", "This packet isn't taking orders right now")
		e.data = p.view()
		return nil, e
	}

	resp := &ResolveResponse{Packet: p.view(), Candidates: []CandidateView{}}

	// Scan telemetry: qr-service's redirect already recorded a REDIRECT
	// scan and passed its id; otherwise record a DIRECT_RESOLVE one.
	var scanID *uuid.UUID
	if id, err := uuid.Parse(req.ScanID); err == nil {
		scanID = &id
	} else if len(req.ClientScanKey) >= 8 {
		if sid, err := s.qr.recordScan(ctx, p.ID, req.ClientScanKey, userAgent, clientIP); err != nil {
			s.log().Warn().Error(err).Log("record DIRECT_RESOLVE scan failed")
		} else if sid != nil {
			if id, err := uuid.Parse(*sid); err == nil {
				scanID = &id
			}
		}
	}
	if scanID != nil {
		v := scanID.String()
		resp.ScanID = &v
	}

	if req.DemoScenario != "" {
		return s.resolveDemo(ctx, p, req.DemoScenario, scanID, resp)
	}

	if s.cfg.QRDiscoverMethod == config.DiscoverMethodGet && s.cfg.CDSDiscoverURL == "" {
		resp.Mode = ModeNoMerchant
		resp.Reason = "DISCOVERY_NOT_CONFIGURED"
		resp.Message = "This BAP has no discovery service configured (CDS_DISCOVER_URL)"
		return resp, nil
	}

	m, err := s.match(ctx, p, buyerSubject)
	if err != nil {
		s.log().Warn().Error(err).Log("discover for packet failed")
		return nil, newErr(502, "DISCOVERY_FAILED", "Couldn't search nearby merchants, try again shortly")
	}
	sum := m.summarize()
	if len(sum.eligible) == 0 {
		resp.Mode = ModeNoMerchant
		resp.MissingItems = sum.missing
		resp.Reason = "NO_ELIGIBLE_MERCHANT"
		resp.Message = "No merchant near this spot stocks every item right now"
		return resp, nil
	}

	// Persist candidates under one guest token.
	token, tokenHash := newGuestToken()
	resp.GuestToken = token
	nd := packetNode(p)
	for i, rc := range sum.eligible {
		id := uuid.New()
		if err := s.insertCandidate(ctx, id, p, scanID, tokenHash, buyerSubject, rc, nd, ""); err != nil {
			return nil, fmt.Errorf("store candidate: %w", err)
		}
		cv := candidateView(id, rc, len(p.Items))
		cv.Recommended = i == 0
		resp.Candidates = append(resp.Candidates, cv)
	}

	resp.Mode = resolveMode(len(resp.Candidates))
	return resp, nil
}

// resolveMode: one merchant goes straight to choosing items.
func resolveMode(candidates int) string {
	if candidates > 1 {
		return ModeSelectMerchant
	}
	return ModeFastCheckout
}

// packetNode is the part of p each candidate carries into checkout.
func packetNode(p *Packet) node {
	return node{
		Title: p.Title, LocationLabel: p.LocationLabel, Lat: p.Lat, Lon: p.Lon,
		AllowNodeAsDelivery: p.AllowNodeAsDelivery, MinBasketValue: p.MinBasketValue,
	}
}

// chosenLines turns the buyer's choice into the lines to select: each
// candidate line with the quantity chosen for it, in the candidate's order.
// A line serving several packet items (one resource) is chosen by its first.
func chosenLines(lines []candidateLine, items []QuoteItem, minBasket float64) ([]candidateLine, error) {
	qty := map[string]int{}
	for _, it := range items {
		qty[it.PacketItemID] += it.Quantity
	}
	var out []candidateLine
	var subtotal float64
	for _, l := range lines {
		if len(l.PacketItemIDs) == 0 {
			continue
		}
		n, ok := qty[l.PacketItemIDs[0]]
		if !ok {
			continue
		}
		delete(qty, l.PacketItemIDs[0])
		l.Select.Quantity = n
		subtotal += l.UnitPrice * float64(n)
		out = append(out, l)
	}
	if len(qty) > 0 || len(out) == 0 {
		return nil, newErr(400, "INVALID_ITEMS", "Some chosen items aren't sold by this merchant, scan the QR code again")
	}
	if subtotal = round2(subtotal); subtotal < minBasket {
		e := newErr(422, "BELOW_MIN_BASKET", fmt.Sprintf("Add a little more: the minimum order here is %.0f", minBasket))
		e.data = map[string]any{"min_basket_value": minBasket, "subtotal": subtotal}
		return nil, e
	}
	return out, nil
}

// match runs discover around the packet's node: one call per TEXT_QUERY
// item (textSearch) and one unfiltered call for GTIN / ION_SKU items, which
// discover can't search by.
// discover runs one Beckn discover: through onix-bap (on_discover comes back
// on the webhook), unless QR_DISCOVER_METHOD=GET picks the direct shortcut.
func (s *Service) discover(ctx context.Context, textSearch string, spatial []map[string]any, buyerSubject *string) (discoverResponse, error) {
	dreq := &clientsvc.ClientDiscoverRequest{TextSearch: textSearch, Spatial: spatial, BuyerSubject: buyerSubject}
	var raw json.RawMessage
	var err error
	if s.cfg.QRDiscoverMethod == config.DiscoverMethodPost {
		raw, err = s.beckn.Discover(ctx, dreq)
	} else {
		raw, err = s.beckn.DiscoverSync(ctx, dreq)
	}
	var out discoverResponse
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

// spatialWithin is the Beckn S_DWITHIN constraint around (lat, lon).
func spatialWithin(lat, lon float64, radiusMeters int) []map[string]any {
	return []map[string]any{{
		"op":             "S_DWITHIN",
		"targets":        "$['provider']['availableAt'][*]['geo']",
		"geometry":       map[string]any{"type": "Point", "coordinates": []float64{lon, lat}},
		"distanceMeters": float64(radiusMeters),
	}}
}

func (s *Service) match(ctx context.Context, p *Packet, buyerSubject *string) (*matcher, error) {
	type job struct {
		query string
		items []int
	}
	var jobs []job
	var codeItems []int
	for i, it := range p.Items {
		if it.MatchType == "TEXT_QUERY" {
			jobs = append(jobs, job{query: it.MatchValue, items: []int{i}})
		} else {
			codeItems = append(codeItems, i)
		}
	}
	if len(codeItems) > 0 {
		jobs = append(jobs, job{items: codeItems})
	}

	spatial := spatialWithin(p.Lat, p.Lon, p.RadiusMeters)

	results := make([]discoverResponse, len(jobs))
	errs := make([]error, len(jobs))
	sem := make(chan struct{}, maxDiscoverCalls)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i], errs[i] = s.discover(ctx, j.query, spatial, buyerSubject)
		}(i, j)
	}
	wg.Wait()

	m := newMatcher(p, s.cfg.QRAllowUnlocatedMerchants)
	for i, j := range jobs {
		if errs[i] != nil {
			return nil, errs[i]
		}
		for _, idx := range j.items {
			m.add(idx, results[i])
		}
	}
	return m, nil
}

func candidateView(id uuid.UUID, rc rankedCandidate, itemsTotal int) CandidateView {
	cv := CandidateView{
		CandidateID: id.String(), ProviderID: rc.m.providerID, ProviderName: rc.m.providerName,
		BppID: rc.m.bppID, Address: rc.m.address, ItemsCovered: rc.covered, ItemsTotal: itemsTotal,
		EstimatedSubtotal: rc.subtotal, DeliveryFee: rc.deliveryFee, Currency: currencyIDR, Lines: []LineView{},
	}
	if rc.m.distance != nil {
		d := int(*rc.m.distance + 0.5)
		cv.DistanceMeters = &d
	}
	for _, l := range rc.lines {
		cv.Lines = append(cv.Lines, l.view())
	}
	return cv
}

// ---------------------------------------------------------------------------
// Quote (Beckn select)
// ---------------------------------------------------------------------------

// Quote selects the items the buyer chose from the candidate's merchant and
// returns the seller's quote. An item the seller marks unavailable fails it.
func (s *Service) Quote(ctx context.Context, req QuoteRequest, guestToken string, buyerSubject *string) (*QuoteResponse, error) {
	c, err := s.loadCandidate(ctx, req.CandidateID)
	if err != nil {
		return nil, err
	}
	if !tokenMatches(guestToken, c.tokenHash) {
		return nil, newErr(403, "INVALID_GUEST_TOKEN", "This checkout link has expired, scan the QR code again")
	}
	if time.Now().After(c.expiresAt) {
		return nil, newErr(410, "CANDIDATE_EXPIRED", "These prices are out of date, scan the QR code again")
	}
	lines, err := chosenLines(c.lines, req.Items, c.node.MinBasketValue)
	if err != nil {
		return nil, err
	}
	if c.demoScenario != "" {
		return s.quoteDemo(ctx, c, lines)
	}
	if buyerSubject == nil {
		buyerSubject = c.buyerSubject
	}

	items := make([]clientsvc.SelectItem, len(lines))
	for i, l := range lines {
		items[i] = l.Select
	}
	txn, err := s.beckn.Select(ctx, &clientsvc.ClientSelectRequest{Items: items}, buyerSubject)
	if err != nil {
		s.log().Warn().Error(err).Log("QR select failed")
		return nil, newErr(502, "BECKN_FAILED", "The merchant couldn't be reached, try again or pick another")
	}
	txnID := uuid.MustParse(txn)
	contract, err := s.waitContract(ctx, txnID, "on_select", time.Time{})
	if err != nil {
		return nil, err
	}
	if closed := closedResources(contract); len(closed) > 0 {
		var gone []string
		for _, l := range lines {
			if closed[l.Select.ResourceID] {
				gone = append(gone, l.Name)
			}
		}
		e := newErr(409, "QUOTE_UNAVAILABLE", "This merchant no longer has: "+strings.Join(gone, ", "))
		e.data = map[string]any{"unavailable_items": gone}
		return nil, e
	}
	pricing, ok := pricingOf(contract)
	if !ok {
		return nil, newErr(409, "QUOTE_UNAVAILABLE", "The merchant didn't price this order")
	}
	if err := s.insertOrder(ctx, txnID, c, buyerSubject, pricing, lines); err != nil {
		return nil, fmt.Errorf("store order: %w", err)
	}
	return s.quoteResponse(txnID, c, lines, pricing), nil
}

func (s *Service) quoteResponse(txnID uuid.UUID, c *storedCandidate, lines []candidateLine, pricing Pricing) *QuoteResponse {
	q := &QuoteResponse{
		TransactionID: txnID.String(), State: StateQuoted, Merchant: c.view,
		Pricing: pricing, Node: c.node, Lines: []LineView{},
	}
	for _, l := range lines {
		q.Lines = append(q.Lines, l.view())
	}
	return q
}

// waitContract polls for the seller's callback (action) on txnID, stored by
// the on_* webhook as a contract snapshot newer than after.
func (s *Service) waitContract(ctx context.Context, txnID uuid.UUID, action string, after time.Time) (map[string]any, error) {
	deadline := time.Now().Add(callbackWait)
	for {
		var raw json.RawMessage
		err := s.pool.QueryRow(ctx,
			`SELECT contract FROM contract_snapshots
			  WHERE transaction_id = $1 AND action = $2 AND created_at > $3
			  ORDER BY created_at DESC LIMIT 1`, txnID, action, after).Scan(&raw)
		if err == nil {
			var contract map[string]any
			if json.Unmarshal(unwrap(raw), &contract) == nil && contract != nil {
				return contract, nil
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("read %s snapshot: %w", action, err)
		}
		if time.Now().After(deadline) {
			return nil, newErr(504, "QUOTE_TIMEOUT", "The merchant didn't answer in time, try again")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
}

// ---------------------------------------------------------------------------
// Checkout (Beckn init)
// ---------------------------------------------------------------------------

// Checkout sends init with the buyer's details and the node as the delivery
// address. A total that differs from the quote is returned as 409
// QUOTE_CHANGED (the order still moves on to AWAITING_CONFIRM at the new
// total; placing it is the buyer's acceptance).
func (s *Service) Checkout(ctx context.Context, req CheckoutRequest, guestToken string) (*CheckoutResponse, error) {
	o, err := s.authorizedOrder(ctx, req.TransactionID, guestToken)
	if err != nil {
		return nil, err
	}
	if o.state != StateQuoted && o.state != StateInitSent && o.state != StateAwaitingConfirm {
		return nil, newErr(409, "INVALID_STATE", "This order is already "+strings.ToLower(o.state))
	}
	name := strings.TrimSpace(req.BuyerName)
	if name == "" || len(name) > 100 {
		return nil, newErr(400, "INVALID_NAME", "Enter your name (up to 100 characters)")
	}
	phone, ok := normalizeIDPhone(req.BuyerPhone)
	if !ok {
		return nil, newErr(400, "INVALID_PHONE", "Enter an Indonesian mobile number, e.g. 0812 3456 7890")
	}
	unit := strings.TrimSpace(req.UnitLabel)
	if len(unit) > 100 {
		return nil, newErr(400, "INVALID_UNIT", "Room / unit can be up to 100 characters")
	}

	billing := map[string]string{
		"name": name, "phone": phone, "email": strings.TrimSpace(req.BuyerEmail),
		"addressRegion": "", "postalCode": "00000", "addressCountry": "ID",
	}
	switch req.FulfillmentMode {
	case FulfillDeliveryToNode:
		if !o.node.AllowNodeAsDelivery {
			return nil, newErr(400, "NODE_DELIVERY_NOT_ALLOWED", "This packet doesn't deliver to the QR location, choose self pickup")
		}
		street := o.node.LocationLabel
		if unit != "" {
			street = unit + ", " + street
		}
		billing["streetAddress"] = street
		billing["addressLocality"] = o.node.Title
	case FulfillSelfPickup:
		billing["streetAddress"] = "Self pickup at " + o.providerName
		billing["addressLocality"] = o.node.LocationLabel
	default:
		return nil, newErr(400, "INVALID_FULFILLMENT_MODE", "Choose delivery to the QR location or self pickup")
	}
	if o.demoScenario != "" {
		return s.checkoutDemo(ctx, o, req.FulfillmentMode, unit, name, phone)
	}
	billingJSON, _ := json.Marshal(billing)

	// The database's clock, since snapshots are stamped by its now().
	var sentAt time.Time
	if err := s.pool.QueryRow(ctx, `SELECT now()`).Scan(&sentAt); err != nil {
		return nil, err
	}
	if err := s.beckn.Init(ctx, &clientsvc.ClientInitRequest{TransactionID: req.TransactionID, Billing: billingJSON}); err != nil {
		s.log().Warn().Error(err).Log("QR init failed")
		s.setOrderError(ctx, o.txnID, err.Error())
		if errors.Is(err, clientsvc.ErrQuoteUnavailable) {
			return nil, newErr(409, "QUOTE_UNAVAILABLE", "The merchant can no longer supply this order")
		}
		return nil, newErr(502, "BECKN_FAILED", "The merchant couldn't be reached, try again")
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE qr_orders SET state = $2, fulfillment_mode = $3, unit_label = NULLIF($4, ''),
		        buyer_name = $5, buyer_phone = $6, last_error = NULL, updated_at = now()
		  WHERE transaction_id = $1`, o.txnID, StateInitSent, req.FulfillmentMode, unit, name, phone); err != nil {
		return nil, err
	}

	contract, err := s.waitContract(ctx, o.txnID, "on_init", sentAt)
	if err != nil {
		return nil, err
	}
	pricing, ok := pricingOf(contract)
	if !ok {
		s.setOrderError(ctx, o.txnID, "on_init carried no price")
		return nil, newErr(409, "QUOTE_UNAVAILABLE", "The merchant didn't price this order")
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE qr_orders SET state = $2, quoted_total = $3, updated_at = now() WHERE transaction_id = $1`,
		o.txnID, StateAwaitingConfirm, pricing.GrandTotal); err != nil {
		return nil, err
	}
	out := &CheckoutResponse{
		TransactionID: o.txnID.String(), State: StateAwaitingConfirm,
		ContractID: str(contract["id"]), Pricing: pricing,
	}
	if o.total != nil && !sameAmount(*o.total, pricing.GrandTotal) {
		prev := *o.total
		out.PreviousTotal = &prev
		e := newErr(409, "QUOTE_CHANGED", "The merchant changed the total, please review it")
		e.data = out
		return nil, e
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Place order (Beckn confirm)
// ---------------------------------------------------------------------------

// PlaceOrder sends confirm for an order whose on_init is in, at the total the
// buyer agreed to (expectedAmount must equal it, or 409 QUOTE_CHANGED).
// Payment is not collected in this flow yet; a payment step goes before
// this call.
func (s *Service) PlaceOrder(ctx context.Context, txnID string, req PlaceOrderRequest, guestToken string) (*OrderView, error) {
	o, err := s.authorizedOrder(ctx, txnID, guestToken)
	if err != nil {
		return nil, err
	}
	if o.state != StateAwaitingConfirm {
		return nil, newErr(409, "INVALID_STATE", "This order can't be placed now (it is "+strings.ToLower(o.state)+")")
	}
	if o.total == nil || !sameAmount(*o.total, req.ExpectedAmount) {
		e := newErr(409, "QUOTE_CHANGED", "The total changed, please review it")
		if o.total != nil {
			e.data = map[string]any{"grand_total": *o.total, "currency": o.currency}
		}
		return nil, e
	}
	// Claim the order so a double tap sends one confirm.
	tag, err := s.pool.Exec(ctx,
		`UPDATE qr_orders SET state = $2, updated_at = now() WHERE transaction_id = $1 AND state = $3`,
		o.txnID, StateConfirmSent, StateAwaitingConfirm)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 1 {
		s.sendConfirm(context.WithoutCancel(ctx), o.txnID, o.demoScenario)
	}
	return s.Order(ctx, txnID, guestToken)
}

// sendConfirm sends Beckn confirm (or the demo stand-in). A failure leaves
// the order FAILED with the error.
func (s *Service) sendConfirm(ctx context.Context, txnID uuid.UUID, demoScenario string) {
	if demoScenario != "" {
		s.confirmDemo(ctx, txnID)
		return
	}
	s.confirm.Lock()
	defer s.confirm.Unlock()
	if err := s.beckn.Confirm(ctx, &clientsvc.ClientConfirmRequest{TransactionID: txnID.String()}); err != nil {
		s.log().Err().Error(err).Log("QR confirm failed")
		if _, err := s.pool.Exec(ctx,
			`UPDATE qr_orders SET state = $2, last_error = $3, updated_at = now() WHERE transaction_id = $1`,
			txnID, StateFailed, "confirm failed: "+err.Error()); err != nil {
			s.log().Warn().Error(err).Log("update QR order after confirm failed")
		}
	}
}

// ---------------------------------------------------------------------------
// Order
// ---------------------------------------------------------------------------

// Order returns a QR checkout's progress, advancing CONFIRM_SENT to
// CONFIRMED once the seller's on_confirm is in.
func (s *Service) Order(ctx context.Context, txnID, guestToken string) (*OrderView, error) {
	o, err := s.authorizedOrder(ctx, txnID, guestToken)
	if err != nil {
		return nil, err
	}
	var txnStatus string
	_ = s.pool.QueryRow(ctx, `SELECT status::text FROM transactions WHERE transaction_id = $1`, o.txnID).Scan(&txnStatus)

	if o.state == StateConfirmSent && txnStatus != "" && txnStatus != "CONFIRM_SENT" {
		if _, err := s.pool.Exec(ctx,
			`UPDATE qr_orders SET state = $2, updated_at = now() WHERE transaction_id = $1 AND state = $3`,
			o.txnID, StateConfirmed, StateConfirmSent); err == nil {
			o.state = StateConfirmed
		}
	}

	v := &OrderView{
		TransactionID: o.txnID.String(), PacketID: o.packetID, ProviderName: o.providerName,
		State: o.state, TransactionStatus: txnStatus, Total: o.total, Currency: o.currency,
		FulfillmentMode: o.fulfillmentMode, UnitLabel: o.unitLabel,
		BuyerName: o.buyerName, LastError: o.lastError, Node: o.node, CreatedAt: o.createdAt,
		UpdatedAt: o.updatedAt, Lines: []LineView{}, DemoScenario: o.demoScenario,
	}
	for _, l := range o.lines {
		v.Lines = append(v.Lines, l.view())
	}
	var raw json.RawMessage
	if err := s.pool.QueryRow(ctx,
		`SELECT contract FROM contract_snapshots WHERE transaction_id = $1 ORDER BY created_at DESC LIMIT 1`,
		o.txnID).Scan(&raw); err == nil {
		var contract map[string]any
		if json.Unmarshal(unwrap(raw), &contract) == nil {
			v.ContractID = str(contract["id"])
			v.FulfillmentStatus = fulfillmentStatus(contract)
		}
	}
	if o.demoScenario != "" && o.state == StateConfirmed {
		v.ContractID = "demo-" + o.txnID.String()[:8]
		v.FulfillmentStatus = demoFulfillment(o.updatedAt, time.Now())
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newGuestToken() (token, hash string) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token = "gt_" + hex.EncodeToString(b)
	return token, hashToken(token)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func tokenMatches(token, hash string) bool {
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(hash)) == 1
}

// normalizeIDPhone accepts 08…, 628… or +628… mobile numbers (spaces and
// dashes allowed) and returns +628….
func normalizeIDPhone(s string) (string, bool) {
	d := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(d, "+62"):
		d = d[3:]
	case strings.HasPrefix(d, "62"):
		d = d[2:]
	case strings.HasPrefix(d, "0"):
		d = d[1:]
	default:
		return "", false
	}
	if !strings.HasPrefix(d, "8") || len(d) < 9 || len(d) > 13 {
		return "", false
	}
	for _, r := range d {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return "+62" + d, true
}

func unwrap(raw json.RawMessage) json.RawMessage {
	var env struct {
		Message struct {
			Contract json.RawMessage `json:"contract"`
		} `json:"message"`
		Contract json.RawMessage `json:"contract"`
	}
	if json.Unmarshal(raw, &env) == nil {
		if len(env.Message.Contract) > 2 {
			return env.Message.Contract
		}
		if len(env.Contract) > 2 {
			return env.Contract
		}
	}
	return raw
}

// closedResources is the set of resource ids in commitments the seller
// marked CLOSED (no longer available).
func closedResources(contract map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, raw := range slice(contract["commitments"]) {
		c := obj(raw)
		st := obj(c["status"])
		code := str(obj(st["descriptor"])["code"])
		if code == "" {
			code = str(st["code"])
		}
		if code != "CLOSED" {
			continue
		}
		for _, r := range slice(c["resources"]) {
			if id := str(obj(r)["id"]); id != "" {
				out[id] = true
			}
		}
	}
	return out
}

// pricingOf reads the price the buyer pays from the contract's first
// consideration: breakup lines of type TAX and DELIVERY_CHARGE are split out,
// the rest are items.
func pricingOf(contract map[string]any) (Pricing, bool) {
	cons := slice(contract["consideration"])
	if len(cons) == 0 {
		cons = slice(contract["considerations"])
	}
	if len(cons) == 0 {
		return Pricing{}, false
	}
	attrs := obj(obj(cons[0])["considerationAttributes"])
	total, ok := attrs["totalAmount"].(float64)
	if !ok {
		return Pricing{}, false
	}
	p := Pricing{Currency: str(attrs["currency"]), GrandTotal: round2(total), Breakup: []BreakupLine{}}
	if p.Currency == "" {
		p.Currency = currencyIDR
	}
	for _, raw := range slice(attrs["breakup"]) {
		b := obj(raw)
		amt, _ := b["amount"].(float64)
		line := BreakupLine{Title: str(b["title"]), Type: str(b["type"]), Amount: round2(amt)}
		p.Breakup = append(p.Breakup, line)
		switch line.Type {
		case "TAX":
			p.Tax += amt
		case "DELIVERY_CHARGE", "DELIVERY":
			p.DeliveryFee += amt
		default:
			p.ItemsSubtotal += amt
		}
	}
	p.Tax, p.DeliveryFee, p.ItemsSubtotal = round2(p.Tax), round2(p.DeliveryFee), round2(p.ItemsSubtotal)
	if len(p.Breakup) == 0 {
		p.ItemsSubtotal = p.GrandTotal
	}
	return p, true
}

// fulfillmentStatus is the first performance's status code (e.g. PACKED).
func fulfillmentStatus(contract map[string]any) string {
	perf := slice(contract["performance"])
	if len(perf) == 0 {
		return ""
	}
	st := obj(obj(perf[0])["status"])
	if c := str(st["code"]); c != "" {
		return c
	}
	return str(obj(st["descriptor"])["code"])
}

func sameAmount(a, b float64) bool { d := a - b; return d < 0.005 && d > -0.005 }

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func slice(v any) []any        { s, _ := v.([]any); return s }
func str(v any) string         { s, _ := v.(string); return s }

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
