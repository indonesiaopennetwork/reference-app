package cancelsvc

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

// Handle answers POST /api/webhook/cancel: ACK at once, then on_cancel.
//
//	@Summary		Beckn cancel
//	@ID			becknCancel
//	@Description	Returns a synchronous ACK, then works asynchronously: fully cancels the ACTIVE contract (every commitment CLOSED, the price consideration CANCELLED, a cancellation fee and refund settlement added, stock restored; reason from message.contract.contractAttributes.cancellation) and sends on_cancel. If the cancellation fails, nothing is sent.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			body	body	server.BecknWebhookRequest	true	"Beckn message forwarded by onix-bpp"
//	@Success		200	{object}	server.BecknAckResponse	"Synchronous ACK; context echoes the inbound context fields"
//	@Failure		400	{object}	server.BecknAckResponse	"NACK: body not decodable into the request type (context then all empty strings), or context.bapUri empty (context echoed). No reason field"
//	@Security
//	@Router			/api/webhook/cancel [post]
func (h *Handler) Handle(c *gin.Context) {
	var req CancelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.lh.WithModule("cancelsvc").Err().Error(err).Log("invalid cancel request")
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
