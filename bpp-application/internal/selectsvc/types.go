package selectsvc

import "encoding/json"

// ---------------------------------------------------------------------------
// Beckn context (shared by INBOUND select and OUTBOUND on_select)
// ---------------------------------------------------------------------------

type BecknContext struct {
	Version       string `json:"version"`
	Action        string `json:"action"`
	Timestamp     string `json:"timestamp"`
	MessageID     string `json:"messageId"`
	TransactionID string `json:"transactionId"`
	BapID         string `json:"bapId"`
	BapURI        string `json:"bapUri"`
	BppID         string `json:"bppId"`
	BppURI        string `json:"bppUri"`
	TTL           string `json:"ttl,omitempty"`
	NetworkID     string `json:"networkId"`
}

// ---------------------------------------------------------------------------
// INBOUND — BAP → BPP: POST /beckn/select
// ---------------------------------------------------------------------------

type SelectRequest struct {
	Context BecknContext   `json:"context"`
	Message SelectMessage  `json:"message"`
}

type SelectMessage struct {
	Contract Contract `json:"contract"`
}

// ---------------------------------------------------------------------------
// OUTBOUND — BPP → BAP: POST {bapUri}/on_select
// ---------------------------------------------------------------------------

type OnSelectRequest struct {
	Context BecknContext     `json:"context"`
	Message OnSelectMessage  `json:"message"`
}

type OnSelectMessage struct {
	Contract Contract `json:"contract"`
}

// ---------------------------------------------------------------------------
// Synchronous ACK returned to the BAP immediately on POST /beckn/select.
// The actual on_select callback is sent asynchronously.
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

// ---------------------------------------------------------------------------
// Contract — shared between select and on_select
// ---------------------------------------------------------------------------

type Contract struct {
	Status        *ContractStatus `json:"status,omitempty"`
	Participants  []Participant   `json:"participants,omitempty"`
	Commitments   []Commitment    `json:"commitments"`
	Consideration []Consideration `json:"consideration,omitempty"`
	Performance   []Performance   `json:"performance,omitempty"`
}

type ContractStatus struct {
	Code string `json:"code"`
}

type Participant struct {
	ID                    string          `json:"id"`
	Descriptor            *Descriptor     `json:"descriptor,omitempty"`
	ParticipantAttributes json.RawMessage `json:"participantAttributes,omitempty"`
}

// ---------------------------------------------------------------------------
// Commitment
// ---------------------------------------------------------------------------

type Commitment struct {
	ID                   string              `json:"id"`
	Status               *CommitmentStatus   `json:"status,omitempty"`
	Resources            []Resource          `json:"resources,omitempty"`
	Offer                *Offer              `json:"offer,omitempty"`
	CommitmentAttributes json.RawMessage     `json:"commitmentAttributes,omitempty"`
}

type CommitmentStatus struct {
	Descriptor *StatusDescriptor `json:"descriptor,omitempty"`
}

type StatusDescriptor struct {
	Code string `json:"code"`
}

type Resource struct {
	ID                 string          `json:"id"`
	Descriptor         *Descriptor     `json:"descriptor,omitempty"`
	ResourceAttributes json.RawMessage `json:"resourceAttributes,omitempty"`
	Quantity           *quantity       `json:"quantity,omitempty"`
}

// Descriptor is the Beckn Descriptor: every field is kept so a resource or
// offer carries the same descriptor it was published with (media, docs, …).
type Descriptor struct {
	Code           string          `json:"code,omitempty"`
	Name           string          `json:"name,omitempty"`
	ShortDesc      string          `json:"shortDesc,omitempty"`
	LongDesc       string          `json:"longDesc,omitempty"`
	ThumbnailImage string          `json:"thumbnailImage,omitempty"`
	Docs           json.RawMessage `json:"docs,omitempty"`
	MediaFile      json.RawMessage `json:"mediaFile,omitempty"`
}

type Offer struct {
	ID              string          `json:"id"`
	ResourceIDs     []string        `json:"resourceIds,omitempty"`
	Descriptor      *Descriptor     `json:"descriptor,omitempty"`
	Provider        *Provider            `json:"provider,omitempty"`
	Considerations  []OfferConsideration `json:"considerations,omitempty"`
	OfferAttributes json.RawMessage      `json:"offerAttributes,omitempty"`
}

// OfferConsideration is one of an offer's catalog prices (Offer.considerations[]).
// Its status is a Descriptor and, unlike Contract.consideration[], optional.
type OfferConsideration struct {
	ID                      string                    `json:"id"`
	Status                  *OfferConsiderationStatus `json:"status,omitempty"`
	ConsiderationAttributes json.RawMessage           `json:"considerationAttributes,omitempty"`
}

type OfferConsiderationStatus struct {
	Code string `json:"code,omitempty"`
	Name string `json:"name,omitempty"`
}

type Provider struct {
	ID         string      `json:"id"`
	Descriptor *Descriptor `json:"descriptor,omitempty"`
}

// ---------------------------------------------------------------------------
// Consideration (pricing)
// ---------------------------------------------------------------------------

type Consideration struct {
	ID                      string                `json:"id"`
	Status                  *ConsiderationStatus  `json:"status,omitempty"`
	ConsiderationAttributes json.RawMessage       `json:"considerationAttributes,omitempty"`
}

type ConsiderationStatus struct {
	Code string `json:"code"`
}

// ---------------------------------------------------------------------------
// Performance (fulfillment)
// ---------------------------------------------------------------------------

type Performance struct {
	ID                    string             `json:"id"`
	Status                *PerformanceStatus `json:"status,omitempty"`
	CommitmentIDs         []string           `json:"commitmentIds,omitempty"`
	PerformanceAttributes json.RawMessage    `json:"performanceAttributes,omitempty"`
}

type PerformanceStatus struct {
	Code string `json:"code"`
}

// ---------------------------------------------------------------------------
// Internal helpers for parsing JSONB blobs from the DB
// ---------------------------------------------------------------------------

// dbConsiderationAttrs mirrors the structure stored in
// offer_considerations.consideration_attributes.
type dbConsiderationAttrs struct {
	Context        string        `json:"@context"`
	Type           string        `json:"@type"`
	Currency       string        `json:"currency"`
	TotalAmount    float64       `json:"totalAmount"`
	Breakup        []breakupItem `json:"breakup,omitempty"`
	PaymentMethods []string      `json:"paymentMethods,omitempty"`
}

type breakupItem struct {
	Title  string  `json:"title"`
	Amount float64 `json:"amount"`
	Type   string  `json:"type"`
	// TaxDetail is set on TAX lines: rate, included, taxableBase, plus ION's
	// IONTaxDetail taxRegime / taxCategory / eFakturRef. Kept as a map so any
	// field the seller published passes through unchanged.
	TaxDetail map[string]any `json:"taxDetail,omitempty"`
}

// scaled returns the line for qty units: amount and any taxDetail.taxableBase
// multiplied by qty; the tax rate is unchanged.
func (b breakupItem) scaled(qty float64) breakupItem {
	out := b
	out.Amount = b.Amount * qty
	if b.TaxDetail != nil {
		out.TaxDetail = make(map[string]any, len(b.TaxDetail))
		for k, v := range b.TaxDetail {
			out.TaxDetail[k] = v
		}
		if base, ok := b.TaxDetail["taxableBase"].(float64); ok {
			out.TaxDetail["taxableBase"] = base * qty
		}
	}
	return out
}

// inboundCommitmentAttrs mirrors the commitmentAttributes sent by the BAP.
type inboundCommitmentAttrs struct {
	Context    string    `json:"@context"`
	Type       string    `json:"@type"`
	LineID     string    `json:"lineId,omitempty"`
	ResourceID string    `json:"resourceId,omitempty"`
	OfferID    string    `json:"offerId,omitempty"`
	Quantity   *quantity `json:"quantity,omitempty"`
}

type quantity struct {
	UnitCode     string  `json:"unitCode"`
	UnitQuantity float64 `json:"unitQuantity"`
}
