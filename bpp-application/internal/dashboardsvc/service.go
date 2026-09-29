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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/remiges-tech/logharbour/logharbour"

	"github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/auth"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/statussvc"
	"github.com/ion/winroom/bpp/internal/orderchange"
	"github.com/ion/winroom/bpp/internal/updatesvc"
)

const defaultPageSize = 20

// Handler bundles all dashboard API handlers and their shared dependencies.
type Handler struct {
	pool      *pgxpool.Pool
	cfg       *config.Config
	lh        *logharbour.Logger
	updateSvc *updatesvc.Service
}

func NewHandler(pool *pgxpool.Pool, cfg *config.Config, lh *logharbour.Logger) *Handler {
	return &Handler{pool: pool, cfg: cfg, lh: lh, updateSvc: updatesvc.NewService(pool, cfg, lh)}
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

	// Stock summary — count out-of-stock and low-stock resources.
	stockRows, err := q.ListResourceStock(ctx, sqlc.ListResourceStockParams{BppID: bppID, ProviderID: &providerID})
	if err != nil {
		// Non-fatal: stock table may not exist yet on older deployments.
		stockRows = nil
	}
	var outOfStock, lowStock int
	for _, s := range stockRows {
		if s.Quantity == 0 {
			outOfStock++
		} else if s.Quantity <= 5 {
			lowStock++
		}
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

	bppID := h.cfg.BppID
	q := sqlc.New(h.pool)
	rows, err := q.ListRecentMessages(c.Request.Context(), sqlc.ListRecentMessagesParams{
		BppID: &bppID,
		Limit: int32(limit),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": rows})
}

// ---------------------------------------------------------------------------
// GET /api/v1/inventory/stock
// ---------------------------------------------------------------------------

// HandleListStock returns the current inventory stock levels for all resources
// belonging to this BPP, joined with their descriptor names.
func (h *Handler) HandleListStock(c *gin.Context) {
	q := sqlc.New(h.pool)
	providerID := auth.ProviderIDFromContext(c)
	rows, err := q.ListResourceStock(c.Request.Context(), sqlc.ListResourceStockParams{BppID: h.cfg.BppID, ProviderID: &providerID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type stockItem struct {
		ResourceID string `json:"resourceId"`
		Name       string `json:"name"`
		CatalogID  string `json:"catalogId"`
		Quantity   int32  `json:"quantity"`
		Sold       int32  `json:"sold"`
		Available  int32  `json:"available"`
		UpdatedAt  string `json:"updatedAt"`
	}

	items := make([]stockItem, 0, len(rows))
	for _, r := range rows {
		name := r.ResourceID
		if r.DescriptorName != nil && *r.DescriptorName != "" {
			name = *r.DescriptorName
		}
		catID := ""
		if r.CatalogID != nil {
			catID = *r.CatalogID
		}
		ts := ""
		if r.UpdatedAt.Valid {
			ts = r.UpdatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		items = append(items, stockItem{
			ResourceID: r.ResourceID,
			Name:       name,
			CatalogID:  catID,
			Quantity:   r.Quantity,
			Sold:       r.Sold,
			Available:  r.Quantity,
			UpdatedAt:  ts,
		})
	}

	c.JSON(http.StatusOK, gin.H{"items": items})
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
// GET /api/v1/update-requests, POST .../approve, POST .../reject
//
// Buyer-proposed contract updates are queued (see updatesvc.Process) rather
// than applied automatically -- these three endpoints are how a seller
// actually reviews and decides them.
// ---------------------------------------------------------------------------

// HandleListUpdateRequests returns the seller's change-request queue.
//
// Each item carries a one-line headline plus only the fields that actually
// differ, computed by internal/orderchange -- deliberately NOT a raw diff
// of the two contracts. The buyer sends a full replacement contract built
// from the BAP's snapshot, while the seller stores the order normalised
// across contract_performances/commitments, so diffing them wholesale
// reports hundreds of phantom "new field" rows (and contract_attributes is
// usually NULL, so everything looks new). See that package's header.
func (h *Handler) HandleListUpdateRequests(c *gin.Context) {
	page, limit := parsePagination(c)
	q := sqlc.New(h.pool)
	ctx := c.Request.Context()
	providerID := auth.ProviderIDFromContext(c)

	var statusFilter sqlc.NullUpdateRequestStatus
	if s := c.Query("status"); s != "" {
		statusFilter = sqlc.NullUpdateRequestStatus{UpdateRequestStatus: sqlc.UpdateRequestStatus(s), Valid: true}
	}

	total, err := q.CountUpdateRequests(ctx, sqlc.CountUpdateRequestsParams{
		BppID:      h.cfg.BppID,
		ProviderID: &providerID,
		Status:     statusFilter,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows, err := q.ListUpdateRequestsDetailed(ctx, sqlc.ListUpdateRequestsDetailedParams{
		BppID:      h.cfg.BppID,
		Limit:      int32(limit),
		Offset:     int32((page - 1) * limit),
		ProviderID: &providerID,
		Status:     statusFilter,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Swap NoPhaseTracking{} for a real PhaseReporter to switch on
	// fulfilment-phase warnings -- this is the single call site referred to
	// in internal/orderchange/feasibility.go's header.
	var phases orderchange.PhaseReporter = orderchange.NoPhaseTracking{}

	type orderContext struct {
		PlacedAt       string            `json:"placedAt"`
		ContractStatus string            `json:"contractStatus"`
		Name           string            `json:"name,omitempty"`
		ItemCount      int               `json:"itemCount"`
		Total          string            `json:"total,omitempty"`
		Phase          orderchange.Phase `json:"phase"`
	}

	type updateRequestItem struct {
		ID            string                `json:"id"`
		ContractID    string                `json:"contractId"`
		TransactionID string                `json:"transactionId"`
		BapID         string                `json:"bapId"`
		Status        string                `json:"status"`
		RejectReason  string                `json:"rejectReason,omitempty"`
		CreatedAt     string                `json:"createdAt"`
		DecidedAt     string                `json:"decidedAt,omitempty"`
		Headline      string                `json:"headline"`
		Changes       []orderchange.Change  `json:"changes"`
		Warnings      []orderchange.Warning `json:"warnings"`
		Order         orderContext          `json:"order"`
		// RequestedAttributes is kept for debugging/audit only -- the UI
		// renders Changes. Dumping this on screen is what made the old
		// page unreadable.
		RequestedAttributes json.RawMessage `json:"requestedAttributes,omitempty"`
	}

	items := make([]updateRequestItem, 0, len(rows))
	for _, r := range rows {
		// Prefer the "before" snapshot taken when the request was queued.
		// Falling back to the live contract is only for rows created
		// before migration 030 -- and that fallback is exactly why an
		// approved request used to show no changes at all, since after
		// approval the live contract already holds the requested values.
		changes := orderchange.SummarizeFromDeliveryDetails(r.PreviousDeliveryDetails, r.RequestedAttributes)
		if len(changes) == 0 && len(r.PreviousDeliveryDetails) == 0 {
			changes = orderchange.Summarize(r.CurrentPerformanceAttributes, r.RequestedAttributes)
		}
		phase, err := phases.PhaseFor(ctx, r.ContractID.String())
		if err != nil {
			// Losing the phase costs advice, not the decision -- show the
			// request rather than failing the whole page.
			h.lh.WithModule("dashboardsvc").Warn().Error(err).Log("could not resolve fulfilment phase for update request")
			phase = orderchange.Phase{Label: "Unknown", Tracked: false}
		}

		itemCount, total := summariseCommitments(r.CommitmentAttributes)

		item := updateRequestItem{
			ID:                  r.ID.String(),
			ContractID:          r.ContractID.String(),
			TransactionID:       r.TransactionID.String(),
			BapID:               r.BapID,
			Status:              string(r.Status),
			Headline:            orderchange.Headline(changes, orderchange.HasDeliveryProposal(r.RequestedAttributes)),
			Changes:             changes,
			Warnings:            orderchange.Warnings(phase, changes),
			RequestedAttributes: r.RequestedAttributes,
			Order: orderContext{
				ContractStatus: string(r.ContractStatus),
				ItemCount:      itemCount,
				Total:          total,
				Phase:          phase,
			},
		}
		if r.OrderName != nil {
			item.Order.Name = *r.OrderName
		}
		if r.OrderPlacedAt.Valid {
			item.Order.PlacedAt = r.OrderPlacedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		if r.RejectReason != nil {
			item.RejectReason = *r.RejectReason
		}
		if r.CreatedAt.Valid {
			item.CreatedAt = r.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		if r.DecidedAt.Valid {
			item.DecidedAt = r.DecidedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// summariseCommitments reduces the order's commitment attributes to the two
// facts the seller needs at a glance: how many line items, and the money.
// Best-effort: an unparseable bag yields a zero count and no total rather
// than an error, since this is context around the decision, not the
// decision itself.
func summariseCommitments(raw interface{}) (int, string) {
	b, err := json.Marshal(raw)
	if err != nil {
		return 0, ""
	}
	var bags []struct {
		Price struct {
			Currency string      `json:"currency"`
			Value    json.Number `json:"value"`
		} `json:"price"`
		Quantity struct {
			UnitQuantity json.Number `json:"unitQuantity"`
		} `json:"quantity"`
	}
	if err := json.Unmarshal(b, &bags); err != nil {
		return 0, ""
	}

	var sum float64
	currency := ""
	for _, bag := range bags {
		v, err := bag.Price.Value.Float64()
		if err != nil {
			continue
		}
		qty := 1.0
		if q, err := bag.Quantity.UnitQuantity.Float64(); err == nil && q > 0 {
			qty = q
		}
		sum += v * qty
		if currency == "" {
			currency = bag.Price.Currency
		}
	}
	if currency == "" || sum == 0 {
		return len(bags), ""
	}
	return len(bags), fmt.Sprintf("%s %.0f", currency, sum)
}

func (h *Handler) HandleApproveUpdateRequest(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid update request id"})
		return
	}
	providerID := auth.ProviderIDFromContext(c)
	if err := h.updateSvc.ApproveRequest(c.Request.Context(), id, &providerID); err != nil {
		h.lh.WithModule("dashboardsvc").Err().Error(err).Log("approve update request failed")
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "APPROVED"})
}

func (h *Handler) HandleRejectUpdateRequest(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid update request id"})
		return
	}
	var body struct {
		// ReasonCode is one of orderchange.RejectReason's keys. It is what
		// gives the BUYER a stable label ("Already dispatched") instead of
		// whatever wording this particular seller typed -- which is the
		// point of sending a reason at all: a buyer who understands why
		// stops re-sending the same request. Free text rides along in
		// Reason. Both optional, per the product decision that a reason is
		// not mandatory.
		ReasonCode string `json:"reasonCode"`
		Reason     string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body) // reason is optional -- an empty body is fine
	providerID := auth.ProviderIDFromContext(c)
	if err := h.updateSvc.RejectRequest(c.Request.Context(), id, &providerID, body.ReasonCode, body.Reason); err != nil {
		h.lh.WithModule("dashboardsvc").Err().Error(err).Log("reject update request failed")
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "REJECTED"})
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

	// Trigger unsolicited on_status
	h.triggerUnsolicitedOnStatus(id, req.Status)

	// If Shipped, schedule Delivered after 10 mins
	if req.Status == "Shipped" {
		go func(cid uuid.UUID) {
			time.Sleep(1 * time.Minute)
			bgCtx := context.Background()
			newStatus := "Delivered"
			err := q.UpdatePerformanceStatus(bgCtx, sqlc.UpdatePerformanceStatusParams{
				StatusCode: &newStatus,
				ContractID: cid,
			})
			if err == nil {
				h.triggerUnsolicitedOnStatus(cid, newStatus)
			}
		}(id)
	}

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

	var contractMap map[string]interface{}
	
	// If contract_attributes is present, use it. Otherwise, fall back to beckn_message_log
	if len(row.ContractAttributes) > 0 && string(row.ContractAttributes) != "null" {
		if err := json.Unmarshal(row.ContractAttributes, &contractMap); err != nil {
			h.lh.WithModule("dashboardsvc").Err().Error(err).Log("failed to parse contract attributes for unsolicited on_status")
			return
		}
	}

	if contractMap == nil {
		var lastPayloadStr string
		err = h.pool.QueryRow(bgCtx, `
			SELECT request_payload 
			FROM beckn_message_log 
			WHERE transaction_id = $1 AND action IN ('on_confirm', 'confirm', 'on_init', 'init') 
			ORDER BY created_at DESC LIMIT 1
		`, row.TransactionID).Scan(&lastPayloadStr)
		if err != nil {
			h.lh.WithModule("dashboardsvc").Err().Error(err).Log("failed to find previous contract payload for unsolicited on_status")
			return
		}

		var parsedReq struct {
			Message struct {
				Contract map[string]interface{} `json:"contract"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(lastPayloadStr), &parsedReq); err != nil {
			h.lh.WithModule("dashboardsvc").Err().Error(err).Log("failed to parse previous contract payload")
			return
		}
		contractMap = parsedReq.Message.Contract
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

	req, err := http.NewRequestWithContext(bgCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		h.lh.WithModule("dashboardsvc").Err().Error(err).Log("failed unsolicited on_status")
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
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
