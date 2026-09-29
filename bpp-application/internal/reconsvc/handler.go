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
	Summary  *Summary      `json:"summary"` // of the due contracts; nil when none
}

// HandleOverview (GET /api/v1/reconciliation): per BAP, the contracts due now
// and still to fall due, the summary a /reconcile would send, the payout
// terms in force, and recent batches.
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
		if d.Due {
			g.Due = append(g.Due, d)
		} else {
			g.Upcoming = append(g.Upcoming, d)
		}
	}
	list := make([]*bapGroup, 0, len(groups))
	for _, g := range groups {
		if len(g.Due) > 0 {
			sm := h.svc.summarize(g.BapID, g.Due, now.Format("2006-01-02"))
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
func (h *Handler) HandleSend(c *gin.Context) {
	var req struct {
		BapID string `json:"bap_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	batch, err := h.svc.Send(c.Request.Context(), auth.ProviderIDFromContext(c), req.BapID)
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
// outcome at once and apply it in the background, like the other webhooks.
func (h *Handler) HandleOnReconcile(c *gin.Context) {
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
		if err := h.svc.ApplyOnReconcile(context.Background(), raw); err != nil {
			h.lh.WithModule("reconsvc").Err().Error(err).Log("on_reconcile not applied")
		}
	}()
}
