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
