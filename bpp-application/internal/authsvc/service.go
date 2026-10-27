// Package authsvc implements the one HTTP endpoint that turns a Zitadel
// login into a seller account in this app's own schema: Zitadel only knows
// about the identity (email/password, "sub"), it has no notion of
// "provider" -- that's this app's own marketplace concept.
package authsvc

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/auth"
	"github.com/ion/winroom/bpp/internal/config"
)

type Handler struct {
	pool     *pgxpool.Pool
	cfg      *config.Config
	verifier *auth.Verifier
}

func NewHandler(pool *pgxpool.Pool, cfg *config.Config, verifier *auth.Verifier) *Handler {
	return &Handler{pool: pool, cfg: cfg, verifier: verifier}
}

type BootstrapRequest struct {
	// Seller's business name. The provider id is provider-<slug> of this name.
	BusinessName string `json:"businessName" binding:"required" example:"Toko Nusantara"`
}

type BootstrapResponse struct {
	ProviderID string `json:"providerId" binding:"required" example:"provider-toko-nusantara"`
	// True when this call created the provider and user rows.
	IsNew bool `json:"isNew" binding:"required"`
}

// HandleBootstrap is called once per Zitadel identity, right after its
// first successful login. If a users row already exists for this "sub" it
// just returns the existing provider_id (idempotent -- safe to call again
// on every login, not just the first). Otherwise it requires a
// BootstrapRequest body and creates a new providers row + users row in one
// transaction, per the "registration creates a new provider" decision.
//
//	@Summary		Create (or fetch) the seller profile for a Zitadel login
//	@ID			bootstrapSeller
//	@Description	Idempotent. If a users row already exists for the token's sub, returns its providerId with isNew false without reading the body. Otherwise requires businessName, fetches the email from Zitadel's userinfo endpoint, and creates a providers row (id provider-<slug of businessName>; a second seller with the same name collides and gets a 500) and a users row in one transaction.
//	@Description	The /api/v1/auth group applies a CORS middleware allowing only LANDING_PAGE_ORIGIN (methods POST, OPTIONS; headers Authorization, Content-Type); an OPTIONS /api/v1/auth/bootstrap preflight is answered 204 before the token check.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		BootstrapRequest	false	"Required only when no seller profile exists yet for this identity"
//	@Success		200		{object}	BootstrapResponse	"Existing or newly created seller profile"
//	@Failure		400		{object}	auth.ErrorResponse	"New identity and the body is missing, invalid, or has no businessName (gin binding error text)"
//	@Failure		401		{object}	auth.ErrorResponse	"Bearer token missing (missing bearer token), or invalid or expired (invalid or expired token)"
//	@Failure		500		{object}	auth.ErrorResponse	"DB error, userinfo fetch failure (fetch userinfo: ...), or provider/user insert failure (create provider: ..., create user: ...)"
//	@Router			/api/v1/auth/bootstrap [post]
func (h *Handler) HandleBootstrap(c *gin.Context) {
	ctx := c.Request.Context()
	subject := auth.SubjectFromContext(c)
	q := sqlc.New(h.pool)

	if existing, err := q.GetUserByZitadelSubject(ctx, subject); err == nil {
		c.JSON(http.StatusOK, BootstrapResponse{ProviderID: existing.ProviderID, IsNew: false})
		return
	} else if err != pgx.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var req BootstrapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// The access token itself carries no "email" claim (Zitadel puts
	// profile/email in the ID token and userinfo response instead, even
	// when the "email" scope was requested) -- fetch it from userinfo,
	// only needed here on the new-user path, not on every request.
	email, err := h.verifier.FetchEmail(ctx, auth.RawTokenFromContext(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "fetch userinfo: " + err.Error()})
		return
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer tx.Rollback(ctx)
	qtx := q.WithTx(tx)

	providerID := slugify(req.BusinessName)
	if err := qtx.UpsertProvider(ctx, sqlc.UpsertProviderParams{
		ID:                   providerID,
		BppID:                h.cfg.BppID,
		DescriptorName:       req.BusinessName,
		DescriptorDocs:       json.RawMessage(`[]`),
		DescriptorMediaFiles: json.RawMessage(`[]`),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create provider: " + err.Error()})
		return
	}

	if _, err := qtx.CreateUser(ctx, sqlc.CreateUserParams{
		ZitadelSubject: subject,
		Email:          email,
		ProviderID:     providerID,
		BppID:          h.cfg.BppID,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "create user: " + err.Error()})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, BootstrapResponse{ProviderID: providerID, IsNew: true})
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns a business name into a provider ID, e.g. "Toko Nusantara"
// -> "provider-toko-nusantara". Two sellers picking the same name collide:
// UpsertProvider itself would silently take over the existing provider row,
// but the whole bootstrap runs in one transaction and users.provider_id has
// a UNIQUE(provider_id, bpp_id) constraint, so the second seller's
// CreateUser fails and the transaction rolls back the provider overwrite
// too -- the conflict surfaces as a 500, not a silent takeover, but it's a
// blunt rejection rather than a clean "name taken" error. Acceptable for a
// reference app; a production system would check availability first or
// append a random suffix.
func slugify(name string) string {
	s := nonAlnum.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "seller"
	}
	return "provider-" + s
}
