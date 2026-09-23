// Package utility holds small, generic, dependency-free helper functions
// shared across layers (currently just JSON HTTP responses). Keep this
// package narrow — a specific new helper file per concern is preferred
// over letting this become an unstructured grab-bag.
package utility

import (
	"encoding/json"
	"net/http"

	"github.com/remiges-tushar/discovery-service/internal/constants"
)

// WriteJSON writes v as a JSON response body with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", constants.ContentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteJSONBytes writes body — already-marshaled JSON — as the response.
// For callers that need to compute something over the exact response
// bytes (e.g. a signature) before they're written: WriteJSON's own
// internal streaming json.Encoder never exposes those bytes, since it
// encodes directly to w.
func WriteJSONBytes(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", constants.ContentTypeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
