package handlers

import (
	"bytes"
	"io"
	"net/http"

	"github.com/remiges-tech/logharbour/logharbour"
	"github.com/remiges-tushar/discovery-service/internal/constants"
)

// SignatureVerifier checks an inbound Authorization header against body.
// Satisfied by *signing.Verifier; a narrow interface so this package
// doesn't need to import internal/signing directly.
type SignatureVerifier interface {
	Verify(authHeader string, body []byte) error
}

// VerifyMiddleware rejects any request without a valid Authorization
// signature from the trusted caller before it reaches the next handler.
// verifier may be nil (no CALLER_BAP_* configured — see config.Config),
// in which case every request passes through unchecked, matching this
// service's existing fail-open-with-a-warning posture for unconfigured
// auth (see cmd/discovery/main.go's buildSigner). signer signs the
// rejection NACK's own CounterSignature (see signOrPlaceholder in
// discover.go) — it may independently be nil (no persistent signing key
// configured), in which case the NACK falls back to a clearly-marked
// placeholder signature.
//
// It reads the full body up front (bounded by maxBodyBytes) to compute the
// digest the signature covers, then restores r.Body so the wrapped
// handler's own JSON decoding sees the same bytes unchanged.
func VerifyMiddleware(verifier SignatureVerifier, signer AckSigner, maxBodyBytes int64, logger *logharbour.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if verifier == nil {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				logger.Warn().LogActivity("discover request rejected: missing Authorization header", map[string]any{"path": r.URL.Path})
				writeSignedNack(w, signer, nil, logger, http.StatusUnauthorized, constants.ErrSignatureMissing, "missing Authorization header")
				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeSignedNack(w, signer, nil, logger, http.StatusBadRequest, constants.ErrInvalidJSON, "request body could not be read (or exceeds the size limit)")
				return
			}

			if err := verifier.Verify(authHeader, body); err != nil {
				logger.Warn().LogActivity("discover request rejected: signature verification failed", map[string]any{"path": r.URL.Path, "error": err.Error()})
				writeSignedNack(w, signer, body, logger, http.StatusUnauthorized, constants.ErrSignatureInvalid, "signature verification failed")
				return
			}

			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}
}
