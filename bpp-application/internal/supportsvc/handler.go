package supportsvc

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bpp/internal/config"
)

type Handler struct {
	svc *Service
	lh  *logharbour.Logger
}

func NewHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Handler {
	return &Handler{svc: NewService(pool, cfg, lh), lh: lh}
}

// Handle answers POST /api/webhook/support: ACK at once, then on_support.
//
//	@Summary		Beckn support
//	@ID			becknSupport
//	@Description	Returns a synchronous ACK, then works asynchronously: creates a support ticket from message.support (orderId is treated as a transaction id when linking it to a contract) and sends on_support with the ticket id and the BPP's hard-coded support channels.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			body	body	server.BecknWebhookRequest	true	"Beckn message forwarded by onix-bpp"
//	@Success		200	{object}	server.BecknAckResponse	"Synchronous ACK; context echoes the inbound context fields"
//	@Failure		400	{object}	server.BecknAckResponse	"NACK: body not decodable into the request type (context then all empty strings), or context.bapUri empty (context echoed). No reason field"
//	@Security
//	@Router			/api/webhook/support [post]
func (h *Handler) Handle(c *gin.Context) {
	var req SupportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.lh.WithModule("supportsvc").Err().Error(err).Log("invalid support request")
		c.JSON(http.StatusBadRequest, buildNACK(BecknContext{}, "invalid request body"))
		return
	}
	if req.Context.BapURI == "" {
		c.JSON(http.StatusBadRequest, buildNACK(req.Context, "bapUri required"))
		return
	}
	c.JSON(http.StatusOK, BecknACK{Context: req.Context, Message: ACKMessage{ACK: ACKStatus{Status: "ACK"}}})
	go func() { h.svc.Process(context.Background(), &req) }()
}

func buildNACK(ctx BecknContext, _ string) BecknACK {
	return BecknACK{Context: ctx, Message: ACKMessage{ACK: ACKStatus{Status: "NACK"}}}
}
