package ondiscoversvc

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/discoverwait"
)

// OnDiscoverHandler handles POST /api/webhook/on_discover — the callback
// from the Discovery Service delivering catalog results for a prior async
// /discover request. The BAP immediately returns ACK and asynchronously
// persists the result for observability, delivering it first to any
// request still waiting on it (see service.go, internal/discoverwait).
type OnDiscoverHandler struct {
	svc *OnDiscoverService
	lh  *logharbour.Logger
}

func NewOnDiscoverHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger, registry *discoverwait.Registry) *OnDiscoverHandler {
	return &OnDiscoverHandler{
		svc: NewOnDiscoverService(pool, cfg, lh, registry),
		lh:  lh,
	}
}

// Handle answers POST /api/webhook/on_discover with an immediate ACK and processes the callback asynchronously.
//
//	@Summary		Receive on_discover (catalogs)
//	@ID				webhookOnDiscover
//	@Description	Catalog results for a discover sent by this BAP (only used when DISCOVER_METHOD=POST). The payload is handed to the GET /api/v1/discover request waiting on the same transactionId (if it has not timed out) and written to beckn_message_log as INBOUND; no transaction status change. The adapter has already verified the Beckn signature; no auth.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			request body server.OnDiscoverRequest true "Beckn on_discover request"
//	@Success		200 {object} server.BecknAckResponse "Synchronous ACK; the message is processed asynchronously afterwards. The context is echoed with action set to on_discover."
//	@Failure		400 {object} server.BecknAckResponse "NACK: the body is not valid JSON or does not match the expected types. Context fields are empty strings except action."
//	@Security
//	@Router			/api/webhook/on_discover [post]
func (h *OnDiscoverHandler) Handle(c *gin.Context) {
	var req OnDiscoverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.lh.WithModule("on_discover_handler").Err().Error(err).Log("invalid on_discover request body")
		c.JSON(http.StatusBadRequest, buildNACK(BecknContext{}))
		return
	}

	// Immediately return ACK — Beckn spec requires a synchronous ACK.
	c.JSON(http.StatusOK, BecknACK{
		Context: contextForACK(req.Context),
		Message: ACKMessage{ACK: ACKStatus{Status: "ACK"}},
	})

	// Persist state asynchronously.
	go func() {
		h.svc.Process(context.Background(), &req)
	}()
}

func contextForACK(in BecknContext) BecknContext {
	out := in
	out.Action = "on_discover"
	return out
}

func buildNACK(ctx BecknContext) BecknACK {
	return BecknACK{
		Context: contextForACK(ctx),
		Message: ACKMessage{ACK: ACKStatus{Status: "NACK"}},
	}
}
