package server

import "encoding/json"

// The types in this file only describe requests/responses for the swag API
// docs (`task docs:api`); no code uses them. Keep them in step with the
// handlers they describe (here, in catalog, and in the *svc webhook
// packages, whose annotations refer to these types as server.<Type>).

// WscMessage mirrors alya wscutils.ErrorMessage.
type WscMessage struct {
	MsgID   int      `json:"msgid" binding:"required" example:"1001"`
	ErrCode string   `json:"errcode" binding:"required" example:"missing"`
	Field   string   `json:"field,omitempty"`
	Vals    []string `json:"vals,omitempty"`
}

// WscSuccessResponse mirrors the alya wscutils.Response success envelope with
// no data (wscutils.NewSuccessResponse(nil)).
type WscSuccessResponse struct {
	Status string `json:"status" binding:"required" enums:"success" example:"success"`
	// Always null here.
	Data any `json:"data" swaggertype:"object" extensions:"x-nullable"`
	// Always null on success.
	Messages []WscMessage `json:"messages" extensions:"x-nullable"`
}

// WscErrorResponse mirrors the alya wscutils.Response error envelope
// (wscutils.NewErrorResponse).
type WscErrorResponse struct {
	Status string `json:"status" binding:"required" enums:"error" example:"error"`
	// Always null on error.
	Data     any          `json:"data" swaggertype:"object" extensions:"x-nullable"`
	Messages []WscMessage `json:"messages" binding:"required"`
}

// HealthData is the data of GET /health.
type HealthData struct {
	Status string `json:"status" binding:"required" example:"ok"`
}

// HealthResponse is GET /health's alya success envelope.
type HealthResponse struct {
	Status   string       `json:"status" binding:"required" enums:"success" example:"success"`
	Data     HealthData   `json:"data" binding:"required"`
	Messages []WscMessage `json:"messages" extensions:"x-nullable"`
}

// BecknContext is the Beckn context the webhook handlers read and echo back
// (the BecknContext type each *svc package declares).
type BecknContext struct {
	Version   string `json:"version" example:"2.0.0"`
	Action    string `json:"action" example:"select"`
	Timestamp string `json:"timestamp" format:"date-time" example:"2026-10-23T10:00:00Z"`
	MessageID string `json:"messageId" format:"uuid" example:"6f1c2a1e-1111-4a5b-9c1d-000000000001"`
	// Used to find the contract (status, rate, update, cancel).
	TransactionID string `json:"transactionId" format:"uuid" example:"6f1c2a1e-2222-4a5b-9c1d-000000000002"`
	BapID         string `json:"bapId" example:"bap.example.com"`
	// Required (non-empty) by select, init, confirm, status, update, cancel,
	// rate and support; otherwise the reply is a 400 NACK.
	BapURI    string `json:"bapUri" example:"https://bap.example.com/bap/receiver"`
	BppID     string `json:"bppId" example:"bpp.example.com"`
	BppURI    string `json:"bppUri" example:"https://bpp.example.com/bpp/receiver"`
	TTL       string `json:"ttl,omitempty" example:"PT30S"`
	NetworkID string `json:"networkId" example:"ion.id/ion-launch"`
}

// BecknWebhookRequest is the Beckn envelope onix-bpp's receiver forwards to
// every /api/webhook route.
type BecknWebhookRequest struct {
	Context BecknContext `json:"context" binding:"required"`
	// Beckn message for the action. It usually carries contract (select,
	// init, confirm, update, cancel, and status with contract.id),
	// ratingInputs (rate), support (support), or reconciliationId, contracts
	// and settlementSummary (reconcile, on_reconcile).
	Message map[string]any `json:"message" binding:"required" swaggertype:"object"`
}

// AckStatus is the ack of a Beckn ACK/NACK reply.
type AckStatus struct {
	Status string `json:"status" binding:"required" enums:"ACK,NACK" example:"ACK"`
}

// AckMessage is the message of a Beckn ACK/NACK reply.
type AckMessage struct {
	Ack AckStatus `json:"ack" binding:"required"`
}

// BecknAckResponse is the synchronous ACK (200) or NACK (400) of the Beckn
// action webhooks. On ACK the context echoes the inbound context; on a NACK
// for an undecodable body every context field is an empty string.
type BecknAckResponse struct {
	Context BecknContext `json:"context" binding:"required"`
	Message AckMessage   `json:"message" binding:"required"`
}

// ReconAckResponse is the synchronous ACK of /reconcile and /on_reconcile:
// context is the inbound context exactly as received (raw JSON), or null if
// the body had none.
type ReconAckResponse struct {
	Context json.RawMessage `json:"context" binding:"required" swaggertype:"object" extensions:"x-nullable"`
	Message AckMessage      `json:"message" binding:"required"`
}

// ReconNackResponse is the 400 NACK of /reconcile and /on_reconcile when the
// body is not a JSON object. It has no context.
type ReconNackResponse struct {
	Message AckMessage `json:"message" binding:"required"`
}
