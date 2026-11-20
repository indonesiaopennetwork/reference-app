package dokusvc

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// NotificationPayload is the subset of DOKU's async payment notification
// this app reads. The full body (every payment-method-specific field) is
// stored verbatim by internal/dokunotifysvc regardless.
type NotificationPayload struct {
	Order struct {
		InvoiceNumber string `json:"invoice_number"`
	} `json:"order"`
	Transaction struct {
		// Status is "SUCCESS" for a completed payment; methods also report
		// "FAILED", and some (e.g. credit card) "VOIDED"/"REFUNDED" for
		// later events on the same transaction.
		Status string `json:"status"`
		// Date is DOKU's own transaction timestamp (UTC, ISO-8601),
		// recorded as this payment's paidAt rather than our receipt time.
		Date string `json:"date"`
		// OriginalRequestID identifies this transaction at DOKU; recorded
		// as the settlement's gatewayRef.
		OriginalRequestID string `json:"original_request_id"`
	} `json:"transaction"`
	Service struct {
		// ID is the payment method actually used, e.g.
		// "VIRTUAL_ACCOUNT_BCA", "CREDIT_CARD", "QRIS", "EMONEY_OVO" -- see
		// MethodFor.
		ID string `json:"id"`
	} `json:"service"`
}

// ParseNotification decodes a DOKU notification body enough to route and
// record it. A missing invoice_number means this isn't a notification this
// app's checkout sessions can produce (invoice_number is always set when
// creating a session) -- that's rejected as an error rather than silently
// ignored.
func ParseNotification(body []byte) (*NotificationPayload, error) {
	var p NotificationPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parse doku notification: %w", err)
	}
	if p.Order.InvoiceNumber == "" {
		return nil, fmt.Errorf("doku notification missing order.invoice_number")
	}
	return &p, nil
}

// VerifyNotification checks a DOKU notification's Signature header against
// its body, using this Client's own secret key and the same algorithm used
// to sign outbound requests (signature.go). notificationPath is the path
// DOKU was configured to POST this notification to (the Request-Target
// component for an inbound call, per DOKU's signature docs) -- this app's
// own DOKU_NOTIFICATION_PATH config value, not anything read from the
// request itself.
func (c *Client) VerifyNotification(header http.Header, notificationPath string, body []byte) error {
	clientID := header.Get("Client-Id")
	requestID := header.Get("Request-Id")
	requestTimestamp := header.Get("Request-Timestamp")
	signature := header.Get("Signature")
	if clientID == "" || requestID == "" || requestTimestamp == "" || signature == "" {
		return fmt.Errorf("doku notification missing required signature headers")
	}
	if clientID != c.clientID {
		return fmt.Errorf("doku notification Client-Id %q does not match configured client", clientID)
	}
	components := signatureComponents(clientID, requestID, requestTimestamp, notificationPath, digest(body))
	if !verify(c.secretKey, components, signature) {
		return fmt.Errorf("doku notification signature mismatch")
	}
	return nil
}
