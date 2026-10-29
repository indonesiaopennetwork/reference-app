package reconsvc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bpp/internal/auth"
	"github.com/ion/winroom/bpp/internal/config"
)

type Handler struct {
	svc *Service
	lh  *logharbour.Logger
}

func NewHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Handler {
	return &Handler{svc: NewService(pool, cfg, lh), lh: lh}
}

// bapGroup is one BAP's contracts in the overview, with the summary a
// /reconcile would carry for the ones due now.
type bapGroup struct {
	BapID    string        `json:"bap_id"`
	Due      []DueContract `json:"due"`
	Upcoming []DueContract `json:"upcoming"`
	Summary  *Summary      `json:"summary"` // of the next batch; nil when none
	// ResendID is the disputed batch the next /reconcile sends again in full.
	ResendID string `json:"resend_id,omitempty"`
}

// HandleOverview (GET /api/v1/reconciliation): per BAP, the contracts due now
// and still to fall due, the summary a /reconcile would send, the payout
// terms in force, and recent batches.
//
//	@Summary		Reconciliation overview per BAP
//	@ID			getReconciliationOverview
//	@Description	For each BAP: the seller's contracts whose settlement is not yet agreed, split into due (due now or disputed) and upcoming; the summary the next /reconcile would carry; and the disputed batch that would be sent again. Also returns the BPP's default payout terms and the 50 most recent reconciliation batches (sent or received). Read-only.
//	@Tags			reconciliation
//	@Produce		json
//	@Security		BearerAuth
//	@Success		200	{object}	ReconciliationOverviewResponse	"Overview"
//	@Failure		401	{object}	auth.ErrorResponse	"Bearer token missing (missing bearer token), or invalid or expired (invalid or expired token)"
//	@Failure		403	{object}	auth.NoProviderResponse	"Valid token, but no seller profile (users row) for it; call POST /api/v1/auth/bootstrap"
//	@Failure		500	{object}	auth.ErrorResponse	"Database or internal error (raw error text)"
//	@Router			/api/v1/reconciliation [get]
func (h *Handler) HandleOverview(c *gin.Context) {
	ctx := c.Request.Context()
	providerID := auth.ProviderIDFromContext(c)
	now := time.Now().UTC()
	contracts, err := h.svc.Contracts(ctx, providerID, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	groups := map[string]*bapGroup{}
	for _, d := range contracts {
		g := groups[d.BapID]
		if g == nil {
			g = &bapGroup{BapID: d.BapID, Due: []DueContract{}, Upcoming: []DueContract{}}
			groups[d.BapID] = g
		}
		if selectable(d) {
			g.Due = append(g.Due, d)
		} else {
			g.Upcoming = append(g.Upcoming, d)
		}
	}
	list := make([]*bapGroup, 0, len(groups))
	for _, g := range groups {
		resendID, batch := nextBatch(g.Due, h.svc.cfg.BppID)
		next := map[uuid.UUID]bool{}
		for _, d := range batch {
			next[d.ContractID] = true
		}
		for i := range g.Due {
			g.Due[i].InNextBatch = next[g.Due[i].ContractID]
			g.Due[i].Selectable = selectable(g.Due[i])
		}
		g.ResendID = resendID
		if len(batch) > 0 {
			contracts := make([]any, 0, len(batch))
			for _, d := range batch {
				contracts = append(contracts, snapshot(d.contract, d.Figures, d.Figures.bag()))
			}
			sm := summaryOf(g.BapID, h.svc.cfg.BppID, now.Format("2006-01-02"), contracts)
			g.Summary = &sm
		}
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].BapID < list[j].BapID })

	batches, err := h.svc.Batches(ctx, providerID, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"now":     now,
		"terms":   gin.H{"settlement_basis": h.svc.cfg.SettlementBasis, "settlement_window": h.svc.cfg.SettlementWindow},
		"baps":    list,
		"batches": batches,
	})
}

// HandleSend (POST /api/v1/reconciliation/send {bap_id}): send /reconcile for
// everything due now with that BAP. A batch that could not be delivered is
// still recorded (FAILED) and returned with the error.
//
//	@Summary		Send /reconcile to a BAP
//	@ID			sendReconciliation
//	@Description	Builds a /reconcile for one BAP and sends it through the onix-bpp caller. With contract_ids it sends the listed contracts that are due or disputed, as a new batch; without them, the disputed batch again in full, or else everything due.
//	@Description	The batch is first recorded in reconciliations (status SENDING) and its payments marked PENDING; if the send fails the batch is marked FAILED and the payments go back to their earlier state. The BAP's /on_reconcile arrives later at /api/webhook/on_reconcile. On 200 the batch status is SENT, or already the BAP's outcome if /on_reconcile arrived first.
//	@Tags			reconciliation
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body	ReconciliationSendRequest	true	"BAP and optional contracts"
//	@Success		200	{object}	ReconciliationSendResponse	"Sent"
//	@Failure		400	{object}	auth.ErrorResponse	"Invalid JSON or a missing required field (gin binding error text)"
//	@Failure		401	{object}	auth.ErrorResponse	"Bearer token missing (missing bearer token), or invalid or expired (invalid or expired token)"
//	@Failure		403	{object}	auth.NoProviderResponse	"Valid token, but no seller profile (users row) for it; call POST /api/v1/auth/bootstrap"
//	@Failure		409	{object}	auth.ErrorResponse	"None of the seller's contracts with the BAP is due, or none of the selected ones is (no contracts are due for reconciliation with this BAP)"
//	@Failure		500	{object}	auth.ErrorResponse	"Database or internal error (raw error text)"
//	@Failure		502	{object}	ReconciliationSendErrorResponse	"Not delivered; the batch is still recorded as FAILED and returned"
//	@Router			/api/v1/reconciliation/send [post]
func (h *Handler) HandleSend(c *gin.Context) {
	var req struct {
		BapID string `json:"bap_id" binding:"required"`
		// ContractIDs: the contracts to send (see choose); empty sends the
		// default batch.
		ContractIDs []string `json:"contract_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	batch, err := h.svc.Send(c.Request.Context(), auth.ProviderIDFromContext(c), req.BapID, req.ContractIDs)
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

// HandleOnReconcile (POST /api/webhook/on_reconcile): ACK the BAP's
// answer to this BPP's /reconcile at once and apply it in the background,
// like the other webhooks.
//
//	@Summary		BAP's answer to this BPP's /reconcile
//	@ID			becknOnReconcile
//	@Description	Returns a synchronous ACK echoing context as received, then works asynchronously: sets the outbound batch message.reconciliationId to AGREED (only when every due contract has accord true and the BAP said AGREED), DISPUTED, or PENDING (some contracts were sent before their settlement window), and updates each contract's settlement reconciliation state to match. Nothing is sent back.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			body	body	server.BecknWebhookRequest	true	"Beckn message forwarded by onix-bpp"
//	@Success		200	{object}	server.ReconAckResponse	"ACK; context is the inbound context as received"
//	@Failure		400	{object}	server.ReconNackResponse	"The body is not a JSON object (NACK, no context)"
//	@Security
//	@Router			/api/webhook/on_reconcile [post]
func (h *Handler) HandleOnReconcile(c *gin.Context) {
	h.ackThen(c, "on_reconcile not applied", h.svc.ApplyOnReconcile)
}

// HandleReconcile (POST /api/webhook/reconcile): ACK a BAP's /reconcile at
// once, then check it against this BPP's figures and answer /on_reconcile.
//
//	@Summary		A BAP's own /reconcile
//	@ID			becknReconcile
//	@Description	Returns a synchronous ACK echoing context as received, then works asynchronously: checks each contract's figures against this BPP's own (the batch is AGREED only if every contract agrees, otherwise DISPUTED), records an INBOUND reconciliations row, updates the settlement states, and sends on_reconcile through the onix-bpp caller.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			body	body	server.BecknWebhookRequest	true	"Beckn message forwarded by onix-bpp"
//	@Success		200	{object}	server.ReconAckResponse	"ACK; context is the inbound context as received"
//	@Failure		400	{object}	server.ReconNackResponse	"The body is not a JSON object (NACK, no context)"
//	@Security
//	@Router			/api/webhook/reconcile [post]
func (h *Handler) HandleReconcile(c *gin.Context) {
	h.ackThen(c, "reconcile not answered", h.svc.Respond)
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
			h.lh.WithModule("reconsvc").Err().Error(err).Log(failure)
		}
	}()
}
