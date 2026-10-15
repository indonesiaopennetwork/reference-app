// Package reconcilesvc is the BAP side of ION settlement reconciliation (ION
// Release 1 core/api/v2.0.0/ion-additional.yaml in ion-specs). Either
// participant may send /reconcile: this BAP answers a BPP's /reconcile by
// checking each contract's figures against its own copy, sends its own
// /reconcile for contracts that are due, and applies the BPP's /on_reconcile.
// The buyer's payment is held at the PJP; once both sides agree a contract
// (accord true) SEAM has the PJP pay it out, reading the agreement from the
// message log -- nothing about the payout is recorded here.
package reconcilesvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
	"github.com/ion/winroom/bap/internal/config"
)

const (
	actionReconcile   = dbsqlc.BecknAction("reconcile")
	actionOnReconcile = dbsqlc.BecknAction("on_reconcile")

	// Batch directions: INBOUND is a /reconcile a BPP sent, OUTBOUND one this
	// BAP sent.
	directionInbound  = "INBOUND"
	directionOutbound = "OUTBOUND"
)

// defaultTerms are the payout terms assumed for a contract whose seller
// stated none (a contract agreed before the terms were carried).
var defaultTerms = Terms{Basis: "AFTER_DELIVERY", Window: "P1D"}

// terms are defaultTerms with this BAP's own payout account (see
// Terms.Account).
func (s *Service) terms() Terms {
	t := defaultTerms
	t.Account = s.account()
	return t
}

// account is this BAP's payout account (for the finder fee).
func (s *Service) account() map[string]any {
	return map[string]any{
		"holderRole":    "BAP",
		"bankCode":      s.cfg.SettlementBankCode,
		"accountNumber": s.cfg.SettlementAccountNumber,
		"accountName":   s.cfg.SettlementAccountName,
	}
}

// ErrNothingDue is returned when no contract with a BPP is due.
var ErrNothingDue = errors.New("no contracts are due for reconciliation with this seller")

type Service struct {
	pool       *pgxpool.Pool
	cfg        *config.Config
	lh         *logharbour.Logger
	httpClient *http.Client
}

func NewService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Service {
	return &Service{pool: pool, cfg: cfg, lh: lh, httpClient: &http.Client{Timeout: 60 * time.Second}}
}

// Context is a Beckn context.
type Context struct {
	Version       string `json:"version"`
	Action        string `json:"action"`
	Timestamp     string `json:"timestamp"`
	MessageID     string `json:"messageId"`
	TransactionID string `json:"transactionId"`
	BapID         string `json:"bapId"`
	BapURI        string `json:"bapUri"`
	BppID         string `json:"bppId"`
	BppURI        string `json:"bppUri"`
	SenderID      string `json:"senderId,omitempty"`
	ReceiverID    string `json:"receiverId,omitempty"`
	TTL           string `json:"ttl,omitempty"`
	NetworkID     string `json:"networkId"`
}

// Request is an inbound /reconcile or /on_reconcile.
type Request struct {
	Context Context `json:"context"`
	Message struct {
		ReconciliationID     string         `json:"reconciliationId"`
		ReconciliationStatus string         `json:"reconciliationStatus"`
		SettlementDate       string         `json:"settlementDate"`
		Contracts            []any          `json:"contracts"`
		SettlementSummary    map[string]any `json:"settlementSummary"`
	} `json:"message"`
}

// Verdict is one contract's outcome, as stored on the batch.
type Verdict struct {
	ContractID   string         `json:"contractId"`
	SettlementID string         `json:"settlementId"`
	Accord       bool           `json:"accord"`
	Dispute      map[string]any `json:"dispute,omitempty"`
	NetPayable   float64        `json:"netPayable"`
	Commission   float64        `json:"commission"`
}

// Summary is a settlementSummary.
type Summary struct {
	BapID            string  `json:"bapId"`
	BppID            string  `json:"bppId"`
	Currency         string  `json:"currency"`
	SettlementDate   string  `json:"settlementDate"`
	ContractCount    int     `json:"contractCount"`
	TotalOrderAmount float64 `json:"totalOrderAmount"`
	TotalCommission  float64 `json:"totalCommission"`
	TotalWithholding float64 `json:"totalWithholding"`
	TotalRefund      float64 `json:"totalRefund"`
	TotalNetPayable  float64 `json:"totalNetPayable"`
	// AgreedCount / DisputedCount count the contracts by accord; an
	// /on_reconcile answer only.
	AgreedCount   *int `json:"agreedCount,omitempty"`
	DisputedCount *int `json:"disputedCount,omitempty"`
}

// stored is this BAP's latest copy of a contract.
type stored struct {
	txnID    uuid.UUID
	id       string
	contract map[string]any
}

// Process answers a BPP's /reconcile: each contract's figures against this
// BAP's own copy, accord per settlement, the batch recorded and every
// contract's outcome stamped on a new snapshot -- AGREED only when the whole
// batch is (see batchStatus) -- then /on_reconcile.
func (s *Service) Process(ctx context.Context, raw []byte) error {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("invalid reconcile: %w", err)
	}
	m := req.Message
	batchTxn, _ := uuid.Parse(req.Context.TransactionID)
	msgID, _ := uuid.Parse(req.Context.MessageID)
	s.logMessage(ctx, batchTxn, msgID, actionReconcile, dbsqlc.MessageDirectionINBOUND, "/api/webhook/reconcile", req.Context, raw, nil, nil)

	replies := make([]any, 0, len(m.Contracts))
	verdicts := make([]Verdict, 0, len(m.Contracts))
	type stamp struct {
		st     stored
		ids    []string
		status string
		early  bool
	}
	var stamps []stamp
	// Contracts whose settlement window hasn't passed here are answered
	// accord false, CodeBeforeWindow; they keep only this batch's id (for
	// the reason) and are reconciled once due.
	// RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW only lets this BAP send early.
	early := map[string]string{}
	if list, err := s.Contracts(ctx, req.Context.BppID, time.Now().UTC()); err == nil {
		for _, d := range list {
			if reason, ok := beforeWindow(d); ok {
				early[d.ContractID] = reason
			}
		}
	}
	agreed, sentEarly := 0, 0
	for _, rawC := range m.Contracts {
		c := asMap(rawC)
		first := asMap(firstOf(slice(c["settlements"])))
		received := asMap(first["settlementAttributes"])
		v := Verdict{ContractID: str(c["id"]), SettlementID: str(first["id"])}
		bag, own, f := s.verify(ctx, c, received, v.SettlementID, m.SettlementDate)
		reason, tooEarly := early[v.ContractID]
		if tooEarly && own != nil {
			bag["accord"] = false
			delete(bag, "dueDate")
			bag["dispute"] = map[string]any{"code": CodeBeforeWindow, "description": reason}
		}
		v.Accord, _ = bag["accord"].(bool)
		v.Dispute = asMap(bag["dispute"])
		v.NetPayable = num(asMap(bag["netPayable"])["value"])
		v.Commission = num(asMap(bag["commission"])["value"])
		replies = append(replies, reply(c, v.SettlementID, first["considerationId"], bag))
		verdicts = append(verdicts, v)
		switch {
		case tooEarly && own != nil:
			sentEarly++
		case v.Accord:
			agreed++
		}
		if own != nil {
			stamps = append(stamps, stamp{*own, append([]string{f.SettlementID}, f.RefundSettlementIDs...), "", tooEarly})
		}
	}
	status := batchOutcome(agreed, len(m.Contracts)-sentEarly, sentEarly)
	for i := range stamps {
		if !stamps[i].early { // an early one keeps only the batch id
			stamps[i].status = contractOutcome(status)
		}
	}
	computed := summaryOf(s.cfg.BapID, req.Context.BppID, m.SettlementDate, replies)
	on := map[string]any{"context": s.replyContext(req.Context), "message": map[string]any{
		"reconciliationId": m.ReconciliationID, "reconciliationStatus": status,
		"contracts": replies, "settlementSummary": computed,
	}}
	body, _ := json.Marshal(on)

	claimed, _ := json.Marshal(m.SettlementSummary)
	comp, _ := json.Marshal(computed)
	vj, _ := json.Marshal(verdicts)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO reconciliations (id, direction, bpp_id, settlement_date, status, claimed_summary, computed_summary,
		                             contracts, request_message_id, request_payload, response_payload)
		VALUES ($1, $2, $3, $4::date, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, claimed_summary = EXCLUDED.claimed_summary,
		    computed_summary = EXCLUDED.computed_summary, contracts = EXCLUDED.contracts,
		    request_payload = EXCLUDED.request_payload, response_payload = EXCLUDED.response_payload, updated_at = NOW()`,
		m.ReconciliationID, directionInbound, req.Context.BppID, m.SettlementDate, status, claimed, comp, vj, msgID, raw, body); err != nil {
		return fmt.Errorf("record reconciliation: %w", err)
	}
	for _, st := range stamps {
		if err := stampOwn(ctx, tx, st.st, st.ids, m.ReconciliationID, st.status); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	respBody, sendErr := s.post(ctx, "on_reconcile", body)
	s.logMessage(ctx, batchTxn, msgID, actionOnReconcile, dbsqlc.MessageDirectionOUTBOUND, s.cfg.BapCallerURL+"/on_reconcile", req.Context, body, respBody, sendErr)
	if sendErr != nil {
		e := sendErr.Error()
		_, _ = s.pool.Exec(ctx, `UPDATE reconciliations SET error_message = $2, updated_at = NOW() WHERE id = $1`, m.ReconciliationID, e)
	}
	return sendErr
}

// verify answers one received contract: the reply bag with accord, and --
// when this BAP holds the contract and can judge it -- its copy and figures.
func (s *Service) verify(ctx context.Context, c, received map[string]any, settlementID, settlementDate string) (map[string]any, *stored, *Figures) {
	own, err := s.latest(ctx, s.pool, str(c["id"]))
	if err != nil {
		return disagree(received, "UNKNOWN_CONTRACT", "the buyer app has no record of this contract"), nil, nil
	}
	f, err := contractFigures(own.contract, s.terms())
	if err != nil || f.SettlementID != settlementID {
		return disagree(received, "UNKNOWN_SETTLEMENT", fmt.Sprintf("settlement %s is not this contract's payment", settlementID)), nil, nil
	}
	if st := str(asMap(asMap(paymentOf(own.contract))["settlementAttributes"])["reconciliationStatus"]); st == "AGREED" || st == "ADJUSTED" {
		return disagree(received, "ALREADY_SETTLED", "this contract's settlement is already agreed"), nil, nil
	}
	bag, accord, dispute := compare(received, f)
	bag["accord"] = accord
	if accord {
		bag["dueDate"] = dueDate(settlementDate)
	} else {
		bag["dispute"] = dispute
	}
	return bag, own, f
}

// reply is a received contract with its settlement replaced by the answer.
func reply(c map[string]any, settlementID string, considerationID any, bag map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range c {
		out[k] = v
	}
	st := map[string]any{"id": settlementID, "settlementAttributes": bag}
	if considerationID != nil {
		st["considerationId"] = considerationID
	}
	out["settlements"] = []any{st}
	return out
}

// DueContract is one of this BAP's contracts with a BPP whose settlement is
// not yet agreed, and when it falls due.
type DueContract struct {
	ContractID           string `json:"contract_id"`
	TransactionID        string `json:"transaction_id"`
	BppID                string `json:"bpp_id"`
	BppURI               string `json:"bpp_uri"`
	Status               string `json:"status"`
	ReconciliationStatus string `json:"reconciliation_status,omitempty"`
	// ReconciliationID is the batch the contract was last in.
	ReconciliationID string `json:"reconciliation_id,omitempty"`
	// InNextBatch: the contract goes in this BAP's next /reconcile (see
	// nextBatch).
	InNextBatch bool `json:"in_next_batch"`
	// Selectable: the operator can choose the contract for a /reconcile (due
	// or disputed; see choose).
	Selectable bool `json:"selectable"`
	// CreatedAt is when the contract was created: its first snapshot.
	CreatedAt *time.Time `json:"created_at"`
	// LastResult: for a contract its last batch didn't reconcile (disputed,
	// or sent before its settlement window), how it fared there.
	LastResult *Result    `json:"last_result,omitempty"`
	BasisEvent string     `json:"basis_event"`
	DueAt      *time.Time `json:"due_at"`
	Due        bool       `json:"due"`
	*Figures

	own stored
	// windowDue: the settlement window has passed (Due before
	// RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW=false makes everything due).
	windowDue bool
}

// Contracts lists this BAP's contracts whose settlement is not yet agreed
// (due ones first), optionally only those with one BPP.
func (s *Service) Contracts(ctx context.Context, bppID string, now time.Time) ([]DueContract, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.transaction_id, COALESCE(t.bpp_id, ''), COALESCE(t.bpp_uri, ''), t.status::text,
		       s.contract_id, s.contract, s.created_at,
		       (SELECT min(created_at) FROM contract_snapshots f WHERE f.transaction_id = t.transaction_id),
		       (SELECT min(created_at) FROM contract_snapshots d WHERE d.transaction_id = t.transaction_id
		          AND (d.contract->'performance' @> '[{"status":{"code":"Delivered"}}]'
		            OR d.contract->'performance' @> '[{"status":{"code":"DELIVERED"}}]'))
		  FROM transactions t
		  JOIN LATERAL (SELECT contract_id, contract, created_at FROM contract_snapshots
		                 WHERE transaction_id = t.transaction_id ORDER BY created_at DESC LIMIT 1) s ON TRUE
		 WHERE t.status IN ('CONFIRMED', 'CANCELLED', 'COMPLETE', 'UPDATE_SENT', 'CANCEL_SENT', 'STATUS_SENT')
		   AND ($1 = '' OR t.bpp_id = $1)`, bppID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueContract
	for rows.Next() {
		var d DueContract
		var txn uuid.UUID
		var contractID *string
		var raw json.RawMessage
		var snapAt time.Time
		var deliveredAt *time.Time
		if err := rows.Scan(&txn, &d.BppID, &d.BppURI, &d.Status, &contractID, &raw, &snapAt, &d.CreatedAt, &deliveredAt); err != nil {
			return nil, err
		}
		var contract map[string]any
		if json.Unmarshal(unwrap(raw), &contract) != nil || contract == nil || contractID == nil {
			continue
		}
		contract["id"] = *contractID
		d.ContractID, d.TransactionID = *contractID, txn.String()
		d.own = stored{txnID: txn, id: *contractID, contract: contract}
		if !assess(&d, deliveredAt, snapAt, now) {
			continue
		}
		d.Figures.Accounts = withAccount(d.Figures.Accounts, s.account())
		d.windowDue = d.Due
		if s.cfg.ReconcileBeforeWindow { // RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW=false
			d.Due = true
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Due != out[j].Due {
			return out[i].Due
		}
		if out[i].DueAt == nil || out[j].DueAt == nil {
			return out[j].DueAt == nil && out[i].DueAt != nil
		}
		return out[i].DueAt.Before(*out[j].DueAt)
	})
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	s.attachResults(ctx, out)
	return out, nil
}

// assess fills d's figures and due time; false when there is nothing to
// reconcile (no payment, or it is pending in a batch or already agreed).
func assess(d *DueContract, deliveredAt *time.Time, snapAt, now time.Time) bool {
	payment := paymentOf(d.own.contract)
	if payment == nil {
		return false
	}
	pa := asMap(payment["settlementAttributes"])
	switch status := str(pa["reconciliationStatus"]); status {
	case "PENDING", "AGREED", "ADJUSTED":
		return false
	default:
		d.ReconciliationStatus = status
		d.ReconciliationID = str(pa["reconciliationId"])
	}
	f, err := contractFigures(d.own.contract, defaultTerms)
	if err != nil {
		return false
	}
	d.Figures = f
	var basisAt *time.Time
	paidAt, paidErr := time.Parse(time.RFC3339, str(pa["paidAt"]))
	switch {
	case f.Basis == "ON_CONFIRMATION" && paidErr == nil:
		d.BasisEvent, basisAt = "confirmed", &paidAt
	case deliveredAt != nil:
		d.BasisEvent, basisAt = "delivered", deliveredAt
	case str(asMap(d.own.contract["status"])["code"]) == "CANCELLED" || d.Status == "CANCELLED":
		d.BasisEvent, basisAt = "cancelled", &snapAt
	default:
		d.BasisEvent = "awaiting delivery"
		return true
	}
	if f.Basis == "AFTER_RETURN_WINDOW" && d.BasisEvent == "delivered" {
		// This BAP doesn't track the seller's return windows; it leaves
		// these contracts to the seller to reconcile.
		d.BasisEvent = "awaiting return window"
		return true
	}
	window, err := parseISODuration(f.Window)
	if err != nil {
		window = 0
	}
	due := basisAt.Add(window)
	d.DueAt = &due
	d.Due = !now.Before(due)
	return true
}

// Send sends this BAP's /reconcile to a BPP: the selected contracts, or by
// default the batch it disputed (again in full, under the same
// reconciliationId) or else every contract with it due now; see choose.
func (s *Service) Send(ctx context.Context, bppID string, selected []string) (*BatchView, error) {
	now := time.Now().UTC()
	all, err := s.Contracts(ctx, bppID, now)
	if err != nil {
		return nil, err
	}
	var due []DueContract
	for _, d := range all {
		if selectable(d) {
			due = append(due, d)
		}
	}
	id, due := choose(due, s.cfg.BapID, selected)
	if len(due) == 0 {
		return nil, ErrNothingDue
	}
	// The BPP's URI, from any order with it that has one recorded.
	bppURI := ""
	for _, d := range all {
		if bppURI == "" {
			bppURI = d.BppURI
		}
	}
	date := now.Format("2006-01-02")
	if id == "" {
		id = reconciliationID(now, s.cfg.BapID, func(candidate string) bool {
			var exists bool
			_ = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM reconciliations WHERE id = $1)`, candidate).Scan(&exists)
			return exists
		})
	}
	contracts := make([]any, 0, len(due))
	for _, d := range due {
		contracts = append(contracts, snapshot(d.own.contract, d.Figures, d.Figures.bag()))
	}
	summary := summaryOf(s.cfg.BapID, bppID, date, contracts)
	msgID, batchTxn := uuid.New(), uuid.New()
	rc := Context{
		Version: "2.0.0", Action: "reconcile", Timestamp: now.Format(time.RFC3339),
		MessageID: msgID.String(), TransactionID: batchTxn.String(),
		BapID: s.cfg.BapID, BapURI: s.cfg.BapURI, BppID: bppID, BppURI: bppURI,
		SenderID: s.cfg.BapID, ReceiverID: bppID, TTL: "PT30S", NetworkID: s.cfg.NetworkID,
	}
	body, _ := json.Marshal(map[string]any{"context": rc, "message": map[string]any{
		"reconciliationId": id, "settlementDate": date, "contracts": contracts, "settlementSummary": summary,
	}})
	sj, _ := json.Marshal(summary)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO reconciliations (id, direction, bpp_id, settlement_date, status, claimed_summary,
		                             request_message_id, request_payload)
		VALUES ($1, $2, $3, $4::date, 'SENDING', $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET status = 'SENDING', settlement_date = EXCLUDED.settlement_date,
		    claimed_summary = EXCLUDED.claimed_summary, request_message_id = EXCLUDED.request_message_id,
		    request_payload = EXCLUDED.request_payload, error_message = NULL, updated_at = NOW()`,
		id, directionOutbound, bppID, date, sj, msgID, body); err != nil {
		return nil, fmt.Errorf("record batch: %w", err)
	}
	for _, d := range due {
		if err := stampOwn(ctx, tx, d.own, append([]string{d.SettlementID}, d.RefundSettlementIDs...), id, "PENDING"); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	respBody, sendErr := s.post(ctx, "reconcile", body)
	s.logMessage(ctx, batchTxn, msgID, actionReconcile, dbsqlc.MessageDirectionOUTBOUND, s.cfg.BapCallerURL+"/reconcile", rc, body, respBody, sendErr)
	status := "SENT"
	var errMsg *string
	if sendErr == nil {
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
	b := &BatchView{ID: id, Direction: directionOutbound, SettlementDate: date, Status: status,
		ClaimedSummary: sj, RequestPayload: body, ErrorMessage: errMsg, CreatedAt: now}
	if sendErr != nil {
		return b, fmt.Errorf("reconcile not delivered: %w", sendErr)
	}
	return b, nil
}

// revert marks an undelivered batch FAILED and restores its contracts'
// earlier reconciliation state, so they go into the next batch.
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
		previous := ""
		if d.ReconciliationStatus != "" {
			previous = str(asMap(asMap(paymentOf(d.own.contract))["settlementAttributes"])["reconciliationId"])
		}
		if err := stampOwn(ctx, tx, d.own, append([]string{d.SettlementID}, d.RefundSettlementIDs...), previous, d.ReconciliationStatus); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ApplyOnReconcile records a BPP's answer to this BAP's /reconcile: the
// batch outcome, and every contract AGREED when the whole batch is agreed,
// otherwise DISPUTED -- the batch is then sent again in full.
func (s *Service) ApplyOnReconcile(ctx context.Context, raw []byte) error {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("invalid on_reconcile: %w", err)
	}
	m := req.Message
	batchTxn, _ := uuid.Parse(req.Context.TransactionID)
	msgID, _ := uuid.Parse(req.Context.MessageID)
	defer s.logMessage(ctx, batchTxn, msgID, actionOnReconcile, dbsqlc.MessageDirectionINBOUND, "/api/webhook/on_reconcile", req.Context, raw, nil, nil)

	verdicts := make([]Verdict, 0, len(m.Contracts))
	agreed := 0
	for _, rawC := range m.Contracts {
		c := asMap(rawC)
		first := asMap(firstOf(slice(c["settlements"])))
		bag := asMap(first["settlementAttributes"])
		accord, _ := bag["accord"].(bool)
		if accord {
			agreed++
		}
		verdicts = append(verdicts, Verdict{ContractID: str(c["id"]), SettlementID: str(first["id"]), Accord: accord,
			Dispute: asMap(bag["dispute"]), NetPayable: num(asMap(bag["netPayable"])["value"]), Commission: num(asMap(bag["commission"])["value"])})
	}
	// Agreed only as a whole: the BPP's status and every accord.
	status := batchStatus(agreed, len(verdicts))
	if m.ReconciliationStatus != "AGREED" {
		status = "DISPUTED"
	}
	comp, _ := json.Marshal(m.SettlementSummary)
	vj, _ := json.Marshal(verdicts)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `
		UPDATE reconciliations SET status = $2, computed_summary = $3, contracts = $4, response_payload = $5, updated_at = NOW()
		 WHERE id = $1 AND direction = $6`, m.ReconciliationID, status, comp, vj, raw, directionOutbound)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("unknown reconciliationId %q", m.ReconciliationID)
	}
	for _, v := range verdicts {
		own, err := s.latest(ctx, tx, v.ContractID)
		if err != nil {
			continue
		}
		if err := stampOwn(ctx, tx, *own, batchSettlements(own.contract, m.ReconciliationID), m.ReconciliationID, contractOutcome(status)); err != nil {
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

// querier is a pool or a transaction.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// latest is this BAP's latest snapshot of a contract.
func (s *Service) latest(ctx context.Context, q querier, contractID string) (*stored, error) {
	var txn uuid.UUID
	var raw json.RawMessage
	if err := q.QueryRow(ctx, `
		SELECT transaction_id, contract FROM contract_snapshots
		 WHERE contract_id = $1 ORDER BY created_at DESC LIMIT 1`, contractID).Scan(&txn, &raw); err != nil {
		return nil, err
	}
	var contract map[string]any
	if err := json.Unmarshal(unwrap(raw), &contract); err != nil || contract == nil {
		return nil, fmt.Errorf("contract %s: unreadable snapshot", contractID)
	}
	contract["id"] = contractID
	return &stored{txnID: txn, id: contractID, contract: contract}, nil
}

// stampOwn records a new snapshot of this BAP's copy with the given
// settlements marked reconciliationId / reconciliationStatus (or unmarked when
// reconID is empty).
func stampOwn(ctx context.Context, tx pgx.Tx, own stored, ids []string, reconID, status string) error {
	include := map[string]bool{}
	for _, id := range ids {
		include[id] = true
	}
	for _, st := range slice(own.contract["settlements"]) {
		if include[str(asMap(st)["id"])] {
			a := asMap(asMap(st)["settlementAttributes"])
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
	b, err := json.Marshal(own.contract)
	if err != nil {
		return err
	}
	if err := dbsqlc.New(tx).UpsertContractSnapshot(ctx, dbsqlc.UpsertContractSnapshotParams{
		TransactionID: own.txnID,
		Action:        actionReconcile,
		ContractID:    &own.id,
		Contract:      b,
	}); err != nil {
		return fmt.Errorf("snapshot %s: %w", own.id, err)
	}
	return nil
}

// summaryOf totals the reconciliation bags of a message.
func summaryOf(bapID, bppID, date string, contracts []any) Summary {
	sm := Summary{BapID: bapID, BppID: bppID, SettlementDate: date, Currency: "IDR"}
	answered := false
	agreed, disputed := 0, 0
	for _, raw := range contracts {
		sm.ContractCount++
		contractAgreed := true
		for _, st := range slice(asMap(raw)["settlements"]) {
			b := asMap(asMap(st)["settlementAttributes"])
			if accord, ok := b["accord"].(bool); ok {
				answered = true
				contractAgreed = contractAgreed && accord
			}
			if c := str(b["currency"]); c != "" {
				sm.Currency = c
			}
			sm.TotalOrderAmount += num(asMap(b["orderAmount"])["value"])
			sm.TotalCommission += num(asMap(b["commission"])["value"])
			sm.TotalWithholding += num(asMap(b["withholdingAmount"])["value"])
			sm.TotalRefund += num(asMap(b["refundAmount"])["value"])
			sm.TotalNetPayable += num(asMap(b["netPayable"])["value"])
		}
		if contractAgreed {
			agreed++
		} else {
			disputed++
		}
	}
	if answered {
		sm.AgreedCount, sm.DisputedCount = &agreed, &disputed
	}
	sm.TotalOrderAmount = round2(sm.TotalOrderAmount)
	sm.TotalCommission = round2(sm.TotalCommission)
	sm.TotalWithholding = round2(sm.TotalWithholding)
	sm.TotalRefund = round2(sm.TotalRefund)
	sm.TotalNetPayable = round2(sm.TotalNetPayable)
	return sm
}

// reconciliationID names a batch by when it is sent and who sends it:
// recon-<settlement date>T<HHMMSS>Z-<senderId>.
func reconciliationID(at time.Time, senderID string, taken func(string) bool) string {
	base := fmt.Sprintf("recon-%sZ-%s", at.UTC().Format("2006-01-02T150405"), senderID)
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		if id := fmt.Sprintf("%s-%d", base, n); !taken(id) {
			return id
		}
	}
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

// replyContext is the /on_reconcile context: same transaction and message id
// as the /reconcile it answers (the sender correlates on messageId).
func (s *Service) replyContext(in Context) Context {
	out := in
	out.Action = "on_reconcile"
	out.Timestamp = time.Now().UTC().Format(time.RFC3339)
	out.BapID, out.BapURI = s.cfg.BapID, s.cfg.BapURI
	out.SenderID, out.ReceiverID = s.cfg.BapID, in.BppID
	if out.Version == "" {
		out.Version = "2.0.0"
	}
	return out
}

// post sends through the onix BAP caller; a non-2xx or NACK is an error.
func (s *Service) post(ctx context.Context, action string, body []byte) ([]byte, error) {
	base := strings.TrimRight(s.cfg.BapCallerURL, "/")
	if base == "" {
		return nil, errors.New("BAP_CALLER_URL is not set")
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
		return respBody, fmt.Errorf("BPP answered HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if bytes.Contains(respBody, []byte(`"NACK"`)) {
		return respBody, fmt.Errorf("BPP NACKed: %s", strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

func (s *Service) logMessage(ctx context.Context, txn, msgID uuid.UUID, action dbsqlc.BecknAction, dir dbsqlc.MessageDirection,
	url string, c Context, payload, response []byte, callErr error) {
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
	if err := dbsqlc.New(s.pool).InsertMessageLog(ctx, dbsqlc.InsertMessageLogParams{
		TransactionID:   pgtype.UUID{Bytes: txn, Valid: txn != uuid.Nil},
		MessageID:       msgID,
		Action:          action,
		Direction:       dir,
		Url:             &url,
		BapID:           &c.BapID,
		BapUri:          &c.BapURI,
		BppID:           &c.BppID,
		BppUri:          &c.BppURI,
		NetworkID:       &c.NetworkID,
		AckStatus:       ack,
		RequestPayload:  payload,
		ResponsePayload: resp,
		ErrorMessage:    errMsg,
	}); err != nil {
		s.lh.WithModule("reconcile_svc").Warn().Error(err).Log("message log insert failed")
	}
}

func firstOf(v []any) any {
	if len(v) == 0 {
		return nil
	}
	return v[0]
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func slice(v any) []any          { s, _ := v.([]any); return s }
func str(v any) string           { s, _ := v.(string); return s }
func num(v any) float64          { f, _ := v.(float64); return f }
func round2(f float64) float64   { return math.Round(f*100) / 100 }
