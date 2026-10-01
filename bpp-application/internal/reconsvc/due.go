// Package reconsvc implements the BPP side of the Beckn reconcile extension
// (docs/specs/reconcile.yaml): finding the settlements that are due, sending
// one /reconcile per BAP, and applying the BAP's /on_reconcile.
package reconsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ion/winroom/bpp/internal/cancellation"
	"github.com/ion/winroom/bpp/internal/config"
)

// DueContract is a contract with settlements not yet reconciled, and when
// they fall due.
type DueContract struct {
	ContractID    uuid.UUID `json:"contract_id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	BapID         string    `json:"bap_id"`
	BapURI        string    `json:"bap_uri"`
	NetworkID     string    `json:"network_id"`
	Status        string    `json:"status"`
	Basis         string    `json:"settlement_basis"`
	Window        string    `json:"settlement_window"`
	// BasisEvent is what started the payout clock ("delivered", "cancelled",
	// "refund"); BasisAt is when. Zero BasisAt: the event hasn't happened.
	BasisEvent string     `json:"basis_event"`
	BasisAt    *time.Time `json:"basis_at"`
	DueAt      *time.Time `json:"due_at"`
	Due        bool       `json:"due"`

	Paid           float64 `json:"paid"`
	GatewayRefund  float64 `json:"gateway_refund"`
	ProviderRefund float64 `json:"provider_refund"`
	Fees           float64 `json:"fees"`
	Currency       string  `json:"currency"`

	// SettlementIDs are the unreconciled settlements this contract contributes.
	SettlementIDs []string `json:"settlement_ids"`

	contract map[string]any
}

// Summary is the settlementSummary of docs/specs/reconcile.yaml.
type Summary struct {
	BapID                     string  `json:"bapId"`
	BppID                     string  `json:"bppId"`
	Currency                  string  `json:"currency"`
	SettlementDate            string  `json:"settlementDate"`
	SettlementBasis           string  `json:"settlementBasis,omitempty"`
	SettlementWindow          string  `json:"settlementWindow,omitempty"`
	ContractCount             int     `json:"contractCount"`
	TotalPaidAmount           float64 `json:"totalPaidAmount"`
	TotalGatewayRefundAmount  float64 `json:"totalGatewayRefundAmount"`
	TotalProviderRefundAmount float64 `json:"totalProviderRefundAmount"`
	TotalFeesRetained         float64 `json:"totalFeesRetained"`
	TotalSettledAmount        float64 `json:"totalSettledAmount"`
	NetPayable                float64 `json:"netPayable"`
}

// Contracts returns this provider's contracts that have unreconciled
// settlements, each with when it is due (due ones first, then by due time).
func (s *Service) Contracts(ctx context.Context, providerID string, now time.Time) ([]DueContract, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, transaction_id, bap_id, bap_uri, COALESCE(network_id, ''), status::text,
		       contract_attributes, delivered_at, updated_at
		  FROM contracts
		 WHERE bpp_id = $1 AND provider_id = $2 AND deleted_at IS NULL
		   AND status IN ('ACTIVE', 'CANCELLED', 'COMPLETE')`, s.cfg.BppID, providerID)
	if err != nil {
		return nil, err
	}
	type row struct {
		id, txn                    uuid.UUID
		bapID, bapURI, net, status string
		attrs                      json.RawMessage
		deliveredAt                *time.Time
		updatedAt                  pgtype.Timestamptz
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.txn, &r.bapID, &r.bapURI, &r.net, &r.status, &r.attrs, &r.deliveredAt, &r.updatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, r)
	}
	rows.Close()

	var out []DueContract
	for _, r := range list {
		contract, err := cancellation.LatestContract(ctx, s.pool, r.txn, r.attrs)
		if err != nil {
			continue // no confirmed contract payload yet
		}
		contract["id"] = r.id.String()
		contract["status"] = map[string]any{"code": r.status}
		d := DueContract{
			ContractID: r.id, TransactionID: r.txn, BapID: r.bapID, BapURI: r.bapURI,
			NetworkID: r.net, Status: r.status, contract: contract,
		}
		var cancelledAt *time.Time
		if r.status == "CANCELLED" && r.updatedAt.Valid {
			t := r.updatedAt.Time
			cancelledAt = &t
		}
		if !s.assess(&d, r.deliveredAt, cancelledAt, now) {
			continue
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Due != out[j].Due {
			return out[i].Due
		}
		if out[i].DueAt == nil || out[j].DueAt == nil {
			return out[j].DueAt == nil && out[i].DueAt != nil
		}
		return out[i].DueAt.Before(*out[j].DueAt)
	})
	return out, nil
}

// assess fills d's amounts and due time from its unreconciled settlements.
// It reports false when the contract has nothing left to reconcile.
func (s *Service) assess(d *DueContract, deliveredAt, cancelledAt *time.Time, now time.Time) bool {
	settlements := slice(d.contract["settlements"])
	var payment map[string]any
	var latestRefundAt *time.Time
	for _, raw := range settlements {
		st := asMap(raw)
		a := asMap(st["settlementAttributes"])
		refund := asMap(a["refund"])
		if refund == nil && payment == nil {
			payment = st
		}
		if str(a["reconciliationId"]) != "" {
			continue // already in a batch
		}
		d.SettlementIDs = append(d.SettlementIDs, str(st["id"]))
		if d.Currency == "" {
			d.Currency = str(a["currency"])
		}
		if refund == nil {
			d.Paid += num(a["settledAmount"])
			continue
		}
		if str(refund["source"]) == "PROVIDER" {
			d.ProviderRefund += num(refund["amount"])
		} else {
			d.GatewayRefund += num(refund["amount"])
		}
		if t, err := time.Parse(time.RFC3339, str(a["settledAt"])); err == nil && (latestRefundAt == nil || t.After(*latestRefundAt)) {
			latestRefundAt = &t
		}
	}
	if len(d.SettlementIDs) == 0 {
		return false
	}
	for _, c := range slice(d.contract["consideration"]) {
		cm := asMap(c)
		if id := str(cm["id"]); strings.HasPrefix(id, "cancellation-") || strings.HasPrefix(id, "restocking-") {
			d.Fees += num(asMap(cm["considerationAttributes"])["totalAmount"])
		}
	}

	// Payout terms come from the payment settlement; the BPP's config is the
	// fallback for settlements created before the terms existed.
	pa := asMap(asMap(payment)["settlementAttributes"])
	d.Basis, d.Window = config.NormalizeSettlementBasis(str(pa["settlementBasis"])), str(pa["settlementWindow"])
	if d.Basis == "" {
		d.Basis = s.cfg.SettlementBasis
	}
	if d.Window == "" {
		d.Window = s.cfg.SettlementWindow
	}

	// What starts the clock: a payment still to reconcile waits for the basis
	// event (or the cancellation, if that never came); settlements added after
	// the payment was reconciled count from the latest refund. Shipment isn't
	// recorded on its own, so AFTER_SHIPMENT counts from delivery.
	paymentPending := payment != nil && str(pa["reconciliationId"]) == ""
	paidAt, paidErr := time.Parse(time.RFC3339, str(pa["settledAt"]))
	switch {
	case !paymentPending && latestRefundAt != nil:
		d.BasisEvent, d.BasisAt = "refund", latestRefundAt
	case d.Basis == "ON_CONFIRMATION" && paidErr == nil:
		d.BasisEvent, d.BasisAt = "confirmed", &paidAt
	case deliveredAt != nil:
		d.BasisEvent, d.BasisAt = "delivered", deliveredAt
		if d.Basis == "AFTER_RETURN_WINDOW" {
			t := deliveredAt.Add(longestReturnWindow(d.contract))
			d.BasisEvent, d.BasisAt = "return window closed", &t
		}
	case cancelledAt != nil:
		d.BasisEvent, d.BasisAt = "cancelled", cancelledAt
	default:
		d.BasisEvent = "awaiting delivery"
		return true
	}
	window, err := cancellation.ParseISODuration(d.Window)
	if err != nil {
		window = 0
	}
	due := d.BasisAt.Add(window)
	d.DueAt = &due
	d.Due = !now.Before(due)
	return true
}

// longestReturnWindow is the widest policies.returns.window among the
// contract's offers (0 when none allows returns).
func longestReturnWindow(contract map[string]any) time.Duration {
	var longest time.Duration
	for _, c := range slice(contract["commitments"]) {
		pol := asMap(asMap(asMap(asMap(asMap(c)["offer"])["offerAttributes"])["policies"])["returns"])
		if allowed, _ := pol["allowed"].(bool); !allowed {
			continue
		}
		if d, err := cancellation.ParseISODuration(str(pol["window"])); err == nil && d > longest {
			longest = d
		}
	}
	return longest
}

// summarize totals the due contracts of one BAP (docs/specs/reconcile.yaml
// SettlementSummary).
func (s *Service) summarize(bapID string, due []DueContract, date string) Summary {
	sm := Summary{BapID: bapID, BppID: s.cfg.BppID, SettlementDate: date, ContractCount: len(due), Currency: "IDR"}
	for i, d := range due {
		if i == 0 {
			sm.SettlementBasis, sm.SettlementWindow = d.Basis, d.Window
			if d.Currency != "" {
				sm.Currency = d.Currency
			}
		}
		if d.Basis != sm.SettlementBasis {
			sm.SettlementBasis = ""
		}
		if d.Window != sm.SettlementWindow {
			sm.SettlementWindow = ""
		}
		sm.TotalPaidAmount += d.Paid
		sm.TotalGatewayRefundAmount += d.GatewayRefund
		sm.TotalProviderRefundAmount += d.ProviderRefund
		sm.TotalFeesRetained += d.Fees
	}
	sm.TotalPaidAmount = round2(sm.TotalPaidAmount)
	sm.TotalGatewayRefundAmount = round2(sm.TotalGatewayRefundAmount)
	sm.TotalProviderRefundAmount = round2(sm.TotalProviderRefundAmount)
	sm.TotalFeesRetained = round2(sm.TotalFeesRetained)
	sm.TotalSettledAmount = round2(sm.TotalPaidAmount - sm.TotalGatewayRefundAmount)
	sm.NetPayable = sm.TotalSettledAmount
	return sm
}

// stamp returns a copy of d's contract with its unreconciled settlements
// marked as part of batch reconciliationID (reconciliationStatus PENDING).
func stamp(d DueContract, reconciliationID string) (map[string]any, error) {
	b, err := json.Marshal(d.contract)
	if err != nil {
		return nil, err
	}
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	include := map[string]bool{}
	for _, id := range d.SettlementIDs {
		include[id] = true
	}
	for _, raw := range slice(c["settlements"]) {
		st := asMap(raw)
		if include[str(st["id"])] {
			a := asMap(st["settlementAttributes"])
			a["reconciliationId"], a["reconciliationStatus"] = reconciliationID, "PENDING"
		}
	}
	return c, nil
}

// reconciliationID names a batch by when it is sent and to whom:
// recon-<settlement date>T<HHMMSS>Z-<bapId>, e.g.
// recon-2026-09-29T183205Z-bap.example.com. The -N suffix only
// disambiguates two batches to the same BAP within one second.
func reconciliationID(at time.Time, bapID string, taken func(string) bool) string {
	base := fmt.Sprintf("recon-%sZ-%s", at.UTC().Format("2006-01-02T150405"), bapID)
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		if id := fmt.Sprintf("%s-%d", base, n); !taken(id) {
			return id
		}
	}
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func slice(v any) []any          { s, _ := v.([]any); return s }
func str(v any) string           { s, _ := v.(string); return s }
func num(v any) float64          { f, _ := v.(float64); return f }
func round2(f float64) float64   { return math.Round(f*100) / 100 }
