package ratesvc

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

// Handle answers POST /api/webhook/rate: ACK at once, then on_rate.
//
//	@Summary		Beckn rate
//	@ID			becknRate
//	@Description	Returns a synchronous ACK, then works asynchronously: finds the contract by context.transactionId, stores each message.ratingInputs[] entry as a rating (target_id is always the contract id), and sends on_rate echoing the targets and ranges. If no contract is found, nothing is stored or sent.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			body	body	server.BecknWebhookRequest	true	"Beckn message forwarded by onix-bpp"
//	@Success		200	{object}	server.BecknAckResponse	"Synchronous ACK; context echoes the inbound context fields"
//	@Failure		400	{object}	server.BecknAckResponse	"NACK: body not decodable into the request type (context then all empty strings), or context.bapUri empty (context echoed). No reason field"
//	@Security
//	@Router			/api/webhook/rate [post]
func (h *Handler) Handle(c *gin.Context) {
	var req RateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.lh.WithModule("ratesvc").Err().Error(err).Log("invalid rate request")
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
