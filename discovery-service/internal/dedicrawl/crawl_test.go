package dedicrawl

import (
	"encoding/json"
	"testing"

	"github.com/remiges-tushar/discovery-service/internal/beckn"
)

// setup builds a full working fixture: one MASTER-less REGULAR catalog
// served as a baseline CatalogFile, and a CatalogIndex pointing at it. It
// returns the Crawler and the index URL to crawl.
func setup(t *testing.T) (*Crawler, string) {
	t.Helper()

	file := CatalogFile{CatalogID: "cat-1", Version: 1, Catalog: sampleCatalog("cat-1")}
	rawFile := marshalFile(t, file)

	srv := fileServer(t, map[string][]byte{"/catalog-v1.json": rawFile})

	entry := IndexEntry{
		CatalogID:    "cat-1",
		EntryVersion: 1,
		CatalogType:  CatalogTypeRegular,
		IsActive:     true,
		Baseline: FileRef{
			Version: file.Version,
			URL:     srv.URL + "/catalog-v1.json",
			Size:    int64(len(rawFile)),
			Digest:  Digest(rawFile),
		},
	}
	rawIndex := marshalFile(t, CatalogIndex{NodeID: "pn-1", Catalogs: []IndexEntry{entry}})
	indexSrv := fileServer(t, map[string][]byte{"/index.json": rawIndex})

	crawler := NewCrawler(srv.Client(), testLogger(), nil, nil, 0.5)
	return crawler, indexSrv.URL + "/index.json"
}

func TestCrawlAll_IndexesAValidCatalog(t *testing.T) {
	crawler, indexURL := setup(t)

	catalogs, anySuccess := crawler.CrawlAll(t.Context(), []string{indexURL})
	if !anySuccess {
		t.Fatal("expected anySuccess=true for a reachable, valid index")
	}
	if len(catalogs) != 1 || catalogs[0].ID != "cat-1" {
		t.Fatalf("snapshot after crawl = %+v, want one catalog cat-1", catalogs)
	}
}

func TestCrawlAll_UnreachableIndexURLFails(t *testing.T) {
	crawler, _ := setup(t)
	_, anySuccess := crawler.CrawlAll(t.Context(), []string{"http://127.0.0.1:1/does-not-exist"})
	if anySuccess {
		t.Fatal("expected anySuccess=false for an unreachable index URL")
	}
}

func TestApplyEntry_DiscardsOnDigestMismatch(t *testing.T) {
	crawler, indexURL := setup(t)

	// A second, independent fixture whose baseline digest doesn't match
	// what's actually served.
	file := CatalogFile{CatalogID: "cat-2", Version: 1, Catalog: sampleCatalog("cat-2")}
	rawFile := marshalFile(t, file)
	srv := fileServer(t, map[string][]byte{"/f.json": rawFile})
	entry := IndexEntry{
		CatalogID: "cat-2", EntryVersion: 1, CatalogType: CatalogTypeRegular, IsActive: true,
		Baseline: FileRef{Version: file.Version, URL: srv.URL + "/f.json", Size: int64(len(rawFile)), Digest: "sha-256:wrong"},
	}
	rawIndex := marshalFile(t, CatalogIndex{NodeID: "pn-2", Catalogs: []IndexEntry{entry}})
	indexSrv := fileServer(t, map[string][]byte{"/idx.json": rawIndex})
	badIndexURL := indexSrv.URL + "/idx.json"

	badCrawler := NewCrawler(srv.Client(), testLogger(), nil, nil, 0.5)
	catalogs, anySuccess := badCrawler.CrawlAll(t.Context(), []string{badIndexURL})
	if !anySuccess {
		t.Fatal("a fetchable-but-all-entries-discarded index should still count as a successful crawl")
	}
	if len(catalogs) != 0 {
		t.Fatalf("expected nothing indexed after a digest mismatch, got %+v", catalogs)
	}

	// The original, valid fixture must still work independently.
	catalogs, anySuccess = crawler.CrawlAll(t.Context(), []string{indexURL})
	if !anySuccess || len(catalogs) != 1 {
		t.Fatalf("valid crawler should be unaffected: catalogs=%+v anySuccess=%v", catalogs, anySuccess)
	}
}

func TestCrawlAll_VersionUnchangedSkipsRefetch(t *testing.T) {
	crawler, indexURL := setup(t)
	if _, anySuccess := crawler.CrawlAll(t.Context(), []string{indexURL}); !anySuccess {
		t.Fatal("first crawl should succeed")
	}
	before, _ := crawler.cursor.get("cat-1")

	if _, anySuccess := crawler.CrawlAll(t.Context(), []string{indexURL}); !anySuccess {
		t.Fatal("second crawl should succeed")
	}
	after, _ := crawler.cursor.get("cat-1")

	if before.entryVersion != after.entryVersion {
		t.Fatalf("entryVersion changed on a re-crawl with no version bump: %d -> %d", before.entryVersion, after.entryVersion)
	}
	if got := crawler.cursor.snapshot(); len(got) != 1 {
		t.Fatalf("expected exactly one catalog after re-crawl, got %d", len(got))
	}
}

func TestCrawlAll_RetiredCatalogIsRemovedAndNeverResurrected(t *testing.T) {
	file := CatalogFile{CatalogID: "cat-4", Version: 1, Catalog: sampleCatalog("cat-4")}
	rawFile := marshalFile(t, file)
	srv := fileServer(t, map[string][]byte{"/f.json": rawFile})
	baseline := FileRef{Version: file.Version, URL: srv.URL + "/f.json", Size: int64(len(rawFile)), Digest: Digest(rawFile)}

	activeEntry := IndexEntry{CatalogID: "cat-4", EntryVersion: 1, CatalogType: CatalogTypeRegular, IsActive: true, Baseline: baseline}
	rawIndex1 := marshalFile(t, CatalogIndex{NodeID: "pn-4", Catalogs: []IndexEntry{activeEntry}})

	retiredAt := "2026-09-16T00:00:00Z"
	retiredEntry := IndexEntry{CatalogID: "cat-4", EntryVersion: 2, CatalogType: CatalogTypeRegular, IsActive: false, Baseline: baseline, RetiredAt: &retiredAt}
	rawIndex2 := marshalFile(t, CatalogIndex{NodeID: "pn-4", Catalogs: []IndexEntry{retiredEntry}})

	// Serve index1 first; we'll swap the handler after the first crawl.
	currentIndex := rawIndex1
	indexSrv := fileServerDynamic(t, "/idx.json", func() []byte { return currentIndex })
	indexURL := indexSrv.URL + "/idx.json"

	crawler := NewCrawler(srv.Client(), testLogger(), nil, nil, 0.5)

	if catalogs, _ := crawler.CrawlAll(t.Context(), []string{indexURL}); len(catalogs) != 1 {
		t.Fatalf("expected cat-4 active after first crawl, got %+v", catalogs)
	}

	currentIndex = rawIndex2
	if catalogs, _ := crawler.CrawlAll(t.Context(), []string{indexURL}); len(catalogs) != 0 {
		t.Fatalf("expected cat-4 removed after retirement, got %+v", catalogs)
	}

	// Reverting the index back to "active" must NOT resurrect it.
	currentIndex = rawIndex1
	if catalogs, _ := crawler.CrawlAll(t.Context(), []string{indexURL}); len(catalogs) != 0 {
		t.Fatalf("retired catalog was resurrected: %+v", catalogs)
	}
}

func TestCrawlAll_DecodesGzipCompressedFiles(t *testing.T) {
	file := CatalogFile{CatalogID: "cat-7", Version: 1, Catalog: sampleCatalog("cat-7")}
	rawFile := marshalFile(t, file)
	gzFile := gzipBytes(t, rawFile)

	srv := fileServer(t, map[string][]byte{"/catalog.json.gz": gzFile})
	entry := IndexEntry{
		CatalogID: "cat-7", EntryVersion: 1, CatalogType: CatalogTypeRegular, IsActive: true,
		// Digest is over the DEcompressed content, per real observed
		// catalog files (see CONTEXT.md D19) — not the gzip bytes.
		Baseline: FileRef{Version: file.Version, URL: srv.URL + "/catalog.json.gz", Size: int64(len(gzFile)), Digest: Digest(rawFile)},
	}
	rawIndex := marshalFile(t, CatalogIndex{NodeID: "pn-7", Catalogs: []IndexEntry{entry}})
	gzIndex := gzipBytes(t, rawIndex)
	indexSrv := fileServer(t, map[string][]byte{"/index.json.gz": gzIndex})

	crawler := NewCrawler(srv.Client(), testLogger(), nil, nil, 0.5)
	catalogs, anySuccess := crawler.CrawlAll(t.Context(), []string{indexSrv.URL + "/index.json.gz"})
	if !anySuccess || len(catalogs) != 1 || catalogs[0].ID != "cat-7" {
		t.Fatalf("expected one gzip-decoded catalog cat-7, got catalogs=%+v anySuccess=%v", catalogs, anySuccess)
	}
}

// TestApplyChange_IsActiveOnlyDiffPreservesContent reproduces a real bug:
// pausing then resuming a catalog publishes two change files that each
// only declare {"isActive": ...} (descriptor/provider/resources omitted
// since they didn't change). applyChange must merge that partial delta
// onto the prior content, not replace it wholesale -- otherwise the
// catalog survives with isActive:true but empty content forever, and can
// never be found by search again.
func TestApplyChange_IsActiveOnlyDiffPreservesContent(t *testing.T) {
	baseline := CatalogFile{CatalogID: "cat-9", Version: 1, Catalog: sampleCatalog("cat-9")}
	rawBaseline := marshalFile(t, baseline)
	baselineRef := FileRef{Version: 1, URL: "/baseline.json", Size: int64(len(rawBaseline)), Digest: Digest(rawBaseline)}

	falseVal, trueVal := false, true
	pauseChange := CatalogChangeFile{CatalogID: "cat-9", FromVersion: 1, ToVersion: 2, Catalog: &beckn.Catalog{IsActive: &falseVal}}
	rawPause := marshalFile(t, pauseChange)
	resumeChange := CatalogChangeFile{CatalogID: "cat-9", FromVersion: 2, ToVersion: 3, Catalog: &beckn.Catalog{IsActive: &trueVal}}
	rawResume := marshalFile(t, resumeChange)

	srv := fileServer(t, map[string][]byte{
		"/baseline.json": rawBaseline,
		"/pause.json":    rawPause,
		"/resume.json":   rawResume,
	})
	baselineRef.URL = srv.URL + "/baseline.json"
	pauseRef := ChangeRef{FromVersion: 1, ToVersion: 2, URL: srv.URL + "/pause.json", Size: int64(len(rawPause)), Digest: Digest(rawPause)}
	resumeRef := ChangeRef{FromVersion: 2, ToVersion: 3, URL: srv.URL + "/resume.json", Size: int64(len(rawResume)), Digest: Digest(rawResume)}

	index := func(entryVersion int, isActive bool, changes []ChangeRef) []byte {
		entry := IndexEntry{
			CatalogID: "cat-9", EntryVersion: entryVersion, CatalogType: CatalogTypeRegular,
			IsActive: isActive, Baseline: baselineRef, Changes: changes,
		}
		return marshalFile(t, CatalogIndex{NodeID: "pn-9", Catalogs: []IndexEntry{entry}})
	}

	currentIndex := index(1, true, nil)
	indexSrv := fileServerDynamic(t, "/idx.json", func() []byte { return currentIndex })
	indexURL := indexSrv.URL + "/idx.json"

	crawler := NewCrawler(srv.Client(), testLogger(), nil, nil, 0.5)

	if catalogs, _ := crawler.CrawlAll(t.Context(), []string{indexURL}); len(catalogs) != 1 || catalogs[0].Descriptor.Name == "" {
		t.Fatalf("expected cat-9 indexed with full content, got %+v", catalogs)
	}

	// Pause: isActive-only change file (v1 -> v2).
	currentIndex = index(2, false, []ChangeRef{pauseRef})
	if catalogs, _ := crawler.CrawlAll(t.Context(), []string{indexURL}); len(catalogs) != 0 {
		t.Fatalf("expected cat-9 excluded while paused, got %+v", catalogs)
	}
	paused, _ := crawler.cursor.get("cat-9")
	if paused.catalog.Descriptor.Name == "" {
		t.Fatal("pausing wiped the catalog's descriptor -- applyChange should merge, not replace")
	}

	// Resume: another isActive-only change file (v2 -> v3).
	currentIndex = index(3, true, []ChangeRef{pauseRef, resumeRef})
	catalogs, _ := crawler.CrawlAll(t.Context(), []string{indexURL})
	if len(catalogs) != 1 {
		t.Fatalf("expected cat-9 included again after resume, got %+v", catalogs)
	}
	if catalogs[0].Descriptor.Name == "" {
		t.Fatal("resuming left the catalog's descriptor empty -- applyChange wiped content on an earlier isActive-only diff")
	}
	if len(catalogs[0].Resources) != 1 {
		t.Fatalf("expected cat-9's resources to survive the isActive-only diffs, got %+v", catalogs[0].Resources)
	}
}

// TestApplyEntry_SurvivesEntryVersionDriftFromNoOpPublish reproduces a
// real bug: a Provider Node bumps entry.EntryVersion on EVERY publish
// call, including ones where nothing in the catalog's content actually
// changed (e.g. a pure isActive toggle, publish mode "metadata"/
// "unchanged"). baseline.version/each change's fromVersion-toVersion use
// a SEPARATE counter that only advances on a real content change. Once a
// no-op publish ticks entryVersion without a matching file, entryVersion
// no longer lines up with anything in entry.Changes -- resolveCatalog
// must use targetContentVersion (entry.Latest.Version, or the max
// toVersion among changes) instead of entry.EntryVersion to keep chaining
// correctly, or a later real content change becomes permanently
// unreachable (this is exactly what happened to a real catalog: a 2nd
// product published after a pause/resume cycle never became
// searchable).
func TestApplyEntry_SurvivesEntryVersionDriftFromNoOpPublish(t *testing.T) {
	baseline := CatalogFile{CatalogID: "cat-10", Version: 1, Catalog: sampleCatalog("cat-10")}
	rawBaseline := marshalFile(t, baseline)

	newResource := beckn.Resource{ID: "res-2", Descriptor: beckn.Descriptor{Name: "Resource Two"}}
	change := CatalogChangeFile{
		CatalogID: "cat-10", FromVersion: 1, ToVersion: 2,
		Resources: ResourceDelta{Upserts: []beckn.Resource{newResource}},
	}
	rawChange := marshalFile(t, change)

	srv := fileServer(t, map[string][]byte{
		"/baseline.json": rawBaseline,
		"/change.json":   rawChange,
	})
	baselineRef := FileRef{Version: 1, URL: srv.URL + "/baseline.json", Size: int64(len(rawBaseline)), Digest: Digest(rawBaseline)}
	changeRef := ChangeRef{FromVersion: 1, ToVersion: 2, URL: srv.URL + "/change.json", Size: int64(len(rawChange)), Digest: Digest(rawChange)}

	// e1: initial publish. entryVersion and content version agree (both 1).
	e1 := IndexEntry{CatalogID: "cat-10", EntryVersion: 1, CatalogType: CatalogTypeRegular, IsActive: true, Baseline: baselineRef}

	// e2: a no-op publish (e.g. pause+resume) -- entryVersion ticks to 2,
	// but nothing about the content changed: still baseline v1, no
	// changes, no Latest. This is the drift: entryVersion(2) no longer
	// matches the content counter (still 1).
	e2 := IndexEntry{CatalogID: "cat-10", EntryVersion: 2, CatalogType: CatalogTypeRegular, IsActive: true, Baseline: baselineRef}

	// e3: a real content change (e.g. adding a 2nd product) lands on top
	// of the drift -- entryVersion ticks to 3, and the actual new content
	// is at version 2 (both via the change's toVersion and via Latest).
	e3 := IndexEntry{
		CatalogID: "cat-10", EntryVersion: 3, CatalogType: CatalogTypeRegular, IsActive: true,
		Baseline: baselineRef, Changes: []ChangeRef{changeRef},
		Latest: &FileRef{Version: 2, URL: srv.URL + "/baseline.json", Size: int64(len(rawBaseline)), Digest: Digest(rawBaseline)},
	}

	index := func(e IndexEntry) []byte {
		return marshalFile(t, CatalogIndex{NodeID: "pn-10", Catalogs: []IndexEntry{e}})
	}
	currentIndex := index(e1)
	indexSrv := fileServerDynamic(t, "/idx.json", func() []byte { return currentIndex })
	indexURL := indexSrv.URL + "/idx.json"

	crawler := NewCrawler(srv.Client(), testLogger(), nil, nil, 0.99)

	if catalogs, _ := crawler.CrawlAll(t.Context(), []string{indexURL}); len(catalogs) != 1 || len(catalogs[0].Resources) != 1 {
		t.Fatalf("expected cat-10 with 1 resource after initial publish, got %+v", catalogs)
	}

	currentIndex = index(e2)
	if catalogs, _ := crawler.CrawlAll(t.Context(), []string{indexURL}); len(catalogs) != 1 || len(catalogs[0].Resources) != 1 {
		t.Fatalf("expected cat-10 unchanged (still 1 resource) after the no-op publish, got %+v", catalogs)
	}

	currentIndex = index(e3)
	catalogs, _ := crawler.CrawlAll(t.Context(), []string{indexURL})
	if len(catalogs) != 1 {
		t.Fatalf("expected cat-10 still present after the real content change, got %+v", catalogs)
	}
	if len(catalogs[0].Resources) != 2 {
		t.Fatalf("2nd product never reachable after entryVersion drifted from a no-op publish: got %d resource(s), want 2 (%+v)",
			len(catalogs[0].Resources), catalogs[0].Resources)
	}
}

// TestApplyEntry_FirstSightingWalksChainPastBaseline reproduces a second
// gap found alongside the drift bug above: a catalog that's already
// several publishes ahead by the time a crawler EVER sees it for the
// first time (e.g. several rapid publishes before the first crawl tick
// runs) used to stop at the baseline and never attempt to walk forward
// via the change chain, permanently missing anything published after the
// baseline was written.
func TestApplyEntry_FirstSightingWalksChainPastBaseline(t *testing.T) {
	baseline := CatalogFile{CatalogID: "cat-11", Version: 1, Catalog: sampleCatalog("cat-11")}
	rawBaseline := marshalFile(t, baseline)

	newResource := beckn.Resource{ID: "res-2", Descriptor: beckn.Descriptor{Name: "Resource Two"}}
	change := CatalogChangeFile{
		CatalogID: "cat-11", FromVersion: 1, ToVersion: 2,
		Resources: ResourceDelta{Upserts: []beckn.Resource{newResource}},
	}
	rawChange := marshalFile(t, change)

	srv := fileServer(t, map[string][]byte{
		"/baseline.json": rawBaseline,
		"/change.json":   rawChange,
	})
	baselineRef := FileRef{Version: 1, URL: srv.URL + "/baseline.json", Size: int64(len(rawBaseline)), Digest: Digest(rawBaseline)}
	changeRef := ChangeRef{FromVersion: 1, ToVersion: 2, URL: srv.URL + "/change.json", Size: int64(len(rawChange)), Digest: Digest(rawChange)}

	// The very first index this crawler ever sees for cat-11 is already
	// past the baseline -- as if several publishes (a no-op toggle, then
	// a real content change) all landed before any crawl tick ran.
	entry := IndexEntry{
		CatalogID: "cat-11", EntryVersion: 3, CatalogType: CatalogTypeRegular, IsActive: true,
		Baseline: baselineRef, Changes: []ChangeRef{changeRef},
		Latest: &FileRef{Version: 2, URL: srv.URL + "/baseline.json", Size: int64(len(rawBaseline)), Digest: Digest(rawBaseline)},
	}
	rawIndex := marshalFile(t, CatalogIndex{NodeID: "pn-11", Catalogs: []IndexEntry{entry}})
	indexSrv := fileServer(t, map[string][]byte{"/idx.json": rawIndex})

	crawler := NewCrawler(srv.Client(), testLogger(), nil, nil, 0.99)
	catalogs, anySuccess := crawler.CrawlAll(t.Context(), []string{indexSrv.URL + "/idx.json"})
	if !anySuccess || len(catalogs) != 1 {
		t.Fatalf("expected cat-11 indexed on first sighting, got catalogs=%+v anySuccess=%v", catalogs, anySuccess)
	}
	if len(catalogs[0].Resources) != 2 {
		t.Fatalf("first sighting stopped at the baseline instead of walking the chain to the target: got %d resource(s), want 2 (%+v)",
			len(catalogs[0].Resources), catalogs[0].Resources)
	}
}

// TestApplyEntry_WalksChainPastAMidSequenceBaselineRewrite reproduces a
// third gap, found live against a real catalog where the first two fixes
// alone weren't enough: a known catalog whose cursor sits BEHIND the
// current baseline (a baseline rewrite -- a real compaction, or the
// automatic size-ratio one easily triggered by a small catalog's change
// files -- landed since the last crawl) used to fall back to that fresh
// baseline and stop there, never attempting to walk any further changes
// published on top of it in the same index entry. This is exactly what
// happens when a no-op publish (pause/resume) triggers a baseline
// rewrite and a real content change (e.g. a 2nd product) lands right
// after it: the rewrite's version becomes the new baseline, but the 2nd
// product's upsert is a further change ON TOP of that baseline, not part
// of it.
func TestApplyEntry_WalksChainPastAMidSequenceBaselineRewrite(t *testing.T) {
	baselineV1 := CatalogFile{CatalogID: "cat-12", Version: 1, Catalog: sampleCatalog("cat-12")}
	rawBaselineV1 := marshalFile(t, baselineV1)

	// The no-op publish (pause/resume) triggers a fresh baseline rewrite
	// at version 3 -- same content as v1, just re-written as a new
	// baseline (as the automatic compaction / a real PN compaction would
	// do), orphaning the old v1->v2 change below (kept around, per
	// NFH-014 CON-TBD-32, but no longer reachable from the new baseline).
	baselineV3 := CatalogFile{CatalogID: "cat-12", Version: 3, Catalog: sampleCatalog("cat-12")}
	rawBaselineV3 := marshalFile(t, baselineV3)

	orphanedChange := CatalogChangeFile{CatalogID: "cat-12", FromVersion: 1, ToVersion: 2}
	rawOrphanedChange := marshalFile(t, orphanedChange)

	newResource := beckn.Resource{ID: "res-2", Descriptor: beckn.Descriptor{Name: "Resource Two"}}
	realChange := CatalogChangeFile{
		CatalogID: "cat-12", FromVersion: 3, ToVersion: 4,
		Resources: ResourceDelta{Upserts: []beckn.Resource{newResource}},
	}
	rawRealChange := marshalFile(t, realChange)

	srv := fileServer(t, map[string][]byte{
		"/baseline-v1.json": rawBaselineV1,
		"/baseline-v3.json": rawBaselineV3,
		"/orphaned.json":    rawOrphanedChange,
		"/real-change.json": rawRealChange,
	})
	baselineRefV1 := FileRef{Version: 1, URL: srv.URL + "/baseline-v1.json", Size: int64(len(rawBaselineV1)), Digest: Digest(rawBaselineV1)}
	baselineRefV3 := FileRef{Version: 3, URL: srv.URL + "/baseline-v3.json", Size: int64(len(rawBaselineV3)), Digest: Digest(rawBaselineV3)}
	orphanedRef := ChangeRef{FromVersion: 1, ToVersion: 2, URL: srv.URL + "/orphaned.json", Size: int64(len(rawOrphanedChange)), Digest: Digest(rawOrphanedChange)}
	realChangeRef := ChangeRef{FromVersion: 3, ToVersion: 4, URL: srv.URL + "/real-change.json", Size: int64(len(rawRealChange)), Digest: Digest(rawRealChange)}

	e1 := IndexEntry{CatalogID: "cat-12", EntryVersion: 1, CatalogType: CatalogTypeRegular, IsActive: true, Baseline: baselineRefV1}
	// The crawler's cursor will sit at contentVersion=1 after e1. e2
	// jumps straight to the rewritten baseline (v3) plus the real change
	// on top of it (v3->v4) -- the orphaned v1->v2 entry is still listed
	// (grace period) but no longer reachable from the new baseline.
	e2 := IndexEntry{
		CatalogID: "cat-12", EntryVersion: 4, CatalogType: CatalogTypeRegular, IsActive: true,
		Baseline: baselineRefV3, Changes: []ChangeRef{orphanedRef, realChangeRef},
		Latest: &FileRef{Version: 4, URL: srv.URL + "/baseline-v3.json", Size: int64(len(rawBaselineV3)), Digest: Digest(rawBaselineV3)},
	}

	index := func(e IndexEntry) []byte { return marshalFile(t, CatalogIndex{NodeID: "pn-12", Catalogs: []IndexEntry{e}}) }
	currentIndex := index(e1)
	indexSrv := fileServerDynamic(t, "/idx.json", func() []byte { return currentIndex })
	indexURL := indexSrv.URL + "/idx.json"

	crawler := NewCrawler(srv.Client(), testLogger(), nil, nil, 0.99)

	if catalogs, _ := crawler.CrawlAll(t.Context(), []string{indexURL}); len(catalogs) != 1 || len(catalogs[0].Resources) != 1 {
		t.Fatalf("expected cat-12 with 1 resource after initial publish, got %+v", catalogs)
	}

	currentIndex = index(e2)
	catalogs, _ := crawler.CrawlAll(t.Context(), []string{indexURL})
	if len(catalogs) != 1 {
		t.Fatalf("expected cat-12 still present after the baseline rewrite + real change, got %+v", catalogs)
	}
	if len(catalogs[0].Resources) != 2 {
		t.Fatalf("2nd product never reachable past a mid-sequence baseline rewrite: got %d resource(s), want 2 (%+v)",
			len(catalogs[0].Resources), catalogs[0].Resources)
	}
}

// TestApplyEntry_CutoverRuleNeverBlocksChainingPastAFreshBaseline is the
// precise cause of a 4th and final gap found reproducing the reported bug
// live: a small catalog's own real change file routinely exceeds
// cutoverFraction of its (also small) baseline size. The cutover rule
// correctly prefers a fresh baseline over a large accumulated chain when
// extending from an OLD cursor (a real cost tradeoff) -- but once a fresh
// baseline has already been fetched, there is no cheaper alternative left
// to fall back to, so re-applying that same size check there only ever
// strands the cursor at the baseline, permanently hiding whatever change
// exists on top of it.
func TestApplyEntry_CutoverRuleNeverBlocksChainingPastAFreshBaseline(t *testing.T) {
	baseline := CatalogFile{CatalogID: "cat-13", Version: 1, Catalog: sampleCatalog("cat-13")}
	rawBaseline := marshalFile(t, baseline)

	newResource := beckn.Resource{ID: "res-2", Descriptor: beckn.Descriptor{Name: "Resource Two"}}
	change := CatalogChangeFile{
		CatalogID: "cat-13", FromVersion: 1, ToVersion: 2,
		Resources: ResourceDelta{Upserts: []beckn.Resource{newResource}},
	}
	rawChange := marshalFile(t, change)

	srv := fileServer(t, map[string][]byte{
		"/baseline.json": rawBaseline,
		"/change.json":   rawChange,
	})
	// Declared sizes (not the real marshaled lengths, same pattern as
	// TestCrawlAll_CutoverRuleFetchesBaselineWhenChangesTooLarge) chosen so
	// the change unambiguously exceeds cutoverFraction=0.5 of the baseline
	// -- exactly the real-world case of a small catalog's own content
	// change being "large" relative to its small baseline.
	baselineRef := FileRef{Version: 1, URL: srv.URL + "/baseline.json", Size: 10, Digest: Digest(rawBaseline)}
	changeRef := ChangeRef{FromVersion: 1, ToVersion: 2, URL: srv.URL + "/change.json", Size: 100, Digest: Digest(rawChange)}

	// A brand-new catalog (first sighting) already at entryVersion 2,
	// baseline + the one real change on top of it.
	entry := IndexEntry{
		CatalogID: "cat-13", EntryVersion: 2, CatalogType: CatalogTypeRegular, IsActive: true,
		Baseline: baselineRef, Changes: []ChangeRef{changeRef},
	}
	rawIndex := marshalFile(t, CatalogIndex{NodeID: "pn-13", Catalogs: []IndexEntry{entry}})
	indexSrv := fileServer(t, map[string][]byte{"/idx.json": rawIndex})

	// cutoverFraction=0.5, the project's real default -- the 100-byte
	// declared change size is 10x the 10-byte declared baseline size.
	crawler := NewCrawler(srv.Client(), testLogger(), nil, nil, 0.5)
	catalogs, anySuccess := crawler.CrawlAll(t.Context(), []string{indexSrv.URL + "/idx.json"})
	if !anySuccess || len(catalogs) != 1 {
		t.Fatalf("expected cat-13 indexed, got catalogs=%+v anySuccess=%v", catalogs, anySuccess)
	}
	if len(catalogs[0].Resources) != 2 {
		t.Fatalf("cutover rule wrongly blocked chaining past an already-fetched baseline: got %d resource(s), want 2 (%+v)",
			len(catalogs[0].Resources), catalogs[0].Resources)
	}
}

func TestCatalogIndex_ParsesRealObservedShape(t *testing.T) {
	// Exact shape observed on a live, real catalog index (top-level
	// "catalogs" key, numeric entryVersion, object signature) — see
	// CONTEXT.md D19. A regression here means the index silently parses
	// as empty instead of failing loudly.
	raw := []byte(`{"nodeId":"sample-pn.example","next_update":"2026-10-01T08:18:04Z","catalogs":[{"catalogId":"cat-x","entryVersion":1,"catalogType":"REGULAR","isActive":true,"baseline":{"version":1,"url":"https://example/cat-x.json.gz","size":100,"digest":"sha-256:abc"},"signature":{"keyId":"key-1","value":"sig"}}]}`)
	var index CatalogIndex
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(index.Catalogs) != 1 {
		t.Fatalf("expected 1 catalog entry, got %d — top-level key mismatch?", len(index.Catalogs))
	}
	if index.Catalogs[0].EntryVersion != 1 {
		t.Fatalf("entryVersion = %d, want 1", index.Catalogs[0].EntryVersion)
	}
	if index.Catalogs[0].Signature.KeyID != "key-1" || index.Catalogs[0].Signature.Value != "sig" {
		t.Fatalf("signature = %+v, want keyId=key-1 value=sig", index.Catalogs[0].Signature)
	}
}
