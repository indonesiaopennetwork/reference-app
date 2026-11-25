package qrsvc

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type storedCandidate struct {
	id            uuid.UUID
	packetID      string
	packetVersion int
	scanID        *uuid.UUID
	tokenHash     string
	buyerSubject  *string
	providerName  string
	currency      string
	lines         []candidateLine
	node          node
	view          CandidateView
	expiresAt     time.Time
	// demoScenario is set for a demo checkout (see demo.go); "" = real.
	demoScenario string
}

func (s *Service) insertCandidate(ctx context.Context, id uuid.UUID, p *Packet, scanID *uuid.UUID, tokenHash string,
	buyerSubject *string, rc rankedCandidate, nd node, demoScenario string) error {
	lines, _ := json.Marshal(rc.lines)
	nodeJSON, _ := json.Marshal(nd)
	view, _ := json.Marshal(candidateView(id, rc, len(p.Items)))
	var dist *int
	if rc.m.distance != nil {
		d := int(*rc.m.distance + 0.5)
		dist = &d
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO qr_candidates (id, packet_id, packet_version, scan_id, guest_token_hash, buyer_subject,
		    bpp_id, bpp_uri, provider_id, provider_name, distance_meters, lines, estimated_subtotal,
		    delivery_fee, currency, node, view, expires_at, demo_scenario)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,NULLIF($19, ''))`,
		id, p.ID, p.Version, scanID, tokenHash, buyerSubject,
		rc.m.bppID, rc.m.bppURI, rc.m.providerID, rc.m.providerName, dist, lines, rc.subtotal,
		rc.deliveryFee, currencyIDR, nodeJSON, view, time.Now().Add(candidateTTL), demoScenario)
	return err
}

func (s *Service) loadCandidate(ctx context.Context, id string) (*storedCandidate, error) {
	cid, err := uuid.Parse(id)
	if err != nil {
		return nil, newErr(400, "INVALID_CANDIDATE", "candidate_id must be a UUID")
	}
	c := &storedCandidate{id: cid}
	var lines, nodeJSON, view []byte
	err = s.pool.QueryRow(ctx, `
		SELECT packet_id, packet_version, scan_id, guest_token_hash, buyer_subject, provider_name,
		       currency, lines, node, view, expires_at, COALESCE(demo_scenario, '')
		  FROM qr_candidates WHERE id = $1`, cid).
		Scan(&c.packetID, &c.packetVersion, &c.scanID, &c.tokenHash, &c.buyerSubject, &c.providerName,
			&c.currency, &lines, &nodeJSON, &view, &c.expiresAt, &c.demoScenario)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, newErr(404, "CANDIDATE_NOT_FOUND", "That merchant option no longer exists, scan the QR code again")
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(lines, &c.lines); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(nodeJSON, &c.node)
	_ = json.Unmarshal(view, &c.view)
	return c, nil
}

// insertOrder stores a quoted order with the lines the buyer chose.
func (s *Service) insertOrder(ctx context.Context, txnID uuid.UUID, c *storedCandidate, buyerSubject *string, pricing Pricing, lines []candidateLine) error {
	linesJSON, _ := json.Marshal(lines)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO qr_orders (transaction_id, candidate_id, packet_id, packet_version, scan_id, guest_token_hash,
		    buyer_subject, provider_name, state, quoted_total, currency, demo_scenario, lines)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12, ''),$13)`,
		txnID, c.id, c.packetID, c.packetVersion, c.scanID, c.tokenHash,
		buyerSubject, c.providerName, StateQuoted, pricing.GrandTotal, pricing.Currency, c.demoScenario, linesJSON)
	return err
}

type storedOrder struct {
	txnID           uuid.UUID
	packetID        string
	tokenHash       string
	providerName    string
	state           string
	total           *float64
	currency        string
	fulfillmentMode string
	unitLabel       string
	buyerName       string
	lastError       string
	node            node
	lines           []candidateLine
	createdAt       time.Time
	updatedAt       time.Time
	// The merchant's per-order delivery fee from its offers, if stated.
	deliveryFee  *float64
	demoScenario string
}

const orderColumns = `
	o.transaction_id, o.packet_id, o.guest_token_hash, o.provider_name, o.state, o.quoted_total::float8,
	o.currency, COALESCE(o.fulfillment_mode, ''), COALESCE(o.unit_label, ''),
	COALESCE(o.buyer_name, ''), COALESCE(o.last_error, ''), c.node, COALESCE(o.lines, c.lines), o.created_at, o.updated_at,
	c.delivery_fee::float8, COALESCE(o.demo_scenario, '')`

func scanOrder(row pgx.Row) (*storedOrder, error) {
	o := &storedOrder{}
	var nodeJSON, lines []byte
	if err := row.Scan(&o.txnID, &o.packetID, &o.tokenHash, &o.providerName, &o.state, &o.total,
		&o.currency, &o.fulfillmentMode, &o.unitLabel, &o.buyerName, &o.lastError,
		&nodeJSON, &lines, &o.createdAt, &o.updatedAt, &o.deliveryFee, &o.demoScenario); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(nodeJSON, &o.node)
	_ = json.Unmarshal(lines, &o.lines)
	return o, nil
}

// authorizedOrder loads a QR order the guest token may act on.
func (s *Service) authorizedOrder(ctx context.Context, txnID, guestToken string) (*storedOrder, error) {
	id, err := uuid.Parse(txnID)
	if err != nil {
		return nil, newErr(400, "INVALID_TRANSACTION", "transaction_id must be a UUID")
	}
	o, err := scanOrder(s.pool.QueryRow(ctx, `SELECT `+orderColumns+`
		FROM qr_orders o JOIN qr_candidates c ON c.id = o.candidate_id
		WHERE o.transaction_id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, newErr(404, "ORDER_NOT_FOUND", "No QR order with that id")
	}
	if err != nil {
		return nil, err
	}
	if !tokenMatches(guestToken, o.tokenHash) {
		return nil, newErr(403, "INVALID_GUEST_TOKEN", "This checkout link has expired, scan the QR code again")
	}
	return o, nil
}

func (s *Service) setOrderError(ctx context.Context, txnID uuid.UUID, msg string) {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if _, err := s.pool.Exec(ctx, `UPDATE qr_orders SET last_error = $2, updated_at = now() WHERE transaction_id = $1`, txnID, msg); err != nil {
		s.log().Warn().Error(err).Log("record QR order error failed")
	}
}

// PacketOrder is one QR order in the packet console's list.
type PacketOrder struct {
	TransactionID     string   `json:"transaction_id" format:"uuid"`
	PacketVersion     int      `json:"packet_version"`
	ProviderName      string   `json:"provider_name"`
	State             string   `json:"state"`
	TransactionStatus string   `json:"transaction_status"`
	Total             *float64 `json:"total"`
	Currency          string   `json:"currency"`
	FulfillmentMode   string   `json:"fulfillment_mode,omitempty"`
	LastError         string   `json:"last_error,omitempty"`
	// Set for a simulator demo checkout (no Beckn calls).
	DemoScenario string    `json:"demo_scenario,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// PacketOrders lists a packet's QR orders, newest first (at most 100).
func (s *Service) PacketOrders(ctx context.Context, packetID string) ([]PacketOrder, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.transaction_id, o.packet_version, o.provider_name, o.state, COALESCE(t.status::text, ''),
		       o.quoted_total::float8, o.currency, COALESCE(o.fulfillment_mode, ''),
		       COALESCE(o.last_error, ''), COALESCE(o.demo_scenario, ''), o.created_at
		  FROM qr_orders o
		  LEFT JOIN transactions t ON t.transaction_id = o.transaction_id
		 WHERE o.packet_id = $1
		 ORDER BY o.created_at DESC
		 LIMIT 100`, packetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PacketOrder{}
	for rows.Next() {
		var po PacketOrder
		var txn uuid.UUID
		if err := rows.Scan(&txn, &po.PacketVersion, &po.ProviderName, &po.State, &po.TransactionStatus,
			&po.Total, &po.Currency, &po.FulfillmentMode,
			&po.LastError, &po.DemoScenario, &po.CreatedAt); err != nil {
			return nil, err
		}
		po.TransactionID = txn.String()
		out = append(out, po)
	}
	return out, rows.Err()
}
