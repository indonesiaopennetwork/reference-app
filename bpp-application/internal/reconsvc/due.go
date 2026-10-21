// Package reconsvc implements the BPP side of ION settlement reconciliation
// (ION Release 1 core/api/v2.0.0/ion-additional.yaml in ion-specs):
// finding the contracts whose settlement is due, sending /reconcile to a BAP
// and applying its /on_reconcile, and answering a BAP's /reconcile.
package reconsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ion/winroom/bpp/internal/cancellation"
	"github.com/ion/winroom/bpp/internal/settlement"
)

// DueContract is a contract whose settlement is not yet agreed, and when it
// falls due.
type DueContract struct {
	ContractID    uuid.UUID `json:"contract_id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	BapID         string    `json:"bap_id"`
	BapURI        string    `json:"bap_uri"`
	NetworkID     string    `json:"network_id"`
	Status        string    `json:"status"`
	// ReconciliationStatus is the payment's state in reconciliation: "" (not
	// sent yet, or pending in its last batch) or DISPUTED (sent again).
	ReconciliationStatus string `json:"reconciliation_status,omitempty"`
	// ReconciliationID is the batch the contract was last in.
	ReconciliationID string `json:"reconciliation_id,omitempty"`
	// InNextBatch: the contract goes in this BPP's next /reconcile to its BAP
	// (see nextBatch).
	InNextBatch bool `json:"in_next_batch"`
	// Selectable: the operator can choose the contract for a /reconcile (due
	// or disputed; see choose).
	Selectable bool `json:"selectable"`
	// CreatedAt is when the contract was created.
	CreatedAt time.Time `json:"created_at"`
	// LastResult: for a contract its last batch didn't reconcile (disputed,
	// or sent before its settlement window), how it fared there.
	LastResult *Result `json:"last_result,omitempty"`
	// BasisEvent is what started the payout clock ("delivered", "cancelled",
	// ...); BasisAt is when. Zero BasisAt: the event hasn't happened.
	BasisEvent string     `json:"basis_event"`
	BasisAt    *time.Time `json:"basis_at"`
	DueAt      *time.Time `json:"due_at"`
	Due        bool       `json:"due"`

	*Figures

	contract map[string]any
}

// Contracts returns this provider's contracts whose settlement is not yet
// agreed, each with when it is due (due ones first, then by due time).
func (s *Service) Contracts(ctx context.Context, providerID string, now time.Time) ([]DueContract, error) {
	return s.contracts(ctx, `AND provider_id = $2`, []any{s.cfg.BppID, providerID}, now)
}

// contractsWithBap is every contract with a BAP whose settlement is not yet
// agreed, across this BPP's providers.
func (s *Service) contractsWithBap(ctx context.Context, bapID string, now time.Time) ([]DueContract, error) {
	return s.contracts(ctx, `AND bap_id = $2`, []any{s.cfg.BppID, bapID}, now)
}

func (s *Service) contracts(ctx context.Context, filter string, args []any, now time.Time) ([]DueContract, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, transaction_id, bap_id, bap_uri, COALESCE(network_id, ''), status::text,
		       contract_attributes, delivered_at, updated_at, created_at
		  FROM contracts
		 WHERE bpp_id = $1 `+filter+` AND deleted_at IS NULL
		   AND status IN ('ACTIVE', 'CANCELLED', 'COMPLETE')`, args...)
	if err != nil {
		return nil, err
	}
	type row struct {
		id, txn                    uuid.UUID
		bapID, bapURI, net, status string
		attrs                      json.RawMessage
		deliveredAt                *time.Time
		updatedAt                  pgtype.Timestamptz
		createdAt                  time.Time
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.txn, &r.bapID, &r.bapURI, &r.net, &r.status, &r.attrs, &r.deliveredAt, &r.updatedAt, &r.createdAt); err != nil {
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
			NetworkID: r.net, Status: r.status, CreatedAt: r.createdAt, contract: contract,
		}
		var cancelledAt *time.Time
		if r.status == "CANCELLED" && r.updatedAt.Valid {
			t := r.updatedAt.Time
			cancelledAt = &t
		}
		if !s.assess(&d, r.deliveredAt, cancelledAt, now) {
			continue
		}
		if s.cfg.ReconcileBeforeWindow { // RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW=false
			d.Due = true
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
	s.attachResults(ctx, out)
	return out, nil
}

// assess fills d's figures and due time. It reports false when the contract
// has nothing to reconcile: no payment, or its payment is already in a
// pending batch or agreed. A DISPUTED payment is reconciled again.
func (s *Service) assess(d *DueContract, deliveredAt, cancelledAt *time.Time, now time.Time) bool {
	payment := paymentOf(d.contract)
	if payment == nil {
		return false
	}
	pa := asMap(payment["settlementAttributes"])
	switch status := str(pa["reconciliationStatus"]); status {
	case "PENDING", "AGREED", "ADJUSTED":
		return false
	default:
		d.ReconciliationStatus = status
		d.ReconciliationID = str(pa["reconciliationId"])
	}
	f, err := contractFigures(d.contract, s.terms())
	if err != nil {
		return false
	}
	d.Figures = f

	// When each payout term falls due; the contract falls due at the latest.
	paidAt, paidErr := time.Parse(time.RFC3339, firstNonEmpty(str(pa["paidAt"]), str(pa["settledAt"])))
	var paid *time.Time
	if paidErr == nil {
		paid = &paidAt
	}
	for _, t := range f.Terms {
		event, at, due, ok := termDue(t, paid, deliveredAt, cancelledAt, d.contract)
		if !ok {
			// A term whose event hasn't happened: the due date isn't known yet.
			f.Basis, f.Window = t.Basis, t.Window
			d.BasisEvent, d.BasisAt, d.DueAt, d.Due = event, nil, nil, false
			f.OrderDate = settlement.OrderDate(d.CreatedAt)
			return true
		}
		if d.DueAt == nil || due.After(*d.DueAt) {
			f.Basis, f.Window = t.Basis, t.Window
			d.BasisEvent, d.BasisAt, d.DueAt = event, at, &due
		}
	}
	d.Due = d.DueAt != nil && !now.Before(*d.DueAt)
	f.OrderDate = settlement.OrderDate(d.CreatedAt)
	if d.DueAt != nil {
		f.DueDate = settlement.Date(*d.DueAt)
	}
	return true
}

// termDue is when a payout term falls due: what starts its clock, when, and
// the due time; ok is false while that event hasn't happened. Shipment isn't
// recorded on its own, so AFTER_SHIPMENT counts from delivery; a contract
// cancelled before delivery counts from the cancellation.
func termDue(t settlement.Term, paidAt, deliveredAt, cancelledAt *time.Time, contract map[string]any) (event string, at *time.Time, due time.Time, ok bool) {
	switch {
	case t.Basis == "ON_CONFIRMATION" && paidAt != nil:
		event, at = "confirmed", paidAt
	case deliveredAt != nil:
		event, at = "delivered", deliveredAt
		if t.Basis == "AFTER_RETURN_WINDOW" {
			closed := deliveredAt.Add(cancellation.LongestReturnWindow(contract))
			event, at = "return window closed", &closed
		}
	case cancelledAt != nil:
		event, at = "cancelled", cancelledAt
	default:
		return "awaiting delivery", nil, time.Time{}, false
	}
	window, err := cancellation.ParseISODuration(t.Window)
	if err != nil {
		window = 0
	}
	return event, at, at.Add(window), true
}

// reconciliationID names a batch by when it is sent and who sends it:
// recon-<settlement date>T<HHMMSS>Z-<senderId>, e.g.
// recon-2026-09-29T183205Z-bpp.example.com. The -N suffix only
// disambiguates two batches from the same sender within one second.
func reconciliationID(at time.Time, senderID string, taken func(string) bool) string {
	base := fmt.Sprintf("recon-%sZ-%s", at.UTC().Format("2006-01-02T150405"), senderID)
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		if id := fmt.Sprintf("%s-%d", base, n); !taken(id) {
			return id
		}
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func slice(v any) []any          { s, _ := v.([]any); return s }
func str(v any) string           { s, _ := v.(string); return s }
func num(v any) float64          { f, _ := v.(float64); return f }
func round2(f float64) float64   { return math.Round(f*100) / 100 }
