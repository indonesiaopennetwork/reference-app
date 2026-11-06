package grievancesvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/auth"
	"github.com/ion/winroom/bap/internal/config"
	"github.com/ion/winroom/bap/internal/grievance"
)

// Handler serves the buyer's grievance API and the on_support webhook.
type Handler struct {
	svc *Service
	lh  *logharbour.Logger
}

// NewHandler returns a Handler.
func NewHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Handler {
	return &Handler{svc: NewService(pool, cfg, lh), lh: lh}
}

// Register mounts the buyer API on v1 (which resolves the optional buyer
// token) and the operator route on admin.
func (h *Handler) Register(v1, admin *gin.RouterGroup) {
	v1.GET("/grievances", h.HandleList)
	v1.POST("/grievances", h.HandleOpen)
	v1.GET("/grievances/:id", h.HandleGet)
	v1.POST("/grievances/:id/decision", h.HandleDecide)
	v1.POST("/grievances/:id/objection", h.HandleObject)
	v1.POST("/grievances/:id/close", h.HandleClose)
	v1.POST("/grievances/:id/response", h.HandleRespond)
	v1.GET("/orders/:id/grievance-options", h.HandleOptions)
	admin.POST("/grievances/:id/goodwill-refund", h.HandleGoodwill)
}

func buyerOf(c *gin.Context) string {
	s, _ := auth.SubjectFromContext(c)
	return s
}

func (h *Handler) fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "grievance or order not found", "code": "NOT_FOUND"})
	case errors.Is(err, ErrBadRequest):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID"})
	default:
		// Not delivered (NACK, adapter down): the event was not recorded,
		// so the frontend keeps the user's input and offers a retry.
		h.lh.WithModule("grievancesvc").Warn().Error(err).Log("grievance request failed")
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "code": "SEND_FAILED"})
	}
}

// HandleList lists the logged-in buyer's grievances.
//
//	@Summary		List my grievances
//	@ID				listGrievances
//	@Description	The logged-in buyer's grievances, open ones first, newest activity first; empty without a token. transaction_id narrows it to one order.
//	@Tags			grievances
//	@Produce		json
//	@Param			transaction_id query string false "Only this order's grievances." format(uuid)
//	@Success		200 {object} GrievanceListResponse "Grievances."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances [get]
func (h *Handler) HandleList(c *gin.Context) {
	list, err := h.svc.List(c.Request.Context(), buyerOf(c), c.Query("transaction_id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	n := 0
	for _, v := range list {
		if v.NeedsAction {
			n++
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": list, "needsAction": n})
}

// HandleOpen opens a buyer grievance about an order.
//
//	@Summary		Raise a problem with an order (grievance OPENED)
//	@ID				openGrievance
//	@Description	Sends /support with an IONGrievance OPENED (ion-specs Grievance/v1) for the buyer's order. Return and refund categories (RETURN_REJECTED, RETURN_PICKUP_FAILED, RETURN_QC_DISPUTED, REFUND_NOT_RECEIVED) link the order's latest return / refund automatically when related is empty. 502 SEND_FAILED: not delivered, nothing recorded; retry.
//	@Tags			grievances
//	@Accept			json
//	@Produce		json
//	@Param			request body OpenRequest true "Grievance"
//	@Success		200 {object} GrievanceView "Opened (ACKed); the seller's answer arrives on /api/webhook/on_support."
//	@Failure		400 {object} ErrorResponse "Invalid category, description, lines or event."
//	@Failure		404 {object} ErrorResponse "Not the buyer's order."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances [post]
func (h *Handler) HandleOpen(c *gin.Context) {
	var req OpenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID"})
		return
	}
	v, err := h.svc.Open(c.Request.Context(), buyerOf(c), req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// HandleGet returns a grievance with its order and marks it seen.
//
//	@Summary		Get a grievance
//	@ID				getGrievance
//	@Description	The grievance from the buyer's side (next step, active offer, earlier versions, timeline, contact channels) with the order's lines, returns and refunds. Marks it seen.
//	@Tags			grievances
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Success		200 {object} GrievanceDetail "Grievance."
//	@Failure		404 {object} ErrorResponse "Not the buyer's grievance."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id} [get]
func (h *Handler) HandleGet(c *gin.Context) {
	d, err := h.svc.Get(c.Request.Context(), buyerOf(c), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

// DecideRequest accepts or rejects the active offer.
type DecideRequest struct {
	Accept *bool  `json:"accept" binding:"required"`
	Reason string `json:"reason"`
}

// HandleDecide accepts or rejects the seller's active offer as a whole.
//
//	@Summary		Accept or reject the seller's offer (RESOLUTION_DECIDED)
//	@ID				decideGrievance
//	@Description	Decides on the latest version of an EXPLICIT_AGREEMENT resolution as a whole. An older version, or one already decided, is refused (400).
//	@Tags			grievances
//	@Accept			json
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Param			request body DecideRequest true "Decision"
//	@Success		200 {object} GrievanceView "Sent."
//	@Failure		400 {object} ErrorResponse "No offer to decide, or the event breaks the rules."
//	@Failure		404 {object} ErrorResponse "Not the buyer's grievance."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id}/decision [post]
func (h *Handler) HandleDecide(c *gin.Context) {
	var req DecideRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "accept (boolean) is required", "code": "INVALID"})
		return
	}
	v, err := h.svc.Decide(c.Request.Context(), buyerOf(c), c.Param("id"), *req.Accept, req.Reason)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// ExplanationRequest carries what is still wrong.
type ExplanationRequest struct {
	Explanation string `json:"explanation" binding:"required"`
}

// HandleObject objects to a RESOLVED grievance.
//
//	@Summary		Object to a resolved grievance (OBJECTION)
//	@ID				objectGrievance
//	@Description	Reopens a RESOLVED grievance the buyer opened, within the objection period of its grievance SLA policy; afterwards it is deemed closed and an objection is refused (400).
//	@Tags			grievances
//	@Accept			json
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Param			request body ExplanationRequest true "What is still wrong"
//	@Success		200 {object} GrievanceView "Sent."
//	@Failure		400 {object} ErrorResponse "Not resolved, period over, or not the originator."
//	@Failure		404 {object} ErrorResponse "Not the buyer's grievance."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id}/objection [post]
func (h *Handler) HandleObject(c *gin.Context) {
	var req ExplanationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID"})
		return
	}
	v, err := h.svc.Object(c.Request.Context(), buyerOf(c), c.Param("id"), req.Explanation)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// HandleClose closes a grievance the buyer opened.
//
//	@Summary		Close a grievance (CLOSED)
//	@ID				closeGrievance
//	@Description	Closes a grievance the buyer opened. Only the originating participant closes.
//	@Tags			grievances
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Success		200 {object} GrievanceView "Sent."
//	@Failure		400 {object} ErrorResponse "Not the originator, or already closed."
//	@Failure		404 {object} ErrorResponse "Not the buyer's grievance."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id}/close [post]
func (h *Handler) HandleClose(c *gin.Context) {
	v, err := h.svc.Close(c.Request.Context(), buyerOf(c), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// HandleRespond answers a seller's grievance.
//
//	@Summary		Answer a seller's grievance (RESOLUTION_PROPOSED)
//	@ID				respondGrievance
//	@Description	Answers a grievance the seller raised with an explanation (INFORMATION_PROVIDED, no execution), for the seller to accept or reject; once accepted the BAP marks it RESOLVED by itself.
//	@Tags			grievances
//	@Accept			json
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Param			request body RespondRequest true "Answer"
//	@Success		200 {object} GrievanceView "Sent."
//	@Failure		400 {object} ErrorResponse "Not a seller grievance, or the event breaks the rules."
//	@Failure		404 {object} ErrorResponse "Not the buyer's grievance."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id}/response [post]
func (h *Handler) HandleRespond(c *gin.Context) {
	var req RespondRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID"})
		return
	}
	v, err := h.svc.Respond(c.Request.Context(), buyerOf(c), c.Param("id"), req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// HandleOptions returns what the "raise a problem" form needs.
//
//	@Summary		Grievance form options for an order
//	@ID				getGrievanceOptions
//	@Description	The order's lines, returns and refunds (so return and refund grievances link them without the buyer typing an id), its grievance SLA policy in figures, and its grievances still open.
//	@Tags			grievances
//	@Produce		json
//	@Param			id path string true "Transaction id of the order." format(uuid)
//	@Success		200 {object} Options "Options."
//	@Failure		404 {object} ErrorResponse "Not the buyer's order."
//	@Security		BearerAuth
//	@Router			/api/v1/orders/{id}/grievance-options [get]
func (h *Handler) HandleOptions(c *gin.Context) {
	o, err := h.svc.GrievanceOptions(c.Request.Context(), buyerOf(c), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, o)
}

// HandleGoodwill resolves a buyer grievance with a refund the BAP funds.
//
//	@Summary		Goodwill refund funded by the buyer app (UNILATERAL)
//	@ID				goodwillRefund
//	@Description	Operator action: proposes a UNILATERAL refund funded and borne by this BAP (no liability for the seller), executes it through the buyer app's own gateway and marks the grievance RESOLVED. Like the reconciliation routes, this admin route has no login.
//	@Tags			grievances
//	@Accept			json
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Param			request body GoodwillRequest true "Refund"
//	@Success		200 {object} GrievanceView "Resolved."
//	@Failure		400 {object} ErrorResponse "Invalid amount, or the event breaks the rules."
//	@Failure		404 {object} ErrorResponse "Unknown grievance."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security
//	@Router			/api/v1/admin/grievances/{id}/goodwill-refund [post]
func (h *Handler) HandleGoodwill(c *gin.Context) {
	var req GoodwillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID"})
		return
	}
	v, err := h.svc.GoodwillRefund(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// HandleOnSupport answers POST /api/webhook/on_support: a grievance event
// from the BPP. It is checked against the grievance's state before the
// answer, so an event that breaks the rules gets a NACK.
//
//	@Summary		Receive a grievance event from the seller (Beckn on_support)
//	@ID				webhookOnSupport
//	@Description	Delivered by onix-bap's receiver. message.support.supportAttributes is an IONGrievance (ion-specs Grievance/v1): checked against the grievance's earlier events (stale decision, unknown action, allocation rules, missing links or execution references, objection/closure by a non-originator, ...) and stored, or NACKed with the reason. A seller grievance is acknowledged automatically. A plain on_support without supportAttributes is logged and ACKed.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			request body grievance.Message true "Beckn on_support"
//	@Success		200 {object} grievance.Ack "ACK, or NACK with error.message."
//	@Failure		400 {object} grievance.Ack "Body is not JSON (NACK)."
//	@Security
//	@Router			/api/webhook/on_support [post]
func (h *Handler) HandleOnSupport(c *gin.Context) {
	start := time.Now()
	raw, _ := c.GetRawData()
	var msg grievance.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		c.JSON(http.StatusBadRequest, grievance.NewAck(grievance.BecknContext{}, err))
		return
	}
	err := h.svc.Receive(c.Request.Context(), &msg)
	var rule *grievance.RuleError
	if err != nil && !errors.As(err, &rule) {
		h.lh.WithModule("grievancesvc").Err().Error(err).Log("on_support processing failed")
	}
	txnID, _ := uuid.Parse(msg.Context.TransactionID)
	msgID, _ := uuid.Parse(msg.Context.MessageID)
	hdr := grievance.Header{
		TransactionID: txnID, BapID: msg.Context.BapID, BapURI: msg.Context.BapURI,
		BppID: msg.Context.BppID, BppURI: msg.Context.BppURI, NetworkID: msg.Context.NetworkID,
	}
	h.svc.logMessage(c.Request.Context(), hdr, msgID, dbsqlc.BecknActionOnSupport, dbsqlc.MessageDirectionINBOUND,
		"/api/webhook/on_support", raw, nil, err, start)
	c.JSON(http.StatusOK, grievance.NewAck(msg.Context, err))
	if err == nil {
		go h.svc.React(context.Background(), &msg)
	} else {
		h.lh.WithModule("grievancesvc").Warn().Log(fmt.Sprintf("on_support NACK: %v", err))
	}
}
