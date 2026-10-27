package onsupportsvc

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bap/internal/config"
)

type Handler struct {
	svc *Service
	lh  *logharbour.Logger
}

func NewHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Handler {
	return &Handler{svc: NewService(pool, cfg, lh), lh: lh}
}

// Handle answers POST /api/webhook/on_support with an immediate ACK and processes the callback asynchronously.
//
//	@Summary		Receive on_support
//	@ID				webhookOnSupport
//	@Description	The BPP's answer to support. Only written to beckn_message_log; no status change or snapshot. The adapter has already verified the Beckn signature; no auth.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			request body server.OnSupportRequest true "Beckn on_support request"
//	@Success		200 {object} server.BecknAckResponse "Synchronous ACK; the message is processed asynchronously afterwards. The context is echoed with action set to on_support."
//	@Failure		400 {object} server.BecknAckResponse "NACK: the body is not valid JSON or does not match the expected types. Context fields are empty strings except action."
//	@Security
//	@Router			/api/webhook/on_support [post]
func (h *Handler) Handle(c *gin.Context) {
	var req OnSupportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.lh.WithModule("on_support_handler").Err().Error(err).Log("invalid on_support body")
		c.JSON(http.StatusBadRequest, buildNACK(BecknContext{}, err.Error()))
		return
	}
	c.JSON(http.StatusOK, BecknACK{
		Context: ackCtx(req.Context),
		Message: ACKMessage{ACK: ACKStatus{Status: "ACK"}},
	})
	go func() { h.svc.Process(context.Background(), &req) }()
}

func ackCtx(in BecknContext) BecknContext {
	out := in
	out.Action = "on_support"
	return out
}

func buildNACK(ctx BecknContext, _ string) BecknACK {
	return BecknACK{Context: ackCtx(ctx), Message: ACKMessage{ACK: ACKStatus{Status: "NACK"}}}
}
