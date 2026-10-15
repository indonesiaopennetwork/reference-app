package reconcilesvc

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
)

// BatchView is one reconciliation batch, sent or received.
type BatchView struct {
	ID              string          `json:"id"`
	Direction       string          `json:"direction"`
	SettlementDate  string          `json:"settlement_date"`
	Status          string          `json:"status"`
	ClaimedSummary  json.RawMessage `json:"claimed_summary"`
	ComputedSummary json.RawMessage `json:"computed_summary"`
	Contracts       json.RawMessage `json:"contracts"`
	RequestPayload  json.RawMessage `json:"request_payload"`
	ResponsePayload json.RawMessage `json:"response_payload"`
	ErrorMessage    *string         `json:"error_message"`
	CreatedAt       time.Time       `json:"created_at"`
}

// bppView is one seller's reconciliation picture from this BAP's side.
type bppView struct {
	BppID string `json:"bpp_id"`
	// Held is what the PJP still holds for contracts not yet agreed
	// (order amount less pre-payout refunds).
	Held      float64       `json:"held_amount"`
	Contracts []DueContract `json:"contracts"`
	// AgreedNetPayable / AgreedCommission are what agreed contracts release:
	// to the BPP and to this BAP. SEAM makes the payout through the PJP.
	AgreedNetPayable float64 `json:"agreed_net_payable"`
	AgreedCommission float64 `json:"agreed_commission"`
	// RefundedAfterPayout is refunded by the BPP itself after a payout --
	// outside reconciliation.
	RefundedAfterPayout float64     `json:"refunded_after_payout"`
	Disputed            int         `json:"disputed_batches"`
	Batches             []BatchView `json:"batches"`
	Currency            string      `json:"currency"`
}

// logEntry is one /reconcile or /on_reconcile in this BAP's message log.
type logEntry struct {
	ID              string          `json:"id"`
	MessageID       string          `json:"message_id"`
	Action          string          `json:"action"`
	Direction       string          `json:"direction"`
	AckStatus       *string         `json:"ack_status"`
	BppID           *string         `json:"bpp_id"`
	URL             *string         `json:"url"`
	ErrorMessage    *string         `json:"error_message"`
	RequestPayload  json.RawMessage `json:"request_payload"`
	ResponsePayload json.RawMessage `json:"response_payload"`
	CreatedAt       time.Time       `json:"created_at"`
}

// HandleAdminOverview (GET /api/v1/reconciliation): per seller (BPP), the
// contracts still to reconcile (and which are due), what the PJP holds for
// them, what agreed contracts release, and every batch sent or received;
// plus the recent /reconcile and /on_reconcile messages, sent or received.
func (h *Handler) HandleAdminOverview(c *gin.Context) {
	ctx := c.Request.Context()
	views, err := h.svc.overview(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	messages, err := h.svc.messages(ctx, 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"bpps": views, "messages": messages, "now": time.Now().UTC()})
}

// messages is this BAP's recent /reconcile and /on_reconcile message log,
// newest first. Reconciliation is between the participants, not any buyer's,
// so it is not in the buyer's message log.
func (s *Service) messages(ctx context.Context, limit int) ([]logEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.id::text, m.message_id::text, m.action::text, m.direction::text, m.ack_status::text, m.bpp_id,
		       m.url, m.error_message, m.request_payload, m.response_payload, m.created_at
		  FROM beckn_message_log m
		 WHERE m.bap_id = $1 AND m.action IN ('reconcile', 'on_reconcile')
		 ORDER BY m.created_at DESC LIMIT $2`, s.cfg.BapID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []logEntry{}
	for rows.Next() {
		var e logEntry
		if err := rows.Scan(&e.ID, &e.MessageID, &e.Action, &e.Direction, &e.AckStatus, &e.BppID,
			&e.URL, &e.ErrorMessage, &e.RequestPayload, &e.ResponsePayload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) overview(ctx context.Context) ([]*bppView, error) {
	views := map[string]*bppView{}
	view := func(bppID string) *bppView {
		if v := views[bppID]; v != nil {
			return v
		}
		v := &bppView{BppID: bppID, Contracts: []DueContract{}, Batches: []BatchView{}, Currency: "IDR"}
		views[bppID] = v
		return v
	}

	open, err := s.Contracts(ctx, "", time.Now().UTC())
	if err != nil {
		return nil, err
	}
	for _, d := range open {
		v := view(d.BppID)
		v.Contracts = append(v.Contracts, d)
		v.Held = round2(v.Held + d.OrderAmount - d.Refund)
		v.RefundedAfterPayout = round2(v.RefundedAfterPayout + d.RefundedAfterPayout)
		if d.Currency != "" {
			v.Currency = d.Currency
		}
	}
	for _, v := range views {
		var due []DueContract
		for _, d := range v.Contracts {
			if selectable(d) {
				due = append(due, d)
			}
		}
		_, batch := nextBatch(due, s.cfg.BapID)
		next := map[string]bool{}
		for _, d := range batch {
			next[d.ContractID] = true
		}
		for i := range v.Contracts {
			v.Contracts[i].InNextBatch = next[v.Contracts[i].ContractID]
			v.Contracts[i].Selectable = selectable(v.Contracts[i])
		}
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, direction, bpp_id, settlement_date::text, status, claimed_summary, computed_summary, contracts,
		       request_payload, response_payload, error_message, created_at
		  FROM reconciliations ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var b BatchView
		var bppID string
		if err := rows.Scan(&b.ID, &b.Direction, &bppID, &b.SettlementDate, &b.Status, &b.ClaimedSummary, &b.ComputedSummary,
			&b.Contracts, &b.RequestPayload, &b.ResponsePayload, &b.ErrorMessage, &b.CreatedAt); err != nil {
			return nil, err
		}
		v := view(bppID)
		v.Batches = append(v.Batches, b)
		if b.Status == "DISPUTED" || b.Status == "PARTIALLY_AGREED" {
			v.Disputed++
		}
		var verdicts []Verdict
		if b.Status == "AGREED" && json.Unmarshal(b.Contracts, &verdicts) == nil {
			for _, vd := range verdicts {
				if vd.Accord {
					v.AgreedNetPayable = round2(v.AgreedNetPayable + vd.NetPayable)
					v.AgreedCommission = round2(v.AgreedCommission + vd.Commission)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]*bppView, 0, len(views))
	for _, v := range views {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BppID < out[j].BppID })
	return out, nil
}

// unwrap returns the contract of a snapshot stored either bare or as
// {"message":{"contract":…}}.
func unwrap(raw json.RawMessage) json.RawMessage {
	var env struct {
		Message struct {
			Contract json.RawMessage `json:"contract"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &env) == nil && len(env.Message.Contract) > 2 {
		return env.Message.Contract
	}
	return raw
}
