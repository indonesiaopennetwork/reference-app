package dashboardsvc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"bytes"
	"context"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/auth"
	"github.com/ion/winroom/bpp/internal/cancellation"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/statussvc"
)

const defaultPageSize = 20

// Handler bundles all dashboard API handlers and their shared dependencies.
type Handler struct {
	pool *pgxpool.Pool
	cfg  *config.Config
	lh   *logharbour.Logger
}

func NewHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Handler {
	return &Handler{pool: pool, cfg: cfg, lh: lh}
}

// ---------------------------------------------------------------------------
// GET /api/v1/dashboard/stats
// ---------------------------------------------------------------------------

// HandleStats returns key metrics: active/pending/today orders, resource/offer counts,
// and message funnel counts (select → init → confirm).
func (h *Handler) HandleStats(c *gin.Context) {
	q := sqlc.New(h.pool)
	ctx := c.Request.Context()
	bppID := h.cfg.BppID
	providerID := auth.ProviderIDFromContext(c)

	orderStats, err := q.GetDashboardStats(ctx, sqlc.GetDashboardStatsParams{BppID: bppID, ProviderID: &providerID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	invStats, err := q.GetInventoryStats(ctx, sqlc.GetInventoryStatsParams{BppID: bppID, ProviderID: &providerID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	funnelRows, err := q.GetMessageFunnel(ctx, &bppID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	funnel := map[string]int64{"select": 0, "init": 0, "confirm": 0}
	for _, row := range funnelRows {
		funnel[string(row.Action)] = row.MessageCount
	}

	// Availability summary: the seller's products published as out of stock
	// or low stock (resourceAttributes.availability.status).
	var outOfStock, lowStock int
	if err := h.pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE r.resource_attributes->'availability'->>'status' = 'OUT_OF_STOCK'),
		       COUNT(*) FILTER (WHERE r.resource_attributes->'availability'->>'status' = 'LOW_STOCK')
		  FROM resources r
		  JOIN catalogs c ON c.id = r.catalog_id AND c.bpp_id = r.bpp_id
		 WHERE r.bpp_id = $1 AND c.provider_id = $2 AND r.deleted_at IS NULL AND c.deleted_at IS NULL`,
		bppID, providerID).Scan(&outOfStock, &lowStock); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"active_orders":  orderStats.ActiveOrders,
		"pending_orders": orderStats.PendingOrders,
		"today_orders":   orderStats.TodayOrders,
		"resource_count": invStats.ResourceCount,
		"offer_count":    invStats.OfferCount,
		"funnel":         funnel,
		"out_of_stock":   outOfStock,
		"low_stock":      lowStock,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/orders
// ---------------------------------------------------------------------------

// HandleListOrders returns a paginated list of contracts.
func (h *Handler) HandleListOrders(c *gin.Context) {
	page, limit := parsePagination(c)
	q := sqlc.New(h.pool)
	ctx := c.Request.Context()
	providerID := auth.ProviderIDFromContext(c)

	total, err := q.CountContracts(ctx, sqlc.CountContractsParams{BppID: h.cfg.BppID, ProviderID: &providerID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows, err := q.ListContracts(ctx, sqlc.ListContractsParams{
		BppID:      h.cfg.BppID,
		Limit:      int32(limit),
		Offset:     int32((page - 1) * limit),
		ProviderID: &providerID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total": total,
		"page":  page,
		"limit": limit,
		"items": rows,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/orders/:id
// ---------------------------------------------------------------------------

// HandleGetOrder returns the full detail for a single contract, including
// commitments and considerations.
func (h *Handler) HandleGetOrder(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contract id"})
		return
	}

	q := sqlc.New(h.pool)
	ctx := c.Request.Context()
	providerID := auth.ProviderIDFromContext(c)

	contract, err := q.GetContractDetail(ctx, sqlc.GetContractDetailParams{ID: id, BppID: h.cfg.BppID, ProviderID: &providerID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	commitments, err := q.ListContractCommitments(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	considerations, err := q.ListContractConsiderations(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"contract":       contract,
		"commitments":    commitments,
		"considerations": considerations,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/inventory/resources
// ---------------------------------------------------------------------------

// HandleListResources returns a paginated list of resources in the BPP's inventory.
func (h *Handler) HandleListResources(c *gin.Context) {
	page, limit := parsePagination(c)
	q := sqlc.New(h.pool)
	ctx := c.Request.Context()
	providerID := auth.ProviderIDFromContext(c)

	total, err := q.CountResources(ctx, sqlc.CountResourcesParams{BppID: h.cfg.BppID, ProviderID: &providerID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows, err := q.ListResources(ctx, sqlc.ListResourcesParams{
		BppID:      h.cfg.BppID,
		Limit:      int32(limit),
		Offset:     int32((page - 1) * limit),
		ProviderID: &providerID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total": total,
		"page":  page,
		"limit": limit,
		"items": rows,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/inventory/offers
// ---------------------------------------------------------------------------

// HandleListOffers returns a paginated list of offers with their considerations.
func (h *Handler) HandleListOffers(c *gin.Context) {
	page, limit := parsePagination(c)
	q := sqlc.New(h.pool)
	ctx := c.Request.Context()
	providerID := auth.ProviderIDFromContext(c)

	total, err := q.CountOffers(ctx, sqlc.CountOffersParams{BppID: h.cfg.BppID, ProviderID: &providerID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows, err := q.ListOffers(ctx, sqlc.ListOffersParams{
		BppID:      h.cfg.BppID,
		Limit:      int32(limit),
		Offset:     int32((page - 1) * limit),
		ProviderID: &providerID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total": total,
		"page":  page,
		"limit": limit,
		"items": rows,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/messages
// ---------------------------------------------------------------------------

// HandleListMessages returns recent Beckn protocol messages from the audit log.
func (h *Handler) HandleListMessages(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	// Only the logged-in seller's messages: other sellers on this BPP share
	// its bpp_id, so that alone would show everyone's.
	providerID := auth.ProviderIDFromContext(c)
	q := sqlc.New(h.pool)
	rows, err := q.ListRecentMessages(c.Request.Context(), sqlc.ListRecentMessagesParams{
		BppID:      h.cfg.BppID,
		ProviderID: &providerID,
		RowLimit:   int32(limit),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": rows})
}

// ---------------------------------------------------------------------------
// GET /api/v1/support-tickets
// ---------------------------------------------------------------------------

func (h *Handler) HandleListSupportTickets(c *gin.Context) {
	page, limit := parsePagination(c)
	q := sqlc.New(h.pool)
	ctx := c.Request.Context()
	providerID := auth.ProviderIDFromContext(c)

	total, err := q.CountSupportTickets(ctx, sqlc.CountSupportTicketsParams{BppID: h.cfg.BppID, ProviderID: &providerID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows, err := q.ListSupportTickets(ctx, sqlc.ListSupportTicketsParams{
		BppID:      h.cfg.BppID,
		Limit:      int32(limit),
		Offset:     int32((page - 1) * limit),
		ProviderID: &providerID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type ticketItem struct {
		ID         string `json:"id"`
		ContractID string `json:"contractId"`
		TxnID      string `json:"transactionId"`
		BapID      string `json:"bapId"`
		Name       string `json:"name"`
		ShortDesc  string `json:"shortDesc"`
		Status     string `json:"status"`
		IsPreview  bool   `json:"isPreview"`
		CreatedAt  string `json:"createdAt"`
		ResolvedAt string `json:"resolvedAt,omitempty"`
	}

	items := make([]ticketItem, 0, len(rows))
	for _, r := range rows {
		cid := ""
		if r.ContractID.Valid {
			cid = uuid.UUID(r.ContractID.Bytes).String()
		}
		txid := ""
		if r.TransactionID.Valid {
			txid = uuid.UUID(r.TransactionID.Bytes).String()
		}
		name := ""
		if r.DescriptorName != nil {
			name = *r.DescriptorName
		}
		sd := ""
		if r.DescriptorShortDesc != nil {
			sd = *r.DescriptorShortDesc
		}
		ca := ""
		if r.CreatedAt.Valid {
			ca = r.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		ra := ""
		if r.ResolvedAt.Valid {
			ra = r.ResolvedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		items = append(items, ticketItem{
			ID:         r.ID.String(),
			ContractID: cid,
			TxnID:      txid,
			BapID:      r.BapID,
			Name:       name,
			ShortDesc:  sd,
			Status:     string(r.Status),
			IsPreview:  r.IsPreview,
			CreatedAt:  ca,
			ResolvedAt: ra,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/ratings
// ---------------------------------------------------------------------------

func (h *Handler) HandleListRatings(c *gin.Context) {
	page, limit := parsePagination(c)
	q := sqlc.New(h.pool)
	ctx := c.Request.Context()
	providerID := auth.ProviderIDFromContext(c)

	total, err := q.CountRatings(ctx, sqlc.CountRatingsParams{BppID: h.cfg.BppID, ProviderID: &providerID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows, err := q.ListRatings(ctx, sqlc.ListRatingsParams{
		BppID:      h.cfg.BppID,
		Limit:      int32(limit),
		Offset:     int32((page - 1) * limit),
		ProviderID: &providerID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type ratingItem struct {
		ID         string          `json:"id"`
		ContractID string          `json:"contractId"`
		TxnID      string          `json:"transactionId"`
		BapID      string          `json:"bapId"`
		TargetID   string          `json:"targetId"`
		Range      json.RawMessage `json:"range"`
		IsPreview  bool            `json:"isPreview"`
		CreatedAt  string          `json:"createdAt"`
	}

	items := make([]ratingItem, 0, len(rows))
	for _, r := range rows {
		ca := ""
		if r.CreatedAt.Valid {
			ca = r.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		items = append(items, ratingItem{
			ID:         r.ID.String(),
			ContractID: r.ContractID.String(),
			TxnID:      r.TransactionID.String(),
			BapID:      r.BapID,
			TargetID:   r.TargetID,
			Range:      r.Range,
			IsPreview:  r.IsPreview,
			CreatedAt:  ca,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// ---------------------------------------------------------------------------
// PUT /api/v1/orders/:id/fulfillment-status
// ---------------------------------------------------------------------------

// autoDeliverAfter is how long an order stays Shipped before it is marked
// Delivered (a stand-in for a real logistics partner's delivery event).
const autoDeliverAfter = 10 * time.Second

// RunAutoDelivery marks orders Delivered once they have been Shipped for
// autoDeliverAfter, and completes delivered ones (completeDelivered), every
// interval until ctx is done. It works from the
// database (contract_performances), so orders shipped before a restart are
// still delivered after it.
func (h *Handler) RunAutoDelivery(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		h.deliverShipped(ctx)
		h.completeDelivered(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (h *Handler) deliverShipped(ctx context.Context) {
	rows, err := h.pool.Query(ctx, `
		SELECT DISTINCT cp.contract_id FROM contract_performances cp
		  JOIN contracts c ON c.id = cp.contract_id
		 WHERE cp.status_code = 'Shipped' AND cp.updated_at <= NOW() - $1::interval
		   AND c.bpp_id = $2 AND c.status = 'ACTIVE' AND c.deleted_at IS NULL`,
		fmt.Sprintf("%d seconds", int(autoDeliverAfter.Seconds())), h.cfg.BppID)
	if err != nil {
		h.lh.WithModule("dashboardsvc").Warn().Error(err).Log("auto-delivery sweep failed")
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

	q := sqlc.New(h.pool)
	delivered := "Delivered"
	for _, id := range ids {
		if err := q.UpdatePerformanceStatus(ctx, sqlc.UpdatePerformanceStatusParams{StatusCode: &delivered, ContractID: id}); err != nil {
			h.lh.WithModule("dashboardsvc").Warn().Error(err).Log("auto-delivery failed for " + id.String())
			continue
		}
		h.markDelivered(ctx, id)
		h.triggerUnsolicitedOnStatus(id, delivered)
	}
}

// markDelivered stamps when the order was delivered -- the start of its return
// window (offer policies.returns.window). The first delivery time is kept.
// The contract becomes COMPLETE now if its settlement basis says so (see
// completeAt), before the caller's on_status tells the buyer app.
func (h *Handler) markDelivered(ctx context.Context, contractID uuid.UUID) {
	if _, err := h.pool.Exec(ctx,
		`UPDATE contracts SET delivered_at = COALESCE(delivered_at, NOW()) WHERE id = $1`, contractID); err != nil {
		h.lh.WithModule("dashboardsvc").Warn().Error(err).Log("failed to record delivered_at")
		return
	}
	h.completeIfDue(ctx, contractID, time.Now())
}

// HandleUpdateFulfillmentStatus updates the contract performance status
func (h *Handler) HandleUpdateFulfillmentStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contract id"})
		return
	}

	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Returns are decided by the offer's return policy when the buyer requests
	// them (updatesvc), not approved by hand.
	if strings.HasPrefix(req.Status, "Return") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "return status is set by the return policy, not manually"})
		return
	}

	q := sqlc.New(h.pool)
	ctx := c.Request.Context()
	bppID := h.cfg.BppID

	// Verify contract belongs to seller
	providerID := auth.ProviderIDFromContext(c)
	_, err = q.GetContractDetail(ctx, sqlc.GetContractDetailParams{ID: id, BppID: bppID, ProviderID: &providerID})
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
		return
	}

	// Update DB
	err = q.UpdatePerformanceStatus(ctx, sqlc.UpdatePerformanceStatusParams{
		StatusCode: &req.Status,
		ContractID: id,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if req.Status == "Delivered" {
		h.markDelivered(ctx, id)
	}

	// Trigger unsolicited on_status
	h.triggerUnsolicitedOnStatus(id, req.Status)

	// Shipped orders become Delivered after autoDeliverAfter -- done by
	// RunAutoDelivery's sweep, not an in-memory timer, so a restart can't lose it.

	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func (h *Handler) triggerUnsolicitedOnStatus(contractID uuid.UUID, status string) {
	bgCtx := context.Background()
	q := sqlc.New(h.pool)

	row, err := q.GetActiveContractByID(bgCtx, sqlc.GetActiveContractByIDParams{
		ID:    contractID,
		BppID: h.cfg.BppID,
	})
	if err != nil {
		h.lh.WithModule("dashboardsvc").Err().Error(err).Log("failed to get active contract for unsolicited on_status")
		return
	}

	// The contract as last stored, else as last sent (on_confirm etc.); the
	// shared loader also renames pre-ION settlement terms.
	contractMap, err := cancellation.LatestContract(bgCtx, h.pool, row.TransactionID, row.ContractAttributes)
	if err != nil {
		h.lh.WithModule("dashboardsvc").Err().Error(err).Log("failed to load contract for unsolicited on_status")
		return
	}

	if contractMap == nil {
		contractMap = make(map[string]interface{})
	}

	contractMap["id"] = row.ID.String()
	contractMap["status"] = map[string]string{"code": string(row.Status)}

	isReturnStatus := status == "Return_Received" || status == "Return_Rejected" || status == "Return_Approved"

	if perfs, ok := contractMap["performance"].([]interface{}); ok && len(perfs) > 0 {
		foundReturn := false
		if isReturnStatus {
			for _, p := range perfs {
				if perf, ok := p.(map[string]interface{}); ok {
					if attrs, ok := perf["performanceAttributes"].(map[string]interface{}); ok && attrs["type"] == "RETURN" {
						perf["status"] = map[string]interface{}{"code": status}
						foundReturn = true
					}
				}
			}
		}
		if !foundReturn && !isReturnStatus {
			if perf, ok := perfs[0].(map[string]interface{}); ok {
				perf["status"] = map[string]interface{}{"code": status}
			}
		}
	}

	contractJSON, _ := json.Marshal(contractMap)

	// Persist updated contract state to the database so that subsequent loads see the new status
	_, execErr := h.pool.Exec(bgCtx, `
		UPDATE contracts SET contract_attributes = $1, updated_at = NOW()
		WHERE id = $2 AND bpp_id = $3
	`, contractJSON, row.ID, h.cfg.BppID)
	if execErr != nil {
		h.lh.WithModule("dashboardsvc").Err().Error(execErr).Log("failed to update contract attributes in db")
	}

	networkID := ""
	if row.NetworkID != nil {
		networkID = *row.NetworkID
	}

	outMsgID := uuid.New()
	onStatusReq := statussvc.OnStatusRequest{
		Context: statussvc.BecknContext{
			Version:       "2.0.0",
			Action:        "on_status",
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			MessageID:     outMsgID.String(),
			TransactionID: row.TransactionID.String(),
			BapID:         row.BapID,
			BapURI:        row.BapUri,
			BppID:         h.cfg.BppID,
			BppURI:        h.cfg.BppURI,
			NetworkID:     networkID,
		},
		Message: statussvc.OnStatusMessage{Contract: contractJSON},
	}

	bapURI := row.BapUri
	url := h.resolveOnStatusURL(bapURI)
	body, _ := json.Marshal(onStatusReq)

	// Record it in the message log (OUTBOUND on_status), ACK or NACK, as
	// every other Beckn call is.
	start := time.Now()
	var respBody []byte
	var sendErr error
	defer func() {
		ack := sqlc.AckStatusACK
		var errMsg *string
		if sendErr != nil {
			ack = sqlc.AckStatusNACK
			m := sendErr.Error()
			errMsg = &m
		}
		var resp json.RawMessage
		if json.Valid(respBody) {
			resp = respBody
		}
		dur := int32(time.Since(start).Milliseconds())
		if err := q.InsertMessageLog(bgCtx, sqlc.InsertMessageLogParams{
			TransactionID:        pgtype.UUID{Bytes: row.TransactionID, Valid: true},
			MessageID:            outMsgID,
			Action:               sqlc.BecknActionOnStatus,
			Direction:            sqlc.MessageDirectionOUTBOUND,
			Url:                  &url,
			BapID:                &row.BapID,
			BapUri:               &row.BapUri,
			BppID:                &h.cfg.BppID,
			BppUri:               &h.cfg.BppURI,
			NetworkID:            &networkID,
			AckStatus:            sqlc.NullAckStatus{AckStatus: ack, Valid: true},
			RequestPayload:       body,
			ResponsePayload:      resp,
			ErrorMessage:         errMsg,
			ProcessingDurationMs: &dur,
		}); err != nil {
			h.lh.WithModule("dashboardsvc").Warn().Error(err).Log("failed to write beckn_message_log for on_status")
		}
	}()

	req, err := http.NewRequestWithContext(bgCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		sendErr = err
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		sendErr = err
		h.lh.WithModule("dashboardsvc").Err().Error(err).Log("failed unsolicited on_status")
		return
	}
	defer resp.Body.Close()
	respBody, _ = io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		sendErr = fmt.Errorf("BAP returned non-2xx status %d: %s", resp.StatusCode, respBody)
		h.lh.WithModule("dashboardsvc").Warn().Error(sendErr).Log("unsolicited on_status NACKed")
		return
	}
	h.lh.WithModule("dashboardsvc").Info().Log("unsolicited on_status sent: " + string(respBody))
}

func (h *Handler) resolveOnStatusURL(_ string) string {
	base := strings.TrimSpace(h.cfg.BppCallerURL)
	if base == "" {
		base = strings.TrimSpace(h.cfg.BppURI)
		if strings.HasSuffix(base, "/") {
			base = strings.TrimSuffix(base, "/")
		}
		if strings.HasSuffix(base, "/bpp/receiver") {
			base = strings.TrimSuffix(base, "/bpp/receiver") + "/bpp/caller"
		}
	}
	base = strings.TrimRight(base, "/")
	return base + "/on_status"
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func parsePagination(c *gin.Context) (page, limit int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ = strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(defaultPageSize)))
	if page < 1 {
		page = 1
	}
	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}
	return
}
