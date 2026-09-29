// Package sweep surfaces contract_update_requests that have been PENDING
// too long -- a nudge that a seller has update requests waiting on them,
// not an automatic recovery mechanism.
//
// This is deliberately read-only, unlike bap-application's own
// internal/sweep (which rolls back stuck transactions): a PENDING request
// genuinely needs a human decision (see updatesvc.Process's own doc on the
// "Update approval model"), so there's nothing safe to do automatically
// here except make the backlog visible. The threshold is measured in
// hours, not minutes, for the same reason as the BAP-side sweep: a
// legitimately slow seller review must never be logged the same way as a
// stuck/lost request.
package sweep

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
)

// DefaultStaleAfter is how long an update request may sit PENDING before
// this package logs it as a stale-review alert.
const DefaultStaleAfter = 24 * time.Hour

// DefaultInterval is how often RunLoop checks for stale pending requests.
const DefaultInterval = 30 * time.Minute

// RunOnce logs (never mutates) every contract_update_requests row that's
// been PENDING longer than staleAfter. Returns the number found.
func RunOnce(ctx context.Context, pool *pgxpool.Pool, lh *logharbour.Logger, staleAfter time.Duration) (int, error) {
	q := dbsqlc.New(pool)
	stale, err := q.ListStalePendingUpdateRequests(ctx, int32(staleAfter.Minutes()))
	if err != nil {
		return 0, err
	}
	for _, r := range stale {
		lh.WithModule("update_request_sweep").Warn().LogActivity(
			"update request has been PENDING seller review past the stale threshold",
			map[string]any{
				"update_request_id": r.ID.String(),
				"contract_id":       r.ContractID.String(),
				"created_at":        r.CreatedAt.Time,
				"stale_after":       staleAfter.String(),
			},
		)
	}
	return len(stale), nil
}

// RunLoop calls RunOnce on a ticker until ctx is cancelled. Single
// background goroutine for the process's lifetime, same pattern as
// bap-application's own sweep -- no new infra.
func RunLoop(ctx context.Context, pool *pgxpool.Pool, lh *logharbour.Logger, interval, staleAfter time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := RunOnce(ctx, pool, lh, staleAfter); err != nil {
				lh.WithModule("update_request_sweep").Warn().Error(err).Log("update request sweep run failed")
			}
		}
	}
}
