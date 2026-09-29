// Package sweep is a last-resort safety net for transactions stuck at
// UPDATE_SENT with no on_update ever arriving.
//
// It deliberately does NOT duplicate clientsvc.Update's own synchronous
// rollback-on-send-failure -- that already handles the common case (BAP
// never even managed to deliver the request to the BPP) immediately, no
// sweep needed. What this package catches is the case that can't be
// detected synchronously: delivery succeeded (BPP ACKed it), but no
// solicited on_update ever came back -- which, since bpp-application now
// queues an update for seller approval rather than applying it
// automatically (see updatesvc.Process), is *expected* to sometimes take a
// long time waiting on a human, not evidence of a technical failure.
//
// That's why the threshold this package uses is deliberately long (hours,
// not the seconds/minutes scale of a normal delivery timeout) -- it's not
// trying to detect "the request is taking a while," it's trying to detect
// "this was almost certainly abandoned or lost," so a legitimately slow
// seller review is never mistaken for a stuck transaction.
package sweep

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	dbsqlc "github.com/ion/winroom/bap/db/sqlc"
)

// DefaultStaleAfter is how long a transaction may sit at UPDATE_SENT before
// this package treats it as abandoned rather than "awaiting seller review."
// Deliberately generous -- see the package doc.
const DefaultStaleAfter = 48 * time.Hour

// DefaultInterval is how often RunLoop checks for stale transactions.
const DefaultInterval = 30 * time.Minute

// RunOnce checks for and rolls back every transaction that's been at
// UPDATE_SENT longer than staleAfter, logging each one it recovers.
// Returns the number of transactions rolled back.
func RunOnce(ctx context.Context, pool *pgxpool.Pool, lh *logharbour.Logger, staleAfter time.Duration) (int, error) {
	q := dbsqlc.New(pool)
	txnIDs, err := q.RollBackStaleUpdateSent(ctx, int32(staleAfter.Minutes()))
	if err != nil {
		return 0, err
	}

	// The transaction is back at CONFIRMED, so its tracking row must not
	// stay PENDING -- otherwise the buyer's order view keeps reporting
	// "awaiting seller review" for a request nothing is waiting on any
	// more. Best-effort: failing to close out the tracking row must not
	// undo the rollback that already succeeded.
	if len(txnIDs) > 0 {
		if _, err := q.AbandonPendingUpdateRequests(ctx, txnIDs); err != nil {
			lh.WithModule("update_sweep").Warn().Error(err).Log("rolled back stale UPDATE_SENT transactions but failed to mark their update requests ABANDONED")
		}
	}

	for _, id := range txnIDs {
		lh.WithModule("update_sweep").Warn().LogActivity(
			"update sweep: transaction was stuck at UPDATE_SENT past the stale threshold (no on_update ever arrived) -- rolled back to CONFIRMED so a retry is possible",
			map[string]any{"transaction_id": id.String(), "stale_after": staleAfter.String()},
		)
	}
	return len(txnIDs), nil
}

// RunLoop calls RunOnce on a ticker until ctx is cancelled. Intended to run
// as a single background goroutine for the process's lifetime -- no new
// infra (queue, cron service, etc.), matching this app's existing bias
// toward simple, in-process background work (see e.g. discoverwait).
func RunLoop(ctx context.Context, pool *pgxpool.Pool, lh *logharbour.Logger, interval, staleAfter time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := RunOnce(ctx, pool, lh, staleAfter); err != nil {
				lh.WithModule("update_sweep").Warn().Error(err).Log("update sweep run failed")
			}
		}
	}
}
