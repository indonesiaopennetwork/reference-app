package reconcilesvc

import (
	"context"
	"encoding/json"
	"io"
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

// Handle (POST /api/webhook/reconcile): ACK at once, then verify the batch,
// record the outcome and answer /on_reconcile in the background.
func (h *Handler) Handle(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	var env struct {
		Context json.RawMessage `json:"context"`
	}
	if err != nil || json.Unmarshal(raw, &env) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": gin.H{"ack": gin.H{"status": "NACK"}}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"context": env.Context, "message": gin.H{"ack": gin.H{"status": "ACK"}}})
	go func() {
		if err := h.svc.Process(context.Background(), raw); err != nil {
			h.lh.WithModule("reconcile_svc").Err().Error(err).Log("reconcile processing failed")
		}
	}()
}
