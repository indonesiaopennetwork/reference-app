package qrsvc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/ion/winroom/bap/internal/clientsvc"
)

// After confirm, a QR order is an ordinary Beckn order: the buyer page asks
// the seller for its status, cancels, rates and raises support with the
// Beckn status / cancel / rate / support actions, through onix-bap like the
// shop does. The seller's on_* answers arrive on the BAP's webhooks and
// update the order (GET /api/v1/qr/orders/:id reads them back).
//
// Demo orders (see demo.go) answer locally: no Beckn message goes out.

// CancelRequest is POST /api/v1/qr/orders/:id/cancel.
type CancelRequest struct {
	ReasonCode string `json:"reason_code" binding:"required" example:"CHANGE_OF_MIND"`
}

// RateRequest is POST /api/v1/qr/orders/:id/rate.
type RateRequest struct {
	Rating   int    `json:"rating" binding:"required,min=1,max=5"`
	Feedback string `json:"feedback,omitempty"`
}

// SupportRequest is POST /api/v1/qr/orders/:id/support.
type SupportRequest struct {
	Description string `json:"description" binding:"required,max=500"`
}

// BecknMessage is one Beckn message of an order, as the BAP logged it.
type BecknMessage struct {
	Action     string    `json:"action" example:"on_select"`
	Direction  string    `json:"direction" enums:"OUTBOUND,INBOUND"`
	AckStatus  string    `json:"ack_status,omitempty" enums:"ACK,NACK"`
	Error      string    `json:"error,omitempty"`
	DurationMs *int32    `json:"duration_ms,omitempty"`
	At         time.Time `json:"at"`
}

// placedOrder loads an order the guest token owns that has been sent to the
// seller (confirm sent or later).
func (s *Service) placedOrder(ctx context.Context, txnID, guestToken string) (*storedOrder, error) {
	o, err := s.authorizedOrder(ctx, txnID, guestToken)
	if err != nil {
		return nil, err
	}
	if o.state != StateConfirmSent && o.state != StateConfirmed {
		return nil, newErr(409, "INVALID_STATE", "This order hasn't been placed with the merchant yet")
	}
	return o, nil
}

// becknErr turns a failed Beckn send into an API error.
func (s *Service) becknErr(action string, err error) error {
	s.log().Warn().Error(err).Log("QR " + action + " failed")
	return newErr(502, "BECKN_FAILED", "The merchant couldn't be reached, try again")
}

// RefreshStatus sends Beckn status; the seller's on_status updates the order.
func (s *Service) RefreshStatus(ctx context.Context, txnID, guestToken string) (*OrderView, error) {
	o, err := s.placedOrder(ctx, txnID, guestToken)
	if err != nil {
		return nil, err
	}
	if o.demoScenario == "" {
		if err := s.beckn.RequestStatus(ctx, &clientsvc.ClientRequestStatusRequest{TransactionID: txnID}); err != nil {
			return nil, s.becknErr("status", err)
		}
	}
	return s.Order(ctx, txnID, guestToken)
}

// Cancel sends Beckn cancel for every item still active, with the buyer's
// reason. The seller's cancellation policy decides (on_cancel).
func (s *Service) Cancel(ctx context.Context, txnID, guestToken string, req CancelRequest) (*OrderView, error) {
	o, err := s.placedOrder(ctx, txnID, guestToken)
	if err != nil {
		return nil, err
	}
	if o.demoScenario != "" {
		_, _ = s.pool.Exec(ctx, `UPDATE transactions SET status = 'CANCELLED', updated_at = now() WHERE transaction_id = $1`, o.txnID)
		return s.Order(ctx, txnID, guestToken)
	}
	if err := s.beckn.Cancel(ctx, &clientsvc.ClientCancelRequest{TransactionID: txnID, ReasonCode: req.ReasonCode}); err != nil {
		// The seller's cancellation policy is checked before anything is sent.
		if errors.Is(err, clientsvc.ErrCancelNotAllowed) {
			return nil, newErr(409, "CANCEL_NOT_ALLOWED", err.Error())
		}
		return nil, s.becknErr("cancel", err)
	}
	return s.Order(ctx, txnID, guestToken)
}

// Rate sends Beckn rate for the order (1 to 5, optional comment).
func (s *Service) Rate(ctx context.Context, txnID, guestToken string, req RateRequest) (*OrderView, error) {
	o, err := s.placedOrder(ctx, txnID, guestToken)
	if err != nil {
		return nil, err
	}
	if o.demoScenario == "" {
		rangeJSON, _ := json.Marshal(map[string]int{"min": 1, "max": 5, "value": req.Rating})
		in := clientsvc.RatingInput{ID: txnID, RatingCategory: "order", Range: rangeJSON}
		if fb := strings.TrimSpace(req.Feedback); fb != "" {
			in.FeedbackFormSubmission, _ = json.Marshal(map[string]any{"data": map[string]string{"comment": fb}})
		}
		if err := s.beckn.Rate(ctx, &clientsvc.ClientRateRequest{TransactionID: txnID, RatingInputs: []clientsvc.RatingInput{in}}); err != nil {
			return nil, s.becknErr("rate", err)
		}
	}
	return s.Order(ctx, txnID, guestToken)
}

// Support sends Beckn support with the buyer's description.
func (s *Service) Support(ctx context.Context, txnID, guestToken string, req SupportRequest) (*OrderView, error) {
	o, err := s.placedOrder(ctx, txnID, guestToken)
	if err != nil {
		return nil, err
	}
	if o.demoScenario == "" {
		if err := s.beckn.Support(ctx, &clientsvc.ClientSupportRequest{TransactionID: txnID, Description: strings.TrimSpace(req.Description)}); err != nil {
			return nil, s.becknErr("support", err)
		}
	}
	return s.Order(ctx, txnID, guestToken)
}

// BecknMessages lists an order's Beckn messages, oldest first.
func (s *Service) BecknMessages(ctx context.Context, txnID string) ([]BecknMessage, error) {
	id, err := uuid.Parse(txnID)
	if err != nil {
		return nil, newErr(400, "INVALID_TRANSACTION", "transaction id must be a UUID")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT action::text, direction::text, COALESCE(ack_status::text, ''), COALESCE(error_message, ''),
		       processing_duration_ms, created_at
		  FROM beckn_message_log WHERE transaction_id = $1
		 ORDER BY created_at LIMIT 200`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BecknMessage{}
	for rows.Next() {
		var m BecknMessage
		if err := rows.Scan(&m.Action, &m.Direction, &m.AckStatus, &m.Error, &m.DurationMs, &m.At); err != nil {
			return nil, err
		}
		if len(m.Error) > 300 {
			m.Error = m.Error[:300] + "…"
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// handlers
// ---------------------------------------------------------------------------

func (h *Handler) orderAction(c *gin.Context, fn func(ctx context.Context, id, token string) (*OrderView, error)) {
	resp, err := fn(c.Request.Context(), c.Param("id"), guestToken(c))
	if err != nil {
		fail(c, h.svc, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// HandleRefreshStatus asks the seller for the order's status.
//
//	@Summary		Ask the seller for a QR order's status (Beckn status)
//	@ID				qrOrderStatus
//	@Tags			qr
//	@Produce		json
//	@Param			X-QR-Guest-Token	header		string	true	"Guest token from resolve"
//	@Param			id					path		string	true	"Transaction id"
//	@Success		200					{object}	OrderView
//	@Router			/api/v1/qr/orders/{id}/status [post]
func (h *Handler) HandleRefreshStatus(c *gin.Context) {
	h.orderAction(c, h.svc.RefreshStatus)
}

// HandleCancel cancels a QR order.
//
//	@Summary		Cancel a QR order (Beckn cancel)
//	@ID				qrOrderCancel
//	@Tags			qr
//	@Accept			json
//	@Produce		json
//	@Param			X-QR-Guest-Token	header		string			true	"Guest token from resolve"
//	@Param			id					path		string			true	"Transaction id"
//	@Param			request				body		CancelRequest	true	"Request body"
//	@Success		200					{object}	OrderView
//	@Failure		409					{object}	ErrorResponse	"CANCEL_NOT_ALLOWED / INVALID_STATE"
//	@Router			/api/v1/qr/orders/{id}/cancel [post]
func (h *Handler) HandleCancel(c *gin.Context) {
	var req CancelRequest
	if !bind(c, &req) {
		return
	}
	h.orderAction(c, func(ctx context.Context, id, token string) (*OrderView, error) {
		return h.svc.Cancel(ctx, id, token, req)
	})
}

// HandleRate rates a QR order.
//
//	@Summary		Rate a QR order (Beckn rate)
//	@ID				qrOrderRate
//	@Tags			qr
//	@Accept			json
//	@Produce		json
//	@Param			X-QR-Guest-Token	header		string		true	"Guest token from resolve"
//	@Param			id					path		string		true	"Transaction id"
//	@Param			request				body		RateRequest	true	"Request body"
//	@Success		200					{object}	OrderView
//	@Router			/api/v1/qr/orders/{id}/rate [post]
func (h *Handler) HandleRate(c *gin.Context) {
	var req RateRequest
	if !bind(c, &req) {
		return
	}
	h.orderAction(c, func(ctx context.Context, id, token string) (*OrderView, error) {
		return h.svc.Rate(ctx, id, token, req)
	})
}

// HandleSupport raises a support request for a QR order.
//
//	@Summary		Ask the seller for help with a QR order (Beckn support)
//	@ID				qrOrderSupport
//	@Tags			qr
//	@Accept			json
//	@Produce		json
//	@Param			X-QR-Guest-Token	header		string			true	"Guest token from resolve"
//	@Param			id					path		string			true	"Transaction id"
//	@Param			request				body		SupportRequest	true	"Request body"
//	@Success		200					{object}	OrderView
//	@Router			/api/v1/qr/orders/{id}/support [post]
func (h *Handler) HandleSupport(c *gin.Context) {
	var req SupportRequest
	if !bind(c, &req) {
		return
	}
	h.orderAction(c, func(ctx context.Context, id, token string) (*OrderView, error) {
		return h.svc.Support(ctx, id, token, req)
	})
}

// HandleBecknMessages lists an order's Beckn messages for the console.
//
//	@Summary		A QR order's Beckn messages
//	@ID				qrOrderBecknMessages
//	@Description	Every Beckn message the BAP logged for the order (select, on_select, init, …), oldest first. Login required.
//	@Tags			qr
//	@Produce		json
//	@Param			id	path		string	true	"Transaction id"
//	@Success		200	{array}		BecknMessage
//	@Security		BearerAuth
//	@Router			/api/v1/qr/orders/{id}/beckn [get]
func (h *Handler) HandleBecknMessages(c *gin.Context) {
	if subject(c) == nil {
		fail(c, h.svc, newErr(401, "LOGIN_REQUIRED", "Log in to see Beckn messages"))
		return
	}
	msgs, err := h.svc.BecknMessages(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, h.svc, err)
		return
	}
	c.JSON(http.StatusOK, msgs)
}
