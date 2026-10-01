// Package auth verifies Zitadel-issued login tokens for buyers, when
// present. Unlike bpp-application's auth package, this is entirely
// non-blocking: bap-frontend's login gate is a frontend-only concern (see
// bap-frontend/src/auth.js), so this package never rejects a request for
// having no token -- it only resolves one to a buyer identity when it can,
// so handlers can scope data (e.g. order history) to that buyer.
//
// Reuses the same Zitadel OIDC application that already gates the seller
// flow (see bpp-application/internal/auth/auth.go) -- SkipClientIDCheck
// means any valid token from this Zitadel instance verifies the same way
// regardless of which app requested it, so no separate buyer app/role is
// needed in Zitadel.
package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"github.com/remiges-tech/logharbour/logharbour"
)

// Gin context key set by OptionalAuth when a valid token is present.
const contextSubjectKey = "auth.subject"

// Verifier validates Zitadel-issued access tokens.
type Verifier struct {
	idTokenVerifier *oidc.IDTokenVerifier
}

// NewVerifier discovers the Zitadel OIDC provider's configuration (issuer,
// JWKS endpoint) via its /.well-known/openid-configuration document. See
// bpp-application/internal/auth/auth.go's NewVerifier for the full
// rationale behind the internalURL host-remap -- identical mechanics here.
func NewVerifier(ctx context.Context, issuer, internalURL string) (*Verifier, error) {
	if internalURL != "" && internalURL != issuer {
		issuerURL, err := url.Parse(issuer)
		if err != nil {
			return nil, err
		}
		internalU, err := url.Parse(internalURL)
		if err != nil {
			return nil, err
		}
		from, to := issuerURL.Host, internalU.Host
		httpClient := &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					if addr == from {
						addr = to
					}
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				},
			},
		}
		ctx = oidc.ClientContext(ctx, httpClient)
	}

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	// SkipClientIDCheck: this verifies a resource-server-facing access
	// token, not an ID token scoped to one specific client's audience.
	return &Verifier{
		idTokenVerifier: provider.Verifier(&oidc.Config{SkipClientIDCheck: true}),
	}, nil
}

type verifiedClaims struct {
	Subject string `json:"sub"`
}

// verify checks the bearer token's signature, issuer, and expiry, returning
// its "sub" claim -- Zitadel's stable, unique user ID.
func (v *Verifier) verify(ctx context.Context, rawToken string) (verifiedClaims, error) {
	token, err := v.idTokenVerifier.Verify(ctx, rawToken)
	if err != nil {
		return verifiedClaims{}, err
	}
	var claims verifiedClaims
	if err := token.Claims(&claims); err != nil {
		return verifiedClaims{}, err
	}
	if claims.Subject == "" {
		return verifiedClaims{}, errors.New("token has no subject claim")
	}
	return claims, nil
}

// Gin context key set by OptionalAuth when a token was presented but could
// not be verified. Handlers use TokenRejected to tell that apart from "no
// token was sent at all" -- see SubjectFromContext's callers.
const contextTokenRejectedKey = "auth.tokenRejected"

// OptionalAuth is Gin middleware: if a valid bearer token is present, it
// stores the buyer's Zitadel subject in the request context. A request with
// no token proceeds with no subject set. A token that IS sent but fails
// verification (expired, or signed by a Zitadel instance that has since been
// reset) is rejected with 401, so the buyer app sends the shopper back to
// log in -- otherwise the order would silently be placed without its buyer
// and never show in their order history. v may be nil (Zitadel was
// unreachable at startup); then this is a no-op, since bap-application should
// keep serving traffic even if Zitadel is down.
//
// A rejected token is also recorded in the context and logged at Warn, so
// the cause of the 401 is visible in the logs. lh may be nil, in which case
// nothing is logged.
func OptionalAuth(v *Verifier, lh *logharbour.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if v == nil {
			c.Next()
			return
		}
		raw := bearerToken(c.GetHeader("Authorization"))
		if raw == "" {
			c.Next()
			return
		}
		claims, err := v.verify(c.Request.Context(), raw)
		if err != nil {
			c.Set(contextTokenRejectedKey, true)
			if lh != nil {
				lh.WithModule("auth").Warn().Error(err).Log("bearer token present but failed verification; rejecting with 401")
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired login; please log in again"})
			return
		}
		c.Set(contextTokenRejectedKey, false)
		c.Set(contextSubjectKey, claims.Subject)
		c.Next()
	}
}

// TokenRejected reports whether this request carried a bearer token that
// failed verification (as opposed to carrying none at all). Handlers use
// it to answer 401 -- which lets the frontend clear the dead token and
// send the buyer back through login -- instead of a 404 that looks like
// the order doesn't exist and leaves them stuck.
func TokenRejected(c *gin.Context) bool {
	rejected, ok := c.Get(contextTokenRejectedKey)
	if !ok {
		return false
	}
	b, _ := rejected.(bool)
	return b
}

// SubjectFromContext returns the authenticated buyer's Zitadel subject, if
// OptionalAuth resolved one for this request. The second return value is
// false when no valid token was presented -- callers must handle that case
// explicitly (e.g. by scoping a query to "nothing") rather than assuming a
// subject is always present, since this package never blocks a request for
// lacking one.
func SubjectFromContext(c *gin.Context) (string, bool) {
	v, ok := c.Get(contextSubjectKey)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimPrefix(header, prefix)
}
