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
