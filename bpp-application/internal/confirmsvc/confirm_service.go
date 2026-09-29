package confirmsvc

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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/httpclient"
)

// ConfirmService handles the full /beckn/confirm flow:
//  1. Transition the contract from DRAFT → ACTIVE in the database and record
//     the BAP's settlements (payment proof).
//  2. Build the on_confirm payload echoing back the confirmed contract, with
//     its settlements marked COMPLETE.
//  3. POST the on_confirm callback to the BAP.
//  4. Write INBOUND and OUTBOUND audit rows to beckn_message_log.
type ConfirmService struct {
	pool       *pgxpool.Pool
	cfg        *config.Config
	lh         *logharbour.Logger
	httpClient *http.Client
}

func NewConfirmService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *ConfirmService {
	return &ConfirmService{
		pool:       pool,
		cfg:        cfg,
		lh:         lh,
		httpClient: httpclient.New(30 * time.Second),
	}
}

// ProcessConfirm is called asynchronously after the BPP returns ACK to the BAP.
// It confirms the contract state, calls on_confirm on the BAP, and logs both messages.
func (s *ConfirmService) ProcessConfirm(ctx context.Context, req *ConfirmRequest) {
	start := time.Now()

	txID, _ := uuid.Parse(req.Context.TransactionID)
	inboundMsgID, _ := uuid.Parse(req.Context.MessageID)
	reqJSON, _ := json.Marshal(req)

	prettyLog(fmt.Sprintf("← IN   CONFIRM   | txn=%.8s… | bap=%s", req.Context.TransactionID, req.Context.BapID), req)

	// Transition DRAFT → ACTIVE, record settlements, and retrieve the contract UUID.
	contractID, err := s.confirmContract(ctx, req)

	// Log INBOUND regardless of whether state transition succeeded.
	ackStatusInbound := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
	var errMsg *string
	if err != nil {
		ackStatusInbound = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
		errMsg = errStrPtr(err)
		s.lh.WithModule("confirmsvc").Err().Error(err).Log("failed to confirm contract state")
	}

	s.writeMessageLog(ctx, dbsqlc.InsertMessageLogParams{
		TransactionID:        uuidToPgtype(txID),
		MessageID:            inboundMsgID,
		Action:               dbsqlc.BecknActionConfirm,
		Direction:            dbsqlc.MessageDirectionINBOUND,
		Url:                  strPtr("/webhook/confirm"),
		BapID:                strPtr(req.Context.BapID),
		BapUri:               strPtr(req.Context.BapURI),
		BppID:                strPtr(req.Context.BppID),
		BppUri:               strPtr(req.Context.BppURI),
		NetworkID:            strPtr(req.Context.NetworkID),
		AckStatus:            ackStatusInbound,
		RequestPayload:       reqJSON,
		ErrorMessage:         errMsg,
		ProcessingDurationMs: durationMs(start),
	})

	if err != nil {
		return
	}

	// Decrement inventory stock for every resource committed in this order.
	s.decrementStock(ctx, req)

	// Build the on_confirm payload — echo the contract back with ACTIVE status and the contract id.
	outboundMsgID := uuid.New()
	confirmedContract := req.Message.Contract
	confirmedContract.ID = contractID.String()
	confirmedContract.Status = &ContractStatus{Code: "ACTIVE"}
	confirmedContract.Settlements = completedSettlements(confirmedContract.Settlements)

	onConfirmReq := OnConfirmRequest{
		Context: BecknContext{
			Version:       req.Context.Version,
			Action:        "on_confirm",
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			MessageID:     outboundMsgID.String(),
			TransactionID: req.Context.TransactionID,
			BapID:         req.Context.BapID,
			BapURI:        req.Context.BapURI,
			BppID:         req.Context.BppID,
			BppURI:        req.Context.BppURI,
			TTL:           req.Context.TTL,
			NetworkID:     req.Context.NetworkID,
		},
		Message: OnConfirmMessage{Contract: confirmedContract},
	}

	// POST on_confirm through ONIX BPP caller (so the request is signed) and log OUTBOUND.
	s.callOnConfirm(ctx, txID, outboundMsgID, onConfirmReq)
}

// decrementStock reduces the on-hand stock by 1 for every resource referenced
// in the confirmed order's commitments. Both direct resource lists and offer
// resource-ID lists are handled. Errors are logged but never fatal — a stock
// bookkeeping failure must not block the order confirmation.
func (s *ConfirmService) decrementStock(ctx context.Context, req *ConfirmRequest) {
	q := dbsqlc.New(s.pool)
	bppID := s.cfg.BppID

	seen := map[string]struct{}{}
	for _, c := range req.Message.Contract.Commitments {
		for _, r := range c.Resources {
			seen[r.ID] = struct{}{}
		}
		if c.Offer != nil {
			for _, rid := range c.Offer.ResourceIDs {
				seen[rid] = struct{}{}
			}
		}
	}

	for resourceID := range seen {
		if err := q.DecrementResourceStock(ctx, dbsqlc.DecrementResourceStockParams{
			ResourceID: resourceID,
			BppID:      bppID,
		}); err != nil {
			s.lh.WithModule("confirmsvc").Warn().Error(err).Log("stock decrement failed for resource " + resourceID)
		} else {
			log.Printf("[stock] decremented stock for resource=%s bpp=%s", resourceID, bppID)
		}
	}
}

// confirmContract transitions the DRAFT contract to ACTIVE and records the
// BAP's settlements as COMPLETE, in one transaction: a contract is only
// activated together with its payment proof.
// Returns the UUID of the confirmed contract.
func (s *ConfirmService) confirmContract(ctx context.Context, req *ConfirmRequest) (uuid.UUID, error) {
	txID, err := uuid.Parse(req.Context.TransactionID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("invalid transactionId: %w", err)
	}
	if err := validateSettlements(req.Message.Contract); err != nil {
		return uuid.UUID{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("begin confirm tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := dbsqlc.New(s.pool).WithTx(tx)

	contractID, err := q.ConfirmContract(ctx, dbsqlc.ConfirmContractParams{
		TransactionID: txID,
		BppID:         s.cfg.BppID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.UUID{}, fmt.Errorf("no DRAFT contract found for transactionId %q: confirm requires a prior init", req.Context.TransactionID)
		}
		return uuid.UUID{}, fmt.Errorf("confirm contract DB update: %w", err)
	}

	for _, st := range req.Message.Contract.Settlements {
		attrCtx, attrType := extractJSONLDMeta(st.SettlementAttributes)
		if err := q.UpsertSettlement(ctx, dbsqlc.UpsertSettlementParams{
			ID:                          st.ID,
			ContractID:                  contractID,
			ConsiderationID:             st.ConsiderationID,
			Status:                      dbsqlc.SettlementStatusCOMPLETE,
			SettlementAttributes:        rawMessageOrNull(st.SettlementAttributes),
			SettlementAttributesContext: attrCtx,
			SettlementAttributesType:    attrType,
		}); err != nil {
			return uuid.UUID{}, fmt.Errorf("upsert settlement %s: %w", st.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.UUID{}, fmt.Errorf("commit confirm tx: %w", err)
	}
	return contractID, nil
}

// validateSettlements checks the confirm carries payment proof: at least one
// settlement, each discharging a consideration of this contract.
func validateSettlements(c ConfirmContract) error {
	if len(c.Settlements) == 0 {
		return fmt.Errorf("confirm has no settlements: payment proof is required")
	}
	considerationIDs := map[string]bool{}
	for _, cons := range c.Consideration {
		considerationIDs[cons.ID] = true
	}
	for _, st := range c.Settlements {
		if st.ID == "" {
			return fmt.Errorf("settlement is missing id")
		}
		if !considerationIDs[st.ConsiderationID] {
			return fmt.Errorf("settlement %s references unknown consideration %q", st.ID, st.ConsiderationID)
		}
	}
	return nil
}

// completedSettlements returns a copy of settlements marked COMPLETE — with no
// real gateway, the payment the BAP reports is treated as already discharged.
func completedSettlements(in []Settlement) []Settlement {
	out := make([]Settlement, len(in))
	for i, st := range in {
		st.Status = string(dbsqlc.SettlementStatusCOMPLETE)
		out[i] = st
	}
	return out
}

// extractJSONLDMeta pulls @context and @type from a JSON-LD blob.
// Returns nil pointers if the fields are absent so sqlc can store NULL.
func extractJSONLDMeta(raw json.RawMessage) (ctx *string, typ *string) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m struct {
		Context string `json:"@context"`
		Type    string `json:"@type"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil
	}
	if m.Context != "" {
		ctx = &m.Context
	}
	if m.Type != "" {
		typ = &m.Type
	}
	return ctx, typ
}

// callOnConfirm POSTs the on_confirm payload to the BAP and logs the OUTBOUND entry.
func (s *ConfirmService) callOnConfirm(
	ctx context.Context,
	txID uuid.UUID,
	msgID uuid.UUID,
	req OnConfirmRequest,
) {
	start := time.Now()
	onConfirmURL, err := s.resolveOnConfirmURL(req.Context.BapURI)
	reqJSON, _ := json.Marshal(req)

	if err != nil {
		s.lh.WithModule("confirmsvc").Err().Error(err).Log("on_confirm target resolution failed")
		s.writeMessageLog(ctx, dbsqlc.InsertMessageLogParams{
			TransactionID:        uuidToPgtype(txID),
			MessageID:            msgID,
			Action:               dbsqlc.BecknActionOnConfirm,
			Direction:            dbsqlc.MessageDirectionOUTBOUND,
			BapID:                strPtr(req.Context.BapID),
			BapUri:               strPtr(req.Context.BapURI),
			BppID:                strPtr(req.Context.BppID),
			BppUri:               strPtr(req.Context.BppURI),
			NetworkID:            strPtr(req.Context.NetworkID),
			AckStatus:            dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true},
			RequestPayload:       reqJSON,
			ErrorMessage:         errStrPtr(err),
			ProcessingDurationMs: durationMs(start),
		})
		return
	}

	var (
		respBody []byte
		callErr  error
	)

	defer func() {
		ackStatus := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
		if callErr != nil {
			ackStatus = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
		}
		s.writeMessageLog(ctx, dbsqlc.InsertMessageLogParams{
			TransactionID:        uuidToPgtype(txID),
			MessageID:            msgID,
			Action:               dbsqlc.BecknActionOnConfirm,
			Direction:            dbsqlc.MessageDirectionOUTBOUND,
			Url:                  strPtr(onConfirmURL),
			BapID:                strPtr(req.Context.BapID),
			BapUri:               strPtr(req.Context.BapURI),
			BppID:                strPtr(req.Context.BppID),
			BppUri:               strPtr(req.Context.BppURI),
			NetworkID:            strPtr(req.Context.NetworkID),
			AckStatus:            ackStatus,
			RequestPayload:       reqJSON,
			ResponsePayload:      rawMessageOrNull(respBody),
			ErrorMessage:         errStrPtr(callErr),
			ProcessingDurationMs: durationMs(start),
		})
	}()

	prettyLog(fmt.Sprintf("→ OUT  ON_CONFIRM | txn=%.8s… | POST %s", txID, onConfirmURL), req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, onConfirmURL, bytes.NewReader(reqJSON))
	if err != nil {
		callErr = fmt.Errorf("build on_confirm request: %w", err)
		prettyLog(fmt.Sprintf("← ERR  ON_CONFIRM | txn=%.8s… | dur=%dms | %s", txID, time.Since(start).Milliseconds(), callErr), map[string]string{"error": callErr.Error()})
		s.lh.WithModule("confirmsvc").Err().Error(callErr).Log("on_confirm HTTP request build failed")
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		callErr = fmt.Errorf("on_confirm HTTP call to BAP failed: %w", err)
		prettyLog(fmt.Sprintf("← ERR  ON_CONFIRM | txn=%.8s… | dur=%dms | %s", txID, time.Since(start).Milliseconds(), callErr), map[string]string{"error": callErr.Error()})
		s.lh.WithModule("confirmsvc").Err().Error(callErr).Log("on_confirm call failed")
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, _ = io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		callErr = fmt.Errorf("BAP returned non-2xx status %d: %s", resp.StatusCode, string(respBody))
		prettyLog(fmt.Sprintf("← ERR  ON_CONFIRM | txn=%.8s… | http=%d | dur=%dms | %s", txID, resp.StatusCode, time.Since(start).Milliseconds(), callErr), map[string]interface{}{"http_code": resp.StatusCode, "error": callErr.Error(), "response": json.RawMessage(respBody)})
		s.lh.WithModule("confirmsvc").Warn().Error(callErr).Log("on_confirm BAP response error")
		return
	}

	prettyLog(fmt.Sprintf("← ACK  ON_CONFIRM | txn=%.8s… | http=%d | dur=%dms", txID, resp.StatusCode, time.Since(start).Milliseconds()), map[string]interface{}{"http_code": resp.StatusCode, "response": json.RawMessage(respBody)})
	s.lh.WithModule("confirmsvc").Log("on_confirm sent successfully")
}

// resolveOnConfirmURL returns the ONIX BPP caller on_confirm endpoint.
// Priority:
//  1. BPP_CALLER_URL env/config (e.g. https://bpptest.remiges.tech/bpp/caller)
//  2. Derived from BPP_URI by replacing /bpp/receiver with /bpp/caller
func (s *ConfirmService) resolveOnConfirmURL(_ string) (string, error) {
	base := strings.TrimSpace(s.cfg.BppCallerURL)
	if base == "" {
		base = strings.TrimSpace(s.cfg.BppURI)
		if base == "" {
			return "", fmt.Errorf("cannot resolve on_confirm URL: both BPP_CALLER_URL and BPP_URI are empty")
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
	return base + "/on_confirm", nil
}

// writeMessageLog inserts one audit row into beckn_message_log.
func (s *ConfirmService) writeMessageLog(ctx context.Context, params dbsqlc.InsertMessageLogParams) {
	q := dbsqlc.New(s.pool)
	if err := q.InsertMessageLog(ctx, params); err != nil {
		s.lh.WithModule("confirmsvc").Warn().Error(err).Log("failed to write beckn_message_log")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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
	s := err.Error()
	return &s
}

func uuidToPgtype(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func durationMs(start time.Time) *int32 {
	ms := int32(time.Since(start).Milliseconds())
	return &ms
}

// prettyLog prints a labelled pretty-JSON block to stdout.
func prettyLog(label string, v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("[beckn] %s\n(marshal error: %v)", label, err)
		return
	}
	log.Printf("[beckn] %s\n%s", label, string(b))
}
