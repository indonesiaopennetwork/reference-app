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
	Quantity        int             `json:"quantity" example:"1"`
	ProviderID      string          `json:"provider_id,omitempty"`
	ProviderName    string          `json:"provider_name,omitempty"`
	OfferAttributes json.RawMessage `json:"offer_attributes,omitempty" swaggertype:"object"`
	// OfferConsiderations is the offer's considerations[] (pricing) exactly as
	// the discover response carried it; sent on as offer.considerations.
	OfferConsiderations json.RawMessage `json:"offer_considerations,omitempty" swaggertype:"array,object"`
	// Resource is the resource's descriptor / resourceAttributes as discover
	// returned them; sent on in commitments[].resources[].
	Resource json.RawMessage `json:"resource,omitempty" swaggertype:"object"`
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
	TransactionID string          `json:"transaction_id" binding:"required" format:"uuid"`
	Billing       json.RawMessage `json:"billing" binding:"required" swaggertype:"object"`
}

// ClientConfirmRequest — frontend initiates Confirm.
type ClientConfirmRequest struct {
	TransactionID string `json:"transaction_id" binding:"required" format:"uuid"`
}

// ClientStatusResponse returns the current state of a transaction/contract.
type ClientStatusResponse struct {
	TransactionID string          `json:"transaction_id" format:"uuid" validate:"required"`
	Status        string          `json:"status" enums:"SELECT_SENT,QUOTE_RECEIVED,INIT_SENT,INIT_RECEIVED,CONFIRM_SENT,CONFIRMED,CANCELLED,COMPLETE,STATUS_SENT,CANCEL_SENT,UPDATE_SENT,RATE_SENT,SUPPORT_SENT,DISCOVER_SENT,DISCOVER_RECEIVED" validate:"required"`
	Contract      json.RawMessage `json:"contract,omitempty" swaggertype:"object"` // Latest snapshot
	UpdatedAt     time.Time       `json:"updated_at" format:"date-time" validate:"required"`
}

// BecknRequest is the base structure for outbound Beckn calls (select/init/confirm).
type BecknRequest struct {
	Context BecknContext    `json:"context"`
	Message json.RawMessage `json:"message"`
}

// BecknContext follows the Beckn Protocol v2.0 context schema.
// Field names are camelCase as required by the v2 spec.
type BecknContext struct {
	Version       string `json:"version" example:"2.0.0"`
	Action        string `json:"action" example:"on_select"`
	Timestamp     string `json:"timestamp" example:"2026-10-23T10:00:00Z"`
	MessageID     string `json:"messageId" format:"uuid"`
	TransactionID string `json:"transactionId" format:"uuid"`
	BapID         string `json:"bapId"`
	BapURI        string `json:"bapUri"`
	BppID         string `json:"bppId,omitempty"`
	BppURI        string `json:"bppUri,omitempty"`
	TTL           string `json:"ttl,omitempty" example:"PT30S"`
	NetworkID     string `json:"networkId"`
}

// ClientDiscoverRequest — carries the optional free-text search term from the frontend.
// Populated from the ?q query parameter on GET /api/v1/discover.
type ClientDiscoverRequest struct {
	TextSearch   string // maps to intent.textSearch in the outbound Beckn request
	ProviderName string // maps to intent.provider.descriptor.name
	// BuyerSubject is the logged-in buyer, recorded against the discover's
	// transactionId for their message log; nil for an anonymous search.
	BuyerSubject *string
}

// ClientCancelRequest — frontend initiates Cancel.
type ClientCancelRequest struct {
	TransactionID string `json:"transaction_id" binding:"required" format:"uuid"`
	// ReasonCode is an ION cancellation reason (e.g. CHANGE_OF_MIND), sent as
	// contractAttributes.cancellation.reason.code.
	ReasonCode string `json:"reason_code" binding:"required" example:"CHANGE_OF_MIND"`
}

// ClientCancelItemsRequest — frontend cancels some or all units of a confirmed
// order. Cancelling everything still active is sent as /cancel; anything less
// as /update (a partial cancellation).
type ClientCancelItemsRequest struct {
	TransactionID string       `json:"transaction_id" binding:"required" format:"uuid"`
	Items         []CancelItem `json:"items" binding:"required,min=1"`
	ReasonCode    string       `json:"reason_code" binding:"required" enums:"CHANGE_OF_MIND,WRONG_ITEM_ORDERED,ORDERED_WRONG_VARIANT,FOUND_BETTER_PRICE,DELIVERY_TOO_LONG,INCORRECT_ADDRESS,DUPLICATE_ORDER,PAYMENT_METHOD_CHANGE,ITEM_NO_LONGER_NEEDED,SELLER_UNRESPONSIVE,BUYER_FRAUD_SUSPECTED,QUALITY_ISSUE_AT_PREVIEW"`
}

// ClientReturnItemsRequest — frontend returns some or all units of a delivered
// order; sent as Beckn update with a RETURN performance. The offer's return
// policy decides it (no manual approval).
type ClientReturnItemsRequest struct {
	TransactionID string       `json:"transaction_id" binding:"required" format:"uuid"`
	Items         []CancelItem `json:"items" binding:"required,min=1"`
}

// CancelItem is a quantity of one commitment to cancel or return; quantity <= 0 means all of it.
type CancelItem struct {
	CommitmentID string  `json:"commitment_id" binding:"required"`
	Quantity     float64 `json:"quantity"`
}

// ClientRequestStatusRequest — frontend triggers a Beckn status request.
type ClientRequestStatusRequest struct {
	TransactionID string `json:"transaction_id" binding:"required" format:"uuid"`
}

// ClientUpdateRequest — frontend triggers a Beckn update with partial contract changes.
type ClientUpdateRequest struct {
	TransactionID string          `json:"transaction_id" binding:"required" format:"uuid"`
	Contract      json.RawMessage `json:"contract" binding:"required" swaggertype:"object"`
}

// RatingInput mirrors the Beckn ratingInput shape.
type RatingInput struct {
	ID                     string          `json:"id" binding:"required"`
	RatingCategory         string          `json:"ratingCategory,omitempty"`
	Descriptor             json.RawMessage `json:"descriptor,omitempty" swaggertype:"object"`
	Range                  json.RawMessage `json:"range" binding:"required" swaggertype:"object"`
	FeedbackFormSubmission json.RawMessage `json:"feedbackFormSubmission,omitempty" swaggertype:"object"`
}

// ClientRateRequest — frontend submits ratings.
type ClientRateRequest struct {
	TransactionID string        `json:"transaction_id" binding:"required" format:"uuid"`
	RatingInputs  []RatingInput `json:"ratingInputs" binding:"required"`
}

// ClientSupportRequest — frontend raises a support request.
type ClientSupportRequest struct {
	TransactionID string `json:"transaction_id" binding:"required" format:"uuid"`
	OrderID       string `json:"orderId,omitempty"`
	Description   string `json:"description,omitempty"`
}
