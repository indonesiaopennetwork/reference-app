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

// Batch is a reconciliations row.
type Batch struct {
	ID              string          `json:"id"`
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

// Batches lists this provider's reconciliation batches, newest first.
func (s *Service) Batches(ctx context.Context, providerID string, limit int) ([]Batch, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, bap_id, settlement_date::text, status, contract_ids::text[], summary,
		       request_payload, response_payload, error_message, created_at, updated_at
		  FROM reconciliations WHERE bpp_id = $1 AND provider_id = $2
		 ORDER BY created_at DESC LIMIT $3`, s.cfg.BppID, providerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Batch{}
	for rows.Next() {
		var b Batch
		if err := rows.Scan(&b.ID, &b.BapID, &b.SettlementDate, &b.Status, &b.ContractIDs, &b.Summary,
			&b.RequestPayload, &b.ResponsePayload, &b.ErrorMessage, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Send builds this provider's /reconcile for one BAP from every contract due
// now and sends it through the onix BPP caller. Only when the BAP ACKs are the
// included settlements stamped with the reconciliationId (PENDING); either
// way the batch is recorded (SENT or FAILED).
func (s *Service) Send(ctx context.Context, providerID, bapID string) (*Batch, error) {
	now := time.Now().UTC()
	all, err := s.Contracts(ctx, providerID, now)
	if err != nil {
		return nil, err
	}
	var due []DueContract
	for _, d := range all {
		if d.Due && d.BapID == bapID {
			due = append(due, d)
		}
	}
	if len(due) == 0 {
		return nil, ErrNothingDue
	}

	date := now.Format("2006-01-02")
	id := reconciliationID(now, bapID, func(candidate string) bool {
		var exists bool
		_ = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM reconciliations WHERE id = $1)`, candidate).Scan(&exists)
		return exists
	})
	summary := s.summarize(bapID, due, date)
	contracts := make([]any, 0, len(due))
	stamped := make(map[uuid.UUID]map[string]any, len(due))
	contractIDs := make([]string, 0, len(due))
	for _, d := range due {
		c, err := stamp(d, id)
		if err != nil {
			return nil, err
		}
		contracts = append(contracts, c)
		stamped[d.ContractID] = c
		contractIDs = append(contractIDs, d.ContractID.String())
	}

	msgID := uuid.New()
	network := due[0].NetworkID
	if network == "" {
		network = s.cfg.NetworkID
	}
	req := map[string]any{
		"context": map[string]any{
			"version": "2.0.0", "action": "reconcile",
			"timestamp":     now.Format(time.RFC3339),
			"messageId":     msgID.String(),
			"transactionId": uuid.New().String(), // the batch, not any one order
			"bapId":         bapID, "bapUri": due[0].BapURI,
			"bppId": s.cfg.BppID, "bppUri": s.cfg.BppURI,
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

	// Record the batch and stamp its settlements BEFORE sending: the BAP may
	// answer /on_reconcile before our HTTP call returns, and must find both.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO reconciliations (id, bpp_id, bap_id, provider_id, settlement_date, status, contract_ids,
		                             summary, request_message_id, request_payload)
		VALUES ($1, $2, $3, $4, $5::date, 'SENDING', $6::uuid[], $7, $8, $9)`,
		id, s.cfg.BppID, bapID, providerID, date, contractIDs, summaryJSON, msgID, body); err != nil {
		return nil, fmt.Errorf("record batch: %w", err)
	}
	for _, d := range due {
		if err := setSettlementState(ctx, tx, d.ContractID, stamped[d.ContractID], d.SettlementIDs, id, "PENDING"); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	respBody, sendErr := s.post(ctx, "reconcile", body)
	s.logMessage(ctx, msgID, actionReconcile, dbsqlc.MessageDirectionOUTBOUND, "/bpp/caller/reconcile", bapID, due[0].BapURI, network, body, respBody, sendErr)

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
	batch := &Batch{ID: id, BapID: bapID, SettlementDate: date, Status: status, ContractIDs: contractIDs,
		Summary: summaryJSON, RequestPayload: body, ErrorMessage: errMsg, CreatedAt: now, UpdatedAt: now}
	if sendErr != nil {
		return batch, fmt.Errorf("reconcile not delivered: %w", sendErr)
	}
	return batch, nil
}

// revert marks an undelivered batch FAILED and takes its settlements back out
// (their earlier, unstamped state), so they go into the next batch.
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
		b, err := json.Marshal(d.contract)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE contracts SET contract_attributes = $2 WHERE id = $1`, d.ContractID, b); err != nil {
			return fmt.Errorf("restore contract %s: %w", d.ContractID, err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE settlements SET settlement_attributes = settlement_attributes - 'reconciliationId' - 'reconciliationStatus', updated_at = NOW()
			 WHERE contract_id = $1 AND settlement_attributes->>'reconciliationId' = $2`, d.ContractID, id); err != nil {
			return fmt.Errorf("restore settlements %s: %w", d.ContractID, err)
		}
	}
	return tx.Commit(ctx)
}

// OnReconcileRequest is an inbound /on_reconcile (docs/specs/reconcile.yaml).
type OnReconcileRequest struct {
	Context struct {
		MessageID string `json:"messageId"`
		BapID     string `json:"bapId"`
		BapURI    string `json:"bapUri"`
		NetworkID string `json:"networkId"`
	} `json:"context"`
	Message struct {
		ReconciliationID     string `json:"reconciliationId"`
		ReconciliationStatus string `json:"reconciliationStatus"`
		Contracts            []struct {
			ContractID           string `json:"contractId"`
			ReconciliationStatus string `json:"reconciliationStatus"`
		} `json:"contracts"`
	} `json:"message"`
}

// ApplyOnReconcile records the BAP's outcome: the batch status and response,
// and each contract's verdict on the settlements it had in this batch (e.g.
// AGREED -- after which later refunds on that contract are provider-paid).
func (s *Service) ApplyOnReconcile(ctx context.Context, raw []byte) error {
	var req OnReconcileRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("invalid on_reconcile: %w", err)
	}
	m := req.Message
	msgID, _ := uuid.Parse(req.Context.MessageID)
	defer s.logMessage(ctx, msgID, actionOnReconcile, dbsqlc.MessageDirectionINBOUND, "/webhook/on_reconcile",
		req.Context.BapID, req.Context.BapURI, req.Context.NetworkID, raw, nil, nil)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `
		UPDATE reconciliations SET status = $2, response_payload = $3, updated_at = NOW()
		 WHERE id = $1 AND bpp_id = $4`, m.ReconciliationID, m.ReconciliationStatus, raw, s.cfg.BppID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("unknown reconciliationId %q", m.ReconciliationID)
	}
	for _, cv := range m.Contracts {
		cid, err := uuid.Parse(cv.ContractID)
		if err != nil || cv.ReconciliationStatus == "" {
			continue
		}
		var attrs json.RawMessage
		if err := tx.QueryRow(ctx, `SELECT contract_attributes FROM contracts WHERE id = $1 AND bpp_id = $2`, cid, s.cfg.BppID).Scan(&attrs); err != nil {
			continue
		}
		var contract map[string]any
		if json.Unmarshal(attrs, &contract) != nil || contract == nil {
			continue
		}
		var ids []string
		for _, rawSt := range slice(contract["settlements"]) {
			st := asMap(rawSt)
			if str(asMap(st["settlementAttributes"])["reconciliationId"]) == m.ReconciliationID {
				ids = append(ids, str(st["id"]))
			}
		}
		if err := setSettlementState(ctx, tx, cid, contract, ids, m.ReconciliationID, cv.ReconciliationStatus); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// setSettlementState writes reconciliationId / reconciliationStatus onto the
// given settlements of a contract -- in its stored full contract
// (contract_attributes) and in the settlements table.
func setSettlementState(ctx context.Context, tx pgx.Tx, contractID uuid.UUID, contract map[string]any, settlementIDs []string, reconID, status string) error {
	include := map[string]bool{}
	for _, id := range settlementIDs {
		include[id] = true
	}
	for _, raw := range slice(contract["settlements"]) {
		st := asMap(raw)
		if include[str(st["id"])] {
			a := asMap(st["settlementAttributes"])
			a["reconciliationId"], a["reconciliationStatus"] = reconID, status
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
		if _, err := tx.Exec(ctx, `
			UPDATE settlements
			   SET settlement_attributes = COALESCE(settlement_attributes, '{}'::jsonb)
			       || jsonb_build_object('reconciliationId', $3::text, 'reconciliationStatus', $4::text),
			       updated_at = NOW()
			 WHERE contract_id = $1 AND id = $2`, contractID, id, reconID, status); err != nil {
			return fmt.Errorf("store settlement %s: %w", id, err)
		}
	}
	return nil
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
