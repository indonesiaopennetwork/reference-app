package cancellation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/stock"
)

// SettlementTerms builds the settlementBasis / settlementWindow attributes
// (seller payout terms) for settlements; empty values are left out.
func SettlementTerms(basis, window string) map[string]any {
	t := map[string]any{}
	if basis != "" {
		t["settlementBasis"] = basis
	}
	if window != "" {
		t["settlementWindow"] = window
	}
	return t
}

// ErrNotActive is returned when the contract is no longer ACTIVE.
var ErrNotActive = errors.New("contract is not ACTIVE")

// LatestContract returns a contract's latest full state: contract_attributes
// when it holds a full contract (the return flow and cancellations keep it
// there), otherwise the most recent init/confirm payload in beckn_message_log.
func LatestContract(ctx context.Context, pool *pgxpool.Pool, txID uuid.UUID, contractAttributes json.RawMessage) (map[string]any, error) {
	var c map[string]any
	if len(contractAttributes) > 0 && json.Unmarshal(contractAttributes, &c) == nil && c["commitments"] != nil {
		return normalizeSettlementBasis(c), nil
	}
	var payload []byte
	if err := pool.QueryRow(ctx, `
		SELECT request_payload FROM beckn_message_log
		WHERE transaction_id = $1 AND action IN ('on_confirm', 'confirm', 'on_init', 'init')
		ORDER BY created_at DESC LIMIT 1`, txID).Scan(&payload); err != nil {
		return nil, fmt.Errorf("load contract from message log: %w", err)
	}
	var msg struct {
		Message struct {
			Contract map[string]any `json:"contract"`
		} `json:"message"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil || msg.Message.Contract == nil {
		return nil, fmt.Errorf("parse stored contract payload: %v", err)
	}
	return normalizeSettlementBasis(msg.Message.Contract), nil
}

// normalizeSettlementBasis renames a pre-ION settlementBasis (DELIVERY, ...)
// in c's settlements, so a contract loaded from an older message still
// validates when it is sent again.
func normalizeSettlementBasis(c map[string]any) map[string]any {
	settlements, _ := c["settlements"].([]any)
	for _, raw := range settlements {
		st, _ := raw.(map[string]any)
		attrs, _ := st["settlementAttributes"].(map[string]any)
		if basis, ok := attrs["settlementBasis"].(string); ok {
			attrs["settlementBasis"] = config.NormalizeSettlementBasis(basis)
		}
	}
	return c
}

// Execute cancels items (everything when empty) for req's reason on the
// transaction's ACTIVE contract and persists the outcome in one transaction: the new contract
// state (contract_attributes, and status CANCELLED when full), the refund
// settlement, and the stock returned by the cancelled units.
func Execute(ctx context.Context, pool *pgxpool.Pool, bppID string, txID uuid.UUID, items []Item, req Request) (*Result, error) {
	row, err := dbsqlc.New(pool).GetActiveContractByTxnID(ctx, dbsqlc.GetActiveContractByTxnIDParams{TransactionID: txID, BppID: bppID})
	if err != nil {
		return nil, fmt.Errorf("contract not found: %w", err)
	}
	if row.Status != dbsqlc.ContractStatusACTIVE {
		return nil, fmt.Errorf("%w (status %s)", ErrNotActive, row.Status)
	}
	contract, err := LatestContract(ctx, pool, txID, row.ContractAttributes)
	if err != nil {
		return nil, err
	}
	contract["id"] = row.ID.String()
	contract["status"] = map[string]any{"code": string(row.Status)}

	// The seller's dashboard records fulfillment progress in
	// contract_performances; check the cutoff against the furthest stage.
	if rows, err := pool.Query(ctx, `SELECT status_code FROM contract_performances WHERE contract_id = $1`, row.ID); err == nil {
		for rows.Next() {
			var code string
			if rows.Scan(&code) == nil && StageRank(code) > StageRank(req.FulfillmentStatus) {
				req.FulfillmentStatus = code
			}
		}
		rows.Close()
	}

	if req.Return {
		req.DeliveredAt = deliveredAt(ctx, pool, row.ID)
	}
	res, err := Apply(contract, items, req, time.Now())
	if err != nil {
		return nil, err
	}
	contractJSON, err := json.Marshal(res.Contract)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	status := dbsqlc.ContractStatusACTIVE
	if res.Full && req.Return {
		status = dbsqlc.ContractStatusCOMPLETE
	} else if res.Full {
		status = dbsqlc.ContractStatusCANCELLED
	}
	tag, err := tx.Exec(ctx,
		`UPDATE contracts SET status = $1, contract_attributes = $2, updated_at = NOW()
		  WHERE id = $3 AND bpp_id = $4 AND status = 'ACTIVE'::contract_status AND deleted_at IS NULL`,
		status, contractJSON, row.ID, bppID)
	if err != nil {
		return nil, fmt.Errorf("update contract: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotActive
	}

	if s := res.RefundSettlement; s != nil {
		attrs, _ := json.Marshal(s["settlementAttributes"])
		attrCtx, attrType := settlementContext, "ion:IONTradeSettlement"
		if err := dbsqlc.New(tx).UpsertSettlement(ctx, dbsqlc.UpsertSettlementParams{
			ID:                          str(s["id"]),
			ContractID:                  row.ID,
			ConsiderationID:             str(s["considerationId"]),
			Status:                      dbsqlc.SettlementStatusCOMMITTED,
			SettlementAttributes:        attrs,
			SettlementAttributesContext: &attrCtx,
			SettlementAttributesType:    &attrType,
		}); err != nil {
			return nil, fmt.Errorf("store refund settlement: %w", err)
		}
	}

	// Keep the commitments table in step: close each cancelled commitment and
	// add its ACTIVE replacement (same offer, remaining quantity).
	for oldID, replID := range res.Closed {
		if _, err := tx.Exec(ctx,
			`UPDATE commitments SET status = 'CLOSED'::commitment_status, updated_at = NOW()
			  WHERE contract_id = $1 AND id = $2`, row.ID, oldID); err != nil {
			return nil, fmt.Errorf("close commitment %s: %w", oldID, err)
		}
		if replID == "" {
			continue
		}
		attrs, _ := json.Marshal(commitmentByID(res.Contract, replID)["commitmentAttributes"])
		if _, err := tx.Exec(ctx,
			`INSERT INTO commitments (id, contract_id, status, commitment_attributes, commitment_attributes_context, commitment_attributes_type)
			 SELECT $3, contract_id, 'ACTIVE'::commitment_status, $4, commitment_attributes_context, commitment_attributes_type
			   FROM commitments WHERE contract_id = $1 AND id = $2
			 ON CONFLICT DO NOTHING`, row.ID, oldID, replID, attrs); err != nil {
			return nil, fmt.Errorf("add replacement commitment %s: %w", replID, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO commitment_offers (commitment_id, contract_id, offer_id, offer_descriptor, offer_attributes, resource_ids)
			 SELECT $3, contract_id, offer_id, offer_descriptor, offer_attributes, resource_ids
			   FROM commitment_offers WHERE contract_id = $1 AND commitment_id = $2`, row.ID, oldID, replID); err != nil {
			return nil, fmt.Errorf("copy offer for commitment %s: %w", replID, err)
		}
	}

	for resourceID, units := range res.RestoredUnits {
		if err := stock.Adjust(ctx, tx, bppID, resourceID, int(math.Round(units))); err != nil {
			return nil, fmt.Errorf("restore stock for %s: %w", resourceID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return res, nil
}

func commitmentByID(contract map[string]any, id string) map[string]any {
	for _, cm := range slice(contract["commitments"]) {
		if m := asMap(cm); str(m["id"]) == id {
			return m
		}
	}
	return nil
}

// deliveredAt is when the contract was delivered (contracts.delivered_at, set
// by the seller dashboard); zero when unknown, which leaves the return window
// unchecked.
func deliveredAt(ctx context.Context, pool *pgxpool.Pool, contractID uuid.UUID) time.Time {
	var t *time.Time
	if err := pool.QueryRow(ctx, `SELECT delivered_at FROM contracts WHERE id = $1`, contractID).Scan(&t); err != nil || t == nil {
		return time.Time{}
	}
	return *t
}

// StoreRejectedReturn records a refused return request as the contract's
// latest state (the RETURN performance as Return_Rejected, with the reason)
// and returns that contract for on_update.
func StoreRejectedReturn(ctx context.Context, pool *pgxpool.Pool, bppID string, txID uuid.UUID, perf map[string]any, reason string) (map[string]any, error) {
	row, err := dbsqlc.New(pool).GetActiveContractByTxnID(ctx, dbsqlc.GetActiveContractByTxnIDParams{TransactionID: txID, BppID: bppID})
	if err != nil {
		return nil, fmt.Errorf("contract not found: %w", err)
	}
	stored, err := LatestContract(ctx, pool, txID, row.ContractAttributes)
	if err != nil {
		return nil, err
	}
	stored["id"] = row.ID.String()
	stored["status"] = map[string]any{"code": string(row.Status)}
	c := RejectReturn(stored, perf, reason)
	b, _ := json.Marshal(c)
	if _, err := pool.Exec(ctx, `UPDATE contracts SET contract_attributes = $1, updated_at = NOW() WHERE id = $2 AND bpp_id = $3`, b, row.ID, bppID); err != nil {
		return nil, fmt.Errorf("store rejected return: %w", err)
	}
	return c, nil
}
