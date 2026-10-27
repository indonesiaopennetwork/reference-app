package onselectsvc

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bap/internal/config"
)

// OnSelectHandler handles POST /bap/receiver/on_select — a callback from the BPP
// carrying a quoted contract (on_select). The BAP immediately returns ACK and
// asynchronously persists the contract snapshot.
type OnSelectHandler struct {
	svc *OnSelectService
	lh  *logharbour.Logger
}

func NewOnSelectHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *OnSelectHandler {
	return &OnSelectHandler{
		svc: NewOnSelectService(pool, cfg, lh),
		lh:  lh,
	}
}

// Handle answers POST /api/webhook/on_select with an immediate ACK and processes the callback asynchronously.
//
//	@Summary		Receive on_select (quote)
//	@ID				webhookOnSelect
//	@Description	The BPP's quoted contract. Asynchronously sets the transaction status to QUOTE_RECEIVED, stores message.contract as the on_select contract snapshot and logs the message. The adapter has already verified the Beckn signature; no auth.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			request body server.BecknContractRequest true "Beckn on_select request"
//	@Success		200 {object} server.BecknAckResponse "Synchronous ACK; the message is processed asynchronously afterwards. The context is echoed with action set to on_select."
//	@Failure		400 {object} server.BecknAckResponse "NACK: the body is not valid JSON or does not match the expected types. Context fields are empty strings except action."
//	@Security
//	@Router			/api/webhook/on_select [post]
func (h *OnSelectHandler) Handle(c *gin.Context) {
	var req OnSelectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.lh.WithModule("on_select_handler").Err().Error(err).Log("invalid on_select request body")
		c.JSON(http.StatusBadRequest, buildNACK(BecknContext{}, err.Error()))
		return
	}

	// Immediately return ACK — Beckn spec requires a synchronous ACK.
	c.JSON(http.StatusOK, BecknACK{
		Context: contextForACK(req.Context, "on_select"),
		Message: ACKMessage{ACK: ACKStatus{Status: "ACK"}},
	})

	// Persist state asynchronously.
	go func() {
		h.svc.Process(context.Background(), &req)
	}()
}

func contextForACK(in BecknContext, action string) BecknContext {
	out := in
	out.Action = action
	return out
}

func buildNACK(ctx BecknContext, _ string) BecknACK {
	return BecknACK{
		Context: contextForACK(ctx, "on_select"),
		Message: ACKMessage{ACK: ACKStatus{Status: "NACK"}},
	}
}
