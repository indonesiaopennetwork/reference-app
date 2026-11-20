// Package dokunotifysvc receives DOKU Checkout's async payment notification
// (POST /api/webhook/doku-notification) and, once verified, drives the real
// Beckn confirm: internal/clientsvc.ClientService.Confirm is only ever
// called from here once a payment is actually PAID, never directly from the
// browser (see internal/dokusvc's package doc and
// internal/clientsvc.ClientService.InitiatePayment).
package dokunotifysvc

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bap/internal/clientsvc"
	"github.com/ion/winroom/bap/internal/config"
)

type Handler struct {
	svc *Service
	lh  *logharbour.Logger
}

func NewHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger, confirmer *clientsvc.ClientService) *Handler {
	return &Handler{svc: NewService(pool, cfg, lh, confirmer), lh: lh}
}

// Handle answers POST /api/webhook/doku-notification.
//
// Unlike this app's Beckn on_* webhooks, this isn't a Beckn ACK/NACK -- DOKU
// doesn't specify a required acknowledgement body -- and verification
// happens BEFORE acknowledging, not after: an unverified POST (wrong
// signature, or not actually from DOKU) gets a 401 and nothing further
// happens, rather than the fast-ack-then-process-async treatment every
// other webhook in this app uses for an already-trusted sender.
//
//	@Summary		Receive a DOKU Checkout payment notification
//	@ID				webhookDokuNotification
//	@Description	Verifies the DOKU HMAC-SHA256 signature (Client-Id/Request-Id/Request-Timestamp/Signature headers) before doing anything else. On success, acknowledges immediately and asynchronously updates the matching payment_sessions row (looked up by order.invoice_number) and, on a successful payment, calls the real Beckn confirm.
//	@Tags			payments
//	@Accept			json
//	@Produce		json
//	@Success		200 "Signature verified; processed asynchronously."
//	@Failure		400 "Body could not be read."
//	@Failure		401 "Signature missing, malformed, or did not verify."
//	@Security
//	@Router			/api/webhook/doku-notification [post]
func (h *Handler) Handle(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if err := h.svc.client.VerifyNotification(c.Request.Header, h.svc.cfg.DokuNotificationPath, body); err != nil {
		h.lh.WithModule("doku_notify_handler").Err().Error(err).Log("doku notification signature verification failed")
		c.Status(http.StatusUnauthorized)
		return
	}
	c.Status(http.StatusOK)
	go func() { h.svc.Process(context.Background(), body) }()
}
