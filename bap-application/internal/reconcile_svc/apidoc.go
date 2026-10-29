package reconcilesvc

// The types in this file only describe requests and responses for the swag
// API docs (`task docs:api` -> docs/api/bap.yaml). No handler uses them; they
// mirror what the handlers write as gin.H{}, function-local or unexported
// types (HandleSend's request, BatchView with its verdicts, bppView,
// logEntry), so they must be kept in step with the handlers. Errors are
// documented with clientsvc.ErrorResponse (the same {"error": ...} body).

import (
	"encoding/json"
	"time"
)

// SendRequest is POST /api/v1/reconciliation/send's body.
type SendRequest struct {
	// The seller to reconcile with.
	BppID string `json:"bpp_id" binding:"required" example:"bpp.example.com"`
	// Contracts to send. Empty or omitted sends the default batch; ids that
	// are neither due nor disputed are ignored.
	ContractIDs []string `json:"contract_ids"`
}

// ReconciliationBatch is one reconciliation batch, sent or received
// (BatchView, with its contracts as verdicts).
type ReconciliationBatch struct {
	// This BAP's own name for the batch (not sent on the wire).
	ID string `json:"id"`
	// The exchange's context.transactionId; a disputed batch is re-sent under
	// the same one.
	TransactionID string `json:"transaction_id" format:"uuid"`
	// INBOUND: a BPP sent /reconcile; OUTBOUND: this BAP sent it.
	Direction      string `json:"direction" enums:"INBOUND,OUTBOUND"`
	SettlementDate string `json:"settlement_date" format:"date"`
	// SENDING/SENT/FAILED track delivery of an outbound batch;
	// AGREED/DISPUTED/PARTIALLY_AGREED/PENDING are outcomes (PENDING: every
	// due contract matched but some were sent before their settlement window).
	Status string `json:"status" enums:"SENDING,SENT,FAILED,AGREED,DISPUTED,PARTIALLY_AGREED,PENDING"`
	// The sender's settlementSummary.
	ClaimedSummary json.RawMessage `json:"claimed_summary" swaggertype:"object" extensions:"x-nullable"`
	// The answerer's settlementSummary.
	ComputedSummary json.RawMessage `json:"computed_summary" swaggertype:"object" extensions:"x-nullable"`
	// Per-contract verdicts.
	Contracts []Verdict `json:"contracts" extensions:"x-nullable"`
	// The /reconcile body.
	RequestPayload json.RawMessage `json:"request_payload" swaggertype:"object" extensions:"x-nullable"`
	// The /on_reconcile body.
	ResponsePayload json.RawMessage `json:"response_payload" swaggertype:"object" extensions:"x-nullable"`
	ErrorMessage    *string         `json:"error_message" extensions:"x-nullable"`
	CreatedAt       time.Time       `json:"created_at" format:"date-time"`
}

// SendResponse is POST /api/v1/reconciliation/send's answer.
type SendResponse struct {
	Batch ReconciliationBatch `json:"batch" validate:"required"`
}

// SendFailedResponse is the 502 answer of POST /api/v1/reconciliation/send:
// the batch was recorded (status FAILED) but could not be delivered.
type SendFailedResponse struct {
	Error string              `json:"error" example:"reconcile not delivered: ..." validate:"required"`
	Batch ReconciliationBatch `json:"batch" validate:"required"`
}

// BppReconciliation is one seller's reconciliation picture (bppView).
type BppReconciliation struct {
	BppID string `json:"bpp_id"`
	// What the PJP still holds for contracts not yet agreed (order amount
	// less pre-payout refunds).
	HeldAmount float64       `json:"held_amount"`
	Contracts  []DueContract `json:"contracts"`
	// Released to the BPP by agreed contracts.
	AgreedNetPayable float64 `json:"agreed_net_payable"`
	// Released to this BAP by agreed contracts.
	AgreedCommission    float64 `json:"agreed_commission"`
	RefundedAfterPayout float64 `json:"refunded_after_payout"`
	// Batches with status DISPUTED or PARTIALLY_AGREED.
	DisputedBatches int                   `json:"disputed_batches"`
	Batches         []ReconciliationBatch `json:"batches"`
	Currency        string                `json:"currency" example:"IDR"`
}

// ReconcileLogEntry is one /reconcile or /on_reconcile in this BAP's
// message log (logEntry).
type ReconcileLogEntry struct {
	ID              string          `json:"id" format:"uuid"`
	MessageID       string          `json:"message_id" format:"uuid"`
	Action          string          `json:"action" enums:"reconcile,on_reconcile"`
	Direction       string          `json:"direction" enums:"INBOUND,OUTBOUND"`
	AckStatus       *string         `json:"ack_status" enums:"ACK,NACK" extensions:"x-nullable"`
	BppID           *string         `json:"bpp_id" extensions:"x-nullable"`
	URL             *string         `json:"url" extensions:"x-nullable"`
	ErrorMessage    *string         `json:"error_message" extensions:"x-nullable"`
	RequestPayload  json.RawMessage `json:"request_payload" swaggertype:"object" extensions:"x-nullable"`
	ResponsePayload json.RawMessage `json:"response_payload" swaggertype:"object" extensions:"x-nullable"`
	CreatedAt       time.Time       `json:"created_at" format:"date-time"`
}

// ReconciliationOverviewResponse is GET /api/v1/reconciliation's answer.
type ReconciliationOverviewResponse struct {
	// One entry per BPP, sorted by bpp_id.
	Bpps []BppReconciliation `json:"bpps" validate:"required"`
	// Up to 100 most recent /reconcile and /on_reconcile messages, newest first.
	Messages []ReconcileLogEntry `json:"messages" validate:"required"`
	// Server time (UTC).
	Now time.Time `json:"now" format:"date-time" validate:"required"`
}
