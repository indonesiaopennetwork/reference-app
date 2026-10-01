package reconcilesvc

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
)

// heldOrder is one order whose payment the gateway still holds (not yet
// released by an agreed reconciliation).
type heldOrder struct {
	TransactionID string  `json:"transaction_id"`
	ContractID    string  `json:"contract_id"`
	Status        string  `json:"status"`
	Held          float64 `json:"held"`
	Paid          float64 `json:"paid"`
	GatewayRefund float64 `json:"gateway_refund"`
	// InBatch is the reconciliationId the payment is PENDING / DISPUTED in, if any.
	InBatch string `json:"in_batch,omitempty"`
}

type batchView struct {
	ID              string          `json:"id"`
	SettlementDate  string          `json:"settlement_date"`
	Status          string          `json:"status"`
	ClaimedSummary  json.RawMessage `json:"claimed_summary"`
	ComputedSummary json.RawMessage `json:"computed_summary"`
	Contracts       json.RawMessage `json:"contracts"`
	Payout          json.RawMessage `json:"payout"`
	RequestPayload  json.RawMessage `json:"request_payload"`
	ResponsePayload json.RawMessage `json:"response_payload"`
	ErrorMessage    *string         `json:"error_message"`
	CreatedAt       time.Time       `json:"created_at"`
}

type bppView struct {
	BppID      string      `json:"bpp_id"`
	HeldAmount float64     `json:"held_amount"`
	HeldOrders []heldOrder `json:"held_orders"`
	Released   float64     `json:"released"`
	Disputed   int         `json:"disputed_batches"`
	Batches    []batchView `json:"batches"`
	Currency   string      `json:"currency"`
}

// HandleAdminOverview (GET /api/v1/reconciliation): per seller (BPP),
// what the gateway still holds for it, what has been released, and every
// batch received with its verdicts, payout and messages.
func (h *Handler) HandleAdminOverview(c *gin.Context) {
	views, err := h.svc.overview(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"bpps": views})
}

func (s *Service) overview(ctx context.Context) ([]*bppView, error) {
	views := map[string]*bppView{}
	view := func(bppID string) *bppView {
		if v := views[bppID]; v != nil {
			return v
		}
		v := &bppView{BppID: bppID, HeldOrders: []heldOrder{}, Batches: []batchView{}, Currency: "IDR"}
		views[bppID] = v
		return v
	}

	// Held funds: each order's latest contract, payments and gateway refunds
	// not yet released by an AGREED / ADJUSTED reconciliation.
	rows, err := s.pool.Query(ctx, `
		SELECT t.transaction_id::text, COALESCE(t.bpp_id, ''), t.status::text, s.contract_id, s.contract
		  FROM transactions t
		  JOIN LATERAL (SELECT contract_id, contract FROM contract_snapshots
		                 WHERE transaction_id = t.transaction_id ORDER BY created_at DESC LIMIT 1) s ON TRUE
		 WHERE t.status IN ('CONFIRMED', 'CANCELLED', 'COMPLETE', 'UPDATE_SENT', 'CANCEL_SENT', 'STATUS_SENT')`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var txn, bppID, status string
		var contractID *string
		var raw json.RawMessage
		if err := rows.Scan(&txn, &bppID, &status, &contractID, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var contract map[string]any
		if json.Unmarshal(unwrap(raw), &contract) != nil {
			continue
		}
		o := heldOrder{TransactionID: txn, Status: status}
		if contractID != nil {
			o.ContractID = *contractID
		}
		var cur string
		for _, st := range slice(contract["settlements"]) {
			a := asMap(asMap(st)["settlementAttributes"])
			if rs := str(a["reconciliationStatus"]); rs == "AGREED" || rs == "ADJUSTED" {
				continue // released to the seller
			}
			if cur == "" {
				cur = str(a["currency"])
			}
			refund := asMap(a["refund"])
			switch {
			case refund == nil:
				o.Paid += num(a["settledAmount"])
				o.InBatch = str(a["reconciliationId"])
			case str(refund["source"]) != "PROVIDER":
				o.GatewayRefund += num(refund["amount"])
			}
		}
		o.Held = round2(o.Paid - o.GatewayRefund)
		if o.Paid == 0 && o.GatewayRefund == 0 {
			continue
		}
		v := view(bppID)
		if cur != "" {
			v.Currency = cur
		}
		v.HeldOrders = append(v.HeldOrders, o)
		v.HeldAmount = round2(v.HeldAmount + o.Held)
	}
	rows.Close()

	brows, err := s.pool.Query(ctx, `
		SELECT id, bpp_id, settlement_date::text, status, claimed_summary, computed_summary, contracts,
		       payout, request_payload, response_payload, error_message, created_at
		  FROM reconciliations ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer brows.Close()
	for brows.Next() {
		var b batchView
		var bppID string
		if err := brows.Scan(&b.ID, &bppID, &b.SettlementDate, &b.Status, &b.ClaimedSummary, &b.ComputedSummary,
			&b.Contracts, &b.Payout, &b.RequestPayload, &b.ResponsePayload, &b.ErrorMessage, &b.CreatedAt); err != nil {
			return nil, err
		}
		v := view(bppID)
		v.Batches = append(v.Batches, b)
		if b.Status == "DISPUTED" {
			v.Disputed++
		}
		var p map[string]any
		if json.Unmarshal(b.Payout, &p) == nil {
			v.Released = round2(v.Released + num(p["amount"]))
		}
	}
	if err := brows.Err(); err != nil {
		return nil, err
	}

	out := make([]*bppView, 0, len(views))
	for _, v := range views {
		sort.Slice(v.HeldOrders, func(i, j int) bool { return v.HeldOrders[i].Held > v.HeldOrders[j].Held })
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
