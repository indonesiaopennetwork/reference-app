// Package handlers is the HTTP transport layer: decode/encode JSON,
// enforce request-size limits, translate service-layer results into HTTP
// status codes. No business logic lives here — see internal/service.
package handlers

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"

	"github.com/remiges-tech/logharbour/logharbour"
	"github.com/remiges-tushar/discovery-service/internal/beckn"
	"github.com/remiges-tushar/discovery-service/internal/constants"
	"github.com/remiges-tushar/discovery-service/internal/service"
	"github.com/remiges-tushar/discovery-service/internal/utility"
)

// AckSigner produces both signatures a synchronous Ack/NACK carries:
//   - Sign: beckn.yaml's own body-embedded Signature field, a
//     CounterSignature over the REQUEST body just received ("proves we
//     authenticated and processed the request").
//   - SignAckHeader: NFH-007's (Authentication and Trust) `Signature`
//     RESPONSE HEADER — a distinct format, over the RESPONSE body,
//     chained to the specific inbound request via its own signature
//     value. See signing.Signer.SignAckHeader's doc for exactly how (and
//     why) this differs from Sign; both are emitted (see writeSignedAck)
//     since NFH-014 confirms /discover/on_discover are otherwise
//     unchanged from plain beckn.yaml.
//
// Same shape as dispatch.RequestSigner (for Sign); satisfied by
// *signing.Signer, the same instance main.go already builds for outbound
// on_discover delivery — one identity, one key, signing every direction.
type AckSigner interface {
	Sign(body []byte) (beckn.Signature, error)
	SignAckHeader(responseBody []byte, requestSignatureBase64 string) (string, error)
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

	h.writeAck(w, r, body)
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
		h.writeNack(w, r, http.StatusBadRequest, nil, constants.ErrInvalidJSON, "request body could not be read (or exceeds the size limit)")
		return beckn.DiscoverRequest{}, nil, false
	}

	var req beckn.DiscoverRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.logger.Warn().LogActivity("discover request rejected: invalid JSON body", nil)
		h.writeNack(w, r, http.StatusBadRequest, body, constants.ErrInvalidJSON, "request body is not valid JSON (or exceeds the size limit)")
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
		h.writeNack(w, r, http.StatusBadRequest, body, errCode, errMsg)
		return beckn.DiscoverRequest{}, nil, false
	}

	return req, body, true
}

func (h *DiscoverHandler) writeAck(w http.ResponseWriter, r *http.Request, reqBody []byte) {
	writeSignedAck(w, r, h.signer, h.logger, http.StatusOK, beckn.Ack{
		Status:    constants.StatusACK,
		Signature: signOrPlaceholder(h.signer, reqBody, h.logger),
	})
}

// writeNack signs an empty reqBody when it's nil (e.g. the request
// couldn't even be read) — the CounterSignature still needs to be
// structurally present and self-consistent, there's just nothing else to
// sign over.
func (h *DiscoverHandler) writeNack(w http.ResponseWriter, r *http.Request, status int, reqBody []byte, errCode, errMsg string) {
	writeSignedNack(w, r, h.signer, h.logger, status, reqBody, errCode, errMsg)
}

// signOrPlaceholder produces the Ack's body-embedded CounterSignature
// (beckn.yaml's Signature field) over reqBody — the exact request bytes
// received, "proves we authenticated and processed the request". Falls
// back to service.PlaceholderSignature, clearly marked as not a real
// signature, when signer is nil (no persistent signing key configured)
// or signing itself fails — a missing/invalid signature must never stop
// an Ack/NACK from being sent. Shared by DiscoverHandler and
// VerifyMiddleware (see auth.go), the two places that write a beckn.Ack.
func signOrPlaceholder(signer AckSigner, reqBody []byte, logger *logharbour.Logger) beckn.Signature {
	if signer == nil {
		return service.PlaceholderSignature()
	}
	sig, err := signer.Sign(reqBody)
	if err != nil {
		logger.Err().LogActivity("failed to sign Ack CounterSignature, falling back to placeholder", map[string]any{"error": err.Error()})
		return service.PlaceholderSignature()
	}
	return sig
}

// writeSignedNack writes a NACK, signed the same way writeSignedAck signs
// a success (see its doc for the two-signature design — body field +
// NFH-007 response header).
func writeSignedNack(w http.ResponseWriter, r *http.Request, signer AckSigner, logger *logharbour.Logger, status int, reqBody []byte, errCode, errMsg string) {
	writeSignedAck(w, r, signer, logger, status, beckn.Ack{
		Status:    constants.StatusNACK,
		Signature: signOrPlaceholder(signer, reqBody, logger),
		Error:     &beckn.Error{ErrorCode: errCode, ErrorMessage: errMsg},
	})
}

// writeSignedAck marshals ack, sets NFH-007's Signature response header
// over those exact bytes (chained to r's own inbound signature, if any —
// see requestSignatureValue), and writes it. Emits both signatures a
// synchronous response can carry: ack.Signature is beckn.yaml's
// body-embedded CounterSignature (already computed by the caller, over
// the REQUEST body — see signOrPlaceholder); the Signature header this
// function adds is NFH-007's, over the RESPONSE body being written here.
// Header signing is skipped (not just placeholder'd) when signer is nil
// — NFH-007 compliance is meaningless without a real key, and silently
// emitting a garbage header would be worse than omitting it.
func writeSignedAck(w http.ResponseWriter, r *http.Request, signer AckSigner, logger *logharbour.Logger, status int, ack beckn.Ack) {
	body, err := json.Marshal(ack)
	if err != nil {
		// beckn.Ack always marshals; this would only fire on an
		// unsupported field type, i.e. a programming error.
		logger.Crit().LogActivity("failed to marshal Ack, this is a bug", map[string]any{"error": err.Error()})
		utility.WriteJSON(w, http.StatusInternalServerError, beckn.Ack{Status: constants.StatusNACK})
		return
	}

	if signer != nil {
		headerSig, err := signer.SignAckHeader(body, requestSignatureValue(r))
		if err != nil {
			logger.Err().LogActivity("failed to sign NFH-007 Signature response header, omitting it", map[string]any{"error": err.Error()})
		} else {
			w.Header().Set("Signature", headerSig)
		}
	}

	utility.WriteJSONBytes(w, status, body)
}

// requestSignatureValue extracts the raw base64 Ed25519 signature value
// from r's Authorization header, per NFH-007 §3.4: "the literal string
// value of the signature attribute only, without the surrounding quotes
// or any other Authorization header components". Returns "" if there's
// no Authorization header, or it doesn't parse — the response is still
// signed either way (see writeSignedAck), just with nothing to chain to.
func requestSignatureValue(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}
	parsed, err := beckn.ParseSignature(authHeader)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(parsed.Signature)
}
