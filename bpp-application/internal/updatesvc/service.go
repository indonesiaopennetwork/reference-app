package updatesvc

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

// Process handles /update and fires on_update to the BAP.
//
// An update that lowers an active commitment's quantity or marks it CLOSED is a
// partial cancellation (full cancellations use /cancel): it goes through
// internal/cancellation, and on_update carries the resulting contract. Any
// other update -- e.g. a return request, which adds a RETURN performance -- is
// stored as the contract's latest state and echoed back as before.
// Both the inbound update and the outbound on_update are written to beckn_message_log.
func (s *Service) Process(ctx context.Context, req *UpdateRequest) {
	start := time.Now()
	prettyLog(fmt.Sprintf("← IN   UPDATE   | txn=%.8s… | bap=%s", req.Context.TransactionID, req.Context.BapID), req)

	txID, txErr := uuid.Parse(req.Context.TransactionID)
	inboundMsgID, _ := uuid.Parse(req.Context.MessageID)
	reqJSON, _ := json.Marshal(req)

	updatedContract, err := s.apply(ctx, txID, txErr, req)

	ackStatus := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
	if err != nil {
		ackStatus = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
		s.lh.WithModule("updatesvc").Err().Error(err).Log("update failed")
	}
	s.writeMessageLog(ctx, dbsqlc.InsertMessageLogParams{
		TransactionID:        uuidToPgtype(txID, txErr == nil),
		MessageID:            inboundMsgID,
		Action:               dbsqlc.BecknActionUpdate,
		Direction:            dbsqlc.MessageDirectionINBOUND,
		Url:                  strPtr("/webhook/update"),
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

	outMsgID := uuid.New()
	onUpdateReq := OnUpdateRequest{
		Context: BecknContext{
			Version:       req.Context.Version,
			Action:        "on_update",
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
		Message: OnUpdateMessage{Contract: updatedContract},
	}

	s.callOnUpdate(ctx, txID, outMsgID, req.Context.BapURI, onUpdateReq)
}

// apply performs the update and returns the contract to send in on_update.
func (s *Service) apply(ctx context.Context, txID uuid.UUID, txErr error, req *UpdateRequest) (json.RawMessage, error) {
	if txErr != nil {
		return nil, fmt.Errorf("invalid transactionId: %w", txErr)
	}
	q := dbsqlc.New(s.pool)
	row, err := q.GetActiveContractByTxnID(ctx, dbsqlc.GetActiveContractByTxnIDParams{
		TransactionID: txID,
		BppID:         s.cfg.BppID,
	})
	if err != nil {
		return nil, fmt.Errorf("contract not found for update: %w", err)
	}

	var requested map[string]any
	_ = json.Unmarshal(req.Message.Contract, &requested)
	if requested != nil {
		stored, err := cancellation.LatestContract(ctx, s.pool, txID, row.ContractAttributes)
		if err != nil {
			return nil, err
		}
		if items := cancellation.ItemsFromUpdate(stored, requested); len(items) > 0 {
			res, err := cancellation.Execute(ctx, s.pool, s.cfg.BppID, txID, items, cancellation.RequestFrom(requested))
			if err != nil {
				return nil, fmt.Errorf("partial cancellation failed: %w", err)
			}
			return json.Marshal(res.Contract)
		}
	}

	// Not a cancellation: persist the update payload as the contract's latest state.
	if req.Message.Contract != nil {
		if _, err := s.pool.Exec(ctx,
			`UPDATE contracts SET contract_attributes = $1, updated_at = NOW()
			 WHERE id = $2 AND bpp_id = $3`,
			[]byte(req.Message.Contract), row.ID, s.cfg.BppID); err != nil {
			return nil, fmt.Errorf("update contract attributes failed: %w", err)
		}
		return req.Message.Contract, nil
	}
	return json.RawMessage(`{"id":"` + row.ID.String() + `"}`), nil
}

// callOnUpdate POSTs on_update through the ONIX BPP caller and logs the OUTBOUND entry.
func (s *Service) callOnUpdate(ctx context.Context, txID, msgID uuid.UUID, bapURI string, req OnUpdateRequest) {
	start := time.Now()
	body, _ := json.Marshal(req)
	url, err := s.resolveOnUpdateURL(bapURI)

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
			Action:               dbsqlc.BecknActionOnUpdate,
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
		s.lh.WithModule("updatesvc").Err().Error(err).Log("on_update target resolution failed")
		return
	}
	prettyLog(fmt.Sprintf("→ OUT  ON_UPDATE | txn=%.8s… | POST %s", req.Context.TransactionID, url), req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		callErr = fmt.Errorf("build on_update request: %w", err)
		s.lh.WithModule("updatesvc").Err().Error(callErr).Log("build on_update request failed")
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		callErr = fmt.Errorf("on_update HTTP call failed: %w", err)
		s.lh.WithModule("updatesvc").Warn().Error(callErr).Log("on_update callback failed")
		return
	}
	defer resp.Body.Close() //nolint:errcheck
	respBody, _ = io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		callErr = fmt.Errorf("BAP returned non-2xx status %d: %s", resp.StatusCode, string(respBody))
		s.lh.WithModule("updatesvc").Warn().Error(callErr).Log("on_update BAP response error")
	}
	prettyLog(fmt.Sprintf("← ACK  ON_UPDATE | http=%d", resp.StatusCode), map[string]interface{}{"response": json.RawMessage(respBody)})
}

// resolveOnUpdateURL returns the ONIX BPP caller on_update endpoint.
// Priority:
//  1. BPP_CALLER_URL env/config (e.g. https://bpptest.remiges.tech/bpp/caller)
//  2. Derived from BPP_URI by replacing /bpp/receiver with /bpp/caller
func (s *Service) resolveOnUpdateURL(_ string) (string, error) {
	base := strings.TrimSpace(s.cfg.BppCallerURL)
	if base == "" {
		base = strings.TrimSpace(s.cfg.BppURI)
		if base == "" {
			return "", fmt.Errorf("cannot resolve on_update URL: both BPP_CALLER_URL and BPP_URI are empty")
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
	return base + "/on_update", nil
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
		s.lh.WithModule("updatesvc").Warn().Error(err).Log("failed to write beckn_message_log")
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
