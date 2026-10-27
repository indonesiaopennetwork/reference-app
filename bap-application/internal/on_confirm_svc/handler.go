package onconfirmsvc

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bap/internal/config"
)

// OnConfirmHandler handles POST /bap/receiver/on_confirm — a callback from the BPP
// with the confirmed (ACTIVE) contract.
type OnConfirmHandler struct {
	svc *OnConfirmService
	lh  *logharbour.Logger
}

func NewOnConfirmHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *OnConfirmHandler {
	return &OnConfirmHandler{
		svc: NewOnConfirmService(pool, cfg, lh),
		lh:  lh,
	}
}

// Handle answers POST /api/webhook/on_confirm with an immediate ACK and processes the callback asynchronously.
//
//	@Summary		Receive on_confirm (active contract)
//	@ID				webhookOnConfirm
//	@Description	The BPP's confirmation. Asynchronously sets the transaction status to CONFIRMED, stores the on_confirm contract snapshot and logs the message. The adapter has already verified the Beckn signature; no auth.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			request body server.BecknContractRequest true "Beckn on_confirm request"
//	@Success		200 {object} server.BecknAckResponse "Synchronous ACK; the message is processed asynchronously afterwards. The context is echoed with action set to on_confirm."
//	@Failure		400 {object} server.BecknAckResponse "NACK: the body is not valid JSON or does not match the expected types. Context fields are empty strings except action."
//	@Security
//	@Router			/api/webhook/on_confirm [post]
func (h *OnConfirmHandler) Handle(c *gin.Context) {
	var req OnConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.lh.WithModule("on_confirm_handler").Err().Error(err).Log("invalid on_confirm request body")
		c.JSON(http.StatusBadRequest, buildNACK(BecknContext{}, err.Error()))
		return
	}

	c.JSON(http.StatusOK, BecknACK{
		Context: contextForACK(req.Context, "on_confirm"),
		Message: ACKMessage{ACK: ACKStatus{Status: "ACK"}},
	})

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
		Context: contextForACK(ctx, "on_confirm"),
		Message: ACKMessage{ACK: ACKStatus{Status: "NACK"}},
	}
}
