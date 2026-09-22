// Package handlers is the HTTP transport layer: decode/encode JSON,
// enforce request-size limits, translate service-layer results into HTTP
// status codes. No business logic lives here — see internal/service.
package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/remiges-tech/logharbour/logharbour"
	"github.com/remiges-tushar/discovery-service/internal/beckn"
	"github.com/remiges-tushar/discovery-service/internal/constants"
	"github.com/remiges-tushar/discovery-service/internal/service"
	"github.com/remiges-tushar/discovery-service/internal/utility"
)

// AckSigner signs the body of the request just received, producing the
// beckn.Ack's required CounterSignature (see beckn.yaml's Signature
// schema — "proves we authenticated and processed the request"). Same
// shape as dispatch.RequestSigner; satisfied by *signing.Signer, the same
// instance main.go already builds for outbound on_discover delivery —
// one identity, one key, signing both directions.
type AckSigner interface {
	Sign(body []byte) (beckn.Signature, error)
}

// DiscoverHandler implements both the async POST /discover path
// (ServeHTTP) and the synchronous GET /discover path (ServeSync, see
// CONTEXT.md D16).
type DiscoverHandler struct {
	svc          *service.DiscoverService
	maxBodyBytes int64
	logger       *logharbour.Logger
	signer       AckSigner
}

// NewDiscoverHandler builds a DiscoverHandler. maxBodyBytes caps how much
// of a /discover request body will be read (see config.Config.MaxRequestBodyBytes).
// Without a cap, decoding an attacker- or bug-supplied unbounded body
// reads it entirely into memory before json.Decode ever gets a chance to
// reject it — a single request could exhaust process memory. signer may
// be nil (no persistent signing key configured, see main.go's
// buildSigner) — the Ack then falls back to service.PlaceholderSignature,
// clearly marked as not a real signature rather than silently wrong.
func NewDiscoverHandler(svc *service.DiscoverService, maxBodyBytes int64, logger *logharbour.Logger, signer AckSigner) *DiscoverHandler {
	return &DiscoverHandler{svc: svc, maxBodyBytes: maxBodyBytes, logger: logger, signer: signer}
}

// ServeHTTP implements the async POST /discover path: Ack immediately,
// deliver on_discover to context.bapUri in the background.
func (h *DiscoverHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, body, ok := h.decodeAndValidate(w, r, h.svc.Validate)
	if !ok {
		return
	}

	h.logger.LogActivity("discover request accepted", map[string]any{
		"transactionId": req.Context.TransactionID,
		"messageId":     req.Context.MessageID,
		"bapId":         req.Context.BapID,
		"bapUri":        req.Context.BapURI,
	})

	h.writeAck(w, body)
	h.svc.ProcessAsync(req)
}

// ServeSync implements the synchronous GET /discover path (see
// CONTEXT.md D16): the full on_discover result is returned inline in the
// response body instead of via an async callback. Mirrors beckn-discovr's
// GET /discover, which a real ION/BAP caller requires.
func (h *DiscoverHandler) ServeSync(w http.ResponseWriter, r *http.Request) {
	req, _, ok := h.decodeAndValidate(w, r, h.svc.ValidateSync)
	if !ok {
		return
	}

	h.logger.LogActivity("discover request accepted (sync)", map[string]any{
		"transactionId": req.Context.TransactionID,
		"messageId":     req.Context.MessageID,
		"bapId":         req.Context.BapID,
	})

	utility.WriteJSON(w, http.StatusOK, h.svc.BuildSync(req))
}

// decodeAndValidate reads the request body (size-bounded per
// h.maxBodyBytes), decodes and validates it, writing a NACK and returning
// ok=false on any failure. Shared by both the async and synchronous
// /discover paths, which differ only in which fields they require (see
// service.Validate vs. service.ValidateSync). Returns the raw body bytes
// alongside the parsed request so callers can sign exactly what was
// received (see writeAck) — reading into a byte slice first, instead of
// streaming straight into json.Decoder, is what makes that possible.
func (h *DiscoverHandler) decodeAndValidate(w http.ResponseWriter, r *http.Request, validate func(beckn.DiscoverRequest) (errCode, errMsg string)) (beckn.DiscoverRequest, []byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn().LogActivity("discover request rejected: failed to read body", map[string]any{"error": err.Error()})
		h.writeNack(w, http.StatusBadRequest, nil, constants.ErrInvalidJSON, "request body could not be read (or exceeds the size limit)")
		return beckn.DiscoverRequest{}, nil, false
	}

	var req beckn.DiscoverRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.logger.Warn().LogActivity("discover request rejected: invalid JSON body", nil)
		h.writeNack(w, http.StatusBadRequest, body, constants.ErrInvalidJSON, "request body is not valid JSON (or exceeds the size limit)")
		return beckn.DiscoverRequest{}, nil, false
	}

	// Debug0-level detail: the full parsed request, visible only when
	// LOG_LEVEL selects a debug tier (see internal/logging.IsDebugLevel).
	h.logger.Debug0().LogDebug("discover request parsed", req)

	if errCode, errMsg := validate(req); errCode != "" {
		h.logger.Warn().LogActivity("discover request rejected: validation failed", map[string]any{
			"transactionId": req.Context.TransactionID,
			"messageId":     req.Context.MessageID,
			"errorCode":     errCode,
		})
		h.writeNack(w, http.StatusBadRequest, body, errCode, errMsg)
		return beckn.DiscoverRequest{}, nil, false
	}

	return req, body, true
}

func (h *DiscoverHandler) writeAck(w http.ResponseWriter, body []byte) {
	utility.WriteJSON(w, http.StatusOK, beckn.Ack{
		Status:    constants.StatusACK,
		Signature: signOrPlaceholder(h.signer, body, h.logger),
	})
}

// writeNack signs an empty body when body is nil (e.g. the request
// couldn't even be read) — the CounterSignature still needs to be
// structurally present and self-consistent, there's just nothing else to
// sign over.
func (h *DiscoverHandler) writeNack(w http.ResponseWriter, status int, body []byte, errCode, errMsg string) {
	writeSignedNack(w, h.signer, body, h.logger, status, errCode, errMsg)
}

// signOrPlaceholder produces the Ack's CounterSignature over body (the
// exact bytes received — "proves we authenticated and processed the
// request", per beckn.yaml's Signature schema). Falls back to
// service.PlaceholderSignature, clearly marked as not a real signature,
// when signer is nil (no persistent signing key configured) or signing
// itself fails — a missing/invalid signature must never stop an Ack/NACK
// from being sent. Shared by DiscoverHandler and VerifyMiddleware (see
// auth.go), the two places that write a beckn.Ack.
func signOrPlaceholder(signer AckSigner, body []byte, logger *logharbour.Logger) beckn.Signature {
	if signer == nil {
		return service.PlaceholderSignature()
	}
	sig, err := signer.Sign(body)
	if err != nil {
		logger.Err().LogActivity("failed to sign Ack CounterSignature, falling back to placeholder", map[string]any{"error": err.Error()})
		return service.PlaceholderSignature()
	}
	return sig
}

// writeSignedNack writes a NACK Ack, signed the same way writeAck signs a
// success — see signOrPlaceholder.
func writeSignedNack(w http.ResponseWriter, signer AckSigner, body []byte, logger *logharbour.Logger, status int, errCode, errMsg string) {
	utility.WriteJSON(w, status, beckn.Ack{
		Status:    constants.StatusNACK,
		Signature: signOrPlaceholder(signer, body, logger),
		Error:     &beckn.Error{ErrorCode: errCode, ErrorMessage: errMsg},
	})
}
