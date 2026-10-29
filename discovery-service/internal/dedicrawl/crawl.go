package dedicrawl

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"

	"github.com/remiges-tech/logharbour/logharbour"
	"github.com/remiges-tushar/discovery-service/internal/beckn"
)

// defaultCutoverFraction is used when Crawler is constructed with a
// non-positive fraction (config validation stopgap — see NewCrawler).
const defaultCutoverFraction = 0.5

// gzipMagic is gzip's two-byte magic number. Real catalog index/file
// responses observed in the wild are served as raw .json.gz bytes with no
// Content-Encoding header announcing that, so fetchBytes detects and
// decompresses by content instead of trusting headers.
var gzipMagic = [2]byte{0x1f, 0x8b}

// Crawler fetches and indexes catalogs from a fixed, directly-configured
// list of catalog index URLs (CATALOG_INDEX_URLS), verifying each file's
// content Digest before trusting it.
//
// Deliberately NOT implemented, see CONTEXT.md D19 for the full reasoning:
//   - Registry/Manifest/SubscriberRecord resolution — no real, live DeDi
//     registry lookup exists to integrate against yet, so index URLs are
//     configured directly instead of resolved from a subscriber reference.
//   - Signature verification — the real keyId format observed on live
//     index/catalog files doesn't decode as a plain Ed25519 public key by
//     any means found so far. Only each file's content Digest is checked.
//     This means a compromised or malicious index URL could serve
//     tampered-but-internally-consistent data undetected; revisit once
//     the real signing scheme is known.
//
// A single Crawler is meant to be long-lived across repeated crawls (see
// Store): its cursorState remembers each catalog's last-indexed version
// and content, so unchanged catalogs are skipped without re-fetching, and
// a retiredAt observation is never reversed by a later crawl that simply
// omits the entry.
type Crawler struct {
	client *http.Client
	logger *logharbour.Logger
	cursor *cursorState

	// networkIDs/schemaTypes optionally scope which index entries this DS
	// indexes at all. Empty means "accept everything" — no scoping
	// configured.
	networkIDs  []string
	schemaTypes []string

	// cutoverFraction is the cutover rule's threshold: fetch the baseline
	// instead of the accumulated change-file chain once the chain's
	// combined size exceeds this fraction of the baseline size.
	cutoverFraction float64
}

// NewCrawler builds a Crawler. cutoverFraction <= 0 falls back to
// defaultCutoverFraction rather than disabling the rule entirely (a
// zero/negative threshold would mean "always cut over", which silently
// defeats the point of change files).
func NewCrawler(client *http.Client, logger *logharbour.Logger, networkIDs, schemaTypes []string, cutoverFraction float64) *Crawler {
	if cutoverFraction <= 0 {
		cutoverFraction = defaultCutoverFraction
	}
	return &Crawler{
		client:          client,
		logger:          logger,
		cursor:          newCursorState(),
		networkIDs:      networkIDs,
		schemaTypes:     schemaTypes,
		cutoverFraction: cutoverFraction,
	}
}

// CrawlAll crawls every configured index URL and returns the full current
// catalog snapshot (across every URL ever crawled by this Crawler, since
// cursorState is cumulative). anySuccess is false only when every index
// URL failed outright (unreachable, invalid JSON) — callers should keep
// serving the previous snapshot rather than replacing it with this
// (possibly empty) one in that case.
func (c *Crawler) CrawlAll(ctx context.Context, indexURLs []string) (catalogs []beckn.Catalog, anySuccess bool) {
	for _, indexURL := range indexURLs {
		if _, err := c.crawlIndex(ctx, indexURL); err != nil {
			c.logger.Warn().LogActivity("catalog index crawl failed", map[string]any{
				"indexUrl": indexURL,
				"error":    err.Error(),
			})
			continue
		}
		anySuccess = true
	}
	return c.cursor.snapshot(), anySuccess
}

func (c *Crawler) crawlIndex(ctx context.Context, indexURL string) (int, error) {
	raw, err := c.fetchBytes(ctx, indexURL)
	if err != nil {
		return 0, fmt.Errorf("fetch catalog index: %w", err)
	}
	var index CatalogIndex
	if err := json.Unmarshal(raw, &index); err != nil {
		return 0, fmt.Errorf("decode catalog index: %w", err)
	}

	ordered := orderMastersFirst(index.Catalogs)

	indexed := 0
	for _, entry := range ordered {
		if !c.inScope(entry) {
			continue
		}
		if err := c.applyEntry(ctx, entry); err != nil {
			c.logger.Warn().LogActivity("catalog entry discarded", map[string]any{
				"catalogId": entry.CatalogID,
				"error":     err.Error(),
			})
			continue
		}
		indexed++
	}
	return indexed, nil
}

// orderMastersFirst returns entries with CatalogType==MASTER before all
// others, preserving relative order within each group, so a REGULAR
// catalog's dependency is always indexed before it.
func orderMastersFirst(entries []IndexEntry) []IndexEntry {
	out := make([]IndexEntry, 0, len(entries))
	for _, e := range entries {
		if e.CatalogType == CatalogTypeMaster {
			out = append(out, e)
		}
	}
	for _, e := range entries {
		if e.CatalogType != CatalogTypeMaster {
			out = append(out, e)
		}
	}
	return out
}

func (c *Crawler) inScope(entry IndexEntry) bool {
	if len(c.networkIDs) > 0 && !intersects(c.networkIDs, entry.NetworkIDs) {
		return false
	}
	if len(c.schemaTypes) > 0 && !intersects(c.schemaTypes, entry.SchemaTypes) {
		return false
	}
	return true
}

func intersects(want, have []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, v := range have {
		set[v] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; ok {
			return true
		}
	}
	return false
}

// applyEntry advances the cursor for entry.CatalogID: honoring a signed
// retiredAt permanently, skipping a fetch when entryVersion is unchanged,
// and otherwise fetching + digest-verifying + applying the baseline or
// change file(s) that bring the catalog up to entry.EntryVersion.
func (c *Crawler) applyEntry(ctx context.Context, entry IndexEntry) error {
	prev, known := c.cursor.get(entry.CatalogID)
	if prev.retired {
		// Permanent tombstone: never resurrected by a later crawl that
		// simply omits/repeats an entry.
		return nil
	}

	if entry.RetiredAt != nil {
		c.cursor.put(entry.CatalogID, cursorEntry{
			entryVersion: entry.EntryVersion,
			isActive:     false,
			retired:      true,
		})
		return nil
	}

	if known && prev.entryVersion == entry.EntryVersion {
		// Unchanged: keep prior content, just refresh isActive in case a
		// pause/unpause happened without a version bump.
		prev.isActive = entry.IsActive
		c.cursor.put(entry.CatalogID, prev)
		return nil
	}

	catalog, newContentVersion, err := c.resolveCatalog(ctx, entry, prev, known)
	if err != nil {
		return err
	}

	c.cursor.put(entry.CatalogID, cursorEntry{
		entryVersion:   entry.EntryVersion,
		contentVersion: newContentVersion,
		isActive:       entry.IsActive,
		catalog:        catalog,
	})
	return nil
}

// targetContentVersion is the content-counter version entry's files
// actually bring a catalog up to -- entry.Latest.Version when the PN
// declares it, otherwise the highest toVersion among entry.Changes (or
// entry.Baseline.Version if there are no changes at all). This is
// deliberately NOT entry.EntryVersion: that counter advances on every
// publish call (including no-op ones, e.g. a pure isActive toggle) while
// baseline.version/latest.version/each change's fromVersion-toVersion use
// a separate counter that only advances on a real content change. Once
// those two counters diverge -- which a single no-op publish is enough to
// cause -- entry.EntryVersion no longer corresponds to any file the PN
// actually wrote, so chaining (or even comparing) against it directly
// can never succeed again.
func targetContentVersion(entry IndexEntry) int {
	if entry.Latest != nil {
		return entry.Latest.Version
	}
	target := entry.Baseline.Version
	for _, cr := range entry.Changes {
		if cr.ToVersion > target {
			target = cr.ToVersion
		}
	}
	return target
}

// resolveCatalog fetches whatever's needed to bring entry.CatalogID's
// content up to targetContentVersion(entry).
//
// It first tries to extend the chain directly from the stored cursor --
// but only when that cursor is at or past entry.Baseline.Version. A
// cursor behind the current baseline means a baseline rewrite (a real
// compaction, or this project's own automatic one: see
// compactionSizeRatioThreshold, which a small catalog's change files can
// trivially exceed) landed since we last looked, superseding whatever
// chain our cursor was following -- entry.Changes may still list older,
// now-orphaned entries from before that rewrite (kept for a grace period
// rather than deleted, see NFH-014 CON-TBD-32), so walking from a
// pre-rewrite cursor would either dead-end or silently skip the rewrite.
//
// Either way (direct extension not possible, or it doesn't fully reach
// the target -- e.g. the cutover rule prefers a fresh baseline), it falls
// back to fetching the baseline and then -- critically -- ALSO tries to
// walk the chain forward from there: a baseline rewrite reaching exactly
// the target is the common case, but a further real content change can
// land in entry.Changes on top of that same rewrite (exactly what happens
// when a no-op publish triggers a baseline rewrite and is immediately
// followed by a real one), and simply stopping at the baseline would
// silently miss it. The same walkChain helper handles a catalog's very
// first sighting too (known=false): it degenerates to "nothing reachable
// from the baseline alone falls through to baseline+continue" below.
func (c *Crawler) resolveCatalog(ctx context.Context, entry IndexEntry, prev cursorEntry, known bool) (beckn.Catalog, int, error) {
	target := targetContentVersion(entry)

	if known && prev.contentVersion >= entry.Baseline.Version {
		// honorCutover=true here: prev is an old cursor, so a large pile
		// of accumulated changes genuinely has a cheaper alternative --
		// just fetch the current baseline instead.
		catalog, version, ok, err := c.walkChain(ctx, entry, prev, target, true)
		if err != nil {
			return beckn.Catalog{}, 0, err
		}
		if ok {
			return catalog, version, nil
		}
	}

	catalog, baseVersion, err := c.fetchBaseline(ctx, entry)
	if err != nil {
		return beckn.Catalog{}, 0, err
	}
	base := cursorEntry{contentVersion: baseVersion, catalog: catalog}
	// honorCutover=false here: base IS the baseline already -- there is
	// no cheaper alternative to fall back to, so whatever changes exist
	// on top of it must be applied regardless of their size relative to
	// the baseline (a tiny catalog's own change files routinely exceed
	// cutoverFraction of its tiny baseline; rejecting them here would
	// strand the cursor at the baseline forever instead of reaching the
	// catalog's actual current content).
	if extended, version, ok, err := c.walkChain(ctx, entry, base, target, false); err != nil {
		return beckn.Catalog{}, 0, err
	} else if ok {
		return extended, version, nil
	}
	return catalog, baseVersion, nil
}

// walkChain tries to advance from's content to target using entry.Changes.
// When honorCutover is set, a chain larger than cutoverFraction of the
// baseline size is rejected (ok=false) in favor of the caller fetching a
// fresh baseline instead -- appropriate only when from is an old cursor a
// fresh baseline would genuinely be cheaper than catching up from, never
// when from already IS that baseline (see resolveCatalog). ok is also
// false (not an error) when no chain reaches target from
// from.contentVersion at all.
func (c *Crawler) walkChain(ctx context.Context, entry IndexEntry, from cursorEntry, target int, honorCutover bool) (beckn.Catalog, int, bool, error) {
	if from.contentVersion == target {
		return from.catalog, from.contentVersion, true, nil
	}

	chain, ok := buildChangeChain(entry.Changes, from.contentVersion, target)
	if !ok {
		return beckn.Catalog{}, 0, false, nil
	}

	if honorCutover {
		var chainSize int64
		for _, cr := range chain {
			chainSize += cr.Size
		}
		if entry.Baseline.Size > 0 && float64(chainSize) > c.cutoverFraction*float64(entry.Baseline.Size) {
			return beckn.Catalog{}, 0, false, nil
		}
	}

	catalog := from.catalog
	for _, cr := range chain {
		delta, err := c.fetchChangeFile(ctx, entry.CatalogID, cr)
		if err != nil {
			return beckn.Catalog{}, 0, false, err
		}
		catalog = applyChange(catalog, delta)
	}
	return catalog, target, true, nil
}

// buildChangeChain walks entry.Changes to find a contiguous sequence
// from fromVersion to toVersion. ok is false if no such chain exists
// (e.g. the PN compacted history past our cursor).
func buildChangeChain(changes []ChangeRef, fromVersion, toVersion int) (chain []ChangeRef, ok bool) {
	byFrom := make(map[int]ChangeRef, len(changes))
	for _, cr := range changes {
		byFrom[cr.FromVersion] = cr
	}
	cursor := fromVersion
	for cursor != toVersion {
		cr, found := byFrom[cursor]
		if !found {
			return nil, false
		}
		chain = append(chain, cr)
		cursor = cr.ToVersion
	}
	return chain, true
}

func (c *Crawler) fetchBaseline(ctx context.Context, entry IndexEntry) (beckn.Catalog, int, error) {
	raw, err := c.fetchBytes(ctx, entry.Baseline.URL)
	if err != nil {
		return beckn.Catalog{}, 0, fmt.Errorf("fetch baseline: %w", err)
	}
	if err := VerifyDigest(raw, entry.Baseline.Digest); err != nil {
		return beckn.Catalog{}, 0, fmt.Errorf("baseline: %w", err)
	}
	var file CatalogFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return beckn.Catalog{}, 0, fmt.Errorf("decode baseline: %w", err)
	}
	if file.CatalogID != entry.CatalogID || file.Version != entry.Baseline.Version {
		return beckn.Catalog{}, 0, fmt.Errorf("baseline catalogId/version %q/%d does not match index entry %q/%d",
			file.CatalogID, file.Version, entry.CatalogID, entry.Baseline.Version)
	}
	return file.Catalog, entry.Baseline.Version, nil
}

func (c *Crawler) fetchChangeFile(ctx context.Context, catalogID string, ref ChangeRef) (CatalogChangeFile, error) {
	raw, err := c.fetchBytes(ctx, ref.URL)
	if err != nil {
		return CatalogChangeFile{}, fmt.Errorf("fetch change file: %w", err)
	}
	if err := VerifyDigest(raw, ref.Digest); err != nil {
		return CatalogChangeFile{}, fmt.Errorf("change file: %w", err)
	}
	var change CatalogChangeFile
	if err := json.Unmarshal(raw, &change); err != nil {
		return CatalogChangeFile{}, fmt.Errorf("decode change file: %w", err)
	}
	if change.CatalogID != catalogID || change.FromVersion != ref.FromVersion || change.ToVersion != ref.ToVersion {
		return CatalogChangeFile{}, fmt.Errorf("change file catalogId/version %q/%d->%d does not match index entry %q/%d->%d",
			change.CatalogID, change.FromVersion, change.ToVersion, catalogID, ref.FromVersion, ref.ToVersion)
	}
	return change, nil
}

// applyChange merges delta's upserts/removals onto base, replacing any
// catalog-level fields delta declares.
//
// delta.Catalog is a PARTIAL document: a Provider Node only includes the
// catalog-level fields that actually changed (e.g. a pure pause/resume
// toggle publishes {"isActive": true} alone, omitting descriptor/provider
// since they didn't change). Each field below is therefore merged onto
// base individually -- applied only when delta.Catalog actually declares
// it (non-nil pointer, non-empty string, or non-zero struct) -- rather
// than replacing base wholesale, which would wipe out every field the
// delta omitted.
func applyChange(base beckn.Catalog, delta CatalogChangeFile) beckn.Catalog {
	if delta.Catalog != nil {
		if delta.Catalog.ID != "" {
			base.ID = delta.Catalog.ID
		}
		if delta.Catalog.BppID != "" {
			base.BppID = delta.Catalog.BppID
		}
		if delta.Catalog.BppURI != "" {
			base.BppURI = delta.Catalog.BppURI
		}
		if delta.Catalog.IsActive != nil {
			base.IsActive = delta.Catalog.IsActive
		}
		if !reflect.DeepEqual(delta.Catalog.Descriptor, beckn.Descriptor{}) {
			base.Descriptor = delta.Catalog.Descriptor
		}
		if !reflect.DeepEqual(delta.Catalog.Provider, beckn.Provider{}) {
			base.Provider = delta.Catalog.Provider
		}
		if delta.Catalog.Validity != nil {
			base.Validity = delta.Catalog.Validity
		}
	}

	resources := make(map[string]beckn.Resource, len(base.Resources))
	for _, r := range base.Resources {
		resources[r.ID] = r
	}
	for _, r := range delta.Resources.Upserts {
		resources[r.ID] = r
	}
	for _, id := range delta.Resources.Removals {
		delete(resources, id)
	}
	base.Resources = mapValuesResources(resources)

	offers := make(map[string]beckn.Offer, len(base.Offers))
	for _, o := range base.Offers {
		offers[o.ID] = o
	}
	for _, o := range delta.Offers.Upserts {
		offers[o.ID] = o
	}
	for _, id := range delta.Offers.Removals {
		delete(offers, id)
	}
	base.Offers = mapValuesOffers(offers)

	return base
}

func mapValuesResources(m map[string]beckn.Resource) []beckn.Resource {
	out := make([]beckn.Resource, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func mapValuesOffers(m map[string]beckn.Offer) []beckn.Offer {
	out := make([]beckn.Offer, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func (c *Crawler) fetchBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: unexpected status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GET %s: read body: %w", url, err)
	}
	return maybeGunzip(body)
}

// maybeGunzip transparently decompresses body if it starts with gzip's
// magic number, regardless of what (if any) Content-Encoding/Content-Type
// headers the server sent — real catalog index/file responses are served
// as plain .json.gz bytes with no such header announcing it.
func maybeGunzip(body []byte) ([]byte, error) {
	if len(body) < 2 || body[0] != gzipMagic[0] || body[1] != gzipMagic[1] {
		return body, nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("open gzip reader: %w", err)
	}
	defer gz.Close() //nolint:errcheck
	decompressed, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("decompress gzip: %w", err)
	}
	return decompressed, nil
}
