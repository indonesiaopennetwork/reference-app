package dashboardsvc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/ion/winroom/bpp/internal/cancellation"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/settlement"
)

// A delivered contract becomes COMPLETE when its settlement basis event has
// passed: at delivery for AFTER_DELIVERY, and for AFTER_SHIPMENT and
// ON_CONFIRMATION too (shipment isn't recorded on its own, and an order isn't
// complete before it arrives); for AFTER_RETURN_WINDOW once the longest
// return window after delivery has closed. It can still be returned while
// COMPLETE (see cancellation.Execute).

// completeAt is when a contract delivered at deliveredAt becomes COMPLETE.
// defaultBasis applies when the contract states none.
func completeAt(contract map[string]any, defaultBasis string, deliveredAt time.Time) time.Time {
	basis, _, _ := settlement.Terms(contract)
	if basis == "" {
		basis = defaultBasis
	}
	if config.NormalizeSettlementBasis(basis) == "AFTER_RETURN_WINDOW" {
		return deliveredAt.Add(cancellation.LongestReturnWindow(contract))
	}
	return deliveredAt
}

// completeIfDue makes the delivered ACTIVE contract id COMPLETE when
// completeAt has passed, and reports whether it did.
func (h *Handler) completeIfDue(ctx context.Context, id uuid.UUID, now time.Time) bool {
	var txn uuid.UUID
	var attrs json.RawMessage
	var deliveredAt *time.Time
	if err := h.pool.QueryRow(ctx, `
		SELECT transaction_id, contract_attributes, delivered_at FROM contracts
		 WHERE id = $1 AND bpp_id = $2 AND status = 'ACTIVE' AND deleted_at IS NULL`, id, h.cfg.BppID).
		Scan(&txn, &attrs, &deliveredAt); err != nil || deliveredAt == nil {
		return false
	}
	contract, err := cancellation.LatestContract(ctx, h.pool, txn, attrs)
	if err != nil || now.Before(completeAt(contract, h.cfg.SettlementBasis, *deliveredAt)) {
		return false
	}
	tag, err := h.pool.Exec(ctx, `
		UPDATE contracts SET status = 'COMPLETE', updated_at = NOW()
		 WHERE id = $1 AND bpp_id = $2 AND status = 'ACTIVE'`, id, h.cfg.BppID)
	if err != nil {
		h.lh.WithModule("dashboardsvc").Warn().Error(err).Log("failed to complete contract " + id.String())
		return false
	}
	return tag.RowsAffected() == 1
}

// completeDelivered completes every delivered ACTIVE contract whose time
// has come -- those waiting for a return window, and any delivered before
// this rule -- and tells the buyer app with an unsolicited on_status.
func (h *Handler) completeDelivered(ctx context.Context) {
	rows, err := h.pool.Query(ctx, `
		SELECT id FROM contracts
		 WHERE bpp_id = $1 AND status = 'ACTIVE' AND delivered_at IS NOT NULL AND deleted_at IS NULL`, h.cfg.BppID)
	if err != nil {
		h.lh.WithModule("dashboardsvc").Warn().Error(err).Log("completion sweep failed")
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	now := time.Now()
	for _, id := range ids {
		if h.completeIfDue(ctx, id, now) {
			h.triggerUnsolicitedOnStatus(id, "Delivered")
		}
	}
}
