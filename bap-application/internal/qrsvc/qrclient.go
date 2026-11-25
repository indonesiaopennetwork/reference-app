package qrsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var (
	errPacketNotFound    = errors.New("packet not found")
	errQRServiceDown     = errors.New("qr-service unavailable")
	errQRNotConfigured   = errors.New("QR_SERVICE_URL is not set")
	maxQRServiceResponse = int64(4 << 20)
)

// qrClient calls ion-qr-code's qr-service.
type qrClient struct {
	baseURL    string
	serviceKey string
	http       *http.Client
}

func newQRClient(baseURL, serviceKey string) *qrClient {
	return &qrClient{baseURL: baseURL, serviceKey: serviceKey, http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *qrClient) configured() bool { return c.baseURL != "" }

// envelope is qr-service's alya response envelope.
type envelope struct {
	Status   string          `json:"status"`
	Data     json.RawMessage `json:"data"`
	Messages []struct {
		ErrCode string   `json:"errcode"`
		Field   string   `json:"field"`
		Vals    []string `json:"vals"`
	} `json:"messages"`
}

func (e envelope) errcode() string {
	if len(e.Messages) == 0 {
		return ""
	}
	return e.Messages[0].ErrCode
}

// getPacket reads a packet (any status) with this BAP's service key.
func (c *qrClient) getPacket(ctx context.Context, id string) (*Packet, error) {
	if !c.configured() {
		return nil, errQRNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/packets/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Service-Key", c.serviceKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errQRServiceDown, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var env envelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxQRServiceResponse)).Decode(&env); err != nil {
		return nil, fmt.Errorf("%w: HTTP %d, unreadable body", errQRServiceDown, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound || env.errcode() == "PACKET_NOT_FOUND" {
		return nil, errPacketNotFound
	}
	if resp.StatusCode != http.StatusOK || env.Status != "success" {
		return nil, fmt.Errorf("%w: HTTP %d %s", errQRServiceDown, resp.StatusCode, env.errcode())
	}
	var p Packet
	if err := json.Unmarshal(env.Data, &p); err != nil {
		return nil, fmt.Errorf("%w: decode packet: %v", errQRServiceDown, err)
	}
	return &p, nil
}

// recordScan records a DIRECT_RESOLVE scan (idempotent on clientScanKey)
// and returns its id; nil when qr-service ignored it (bot User-Agent).
func (c *qrClient) recordScan(ctx context.Context, packetID, clientScanKey, userAgent, clientIP string) (*string, error) {
	body, _ := json.Marshal(map[string]any{"data": map[string]any{
		"packet_id":       packetID,
		"scan_source":     "DIRECT_RESOLVE",
		"client_scan_key": clientScanKey,
	}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/scans", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if clientIP != "" {
		req.Header.Set("X-Forwarded-For", clientIP)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	var env envelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxQRServiceResponse)).Decode(&env); err != nil {
		return nil, fmt.Errorf("record scan: HTTP %d", resp.StatusCode)
	}
	if env.Status != "success" {
		return nil, fmt.Errorf("record scan: HTTP %d %s", resp.StatusCode, env.errcode())
	}
	var out struct {
		ScanID *string `json:"scan_id"`
	}
	_ = json.Unmarshal(env.Data, &out)
	return out.ScanID, nil
}
