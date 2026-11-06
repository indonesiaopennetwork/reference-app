package grievancesvc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/auth"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/grievance"
)

// Handler serves the seller's grievance API and the support webhook.
type Handler struct {
	svc *Service
	lh  *logharbour.Logger
}

// NewHandler returns a Handler.
func NewHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Handler {
	return &Handler{svc: NewService(pool, cfg, lh), lh: lh}
}

// Register mounts the seller API on dash (behind RequireSeller).
func (h *Handler) Register(dash *gin.RouterGroup) {
	dash.GET("/grievances", h.HandleList)
	dash.GET("/grievances/:id", h.HandleGet)
	dash.POST("/grievances/:id/acknowledge", h.HandleAcknowledge)
	dash.POST("/grievances/:id/proposal", h.HandlePropose)
	dash.POST("/grievances/:id/execute", h.HandleExecute)
	dash.POST("/grievances/:id/resolve", h.HandleResolve)
	dash.POST("/grievances/:id/decision", h.HandleDecide)
	dash.POST("/grievances/:id/objection", h.HandleObject)
	dash.POST("/grievances/:id/close", h.HandleClose)
	dash.GET("/orders/:id/grievances", h.HandleOrderGrievances)
	dash.POST("/orders/:id/grievances", h.HandleRaise)
}

func (h *Handler) fail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "grievance or order not found", "code": "NOT_FOUND"})
	case errors.Is(err, ErrBadRequest):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID"})
	default:
		h.lh.WithModule("grievancesvc").Warn().Error(err).Log("grievance request failed")
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "code": "SEND_FAILED"})
	}
}

// done answers an action with the grievance as it now stands.
func (h *Handler) done(c *gin.Context, id string) {
	d, err := h.svc.Get(c.Request.Context(), auth.ProviderIDFromContext(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

// action wraps a body-less action on a grievance.
func (h *Handler) action(fn func(ctx context.Context, provider, id string) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if err := fn(c.Request.Context(), auth.ProviderIDFromContext(c), id); err != nil {
			h.fail(c, err)
			return
		}
		h.done(c, id)
	}
}

// HandleAcknowledge acknowledge a buyer grievance.
//
//	@Summary		Acknowledge a buyer grievance (ACKNOWLEDGED)
//	@ID				acknowledgeGrievance
//	@Description	Sends the direct on_support callback of the grievance's OPENED (same messageId). Only needed with GRIEVANCE_AUTO_ACKNOWLEDGE=false; otherwise it is sent on arrival.
//	@Tags			grievances
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Success		200 {object} GrievanceDetail "Sent."
//	@Failure		400 {object} ErrorResponse "Already acknowledged, or the event breaks the rules."
//	@Failure		404 {object} ErrorResponse "Not one of the seller's grievances."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id}/acknowledge [post]
func (h *Handler) HandleAcknowledge(c *gin.Context) { h.action(h.svc.Acknowledge)(c) }

// HandleExecute carry out the accepted solution.
//
//	@Summary		Carry out the accepted solution (ACTION_COMPLETED)
//	@ID				executeResolution
//	@Description	Runs each pending action through the order's own flows -- a refund settlement (a linked refund that never arrived is paid out again on its settlement), or a redelivery/replacement performance -- sends the updated contract with an unsolicited on_update, then ACTION_COMPLETED with each action's execution reference. Refused until an EXPLICIT_AGREEMENT version is accepted.
//	@Tags			grievances
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Success		200 {object} GrievanceDetail "Sent."
//	@Failure		400 {object} ErrorResponse "Not accepted yet, or nothing left to carry out."
//	@Failure		404 {object} ErrorResponse "Not one of the seller's grievances."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id}/execute [post]
func (h *Handler) HandleExecute(c *gin.Context) { h.action(h.svc.Execute)(c) }

// HandleResolve mark the grievance resolved.
//
//	@Summary		Mark the grievance resolved (RESOLVED)
//	@ID				resolveGrievance
//	@Description	Sends RESOLVED once every action that moves money or goods has an execution reference (and, for EXPLICIT_AGREEMENT, the version was accepted).
//	@Tags			grievances
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Success		200 {object} GrievanceDetail "Sent."
//	@Failure		400 {object} ErrorResponse "Actions still pending, or not accepted."
//	@Failure		404 {object} ErrorResponse "Not one of the seller's grievances."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id}/resolve [post]
func (h *Handler) HandleResolve(c *gin.Context) { h.action(h.svc.Resolve)(c) }

// HandleClose close a seller grievance.
//
//	@Summary		Close a seller grievance (CLOSED)
//	@ID				closeGrievance
//	@Description	Closes a grievance the seller raised. Only the originating participant closes.
//	@Tags			grievances
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Success		200 {object} GrievanceDetail "Sent."
//	@Failure		400 {object} ErrorResponse "Not the originator, or already closed."
//	@Failure		404 {object} ErrorResponse "Not one of the seller's grievances."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id}/close [post]
func (h *Handler) HandleClose(c *gin.Context) { h.action(h.svc.Close)(c) }

// HandleList lists the seller's grievances.
//
//	@Summary		Grievance inbox
//	@ID				listGrievances
//	@Description	The seller's grievances (buyer and seller raised), most urgent first: waiting on the seller, then by the nearest response/resolution target of the order's grievance SLA policy. needsAction counts the ones waiting on the seller (over all, not just this filter).
//	@Tags			grievances
//	@Produce		json
//	@Param			status query string false "Case status." Enums(OPEN, IN_PROGRESS, RESOLVED, CLOSED)
//	@Param			category query string false "Category, e.g. DAMAGED_ITEM."
//	@Param			only query string false "action: waiting on the seller; sla: a target within 2 hours or past." Enums(action, sla)
//	@Success		200 {object} GrievanceListResponse "Grievances."
//	@Failure		401 {object} auth.ErrorResponse "Bearer token missing or invalid."
//	@Failure		403 {object} auth.NoProviderResponse "No seller profile."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances [get]
func (h *Handler) HandleList(c *gin.Context) {
	items, needs, err := h.svc.List(c.Request.Context(), auth.ProviderIDFromContext(c), ListFilter{
		Status: c.Query("status"), Category: c.Query("category"), Only: c.Query("only"),
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "needsAction": needs})
}

// HandleGet returns a grievance's detail and marks it seen.
//
//	@Summary		Get a grievance
//	@ID				getGrievance
//	@Description	The grievance from the seller's side (next step, active offer, earlier versions, timeline) with the order's lines, returns and refunds and the returns that passed QC (for a refund under the return policy). Marks it seen.
//	@Tags			grievances
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Success		200 {object} GrievanceDetail "Grievance."
//	@Failure		404 {object} ErrorResponse "Not one of the seller's grievances."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id} [get]
func (h *Handler) HandleGet(c *gin.Context) { h.done(c, c.Param("id")) }

// HandlePropose sends a resolution version.
//
//	@Summary		Propose a solution (RESOLUTION_PROPOSED)
//	@ID				proposeResolution
//	@Description	Sends version 1, or the next version after a rejection or objection. route EXPLICIT waits for the buyer's decision; POLICY cites the order's return policy and the QC of an approved return (qcReference) and needs no decision; UNILATERAL is borne by the seller without asking. Refund amounts are capped at what the buyer paid for the lines. A refund on a REFUND_NOT_RECEIVED grievance reuses the linked refund settlement.
//	@Tags			grievances
//	@Accept			json
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Param			request body ProposalRequest true "Solution"
//	@Success		200 {object} GrievanceDetail "Sent."
//	@Failure		400 {object} ErrorResponse "Invalid route, action, amount or QC record, or the event breaks the rules."
//	@Failure		404 {object} ErrorResponse "Not one of the seller's grievances."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id}/proposal [post]
func (h *Handler) HandlePropose(c *gin.Context) {
	var req ProposalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID"})
		return
	}
	id := c.Param("id")
	if err := h.svc.Propose(c.Request.Context(), auth.ProviderIDFromContext(c), id, req); err != nil {
		h.fail(c, err)
		return
	}
	h.done(c, id)
}

// DecideRequest accepts or rejects the buyer's answer.
type DecideRequest struct {
	Accept *bool  `json:"accept" binding:"required"`
	Reason string `json:"reason"`
}

// HandleDecide decides on the buyer's answer to a seller grievance.
//
//	@Summary		Accept or reject the buyer's answer (RESOLUTION_DECIDED)
//	@ID				decideResolution
//	@Description	For a grievance the seller raised: accepts or rejects the latest version of the buyer app's answer as a whole.
//	@Tags			grievances
//	@Accept			json
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Param			request body DecideRequest true "Decision"
//	@Success		200 {object} GrievanceDetail "Sent."
//	@Failure		400 {object} ErrorResponse "Nothing to decide, or the event breaks the rules."
//	@Failure		404 {object} ErrorResponse "Not one of the seller's grievances."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id}/decision [post]
func (h *Handler) HandleDecide(c *gin.Context) {
	var req DecideRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "accept (boolean) is required", "code": "INVALID"})
		return
	}
	id := c.Param("id")
	if err := h.svc.Decide(c.Request.Context(), auth.ProviderIDFromContext(c), id, *req.Accept, req.Reason); err != nil {
		h.fail(c, err)
		return
	}
	h.done(c, id)
}

// ExplanationRequest carries what is still wrong.
type ExplanationRequest struct {
	Explanation string `json:"explanation" binding:"required"`
}

// HandleObject objects to a resolved seller grievance.
//
//	@Summary		Object to a resolved grievance (OBJECTION)
//	@ID				objectGrievance
//	@Description	Reopens a RESOLVED grievance the seller raised, within the objection period of its grievance SLA policy.
//	@Tags			grievances
//	@Accept			json
//	@Produce		json
//	@Param			id path string true "Grievance id."
//	@Param			request body ExplanationRequest true "What is still wrong"
//	@Success		200 {object} GrievanceDetail "Sent."
//	@Failure		400 {object} ErrorResponse "Not resolved, period over, or not the originator."
//	@Failure		404 {object} ErrorResponse "Not one of the seller's grievances."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/grievances/{id}/objection [post]
func (h *Handler) HandleObject(c *gin.Context) {
	var req ExplanationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID"})
		return
	}
	id := c.Param("id")
	if err := h.svc.Object(c.Request.Context(), auth.ProviderIDFromContext(c), id, req.Explanation); err != nil {
		h.fail(c, err)
		return
	}
	h.done(c, id)
}

// HandleOrderGrievances lists an order's grievances.
//
//	@Summary		An order's grievances
//	@ID				listOrderGrievances
//	@Tags			grievances
//	@Produce		json
//	@Param			id path string true "Contract id." format(uuid)
//	@Success		200 {object} GrievanceListResponse "Grievances (needsAction is 0)."
//	@Security		BearerAuth
//	@Router			/api/v1/orders/{id}/grievances [get]
func (h *Handler) HandleOrderGrievances(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id", "code": "INVALID"})
		return
	}
	list, err := h.svc.OrderGrievances(c.Request.Context(), auth.ProviderIDFromContext(c), id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": list, "needsAction": 0})
}

// HandleRaise raises a seller grievance about an order.
//
//	@Summary		Report a problem with an order (seller grievance OPENED)
//	@ID				raiseGrievance
//	@Description	Sends an unsolicited /on_support with an IONGrievance OPENED, raisedBy.side SELLER, against the buyer -- e.g. WRONG_RETURN about a return (returnId links its performance and fills the affected lines).
//	@Tags			grievances
//	@Accept			json
//	@Produce		json
//	@Param			id path string true "Contract id." format(uuid)
//	@Param			request body RaiseRequest true "Grievance"
//	@Success		200 {object} GrievanceDetail "Opened."
//	@Failure		400 {object} ErrorResponse "Invalid category, description, return or lines."
//	@Failure		404 {object} ErrorResponse "Not one of the seller's orders."
//	@Failure		502 {object} ErrorResponse "Not delivered (SEND_FAILED)."
//	@Security		BearerAuth
//	@Router			/api/v1/orders/{id}/grievances [post]
func (h *Handler) HandleRaise(c *gin.Context) {
	contractID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid order id", "code": "INVALID"})
		return
	}
	var req RaiseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID"})
		return
	}
	id, err := h.svc.Raise(c.Request.Context(), auth.ProviderIDFromContext(c), contractID, req)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.done(c, id)
}

// HandleSupport answers POST /api/webhook/support: a grievance event from
// the BAP, checked against the grievance's state before the answer, so an
// event that breaks the rules gets a NACK.
//
//	@Summary		Receive a grievance event from the buyer app (Beckn support)
//	@ID				webhookSupport
//	@Description	Delivered by onix-bpp's receiver. message.support.supportAttributes is an IONGrievance (ion-specs Grievance/v1): checked against the grievance's earlier events (stale decision, proposer deciding its own proposal, allocation rules, missing links, objection/closure by a non-originator, ...) and stored, or NACKed with the reason. A new buyer grievance is acknowledged with its direct on_support callback (same messageId) unless GRIEVANCE_AUTO_ACKNOWLEDGE=false. A plain support without supportAttributes is ACKed and answered with an on_support echoing the order.
//	@Tags			beckn webhooks
//	@Accept			json
//	@Produce		json
//	@Param			request body grievance.Message true "Beckn support"
//	@Success		200 {object} grievance.Ack "ACK, or NACK with error.message."
//	@Failure		400 {object} grievance.Ack "Body is not JSON (NACK)."
//	@Security
//	@Router			/api/webhook/support [post]
func (h *Handler) HandleSupport(c *gin.Context) {
	start := time.Now()
	raw, _ := c.GetRawData()
	var msg grievance.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		c.JSON(http.StatusBadRequest, grievance.NewAck(grievance.BecknContext{}, err))
		return
	}
	isGrievance, err := h.svc.Receive(c.Request.Context(), &msg)
	var rule *grievance.RuleError
	if err != nil && !errors.As(err, &rule) {
		h.lh.WithModule("grievancesvc").Err().Error(err).Log("support processing failed")
	}
	txID, _ := uuid.Parse(msg.Context.TransactionID)
	msgID, _ := uuid.Parse(msg.Context.MessageID)
	hdr := grievance.Header{
		TransactionID: txID, BapID: msg.Context.BapID, BapURI: msg.Context.BapURI,
		BppID: msg.Context.BppID, BppURI: msg.Context.BppURI, NetworkID: msg.Context.NetworkID,
	}
	h.svc.logMessage(c.Request.Context(), hdr, msgID, dbsqlc.BecknActionSupport, dbsqlc.MessageDirectionINBOUND,
		"/api/webhook/support", raw, nil, err, start)
	c.JSON(http.StatusOK, grievance.NewAck(msg.Context, err))
	if err != nil {
		return
	}
	if isGrievance {
		go h.svc.React(context.Background(), &msg)
		return
	}
	go h.svc.answerPlain(context.Background(), hdr, msgID, msg.Message.Support.OrderID)
}

// answerPlain answers a plain Beckn support request (no grievance) with
// its on_support callback: the order, without a made-up ticket id, and no
// contact channels until the BPP has them as Attributes entries.
func (s *Service) answerPlain(ctx context.Context, h grievance.Header, msgID uuid.UUID, orderID string) {
	var msg grievance.Message
	msg.Context = grievance.BecknContext{
		Action: "on_support", Version: "2.0.0", BapID: h.BapID, BapURI: h.BapURI, BppID: h.BppID, BppURI: h.BppURI,
		TransactionID: h.TransactionID.String(), MessageID: msgID.String(),
		Timestamp: s.now().UTC().Format(time.RFC3339), TTL: "PT30S", NetworkID: h.NetworkID,
	}
	msg.Message.Support = grievance.Support{OrderID: orderID}
	if err := s.post(ctx, h, msgID, "on_support", dbsqlc.BecknActionOnSupport, msg); err != nil {
		s.lh.WithModule("grievancesvc").Warn().Error(err).Log("on_support to a plain support failed")
	}
}
