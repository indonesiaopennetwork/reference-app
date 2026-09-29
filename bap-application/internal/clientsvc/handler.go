package clientsvc

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bap/internal/auth"
	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/discoverwait"
)

type ClientHandler struct {
	svc            *ClientService
	lh             *logharbour.Logger
	discoverMethod string
}

func NewClientHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger, discoverRegistry *discoverwait.Registry) *ClientHandler {
	return &ClientHandler{
		svc:            NewClientService(pool, cfg, lh, discoverRegistry),
		lh:             lh,
		discoverMethod: cfg.DiscoverMethod,
	}
}

// HandleSelect triggers a Beckn select request.
func (h *ClientHandler) HandleSelect(c *gin.Context) {
	var req ClientSelectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var buyerSubject *string
	if sub, ok := auth.SubjectFromContext(c); ok {
		buyerSubject = &sub
	}

	txnID, err := h.svc.Select(c.Request.Context(), &req, buyerSubject)
	if err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("select failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"transaction_id": txnID})
}

// HandleInit triggers a Beckn init request.
func (h *ClientHandler) HandleInit(c *gin.Context) {
	var req ClientInitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.svc.Init(c.Request.Context(), &req); err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("init failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "INIT_SENT"})
}

// HandleConfirm triggers a Beckn confirm request.
func (h *ClientHandler) HandleConfirm(c *gin.Context) {
	var req ClientConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.svc.Confirm(c.Request.Context(), &req); err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("confirm failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "CONFIRM_SENT"})
}

// HandleStatus returns the transaction lifecycle state.
func (h *ClientHandler) HandleStatus(c *gin.Context) {
	txnID := c.Param("id")
	if txnID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id parameter required"})
		return
	}

	resp, err := h.svc.GetStatus(c.Request.Context(), txnID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "transaction not found"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// HandleDiscover triggers a Discover request. The browser-facing contract
// is always a plain GET — which of the two underlying implementations
// actually handles it is a config-time choice, DISCOVER_METHOD (see
// config.Config.DiscoverMethod): exactly one is active at a time.
//   - "GET"  → ClientService.DiscoverSync, direct to discovery-service,
//     bypassing onix-bap.
//   - "POST" → ClientService.Discover, the real async Beckn contract via
//     onix-bap (same adapter-mediated path as every other action).
//
// GET /api/v1/discover?q=<text search term>
// Requires a non-empty textSearch — defaults to "coffee" for the initial catalog load.
func (h *ClientHandler) HandleDiscover(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		q = "coffee"
	}
	req := ClientDiscoverRequest{TextSearch: q}

	var (
		resp json.RawMessage
		err  error
	)
	switch h.discoverMethod {
	case config.DiscoverMethodPost:
		resp, err = h.svc.Discover(c.Request.Context(), &req)
	default:
		resp, err = h.svc.DiscoverSync(c.Request.Context(), &req)
	}
	if err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("discover failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Data(http.StatusOK, "application/json", resp)
}

// HandleRequestStatus triggers a Beckn status request (distinct from the local GET /status/:id poll).
func (h *ClientHandler) HandleRequestStatus(c *gin.Context) {
	var req ClientRequestStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.RequestStatus(c.Request.Context(), &req); err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("status request failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "STATUS_SENT"})
}

// HandleCancel triggers a Beckn cancel request.
func (h *ClientHandler) HandleCancel(c *gin.Context) {
	var req ClientCancelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.Cancel(c.Request.Context(), &req); err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("cancel failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "CANCEL_SENT"})
}

// HandleCancelItems cancels some or all of an order's items: a full cancellation
// goes out as Beckn cancel, a partial one as Beckn update.
func (h *ClientHandler) HandleCancelItems(c *gin.Context) {
	var req ClientCancelItemsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	status, err := h.svc.CancelItems(c.Request.Context(), &req)
	if err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("cancel items failed")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// HandleUpdate triggers a Beckn update request.
func (h *ClientHandler) HandleUpdate(c *gin.Context) {
	var req ClientUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.Update(c.Request.Context(), &req); err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("update failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "UPDATE_SENT"})
}

// HandleRate submits ratings for a completed order.
func (h *ClientHandler) HandleRate(c *gin.Context) {
	var req ClientRateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.Rate(c.Request.Context(), &req); err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("rate failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "RATE_SENT"})
}

// HandleSupport raises a support request for an order.
func (h *ClientHandler) HandleSupport(c *gin.Context) {
	var req ClientSupportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.svc.Support(c.Request.Context(), &req); err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("support failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "SUPPORT_SENT"})
}

// HandleCatalog returns the food retail catalog.
func (h *ClientHandler) HandleCatalog(c *gin.Context) {
	catalog, err := h.svc.GetCatalog(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json", catalog)
}
