package catalog

import (
	"encoding/json"
	"strings"
)

// ProviderPublishRequest is what a provider sends to POST /v1/catalog/publish.
// Beckn context fields (bppId, networkId, etc.) are intentionally absent —
// the BPP injects them automatically from its own config before forwarding to the CDS.
type ProviderPublishRequest struct {
	Catalogs []Catalog `json:"catalogs" binding:"required,min=1"`
}

// PublishAck is returned to the provider on success.
type PublishAck struct {
	Status  string `json:"status" example:"ACK"`
	Message string `json:"message,omitempty" example:"Catalog published successfully"`
}

// BecknPublishRequest is the Beckn-compliant payload sent from the BPP to the CDS.
type BecknPublishRequest struct {
	Context BecknContext        `json:"context"`
	Message BecknPublishMessage `json:"message"`
}

type BecknContext struct {
	Version       string `json:"version"`
	Action        string `json:"action"`
	Timestamp     string `json:"timestamp"`
	TransactionID string `json:"transactionId"`
	MessageID     string `json:"messageId"`
	BppID         string `json:"bppId"`
	BppURI        string `json:"bppUri"`
	NetworkID     string `json:"networkId"`
}

type BecknPublishMessage struct {
	Catalogs []CDSCatalog `json:"catalogs"`
}

// ---------------------------------------------------------------------------
// CDS-safe types — mirrors Catalog/Resource/Offer but strips BPP-internal
// fields (e.g. PublishDirectives) that are not part of the Beckn catalog spec
// and would cause the CDS to reject the payload.
// ---------------------------------------------------------------------------

// CDSCatalog intentionally excludes PublishDirectives — that is a BPP-internal
// classification field; sending it (especially catalogType:"master") to the CDS
// causes the catalog to be hidden from regular discover queries.
type CDSCatalog struct {
	ID         string        `json:"id"`
	Descriptor Descriptor    `json:"descriptor"`
	Provider   Provider      `json:"provider"`
	Resources  []CDSResource `json:"resources,omitempty"`
	Offers     []Offer       `json:"offers,omitempty"`
	Validity   *TimePeriod   `json:"validity,omitempty"`
}

// CDSResource is a Resource as the CDS receives it. A resource publishes its
// availability (resourceAttributes.availability.status), never a stock count.
type CDSResource struct {
	ID                 string          `json:"id"`
	Descriptor         Descriptor      `json:"descriptor"`
	ResourceAttributes json.RawMessage `json:"resourceAttributes,omitempty"`
}

// ToCDSCatalog strips BPP-internal fields (PublishDirectives)
// from a Catalog before forwarding to the CDS. Exported for use by dashboardsvc.
func ToCDSCatalog(cat Catalog) CDSCatalog {
	cdsResources := make([]CDSResource, len(cat.Resources))
	for i, r := range cat.Resources {
		cdsResources[i] = CDSResource{
			ID:                 r.ID,
			Descriptor:         r.Descriptor,
			ResourceAttributes: r.ResourceAttributes,
		}
	}
	return CDSCatalog{
		ID:         cat.ID,
		Descriptor: cat.Descriptor,
		Provider:   cat.Provider,
		Resources:  cdsResources,
		Offers:     cat.Offers,
		Validity:   cat.Validity,
	}
}

// ---------------------------------------------------------------------------
// onix-catalog-publisher payload types — a separate destination from the CDS
// types above, with its own shape (bppId/bppUri/isActive on the catalog,
// provider+availableAt duplicated per resource, and publishDirectives lifted
// to a top-level array keyed by catalogId). Used only by the provider-facing
// publish path (PublishService.forwardToCDS); the CDS types above are
// untouched and still used by the dashboard publish path.
// ---------------------------------------------------------------------------

type OnixPublishRequest struct {
	Context BecknContext       `json:"context"`
	Message OnixPublishMessage `json:"message"`
	// Retire lists namespaced "<bppId>/<catalogId>" ids to permanently
	// retire -- the catalog-publisher plugin's own addition, a sibling of
	// context/message with no beckn.yaml equivalent. A call may carry
	// Retire with an empty Message.Catalogs; the plugin accepts that as a
	// pure retirement, no catalog content required.
	Retire []string `json:"retire,omitempty"`
	// ForceBaseline bypasses onix-catalog-publish's diff-against-prior-state
	// logic and always writes a fresh baseline instead of a change file --
	// the manual counterpart to its compactionChangeCountThreshold/
	// compactionSizeRatioThreshold config (which trigger the same thing
	// automatically). Deliberately a top-level sibling of context/message,
	// not nested per-catalog under message.publishDirectives[] -- verified
	// empirically against the actual deployed fidedocker/catalog-publisher
	// image (2026-08-14 build): a per-directive forceBaseline was silently
	// ignored (mode=unchanged); this top-level field produced a real
	// mode=baseline. Applies to every catalog in this call uniformly.
	ForceBaseline bool `json:"forceBaseline,omitempty"`
}

type OnixPublishMessage struct {
	Catalogs          []OnixCatalog          `json:"catalogs"`
	PublishDirectives []OnixPublishDirective `json:"publishDirectives,omitempty"`
}

type OnixCatalog struct {
	ID         string         `json:"id"`
	BppID      string         `json:"bppId"`
	BppURI     string         `json:"bppUri"`
	IsActive   bool           `json:"isActive"`
	Descriptor Descriptor     `json:"descriptor"`
	Provider   Provider       `json:"provider"`
	Resources  []OnixResource `json:"resources,omitempty"`
	Offers     []Offer        `json:"offers,omitempty"`
	Validity   *TimePeriod    `json:"validity,omitempty"`
}

// OnixResource mirrors Resource but adds provider + availableAt (duplicated
// from the catalog's provider — no per-resource location data exists yet)
// and omits rating (no rating aggregate exists yet) and stockQuantity
// (BPP-internal, same reasoning as CDSResource).
type OnixResource struct {
	ID                 string          `json:"id"`
	Descriptor         Descriptor      `json:"descriptor"`
	ResourceAttributes json.RawMessage `json:"resourceAttributes,omitempty"`
	Provider           *Provider       `json:"provider,omitempty"`
	AvailableAt        []Location      `json:"availableAt,omitempty"`
}

type OnixPublishDirective struct {
	CatalogID   string   `json:"catalogId"`
	VisibleTo   []string `json:"visibleTo,omitempty"`
	CatalogType string   `json:"catalogType,omitempty"`
}

// onixVisibleToNetworks is a hardcoded placeholder for publishDirectives.visibleTo
// until a real per-catalog/config-driven source exists.
var onixVisibleToNetworks = []string{
	"beckn.one/testnet",
	"nfh.global/testnet",
	"ion.id/ion-launch",
	"ion.id/staging",
	"ion.id/testnet",
}

// ToOnixCatalog converts a Catalog into the shape onix-catalog-publisher
// expects: bppId/bppUri/isActive stamped on the catalog, and the catalog's
// provider (incl. availableAt) duplicated onto every resource.
//
// The catalog id is namespaced as "<bppId>/<catalogId>" — a bare id isn't
// guaranteed unique across the network (two different BPPs could otherwise
// publish colliding catalog ids). Must match ToOnixPublishDirective's
// CatalogID exactly, since the plugin associates a publish directive with
// its catalog by this id.
func ToOnixCatalog(cat Catalog, bppID, bppURI string) OnixCatalog {
	resources := make([]OnixResource, len(cat.Resources))
	for i, r := range cat.Resources {
		provider := cat.Provider
		resources[i] = OnixResource{
			ID:                 r.ID,
			Descriptor:         r.Descriptor,
			ResourceAttributes: r.ResourceAttributes,
			Provider:           &provider,
			AvailableAt:        cat.Provider.AvailableAt,
		}
	}
	return OnixCatalog{
		ID:         bppID + "/" + cat.ID,
		BppID:      bppID,
		BppURI:     bppURI,
		IsActive:   cat.IsActive,
		Descriptor: cat.Descriptor,
		Provider:   cat.Provider,
		Resources:  resources,
		Offers:     cat.Offers,
		Validity:   cat.Validity,
	}
}

// ToOnixPublishDirective builds the top-level publishDirectives entry for
// one catalog, keyed by catalogId so multi-catalog requests produce one
// directive per catalog. bppID must be the same value passed to
// ToOnixCatalog for this catalog — see its doc comment on the id namespacing.
func ToOnixPublishDirective(cat Catalog, bppID string) OnixPublishDirective {
	catalogType := "REGULAR"
	if cat.PublishDirectives != nil && cat.PublishDirectives.CatalogType != "" {
		catalogType = strings.ToUpper(cat.PublishDirectives.CatalogType)
	}
	return OnixPublishDirective{
		CatalogID:   bppID + "/" + cat.ID,
		VisibleTo:   onixVisibleToNetworks,
		CatalogType: catalogType,
	}
}

// ---------------------------------------------------------------------------
// Shared domain types (used in both provider request and CDS forward payload)
// ---------------------------------------------------------------------------

type Catalog struct {
	ID                string             `json:"id"`
	Descriptor        Descriptor         `json:"descriptor"`
	Provider          Provider           `json:"provider"`
	Resources         []Resource         `json:"resources,omitempty"`
	Offers            []Offer            `json:"offers,omitempty"`
	Validity          *TimePeriod        `json:"validity,omitempty"`
	PublishDirectives *PublishDirectives `json:"publishDirectives,omitempty"`
	// IsActive mirrors catalogs.is_active -- a freely-reversible pause/resume
	// signal (distinct from retiring a catalog), consumed by ToOnixCatalog.
	IsActive bool `json:"-"`
}

type Descriptor struct {
	Name      string      `json:"name"`
	Code      string      `json:"code,omitempty"`
	ShortDesc string      `json:"shortDesc,omitempty"`
	LongDesc  string      `json:"longDesc,omitempty"`
	MediaFile []MediaFile `json:"mediaFile,omitempty"`
}

type MediaFile struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Label    string `json:"label,omitempty"`
}

type Provider struct {
	ID          string     `json:"id"`
	Descriptor  Descriptor `json:"descriptor"`
	AvailableAt []Location `json:"availableAt,omitempty"`
}

type Location struct {
	Geo     GeoJSON `json:"geo"`
	Address Address `json:"address"`
}

type GeoJSON struct {
	Type        string    `json:"type" example:"Point"`
	Coordinates []float64 `json:"coordinates"`
}

type Address struct {
	StreetAddress   string `json:"streetAddress,omitempty"`
	AddressLocality string `json:"addressLocality,omitempty"`
	AddressRegion   string `json:"addressRegion,omitempty"`
	PostalCode      string `json:"postalCode,omitempty"`
	AddressCountry  string `json:"addressCountry,omitempty"`
}

type Resource struct {
	ID         string     `json:"id"`
	Descriptor Descriptor `json:"descriptor"`
	// JSON-LD resource attributes (@context and @type are stored too), e.g.
	// crc and availability.status (IN_STOCK, LOW_STOCK, OUT_OF_STOCK).
	// Stored and published as-is.
	ResourceAttributes json.RawMessage `json:"resourceAttributes,omitempty" swaggertype:"object"`
}

type Offer struct {
	ID             string          `json:"id"`
	Descriptor     Descriptor      `json:"descriptor"`
	Provider       *Provider       `json:"provider,omitempty"`
	ResourceIDs    []string        `json:"resourceIds"`
	Considerations []Consideration `json:"considerations,omitempty"`
	Validity       *TimePeriod     `json:"validity,omitempty"`
	// JSON-LD offer attributes (policies such as returns, and commission and
	// payout terms). Missing terms are filled with BPP defaults when written.
	OfferAttributes json.RawMessage `json:"offerAttributes,omitempty" swaggertype:"object"`
}

type Consideration struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	// JSON-LD price attributes (currency, totalAmount, breakup, ...).
	ConsiderationAttributes json.RawMessage `json:"considerationAttributes,omitempty" swaggertype:"object"`
}

type Status struct {
	Name string `json:"name"`
	Code string `json:"code,omitempty"`
}

type TimePeriod struct {
	// RFC 3339. Values that can't be parsed are stored as NULL.
	StartDate string `json:"startDate,omitempty" format:"date-time"`
	EndDate   string `json:"endDate,omitempty" format:"date-time"`
}

type PublishDirectives struct {
	// master or regular are stored; sent upper-cased as the publish
	// directive's catalogType (default REGULAR).
	CatalogType string `json:"catalogType,omitempty" example:"regular"`
}
