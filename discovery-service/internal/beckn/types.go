// Package beckn contains wire types for the Beckn Protocol v2.0.0 /discover
// and /on_discover actions, as vendored in indonesiaopennetwork/ion-specs
// schema/core/v2/api/v2.0.0/beckn.yaml. Field names are v2 (resources,
// resourceAttributes, camelCase context) — v1 shapes are not supported.
package beckn

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Context carries addressing, protocol version, and message identity for
// every Beckn v2 API call. See beckn.yaml#/components/schemas/Context.
type Context struct {
	Domain        string   `json:"domain,omitempty"`
	Action        string   `json:"action"`
	Version       string   `json:"version"`
	BapID         string   `json:"bapId,omitempty"`
	BapURI        string   `json:"bapUri,omitempty"`
	BppID         string   `json:"bppId,omitempty"`
	BppURI        string   `json:"bppUri,omitempty"`
	TransactionID string   `json:"transactionId"`
	MessageID     string   `json:"messageId"`
	NetworkID     string   `json:"networkId,omitempty"`
	Timestamp     string   `json:"timestamp,omitempty"`
	TTL           string   `json:"ttl,omitempty"`
	SchemaContext []string `json:"schemaContext,omitempty"`
}

// DiscoverRequest is the POST /discover request body.
type DiscoverRequest struct {
	Context Context         `json:"context"`
	Message DiscoverMessage `json:"message"`
}

type DiscoverMessage struct {
	Intent Intent `json:"intent"`
}

// Intent is the declaration of discovery intent. All fields are optional and
// independently combinable per beckn.yaml#/components/schemas/Intent.
type Intent struct {
	TextSearch  string              `json:"textSearch,omitempty"`
	Filters     *Filters            `json:"filters,omitempty"`
	Spatial     []SpatialConstraint `json:"spatial,omitempty"`
	MediaSearch *MediaSearch        `json:"mediaSearch,omitempty"`
}

type Filters struct {
	Type       string `json:"type"`
	Expression string `json:"expression"`
}

type SpatialConstraint struct {
	Op             string           `json:"op"`
	Targets        any              `json:"targets"`
	Geometry       *GeoJSONGeometry `json:"geometry,omitempty"`
	DistanceMeters *float64         `json:"distanceMeters,omitempty"`
	Quantifier     string           `json:"quantifier,omitempty"`
	SRID           string           `json:"srid,omitempty"`
}

type GeoJSONGeometry struct {
	Type        string            `json:"type"`
	Coordinates any               `json:"coordinates,omitempty"`
	Geometries  []GeoJSONGeometry `json:"geometries,omitempty"`
	Bbox        []float64         `json:"bbox,omitempty"`
}

type MediaSearch struct {
	Media   []MediaInput `json:"media,omitempty"`
	Options any          `json:"options,omitempty"`
}

type MediaInput struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

// Ack is the synchronous response to every accepted (or rejected) Beckn v2
// request. Signature is a CounterSignature proving receipt/processing.
type Ack struct {
	Status    string    `json:"status"`
	Signature Signature `json:"signature"`
	Error     *Error    `json:"error,omitempty"`
}

// Signature is a Beckn HTTP Signature / CounterSignature per beckn.yaml
// #/components/schemas/Signature — a STRING following the
// draft-cavage-http-signatures-12 profile:
//
//	Signature keyId="{subscriberId}|{uniqueKeyId}|{algorithm}",algorithm="{algorithm}",created="{unixTimestamp}",expires="{unixTimestamp}",headers="{signedHeaders}",signature="{base64Signature}"
//
// It is NOT a JSON object — marshaling this as `{"keyId": ..., ...}`
// (an earlier version of this codebase did exactly that) does not
// conform to the spec. Build one with FormatSignature.
type Signature string

// FormatSignature assembles a Signature string per the pattern above.
// keyID is the FQDN subscriber identity ({subscriberId}), uniqueKeyID
// identifies the specific signing key, created/expires are Unix
// timestamps (seconds), headers is the space-separated signed-header
// list (MUST include "(created) (expires) digest" per spec), and
// signatureValue is the base64-encoded signature bytes.
func FormatSignature(subscriberID, uniqueKeyID, algorithm string, created, expires int64, headers, signatureValue string) Signature {
	return Signature(fmt.Sprintf(
		`Signature keyId="%s|%s|%s",algorithm="%s",created="%d",expires="%d",headers="%s",signature="%s"`,
		subscriberID, uniqueKeyID, algorithm, algorithm, created, expires, headers, signatureValue,
	))
}

// SplitKeyID splits a combined "{subscriberId}|{uniqueKeyId}|{algorithm}"
// key id — the format root .env's BAP_KEY_ID/BPP_KEY_ID already use, and
// what a Signature header's own "keyId" field carries — into its three
// parts. Reuse this instead of asking for the parts pre-split: this repo's
// other Go services (see bap-application/internal/clientsvc/becknsign.go)
// already pass BAP_KEY_ID/BPP_KEY_ID through as a single combined string.
func SplitKeyID(combined string) (subscriberID, uniqueKeyID, algorithm string, err error) {
	parts := strings.SplitN(combined, "|", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf(`keyId %q is not "subscriberId|uniqueKeyId|algorithm"`, combined)
	}
	return parts[0], parts[1], parts[2], nil
}

// ParsedSignature holds the fields extracted from an inbound Authorization
// header by ParseSignature, ready for a verifier to check.
type ParsedSignature struct {
	SubscriberID string
	UniqueKeyID  string
	Algorithm    string
	Created      int64
	Expires      int64
	Headers      string
	Signature    []byte // decoded from the header's base64 "signature" field
}

// signatureFieldPattern matches one `key="value"` pair inside the header,
// e.g. `keyId="a|b|c"` or `created="1234"`.
var signatureFieldPattern = regexp.MustCompile(`(\w+)="([^"]*)"`)

// ParseSignature parses an inbound Authorization header value formatted per
// FormatSignature's pattern (the "Signature " prefix is optional — callers
// may pass either the full header value or just its parameter list).
func ParseSignature(header string) (ParsedSignature, error) {
	header = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(header), "Signature"))

	fields := map[string]string{}
	for _, m := range signatureFieldPattern.FindAllStringSubmatch(header, -1) {
		fields[m[1]] = m[2]
	}

	keyID, ok := fields["keyId"]
	if !ok {
		return ParsedSignature{}, fmt.Errorf("missing keyId field")
	}
	subscriberID, uniqueKeyID, algorithm, err := SplitKeyID(keyID)
	if err != nil {
		return ParsedSignature{}, err
	}

	sigB64, ok := fields["signature"]
	if !ok {
		return ParsedSignature{}, fmt.Errorf("missing signature field")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return ParsedSignature{}, fmt.Errorf("decode base64 signature: %w", err)
	}

	created, err := strconv.ParseInt(fields["created"], 10, 64)
	if err != nil {
		return ParsedSignature{}, fmt.Errorf("invalid created timestamp %q: %w", fields["created"], err)
	}
	expires, err := strconv.ParseInt(fields["expires"], 10, 64)
	if err != nil {
		return ParsedSignature{}, fmt.Errorf("invalid expires timestamp %q: %w", fields["expires"], err)
	}

	return ParsedSignature{
		SubscriberID: subscriberID,
		UniqueKeyID:  uniqueKeyID,
		Algorithm:    algorithm,
		Created:      created,
		Expires:      expires,
		Headers:      fields["headers"],
		Signature:    sig,
	}, nil
}

type Error struct {
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
}

// OnDiscoverRequest is the POST {bapUri}/on_discover callback body we send.
type OnDiscoverRequest struct {
	Context Context           `json:"context"`
	Message OnDiscoverMessage `json:"message"`
}

type OnDiscoverMessage struct {
	Catalogs []Catalog `json:"catalogs"`
}

type Catalog struct {
	ID         string      `json:"id"`
	BppID      string      `json:"bppId,omitempty"`
	BppURI     string      `json:"bppUri,omitempty"`
	IsActive   *bool       `json:"isActive,omitempty"`
	Descriptor Descriptor  `json:"descriptor"`
	Provider   Provider    `json:"provider"`
	Resources  []Resource  `json:"resources,omitempty"`
	Offers     []Offer     `json:"offers,omitempty"`
	Validity   *TimePeriod `json:"validity,omitempty"`
}

type Descriptor struct {
	Code           string      `json:"code,omitempty"`
	Name           string      `json:"name,omitempty"`
	ShortDesc      string      `json:"shortDesc,omitempty"`
	LongDesc       string      `json:"longDesc,omitempty"`
	ThumbnailImage string      `json:"thumbnailImage,omitempty"`
	MediaFile      []MediaFile `json:"mediaFile,omitempty"`
}

// MediaFile is an image/audio/video attached to a Descriptor.
type MediaFile struct {
	Label    string `json:"label,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
}

type Provider struct {
	ID                 string         `json:"id"`
	Descriptor         Descriptor     `json:"descriptor"`
	AvailableAt        []Location     `json:"availableAt,omitempty"`
	ProviderAttributes map[string]any `json:"providerAttributes,omitempty"`
}

type Location struct {
	Geo     GeoJSONGeometry `json:"geo"`
	Address *Address        `json:"address,omitempty"`
}

type Address struct {
	AddressCountry  string `json:"addressCountry,omitempty"`
	AddressLocality string `json:"addressLocality,omitempty"`
	AddressRegion   string `json:"addressRegion,omitempty"`
	ExtendedAddress string `json:"extendedAddress,omitempty"`
	PostalCode      string `json:"postalCode,omitempty"`
	StreetAddress   string `json:"streetAddress,omitempty"`
}

type Resource struct {
	ID                 string         `json:"id"`
	Descriptor         Descriptor     `json:"descriptor"`
	ResourceAttributes map[string]any `json:"resourceAttributes,omitempty"`
}

type Offer struct {
	ID             string          `json:"id"`
	Descriptor     Descriptor      `json:"descriptor"`
	Provider       *Provider       `json:"provider,omitempty"`
	ResourceIDs    []string        `json:"resourceIds,omitempty"`
	Considerations []Consideration `json:"considerations,omitempty"`
	Validity       *TimePeriod     `json:"validity,omitempty"`
	// OfferAttributes is an open JSON-LD bag for domain-specific offer
	// terms (return/payment/serviceability policies, etc.) — see
	// beckn.yaml#/components/schemas/Attributes.
	OfferAttributes map[string]any `json:"offerAttributes,omitempty"`
}

// Consideration is the value proposed/exchanged under an Offer — e.g. a
// price with a breakup. See beckn.yaml#/components/schemas/Consideration.
type Consideration struct {
	ID                      string         `json:"id"`
	Status                  Descriptor     `json:"status"`
	ConsiderationAttributes map[string]any `json:"considerationAttributes,omitempty"`
}

type TimePeriod struct {
	StartDate string `json:"startDate,omitempty"`
	EndDate   string `json:"endDate,omitempty"`
	StartTime string `json:"startTime,omitempty"`
	EndTime   string `json:"endTime,omitempty"`
}
