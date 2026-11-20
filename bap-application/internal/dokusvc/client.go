package dokusvc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ion/winroom/bap/internal/httpclient"
)

// prettyLog prints a labelled pretty-JSON block to stdout, mirroring
// internal/clientsvc/service.go's own prettyLog for Beckn calls -- plain
// log.Printf, not the structured *logharbour.Logger also used in this app,
// so the DOKU request/response is easy to read in `docker compose logs`.
func prettyLog(label string, v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("[doku] %s\n(marshal error: %v)", label, err)
		return
	}
	log.Printf("[doku] %s\n%s", label, string(b))
}

// checkoutPath is DOKU Checkout's session-creation endpoint, also used as
// the Request-Target signature component for that call.
const checkoutPath = "/checkout/v1/payment"

// Client calls the DOKU Checkout API and verifies its notification webhook.
// One instance is reused for both, since both use the same Client-Id/Secret
// Key pair and signature algorithm.
type Client struct {
	baseURL   string
	clientID  string
	secretKey string
	http      *http.Client
}

// NewClient builds a Client against baseURL (e.g.
// "https://api-sandbox.doku.com" for sandbox, "https://api.doku.com" for
// production), signing with clientID/secretKey from the DOKU Back Office.
func NewClient(baseURL, clientID, secretKey string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		clientID:  clientID,
		secretKey: secretKey,
		http:      httpclient.New(30 * time.Second),
	}
}

// CheckoutRequest is what this app needs to create a DOKU Checkout session.
// Only the fields DOKU documents as generally required/recommended for a
// VA/QRIS/e-wallet/card order are included -- see DOKU's "Checkout" request
// body docs for the full field set (per-method conditional fields like
// shipping_address for Kredivo/Indodana are not sent, since this app's
// catalog doesn't collect what they'd need).
type CheckoutRequest struct {
	// Amount is the order total in IDR, no decimal places (DOKU requirement).
	Amount int64
	// InvoiceNumber is this BAP's own unique id for the order -- the
	// transaction id. DOKU's async notification reports it back unchanged,
	// which is how internal/dokunotifysvc finds the matching payment_sessions row.
	InvoiceNumber string
	// Currency defaults to IDR when empty.
	Currency string
	// CallbackURL/CallbackURLResult: where DOKU sends the buyer's browser
	// back to once they're done on DOKU's page (see package doc) -- UX
	// only, not a payment-status signal.
	CallbackURL       string
	CallbackURLResult string
	// PaymentDueDateMinutes: how long the session stays open. DOKU defaults
	// to 60 when omitted (0 here).
	PaymentDueDateMinutes int
	// Customer fields are best-effort: DOKU only hard-requires them for
	// specific payment methods (Akulaku/Kredivo/Indodana/Jenius), not sent
	// if unknown.
	CustomerName  string
	CustomerEmail string
	CustomerPhone string
}

// CheckoutResponse is the subset of DOKU's success response this app uses.
type CheckoutResponse struct {
	// PaymentURL is response.payment.url -- redirect the buyer's browser here.
	PaymentURL string
	// TokenID is response.payment.token_id.
	TokenID string
	// ExpiredDate is response.payment.expired_date, format "yyyyMMddHHmmss", UTC+7.
	ExpiredDate string
	// SessionID is response.order.session_id, DOKU-generated.
	SessionID string
}

type checkoutRequestBody struct {
	Order struct {
		Amount            int64  `json:"amount"`
		InvoiceNumber     string `json:"invoice_number"`
		Currency          string `json:"currency,omitempty"`
		CallbackURL       string `json:"callback_url,omitempty"`
		CallbackURLResult string `json:"callback_url_result,omitempty"`
		AutoRedirect      bool   `json:"auto_redirect"`
	} `json:"order"`
	Payment struct {
		PaymentDueDate int `json:"payment_due_date,omitempty"`
	} `json:"payment"`
	Customer *struct {
		Name  string `json:"name,omitempty"`
		Email string `json:"email,omitempty"`
		Phone string `json:"phone,omitempty"`
	} `json:"customer,omitempty"`
}

type checkoutResponseBody struct {
	Response struct {
		Order struct {
			SessionID string `json:"session_id"`
		} `json:"order"`
		Payment struct {
			TokenID     string `json:"token_id"`
			URL         string `json:"url"`
			ExpiredDate string `json:"expired_date"`
		} `json:"payment"`
	} `json:"response"`
	ErrorMessages []string `json:"error_messages"`
}

// CreateCheckout calls POST {baseURL}/checkout/v1/payment to create a DOKU
// Checkout hosted-payment-page session for req, returning the URL to send
// the buyer's browser to.
func (c *Client) CreateCheckout(ctx context.Context, req CheckoutRequest) (*CheckoutResponse, error) {
	currency := req.Currency
	if currency == "" {
		currency = "IDR"
	}

	var body checkoutRequestBody
	body.Order.Amount = req.Amount
	body.Order.InvoiceNumber = req.InvoiceNumber
	body.Order.Currency = currency
	body.Order.CallbackURL = req.CallbackURL
	body.Order.CallbackURLResult = req.CallbackURLResult
	body.Order.AutoRedirect = true
	body.Payment.PaymentDueDate = req.PaymentDueDateMinutes
	if req.CustomerName != "" || req.CustomerEmail != "" || req.CustomerPhone != "" {
		body.Customer = &struct {
			Name  string `json:"name,omitempty"`
			Email string `json:"email,omitempty"`
			Phone string `json:"phone,omitempty"`
		}{Name: req.CustomerName, Email: req.CustomerEmail, Phone: req.CustomerPhone}
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal doku checkout request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+checkoutPath, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range c.signedHeaders(checkoutPath, bodyJSON) {
		httpReq.Header.Set(k, v)
	}

	prettyLog(fmt.Sprintf("→ OUT  CHECKOUT | POST %s", c.baseURL+checkoutPath), body)

	start := time.Now()
	resp, err := c.http.Do(httpReq)
	dur := time.Since(start)
	if err != nil {
		prettyLog(fmt.Sprintf("← ERR  CHECKOUT | dur=%dms | %s", dur.Milliseconds(), err), nil)
		return nil, fmt.Errorf("doku checkout request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, _ := io.ReadAll(resp.Body)
	var respForLog interface{}
	if jsonErr := json.Unmarshal(respBody, &respForLog); jsonErr != nil {
		respForLog = string(respBody)
	}
	prettyLog(fmt.Sprintf("← ACK  CHECKOUT | http=%d | dur=%dms", resp.StatusCode, dur.Milliseconds()), respForLog)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doku checkout returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed checkoutResponseBody
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parse doku checkout response: %w", err)
	}
	if parsed.Response.Payment.URL == "" {
		return nil, fmt.Errorf("doku checkout response missing payment.url: %s", string(respBody))
	}

	return &CheckoutResponse{
		PaymentURL:  parsed.Response.Payment.URL,
		TokenID:     parsed.Response.Payment.TokenID,
		ExpiredDate: parsed.Response.Payment.ExpiredDate,
		SessionID:   parsed.Response.Order.SessionID,
	}, nil
}

// signedHeaders builds the four headers every DOKU server-to-server request
// carries: Client-Id, a fresh Request-Id, Request-Timestamp (UTC, ISO-8601),
// and the HMAC-SHA256 Signature (see signature.go).
func (c *Client) signedHeaders(requestTarget string, body []byte) map[string]string {
	requestID := uuid.New().String()
	requestTimestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	components := signatureComponents(c.clientID, requestID, requestTimestamp, requestTarget, digest(body))
	return map[string]string{
		"Client-Id":         c.clientID,
		"Request-Id":        requestID,
		"Request-Timestamp": requestTimestamp,
		"Signature":         sign(c.secretKey, components),
	}
}

// MethodFor maps a DOKU payment_method_types/service.id value (e.g.
// "VIRTUAL_ACCOUNT_BCA", "CREDIT_CARD", "QRIS", "EMONEY_OVO") onto this
// app's settlement method vocabulary (internal/settlement's IONPayment
// "method" enum: QRIS, VIRTUAL_ACCOUNT, EWALLET, BANK_TRANSFER, CREDIT_CARD,
// DEBIT_CARD, DIRECT_DEBIT, BNPL, ...). DOKU's convenience-store methods
// (Alfamart/Indomaret) have no close IONPayment equivalent and fall back to
// EWALLET, same as any other value this mapping doesn't recognize.
func MethodFor(serviceID string) string {
	switch {
	case strings.HasPrefix(serviceID, "VIRTUAL_ACCOUNT"):
		return "VIRTUAL_ACCOUNT"
	case serviceID == "QRIS":
		return "QRIS"
	case serviceID == "CREDIT_CARD", serviceID == "KARTU_KREDIT_INDONESIA":
		return "CREDIT_CARD"
	case serviceID == "GOOGLE_PAY":
		return "EWALLET"
	case strings.HasPrefix(serviceID, "EMONEY_"):
		return "EWALLET"
	case strings.HasPrefix(serviceID, "PEER_TO_PEER_"):
		return "BNPL"
	case strings.HasPrefix(serviceID, "DIRECT_DEBIT"):
		return "DIRECT_DEBIT"
	default:
		return "EWALLET"
	}
}
