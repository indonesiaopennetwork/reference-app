package reconsvc

import (
	"encoding/json"
	"time"
)

// The types in this file only describe requests/responses for the swag API
// docs (`task docs:api`); no code uses them. They mirror the gin.H bodies
// and function-local types of HandleOverview and HandleSend, and must be
// kept in step with those handlers.

// ReconciliationSendRequest mirrors HandleSend's body.
type ReconciliationSendRequest struct {
	BapID string `json:"bap_id" binding:"required" example:"bap.example.com"`
	// Contract UUIDs to send. Only the ones that are due or disputed are
	// included. Leave empty to send the default batch.
	ContractIDs []string `json:"contract_ids"`
}

// ReconciliationBatch mirrors Batch (a reconciliations row), with summary
// described as the Summary it holds.
type ReconciliationBatch struct {
	// reconciliationId.
	ID            string `json:"id"`
	TransactionID string `json:"transaction_id"`
	Direction     string `json:"direction" enums:"OUTBOUND,INBOUND"`
	BapID         string `json:"bap_id"`
	// Settlement date (YYYY-MM-DD).
	SettlementDate string `json:"settlement_date" format:"date"`
	// SENDING, SENT and FAILED come from this BPP's sending; AGREED,
	// DISPUTED and PENDING are the reconcile outcome.
	Status      string   `json:"status" enums:"SENDING,SENT,FAILED,AGREED,DISPUTED,PENDING"`
	ContractIDs []string `json:"contract_ids"`
	// The settlementSummary of the /reconcile.
	Summary *Summary `json:"summary" extensions:"x-nullable"`
	// The /reconcile Beckn message sent or received.
	RequestPayload json.RawMessage `json:"request_payload" swaggertype:"object" extensions:"x-nullable"`
	// The /on_reconcile Beckn message received or sent.
	ResponsePayload json.RawMessage `json:"response_payload" swaggertype:"object" extensions:"x-nullable"`
	ErrorMessage    *string         `json:"error_message" extensions:"x-nullable"`
	CreatedAt       time.Time       `json:"created_at" format:"date-time"`
	UpdatedAt       time.Time       `json:"updated_at" format:"date-time"`
}

// ReconciliationTerms are the BPP's default payout terms from config.
type ReconciliationTerms struct {
	SettlementBasis  string `json:"settlement_basis" example:"AFTER_DELIVERY"`
	SettlementWindow string `json:"settlement_window" example:"P1D"`
}

// ReconciliationBap mirrors bapGroup: one BAP's contracts in the overview.
type ReconciliationBap struct {
	BapID string `json:"bap_id" binding:"required"`
	// Contracts due now or disputed.
	Due      []DueContract `json:"due" binding:"required"`
	Upcoming []DueContract `json:"upcoming" binding:"required"`
	// Summary of the next batch, or null when there is none.
	Summary *Summary `json:"summary" binding:"required" extensions:"x-nullable"`
	// The disputed batch the next /reconcile sends again in full.
	ResendID string `json:"resend_id,omitempty"`
}

// ReconciliationOverviewResponse is GET /api/v1/reconciliation.
type ReconciliationOverviewResponse struct {
	Now   time.Time           `json:"now" binding:"required" format:"date-time"`
	Terms ReconciliationTerms `json:"terms" binding:"required"`
	Baps  []ReconciliationBap `json:"baps" binding:"required"`
	// The 50 most recent batches for this seller, newest first.
	Batches []ReconciliationBatch `json:"batches" binding:"required"`
}

// ReconciliationSendResponse is POST /api/v1/reconciliation/send on success.
type ReconciliationSendResponse struct {
	Batch ReconciliationBatch `json:"batch" binding:"required"`
}

// ReconciliationSendErrorResponse is POST /api/v1/reconciliation/send's 502:
// not delivered, the batch still recorded as FAILED.
type ReconciliationSendErrorResponse struct {
	Error string              `json:"error" binding:"required" example:"reconcile not delivered: ..."`
	Batch ReconciliationBatch `json:"batch" binding:"required"`
}
