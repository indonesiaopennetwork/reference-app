package clientsvc

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ion/winroom/bap/internal/auth"
)

// HandleGetOrder returns full details for a single order transaction,
// scoped to the requesting buyer (if one is resolved -- see
// internal/auth.OptionalAuth). A transaction with no matching
// buyer_subject is treated as not found, same as one that doesn't exist,
// so a buyer can't view another buyer's order by guessing its UUID.
// GET /api/v1/orders/:id
func (h *ClientHandler) HandleGetOrder(c *gin.Context) {
	txnID := c.Param("id")
	if txnID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id required"})
		return
	}
	buyerSubject, hasBuyer := auth.SubjectFromContext(c)
	if !hasBuyer {
		c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
		return
	}
	resp, err := h.svc.GetStatusForBuyer(c.Request.Context(), txnID, buyerSubject)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
		return
	}

	// Also collect all contract snapshots for the timeline
	const snapshotsSQL = `
		SELECT action, contract, created_at
		FROM contract_snapshots
		WHERE transaction_id = $1
		ORDER BY created_at ASC`
	rows, err := h.svc.pool.Query(c.Request.Context(), snapshotsSQL, txnID)
	type SnapshotEntry struct {
		Action    string          `json:"action"`
		Contract  json.RawMessage `json:"contract"`
		CreatedAt string          `json:"created_at"`
	}
	snapshots := []SnapshotEntry{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var s SnapshotEntry
			var contract []byte
			var ts pgtype.Timestamptz
			if err := rows.Scan(&s.Action, &contract, &ts); err == nil {
				if contract != nil {
					s.Contract = json.RawMessage(contract)
				}
				if ts.Valid {
					s.CreatedAt = ts.Time.UTC().Format(time.RFC3339)
				}
				snapshots = append(snapshots, s)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"transaction_id": resp.TransactionID,
		"status":         resp.Status,
		"updated_at":     resp.UpdatedAt.UTC().Format(time.RFC3339),
		"contract":       resp.Contract,
		"snapshots":      snapshots,
	})
}

// HandleListOrders returns paginated order history, scoped to the
// requesting buyer (if one is resolved -- see internal/auth.OptionalAuth).
// A request with no resolvable buyer identity gets an empty page rather
// than falling back to every order ever placed through this BAP.
// GET /api/v1/orders?page=1&limit=20
func (h *ClientHandler) HandleListOrders(c *gin.Context) {
	ctx := c.Request.Context()

	buyerSubject, hasBuyer := auth.SubjectFromContext(c)
	if !hasBuyer {
		c.JSON(http.StatusOK, gin.H{"items": []any{}, "total": 0, "page": 1, "limit": 20})
		return
	}

	limit := 20
	page := 1
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if v := c.Query("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	offset := (page - 1) * limit

	const q = `
		SELECT
		    t.transaction_id,
		    t.bpp_id,
		    t.status,
		    t.created_at,
		    t.updated_at,
		    cs.contract,
		    cs.action AS latest_action
		FROM transactions t
		LEFT JOIN LATERAL (
		    SELECT contract, action
		    FROM contract_snapshots
		    WHERE transaction_id = t.transaction_id
		    ORDER BY updated_at DESC
		    LIMIT 1
		) cs ON true
		WHERE t.buyer_subject = $1
		ORDER BY t.created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := h.svc.pool.Query(ctx, q, buyerSubject, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type OrderItem struct {
		TransactionID string          `json:"transaction_id"`
		BppID         string          `json:"bpp_id"`
		Status        string          `json:"status"`
		CreatedAt     string          `json:"created_at"`
		UpdatedAt     string          `json:"updated_at"`
		Contract      json.RawMessage `json:"contract"`
		LatestAction  string          `json:"latest_action"`
		// Extracted from contract for quick display
		ItemName  string  `json:"item_name"`
		ItemPrice float64 `json:"item_price"`
		Currency  string  `json:"currency"`
	}

	items := []OrderItem{}
	for rows.Next() {
		var item OrderItem
		var bppID *string
		var contract []byte
		var action *string
		var createdAt, updatedAt pgtype.Timestamptz

		if err := rows.Scan(
			&item.TransactionID,
			&bppID,
			&item.Status,
			&createdAt,
			&updatedAt,
			&contract,
			&action,
		); err != nil {
			continue
		}
		if bppID != nil {
			item.BppID = *bppID
		}
		if action != nil {
			item.LatestAction = *action
		}
		if contract != nil {
			item.Contract = json.RawMessage(contract)
		} else {
			item.Contract = json.RawMessage(`{}`)
		}
		if createdAt.Valid {
			item.CreatedAt = createdAt.Time.UTC().Format(time.RFC3339)
		}
		if updatedAt.Valid {
			item.UpdatedAt = updatedAt.Time.UTC().Format(time.RFC3339)
		}

		// Extract item name, price, and return status from the contract snapshot for quick display
		var contractData struct {
			Commitments []struct {
				Offer struct {
					Descriptor struct {
						Name string `json:"name"`
					} `json:"descriptor"`
				} `json:"offer"`
				CommitmentAttributes struct {
					Price struct {
						Value    float64 `json:"value"`
						Currency string  `json:"currency"`
					} `json:"price"`
				} `json:"commitmentAttributes"`
			} `json:"commitments"`
			Performance []struct {
				Status struct {
					Code string `json:"code"`
				} `json:"status"`
				PerformanceAttributes struct {
					Type string `json:"type"`
				} `json:"performanceAttributes"`
			} `json:"performance"`
		}

		if len(item.Contract) > 2 {
			if err := json.Unmarshal(item.Contract, &contractData); err == nil {
				if len(contractData.Commitments) > 0 {
					item.ItemName = contractData.Commitments[0].Offer.Descriptor.Name
					item.ItemPrice = contractData.Commitments[0].CommitmentAttributes.Price.Value
					item.Currency = contractData.Commitments[0].CommitmentAttributes.Price.Currency
				}
				
				// Extract return status if any
				for _, p := range contractData.Performance {
					if p.PerformanceAttributes.Type == "RETURN" && p.Status.Code != "" {
						item.Status = p.Status.Code
					}
				}
			}
		}

		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var total int64
	if err := h.svc.pool.QueryRow(ctx, `SELECT COUNT(*) FROM transactions WHERE buyer_subject = $1`, buyerSubject).Scan(&total); err != nil {
		total = int64(len(items))
	}

	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}
