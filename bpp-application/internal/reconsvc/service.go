package reconsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	return Terms{Basis: s.cfg.SettlementBasis, Window: s.cfg.SettlementWindow, Account: map[string]any{
		"holderRole":    "BPP",
		"bankCode":      s.cfg.SettlementBankCode,
		"accountNumber": s.cfg.SettlementAccountNumber,
		"accountName":   s.cfg.SettlementAccountName,
	}}
}

// Batch is a reconciliations row.
type Batch struct {
	ID              string          `json:"id"`
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
		SELECT r.id, r.direction, r.bap_id, r.settlement_date::text, r.status, r.contract_ids::text[], r.summary,
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
		if err := rows.Scan(&b.ID, &b.Direction, &b.BapID, &b.SettlementDate, &b.Status, &b.ContractIDs, &b.Summary,
			&b.RequestPayload, &b.ResponsePayload, &b.ErrorMessage, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Send builds this provider's /reconcile for one BAP -- the selected
// contracts, or by default the batch the BAP disputed (again in full) or else
// every contract due now; see choose -- and sends it through the onix
// BPP caller. The included payments are marked PENDING in the batch before
// sending and taken back out if it is not delivered.
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
	if id == "" {
		id = reconciliationID(now, s.cfg.BppID, s.idTaken(ctx))
	}
	summary := summarize(bapID, s.cfg.BppID, due, date)
	contracts := make([]any, 0, len(due))
	contractIDs := make([]string, 0, len(due))
	for _, d := range due {
		contracts = append(contracts, snapshot(d.contract, d.Figures, d.Figures.bag()))
		contractIDs = append(contractIDs, d.ContractID.String())
	}

	msgID := uuid.New()
	network := firstNonEmpty(due[0].NetworkID, s.cfg.NetworkID)
	req := map[string]any{
		"context": map[string]any{
			"version": "2.0.0", "action": "reconcile",
			"timestamp":     now.Format(time.RFC3339),
			"messageId":     msgID.String(),
			"transactionId": uuid.New().String(), // the batch, not any one order
			"bapId":         bapID, "bapUri": bapURI,
			"bppId": s.cfg.BppID, "bppUri": s.cfg.BppURI,
			"senderId": s.cfg.BppID, "receiverId": bapID,
			"ttl": "PT30S", "networkId": network,
		},
		"message": map[string]any{
			"reconciliationId":  id,
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
		                             summary, request_message_id, request_payload)
		VALUES ($1, $2, $3, $4, $5, $6::date, 'SENDING', $7::uuid[], $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET status = 'SENDING', settlement_date = EXCLUDED.settlement_date,
		    contract_ids = EXCLUDED.contract_ids, summary = EXCLUDED.summary,
		    request_message_id = EXCLUDED.request_message_id, request_payload = EXCLUDED.request_payload,
		    error_message = NULL, updated_at = NOW()`,
		id, directionOutbound, s.cfg.BppID, bapID, providerID, date, contractIDs, summaryJSON, msgID, body); err != nil {
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
	batch := &Batch{ID: id, Direction: directionOutbound, BapID: bapID, SettlementDate: date, Status: status,
		ContractIDs: contractIDs, Summary: summaryJSON, RequestPayload: body, ErrorMessage: errMsg, CreatedAt: now, UpdatedAt: now}
	if sendErr != nil {
		return batch, fmt.Errorf("reconcile not delivered: %w", sendErr)
	}
	return batch, nil
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
		ReconciliationID     string         `json:"reconciliationId"`
		ReconciliationStatus string         `json:"reconciliationStatus"`
		SettlementDate       string         `json:"settlementDate"`
		Contracts            []any          `json:"contracts"`
		SettlementSummary    map[string]any `json:"settlementSummary"`
	} `json:"message"`
}

// codeBeforeWindow is the BAP's dispute code for a contract sent before its
// settlement window has passed.
const codeBeforeWindow = "SETTLEMENT_WINDOW_NOT_REACHED"

// ApplyOnReconcile records the BAP's answer to a /reconcile this BPP sent:
// the batch status and response, and its payments' outcome -- all AGREED
// when the BAP agrees with every one (SEAM then has the PJP pay them out),
// otherwise all DISPUTED, except a contract the BAP found not yet due
// (codeBeforeWindow), which leaves the batch to be sent again once due.
func (s *Service) ApplyOnReconcile(ctx context.Context, raw []byte) error {
	var req beckn
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("invalid on_reconcile: %w", err)
	}
	m := req.Message
	msgID, _ := uuid.Parse(req.Context.MessageID)
	defer s.logMessage(ctx, msgID, actionOnReconcile, dbsqlc.MessageDirectionINBOUND, "/webhook/on_reconcile",
		req.Context.BapID, req.Context.BapURI, req.Context.NetworkID, raw, nil, nil)

	// Agreed only as a whole: the BAP's status and every accord, leaving
	// out contracts sent before their settlement window (PENDING when that
	// is all of them).
	agreed, total, early := 0, 0, 0
	for _, rawC := range m.Contracts {
		total++
		if str(asMap(asMap(asMap(firstOf(slice(asMap(rawC)["settlements"])))["settlementAttributes"])["dispute"])["code"]) == codeBeforeWindow {
			early++
			continue
		}
		all := len(slice(asMap(rawC)["settlements"])) > 0
		for _, st := range slice(asMap(rawC)["settlements"]) {
			if accord, _ := asMap(asMap(st)["settlementAttributes"])["accord"].(bool); !accord {
				all = false
			}
		}
		if all {
			agreed++
		}
	}
	status := batchOutcome(agreed, total-early, early)
	if status == "AGREED" && m.ReconciliationStatus != "AGREED" {
		status = "DISPUTED"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `
		UPDATE reconciliations SET status = $2, response_payload = $3, updated_at = NOW()
		 WHERE id = $1 AND bpp_id = $4 AND direction = $5`, m.ReconciliationID, status, raw, s.cfg.BppID, directionOutbound)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("unknown reconciliationId %q", m.ReconciliationID)
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
		reconID, outcome := m.ReconciliationID, contractOutcome(status)
		if str(asMap(asMap(asMap(firstOf(slice(c["settlements"])))["settlementAttributes"])["dispute"])["code"]) == codeBeforeWindow {
			// Sent before its settlement window: not reconciled by the batch
			// (it keeps only the batch id, for the reason); sent again once due.
			outcome = ""
		}
		if err := setSettlementState(ctx, tx, cid, contract, batchSettlements(contract, m.ReconciliationID), reconID, outcome); err != nil {
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

// Respond answers a BAP's /reconcile: it checks every contract's figures
// against this BPP's own, replies /on_reconcile with its figures and accord
// per settlement, and records the batch. Its payments become AGREED here too
// when the whole batch is agreed; otherwise all of them stay DISPUTED for the
// BAP to send again.
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
	agreed := 0
	for _, rawC := range m.Contracts {
		c := asMap(rawC)
		received := asMap(asMap(firstOf(slice(c["settlements"])))["settlementAttributes"])
		settlementID := str(asMap(firstOf(slice(c["settlements"])))["id"])
		reply, out := s.verify(ctx, c, received, settlementID, m.SettlementDate)
		replies = append(replies, reply)
		if out != nil {
			outcomes = append(outcomes, *out)
			contractIDs = append(contractIDs, out.id.String())
			if out.status == "AGREED" {
				agreed++
			}
		}
	}
	status := batchStatus(agreed, len(m.Contracts))
	for i := range outcomes {
		outcomes[i].status = contractOutcome(status)
	}
	summary := summaryOf(req.Context.BapID, s.cfg.BppID, m.SettlementDate, replies)
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
		"reconciliationId": m.ReconciliationID, "reconciliationStatus": status,
		"contracts": replies, "settlementSummary": summary,
	}}
	body, _ := json.Marshal(on)
	received, _ := json.Marshal(m.SettlementSummary)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO reconciliations (id, direction, bpp_id, bap_id, settlement_date, status, contract_ids,
		                             summary, request_message_id, request_payload, response_payload)
		VALUES ($1, $2, $3, $4, $5::date, $6, $7::uuid[], $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, contract_ids = EXCLUDED.contract_ids,
		    summary = EXCLUDED.summary, request_payload = EXCLUDED.request_payload,
		    response_payload = EXCLUDED.response_payload, updated_at = NOW()`,
		m.ReconciliationID, directionInbound, s.cfg.BppID, req.Context.BapID, m.SettlementDate, status, contractIDs,
		received, msgID, raw, body); err != nil {
		return fmt.Errorf("record batch: %w", err)
	}
	for _, o := range outcomes {
		if err := setSettlementState(ctx, tx, o.id, o.contract, o.ids, m.ReconciliationID, o.status); err != nil {
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
		_, _ = s.pool.Exec(ctx, `UPDATE reconciliations SET error_message = $2, updated_at = NOW() WHERE id = $1`, m.ReconciliationID, e)
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
// contract or settlement is not this BPP's or is already settled.
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
		return reply(disagree(received, "UNKNOWN_CONTRACT", "not a contract id of this provider app")), nil
	}
	contract, err := s.storedContract(ctx, s.pool, cid)
	if err != nil {
		return reply(disagree(received, "UNKNOWN_CONTRACT", "the provider app has no record of this contract")), nil
	}
	f, err := contractFigures(contract, s.terms())
	if err != nil || f.SettlementID != settlementID {
		return reply(disagree(received, "UNKNOWN_SETTLEMENT", fmt.Sprintf("settlement %s is not this contract's payment", settlementID))), nil
	}
	if st := str(asMap(asMap(paymentOf(contract))["settlementAttributes"])["reconciliationStatus"]); st == "AGREED" || st == "ADJUSTED" {
		return reply(disagree(received, "ALREADY_SETTLED", "this contract's settlement is already agreed")), nil
	}
	bag, accord, dispute := compare(received, f)
	bag["accord"] = accord
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

// storedContract is this BPP's latest full state of a contract.
func (s *Service) storedContract(ctx context.Context, q querier, id uuid.UUID) (map[string]any, error) {
	var attrs json.RawMessage
	if err := q.QueryRow(ctx, `SELECT contract_attributes FROM contracts WHERE id = $1 AND bpp_id = $2 AND deleted_at IS NULL`,
		id, s.cfg.BppID).Scan(&attrs); err != nil {
		return nil, err
	}
	var contract map[string]any
	if err := json.Unmarshal(attrs, &contract); err != nil || contract == nil || contract["settlements"] == nil {
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
