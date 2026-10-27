package server

// The types in this file only describe requests and responses for the swag
// API docs (`task docs:api` -> docs/api/bap.yaml): the health check and the
// Beckn webhook envelopes (/api/webhook/*) shared by the on_*_svc and
// reconcile_svc handlers. No handler uses them, so they must be kept in step
// with the handlers and their packages' own request/ACK types.

import (
	"encoding/json"

	"github.com/ion/winroom/bap/internal/clientsvc"
)

// HealthResponse is GET /health's answer.
type HealthResponse struct {
	Status string `json:"status" enums:"ok" example:"ok" validate:"required"`
}

// BecknContractRequest is a Beckn callback carrying a contract (on_select,
// on_init, on_confirm, on_status, on_cancel, on_update). Only
// message.contract is read; it is stored as-is.
type BecknContractRequest struct {
	Context clientsvc.BecknContext `json:"context"`
	Message BecknContractMessage   `json:"message"`
}

// BecknContractMessage is the message of a BecknContractRequest.
type BecknContractMessage struct {
	// Opaque Beckn v2 / ION TradeContract JSON (commitments, consideration,
	// performance, settlements, contractAttributes, ...).
	Contract json.RawMessage `json:"contract" swaggertype:"object"`
}

// OnDiscoverRequest is the on_discover callback: catalog results for a
// discover this BAP sent through onix-bap.
type OnDiscoverRequest struct {
	Context clientsvc.BecknContext `json:"context"`
	Message OnDiscoverMessage      `json:"message"`
}

// OnDiscoverMessage is the message of an OnDiscoverRequest.
type OnDiscoverMessage struct {
	// Catalogs, passed through as-is.
	Catalogs json.RawMessage `json:"catalogs" swaggertype:"array,object"`
}

// OnRateRequest is the on_rate callback.
type OnRateRequest struct {
	Context clientsvc.BecknContext `json:"context"`
	Message OnRateMessage          `json:"message"`
}

// OnRateMessage is the message of an OnRateRequest.
type OnRateMessage struct {
	// Opaque Beckn ratings payload.
	Ratings json.RawMessage `json:"ratings" swaggertype:"object"`
}

// OnSupportRequest is the on_support callback.
type OnSupportRequest struct {
	Context clientsvc.BecknContext `json:"context"`
	Message OnSupportMessage       `json:"message"`
}

// OnSupportMessage is the message of an OnSupportRequest.
type OnSupportMessage struct {
	// Opaque Beckn support payload.
	Support json.RawMessage `json:"support" swaggertype:"object"`
}

// AckMessage is the message of every webhook's synchronous answer.
type AckMessage struct {
	Ack AckStatus `json:"ack" validate:"required"`
}

// AckStatus is ACK when the message was accepted for processing, NACK when
// its body could not be parsed.
type AckStatus struct {
	Status string `json:"status" enums:"ACK,NACK" validate:"required"`
}

// BecknAckResponse is the on_* webhooks' synchronous answer: the request's
// context echoed with action set to the webhook's action (on a NACK, an
// empty context except action).
type BecknAckResponse struct {
	Context clientsvc.BecknContext `json:"context" validate:"required"`
	Message AckMessage             `json:"message" validate:"required"`
}

// ReconcileAckResponse is the synchronous ACK of /reconcile and
// /on_reconcile: the request's context echoed verbatim (null if absent).
type ReconcileAckResponse struct {
	// The request's context, echoed as received.
	Context map[string]any `json:"context" swaggertype:"object" extensions:"x-nullable" validate:"required"`
	Message AckMessage     `json:"message" validate:"required"`
}

// ReconcileNackResponse is the NACK of /reconcile and /on_reconcile when the
// body could not be read or is not a JSON object. It has no context key.
type ReconcileNackResponse struct {
	Message AckMessage `json:"message" validate:"required"`
}
