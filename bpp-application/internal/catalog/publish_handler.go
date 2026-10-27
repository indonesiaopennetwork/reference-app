package catalog

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/alya/service"
	"github.com/remiges-tech/alya/wscutils"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bpp/internal/config"
)

// Message IDs for catalog publish errors (used by wscutils response envelope).
const (
	msgIDInvalidRequest = 1001
	msgIDPublishFailed  = 1002
)

// PublishHandler handles POST /v1/catalog/publish — the provider-facing catalog publish endpoint.
type PublishHandler struct {
	publishSvc *PublishService
	lh         *logharbour.Logger
}

func NewPublishHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *PublishHandler {
	return &PublishHandler{
		publishSvc: NewPublishService(pool, cfg, lh),
		lh:         lh,
	}
}

// Handle is the Alya-compatible handler for the catalog publish route.
//
// Flow:
//  1. Parse and validate the provider's catalog payload.
//  2. Save catalog data to the database (transaction).
//  3. Forward a Beckn-compatible request to the CDS.
//  4. Return ACK to the provider.
//
// API docs (swag):
//
//	@Summary		Provider catalog publish (no auth)
//	@ID			providerPublishCatalog
//	@Description	Provider-facing publish API with no auth middleware. For each catalog it upserts the provider (replacing its locations) and the catalog, and replaces the catalog's resources and offers in one DB transaction; offers without terms get this BPP's default terms.
//	@Description	It then POSTs one catalog/publish request (onix-catalog-publish shape, catalog ids namespaced as <bppId>/<catalogId>) to CDS_PUBLISH_URL, signed when a BPP private key is set; when CDS_PUBLISH_URL is empty the forward is skipped and the call still ACKs. Inbound and outbound messages are logged to beckn_message_log.
//	@Tags			catalogs
//	@Accept			json
//	@Produce		json
//	@Param			body	body		ProviderPublishRequest		true	"Catalogs to save and publish"
//	@Success		200		{object}	PublishAckResponse			"Saved, and forwarded when configured"
//	@Failure		400		{object}	server.WscErrorResponse	"Body is not valid JSON, or catalogs is missing or empty (msgid 1001, errcode missing)"
//	@Failure		500		{object}	server.WscErrorResponse	"DB save failed, or the CDS forward failed (transport error or non-2xx reply): msgid 1002, errcode database_error"
//	@Security
//	@Router			/api/v1/catalog/publish [post]
func (h *PublishHandler) Handle(c *gin.Context, _ *service.Service) {
	var req ProviderPublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.lh.WithModule("catalog.handler").Warn().Error(err).Log("invalid publish request")
		c.JSON(http.StatusBadRequest, wscutils.NewErrorResponse(msgIDInvalidRequest, wscutils.ErrcodeMissing))
		return
	}

	ack, err := h.publishSvc.Publish(c.Request.Context(), &req)
	if err != nil {
		h.lh.WithModule("catalog.handler").Err().Error(err).Log("catalog publish failed")
		c.JSON(http.StatusInternalServerError, wscutils.NewErrorResponse(msgIDPublishFailed, wscutils.ErrcodeDatabaseError))
		return
	}

	c.JSON(http.StatusOK, wscutils.NewSuccessResponse(ack))
}
