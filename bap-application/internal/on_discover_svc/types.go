package ondiscoversvc

import "encoding/json"

// ---------------------------------------------------------------------------
// Beckn context
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// INBOUND — Discovery Service → BAP: POST /bap/receiver/on_discover
// ---------------------------------------------------------------------------

type OnDiscoverRequest struct {
	Context BecknContext      `json:"context"`
	Message OnDiscoverMessage `json:"message"`
}

type OnDiscoverMessage struct {
	Catalogs json.RawMessage `json:"catalogs"` // store the full catalogs array as-is
}

// ---------------------------------------------------------------------------
// Synchronous ACK returned to the Discovery Service immediately
// ---------------------------------------------------------------------------

type BecknACK struct {
	Context BecknContext `json:"context"`
	Message ACKMessage   `json:"message"`
}

type ACKMessage struct {
	ACK ACKStatus `json:"ack"`
}

type ACKStatus struct {
	Status string `json:"status"` // "ACK" | "NACK"
}
