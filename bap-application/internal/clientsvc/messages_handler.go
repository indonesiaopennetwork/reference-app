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
