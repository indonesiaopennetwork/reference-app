package clientsvc

import (
	"encoding/json"
	"time"
)

// SelectItem represents a single line in a select request.
// The frontend supplies the offer and provider details discovered earlier,
// including the BPP identity from the catalog so the request is routed to
// the correct BPP rather than the one hard-coded in server config.
type SelectItem struct {
	ResourceID      string          `json:"resource_id"`
	OfferID         string          `json:"offer_id"`
	OfferName       string          `json:"offer_name,omitempty"`
	Quantity        int             `json:"quantity"`
	ProviderID      string          `json:"provider_id,omitempty"`
	ProviderName    string          `json:"provider_name,omitempty"`
	OfferAttributes json.RawMessage `json:"offer_attributes,omitempty"`
	// OfferConsiderations is the offer's considerations[] (pricing) exactly as
	// the discover response carried it; sent on as offer.considerations.
	OfferConsiderations json.RawMessage `json:"offer_considerations,omitempty"`
	// Resource is the resource's descriptor / resourceAttributes as discover
	// returned them; sent on in commitments[].resources[].
	Resource json.RawMessage `json:"resource,omitempty"`
	// BppID and BppURI are populated from the catalog's bppId/bppUri fields in the
	// discover response. They override the server-config defaults so the select is
	// routed to the BPP that actually owns the resource.
	BppID  string `json:"bpp_id,omitempty"`
	BppURI string `json:"bpp_uri,omitempty"`
}

// ClientSelectRequest is the payload from the React frontend to initiate a Select.
// BPP target is taken from the first item's BppID/BppURI (set from the catalog);
// server config values are used only as a fallback when those fields are empty.
type ClientSelectRequest struct {
	Items []SelectItem `json:"items" binding:"required"`
}

// ClientInitRequest — frontend initiates Init.
// Billing carries the structured buyer address/contact details; the BAP service
// patches them into the on_select contract snapshot before sending to the adapter.
type ClientInitRequest struct {
	TransactionID string          `json:"transaction_id" binding:"required"`
	Billing       json.RawMessage `json:"billing" binding:"required"`
}

// ClientConfirmRequest — frontend initiates Confirm.
type ClientConfirmRequest struct {
	TransactionID string `json:"transaction_id" binding:"required"`
}

// ClientStatusResponse returns the current state of a transaction/contract.
type ClientStatusResponse struct {
	TransactionID string          `json:"transaction_id"`
	Status        string          `json:"status"`
	Contract      json.RawMessage `json:"contract,omitempty"` // Latest snapshot
	UpdatedAt     time.Time       `json:"updated_at"`

	// UpdateRequest carries the outcome of the buyer's most recent change
	// request, when there is one. It rides along on the status poll so the
	// checkout screen can tell the buyer what the seller decided.
	//
	// Without it the buyer only ever sees the transaction return to
	// CONFIRMED, which is identical whether the seller approved or
	// rejected -- on_update has no field for the decision, so it's
	// reconstructed server-side (see internal/on_update_svc/classify.go).
	UpdateRequest *ClientUpdateOutcome `json:"update_request,omitempty"`
}

// ClientUpdateOutcome is the buyer-facing view of one change request.
type ClientUpdateOutcome struct {
	// Outcome is PENDING / APPLIED / DECLINED / MODIFIED / ABANDONED.
	Outcome   string     `json:"outcome"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

// BecknRequest is the base structure for outbound Beckn calls (select/init/confirm).
type BecknRequest struct {
	Context BecknContext    `json:"context"`
	Message json.RawMessage `json:"message"`
}

// BecknContext follows the Beckn Protocol v2.0 context schema.
// Field names are camelCase as required by the v2 spec.
type BecknContext struct {
	Version       string `json:"version"`
	Action        string `json:"action"`
	Timestamp     string `json:"timestamp"`
	MessageID     string `json:"messageId"`
	TransactionID string `json:"transactionId"`
	BapID         string `json:"bapId"`
	BapURI        string `json:"bapUri"`
	BppID         string `json:"bppId,omitempty"`
	BppURI        string `json:"bppUri,omitempty"`
	TTL           string `json:"ttl,omitempty"`
	NetworkID     string `json:"networkId"`
}

// ClientDiscoverRequest — carries the optional free-text search term from the frontend.
// Populated from the ?q query parameter on GET /api/v1/discover.
type ClientDiscoverRequest struct {
	TextSearch string // maps to intent.textSearch in the outbound Beckn request
}

// ClientCancelRequest — frontend initiates Cancel.
type ClientCancelRequest struct {
	TransactionID string `json:"transaction_id" binding:"required"`
	// ReasonCode is an ION cancellation reason (e.g. CHANGE_OF_MIND), sent as
	// contractAttributes.cancellation.reason.code.
	ReasonCode string `json:"reason_code" binding:"required"`
}

// ClientCancelItemsRequest — frontend cancels some or all units of a confirmed
// order. Cancelling everything still active is sent as /cancel; anything less
// as /update (a partial cancellation).
type ClientCancelItemsRequest struct {
	TransactionID string       `json:"transaction_id" binding:"required"`
	Items         []CancelItem `json:"items" binding:"required,min=1"`
	ReasonCode    string       `json:"reason_code" binding:"required"`
}

// ClientReturnItemsRequest — frontend returns some or all units of a delivered
// order; sent as Beckn update with a RETURN performance. The offer's return
// policy decides it (no manual approval).
type ClientReturnItemsRequest struct {
	TransactionID string       `json:"transaction_id" binding:"required"`
	Items         []CancelItem `json:"items" binding:"required,min=1"`
}

// CancelItem is a quantity of one commitment to cancel or return; quantity <= 0 means all of it.
type CancelItem struct {
	CommitmentID string  `json:"commitment_id" binding:"required"`
	Quantity     float64 `json:"quantity"`
}

// ClientRequestStatusRequest — frontend triggers a Beckn status request.
type ClientRequestStatusRequest struct {
	TransactionID string `json:"transaction_id" binding:"required"`
}

// ClientUpdateRequest — frontend triggers a Beckn update with partial contract changes.
type ClientUpdateRequest struct {
	TransactionID string          `json:"transaction_id" binding:"required"`
	Contract      json.RawMessage `json:"contract" binding:"required"`
}

// RatingInput mirrors the Beckn ratingInput shape.
type RatingInput struct {
	ID                     string          `json:"id" binding:"required"`
	RatingCategory         string          `json:"ratingCategory,omitempty"`
	Descriptor             json.RawMessage `json:"descriptor,omitempty"`
	Range                  json.RawMessage `json:"range" binding:"required"`
	FeedbackFormSubmission json.RawMessage `json:"feedbackFormSubmission,omitempty"`
}

// ClientRateRequest — frontend submits ratings.
type ClientRateRequest struct {
	TransactionID string        `json:"transaction_id" binding:"required"`
	RatingInputs  []RatingInput `json:"ratingInputs" binding:"required"`
}

// ClientSupportRequest — frontend raises a support request.
type ClientSupportRequest struct {
	TransactionID string `json:"transaction_id" binding:"required"`
	OrderID       string `json:"orderId,omitempty"`
	Description   string `json:"description,omitempty"`
}
