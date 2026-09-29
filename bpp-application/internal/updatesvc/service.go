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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/httpclient"
	"github.com/ion/winroom/bpp/internal/orderchange"
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

// Process validates the incoming /update request and, if valid, queues it
// as a PENDING contract_update_request for seller review -- it does NOT
// mutate the contract or send on_update itself. That happens later, when a
// seller calls ApproveRequest/RejectRequest via the dashboard (see
// server.go's /dashboard/update-requests routes).
//
// This is a deliberate departure from confirmsvc/cancelsvc's fully-automatic
// pattern: an update changes committed order terms after the fact, which
// this app treats as something a seller must actually decide on, not
// something any caller can force through unattended. See the "Update
// approval model" decision.
//
// Every inbound update is still audited to beckn_message_log (ACK or NACK)
// regardless of outcome, same as confirmsvc, with PII redacted from the
// logged copy (see redact.go) -- the real payload is only ever persisted
// unredacted in contract_update_requests, reachable solely through the
// seller-authenticated dashboard.
func (s *Service) Process(ctx context.Context, req *UpdateRequest) {
	start := time.Now()
	reqJSON, _ := json.Marshal(req)
	loggedJSON := redactForLog(reqJSON)
	inboundMsgID, _ := uuid.Parse(req.Context.MessageID)

	prettyLog(fmt.Sprintf("← IN   UPDATE   | txn=%.8s… | bap=%s", req.Context.TransactionID, req.Context.BapID), redactedLogView(req))

	requestID, err := s.queueUpdateRequest(ctx, req)

	ackStatusInbound := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
	var errMsg *string
	if err != nil {
		ackStatusInbound = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
		errMsg = errStrPtr(err)
		s.lh.WithModule("updatesvc").Err().Error(err).Log("failed to queue update request")
	} else {
		s.lh.WithModule("updatesvc").LogActivity(
			fmt.Sprintf("update request %.8s… queued for seller review, txn=%.8s…", requestID, req.Context.TransactionID),
			nil,
		)
	}

	s.writeMessageLog(ctx, dbsqlc.InsertMessageLogParams{
		TransactionID:        uuidToPgtype(parseTxnID(req.Context.TransactionID)),
		MessageID:            inboundMsgID,
		Action:               dbsqlc.BecknActionUpdate,
		Direction:            dbsqlc.MessageDirectionINBOUND,
		Url:                  strPtr("/api/webhook/update"),
		BapID:                strPtr(req.Context.BapID),
		BapUri:               strPtr(req.Context.BapURI),
		BppID:                strPtr(req.Context.BppID),
		BppUri:               strPtr(req.Context.BppURI),
		NetworkID:            strPtr(req.Context.NetworkID),
		AckStatus:            ackStatusInbound,
		RequestPayload:       loggedJSON,
		ErrorMessage:         errMsg,
		ProcessingDurationMs: durationMs(start),
	})

	// No on_update is sent here either way: a validation failure has
	// nothing valid to report, and a successfully-queued request has no
	// decision yet to report -- on_update is only ever sent from
	// ApproveRequest/RejectRequest once a seller actually decides.
}

// queueUpdateRequest validates the request and, if valid, inserts a
// PENDING contract_update_requests row -- returning a descriptive error
// for every rejection reason (malformed transactionId, missing contract
// payload, or a contract that doesn't exist / isn't ACTIVE) so Process's
// audit log records *why* a request was refused before it ever reached a
// seller.
func (s *Service) queueUpdateRequest(ctx context.Context, req *UpdateRequest) (uuid.UUID, error) {
	txID, err := uuid.Parse(req.Context.TransactionID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid transactionId %q: %w", req.Context.TransactionID, err)
	}
	if len(req.Message.Contract) == 0 {
		return uuid.Nil, fmt.Errorf("update request for txn %s carries no contract payload", req.Context.TransactionID)
	}

	q := dbsqlc.New(s.pool)
	contract, err := q.GetActiveContractByTxnID(ctx, dbsqlc.GetActiveContractByTxnIDParams{
		TransactionID: txID,
		BppID:         s.cfg.BppID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, fmt.Errorf("no ACTIVE contract found for transactionId %q: already cancelled/complete, or never confirmed", req.Context.TransactionID)
		}
		return uuid.Nil, fmt.Errorf("contract lookup: %w", err)
	}
	if contract.Status != dbsqlc.ContractStatusACTIVE {
		return uuid.Nil, fmt.Errorf("contract for transactionId %q is %s, not ACTIVE: update rejected", req.Context.TransactionID, contract.Status)
	}

	// Snapshot the delivery block as it stands now, so the seller can still
	// see WHAT changed after they approve -- once approved, the live
	// contract holds the requested values and a live diff shows nothing.
	// Best-effort: no performance row just means no "before" column, and
	// the dashboard falls back to the live contract.
	var previousDelivery []byte
	if perf, perfErr := q.GetCurrentPerformanceForContract(ctx, contract.ID); perfErr == nil {
		previousDelivery = orderchange.DeliveryDetailsFromPerformanceJSON(perf.PerformanceAttributes)
	}

	// Refuse a request that changes nothing a seller could act on.
	//
	// Our own BAP already refuses these before sending (see
	// clientsvc.ErrNoChangeRequested), so this is the backstop for any
	// other participant. It reuses the same comparison the review screen
	// renders from, so "queued" and "has something to show" can never
	// disagree -- a queued request with an empty change list is exactly
	// the blank row this prevents.
	//
	// Note this runs after the handler has already ACKed (the handler ACKs
	// then processes in a goroutine), so the refusal reaches the caller as
	// a NACK in beckn_message_log rather than an HTTP error. A peer that
	// does not pre-check its own no-ops will therefore see no on_update;
	// that is a deliberate trade-off against restructuring the ACK flow,
	// and is why our BAP checks first.
	if len(previousDelivery) > 0 &&
		len(orderchange.SummarizeFromDeliveryDetails(previousDelivery, req.Message.Contract)) == 0 {
		return uuid.Nil, fmt.Errorf("update request for txn %s changes nothing: submitted delivery details match the contract's current details", req.Context.TransactionID)
	}

	requestID, err := q.InsertUpdateRequest(ctx, dbsqlc.InsertUpdateRequestParams{
		ContractID:              contract.ID,
		TransactionID:           txID,
		BapID:                   req.Context.BapID,
		BapUri:                  req.Context.BapURI,
		RequestedAttributes:     req.Message.Contract,
		PreviousDeliveryDetails: previousDelivery,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert update request: %w", err)
	}
	return requestID, nil
}

// ApproveRequest applies a PENDING request's proposed attributes to the
// contract and sends on_update with the newly-applied contract. providerID
// scopes the decision to the calling seller's own contracts (nil bypasses,
// for internal/ops use), same convention as the rest of dashboardsvc.
func (s *Service) ApproveRequest(ctx context.Context, requestID uuid.UUID, providerID *string) error {
	q := dbsqlc.New(s.pool)
	reqRow, err := q.GetUpdateRequestForDecision(ctx, dbsqlc.GetUpdateRequestForDecisionParams{
		ID:         requestID,
		BppID:      s.cfg.BppID,
		ProviderID: providerID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("no update request %s found for this seller", requestID)
		}
		return fmt.Errorf("lookup update request: %w", err)
	}

	rows, err := q.DecideUpdateRequestIfPending(ctx, dbsqlc.DecideUpdateRequestIfPendingParams{
		ID:     requestID,
		Status: dbsqlc.UpdateRequestStatusAPPROVED,
	})
	if err != nil {
		return fmt.Errorf("decide update request: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("update request %s was already decided (approved/rejected elsewhere)", requestID)
	}

	// Apply the approved delivery details to contract_performances, which
	// is where fulfilment actually reads the address from.
	//
	// Without this, approving an address change was purely cosmetic: the
	// proposal was stored on the contract but the parcel still went to the
	// old address, because contracts.contract_attributes is a JSON-LD
	// extension bag that nothing consults for delivery. Only the
	// whitelisted delivery block is applied -- see internal/orderchange
	// for why a wholesale contract overwrite is the wrong move here.
	if dd := orderchange.DeliveryDetailsJSON(reqRow.RequestedAttributes); len(dd) > 0 {
		rowsApplied, applyErr := q.ApplyPerformanceDeliveryDetails(ctx, dbsqlc.ApplyPerformanceDeliveryDetailsParams{
			ContractID:      reqRow.ContractID,
			DeliveryDetails: dd,
		})
		switch {
		case applyErr != nil:
			// The seller's decision is already recorded as APPROVED, so
			// failing to apply it is loud: the order is now inconsistent
			// with what the seller agreed to and needs a human.
			s.lh.WithModule("updatesvc").Err().Error(applyErr).Log(
				fmt.Sprintf("approved update request %.8s… but FAILED to apply delivery details to contract_performances", requestID))
			return fmt.Errorf("apply approved delivery details: %w", applyErr)
		case rowsApplied == 0:
			s.lh.WithModule("updatesvc").Warn().Log(
				fmt.Sprintf("approved update request %.8s… but no contract_performances row exists to apply it to", requestID))
		}
	}

	contractRow, err := q.UpdateContractIfActive(ctx, dbsqlc.UpdateContractIfActiveParams{
		ContractAttributes: reqRow.RequestedAttributes,
		TransactionID:      reqRow.TransactionID,
		BppID:              s.cfg.BppID,
	})
	if err != nil {
		// The decision is already recorded as APPROVED; the contract mutation
		// itself failing (e.g. it was cancelled between queueing and
		// approval) is logged distinctly rather than silently losing the fact
		// that a seller approved something that could no longer be applied.
		s.lh.WithModule("updatesvc").Err().Error(err).Log(fmt.Sprintf("approved update request %.8s… but contract mutation failed", requestID))
		return fmt.Errorf("apply approved update: %w", err)
	}

	s.sendDecision(ctx, reqRow, contractRow.ID, reqRow.RequestedAttributes, orderchange.ApproveDecision())
	return nil
}

// RejectRequest marks a PENDING request REJECTED and sends on_update back
// with the contract's UNCHANGED current attributes -- see this package's
// own notes on the open spec-fit question this represents (on_update's
// wire shape has no field for "rejected, here's why"; the reason is
// carried only as this app's own internal record, surfaced to the buyer
// out of band via the order detail view, not as a real Beckn extension).
func (s *Service) RejectRequest(ctx context.Context, requestID uuid.UUID, providerID *string, reasonCode, reason string) error {
	q := dbsqlc.New(s.pool)
	reqRow, err := q.GetUpdateRequestForDecision(ctx, dbsqlc.GetUpdateRequestForDecisionParams{
		ID:         requestID,
		BppID:      s.cfg.BppID,
		ProviderID: providerID,
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("no update request %s found for this seller", requestID)
		}
		return fmt.Errorf("lookup update request: %w", err)
	}

	// Persist something readable even when the seller only tapped a preset
	// and typed nothing: fall back to the code's human label, so the
	// seller's own record of WHY they declined is never blank. The code
	// itself still goes on the wire to the buyer (see RejectDecision).
	storedReason := reason
	if storedReason == "" {
		if label, ok := orderchange.RejectReason[reasonCode]; ok {
			storedReason = label
		}
	}
	var reasonPtr *string
	if storedReason != "" {
		reasonPtr = &storedReason
	}
	rows, err := q.DecideUpdateRequestIfPending(ctx, dbsqlc.DecideUpdateRequestIfPendingParams{
		ID:           requestID,
		Status:       dbsqlc.UpdateRequestStatusREJECTED,
		RejectReason: reasonPtr,
	})
	if err != nil {
		return fmt.Errorf("decide update request: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("update request %s was already decided (approved/rejected elsewhere)", requestID)
	}

	contract, err := q.GetActiveContractByTxnID(ctx, dbsqlc.GetActiveContractByTxnIDParams{
		TransactionID: reqRow.TransactionID,
		BppID:         s.cfg.BppID,
	})
	if err != nil {
		s.lh.WithModule("updatesvc").Err().Error(err).Log(fmt.Sprintf("rejected update request %.8s… but could not re-fetch contract to notify BAP", requestID))
		return nil
	}

	s.sendDecision(ctx, reqRow, contract.ID, contract.ContractAttributes, orderchange.RejectDecision(reasonCode, reason))
	return nil
}

// sendDecision delivers on_update, carrying the seller's actual decision
// (and, on a rejection, their reason) at
// contract.contractAttributes.updateDecision -- see
// internal/orderchange/decision.go for why that slot is schema-legal and
// why an inferred outcome alone is not enough.
func (s *Service) sendDecision(ctx context.Context, reqRow dbsqlc.GetUpdateRequestForDecisionRow, contractID uuid.UUID, attributes json.RawMessage, decision orderchange.UpdateDecision) {
	outMsgID := uuid.New()
	updatedContract := attributes
	if len(updatedContract) == 0 {
		updatedContract = json.RawMessage(`{"id":"` + contractID.String() + `"}`)
	}
	updatedContract = orderchange.AttachDecision(updatedContract, decision)

	onUpdateReq := OnUpdateRequest{
		Context: BecknContext{
			Version:       "2.0.0",
			Action:        "on_update",
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			MessageID:     outMsgID.String(),
			TransactionID: reqRow.TransactionID.String(),
			BapID:         reqRow.BapID,
			BapURI:        reqRow.BapUri,
			BppID:         s.cfg.BppID,
			BppURI:        s.cfg.BppURI,
			NetworkID:     s.cfg.NetworkID,
		},
		Message: OnUpdateMessage{Contract: updatedContract},
	}
	// Deliver on a DETACHED context, in the background.
	//
	// ctx here is the seller's HTTP request context. Delivering on it tied
	// the callback's lifetime to the seller's browser: onix validates the
	// outbound contract against the ION Extended Schemas, which costs
	// ~9-22s on a cold cache, and when the seller's request ended first the
	// POST died with "context canceled" -- so the decision was recorded,
	// the seller saw success, and the buyer was never told anything. The
	// audit log write failed for the same reason, leaving no trace.
	//
	// confirmsvc and cancelsvc already deliver their callbacks on
	// context.Background() in a goroutine for exactly this reason; this was
	// the one decision path that did not.
	go func() {
		deliverCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		s.callOnUpdate(deliverCtx, reqRow.TransactionID, outMsgID, onUpdateReq)
	}()
}

// callOnUpdate POSTs the on_update payload to the BAP and logs the OUTBOUND entry.
func (s *Service) callOnUpdate(ctx context.Context, txID uuid.UUID, msgID uuid.UUID, req OnUpdateRequest) {
	start := time.Now()
	url, err := s.resolveOnUpdateURL(req.Context.BapURI)
	reqJSON, _ := json.Marshal(req)
	loggedJSON := redactForLog(reqJSON)

	if err != nil {
		s.lh.WithModule("updatesvc").Err().Error(err).Log("on_update target resolution failed")
		s.writeMessageLog(ctx, dbsqlc.InsertMessageLogParams{
			TransactionID:        uuidToPgtype(txID),
			MessageID:            msgID,
			Action:               dbsqlc.BecknActionOnUpdate,
			Direction:            dbsqlc.MessageDirectionOUTBOUND,
			BapID:                strPtr(req.Context.BapID),
			BapUri:               strPtr(req.Context.BapURI),
			BppID:                strPtr(req.Context.BppID),
			BppUri:               strPtr(req.Context.BppURI),
			NetworkID:            strPtr(req.Context.NetworkID),
			AckStatus:            dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true},
			RequestPayload:       loggedJSON,
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
			Action:               dbsqlc.BecknActionOnUpdate,
			Direction:            dbsqlc.MessageDirectionOUTBOUND,
			Url:                  strPtr(url),
			BapID:                strPtr(req.Context.BapID),
			BapUri:               strPtr(req.Context.BapURI),
			BppID:                strPtr(req.Context.BppID),
			BppUri:               strPtr(req.Context.BppURI),
			NetworkID:            strPtr(req.Context.NetworkID),
			AckStatus:            ackStatus,
			RequestPayload:       loggedJSON,
			ResponsePayload:      rawMessageOrNull(respBody),
			ErrorMessage:         errStrPtr(callErr),
			ProcessingDurationMs: durationMs(start),
		})
	}()

	prettyLog(fmt.Sprintf("→ OUT  ON_UPDATE | txn=%.8s… | POST %s", txID, url), redactedLogView(req))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqJSON))
	if err != nil {
		callErr = fmt.Errorf("build on_update request: %w", err)
		s.lh.WithModule("updatesvc").Err().Error(callErr).Log("on_update HTTP request build failed")
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		callErr = fmt.Errorf("on_update HTTP call to BAP failed: %w", err)
		s.lh.WithModule("updatesvc").Warn().Error(callErr).Log("on_update callback failed")
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, _ = io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		callErr = fmt.Errorf("BAP returned non-2xx status %d: %s", resp.StatusCode, string(respBody))
		s.lh.WithModule("updatesvc").Warn().Error(callErr).Log("on_update BAP response error")
		return
	}

	prettyLog(fmt.Sprintf("← ACK  ON_UPDATE | txn=%.8s… | http=%d | dur=%dms", txID, resp.StatusCode, time.Since(start).Milliseconds()), map[string]interface{}{"http_code": resp.StatusCode, "response": json.RawMessage(respBody)})
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

// writeMessageLog inserts one audit row into beckn_message_log.
func (s *Service) writeMessageLog(ctx context.Context, params dbsqlc.InsertMessageLogParams) {
	q := dbsqlc.New(s.pool)
	if err := q.InsertMessageLog(ctx, params); err != nil {
		s.lh.WithModule("updatesvc").Warn().Error(err).Log("failed to write beckn_message_log")
	}
}

func prettyLog(label string, v interface{}) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("[beckn] %s\n(marshal error: %v)", label, err)
		return
	}
	log.Printf("[beckn] %s\n%s", label, string(b))
}

// redactedLogView marshals v and re-parses it through redactForLog so
// stdout's pretty-printed request/response logs get the same PII masking
// as the structured audit trail -- prettyLog is for human debugging, not
// an excuse to bypass redaction.
func redactedLogView(v interface{}) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return redactForLog(raw)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseTxnID best-effort parses a transactionId for audit logging -- a
// malformed one already produced a descriptive error from queueUpdateRequest,
// so the message log row just gets a zero-value UUID rather than losing the
// audit row entirely.
func parseTxnID(s string) uuid.UUID {
	id, _ := uuid.Parse(s)
	return id
}

func uuidToPgtype(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil}
}

func durationMs(start time.Time) *int32 {
	ms := int32(time.Since(start).Milliseconds())
	return &ms
}

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
