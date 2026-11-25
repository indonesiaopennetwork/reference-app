// Package qrsvc is the buyer side of ION QR packets.
//
// An admin defines a packet in ion-qr-code's qr-service (a curated basket of
// items tied to a physical node: a kos, an apartment lobby, a warung) and
// prints its QR. A buyer who scans it is redirected by qr-service to
// bap-frontend with ?packet_id=&scan_id=, and this package takes over:
//
//  1. resolve  - read the packet from qr-service, run discover around the
//     node, and find the merchants that stock every item. One
//     merchant: go straight to a quote (FAST_CHECKOUT). Several: let the
//     buyer pick (SELECT_MERCHANT). None: say why (NO_MERCHANT).
//  2. quote    - a normal Beckn select of the items the buyer chose; the
//     seller's on_select is the binding quote. An item the seller can no
//     longer supply ends the quote.
//  3. checkout - Beckn init with the buyer's name, phone and the node (plus
//     room/unit) as the delivery address. A changed on_init total is
//     reported as QUOTE_CHANGED for the buyer to accept.
//  4. place    - the buyer places the order at the on_init total and this
//     package sends Beckn confirm. Collecting payment is not part of this
//     flow yet; a payment step belongs here, before confirm.
//
// Every step after resolve is authorised by the guest token resolve hands
// out, so a buyer needs no login. A logged-in buyer's orders are also
// recorded against them and show in the shop's order history.
package qrsvc

import (
	"encoding/json"
	"time"

	"github.com/ion/winroom/bap/internal/clientsvc"
)

// Resolve modes. FAST_CHECKOUT: one merchant, so the buyer goes straight to
// choosing items; SELECT_MERCHANT: the buyer picks a merchant first.
const (
	ModeFastCheckout   = "FAST_CHECKOUT"
	ModeSelectMerchant = "SELECT_MERCHANT"
	ModeNoMerchant     = "NO_MERCHANT"
)

// currencyIDR is the only currency QR orders are in.
const currencyIDR = "IDR"

// qr_orders.state values.
const (
	StateQuoted          = "QUOTED"
	StateInitSent        = "INIT_SENT"
	StateAwaitingConfirm = "AWAITING_CONFIRM" // on_init in; the buyer can place the order
	StateConfirmSent     = "CONFIRM_SENT"
	StateConfirmed       = "CONFIRMED"
	StateFailed          = "FAILED"
)

// Fulfilment modes the buyer can choose.
const (
	FulfillDeliveryToNode = "DELIVERY_TO_QR_NODE"
	FulfillSelfPickup     = "SELF_PICKUP"
)

// GuestTokenHeader carries the guest token on every call after resolve.
const GuestTokenHeader = "X-QR-Guest-Token"

// ---------------------------------------------------------------------------
// qr-service packet (GET /api/v1/packets/:id)
// ---------------------------------------------------------------------------

// Packet is qr-service's PacketResponse.
type Packet struct {
	ID                  string       `json:"id"`
	Title               string       `json:"title"`
	FormatType          string       `json:"format_type"`
	AllowNodeAsDelivery bool         `json:"allow_node_as_delivery"`
	LocationLabel       string       `json:"location_label"`
	Lat                 float64      `json:"lat"`
	Lon                 float64      `json:"lon"`
	RadiusMeters        int          `json:"radius_meters"`
	MinBasketValue      float64      `json:"min_basket_value"`
	Status              string       `json:"status"`
	Version             int          `json:"version"`
	Items               []PacketItem `json:"items"`
}

// PacketItem is one item of a packet's basket.
type PacketItem struct {
	ID          string `json:"id"`
	MatchType   string `json:"match_type" enums:"GTIN,ION_SKU,TEXT_QUERY"`
	MatchValue  string `json:"match_value"`
	DisplayName string `json:"display_name"`
	SortOrder   int    `json:"sort_order"`
}

// PacketView is the public view of a packet the buyer page shows.
type PacketView struct {
	ID                  string       `json:"id"`
	Title               string       `json:"title"`
	FormatType          string       `json:"format_type"`
	AllowNodeAsDelivery bool         `json:"allow_node_as_delivery"`
	LocationLabel       string       `json:"location_label"`
	Lat                 float64      `json:"lat"`
	Lon                 float64      `json:"lon"`
	RadiusMeters        int          `json:"radius_meters"`
	MinBasketValue      float64      `json:"min_basket_value"`
	Status              string       `json:"status"`
	Version             int          `json:"version"`
	Items               []PacketItem `json:"items"`
}

func (p *Packet) view() PacketView {
	return PacketView{
		ID: p.ID, Title: p.Title, FormatType: p.FormatType,
		AllowNodeAsDelivery: p.AllowNodeAsDelivery, LocationLabel: p.LocationLabel,
		Lat: p.Lat, Lon: p.Lon, RadiusMeters: p.RadiusMeters, MinBasketValue: p.MinBasketValue,
		Status: p.Status, Version: p.Version, Items: p.Items,
	}
}

// node is the part of a packet checkout needs, copied onto each candidate.
type node struct {
	Title               string  `json:"title"`
	LocationLabel       string  `json:"location_label"`
	Lat                 float64 `json:"lat"`
	Lon                 float64 `json:"lon"`
	AllowNodeAsDelivery bool    `json:"allow_node_as_delivery"`
	// The product subtotal the buyer's chosen items must reach.
	MinBasketValue float64 `json:"min_basket_value"`
}

// ---------------------------------------------------------------------------
// API requests / responses
// ---------------------------------------------------------------------------

// ResolveRequest is POST /api/v1/qr/resolve.
type ResolveRequest struct {
	PacketID string `json:"packet_id" binding:"required" example:"k8x2m9pq7r4w"`
	// scan_id from qr-service's redirect (?scan_id=). Optional.
	ScanID string `json:"scan_id,omitempty" format:"uuid"`
	// When there is no scan_id (the link was opened another way), a
	// client-generated key: the scan is recorded in qr-service as
	// DIRECT_RESOLVE, idempotently on this key.
	ClientScanKey string `json:"client_scan_key,omitempty"`
	// Scan simulator only (needs QR_DEMO_ENABLED=true): this BAP plays the
	// merchants instead of the Beckn network. SINGLE = one merchant (fast
	// checkout), MULTI = three to pick from, QUOTE_CHANGED = the total
	// changes at checkout.
	DemoScenario string `json:"demo_scenario,omitempty" enums:"SINGLE,MULTI,QUOTE_CHANGED"`
}

// ResolveResponse answers POST /api/v1/qr/resolve.
type ResolveResponse struct {
	Mode   string     `json:"mode" enums:"FAST_CHECKOUT,SELECT_MERCHANT,NO_MERCHANT"`
	Packet PacketView `json:"packet"`
	ScanID *string    `json:"scan_id,omitempty"`
	// Authorises every later call of this checkout (header X-QR-Guest-Token).
	GuestToken string `json:"guest_token,omitempty"`
	// Ranked best first. Present for FAST_CHECKOUT too (the one candidate).
	// Each line is priced for one unit; the buyer picks quantities, then quotes.
	Candidates []CandidateView `json:"candidates"`
	// NO_MERCHANT only.
	Reason  string `json:"reason,omitempty" enums:"NO_ELIGIBLE_MERCHANT,DISCOVERY_NOT_CONFIGURED"`
	Message string `json:"message,omitempty"`
	// NO_MERCHANT: the items no single nearby merchant can supply.
	MissingItems []string `json:"missing_items,omitempty"`
	// Set when this is a demo checkout (no Beckn calls).
	DemoScenario string `json:"demo_scenario,omitempty"`
}

// CandidateView is one merchant that can serve the packet.
type CandidateView struct {
	CandidateID  string `json:"candidate_id" format:"uuid"`
	ProviderID   string `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	BppID        string `json:"bpp_id"`
	// Null when the merchant's catalog states no location.
	DistanceMeters *int   `json:"distance_meters"`
	Address        string `json:"address,omitempty"`
	ItemsCovered   int    `json:"items_covered"`
	ItemsTotal     int    `json:"items_total"`
	// One of each item.
	EstimatedSubtotal float64 `json:"estimated_subtotal"`
	// The seller's per-order delivery fee from its offers; null when none stated.
	DeliveryFee *float64   `json:"delivery_fee"`
	Currency    string     `json:"currency"`
	Lines       []LineView `json:"lines"`
	Recommended bool       `json:"recommended"`
}

// LineView is one item of a candidate or a quote.
type LineView struct {
	PacketItemID string  `json:"packet_item_id"`
	Name         string  `json:"name"`
	OfferName    string  `json:"offer_name,omitempty"`
	Image        string  `json:"image,omitempty"`
	Quantity     int     `json:"quantity"`
	UnitPrice    float64 `json:"unit_price"`
	Subtotal     float64 `json:"subtotal"`
}

// QuoteRequest is POST /api/v1/qr/quote.
type QuoteRequest struct {
	CandidateID string `json:"candidate_id" binding:"required" format:"uuid"`
	// What the buyer chose from the candidate's lines. Their product subtotal
	// must reach the packet's minimum basket.
	Items []QuoteItem `json:"items" binding:"required,min=1,dive"`
}

// QuoteItem is one item the buyer chose: a candidate line (by its
// packet_item_id) and how many.
type QuoteItem struct {
	PacketItemID string `json:"packet_item_id" binding:"required"`
	Quantity     int    `json:"quantity" binding:"required,min=1,max=999"`
}

// QuoteResponse is the seller's binding quote (on_select).
type QuoteResponse struct {
	TransactionID string        `json:"transaction_id" format:"uuid"`
	State         string        `json:"state"`
	Merchant      CandidateView `json:"merchant"`
	Lines         []LineView    `json:"lines"`
	Pricing       Pricing       `json:"pricing"`
	Node          node          `json:"node"`
}

// Pricing is a quote's money, from the contract's consideration.
type Pricing struct {
	Currency      string        `json:"currency"`
	ItemsSubtotal float64       `json:"items_subtotal"`
	Tax           float64       `json:"tax"`
	DeliveryFee   float64       `json:"delivery_fee"`
	GrandTotal    float64       `json:"grand_total"`
	Breakup       []BreakupLine `json:"breakup"`
}

// BreakupLine is one consideration breakup entry.
type BreakupLine struct {
	Title  string  `json:"title"`
	Type   string  `json:"type"`
	Amount float64 `json:"amount"`
}

// CheckoutRequest is POST /api/v1/qr/checkout.
type CheckoutRequest struct {
	TransactionID   string `json:"transaction_id" binding:"required" format:"uuid"`
	FulfillmentMode string `json:"fulfillment_mode" binding:"required" enums:"DELIVERY_TO_QR_NODE,SELF_PICKUP"`
	// Room / unit at the node, e.g. "Kamar 12". Kept apart from the node address.
	UnitLabel  string `json:"unit_label,omitempty"`
	BuyerName  string `json:"buyer_name" binding:"required"`
	BuyerPhone string `json:"buyer_phone" binding:"required" example:"081234567890"`
	BuyerEmail string `json:"buyer_email,omitempty"`
}

// CheckoutResponse answers POST /api/v1/qr/checkout (and the data of its
// 409 QUOTE_CHANGED).
type CheckoutResponse struct {
	TransactionID string  `json:"transaction_id" format:"uuid"`
	State         string  `json:"state"`
	ContractID    string  `json:"contract_id,omitempty"`
	Pricing       Pricing `json:"pricing"`
	// Set on QUOTE_CHANGED: the total the buyer saw before.
	PreviousTotal *float64 `json:"previous_total,omitempty"`
}

// PlaceOrderRequest is POST /api/v1/qr/orders/:id/place.
type PlaceOrderRequest struct {
	// The total the buyer agreed to; must equal the order's current total.
	ExpectedAmount float64 `json:"expected_amount" binding:"required"`
}

// OrderView answers GET /api/v1/qr/orders/:transaction_id.
type OrderView struct {
	TransactionID     string `json:"transaction_id" format:"uuid"`
	PacketID          string `json:"packet_id"`
	ProviderName      string `json:"provider_name"`
	State             string `json:"state"`
	TransactionStatus string `json:"transaction_status"`
	// The seller's fulfilment stage from its latest contract (e.g. PACKED).
	FulfillmentStatus string     `json:"fulfillment_status,omitempty"`
	ContractID        string     `json:"contract_id,omitempty"`
	Total             *float64   `json:"total"`
	Currency          string     `json:"currency"`
	FulfillmentMode   string     `json:"fulfillment_mode,omitempty"`
	UnitLabel         string     `json:"unit_label,omitempty"`
	BuyerName         string     `json:"buyer_name,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	Lines             []LineView `json:"lines"`
	Node              node       `json:"node"`
	DemoScenario      string     `json:"demo_scenario,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// ErrorResponse is every /api/v1/qr error.
type ErrorResponse struct {
	Error string `json:"error"`
	// Machine-readable: PACKET_NOT_FOUND, PACKET_NOT_ACTIVE, QR_NOT_CONFIGURED,
	// QR_SERVICE_UNAVAILABLE, INVALID_GUEST_TOKEN, CANDIDATE_EXPIRED,
	// QUOTE_UNAVAILABLE, QUOTE_TIMEOUT, QUOTE_CHANGED, INVALID_STATE,
	// INVALID_PHONE, NODE_DELIVERY_NOT_ALLOWED,
	// DISCOVERY_FAILED, BECKN_FAILED, ...
	Code string          `json:"code"`
	Data json.RawMessage `json:"data,omitempty" swaggertype:"object"`
}

// candidateLine is one stored line of a candidate: the packet item it
// serves and the select item that orders it.
type candidateLine struct {
	PacketItemIDs []string             `json:"packet_item_ids"`
	Name          string               `json:"name"`
	Image         string               `json:"image,omitempty"`
	UnitPrice     float64              `json:"unit_price"`
	DeliveryFee   *float64             `json:"delivery_fee,omitempty"`
	Select        clientsvc.SelectItem `json:"select"`
}

func (l candidateLine) view() LineView {
	id := ""
	if len(l.PacketItemIDs) > 0 {
		id = l.PacketItemIDs[0]
	}
	return LineView{
		PacketItemID: id,
		Name:         l.Name,
		OfferName:    l.Select.OfferName,
		Image:        l.Image,
		Quantity:     l.Select.Quantity,
		UnitPrice:    l.UnitPrice,
		Subtotal:     round2(l.UnitPrice * float64(l.Select.Quantity)),
	}
}
