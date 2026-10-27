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
//
//	@Summary		Receive a BPP's /reconcile (settlement batch)
//	@ID				webhookReconcile
//	@Description	ION reconciliation extension. ACKs at once, then in the background checks each contract's settlement figures against this BAP's own copy (contracts not yet past their settlement window are answered accord: false with code SETTLEMENT_WINDOW_NOT_REACHED), records the batch as INBOUND in reconciliations, stamps each contract's outcome on a new snapshot and sends /on_reconcile back via onix-bap. No auth (signature verified by the adapter).
//	@Tags			beckn webhooks, reconciliation
//	@Accept			json
//	@Produce		json
//	@Param			request body Request true "ION /reconcile (ion-specs core/api/v2.0.0/ion-additional.yaml); only the fields shown are read."
//	@Success		200 {object} server.ReconcileAckResponse "Synchronous ACK echoing the request's context verbatim; processing continues in the background."
//	@Failure		400 {object} server.ReconcileNackResponse "NACK: the body could not be read or is not a JSON object (no context key)."
//	@Security
//	@Router			/api/webhook/reconcile [post]
func (h *Handler) Handle(c *gin.Context) {
	h.ackThen(c, "reconcile processing failed", h.svc.Process)
}

// HandleOnReconcile (POST /api/webhook/on_reconcile): ACK a BPP's answer to
// this BAP's /reconcile at once and apply it in the background.
//
//	@Summary		Receive a BPP's /on_reconcile
//	@ID				webhookOnReconcile
//	@Description	A BPP's answer to a /reconcile this BAP sent. ACKs at once, then in the background updates the OUTBOUND batch with the same context.messageId (else transactionId) to AGREED or DISPUTED from the per-contract accords, and stamps each contract's outcome. An unknown batch is only logged as an error (the ACK has already been sent). No auth (signature verified by the adapter).
//	@Tags			beckn webhooks, reconciliation
//	@Accept			json
//	@Produce		json
//	@Param			request body Request true "ION /on_reconcile; only the fields shown are read."
//	@Success		200 {object} server.ReconcileAckResponse "Synchronous ACK echoing the request's context verbatim; processing continues in the background."
//	@Failure		400 {object} server.ReconcileNackResponse "NACK: the body could not be read or is not a JSON object (no context key)."
//	@Security
//	@Router			/api/webhook/on_reconcile [post]
func (h *Handler) HandleOnReconcile(c *gin.Context) {
	h.ackThen(c, "on_reconcile not applied", h.svc.ApplyOnReconcile)
}

// HandleSend (POST /api/v1/reconciliation/send {bpp_id}): send this BAP's
// /reconcile for every contract due with that BPP. A batch that could not be
// delivered is still recorded (FAILED) and returned with the error.
//
//	@Summary		Send this BAP's /reconcile to a BPP
//	@ID				sendReconcile
//	@Description	Builds and sends a /reconcile batch to the given BPP via onix-bap: the selected contract_ids (those due or disputed), or by default the batch this BAP previously sent that was disputed (re-sent in the same exchange: same transactionId, new messageId), else every contract with that BPP that is due now. The batch is recorded as OUTBOUND (SENDING -> SENT, or FAILED if delivery fails); the BPP's answer arrives later on /api/webhook/on_reconcile. Open: no login required.
//	@Tags			reconciliation
//	@Accept			json
//	@Produce		json
//	@Param			request body SendRequest true "The seller and, optionally, the contracts to send."
//	@Success		200 {object} SendResponse "Batch sent (adapter ACKed)."
//	@Failure		400 {object} clientsvc.ErrorResponse "Request body missing, not JSON, or missing bpp_id."
//	@Failure		409 {object} clientsvc.ErrorResponse "No contract with this BPP is due (or none of the selected ones is due/disputed)."
//	@Failure		500 {object} clientsvc.ErrorResponse "Database error, or the batch could not be built (the error text says which)."
//	@Failure		502 {object} SendFailedResponse "The batch was recorded but could not be delivered; it is returned with status FAILED and its contracts are restored to their earlier reconciliation state."
//	@Security
//	@Router			/api/v1/reconciliation/send [post]
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
