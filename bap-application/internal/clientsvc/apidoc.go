package clientsvc

// The types in this file only describe requests and responses for the swag
// API docs (`task docs:api` -> docs/api/bap.yaml). No handler uses them; they
// mirror what the handlers write as gin.H{} or as function-local types, so
// they must be kept in step with the handlers.

import "encoding/json"

// ErrorResponse is the body of every client API error.
type ErrorResponse struct {
	// Human-readable error (often the underlying Go error text).
	Error string `json:"error" example:"transaction not found" validate:"required"`
}

// SelectResponse is POST /api/v1/select's answer.
type SelectResponse struct {
	TransactionID string `json:"transaction_id" format:"uuid" validate:"required"`
}

// ActionStatusResponse acknowledges that a Beckn action was sent; status is
// the transaction status just set.
type ActionStatusResponse struct {
	Status string `json:"status" enums:"INIT_SENT,CONFIRM_SENT,STATUS_SENT,CANCEL_SENT,UPDATE_SENT,RATE_SENT,SUPPORT_SENT" validate:"required"`
}

// CancelItemsResponse is POST /api/v1/cancel-items's answer: CANCEL_SENT when
// everything still active was cancelled (Beckn cancel), else UPDATE_SENT.
type CancelItemsResponse struct {
	Status string `json:"status" enums:"CANCEL_SENT,UPDATE_SENT" validate:"required"`
}

// ReturnItemsResponse is POST /api/v1/return-items's answer.
type ReturnItemsResponse struct {
	Status string `json:"status" enums:"UPDATE_SENT" validate:"required"`
}

// DemoCatalogItem is one product of the hard-coded demo catalog
// (ClientService.GetCatalog).
type DemoCatalogItem struct {
	ID       string  `json:"id" example:"p1"`
	Name     string  `json:"name" example:"Organic Avocados"`
	Price    float64 `json:"price" example:"120"`
	Category string  `json:"category" example:"Produce"`
	Image    string  `json:"image" format:"uri"`
}

// DiscoverResponse is the opaque Beckn discover result, passed through
// unparsed: typically {context, message: {catalogs: [...]}} where each
// catalog carries its bppId/bppUri, providers, resources and offers.
type DiscoverResponse map[string]any

// Billing is the buyer's contact and delivery address in
// ClientInitRequest.billing (parsed by buildInitMessage as billingInfo),
// patched into the contract's buyer participant and delivery performance.
type Billing struct {
	Name            string `json:"name"`
	Email           string `json:"email"`
	Phone           string `json:"phone"`
	StreetAddress   string `json:"streetAddress"`
	AddressLocality string `json:"addressLocality"`
	AddressRegion   string `json:"addressRegion"`
	PostalCode      string `json:"postalCode"`
	AddressCountry  string `json:"addressCountry"`
}

// OrderSummary is one row of GET /api/v1/orders (HandleListOrders' OrderItem).
type OrderSummary struct {
	TransactionID string `json:"transaction_id" format:"uuid"`
	BppID         string `json:"bpp_id"`
	// The transaction status, overridden by the status code of a RETURN
	// performance when the contract has one (e.g. Return_Initiated).
	Status    string `json:"status"`
	CreatedAt string `json:"created_at" format:"date-time"`
	UpdatedAt string `json:"updated_at" format:"date-time"`
	// Latest contract snapshot (opaque Beckn/ION contract JSON); {} when none.
	Contract json.RawMessage `json:"contract" swaggertype:"object"`
	// Beckn action of the latest snapshot (e.g. on_confirm); empty if none.
	LatestAction string `json:"latest_action"`
	// First commitment's resource name, falling back to its offer name.
	ItemName string `json:"item_name"`
	// First commitment's commitmentAttributes.price.value.
	ItemPrice float64 `json:"item_price"`
	Currency  string  `json:"currency"`
}

// OrderListResponse is GET /api/v1/orders's answer.
type OrderListResponse struct {
	Items []OrderSummary `json:"items" validate:"required"`
	// Total orders owned by the buyer.
	Total int64 `json:"total" validate:"required"`
	Page  int   `json:"page" example:"1" validate:"required"`
	Limit int   `json:"limit" example:"20" validate:"required"`
}

// ContractSnapshotEntry is one contract snapshot in an order's timeline
// (HandleGetOrder's SnapshotEntry).
type ContractSnapshotEntry struct {
	// Beckn action that produced the snapshot (e.g. on_select, on_confirm).
	Action    string          `json:"action"`
	Contract  json.RawMessage `json:"contract" swaggertype:"object"`
	CreatedAt string          `json:"created_at" format:"date-time"`
}

// OrderDetailResponse is GET /api/v1/orders/{id}'s answer.
type OrderDetailResponse struct {
	TransactionID string `json:"transaction_id" format:"uuid" validate:"required"`
	Status        string `json:"status" enums:"SELECT_SENT,QUOTE_RECEIVED,INIT_SENT,INIT_RECEIVED,CONFIRM_SENT,CONFIRMED,CANCELLED,COMPLETE,STATUS_SENT,CANCEL_SENT,UPDATE_SENT,RATE_SENT,SUPPORT_SENT,DISCOVER_SENT,DISCOVER_RECEIVED" validate:"required"`
	UpdatedAt     string `json:"updated_at" format:"date-time" validate:"required"`
	// Latest contract snapshot (opaque Beckn/ION contract JSON); null when
	// there is none yet.
	Contract json.RawMessage `json:"contract" swaggertype:"object" extensions:"x-nullable"`
	// All contract snapshots of the transaction, oldest first (empty if the
	// query fails).
	Snapshots []ContractSnapshotEntry `json:"snapshots" validate:"required"`
}

// MessageLogEntry is one row of the buyer's Beckn message log
// (HandleListMessages' messageLogRow).
type MessageLogEntry struct {
	ID            string  `json:"id" format:"uuid"`
	MessageID     string  `json:"message_id" format:"uuid"`
	Action        string  `json:"action" enums:"discover,on_discover,select,on_select,init,on_init,confirm,on_confirm,status,on_status,track,on_track,update,on_update,cancel,on_cancel,rate,on_rate,support,on_support,reconcile,on_reconcile"`
	Direction     string  `json:"direction" enums:"INBOUND,OUTBOUND"`
	AckStatus     *string `json:"ack_status" enums:"ACK,NACK" extensions:"x-nullable"`
	TransactionID *string `json:"transaction_id" format:"uuid" extensions:"x-nullable"`
	URL           *string `json:"url" extensions:"x-nullable"`
	ErrorMessage  *string `json:"error_message" extensions:"x-nullable"`
	// The full Beckn request as sent or received.
	RequestPayload json.RawMessage `json:"request_payload" swaggertype:"object" extensions:"x-nullable"`
	// The response body (e.g. the adapter ACK), when recorded.
	ResponsePayload json.RawMessage `json:"response_payload" swaggertype:"object" extensions:"x-nullable"`
	DurationMs      *int32          `json:"processing_duration_ms" extensions:"x-nullable"`
	CreatedAt       string          `json:"created_at" format:"date-time"`
}

// MessageListResponse is GET /api/v1/messages's answer.
type MessageListResponse struct {
	Items []MessageLogEntry `json:"items" validate:"required"`
}
