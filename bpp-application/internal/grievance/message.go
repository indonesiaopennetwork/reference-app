package grievance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// BecknContext is the context of a /support or /on_support message.
type BecknContext struct {
	Domain        string   `json:"domain,omitempty"`
	Action        string   `json:"action"`
	Version       string   `json:"version"`
	BapID         string   `json:"bapId"`
	BapURI        string   `json:"bapUri"`
	BppID         string   `json:"bppId"`
	BppURI        string   `json:"bppUri"`
	TransactionID string   `json:"transactionId"`
	MessageID     string   `json:"messageId"`
	Timestamp     string   `json:"timestamp"`
	TTL           string   `json:"ttl,omitempty"`
	NetworkID     string   `json:"networkId,omitempty"`
	SchemaContext []string `json:"schemaContext,omitempty"`
}

// Message is a /support or /on_support request.
type Message struct {
	Context BecknContext `json:"context"`
	Message struct {
		Support Support `json:"support"`
	} `json:"message"`
}

// NewMessage builds a grievance message for event g on the order in h.
// name is the descriptor name shown with the grievance (its short title).
func NewMessage(action string, h Header, msgID string, g *Grievance, name string, channels []json.RawMessage, now time.Time) (*Message, error) {
	attrs, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	m := &Message{Context: BecknContext{
		Action: action, Version: "2.0.0",
		BapID: h.BapID, BapURI: h.BapURI, BppID: h.BppID, BppURI: h.BppURI,
		TransactionID: h.TransactionID.String(), MessageID: msgID,
		Timestamp: now.UTC().Format(time.RFC3339), TTL: "PT30S", NetworkID: h.NetworkID,
		SchemaContext: []string{Context},
	}}
	m.Message.Support = Support{
		OrderID:           h.OrderID,
		Descriptor:        &Descriptor{Code: DescriptorCode, Name: name},
		Channels:          channels,
		SupportAttributes: attrs,
	}
	return m, nil
}

// Ack is the synchronous answer to a Beckn request.
type Ack struct {
	Context BecknContext `json:"context"`
	Message struct {
		Ack struct {
			Status string `json:"status"`
		} `json:"ack"`
	} `json:"message"`
	Error *AckError `json:"error,omitempty"`
}

// AckError says why a request was NACKed.
type AckError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewAck answers a request: ACK, or NACK with err's message.
func NewAck(ctx BecknContext, err error) Ack {
	var a Ack
	a.Context = ctx
	a.Message.Ack.Status = "ACK"
	if err != nil {
		a.Message.Ack.Status = "NACK"
		a.Error = &AckError{Code: "GRIEVANCE_EVENT_REJECTED", Message: err.Error()}
	}
	return a
}

// Post sends body to url and reads the answer. A non-200 status or a NACK
// body is an error; a NACK wraps ErrNacked. The response body is returned
// either way, for the message log.
func Post(ctx context.Context, client *http.Client, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	respBody, _ := io.ReadAll(resp.Body)
	var ack Ack
	_ = json.Unmarshal(respBody, &ack)
	if ack.Message.Ack.Status == "NACK" {
		reason := string(respBody)
		if ack.Error != nil && ack.Error.Message != "" {
			reason = ack.Error.Message
		}
		return respBody, fmt.Errorf("%w: %s", ErrNacked, reason)
	}
	if resp.StatusCode != http.StatusOK {
		return respBody, fmt.Errorf("adapter returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}
