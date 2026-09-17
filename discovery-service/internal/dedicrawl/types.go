// Package dedicrawl fetches Provider Node-hosted, self-signed catalog
// files from a directly-configured list of catalog index URLs
// (CATALOG_INDEX_URLS), verifying each file's content digest before
// trusting it. It replaces the earlier internal/catalogsource stopgap
// (D17), which polled a BPP's internal dashboard API and required a
// Zitadel seller bearer token that has no legitimate way to see every
// provider's catalogs anyway.
//
// See internal/service.CatalogStore: dedicrawl.Store implements that same
// port, so nothing outside this package needs to change to consume it.
//
// See CONTEXT.md D19 for why this is index-URL-direct rather than
// Registry/SubscriberRecord-resolved, and why signature verification
// isn't implemented yet.
package dedicrawl

import "github.com/remiges-tushar/discovery-service/internal/beckn"

// Catalog types (MASTER extends via Dependencies; REGULAR is a leaf).
const (
	CatalogTypeMaster  = "MASTER"
	CatalogTypeRegular = "REGULAR"
)

// Signature is a Provider Node's detached signature over a file/entry's
// canonical form (minus this field itself), as observed on real catalog
// index/file responses. It is parsed and carried through, but Crawler
// does NOT verify it (see CONTEXT.md D19) — only each file's content
// Digest is checked.
type Signature struct {
	KeyID            string `json:"keyId"`
	Value            string `json:"value"`
	Canonicalization string `json:"canonicalization,omitempty"`
}

// CatalogFile is a Provider Node's self-signed baseline catalog document.
// It wraps the existing beckn.Catalog schema unmodified.
type CatalogFile struct {
	CatalogID  string        `json:"catalogId"`
	Version    int           `json:"version"`
	NextUpdate string        `json:"next_update,omitempty"`
	Catalog    beckn.Catalog `json:"catalog"`
	RetiredAt  *string       `json:"retiredAt,omitempty"`
	Signature  Signature     `json:"signature"`
}

// ResourceDelta is the resources half of a CatalogChangeFile.
type ResourceDelta struct {
	Upserts  []beckn.Resource `json:"upserts,omitempty"`
	Removals []string         `json:"removals,omitempty"`
}

// OfferDelta is the offers half of a CatalogChangeFile.
type OfferDelta struct {
	Upserts  []beckn.Offer `json:"upserts,omitempty"`
	Removals []string      `json:"removals,omitempty"`
}

// CatalogChangeFile is a Provider Node's self-signed incremental delta
// against a prior version.
type CatalogChangeFile struct {
	CatalogID   string         `json:"catalogId"`
	FromVersion int            `json:"fromVersion"`
	ToVersion   int            `json:"toVersion"`
	Resources   ResourceDelta  `json:"resources"`
	Offers      OfferDelta     `json:"offers"`
	Catalog     *beckn.Catalog `json:"catalog,omitempty"`
	Signature   Signature      `json:"signature"`
}

// FileRef points at a signed, content-addressed baseline or "latest" file.
type FileRef struct {
	Version int    `json:"version"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	Digest  string `json:"digest"`
}

// ChangeRef points at one signed change file spanning a version range.
type ChangeRef struct {
	FromVersion int    `json:"fromVersion"`
	ToVersion   int    `json:"toVersion"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
	Digest      string `json:"digest"`
}

// MasterRef is a REGULAR catalog's declared dependency on a MASTER
// catalog it extends.
type MasterRef struct {
	CatalogID string `json:"catalogId"`
	Version   int    `json:"version"`
	IndexURL  string `json:"indexUrl"`
}

// IndexEntry is one Provider Node's declaration, within its catalog
// index, of a single catalog's current publication state.
type IndexEntry struct {
	CatalogID    string `json:"catalogId"`
	EntryVersion int    `json:"entryVersion"`
	CatalogType  string `json:"catalogType"`
	Dependencies struct {
		Masters []MasterRef `json:"masters,omitempty"`
	} `json:"dependencies"`
	NetworkIDs  []string    `json:"networkIds,omitempty"`
	SchemaTypes []string    `json:"schemaTypes,omitempty"`
	IsActive    bool        `json:"isActive"`
	Baseline    FileRef     `json:"baseline"`
	Changes     []ChangeRef `json:"changes,omitempty"`
	Latest      *FileRef    `json:"latest,omitempty"`
	RetiredAt   *string     `json:"retiredAt,omitempty"`
	Signature   Signature   `json:"signature"`
}

// CatalogIndex is the top-level document served at a configured catalog
// index URL (e.g. ".../index/becknCatalogs.index.json").
type CatalogIndex struct {
	NodeID     string       `json:"nodeId"`
	NextUpdate string       `json:"next_update,omitempty"`
	Catalogs   []IndexEntry `json:"catalogs"`
}
