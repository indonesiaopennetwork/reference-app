package updatesvc

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

// Handle answers POST /api/webhook/update: ACK at once, then on_update.
//
//	@Summary		Beckn update
//	@ID			becknUpdate
//	@Description	Returns a synchronous ACK, then works asynchronously. An update adding a RETURN performance is a return request, decided by the offer's return policy; one lowering a commitment's quantity or marking it CLOSED is a partial cancellation; any other update is stored as the contract's latest state. Then sends on_update with the resulting contract, unless the update failed.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			body	body	server.BecknWebhookRequest	true	"Beckn message forwarded by onix-bpp"
//	@Success		200	{object}	server.BecknAckResponse	"Synchronous ACK; context echoes the inbound context fields"
//	@Failure		400	{object}	server.BecknAckResponse	"NACK: body not decodable into the request type (context then all empty strings), or context.bapUri empty (context echoed). No reason field"
//	@Security
//	@Router			/api/webhook/update [post]
func (h *Handler) Handle(c *gin.Context) {
	var req UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.lh.WithModule("updatesvc").Err().Error(err).Log("invalid update request")
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
