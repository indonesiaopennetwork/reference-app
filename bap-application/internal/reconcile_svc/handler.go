package reconcilesvc

import (
	"context"
	"encoding/json"
	"errors"
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

// Handle (POST /api/webhook/reconcile): ACK a BPP's /reconcile at once, then
// check it, record the outcome and answer /on_reconcile in the background.
func (h *Handler) Handle(c *gin.Context) {
	h.ackThen(c, "reconcile processing failed", h.svc.Process)
}

// HandleOnReconcile (POST /api/webhook/on_reconcile): ACK a BPP's answer to
// this BAP's /reconcile at once and apply it in the background.
func (h *Handler) HandleOnReconcile(c *gin.Context) {
	h.ackThen(c, "on_reconcile not applied", h.svc.ApplyOnReconcile)
}

// HandleSend (POST /api/v1/reconciliation/send {bpp_id}): send this BAP's
// /reconcile for every contract due with that BPP. A batch that could not be
// delivered is still recorded (FAILED) and returned with the error.
func (h *Handler) HandleSend(c *gin.Context) {
	var req struct {
		BppID string `json:"bpp_id" binding:"required"`
		// ContractIDs: the contracts to send (see choose); empty sends the
		// default batch.
		ContractIDs []string `json:"contract_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	batch, err := h.svc.Send(c.Request.Context(), req.BppID, req.ContractIDs)
	switch {
	case errors.Is(err, ErrNothingDue):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case err != nil && batch == nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	case err != nil:
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "batch": batch})
	default:
		c.JSON(http.StatusOK, gin.H{"batch": batch})
	}
}

func (h *Handler) ackThen(c *gin.Context, failure string, process func(context.Context, []byte) error) {
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
		if err := process(context.Background(), raw); err != nil {
			h.lh.WithModule("reconcile_svc").Err().Error(err).Log(failure)
		}
	}()
}
