package dashboardsvc

import (
	"encoding/json"

	"github.com/ion/winroom/bpp/internal/catalog"
)

// The types in this file only describe requests/responses for the swag API
// docs (`task docs:api`); no code uses them. They mirror the gin.H bodies,
// function-local types and db/sqlc rows the dashboard handlers write, and
// must be kept in step with those handlers.

// ---------------------------------------------------------------------------
// shared
// ---------------------------------------------------------------------------

// PageMeta is the paging part of every paginated list response.
type PageMeta struct {
	// Total matching rows across all pages.
	Total int64 `json:"total" binding:"required"`
	Page  int   `json:"page" binding:"required" example:"1"`
	Limit int   `json:"limit" binding:"required" example:"20"`
}

// IDName is an {id, name} pair (providers, catalog resources).
type IDName struct {
	ID   string `json:"id" binding:"required"`
	Name string `json:"name" binding:"required"`
}

// IDNameListResponse is {"items": [{id, name}]}.
type IDNameListResponse struct {
	Items []IDName `json:"items" binding:"required"`
}

// StatusOKResponse is {"status": "ok"}.
type StatusOKResponse struct {
	Status string `json:"status" binding:"required" enums:"ok" example:"ok"`
}

// StatusUpdatedResponse is {"status": "updated"}.
type StatusUpdatedResponse struct {
	Status string `json:"status" binding:"required" enums:"updated" example:"updated"`
}

// StatusDeletedResponse is {"status": "deleted"}.
type StatusDeletedResponse struct {
	Status string `json:"status" binding:"required" enums:"deleted" example:"deleted"`
}

// ---------------------------------------------------------------------------
// dashboard
// ---------------------------------------------------------------------------

// DashboardStatsResponse is GET /api/v1/dashboard/stats.
type DashboardStatsResponse struct {
	// Contracts with status ACTIVE.
	ActiveOrders int64 `json:"active_orders" binding:"required"`
	// Contracts with status DRAFT.
	PendingOrders int64 `json:"pending_orders" binding:"required"`
	// Contracts created in the last 24 hours.
	TodayOrders   int64 `json:"today_orders" binding:"required"`
	ResourceCount int64 `json:"resource_count" binding:"required"`
	OfferCount    int64 `json:"offer_count" binding:"required"`
	// Message counts per action. Always has select, init and confirm.
	Funnel map[string]int64 `json:"funnel" binding:"required"`
	// Products published as OUT_OF_STOCK.
	OutOfStock int `json:"out_of_stock" binding:"required"`
	// Products published as LOW_STOCK.
	LowStock int `json:"low_stock" binding:"required"`
}

// NullAckStatus mirrors sqlc.NullAckStatus, serialised as an object.
type NullAckStatus struct {
	AckStatus string `json:"ack_status" enums:"ACK,NACK,"`
	Valid     bool   `json:"valid"`
}

// MessageLogItem mirrors sqlc.ListRecentMessagesRow (a beckn_message_log row).
type MessageLogItem struct {
	ID        string `json:"id" format:"uuid"`
	MessageID string `json:"message_id" format:"uuid"`
	// Beckn action (select, on_select, ..., catalog_publish, catalog_retire,
	// reconcile, on_reconcile).
	Action          string          `json:"action"`
	Direction       string          `json:"direction" enums:"INBOUND,OUTBOUND"`
	AckStatus       NullAckStatus   `json:"ack_status"`
	TransactionID   *string         `json:"transaction_id" format:"uuid" extensions:"x-nullable"`
	URL             *string         `json:"url" extensions:"x-nullable"`
	ErrorMessage    *string         `json:"error_message" extensions:"x-nullable"`
	RequestPayload  json.RawMessage `json:"request_payload" swaggertype:"object" extensions:"x-nullable"`
	ResponsePayload json.RawMessage `json:"response_payload" swaggertype:"object" extensions:"x-nullable"`
	CreatedAt       *string         `json:"created_at" format:"date-time" extensions:"x-nullable"`
}

// MessageListResponse is GET /api/v1/messages.
type MessageListResponse struct {
	Items []MessageLogItem `json:"items" binding:"required"`
}

// SupportTicket mirrors HandleListSupportTickets' ticketItem.
type SupportTicket struct {
	ID string `json:"id" binding:"required" format:"uuid"`
	// Contract UUID, or "" when the ticket isn't linked to one.
	ContractID string `json:"contractId" binding:"required"`
	// Transaction UUID, or "".
	TransactionID string `json:"transactionId" binding:"required"`
	BapID         string `json:"bapId" binding:"required"`
	Name          string `json:"name" binding:"required"`
	ShortDesc     string `json:"shortDesc" binding:"required"`
	Status        string `json:"status" binding:"required" enums:"OPEN,IN_PROGRESS,RESOLVED,CLOSED"`
	IsPreview     bool   `json:"isPreview" binding:"required"`
	CreatedAt     string `json:"createdAt" binding:"required" format:"date-time"`
	// Left out when the ticket isn't resolved.
	ResolvedAt string `json:"resolvedAt,omitempty" format:"date-time"`
}

// SupportTicketListResponse is GET /api/v1/support-tickets.
type SupportTicketListResponse struct {
	PageMeta
	Items []SupportTicket `json:"items" binding:"required"`
}

// Rating mirrors HandleListRatings' ratingItem.
type Rating struct {
	ID            string `json:"id" binding:"required" format:"uuid"`
	ContractID    string `json:"contractId" binding:"required" format:"uuid"`
	TransactionID string `json:"transactionId" binding:"required" format:"uuid"`
	BapID         string `json:"bapId" binding:"required"`
	TargetID      string `json:"targetId" binding:"required"`
	// Beckn rating range as the BAP sent it (raw JSON).
	Range     json.RawMessage `json:"range" binding:"required" swaggertype:"object"`
	IsPreview bool            `json:"isPreview" binding:"required"`
	CreatedAt string          `json:"createdAt" binding:"required" format:"date-time"`
}

// RatingListResponse is GET /api/v1/ratings.
type RatingListResponse struct {
	PageMeta
	Items []Rating `json:"items" binding:"required"`
}

// ---------------------------------------------------------------------------
// orders
// ---------------------------------------------------------------------------

// FulfillmentStatusRequest mirrors HandleUpdateFulfillmentStatus' body.
type FulfillmentStatusRequest struct {
	// New fulfillment status code: any non-empty string not starting with
	// "Return". The dashboard sends Packed and Shipped; Delivered also
	// triggers delivery handling.
	Status string `json:"status" binding:"required" example:"Shipped"`
}

// OrderListItem mirrors sqlc.ListContractsRow.
type OrderListItem struct {
	ID            string  `json:"id" format:"uuid"`
	TransactionID string  `json:"transaction_id" format:"uuid"`
	Status        string  `json:"status" enums:"DRAFT,ACTIVE,CANCELLED,COMPLETE"`
	BapID         string  `json:"bap_id"`
	CreatedAt     *string `json:"created_at" format:"date-time" extensions:"x-nullable"`
	// The order's offer descriptor, or {}.
	OfferDescriptor json.RawMessage `json:"offer_descriptor" swaggertype:"object"`
	// Delivery progress (e.g. PENDING, Packed, Shipped, Delivered), or "".
	FulfillmentStatus string `json:"fulfillment_status"`
	// Latest return outcome (e.g. Return_Approved, Return_Rejected), or "".
	ReturnStatus string `json:"return_status"`
}

// OrderListResponse is GET /api/v1/orders.
type OrderListResponse struct {
	PageMeta
	Items []OrderListItem `json:"items" binding:"required"`
}

// OrderContract mirrors sqlc.GetContractDetailRow.
type OrderContract struct {
	ID            string  `json:"id" format:"uuid"`
	TransactionID string  `json:"transaction_id" format:"uuid"`
	Status        string  `json:"status" enums:"DRAFT,ACTIVE,CANCELLED,COMPLETE"`
	BapID         string  `json:"bap_id"`
	BapURI        string  `json:"bap_uri"`
	Domain        *string `json:"domain" extensions:"x-nullable"`
	// Latest stored Beckn contract JSON.
	ContractAttributes json.RawMessage `json:"contract_attributes" swaggertype:"object" extensions:"x-nullable"`
	CreatedAt          *string         `json:"created_at" format:"date-time" extensions:"x-nullable"`
	UpdatedAt          *string         `json:"updated_at" format:"date-time" extensions:"x-nullable"`
}

// OrderCommitment mirrors sqlc.ListContractCommitmentsRow.
type OrderCommitment struct {
	ID                   string          `json:"id"`
	Status               string          `json:"status" enums:"DRAFT,ACTIVE,CLOSED"`
	CommitmentAttributes json.RawMessage `json:"commitment_attributes" swaggertype:"object" extensions:"x-nullable"`
	OfferID              *string         `json:"offer_id" extensions:"x-nullable"`
	OfferDescriptor      json.RawMessage `json:"offer_descriptor" swaggertype:"object" extensions:"x-nullable"`
	ResourceIDs          []string        `json:"resource_ids" extensions:"x-nullable"`
}

// OrderConsideration mirrors sqlc.ListContractConsiderationsRow.
type OrderConsideration struct {
	ID                      string          `json:"id"`
	StatusCode              string          `json:"status_code"`
	StatusName              *string         `json:"status_name" extensions:"x-nullable"`
	ConsiderationAttributes json.RawMessage `json:"consideration_attributes" swaggertype:"object" extensions:"x-nullable"`
}

// OrderDetailResponse is GET /api/v1/orders/{id}.
type OrderDetailResponse struct {
	Contract       OrderContract        `json:"contract" binding:"required"`
	Commitments    []OrderCommitment    `json:"commitments" binding:"required"`
	Considerations []OrderConsideration `json:"considerations" binding:"required"`
}

// ---------------------------------------------------------------------------
// inventory
// ---------------------------------------------------------------------------

// ResourceRow mirrors sqlc.ListResourcesRow.
type ResourceRow struct {
	ID                  string          `json:"id"`
	CatalogID           string          `json:"catalog_id"`
	DescriptorName      *string         `json:"descriptor_name" extensions:"x-nullable"`
	DescriptorShortDesc *string         `json:"descriptor_short_desc" extensions:"x-nullable"`
	ResourceAttributes  json.RawMessage `json:"resource_attributes" swaggertype:"object" extensions:"x-nullable"`
	CreatedAt           *string         `json:"created_at" format:"date-time" extensions:"x-nullable"`
}

// ResourceListResponse is GET /api/v1/inventory/resources.
type ResourceListResponse struct {
	PageMeta
	Items []ResourceRow `json:"items" binding:"required"`
}

// OfferRow mirrors sqlc.ListOffersRow.
type OfferRow struct {
	ID                  string   `json:"id"`
	CatalogID           string   `json:"catalog_id"`
	DescriptorName      *string  `json:"descriptor_name" extensions:"x-nullable"`
	DescriptorShortDesc *string  `json:"descriptor_short_desc" extensions:"x-nullable"`
	ResourceIDs         []string `json:"resource_ids"`
	ValidityStart       *string  `json:"validity_start" format:"date-time" extensions:"x-nullable"`
	ValidityEnd         *string  `json:"validity_end" format:"date-time" extensions:"x-nullable"`
	CreatedAt           *string  `json:"created_at" format:"date-time" extensions:"x-nullable"`
}

// OfferListResponse is GET /api/v1/inventory/offers.
type OfferListResponse struct {
	PageMeta
	Items []OfferRow `json:"items" binding:"required"`
}

// InventoryItem mirrors HandleInventoryItems' invItem.
type InventoryItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ShortDesc    string `json:"shortDesc"`
	CatalogID    string `json:"catalogId"`
	CatalogName  string `json:"catalogName"`
	ProviderID   string `json:"providerId"`
	ProviderName string `json:"providerName"`
	// Published resourceAttributes.availability.status, or "".
	Availability string `json:"availability" example:"IN_STOCK"`
	CreatedAt    string `json:"createdAt" format:"date-time"`
}

// InventoryItemsResponse is GET /api/v1/inventory/items.
type InventoryItemsResponse struct {
	PageMeta
	Items []InventoryItem `json:"items" binding:"required"`
	// Product count per resourceAttributes.availability.status over all
	// matching rows (not just this page). Products with no status are
	// counted under "".
	Availability map[string]int64 `json:"availability" binding:"required"`
}

// ---------------------------------------------------------------------------
// catalogs
// ---------------------------------------------------------------------------

// CatalogListItem mirrors HandleListCatalogs' catalogRow.
type CatalogListItem struct {
	ID                 string  `json:"id" binding:"required"`
	Name               string  `json:"name" binding:"required"`
	ShortDesc          string  `json:"shortDesc" binding:"required"`
	ProviderID         string  `json:"providerId" binding:"required"`
	CatalogType        *string `json:"catalogType" enums:"master,regular" extensions:"x-nullable"`
	CRC                *string `json:"crc" extensions:"x-nullable"`
	ValidityStart      *string `json:"validityStart" format:"date-time" extensions:"x-nullable"`
	ValidityEnd        *string `json:"validityEnd" format:"date-time" extensions:"x-nullable"`
	CreatedAt          string  `json:"createdAt" binding:"required" format:"date-time"`
	NetworkPublishedAt *string `json:"networkPublishedAt" format:"date-time" extensions:"x-nullable"`
	// True once networkPublishedAt is set.
	IsPublished   bool  `json:"isPublished" binding:"required"`
	IsActive      bool  `json:"isActive" binding:"required"`
	ResourceCount int64 `json:"resourceCount" binding:"required"`
	OfferCount    int64 `json:"offerCount" binding:"required"`
}

// CatalogListResponse is GET /api/v1/catalogs.
type CatalogListResponse struct {
	PageMeta
	Items []CatalogListItem `json:"items" binding:"required"`
}

// CreateCatalogResponse is POST /api/v1/catalogs.
type CreateCatalogResponse struct {
	Status string `json:"status" binding:"required" enums:"ok" example:"ok"`
	ID     string `json:"id" binding:"required" example:"catalog-fashion-001"`
}

// CatalogDetail mirrors HandleGetCatalog's catalog row.
type CatalogDetail struct {
	ID                 string  `json:"id" binding:"required"`
	Name               string  `json:"name" binding:"required"`
	ShortDesc          string  `json:"shortDesc" binding:"required"`
	ProviderID         string  `json:"providerId" binding:"required"`
	ProviderName       string  `json:"providerName" binding:"required"`
	CatalogType        *string `json:"catalogType" enums:"master,regular" extensions:"x-nullable"`
	CRC                *string `json:"crc" extensions:"x-nullable"`
	ValidityStart      *string `json:"validityStart" format:"date-time" extensions:"x-nullable"`
	ValidityEnd        *string `json:"validityEnd" format:"date-time" extensions:"x-nullable"`
	CreatedAt          string  `json:"createdAt" binding:"required" format:"date-time"`
	NetworkPublishedAt *string `json:"networkPublishedAt" format:"date-time" extensions:"x-nullable"`
	IsPublished        bool    `json:"isPublished" binding:"required"`
	IsActive           bool    `json:"isActive" binding:"required"`
}

// CatalogDetailResource mirrors HandleGetCatalog's resItem.
type CatalogDetailResource struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	ShortDesc          string              `json:"shortDesc"`
	MediaFiles         []catalog.MediaFile `json:"mediaFiles" extensions:"x-nullable"`
	ResourceAttributes json.RawMessage     `json:"resourceAttributes" swaggertype:"object" extensions:"x-nullable"`
	CreatedAt          string              `json:"createdAt" format:"date-time"`
}

// CatalogDetailOffer mirrors HandleGetCatalog's offerItem.
type CatalogDetailOffer struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ShortDesc     string   `json:"shortDesc"`
	ResourceIDs   []string `json:"resourceIds" extensions:"x-nullable"`
	ValidityStart *string  `json:"validityStart" format:"date-time" extensions:"x-nullable"`
	ValidityEnd   *string  `json:"validityEnd" format:"date-time" extensions:"x-nullable"`
	// Attributes of the offer's first consideration (its price).
	ConsiderationAttributes json.RawMessage `json:"considerationAttributes" swaggertype:"object" extensions:"x-nullable"`
}

// CatalogDetailResponse is GET /api/v1/catalogs/{id}.
type CatalogDetailResponse struct {
	Catalog   CatalogDetail           `json:"catalog" binding:"required"`
	Resources []CatalogDetailResource `json:"resources" binding:"required"`
	Offers    []CatalogDetailOffer    `json:"offers" binding:"required"`
}

// SetCatalogActiveResponse is PATCH /api/v1/catalogs/{id}/active.
type SetCatalogActiveResponse struct {
	Status   string `json:"status" binding:"required" enums:"updated" example:"updated"`
	IsActive bool   `json:"isActive" binding:"required"`
	// Only for a catalog published before: whether the automatic republish
	// succeeded.
	Published *bool `json:"published,omitempty"`
	// Only when the automatic republish failed: its error text.
	PublishError string `json:"publishError,omitempty"`
}

// ResourceResponse is GET /api/v1/catalogs/{id}/resources/{rid}.
type ResourceResponse struct {
	Resource catalog.Resource `json:"resource" binding:"required"`
}

// OfferResponse is GET /api/v1/catalogs/{id}/offers/{oid}.
type OfferResponse struct {
	Offer catalog.Offer `json:"offer" binding:"required"`
}

// PublishCatalogRequest mirrors HandlePublishCatalog's optional body.
type PublishCatalogRequest struct {
	// Skip onix-catalog-publish's diff against the last published state and
	// write a fresh baseline (manual compaction).
	ForceBaseline bool `json:"forceBaseline" example:"false"`
}

// PublishCatalogResponse is POST /api/v1/catalogs/{id}/publish on success.
type PublishCatalogResponse struct {
	Status  string `json:"status" binding:"required" example:"ACK"`
	Message string `json:"message" binding:"required" example:"Catalog published to CDS"`
}
