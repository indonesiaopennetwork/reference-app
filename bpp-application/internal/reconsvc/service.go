package reconsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/cancellation"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/httpclient"
	"github.com/ion/winroom/bpp/internal/settlement"
)

const (
	actionReconcile   = dbsqlc.BecknAction("reconcile")
	actionOnReconcile = dbsqlc.BecknAction("on_reconcile")

	// Batch directions: OUTBOUND is a /reconcile this BPP sent, INBOUND one a
	// BAP sent.
	directionOutbound = "OUTBOUND"
	directionInbound  = "INBOUND"
)

// ErrNothingDue is returned when a BAP has no contracts due for reconciliation.
var ErrNothingDue = errors.New("no contracts are due for reconciliation with this BAP")

type Service struct {
	pool       *pgxpool.Pool
	cfg        *config.Config
	lh         *logharbour.Logger
	httpClient *http.Client
}

func NewService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Service {
	return &Service{pool: pool, cfg: cfg, lh: lh, httpClient: httpclient.New(60 * time.Second)}
}

func (s *Service) terms() Terms {
	return Terms{Basis: s.cfg.SettlementBasis, Window: s.cfg.SettlementWindow, SelfID: s.cfg.BppID, Account: map[string]any{
		"holderRole":    "BPP",
		"bankCode":      s.cfg.SettlementBankCode,
		"accountNumber": s.cfg.SettlementAccountNumber,
		"accountName":   s.cfg.SettlementAccountName,
	}}
}

// Batch is a reconciliations row: one exchange (context.transactionId) with
// a BAP. ID is this BPP's own name for it, not sent on the wire.
type Batch struct {
	ID              string          `json:"id"`
	TransactionID   string          `json:"transaction_id"`
	Direction       string          `json:"direction"`
	BapID           string          `json:"bap_id"`
	SettlementDate  string          `json:"settlement_date"`
	Status          string          `json:"status"`
	ContractIDs     []string        `json:"contract_ids"`
	Summary         json.RawMessage `json:"summary"`
	RequestPayload  json.RawMessage `json:"request_payload"`
	ResponsePayload json.RawMessage `json:"response_payload"`
	ErrorMessage    *string         `json:"error_message"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// Batches lists the reconciliation batches with this provider's contracts,
// sent or received, newest first.
func (s *Service) Batches(ctx context.Context, providerID string, limit int) ([]Batch, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, COALESCE(r.transaction_id, ''), r.direction, r.bap_id, r.settlement_date::text, r.status, r.contract_ids::text[], r.summary,
		       r.request_payload, r.response_payload, r.error_message, r.created_at, r.updated_at
		  FROM reconciliations r
		 WHERE r.bpp_id = $1
		   AND (r.provider_id = $2 OR EXISTS (
		        SELECT 1 FROM contracts c WHERE c.id = ANY (r.contract_ids) AND c.provider_id = $2))
		 ORDER BY r.created_at DESC LIMIT $3`, s.cfg.BppID, providerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Batch{}
	for rows.Next() {
		var b Batch
		if err := rows.Scan(&b.ID, &b.TransactionID, &b.Direction, &b.BapID, &b.SettlementDate, &b.Status, &b.ContractIDs, &b.Summary,
			&b.RequestPayload, &b.ResponsePayload, &b.ErrorMessage, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Send builds this provider's /reconcile for one BAP -- the selected
// contracts, or by default the unresolved settlements of an exchange this BPP
// started (again in that exchange) or else every contract due now; see
// choose -- and sends it through the onix BPP caller. The included payments
// are marked PENDING in the batch before sending and taken back out if it is
// not delivered.
func (s *Service) Send(ctx context.Context, providerID, bapID string, selected []string) (*Batch, error) {
	now := time.Now().UTC()
	all, err := s.Contracts(ctx, providerID, now)
	if err != nil {
		return nil, err
	}
	var due []DueContract
	for _, d := range all {
		if selectable(d) && d.BapID == bapID {
			due = append(due, d)
		}
	}
	id, due := choose(due, s.cfg.BppID, selected)
	if len(due) == 0 {
		return nil, ErrNothingDue
	}

	// The BAP's URI, from any of its contracts that has one recorded.
	bapURI := ""
	for _, d := range all {
		if d.BapID == bapID {
			bapURI = firstNonEmpty(bapURI, d.BapURI)
		}
	}
	date := now.Format("2006-01-02")
	// A resend stays in its exchange (same transactionId); a new batch
	// starts one.
	txnID := ""
	if id != "" {
		_ = s.pool.QueryRow(ctx, `SELECT COALESCE(transaction_id, '') FROM reconciliations WHERE id = $1`, id).Scan(&txnID)
	} else {
		id = reconciliationID(now, s.cfg.BppID, s.idTaken(ctx))
	}
	if txnID == "" {
		txnID = uuid.New().String()
	}
	contracts := make([]any, 0, len(due))
	contractIDs := make([]string, 0, len(due))
	for _, d := range due {
		contracts = append(contracts, snapshot(d.contract, d.Figures, d.Figures.bag()))
		contractIDs = append(contractIDs, d.ContractID.String())
	}
	summary := summaryOf(bapID, s.cfg.BppID, date, contracts)
	summary.OrderDayCounts = s.orderDayCounts(ctx, providerID, bapID, due, all)

	msgID := uuid.New()
	network := firstNonEmpty(due[0].NetworkID, s.cfg.NetworkID)
	req := map[string]any{
		"context": map[string]any{
			"version": "2.0.0", "action": "reconcile",
			"timestamp":     now.Format(time.RFC3339),
			"messageId":     msgID.String(),
			"transactionId": txnID, // the exchange, not any one order
			"bapId":         bapID, "bapUri": bapURI,
			"bppId": s.cfg.BppID, "bppUri": s.cfg.BppURI,
			"senderId": s.cfg.BppID, "receiverId": bapID,
			"ttl": "PT30S", "networkId": network,
		},
		"message": map[string]any{
			"settlementDate":    date,
			"contracts":         contracts,
			"settlementSummary": summary,
		},
	}
	body, _ := json.Marshal(req)
	summaryJSON, _ := json.Marshal(summary)

	// Record the batch and mark its payments BEFORE sending: the BAP may
	// answer /on_reconcile before our HTTP call returns, and must find both.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO reconciliations (id, direction, bpp_id, bap_id, provider_id, settlement_date, status, contract_ids,
		                             summary, request_message_id, request_payload, transaction_id)
		VALUES ($1, $2, $3, $4, $5, $6::date, 'SENDING', $7::uuid[], $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET status = 'SENDING', settlement_date = EXCLUDED.settlement_date,
		    contract_ids = EXCLUDED.contract_ids, summary = EXCLUDED.summary,
		    request_message_id = EXCLUDED.request_message_id, request_payload = EXCLUDED.request_payload,
		    transaction_id = EXCLUDED.transaction_id, error_message = NULL, updated_at = NOW()`,
		id, directionOutbound, s.cfg.BppID, bapID, providerID, date, contractIDs, summaryJSON, msgID, body, txnID); err != nil {
		return nil, fmt.Errorf("record batch: %w", err)
	}
	for _, d := range due {
		if err := setSettlementState(ctx, tx, d.ContractID, d.contract, d.settlementIDs(), id, "PENDING"); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	respBody, sendErr := s.post(ctx, "reconcile", body)
	s.logMessage(ctx, msgID, actionReconcile, dbsqlc.MessageDirectionOUTBOUND, "/bpp/caller/reconcile", bapID, bapURI, network, body, respBody, sendErr)

	status := "SENT"
	var errMsg *string
	if sendErr == nil {
		// Keep an outcome the BAP's /on_reconcile may already have recorded.
		if _, err := s.pool.Exec(ctx, `UPDATE reconciliations SET status = 'SENT', updated_at = NOW() WHERE id = $1 AND status = 'SENDING'`, id); err != nil {
			return nil, err
		}
		_ = s.pool.QueryRow(ctx, `SELECT status FROM reconciliations WHERE id = $1`, id).Scan(&status)
	} else {
		status = "FAILED"
		e := sendErr.Error()
		errMsg = &e
		if err := s.revert(ctx, id, due, e); err != nil {
			return nil, err
		}
	}
	batch := &Batch{ID: id, TransactionID: txnID, Direction: directionOutbound, BapID: bapID, SettlementDate: date, Status: status,
		ContractIDs: contractIDs, Summary: summaryJSON, RequestPayload: body, ErrorMessage: errMsg, CreatedAt: now, UpdatedAt: now}
	if sendErr != nil {
		return batch, fmt.Errorf("reconcile not delivered: %w", sendErr)
	}
	return batch, nil
}

// orderDayCounts is the settlementSummary's orderDayCounts for a batch to a
// BAP: for each order day of its contracts, this provider's orders with the
// BAP created that day, and how many of them are not yet reconciled (open:
// every contract still to reconcile).
func (s *Service) orderDayCounts(ctx context.Context, providerID, bapID string, batch, open []DueContract) []OrderDayCount {
	var out []OrderDayCount
	seen := map[string]bool{}
	for _, d := range batch {
		day := d.OrderDate
		if day == "" || seen[day] {
			continue
		}
		seen[day] = true
		c := OrderDayCount{OrderDate: day}
		if err := s.pool.QueryRow(ctx, `
			SELECT count(*) FROM contracts
			 WHERE bpp_id = $1 AND provider_id = $2 AND bap_id = $3 AND deleted_at IS NULL
			   AND status IN ('ACTIVE', 'CANCELLED', 'COMPLETE')
			   AND (created_at AT TIME ZONE 'Asia/Jakarta')::date = $4::date`,
			s.cfg.BppID, providerID, bapID, day).Scan(&c.ReportedOrderCount); err != nil {
			continue
		}
		for _, o := range open {
			if o.BapID == bapID && o.OrderDate == day {
				c.RemainingOrderCount++
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OrderDate < out[j].OrderDate })
	return out
}

// settlementIDs are the settlements a contract's figures cover: the payment
// and its pre-payout refunds.
func (d DueContract) settlementIDs() []string {
	return append([]string{d.SettlementID}, d.RefundSettlementIDs...)
}

// revert marks an undelivered batch FAILED and puts its settlements back in
// their earlier state, so they go into the next batch.
func (s *Service) revert(ctx context.Context, id string, due []DueContract, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE reconciliations SET status = 'FAILED', error_message = $2, updated_at = NOW() WHERE id = $1`, id, reason); err != nil {
		return err
	}
	for _, d := range due {
		previous, prevStatus := "", d.ReconciliationStatus
		if prevStatus != "" {
			previous = str(asMap(asMap(paymentOf(d.contract))["settlementAttributes"])["reconciliationId"])
		}
		if err := setSettlementState(ctx, tx, d.ContractID, d.contract, d.settlementIDs(), previous, prevStatus); err != nil {
			return fmt.Errorf("restore contract %s: %w", d.ContractID, err)
		}
	}
	return tx.Commit(ctx)
}

// beckn is the envelope of a /reconcile or /on_reconcile.
type beckn struct {
	Context struct {
		Version       string `json:"version"`
		MessageID     string `json:"messageId"`
		TransactionID string `json:"transactionId"`
		BapID         string `json:"bapId"`
		BapURI        string `json:"bapUri"`
		BppID         string `json:"bppId"`
		BppURI        string `json:"bppUri"`
		SenderID      string `json:"senderId"`
		NetworkID     string `json:"networkId"`
		TTL           string `json:"ttl"`
	} `json:"context"`
	Message struct {
		ReconciliationStatus string         `json:"reconciliationStatus"`
		SettlementDate       string         `json:"settlementDate"`
		Contracts            []any          `json:"contracts"`
		SettlementSummary    map[string]any `json:"settlementSummary"`
	} `json:"message"`
}

// ApplyOnReconcile records the BAP's answer to a /reconcile this BPP sent --
// found by its messageId (or, failing that, its exchange's transactionId):
// the answer and its reconciliationStatus, and each settlement's own outcome
// (settlementOutcome): AGREED ones go on to SEAM, DISPUTED and pending ones
// are sent again.
func (s *Service) ApplyOnReconcile(ctx context.Context, raw []byte) error {
	var req beckn
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("invalid on_reconcile: %w", err)
	}
	m := req.Message
	msgID, _ := uuid.Parse(req.Context.MessageID)
	defer s.logMessage(ctx, msgID, actionOnReconcile, dbsqlc.MessageDirectionINBOUND, "/webhook/on_reconcile",
		req.Context.BapID, req.Context.BapURI, req.Context.NetworkID, raw, nil, nil)

	status := firstNonEmpty(summaryOf(req.Context.BapID, s.cfg.BppID, m.SettlementDate, m.Contracts).status(), m.ReconciliationStatus)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var id string
	if err := tx.QueryRow(ctx, `
		SELECT id FROM reconciliations
		 WHERE bpp_id = $1 AND direction = $2 AND (request_message_id = $3 OR transaction_id = $4)
		 ORDER BY (request_message_id = $3) DESC, updated_at DESC LIMIT 1`,
		s.cfg.BppID, directionOutbound, msgID, req.Context.TransactionID).Scan(&id); err != nil {
		return fmt.Errorf("no /reconcile with messageId %s or transactionId %s", req.Context.MessageID, req.Context.TransactionID)
	}
	if _, err := tx.Exec(ctx, `UPDATE reconciliations SET status = $2, response_payload = $3, updated_at = NOW() WHERE id = $1`,
		id, status, raw); err != nil {
		return err
	}
	for _, rawC := range m.Contracts {
		c := asMap(rawC)
		cid, err := uuid.Parse(str(c["id"]))
		if err != nil {
			continue
		}
		contract, err := s.storedContract(ctx, tx, cid)
		if err != nil {
			continue
		}
		bag := asMap(asMap(firstOf(slice(c["settlements"])))["settlementAttributes"])
		accord, _ := bag["accord"].(bool)
		outcome := settlementOutcome(accord, str(asMap(bag["dispute"])["code"]))
		if err := setSettlementState(ctx, tx, cid, contract, batchSettlements(contract, id), id, outcome); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// batchSettlements are a contract's settlements marked as in batch reconID.
func batchSettlements(contract map[string]any, reconID string) []string {
	var ids []string
	for _, raw := range slice(contract["settlements"]) {
		st := asMap(raw)
		if str(asMap(st["settlementAttributes"])["reconciliationId"]) == reconID {
			ids = append(ids, str(st["id"]))
		}
	}
	return ids
}

// Respond answers a BAP's /reconcile: it checks every settlement's figures
// against this BPP's own, replies /on_reconcile with its figures and accord
// per settlement, and records the answer in the exchange's batch (one per
// transactionId). Each settlement's outcome is its own: AGREED ones go on to
// SEAM, the others stay for the BAP to send again.
func (s *Service) Respond(ctx context.Context, raw []byte) error {
	var req beckn
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("invalid reconcile: %w", err)
	}
	m := req.Message
	msgID, _ := uuid.Parse(req.Context.MessageID)
	s.logMessage(ctx, msgID, actionReconcile, dbsqlc.MessageDirectionINBOUND, "/webhook/reconcile",
		req.Context.BapID, req.Context.BapURI, req.Context.NetworkID, raw, nil, nil)

	var outcomes []outcome
	replies := make([]any, 0, len(m.Contracts))
	contractIDs := []string{}
	for _, rawC := range m.Contracts {
		c := asMap(rawC)
		received := asMap(asMap(firstOf(slice(c["settlements"])))["settlementAttributes"])
		settlementID := str(asMap(firstOf(slice(c["settlements"])))["id"])
		reply, out := s.verify(ctx, c, received, settlementID, m.SettlementDate)
		replies = append(replies, reply)
		if out != nil {
			outcomes = append(outcomes, *out)
			contractIDs = append(contractIDs, out.id.String())
		}
	}
	summary := summaryOf(req.Context.BapID, s.cfg.BppID, m.SettlementDate, replies)
	status := summary.status()
	ctxOut := map[string]any{
		"version": firstNonEmpty(req.Context.Version, "2.0.0"), "action": "on_reconcile",
		"timestamp":     time.Now().UTC().Format(time.RFC3339),
		"messageId":     req.Context.MessageID,
		"transactionId": req.Context.TransactionID,
		"bapId":         req.Context.BapID, "bapUri": req.Context.BapURI,
		"bppId": s.cfg.BppID, "bppUri": s.cfg.BppURI,
		"senderId": s.cfg.BppID, "receiverId": req.Context.BapID,
		"ttl": firstNonEmpty(req.Context.TTL, "PT30S"), "networkId": req.Context.NetworkID,
	}
	on := map[string]any{"context": ctxOut, "message": map[string]any{
		"settlementDate": m.SettlementDate, "reconciliationStatus": status,
		"contracts": replies, "settlementSummary": summary,
	}}
	body, _ := json.Marshal(on)
	received, _ := json.Marshal(m.SettlementSummary)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// The exchange's batch: the one this transactionId already has, or a new
	// one named after the BAP that started it.
	var id string
	if err := tx.QueryRow(ctx, `SELECT id FROM reconciliations WHERE bpp_id = $1 AND direction = $2 AND transaction_id = $3`,
		s.cfg.BppID, directionInbound, req.Context.TransactionID).Scan(&id); err != nil {
		id = reconciliationID(time.Now().UTC(), firstNonEmpty(req.Context.SenderID, req.Context.BapID), s.idTaken(ctx))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO reconciliations (id, direction, bpp_id, bap_id, settlement_date, status, contract_ids,
		                             summary, request_message_id, request_payload, response_payload, transaction_id)
		VALUES ($1, $2, $3, $4, $5::date, $6, $7::uuid[], $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, contract_ids = EXCLUDED.contract_ids,
		    summary = EXCLUDED.summary, request_message_id = EXCLUDED.request_message_id,
		    request_payload = EXCLUDED.request_payload, response_payload = EXCLUDED.response_payload, updated_at = NOW()`,
		id, directionInbound, s.cfg.BppID, req.Context.BapID, m.SettlementDate, status, contractIDs,
		received, msgID, raw, body, req.Context.TransactionID); err != nil {
		return fmt.Errorf("record batch: %w", err)
	}
	for _, o := range outcomes {
		if err := setSettlementState(ctx, tx, o.id, o.contract, o.ids, id, o.status); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	respBody, sendErr := s.post(ctx, "on_reconcile", body)
	s.logMessage(ctx, msgID, actionOnReconcile, dbsqlc.MessageDirectionOUTBOUND, "/bpp/caller/on_reconcile",
		req.Context.BapID, req.Context.BapURI, req.Context.NetworkID, body, respBody, sendErr)
	if sendErr != nil {
		e := sendErr.Error()
		_, _ = s.pool.Exec(ctx, `UPDATE reconciliations SET error_message = $2, updated_at = NOW() WHERE id = $1`, id, e)
	}
	return sendErr
}

// outcome is what an answered contract records: its settlements' new state.
type outcome struct {
	id       uuid.UUID
	contract map[string]any
	ids      []string
	status   string
}

// verify answers one contract of a BAP's /reconcile: this BPP's own figures
// with accord (and the outcome to record), or a disagreement when the
// contract or settlement is not this BPP's. A settlement already agreed is
// agreed again, with nothing recorded: it is never paid twice.
func (s *Service) verify(ctx context.Context, c, received map[string]any, settlementID, settlementDate string) (map[string]any, *outcome) {
	reply := func(bag map[string]any) map[string]any {
		cp := map[string]any{}
		for k, v := range c {
			cp[k] = v
		}
		st := map[string]any{"id": settlementID, "settlementAttributes": bag}
		if first := asMap(firstOf(slice(c["settlements"]))); first["considerationId"] != nil {
			st["considerationId"] = first["considerationId"]
		}
		cp["settlements"] = []any{st}
		return cp
	}
	cid, err := uuid.Parse(str(c["id"]))
	if err != nil {
		return reply(disagree(received, codeMissingContract, "not a contract id of this provider app")), nil
	}
	contract, err := s.storedContract(ctx, s.pool, cid)
	if err != nil {
		return reply(disagree(received, codeMissingContract, "the provider app has no record of this contract")), nil
	}
	f, err := contractFigures(contract, s.terms())
	if err != nil || f.SettlementID != settlementID {
		return reply(disagree(received, codeMissingSettlement, fmt.Sprintf("settlement %s is not this contract's payment", settlementID))), nil
	}
	var createdAt time.Time
	if s.pool.QueryRow(ctx, `SELECT created_at FROM contracts WHERE id = $1`, cid).Scan(&createdAt) == nil {
		f.OrderDate = settlement.OrderDate(createdAt)
	}
	bag, accord, dispute := compare(received, f)
	bag["accord"] = accord
	if st := str(asMap(asMap(paymentOf(contract))["settlementAttributes"])["reconciliationStatus"]); st == "AGREED" || st == "ADJUSTED" {
		if accord {
			bag["dueDate"] = dueDate(settlementDate)
			return reply(bag), nil // agreed before: agreed again, not paid again
		}
	}
	status := "DISPUTED"
	if accord {
		bag["dueDate"] = dueDate(settlementDate)
		status = "AGREED"
	} else {
		bag["dispute"] = dispute
	}
	return reply(bag), &outcome{id: cid, contract: contract, ids: append([]string{f.SettlementID}, f.RefundSettlementIDs...), status: status}
}

// dueDate is when an agreed payout is due: the batch's settlement date, or
// today when that has passed.
func dueDate(settlementDate string) string {
	today := time.Now().UTC().Format("2006-01-02")
	if settlementDate > today {
		return settlementDate
	}
	return today
}

// querier is a pool or a transaction.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// storedContract is this BPP's latest full state of a contract: its
// contract_attributes, or -- for a contract whose full state is only in the
// message log (cancellation.LatestContract) -- its latest init/confirm.
func (s *Service) storedContract(ctx context.Context, q querier, id uuid.UUID) (map[string]any, error) {
	var txn uuid.UUID
	var attrs json.RawMessage
	if err := q.QueryRow(ctx, `SELECT transaction_id, contract_attributes FROM contracts WHERE id = $1 AND bpp_id = $2 AND deleted_at IS NULL`,
		id, s.cfg.BppID).Scan(&txn, &attrs); err != nil {
		return nil, err
	}
	contract, err := cancellation.LatestContract(ctx, s.pool, txn, attrs)
	if err != nil || contract == nil || contract["settlements"] == nil {
		return nil, fmt.Errorf("contract %s has no stored settlements", id)
	}
	contract["id"] = id.String()
	return contract, nil
}

// setSettlementState writes reconciliationId / reconciliationStatus onto the
// given settlements of a contract -- in its stored full contract
// (contract_attributes) and in the settlements table. An empty reconID takes
// them off again.
func setSettlementState(ctx context.Context, tx pgx.Tx, contractID uuid.UUID, contract map[string]any, settlementIDs []string, reconID, status string) error {
	include := map[string]bool{}
	for _, id := range settlementIDs {
		include[id] = true
	}
	for _, raw := range slice(contract["settlements"]) {
		st := asMap(raw)
		if include[str(st["id"])] {
			a := asMap(st["settlementAttributes"])
			if reconID == "" {
				delete(a, "reconciliationId")
				delete(a, "reconciliationStatus")
			} else if status == "" {
				// last in that batch, but not reconciled by it (sent early)
				a["reconciliationId"] = reconID
				delete(a, "reconciliationStatus")
			} else {
				a["reconciliationId"], a["reconciliationStatus"] = reconID, status
			}
		}
	}
	b, err := json.Marshal(contract)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE contracts SET contract_attributes = $2, updated_at = updated_at WHERE id = $1`, contractID, b); err != nil {
		return fmt.Errorf("store contract %s: %w", contractID, err)
	}
	for id := range include {
		var err error
		if reconID == "" || status == "" {
			_, err = tx.Exec(ctx, `
				UPDATE settlements SET settlement_attributes = settlement_attributes - 'reconciliationId' - 'reconciliationStatus',
				       updated_at = NOW()
				 WHERE contract_id = $1 AND id = $2`, contractID, id)
		} else {
			_, err = tx.Exec(ctx, `
				UPDATE settlements
				   SET settlement_attributes = COALESCE(settlement_attributes, '{}'::jsonb)
				       || jsonb_build_object('reconciliationId', $3::text, 'reconciliationStatus', $4::text),
				       updated_at = NOW()
				 WHERE contract_id = $1 AND id = $2`, contractID, id, reconID, status)
		}
		if err != nil {
			return fmt.Errorf("store settlement %s: %w", id, err)
		}
	}
	return nil
}

// idTaken reports whether a reconciliationId is already used.
func (s *Service) idTaken(ctx context.Context) func(string) bool {
	return func(candidate string) bool {
		var exists bool
		_ = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM reconciliations WHERE id = $1)`, candidate).Scan(&exists)
		return exists
	}
}

// post sends a signed-on-the-way request through the onix BPP caller.
// A non-2xx status or a NACK body is an error.
func (s *Service) post(ctx context.Context, action string, body []byte) ([]byte, error) {
	base := strings.TrimRight(s.cfg.BppCallerURL, "/")
	if base == "" {
		return nil, errors.New("BPP_CALLER_URL is not set")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/"+action, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s call failed: %w", action, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return respBody, fmt.Errorf("BAP answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if bytes.Contains(respBody, []byte(`"NACK"`)) {
		return respBody, fmt.Errorf("BAP NACKed: %s", strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

func (s *Service) logMessage(ctx context.Context, msgID uuid.UUID, action dbsqlc.BecknAction, dir dbsqlc.MessageDirection,
	url, bapID, bapURI, networkID string, payload, response []byte, callErr error) {
	ack := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
	var errMsg *string
	if callErr != nil {
		ack = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
		e := callErr.Error()
		errMsg = &e
	}
	var resp json.RawMessage
	if len(response) > 0 && json.Valid(response) {
		resp = response
	}
	bppID := s.cfg.BppID
	if err := dbsqlc.New(s.pool).InsertMessageLog(ctx, dbsqlc.InsertMessageLogParams{
		TransactionID:   pgtype.UUID{},
		MessageID:       msgID,
		Action:          action,
		Direction:       dir,
		Url:             &url,
		BapID:           &bapID,
		BapUri:          &bapURI,
		BppID:           &bppID,
		NetworkID:       &networkID,
		AckStatus:       ack,
		RequestPayload:  payload,
		ResponsePayload: resp,
		ErrorMessage:    errMsg,
	}); err != nil {
		s.lh.WithModule("reconsvc").Warn().Error(err).Log("failed to write beckn_message_log")
	}
}

func firstOf(v []any) any {
	if len(v) == 0 {
		return nil
	}
	return v[0]
}
