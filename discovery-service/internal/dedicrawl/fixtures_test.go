package dedicrawl

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/remiges-tech/logharbour/logharbour"
	"github.com/remiges-tushar/discovery-service/internal/beckn"
)

func testLogger() *logharbour.Logger {
	ctx := logharbour.NewLoggerContext(logharbour.Info)
	return logharbour.NewLoggerWithFallback(ctx, "dedicrawl-test", logharbour.NewFallbackWriter(io.Discard, io.Discard))
}

func sampleCatalog(id string) beckn.Catalog {
	return beckn.Catalog{
		ID:         id,
		Descriptor: beckn.Descriptor{Name: "Test Catalog " + id},
		Provider:   beckn.Provider{ID: "provider-1", Descriptor: beckn.Descriptor{Name: "Provider One"}},
		Resources:  []beckn.Resource{{ID: "res-1", Descriptor: beckn.Descriptor{Name: "Resource One"}}},
		Offers:     []beckn.Offer{{ID: "offer-1", Descriptor: beckn.Descriptor{Name: "Offer One"}, ResourceIDs: []string{"res-1"}}},
	}
}

// marshalFile marshals v (a CatalogFile/CatalogChangeFile/CatalogIndex)
// to raw JSON bytes, as a Provider Node would actually serve it — no
// signing, since Crawler doesn't verify signatures (see CONTEXT.md D19).
func marshalFile(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// gzipBytes compresses raw the same way a real Provider Node's .json.gz
// catalog files are served.
func gzipBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// fileServer serves a fixed set of URL-path -> raw-bytes pairs over HTTP,
// standing in for a Provider Node's self-hosted static catalog files.
func fileServer(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range files {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fileServerDynamic serves path by calling body() fresh on every request,
// letting a test swap the response between crawls (e.g. to simulate an
// index being updated between two Refresh ticks).
func fileServerDynamic(t *testing.T, path string, body func() []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
