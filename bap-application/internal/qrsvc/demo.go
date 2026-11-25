package qrsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/ion/winroom/bap/internal/clientsvc"
)

// Demo scenarios for the QR scan simulator. With QR_DEMO_ENABLED=true a scan
// may ask for one (ResolveRequest.DemoScenario); this BAP then plays the
// merchants itself: no discover, select, init or confirm goes out. The rest
// is the real flow: candidates, orders, the order states
// and the console's live activity all use the same tables and code.
//
// Prices are deterministic per item (same packet, same totals). Demo orders
// get a transactions row without a buyer, so they never appear in anyone's
// shop order history.
const (
	DemoSingle       = "SINGLE"        // one merchant: fast checkout
	DemoMulti        = "MULTI"         // three merchants: the buyer picks
	DemoQuoteChanged = "QUOTE_CHANGED" // the price changes at checkout
)

// demoBppID marks a demo transaction's seller.
const demoBppID = "demo.qr.local"

type demoMerchant struct {
	id, name, address string
	distance          int
	priceFactor       float64
	deliveryFee       float64
}

var demoMerchants = []demoMerchant{
	{"demo-warung-bu-sri", "Warung Bu Sri (demo)", "Jl. Margonda Raya Gg. Melati No. 3", 350, 1.00, 3000},
	{"demo-toko-pak-budi", "Toko Pak Budi (demo)", "Jl. Kukusan Raya No. 21", 820, 0.96, 5000},
	{"demo-minimarket-sejahtera", "Minimarket Sejahtera (demo)", "Jl. Margonda Raya No. 410", 1450, 1.05, 7000},
}

// Demo fulfilment: seconds after confirmation each stage is shown.
var demoStages = []struct {
	after time.Duration
	code  string
}{
	{0, "PENDING"}, {10 * time.Second, "PACKED"}, {25 * time.Second, "OUT_FOR_DELIVERY"}, {45 * time.Second, "DELIVERED"},
}

func validDemoScenario(s string) bool {
	return s == DemoSingle || s == DemoMulti || s == DemoQuoteChanged
}

// demoUnitPrice is a stable price per item: IDR 2,000 .. 15,000 in steps of 500.
func demoUnitPrice(it PacketItem) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(it.MatchType + ":" + it.MatchValue))
	return float64(2000 + (h.Sum32()%27)*500)
}

// resolveDemo answers a resolve with demo merchants instead of discover.
func (s *Service) resolveDemo(ctx context.Context, p *Packet, scenario string, scanID *uuid.UUID,
	resp *ResolveResponse) (*ResolveResponse, error) {
	if !s.cfg.QRDemoEnabled {
		return nil, newErr(400, "DEMO_DISABLED", "Demo scenarios are off on this BAP (QR_DEMO_ENABLED)")
	}
	if !validDemoScenario(scenario) {
		return nil, newErr(400, "INVALID_DEMO_SCENARIO", "demo_scenario must be SINGLE, MULTI or QUOTE_CHANGED")
	}
	resp.DemoScenario = scenario
	merchants := demoMerchants[:1]
	if scenario == DemoMulti {
		merchants = demoMerchants
	}

	token, tokenHash := newGuestToken()
	resp.GuestToken = token
	nd := packetNode(p)
	for i, dm := range merchants {
		rc := demoCandidate(p, dm)
		id := uuid.New()
		if err := s.insertCandidate(ctx, id, p, scanID, tokenHash, nil, rc, nd, scenario); err != nil {
			return nil, fmt.Errorf("store demo candidate: %w", err)
		}
		cv := candidateView(id, rc, len(p.Items))
		cv.Recommended = i == 0
		resp.Candidates = append(resp.Candidates, cv)
	}
	resp.Mode = resolveMode(len(resp.Candidates))
	return resp, nil
}

// demoCandidate prices every packet item at dm (demo merchants stock everything).
func demoCandidate(p *Packet, dm demoMerchant) rankedCandidate {
	dist := float64(dm.distance)
	fee := dm.deliveryFee
	rc := rankedCandidate{
		m: &merchant{
			key: demoBppID + "|" + dm.id, bppID: demoBppID, providerID: dm.id, providerName: dm.name,
			distance: &dist, address: dm.address,
		},
		deliveryFee: &fee,
	}
	for _, it := range p.Items {
		unit := math.Round(demoUnitPrice(it)*dm.priceFactor/100) * 100
		rc.lines = append(rc.lines, candidateLine{
			PacketItemIDs: []string{it.ID}, Name: it.DisplayName, UnitPrice: unit,
			Select: clientsvc.SelectItem{
				ResourceID: "demo-" + it.ID, OfferID: "demo-offer-" + it.ID, OfferName: it.DisplayName,
				Quantity: 1, ProviderID: dm.id, ProviderName: dm.name, BppID: demoBppID,
			},
		})
		rc.covered++
		rc.subtotal += unit
	}
	rc.subtotal = round2(rc.subtotal)
	return rc
}

// demoPricing prices lines plus the delivery fee; no tax.
func demoPricing(lines []candidateLine, fee *float64) Pricing {
	p := Pricing{Currency: currencyIDR, Breakup: []BreakupLine{}}
	for _, l := range lines {
		amt := round2(l.UnitPrice * float64(l.Select.Quantity))
		p.Breakup = append(p.Breakup, BreakupLine{Title: l.Name, Type: "BASE_PRICE", Amount: amt})
		p.ItemsSubtotal += amt
	}
	if fee != nil && *fee > 0 {
		p.DeliveryFee = *fee
		p.Breakup = append(p.Breakup, BreakupLine{Title: "Delivery fee", Type: "DELIVERY_CHARGE", Amount: *fee})
	}
	p.ItemsSubtotal = round2(p.ItemsSubtotal)
	p.GrandTotal = round2(p.ItemsSubtotal + p.DeliveryFee)
	return p
}

// quoteDemo stands in for select + on_select, for the buyer's chosen lines.
func (s *Service) quoteDemo(ctx context.Context, c *storedCandidate, lines []candidateLine) (*QuoteResponse, error) {
	txnID := uuid.New()
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO transactions (transaction_id, bap_id, network_id, bpp_id, bpp_uri, status)
		 VALUES ($1, $2, $3, $4, '', 'QUOTE_RECEIVED')`,
		txnID, s.cfg.BapID, s.cfg.NetworkID, demoBppID); err != nil {
		return nil, fmt.Errorf("store demo transaction: %w", err)
	}
	pricing := demoPricing(lines, c.view.DeliveryFee)
	if err := s.insertOrder(ctx, txnID, c, nil, pricing, lines); err != nil {
		return nil, fmt.Errorf("store demo order: %w", err)
	}
	return s.quoteResponse(txnID, c, lines, pricing), nil
}

// checkoutDemo stands in for init + on_init. In QUOTE_CHANGED, the first
// checkout raises the first item's price and answers QUOTE_CHANGED like a
// real changed on_init.
func (s *Service) checkoutDemo(ctx context.Context, o *storedOrder, mode, unit, name, phone string) (*CheckoutResponse, error) {
	lines := o.lines
	changed := false
	if o.demoScenario == DemoQuoteChanged && o.state == StateQuoted && len(lines) > 0 {
		changed = true
		lines = append([]candidateLine(nil), lines...)
		lines[0].UnitPrice += 1500
	}
	pricing := demoPricing(lines, o.deliveryFee)
	linesJSON, _ := json.Marshal(lines)
	if _, err := s.pool.Exec(ctx,
		`UPDATE qr_orders SET state = $2, fulfillment_mode = $3, unit_label = NULLIF($4, ''), buyer_name = $5,
		        buyer_phone = $6, quoted_total = $7, lines = $8, last_error = NULL, updated_at = now()
		  WHERE transaction_id = $1`,
		o.txnID, StateAwaitingConfirm, mode, unit, name, phone, pricing.GrandTotal, linesJSON); err != nil {
		return nil, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE transactions SET status = 'INIT_RECEIVED', updated_at = now() WHERE transaction_id = $1`, o.txnID)
	out := &CheckoutResponse{TransactionID: o.txnID.String(), State: StateAwaitingConfirm, ContractID: "demo-" + o.txnID.String()[:8], Pricing: pricing}
	if changed && o.total != nil && !sameAmount(*o.total, pricing.GrandTotal) {
		prev := *o.total
		out.PreviousTotal = &prev
		e := newErr(409, "QUOTE_CHANGED", "The merchant changed the total, please review it")
		e.data = out
		return nil, e
	}
	return out, nil
}

// confirmDemo stands in for confirm + on_confirm.
func (s *Service) confirmDemo(ctx context.Context, txnID uuid.UUID) {
	_, _ = s.pool.Exec(ctx, `UPDATE transactions SET status = 'CONFIRMED', updated_at = now() WHERE transaction_id = $1`, txnID)
	if _, err := s.pool.Exec(ctx,
		`UPDATE qr_orders SET state = $2, last_error = NULL, updated_at = now() WHERE transaction_id = $1`,
		txnID, StateConfirmed); err != nil {
		s.log().Warn().Error(err).Log("update demo QR order after confirm failed")
	}
}

// demoFulfillment is the demo order's stage, from the time since it was confirmed.
func demoFulfillment(confirmedAt, now time.Time) string {
	code := demoStages[0].code
	for _, st := range demoStages {
		if now.Sub(confirmedAt) >= st.after {
			code = st.code
		}
	}
	return code
}
