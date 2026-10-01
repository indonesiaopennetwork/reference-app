// Package stock adjusts resource_stock by an order's actual quantities.
package stock

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is satisfied by *pgxpool.Pool and pgx.Tx.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Adjust moves units between on-hand and sold for one resource: a negative
// delta sells units (confirm), a positive delta returns them (cancellation).
// Neither counter goes below zero.
func Adjust(ctx context.Context, db Execer, bppID, resourceID string, delta int) error {
	_, err := db.Exec(ctx,
		`UPDATE resource_stock
		    SET quantity = GREATEST(0, quantity + $3), sold = GREATEST(0, sold - $3), updated_at = NOW()
		  WHERE resource_id = $1 AND bpp_id = $2`,
		resourceID, bppID, delta)
	return err
}
