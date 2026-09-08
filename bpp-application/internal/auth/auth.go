// Package auth verifies Zitadel-issued seller login tokens and resolves
// them to the provider_id the dashboard API should scope every query to.
//
// Zitadel's landing-page OIDC application is configured (see
// scripts/setup-zitadel.sh) with accessTokenType=JWT, so access tokens are
// verified locally against Zitadel's JWKS (fetched once at startup, cached
// and auto-refreshed by go-oidc) instead of a network round-trip to
// Zitadel's introspection endpoint on every request.
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
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/ion/winroom/bpp/db/sqlc"
)

// Gin context keys set by this package's middleware.
const (
	contextProviderIDKey = "auth.provider_id" // set by RequireSeller
	contextSubjectKey    = "auth.subject"     // set by RequireToken and RequireSeller
	contextRawTokenKey   = "auth.raw_token"   // set by RequireToken and RequireSeller
)

// Verifier validates Zitadel-issued access tokens.
type Verifier struct {
	provider        *oidc.Provider
	idTokenVerifier *oidc.IDTokenVerifier
	// httpClient is nil when issuer == internalURL (plain http.DefaultClient
	// is fine); otherwise it's the same host-dial-override client built in
	// NewVerifier, which FetchEmail must reuse explicitly -- go-oidc's
	// Provider.UserInfo reads its HTTP client from the context *passed to
	// that call*, not from whatever context NewProvider was originally
	// called with.
	httpClient *http.Client
}

// NewVerifier discovers the Zitadel OIDC provider's configuration (issuer,
// JWKS endpoint) via its /.well-known/openid-configuration document. Every
// request is made against issuer (e.g. http://localhost:8090) -- both
// because that's the "iss" claim tokens actually carry (go-oidc itself
// rejects a provider whose discovery doc issuer doesn't match the string
// you asked for) and because Zitadel resolves which instance to serve
// purely from the request's Host header (host-header-based
// multi-tenancy). When issuer isn't reachable from inside this container
// as-is (e.g. it's "localhost:8090", meaning "the browser's localhost",
// not this container's own), internalURL gives the real address to dial;
// a custom Transport connects there at the TCP level while every header
// (Host included) still reflects issuer, so both go-oidc's self-check and
// Zitadel's instance resolution see a consistent, matching issuer. When
// internalURL == issuer (or is empty) this is a plain http.DefaultTransport.
// Call once at startup; the returned Verifier is safe for concurrent use.
func NewVerifier(ctx context.Context, issuer, internalURL string) (*Verifier, error) {
	var httpClient *http.Client
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
		httpClient = &http.Client{
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
		provider:        provider,
		idTokenVerifier: provider.Verifier(&oidc.Config{SkipClientIDCheck: true}),
		httpClient:      httpClient,
	}, nil
}

// FetchEmail calls Zitadel's userinfo endpoint with the given access token.
// The access token's own claims (verified by RequireToken/RequireSeller)
// don't include "email" even though the frontend requests the "email"
// scope -- Zitadel puts profile/email claims in the ID token and userinfo
// response, not the access token. Only call this where email is actually
// needed (new-user bootstrap), not on every authenticated request -- it's
// a real network round-trip to Zitadel each time.
func (v *Verifier) FetchEmail(ctx context.Context, rawToken string) (string, error) {
	if v.httpClient != nil {
		ctx = oidc.ClientContext(ctx, v.httpClient)
	}
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: rawToken})
	info, err := v.provider.UserInfo(ctx, tokenSource)
	if err != nil {
		return "", err
	}
	return info.Email, nil
}

type verifiedClaims struct {
	Subject string `json:"sub"`
}

// verify checks the bearer token's signature, issuer, and expiry, returning
// its "sub" claim -- Zitadel's stable, unique user ID. (Zitadel access
// tokens don't carry "email"/"profile" claims even when those scopes are
// requested -- those land in the ID token and the userinfo endpoint
// instead; see FetchEmail.)
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

// RequireToken is Gin middleware: verifies the Authorization: Bearer token
// and stores its subject/email in the request context, without requiring a
// users row to already exist. Use this (not RequireSeller) for the one
// route a valid-but-not-yet-registered Zitadel identity must be able to
// call: POST /api/v1/auth/bootstrap.
func RequireToken(v *Verifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearerToken(c.GetHeader("Authorization"))
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}

		claims, err := v.verify(c.Request.Context(), raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set(contextSubjectKey, claims.Subject)
		c.Set(contextRawTokenKey, raw)
		c.Next()
	}
}

// RequireSeller is Gin middleware: verifies the Authorization: Bearer token
// and resolves it to a provider_id via the users table (see
// 026_users.sql), storing it in the request context. A syntactically valid
// token with no matching users row (a Zitadel account that never completed
// POST /api/v1/auth/bootstrap) is rejected with 403, not 401 -- the token
// itself is fine, there's just no seller profile behind it yet.
func RequireSeller(v *Verifier, pool *pgxpool.Pool) gin.HandlerFunc {
	q := sqlc.New(pool)
	return func(c *gin.Context) {
		raw := bearerToken(c.GetHeader("Authorization"))
		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}

		claims, err := v.verify(c.Request.Context(), raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		user, err := q.GetUserByZitadelSubject(c.Request.Context(), claims.Subject)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "no seller profile for this account",
				"code":  "NO_PROVIDER",
			})
			return
		}

		c.Set(contextSubjectKey, claims.Subject)
		c.Set(contextRawTokenKey, raw)
		c.Set(contextProviderIDKey, user.ProviderID)
		c.Next()
	}
}

// ProviderIDFromContext returns the authenticated seller's provider_id, set
// by RequireSeller. Only call from a handler mounted behind RequireSeller --
// calling it elsewhere is a routing bug, so it panics rather than returning
// a zero value that would silently scope a query to "".
func ProviderIDFromContext(c *gin.Context) string {
	return c.MustGet(contextProviderIDKey).(string)
}

// SubjectFromContext returns the authenticated Zitadel subject ("sub"
// claim), set by RequireToken or RequireSeller.
func SubjectFromContext(c *gin.Context) string {
	return c.MustGet(contextSubjectKey).(string)
}

// RawTokenFromContext returns the verified bearer token string, set by
// RequireToken or RequireSeller -- needed to call FetchEmail, which has to
// re-present the token to Zitadel's userinfo endpoint.
func RawTokenFromContext(c *gin.Context) string {
	return c.MustGet(contextRawTokenKey).(string)
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimPrefix(header, prefix)
}
