package dedicrawl

import (
	"testing"

	"github.com/remiges-tushar/discovery-service/internal/beckn"
)

func TestCrawlAll_AppliesChangeFileChain(t *testing.T) {
	baselineCatalog := sampleCatalog("cat-5")
	file := CatalogFile{CatalogID: "cat-5", Version: 1, Catalog: baselineCatalog}
	rawFile := marshalFile(t, file)

	newResource := beckn.Resource{ID: "res-2", Descriptor: beckn.Descriptor{Name: "Resource Two"}}
	change := CatalogChangeFile{
		CatalogID:   "cat-5",
		FromVersion: 1,
		ToVersion:   2,
		Resources:   ResourceDelta{Upserts: []beckn.Resource{newResource}, Removals: []string{"res-1"}},
	}
	rawChange := marshalFile(t, change)

	srv := fileServer(t, map[string][]byte{
		"/baseline.json": rawFile,
		"/change.json":   rawChange,
	})

	baseline := FileRef{Version: file.Version, URL: srv.URL + "/baseline.json", Size: int64(len(rawFile)), Digest: Digest(rawFile)}
	changeRef := ChangeRef{FromVersion: change.FromVersion, ToVersion: change.ToVersion, URL: srv.URL + "/change.json", Size: int64(len(rawChange)), Digest: Digest(rawChange)}

	entryV1 := IndexEntry{CatalogID: "cat-5", EntryVersion: 1, CatalogType: CatalogTypeRegular, IsActive: true, Baseline: baseline}
	entryV2 := IndexEntry{
		CatalogID: "cat-5", EntryVersion: 2, CatalogType: CatalogTypeRegular, IsActive: true,
		Baseline: baseline, Changes: []ChangeRef{changeRef},
	}

	rawIndexV1 := marshalFile(t, CatalogIndex{NodeID: "pn-5", Catalogs: []IndexEntry{entryV1}})
	rawIndexV2 := marshalFile(t, CatalogIndex{NodeID: "pn-5", Catalogs: []IndexEntry{entryV2}})
	currentIndex := rawIndexV1
	indexSrv := fileServerDynamic(t, "/idx.json", func() []byte { return currentIndex })
	indexURL := indexSrv.URL + "/idx.json"

	// High cutover fraction so a small change file is never treated as
	// bigger than the baseline, keeping this test focused on chain
	// application rather than the cutover rule (covered separately).
	crawler := NewCrawler(srv.Client(), testLogger(), nil, nil, 0.99)

	if _, anySuccess := crawler.CrawlAll(t.Context(), []string{indexURL}); !anySuccess {
		t.Fatal("baseline crawl should succeed")
	}

	currentIndex = rawIndexV2
	catalogs, anySuccess := crawler.CrawlAll(t.Context(), []string{indexURL})
	if !anySuccess {
		t.Fatal("change-file crawl should succeed")
	}

	if len(catalogs) != 1 {
		t.Fatalf("expected exactly one catalog, got %+v", catalogs)
	}
	cat := catalogs[0]
	if len(cat.Resources) != 1 || cat.Resources[0].ID != "res-2" {
		t.Fatalf("expected res-1 removed and res-2 upserted, got resources %+v", cat.Resources)
	}
}

func TestCrawlAll_CutoverRuleFetchesBaselineWhenChangesTooLarge(t *testing.T) {
	file := CatalogFile{CatalogID: "cat-6", Version: 1, Catalog: sampleCatalog("cat-6")}
	rawFile := marshalFile(t, file)
	// An oversized change file's declared Size will exceed the cutover
	// threshold even though its real byte length is small — Size is a
	// declared index value, not independently re-measured by the crawler.
	rawChange := marshalFile(t, CatalogChangeFile{CatalogID: "cat-6", FromVersion: 1, ToVersion: 2})

	srv := fileServer(t, map[string][]byte{"/baseline.json": rawFile, "/change.json": rawChange})
	baseline := FileRef{Version: file.Version, URL: srv.URL + "/baseline.json", Size: 10, Digest: Digest(rawFile)}
	changeRef := ChangeRef{FromVersion: 1, ToVersion: 2, URL: srv.URL + "/change.json", Size: 100, Digest: Digest(rawChange)}

	entryV1 := IndexEntry{CatalogID: "cat-6", EntryVersion: 1, CatalogType: CatalogTypeRegular, IsActive: true, Baseline: baseline}
	entryV2 := IndexEntry{CatalogID: "cat-6", EntryVersion: 2, CatalogType: CatalogTypeRegular, IsActive: true, Baseline: baseline, Changes: []ChangeRef{changeRef}}

	rawIndexV1 := marshalFile(t, CatalogIndex{NodeID: "pn-6", Catalogs: []IndexEntry{entryV1}})
	rawIndexV2 := marshalFile(t, CatalogIndex{NodeID: "pn-6", Catalogs: []IndexEntry{entryV2}})
	currentIndex := rawIndexV1
	indexSrv := fileServerDynamic(t, "/idx.json", func() []byte { return currentIndex })
	indexURL := indexSrv.URL + "/idx.json"

	// cutoverFraction=0.5: change Size (100) far exceeds 0.5*baseline Size (5) -> must fetch baseline.
	crawler := NewCrawler(srv.Client(), testLogger(), nil, nil, 0.5)

	if _, anySuccess := crawler.CrawlAll(t.Context(), []string{indexURL}); !anySuccess {
		t.Fatal("baseline crawl should succeed")
	}
	currentIndex = rawIndexV2
	catalogs, anySuccess := crawler.CrawlAll(t.Context(), []string{indexURL})
	if !anySuccess {
		t.Fatal("cutover crawl should succeed")
	}

	if len(catalogs) != 1 || catalogs[0].ID != "cat-6" {
		t.Fatalf("expected cat-6 present via baseline fallback, got %+v", catalogs)
	}
	// The baseline (not the empty-delta change file) must have been used,
	// so the original resource is still present.
	if len(catalogs[0].Resources) != 1 || catalogs[0].Resources[0].ID != "res-1" {
		t.Fatalf("expected baseline content (res-1 present), got resources %+v", catalogs[0].Resources)
	}
}

func TestCrawlIndex_OrdersMasterEntriesBeforeRegular(t *testing.T) {
	var order []string
	entries := []IndexEntry{
		{CatalogID: "regular-1", CatalogType: CatalogTypeRegular},
		{CatalogID: "master-1", CatalogType: CatalogTypeMaster},
		{CatalogID: "regular-2", CatalogType: CatalogTypeRegular},
	}
	for _, e := range orderMastersFirst(entries) {
		order = append(order, e.CatalogID)
	}
	if len(order) != 3 || order[0] != "master-1" {
		t.Fatalf("expected master-1 first, got order %v", order)
	}
}

func TestInScope_FiltersByNetworkAndSchemaType(t *testing.T) {
	c := &Crawler{networkIDs: []string{"net-a"}, schemaTypes: []string{"retail"}}

	inScope := IndexEntry{NetworkIDs: []string{"net-a", "net-b"}, SchemaTypes: []string{"retail"}}
	if !c.inScope(inScope) {
		t.Error("expected entry matching both scope filters to be in scope")
	}

	wrongNetwork := IndexEntry{NetworkIDs: []string{"net-z"}, SchemaTypes: []string{"retail"}}
	if c.inScope(wrongNetwork) {
		t.Error("expected entry with no matching networkId to be out of scope")
	}

	wrongSchema := IndexEntry{NetworkIDs: []string{"net-a"}, SchemaTypes: []string{"logistics"}}
	if c.inScope(wrongSchema) {
		t.Error("expected entry with no matching schemaType to be out of scope")
	}
}
