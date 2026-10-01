package cancelsvc

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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/cancellation"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/httpclient"
)

type Service struct {
	pool       *pgxpool.Pool
	cfg        *config.Config
	lh         *logharbour.Logger
	httpClient *http.Client
}

func NewService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Service {
	return &Service{pool: pool, cfg: cfg, lh: lh, httpClient: httpclient.New(30 * time.Second)}
}

// Process fully cancels the ACTIVE contract (see internal/cancellation: every
// commitment CLOSED, price consideration CANCELLED, cancellation fee and
// refund settlement added, stock restored) and fires on_cancel to the BAP.
// Partial cancellations arrive as /update instead (see updatesvc).
// Both the inbound cancel and the outbound on_cancel are written to beckn_message_log.
func (s *Service) Process(ctx context.Context, req *CancelRequest) {
	start := time.Now()
	prettyLog(fmt.Sprintf("← IN   CANCEL   | txn=%.8s… | bap=%s", req.Context.TransactionID, req.Context.BapID), req)

	txID, txErr := uuid.Parse(req.Context.TransactionID)
	inboundMsgID, _ := uuid.Parse(req.Context.MessageID)
	reqJSON, _ := json.Marshal(req)

	var res *cancellation.Result
	err := txErr
	if err != nil {
		err = fmt.Errorf("invalid transactionId: %w", err)
	} else if res, err = cancellation.Execute(ctx, s.pool, s.cfg.BppID, txID, nil, s.cancelRequest(req)); err != nil {
		err = fmt.Errorf("cancel failed: %w", err)
	}

	// Log INBOUND regardless of whether the cancellation succeeded.
	ackStatus := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
	if err != nil {
		ackStatus = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
		s.lh.WithModule("cancelsvc").Err().Error(err).Log("cancel failed")
	}
	s.writeMessageLog(ctx, dbsqlc.InsertMessageLogParams{
		TransactionID:        uuidToPgtype(txID, txErr == nil),
		MessageID:            inboundMsgID,
		Action:               dbsqlc.BecknActionCancel,
		Direction:            dbsqlc.MessageDirectionINBOUND,
		Url:                  strPtr("/webhook/cancel"),
		BapID:                strPtr(req.Context.BapID),
		BapUri:               strPtr(req.Context.BapURI),
		BppID:                strPtr(req.Context.BppID),
		BppUri:               strPtr(req.Context.BppURI),
		NetworkID:            strPtr(req.Context.NetworkID),
		AckStatus:            ackStatus,
		RequestPayload:       reqJSON,
		ErrorMessage:         errStrPtr(err),
		ProcessingDurationMs: durationMs(start),
	})
	if err != nil {
		return
	}

	contractJSON, _ := json.Marshal(res.Contract)

	outMsgID := uuid.New()
	onCancelReq := OnCancelRequest{
		Context: BecknContext{
			Version:       req.Context.Version,
			Action:        "on_cancel",
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			MessageID:     outMsgID.String(),
			TransactionID: req.Context.TransactionID,
			BapID:         req.Context.BapID,
			BapURI:        req.Context.BapURI,
			BppID:         req.Context.BppID,
			BppURI:        req.Context.BppURI,
			TTL:           req.Context.TTL,
			NetworkID:     req.Context.NetworkID,
		},
		Message: OnCancelMessage{Contract: contractJSON},
	}

	s.callOnCancel(ctx, txID, outMsgID, req.Context.BapURI, onCancelReq)
}

// cancelRequest reads the reason the BAP declared in the cancel contract's
// contractAttributes.cancellation.
func (s *Service) cancelRequest(req *CancelRequest) cancellation.Request {
	var contract map[string]any
	_ = json.Unmarshal(req.Message.Contract, &contract)
	r := cancellation.RequestFrom(contract)
	r.SettlementTerms = cancellation.SettlementTerms(s.cfg.SettlementBasis, s.cfg.SettlementWindow)
	return r
}

// callOnCancel POSTs on_cancel through the ONIX BPP caller and logs the OUTBOUND entry.
func (s *Service) callOnCancel(ctx context.Context, txID, msgID uuid.UUID, bapURI string, req OnCancelRequest) {
	start := time.Now()
	body, _ := json.Marshal(req)
	url, err := s.resolveOnCancelURL(bapURI)

	var (
		respBody []byte
		callErr  = err
	)
	defer func() {
		ackStatus := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
		if callErr != nil {
			ackStatus = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
		}
		s.writeMessageLog(ctx, dbsqlc.InsertMessageLogParams{
			TransactionID:        uuidToPgtype(txID, true),
			MessageID:            msgID,
			Action:               dbsqlc.BecknActionOnCancel,
			Direction:            dbsqlc.MessageDirectionOUTBOUND,
			Url:                  strPtr(url),
			BapID:                strPtr(req.Context.BapID),
			BapUri:               strPtr(req.Context.BapURI),
			BppID:                strPtr(req.Context.BppID),
			BppUri:               strPtr(req.Context.BppURI),
			NetworkID:            strPtr(req.Context.NetworkID),
			AckStatus:            ackStatus,
			RequestPayload:       body,
			ResponsePayload:      rawMessageOrNull(respBody),
			ErrorMessage:         errStrPtr(callErr),
			ProcessingDurationMs: durationMs(start),
		})
	}()

	if err != nil {
		s.lh.WithModule("cancelsvc").Err().Error(err).Log("on_cancel target resolution failed")
		return
	}
	prettyLog(fmt.Sprintf("→ OUT  ON_CANCEL | txn=%.8s… | POST %s", req.Context.TransactionID, url), req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		callErr = fmt.Errorf("build on_cancel request: %w", err)
		s.lh.WithModule("cancelsvc").Err().Error(callErr).Log("build on_cancel request failed")
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		callErr = fmt.Errorf("on_cancel HTTP call failed: %w", err)
		s.lh.WithModule("cancelsvc").Warn().Error(callErr).Log("on_cancel callback failed")
		return
	}
	defer resp.Body.Close() //nolint:errcheck
	respBody, _ = io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		callErr = fmt.Errorf("BAP returned non-2xx status %d: %s", resp.StatusCode, string(respBody))
		s.lh.WithModule("cancelsvc").Warn().Error(callErr).Log("on_cancel BAP response error")
	}
	prettyLog(fmt.Sprintf("← ACK  ON_CANCEL | http=%d", resp.StatusCode), map[string]interface{}{"response": json.RawMessage(respBody)})
}

// resolveOnCancelURL returns the ONIX BPP caller on_cancel endpoint.
// Priority:
//  1. BPP_CALLER_URL env/config (e.g. https://bpptest.remiges.tech/bpp/caller)
//  2. Derived from BPP_URI by replacing /bpp/receiver with /bpp/caller
func (s *Service) resolveOnCancelURL(_ string) (string, error) {
	base := strings.TrimSpace(s.cfg.BppCallerURL)
	if base == "" {
		base = strings.TrimSpace(s.cfg.BppURI)
		if base == "" {
			return "", fmt.Errorf("cannot resolve on_cancel URL: both BPP_CALLER_URL and BPP_URI are empty")
		}
		if strings.HasSuffix(base, "/") {
			base = strings.TrimSuffix(base, "/")
		}
		if strings.HasSuffix(base, "/bpp/receiver") {
			base = strings.TrimSuffix(base, "/bpp/receiver") + "/bpp/caller"
		} else {
			return "", fmt.Errorf("cannot derive caller URL from BPP_URI %q; set BPP_CALLER_URL", s.cfg.BppURI)
		}
	}
	base = strings.TrimRight(base, "/")
	return base + "/on_cancel", nil
}

func prettyLog(label string, v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("[beckn] %s\n(marshal error: %v)", label, err)
		return
	}
	log.Printf("[beckn] %s\n%s", label, string(b))
}

// writeMessageLog persists a beckn_message_log row; failures are logged, never fatal.
func (s *Service) writeMessageLog(ctx context.Context, params dbsqlc.InsertMessageLogParams) {
	if err := dbsqlc.New(s.pool).InsertMessageLog(ctx, params); err != nil {
		s.lh.WithModule("cancelsvc").Warn().Error(err).Log("failed to write beckn_message_log")
	}
}

// rawMessageOrNull returns nil (SQL NULL) for an empty body so pgx doesn't send
// an empty byte slice to a JSONB column.
func rawMessageOrNull(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func errStrPtr(err error) *string {
	if err == nil {
		return nil
	}
	msg := err.Error()
	return &msg
}

func uuidToPgtype(id uuid.UUID, valid bool) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: valid}
}

func durationMs(start time.Time) *int32 {
	d := int32(time.Since(start).Milliseconds())
	return &d
}
