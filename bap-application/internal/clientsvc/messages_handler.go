package clientsvc

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ion/winroom/bap/internal/auth"
)

type messageLogRow struct {
	ID              string          `json:"id"`
	MessageID       string          `json:"message_id"`
	Action          string          `json:"action"`
	Direction       string          `json:"direction"`
	AckStatus       *string         `json:"ack_status"`
	TransactionID   *string         `json:"transaction_id"`
	URL             *string         `json:"url"`
	ErrorMessage    *string         `json:"error_message"`
	RequestPayload  json.RawMessage `json:"request_payload"`
	ResponsePayload json.RawMessage `json:"response_payload"`
	DurationMs      *int32          `json:"processing_duration_ms"`
	CreatedAt       time.Time       `json:"created_at"`
}

// HandleListMessages returns the logged-in buyer's Beckn messages, newest
// first: their searches (discover / on_discover, see discover_requests) and
// every message of their orders (transactions.buyer_subject). Other buyers'
// messages -- which carry their names, phones and addresses -- never appear.
// GET /api/v1/messages?limit=50&action=discover
//
//	@Summary		The buyer's Beckn message log
//	@ID				listMessages
//	@Description	The logged-in buyer's Beckn messages from beckn_message_log, newest first: their discover searches and every message of their orders. The bearer token is optional for the /api/v1 group, but this route needs a buyer identity: with no token it answers 401.
//	@Tags			messages
//	@Produce		json
//	@Param			limit query int false "Max rows, 1-200. Out-of-range or non-numeric values fall back to 50." default(50) minimum(1) maximum(200)
//	@Param			action query string false "Only messages with this Beckn action (exact match). Empty means all." Enums(discover, on_discover, select, on_select, init, on_init, confirm, on_confirm, status, on_status, track, on_track, update, on_update, cancel, on_cancel, rate, on_rate, support, on_support, reconcile, on_reconcile)
//	@Success		200 {object} MessageListResponse "Messages."
//	@Failure		401 {object} ErrorResponse "No buyer identity (no token: log in to see your messages), or an invalid/expired token."
//	@Failure		500 {object} ErrorResponse "Transaction or snapshot not found, database error, or the adapter call failed / returned non-200 (the error text says which)."
//	@Security		BearerAuth
//	@Router			/api/v1/messages [get]
func (h *ClientHandler) HandleListMessages(c *gin.Context) {
	buyerSubject, ok := auth.SubjectFromContext(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "log in to see your messages"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	rows, err := h.svc.pool.Query(c.Request.Context(), `
		SELECT m.id::text, m.message_id::text, m.action::text, m.direction::text, m.ack_status::text,
		       m.transaction_id::text, m.url, m.error_message, m.request_payload, m.response_payload,
		       m.processing_duration_ms, m.created_at
		  FROM beckn_message_log m
		 WHERE m.bap_id = $1
		   AND (m.transaction_id IN (SELECT transaction_id FROM transactions WHERE buyer_subject = $2)
		     OR m.transaction_id IN (SELECT transaction_id FROM discover_requests WHERE buyer_subject = $2))
		   AND ($3 = '' OR m.action::text = $3)
		 ORDER BY m.created_at DESC
		 LIMIT $4`, h.svc.cfg.BapID, buyerSubject, c.Query("action"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []messageLogRow{}
	for rows.Next() {
		var m messageLogRow
		var created pgtype.Timestamptz
		if err := rows.Scan(&m.ID, &m.MessageID, &m.Action, &m.Direction, &m.AckStatus, &m.TransactionID, &m.URL,
			&m.ErrorMessage, &m.RequestPayload, &m.ResponsePayload, &m.DurationMs, &created); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		m.CreatedAt = created.Time
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
