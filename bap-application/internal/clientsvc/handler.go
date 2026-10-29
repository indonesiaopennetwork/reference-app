package clientsvc

import (
	"encoding/json"
	"errors"
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
//
//	@Summary		Start an order (Beckn select)
//	@ID				select
//	@Description	Creates a new transaction (status SELECT_SENT, owned by the logged-in buyer if any) and sends a Beckn select built from the items to onix-bap. The BPP is taken from the first item's bpp_id/bpp_uri, falling back to server config; the quote arrives later via /api/webhook/on_select (status QUOTE_RECEIVED). The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			orders
//	@Accept			json
//	@Produce		json
//	@Param			request body ClientSelectRequest true "Request body"
//	@Success		200 {object} SelectResponse "Select sent and ACKed by the adapter."
//	@Failure		400 {object} ErrorResponse "Request body missing, not JSON, or missing a required field."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Failure		500 {object} ErrorResponse "Building the message failed (e.g. an empty items array) or the adapter call failed/returned non-200."
//	@Security		BearerAuth
//	@Router			/api/v1/select [post]
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
//
//	@Summary		Submit buyer details (Beckn init)
//	@ID				init
//	@Description	Loads the latest (on_select) contract snapshot, checks the quote is still fulfillable, patches in the buyer's billing/delivery details and the settlement declaration, sets the status to INIT_SENT and sends Beckn init via onix-bap. The draft contract arrives later via /api/webhook/on_init (status INIT_RECEIVED). The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			orders
//	@Accept			json
//	@Produce		json
//	@Param			request body ClientInitRequest{billing=Billing} true "Request body"
//	@Success		200 {object} ActionStatusResponse "Init sent (status INIT_SENT)."
//	@Failure		400 {object} ErrorResponse "Request body missing, not JSON, or missing a required field."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Failure		409 {object} ErrorResponse "The quote is no longer available (an item was marked CLOSED by the seller, or the quote has no price)."
//	@Failure		500 {object} ErrorResponse "Transaction or on_select snapshot not found, invalid billing, DB error, or adapter failure."
//	@Security		BearerAuth
//	@Router			/api/v1/init [post]
func (h *ClientHandler) HandleInit(c *gin.Context) {
	var req ClientInitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.svc.Init(c.Request.Context(), &req); err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("init failed")
		status := http.StatusInternalServerError
		if errors.Is(err, ErrQuoteUnavailable) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "INIT_SENT"})
}

// HandleConfirm triggers a Beckn confirm request.
//
//	@Summary		Pay and confirm (Beckn confirm)
//	@ID				confirm
//	@Description	Loads the latest (on_init) contract snapshot, records a mock payment on its settlement (status PAID, paidAt, a generated gatewayRef; there is no real payment gateway), sets the status to CONFIRM_SENT and sends Beckn confirm via onix-bap. The confirmation arrives later via /api/webhook/on_confirm (status CONFIRMED). The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			orders
//	@Accept			json
//	@Produce		json
//	@Param			request body ClientConfirmRequest true "Request body"
//	@Success		200 {object} ActionStatusResponse "Confirm sent (status CONFIRM_SENT)."
//	@Failure		400 {object} ErrorResponse "Request body missing, not JSON, or missing a required field."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Failure		500 {object} ErrorResponse "Transaction or snapshot not found, database error, or the adapter call failed / returned non-200 (the error text says which)."
//	@Security		BearerAuth
//	@Router			/api/v1/confirm [post]
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
//
//	@Summary		Poll a transaction's local state
//	@ID				getTransactionStatus
//	@Description	Reads the transaction's current status and latest contract snapshot from the BAP database (no Beckn call; see POST /api/v1/request-status for that). Not scoped to the buyer: any transaction id resolves, with or without a token. The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			orders
//	@Produce		json
//	@Param			id path string true "Transaction id (UUID) returned by POST /api/v1/select." format(uuid)
//	@Success		200 {object} ClientStatusResponse "Current state."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Failure		404 {object} ErrorResponse "Unknown or malformed transaction id."
//	@Security		BearerAuth
//	@Router			/api/v1/status/{id} [get]
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
//
//	@Summary		Search the network (Beckn discover)
//	@ID				discover
//	@Description	Sends a Beckn discover with intent.textSearch = q (plus " provider_name" when given) and returns the result synchronously. DISCOVER_METHOD picks the path: GET (default) calls the discovery service's sync GET /discover directly and returns its body as-is (or {"message":{"catalogs":[]}} when no discovery URL is configured); POST sends discover through onix-bap and waits up to 60s for the on_discover webhook, returning that callback's body. A logged-in buyer's search is recorded in their message log. The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			discover & catalog
//	@Produce		json
//	@Param			q query string false "Free-text search term. When both q and provider_name are empty, coffee is used."
//	@Param			provider_name query string false "Provider name; appended to the text search (<q> <provider_name>)."
//	@Success		200 {object} DiscoverResponse "Discover result as returned by the discovery service, or the on_discover callback body (context + message.catalogs)."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Failure		500 {object} ErrorResponse "Discovery call failed, returned a non-200, or (POST mode) the on_discover callback did not arrive within 60s."
//	@Security		BearerAuth
//	@Router			/api/v1/discover [get]
func (h *ClientHandler) HandleDiscover(c *gin.Context) {
	q := c.Query("q")
	providerName := c.Query("provider_name")
	if q == "" && providerName == "" {
		q = "coffee"
	}

	req := ClientDiscoverRequest{
		TextSearch:   q,
		ProviderName: providerName,
	}
	if sub, ok := auth.SubjectFromContext(c); ok {
		req.BuyerSubject = &sub
	}

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
//
//	@Summary		Ask the BPP for status (Beckn status)
//	@ID				requestStatus
//	@Description	Sets the transaction status to STATUS_SENT and sends Beckn status with the latest contract via onix-bap. The answer arrives via /api/webhook/on_status, which moves the status on according to the contract's state. The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			order actions
//	@Accept			json
//	@Produce		json
//	@Param			request body ClientRequestStatusRequest true "Request body"
//	@Success		200 {object} ActionStatusResponse "Status request sent (status STATUS_SENT)."
//	@Failure		400 {object} ErrorResponse "Request body missing, not JSON, or missing a required field."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Failure		500 {object} ErrorResponse "Transaction or snapshot not found, database error, or the adapter call failed / returned non-200 (the error text says which)."
//	@Security		BearerAuth
//	@Router			/api/v1/request-status [post]
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
//
//	@Summary		Cancel the whole order (Beckn cancel)
//	@ID				cancelOrder
//	@Description	Declares the cancellation (reason and every still-active commitment) in the latest contract's contractAttributes.cancellation, checks the offers' cancellation cut-off, sets the status to CANCEL_SENT and sends Beckn cancel via onix-bap; the result arrives via /api/webhook/on_cancel (status CANCELLED). Unlike /cancel-items, reason_code is not checked against the known list. The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			order actions
//	@Accept			json
//	@Produce		json
//	@Param			request body ClientCancelRequest true "Request body"
//	@Success		200 {object} ActionStatusResponse "Cancel sent (status CANCEL_SENT)."
//	@Failure		400 {object} ErrorResponse "Request body missing, not JSON, or missing a required field."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Failure		500 {object} ErrorResponse "Transaction/snapshot not found, cancellation not allowed by the offer policy, DB error, or adapter failure."
//	@Security		BearerAuth
//	@Router			/api/v1/cancel [post]
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
//
//	@Summary		Cancel some or all items
//	@ID				cancelItems
//	@Description	Validates the items against the latest contract (active commitments, quantities, cancellation cut-off, known reason code). If they add up to everything still active it behaves like POST /api/v1/cancel (Beckn cancel, status CANCEL_SENT); otherwise it lowers the quantities (or closes the commitments), declares the partial cancellation and sends Beckn update (status UPDATE_SENT). The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			order actions
//	@Accept			json
//	@Produce		json
//	@Param			request body ClientCancelItemsRequest true "Request body"
//	@Success		200 {object} CancelItemsResponse "Cancel or update sent; status says which."
//	@Failure		400 {object} ErrorResponse "Invalid body, or any failure while validating or sending (unknown commitment, too many units, cut-off passed, unknown reason, snapshot missing, adapter failure). All errors are reported as 400."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Security		BearerAuth
//	@Router			/api/v1/cancel-items [post]
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

// HandleReturnItems requests a return of some or all of a delivered order's items.
//
//	@Summary		Return some or all delivered items
//	@ID				returnItems
//	@Description	Checks each item against its offer's return policy (returns allowed, order delivered, still inside the return window counted from delivery), lowers the returned quantities (or closes the commitments), adds a RETURN performance with status Return_Initiated and sends Beckn update via onix-bap (status UPDATE_SENT). The BPP's decision arrives via /api/webhook/on_update. The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			order actions
//	@Accept			json
//	@Produce		json
//	@Param			request body ClientReturnItemsRequest true "Request body"
//	@Success		200 {object} ReturnItemsResponse "Update sent."
//	@Failure		400 {object} ErrorResponse "Invalid body, or any failure while validating or sending (unknown commitment, not returnable, window closed, too many units, adapter failure). All errors are reported as 400."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Security		BearerAuth
//	@Router			/api/v1/return-items [post]
func (h *ClientHandler) HandleReturnItems(c *gin.Context) {
	var req ClientReturnItemsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	status, err := h.svc.ReturnItems(c.Request.Context(), &req)
	if err != nil {
		h.lh.WithModule("client_handler").Err().Error(err).Log("return items failed")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// HandleUpdate triggers a Beckn update request.
//
//	@Summary		Send a raw contract update (Beckn update)
//	@ID				updateOrder
//	@Description	Sends the given contract as-is as Beckn update via onix-bap and sets the status to UPDATE_SENT. The BPP's answer arrives via /api/webhook/on_update. The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			order actions
//	@Accept			json
//	@Produce		json
//	@Param			request body ClientUpdateRequest true "Request body"
//	@Success		200 {object} ActionStatusResponse "Update sent (status UPDATE_SENT)."
//	@Failure		400 {object} ErrorResponse "Request body missing, not JSON, or missing a required field."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Failure		500 {object} ErrorResponse "Transaction or snapshot not found, database error, or the adapter call failed / returned non-200 (the error text says which)."
//	@Security		BearerAuth
//	@Router			/api/v1/update [post]
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
//
//	@Summary		Rate an order (Beckn rate)
//	@ID				rateOrder
//	@Description	Maps each rating input to the Beckn RatingInput wire shape (target.id, target.descriptor, range, feedbackFormSubmission; ratingCategory is not sent), sets the status to RATE_SENT and sends Beckn rate via onix-bap. The answer arrives via /api/webhook/on_rate. The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			order actions
//	@Accept			json
//	@Produce		json
//	@Param			request body ClientRateRequest true "Request body"
//	@Success		200 {object} ActionStatusResponse "Rate sent (status RATE_SENT)."
//	@Failure		400 {object} ErrorResponse "Request body missing, not JSON, or missing a required field."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Failure		500 {object} ErrorResponse "Transaction or snapshot not found, database error, or the adapter call failed / returned non-200 (the error text says which)."
//	@Security		BearerAuth
//	@Router			/api/v1/rate [post]
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
//
//	@Summary		Raise a support request (Beckn support)
//	@ID				requestSupport
//	@Description	Sends Beckn support with support.orderId (defaults to the transaction id) and support.descriptor.shortDesc = description, and sets the status to SUPPORT_SENT. The answer arrives via /api/webhook/on_support. The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			order actions
//	@Accept			json
//	@Produce		json
//	@Param			request body ClientSupportRequest true "Request body"
//	@Success		200 {object} ActionStatusResponse "Support request sent (status SUPPORT_SENT)."
//	@Failure		400 {object} ErrorResponse "Request body missing, not JSON, or missing a required field."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Failure		500 {object} ErrorResponse "Transaction or snapshot not found, database error, or the adapter call failed / returned non-200 (the error text says which)."
//	@Security		BearerAuth
//	@Router			/api/v1/support [post]
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
//
//	@Summary		Static demo catalog
//	@ID				getCatalog
//	@Description	Returns a hard-coded list of five demo products. It does not query any BPP or the network. The bearer token is optional: without one the request is served anonymously; one that fails verification is rejected with 401.
//	@Tags			discover & catalog
//	@Produce		json
//	@Success		200 {array} DemoCatalogItem "Demo products."
//	@Failure		401 {object} ErrorResponse "A bearer token was sent but failed verification (no token is fine)."
//	@Failure		500 {object} ErrorResponse "Catalog could not be loaded (not reached by the current hard-coded catalog)."
//	@Security		BearerAuth
//	@Router			/api/v1/catalog [get]
func (h *ClientHandler) HandleCatalog(c *gin.Context) {
	catalog, err := h.svc.GetCatalog(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json", catalog)
}
