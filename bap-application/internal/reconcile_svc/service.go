// Package reconcilesvc is the BAP side of the Beckn reconcile extension
// (docs/specs/reconcile.yaml). A BPP sends /reconcile with the contracts whose
// settlement is due; this BAP checks every settlement against its own copy of
// each contract, agrees or disputes per contract, releases the payout from the
// payment gateway when everything agrees, and answers /on_reconcile.
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
	// mockGatewayName matches the stand-in gateway used for payments: the
	// payout is the gateway releasing the held funds to the seller.
	mockGatewayName = "mock-gateway"
)

type Service struct {
	pool       *pgxpool.Pool
	cfg        *config.Config
	lh         *logharbour.Logger
	httpClient *http.Client
}

func NewService(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Service {
	return &Service{pool: pool, cfg: cfg, lh: lh, httpClient: &http.Client{Timeout: 60 * time.Second}}
}

// Request is an inbound /reconcile.
type Request struct {
	Context Context `json:"context"`
	Message struct {
		ReconciliationID  string           `json:"reconciliationId"`
		SettlementDate    string           `json:"settlementDate"`
		Contracts         []map[string]any `json:"contracts"`
		SettlementSummary map[string]any   `json:"settlementSummary"`
	} `json:"message"`
}

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
	TTL           string `json:"ttl,omitempty"`
	NetworkID     string `json:"networkId"`
}

// Verdict is one contract's outcome (ContractReconciliation).
type Verdict struct {
	ContractID           string         `json:"contractId"`
	ReconciliationStatus string         `json:"reconciliationStatus"`
	ClaimedAmount        float64        `json:"claimedAmount"`
	Dispute              map[string]any `json:"dispute,omitempty"`

	txnID       uuid.UUID
	contract    map[string]any // this BAP's copy
	settlements []string       // settlement ids in this batch
}

// Summary mirrors the spec's SettlementSummary.
type Summary struct {
	BapID                     string  `json:"bapId"`
	BppID                     string  `json:"bppId"`
	Currency                  string  `json:"currency"`
	SettlementDate            string  `json:"settlementDate"`
	SettlementBasis           string  `json:"settlementBasis,omitempty"`
	SettlementWindow          string  `json:"settlementWindow,omitempty"`
	ContractCount             int     `json:"contractCount"`
	TotalPaidAmount           float64 `json:"totalPaidAmount"`
	TotalGatewayRefundAmount  float64 `json:"totalGatewayRefundAmount"`
	TotalProviderRefundAmount float64 `json:"totalProviderRefundAmount"`
	TotalFeesRetained         float64 `json:"totalFeesRetained"`
	TotalSettledAmount        float64 `json:"totalSettledAmount"`
	NetPayable                float64 `json:"netPayable"`
}

// Process verifies a /reconcile, records the outcome (batch, stamped contract
// snapshots, payout) and sends /on_reconcile.
func (s *Service) Process(ctx context.Context, raw []byte) error {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("invalid reconcile: %w", err)
	}
	m := req.Message
	batchTxn, _ := uuid.Parse(req.Context.TransactionID)
	msgID, _ := uuid.Parse(req.Context.MessageID)
	s.logMessage(ctx, batchTxn, msgID, actionReconcile, dbsqlc.MessageDirectionINBOUND, "/api/webhook/reconcile", req.Context, raw, nil, nil)

	verdicts, computed := s.verify(ctx, req)
	status := "AGREED"
	var batchDispute map[string]any
	for _, v := range verdicts {
		if v.ReconciliationStatus != "AGREED" {
			status = "DISPUTED"
		}
	}
	claimedNet := num(m.SettlementSummary["netPayable"])
	if status == "AGREED" && round2(claimedNet) != computed.NetPayable {
		status = "DISPUTED"
		batchDispute = map[string]any{"code": "SUMMARY_MISMATCH", "name": fmt.Sprintf(
			"claimed netPayable %v, buyer app computes %v", claimedNet, computed.NetPayable)}
	}

	var payout map[string]any
	if status == "AGREED" {
		payout = map[string]any{
			"status":   "COMPLETE",
			"amount":   computed.NetPayable,
			"currency": computed.Currency,
			"paidAt":   time.Now().UTC().Format(time.RFC3339),
			"method":   "BANK_TRANSFER",
			"gateway":  map[string]any{"name": mockGatewayName, "transactionId": uuid.New().String()},
		}
	}

	msg := map[string]any{
		"reconciliationId":     m.ReconciliationID,
		"reconciliationStatus": status,
		"contracts":            verdicts,
		"settlementSummary":    computed,
	}
	if payout != nil {
		msg["payout"] = payout
	}
	if batchDispute != nil {
		for i := range verdicts {
			if verdicts[i].Dispute == nil {
				verdicts[i].Dispute = batchDispute
			}
		}
	}
	on := map[string]any{"context": s.replyContext(req.Context), "message": msg}
	body, _ := json.Marshal(on)

	if err := s.record(ctx, req, raw, status, computed, verdicts, payout); err != nil {
		return err
	}

	respBody, sendErr := s.post(ctx, "on_reconcile", body)
	s.logMessage(ctx, batchTxn, msgID, actionOnReconcile, dbsqlc.MessageDirectionOUTBOUND, s.cfg.BapCallerURL+"/on_reconcile", req.Context, body, respBody, sendErr)
	var errMsg *string
	if sendErr != nil {
		e := sendErr.Error()
		errMsg = &e
	}
	if _, err := s.pool.Exec(ctx, `UPDATE reconciliations SET response_payload = $2, error_message = $3, updated_at = NOW() WHERE id = $1`,
		m.ReconciliationID, body, errMsg); err != nil {
		return err
	}
	return sendErr
}

// verify checks every contract of the batch against this BAP's latest copy:
// each settlement the batch reconciles must exist here with the same amounts
// and refund source. It returns the verdicts and the summary computed from
// this BAP's own figures.
func (s *Service) verify(ctx context.Context, req Request) ([]Verdict, Summary) {
	m := req.Message
	sm := Summary{
		BapID: s.cfg.BapID, BppID: req.Context.BppID, SettlementDate: m.SettlementDate, Currency: "IDR",
		SettlementBasis: str(m.SettlementSummary["settlementBasis"]), SettlementWindow: str(m.SettlementSummary["settlementWindow"]),
	}
	verdicts := make([]Verdict, 0, len(m.Contracts))
	for _, claimed := range m.Contracts {
		v := Verdict{ContractID: str(claimed["id"]), ReconciliationStatus: "AGREED"}
		var snap json.RawMessage
		err := s.pool.QueryRow(ctx, `
			SELECT transaction_id, contract FROM contract_snapshots
			 WHERE contract_id = $1 ORDER BY created_at DESC LIMIT 1`, v.ContractID).Scan(&v.txnID, &snap)
		if err != nil {
			v.dispute("UNKNOWN_CONTRACT", "the buyer app has no record of this contract")
			verdicts = append(verdicts, v)
			continue
		}
		_ = json.Unmarshal(snap, &v.contract)
		paid, gw, prov, currency := checkContract(&v, claimed, v.contract, m.ReconciliationID)
		if currency != "" {
			sm.Currency = currency
		}
		v.ClaimedAmount = round2(paid - gw)
		sm.ContractCount++
		sm.TotalPaidAmount += paid
		sm.TotalGatewayRefundAmount += gw
		sm.TotalProviderRefundAmount += prov
		for _, c := range slice(v.contract["consideration"]) {
			if id := str(asMap(c)["id"]); strings.HasPrefix(id, "cancellation-") || strings.HasPrefix(id, "restocking-") {
				sm.TotalFeesRetained += num(asMap(asMap(c)["considerationAttributes"])["totalAmount"])
			}
		}
		verdicts = append(verdicts, v)
	}
	sm.TotalPaidAmount = round2(sm.TotalPaidAmount)
	sm.TotalGatewayRefundAmount = round2(sm.TotalGatewayRefundAmount)
	sm.TotalProviderRefundAmount = round2(sm.TotalProviderRefundAmount)
	sm.TotalFeesRetained = round2(sm.TotalFeesRetained)
	sm.TotalSettledAmount = round2(sm.TotalPaidAmount - sm.TotalGatewayRefundAmount)
	sm.NetPayable = sm.TotalSettledAmount
	sort.SliceStable(verdicts, func(i, j int) bool { return verdicts[i].ContractID < verdicts[j].ContractID })
	return verdicts, sm
}

// dispute marks the verdict DISPUTED with the first reason found.
func (v *Verdict) dispute(code, text string) {
	if v.Dispute == nil {
		v.ReconciliationStatus = "DISPUTED"
		v.Dispute = map[string]any{"code": code, "name": text}
	}
}

// checkContract compares the settlements a /reconcile claims for batch
// reconID against this BAP's copy of the contract (ours), recording any
// dispute on v and the batch settlement ids. It returns the amounts from this
// BAP's own figures: payments, gateway-paid and provider-paid refunds.
func checkContract(v *Verdict, claimed, ours map[string]any, reconID string) (paid, gw, prov float64, currency string) {
	own := map[string]map[string]any{}
	for _, st := range slice(ours["settlements"]) {
		own[str(asMap(st)["id"])] = asMap(st)
	}
	for _, st := range slice(claimed["settlements"]) {
		cs := asMap(st)
		ca := asMap(cs["settlementAttributes"])
		if str(ca["reconciliationId"]) != reconID {
			continue
		}
		id := str(cs["id"])
		v.settlements = append(v.settlements, id)
		o, ok := own[id]
		if !ok {
			v.dispute("UNKNOWN_SETTLEMENT", fmt.Sprintf("settlement %s is not in the buyer app's records", id))
			continue
		}
		oa := asMap(o["settlementAttributes"])
		if c := str(oa["currency"]); c != "" && currency == "" {
			currency = c
		}
		cr, or := asMap(ca["refund"]), asMap(oa["refund"])
		switch {
		case (cr == nil) != (or == nil):
			v.dispute("SETTLEMENT_MISMATCH", fmt.Sprintf("settlement %s: payment vs refund differs", id))
		case cr == nil:
			if num(oa["settledAmount"]) != num(ca["settledAmount"]) {
				v.dispute("AMOUNT_MISMATCH", fmt.Sprintf("settlement %s: paid %v here, %v claimed", id, num(oa["settledAmount"]), num(ca["settledAmount"])))
			}
			paid += num(oa["settledAmount"])
		default:
			if num(or["amount"]) != num(cr["amount"]) {
				v.dispute("AMOUNT_MISMATCH", fmt.Sprintf("settlement %s: refund %v here, %v claimed", id, num(or["amount"]), num(cr["amount"])))
			}
			source := str(or["source"])
			if source == "" {
				source = "PAYMENT_GATEWAY" // refunds recorded before refund.source existed
			}
			if claimedSource := str(cr["source"]); claimedSource != "" && claimedSource != source {
				v.dispute("REFUND_SOURCE_MISMATCH", fmt.Sprintf("settlement %s: refund source %s here, %s claimed", id, source, claimedSource))
			}
			if source == "PROVIDER" {
				prov += num(or["amount"])
			} else {
				gw += num(or["amount"])
			}
		}
	}
	if len(v.settlements) == 0 {
		v.dispute("NO_SETTLEMENTS", "the contract carries no settlement for this reconciliation")
	}
	return paid, gw, prov, currency
}

// record stores the batch and, for every contract this BAP knows, a new
// snapshot of its own copy with the batch's settlements stamped with the
// outcome (reconciliationId / reconciliationStatus).
func (s *Service) record(ctx context.Context, req Request, raw []byte, status string, computed Summary, verdicts []Verdict, payout map[string]any) error {
	claimed, _ := json.Marshal(req.Message.SettlementSummary)
	comp, _ := json.Marshal(computed)
	vj, _ := json.Marshal(verdicts)
	var pj []byte
	if payout != nil {
		pj, _ = json.Marshal(payout)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO reconciliations (id, bpp_id, settlement_date, status, claimed_summary, computed_summary, contracts, payout, request_payload)
		VALUES ($1, $2, $3::date, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status, claimed_summary = EXCLUDED.claimed_summary,
		    computed_summary = EXCLUDED.computed_summary, contracts = EXCLUDED.contracts, payout = EXCLUDED.payout,
		    request_payload = EXCLUDED.request_payload, updated_at = NOW()`,
		req.Message.ReconciliationID, req.Context.BppID, req.Message.SettlementDate, status, claimed, comp, vj, pj, raw); err != nil {
		return fmt.Errorf("record reconciliation: %w", err)
	}
	for _, v := range verdicts {
		if v.contract == nil || len(v.settlements) == 0 {
			continue
		}
		stampOwn(v.contract, v.settlements, req.Message.ReconciliationID, v.ReconciliationStatus)
		b, _ := json.Marshal(v.contract)
		if err := insertSnapshot(ctx, tx, v.txnID, v.ContractID, b); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func insertSnapshot(ctx context.Context, tx pgx.Tx, txnID uuid.UUID, contractID string, contract []byte) error {
	if err := dbsqlc.New(tx).UpsertContractSnapshot(ctx, dbsqlc.UpsertContractSnapshotParams{
		TransactionID: txnID,
		Action:        actionReconcile,
		ContractID:    &contractID,
		Contract:      contract,
	}); err != nil {
		return fmt.Errorf("snapshot %s: %w", contractID, err)
	}
	return nil
}

// stampOwn marks this BAP's copies of the batch's settlements with the outcome.
func stampOwn(contract map[string]any, ids []string, reconID, status string) {
	include := map[string]bool{}
	for _, id := range ids {
		include[id] = true
	}
	for _, st := range slice(contract["settlements"]) {
		if include[str(asMap(st)["id"])] {
			a := asMap(asMap(st)["settlementAttributes"])
			a["reconciliationId"], a["reconciliationStatus"] = reconID, status
		}
	}
}

// replyContext is the /on_reconcile context: same transaction and message id
// as the /reconcile it answers (the BPP correlates on messageId).
func (s *Service) replyContext(in Context) Context {
	out := in
	out.Action = "on_reconcile"
	out.Timestamp = time.Now().UTC().Format(time.RFC3339)
	out.BapID, out.BapURI = s.cfg.BapID, s.cfg.BapURI
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

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func slice(v any) []any          { s, _ := v.([]any); return s }
func str(v any) string           { s, _ := v.(string); return s }
func num(v any) float64          { f, _ := v.(float64); return f }
func round2(f float64) float64   { return math.Round(f*100) / 100 }
