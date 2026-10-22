package dashboardsvc

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbsqlc "github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/auth"
	"github.com/ion/winroom/bpp/internal/catalog"
	"github.com/ion/winroom/bpp/internal/httpclient"
	"github.com/ion/winroom/bpp/internal/offerterms"
)

// actionCatalogRetire is a 'catalog_retire' beckn_action -- added by
// migration 033, ahead of this committed db/sqlc generated code (same
// lag/cast pattern as reconsvc's actionReconcile).
const actionCatalogRetire = dbsqlc.BecknAction("catalog_retire")

// catalogOwnedByProvider reports whether catalogID belongs to providerID
// (and this bpp). Every handler below that mutates or reads a specific
// catalog by ID must check this first -- catalogID always comes from the
// URL, and this is the only thing standing between "the logged-in seller's
// own catalog" and "any seller's catalog, if you know or guess its ID."
func (h *Handler) catalogOwnedByProvider(ctx context.Context, catalogID, providerID string) (bool, error) {
	var exists bool
	err := h.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM catalogs WHERE id = $1 AND bpp_id = $2 AND provider_id = $3 AND deleted_at IS NULL)`,
		catalogID, h.cfg.BppID, providerID,
	).Scan(&exists)
	return exists, err
}

// ---------------------------------------------------------------------------
// GET /api/v1/catalogs
// Query params: search (matches id / name / provider_id), page, limit.
// Always scoped to the authenticated seller's own provider -- provider_id
// is no longer a client-chosen filter (there's only ever one to choose).
// ---------------------------------------------------------------------------

func (h *Handler) HandleListCatalogs(c *gin.Context) {
	page, limit := parsePagination(c)
	search := c.DefaultQuery("search", "")
	providerFilter := auth.ProviderIDFromContext(c)
	ctx := c.Request.Context()

	countSQL := `
		SELECT COUNT(DISTINCT c.id)
		FROM catalogs c
		WHERE c.bpp_id = $1
		  AND c.deleted_at IS NULL
		  AND ($2 = '' OR c.provider_id = $2)
		  AND ($3 = '' OR c.id ILIKE '%' || $3 || '%'
		                 OR c.descriptor_name ILIKE '%' || $3 || '%'
		                 OR c.provider_id     ILIKE '%' || $3 || '%')`

	var total int64
	if err := h.pool.QueryRow(ctx, countSQL, h.cfg.BppID, providerFilter, search).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	listSQL := `
		SELECT
		    c.id,
		    c.descriptor_name,
		    c.descriptor_short_desc,
		    c.provider_id,
		    c.catalog_type,
		    c.crc,
		    c.validity_start,
		    c.validity_end,
		    c.created_at,
		    c.network_published_at,
		    c.is_active,
		    COUNT(DISTINCT r.id) AS resource_count,
		    COUNT(DISTINCT o.id) AS offer_count
		FROM catalogs c
		LEFT JOIN resources r ON r.catalog_id = c.id AND r.bpp_id = c.bpp_id AND r.deleted_at IS NULL
		LEFT JOIN offers    o ON o.catalog_id = c.id AND o.bpp_id = c.bpp_id AND o.deleted_at IS NULL
		WHERE c.bpp_id = $1
		  AND c.deleted_at IS NULL
		  AND ($2 = '' OR c.provider_id = $2)
		  AND ($3 = '' OR c.id ILIKE '%' || $3 || '%'
		                 OR c.descriptor_name ILIKE '%' || $3 || '%'
		                 OR c.provider_id     ILIKE '%' || $3 || '%')
		GROUP BY c.id, c.descriptor_name, c.descriptor_short_desc, c.provider_id,
		         c.catalog_type, c.crc, c.validity_start, c.validity_end, c.created_at, c.network_published_at, c.is_active
		ORDER BY c.created_at DESC
		LIMIT $4 OFFSET $5`

	rows, err := h.pool.Query(ctx, listSQL, h.cfg.BppID, providerFilter, search, limit, (page-1)*limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type catalogRow struct {
		ID                 string  `json:"id"`
		Name               string  `json:"name"`
		ShortDesc          string  `json:"shortDesc"`
		ProviderID         string  `json:"providerId"`
		CatalogType        *string `json:"catalogType"`
		CRC                *string `json:"crc"`
		ValidityStart      *string `json:"validityStart"`
		ValidityEnd        *string `json:"validityEnd"`
		CreatedAt          string  `json:"createdAt"`
		NetworkPublishedAt *string `json:"networkPublishedAt"`
		IsPublished        bool    `json:"isPublished"`
		IsActive           bool    `json:"isActive"`
		ResourceCount      int64   `json:"resourceCount"`
		OfferCount         int64   `json:"offerCount"`
	}

	items := make([]catalogRow, 0)
	for rows.Next() {
		var row catalogRow
		var catType *string
		var vs, ve, npAt pgtype.Timestamptz
		var sd *string
		var ca pgtype.Timestamptz
		var rc, oc int64

		if err := rows.Scan(&row.ID, &row.Name, &sd, &row.ProviderID, &catType, &row.CRC,
			&vs, &ve, &ca, &npAt, &row.IsActive, &rc, &oc); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		row.ShortDesc = ptrStr(sd)
		row.CatalogType = catType
		if vs.Valid {
			s := vs.Time.UTC().Format(time.RFC3339)
			row.ValidityStart = &s
		}
		if ve.Valid {
			s := ve.Time.UTC().Format(time.RFC3339)
			row.ValidityEnd = &s
		}
		if ca.Valid {
			row.CreatedAt = ca.Time.UTC().Format(time.RFC3339)
		}
		if npAt.Valid {
			s := npAt.Time.UTC().Format(time.RFC3339)
			row.NetworkPublishedAt = &s
			row.IsPublished = true
		}
		row.ResourceCount = rc
		row.OfferCount = oc
		items = append(items, row)
	}

	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": page, "limit": limit})
}

// ---------------------------------------------------------------------------
// GET /api/v1/providers  — for filter dropdown
// ---------------------------------------------------------------------------

// HandleListProviders returns only the authenticated seller's own provider
// -- one row, always. (Kept as a list response for frontend compatibility;
// there's nothing left to filter since a seller has exactly one provider.)
func (h *Handler) HandleListProviders(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := h.pool.Query(ctx,
		`SELECT id, descriptor_name FROM providers WHERE bpp_id = $1 AND id = $2 AND deleted_at IS NULL ORDER BY descriptor_name`,
		h.cfg.BppID, auth.ProviderIDFromContext(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type prow struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	items := make([]prow, 0)
	for rows.Next() {
		var r prow
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			continue
		}
		items = append(items, r)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// ---------------------------------------------------------------------------
// POST /api/v1/catalogs  — create / upsert catalog metadata only
// ---------------------------------------------------------------------------

type CreateCatalogRequest struct {
	ID         string `json:"id"         binding:"required"`
	Descriptor struct {
		Name      string `json:"name"      binding:"required"`
		ShortDesc string `json:"shortDesc"`
		LongDesc  string `json:"longDesc"`
	} `json:"descriptor" binding:"required"`
	Provider struct {
		ID         string `json:"id"         binding:"required"`
		Descriptor struct {
			Name string `json:"name" binding:"required"`
		} `json:"descriptor" binding:"required"`
	} `json:"provider" binding:"required"`
	Validity *struct {
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
	} `json:"validity"`
	// CRC is the catalog's ION Catalogue Resource Category (e.g.
	// TRC-fashion; ion-specs docs/ION_Resource_Categories.md), optional.
	// The seller app publishes its products with resourceAttributes.crc set
	// to it. Left unchanged when empty.
	CRC string `json:"crc"`
}

func (h *Handler) HandleCreateCatalog(c *gin.Context) {
	var req CreateCatalogRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	q := dbsqlc.New(h.pool)
	providerID := auth.ProviderIDFromContext(c)

	// UpsertCatalog below is a real INSERT ... ON CONFLICT DO UPDATE that
	// happily overwrites provider_id on conflict -- without this check, any
	// authenticated seller could "steal" another seller's existing catalog
	// ID just by POSTing it again, instantly reassigning it to themselves.
	if owned, err := h.catalogOwnedByProvider(ctx, req.ID, providerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	} else if !owned {
		// Deliberately no "AND deleted_at IS NULL" here -- a retired
		// (soft-deleted) catalog ID must never be reusable either (Beckn
		// spec NFH-014 §10.5 CON-TBD-26: a retired catalogId can never be
		// re-listed, even under a new publish), so this also has to catch
		// IDs this same provider previously deleted, not just another
		// provider's still-live one.
		var alreadyExists bool
		if err := h.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM catalogs WHERE id = $1 AND bpp_id = $2)`,
			req.ID, h.cfg.BppID,
		).Scan(&alreadyExists); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if alreadyExists {
			c.JSON(http.StatusConflict, gin.H{"error": "a catalog with this ID already exists or was previously deleted and cannot be reused"})
			return
		}
	}

	// Upsert provider -- always the caller's own, regardless of whatever
	// provider info was submitted (a seller already has exactly one
	// provider, created at registration; this just lets them update its
	// display name via this form).
	if err := q.UpsertProvider(ctx, dbsqlc.UpsertProviderParams{
		ID:                   providerID,
		BppID:                h.cfg.BppID,
		DescriptorName:       req.Provider.Descriptor.Name,
		DescriptorDocs:       json.RawMessage(`[]`),
		DescriptorMediaFiles: json.RawMessage(`[]`),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "upsert provider: " + err.Error()})
		return
	}

	// Upsert catalog.
	if err := q.UpsertCatalog(ctx, dbsqlc.UpsertCatalogParams{
		ID:                   req.ID,
		BppID:                h.cfg.BppID,
		BppUri:               h.cfg.BppURI,
		ProviderID:           providerID,
		DescriptorName:       req.Descriptor.Name,
		DescriptorShortDesc:  strPtr(req.Descriptor.ShortDesc),
		DescriptorLongDesc:   strPtr(req.Descriptor.LongDesc),
		DescriptorDocs:       json.RawMessage(`[]`),
		DescriptorMediaFiles: json.RawMessage(`[]`),
		CatalogType:          dbsqlc.NullCatalogType{},
		ValidityStart:        parseTS(req.Validity, true),
		ValidityEnd:          parseTS(req.Validity, false),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "upsert catalog: " + err.Error()})
		return
	}
	if _, err := h.pool.Exec(ctx,
		`UPDATE catalogs SET crc = COALESCE(NULLIF($3, ''), crc) WHERE id = $1 AND bpp_id = $2`,
		req.ID, h.cfg.BppID, strings.TrimSpace(req.CRC)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save catalog CRC: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "id": req.ID})
}

// ---------------------------------------------------------------------------
// GET /api/v1/catalogs/:id  — catalog detail with resources + offers
// ---------------------------------------------------------------------------

func (h *Handler) HandleGetCatalog(c *gin.Context) {
	catalogID := c.Param("id")
	ctx := c.Request.Context()

	// Catalog row.
	var cat struct {
		ID                 string  `json:"id"`
		Name               string  `json:"name"`
		ShortDesc          string  `json:"shortDesc"`
		ProviderID         string  `json:"providerId"`
		ProviderName       string  `json:"providerName"`
		CatalogType        *string `json:"catalogType"`
		CRC                *string `json:"crc"`
		ValidityStart      *string `json:"validityStart"`
		ValidityEnd        *string `json:"validityEnd"`
		CreatedAt          string  `json:"createdAt"`
		NetworkPublishedAt *string `json:"networkPublishedAt"`
		IsPublished        bool    `json:"isPublished"`
		IsActive           bool    `json:"isActive"`
	}
	var vs, ve, ca, npAt pgtype.Timestamptz
	var sd *string
	var catType *string
	err := h.pool.QueryRow(ctx, `
		SELECT c.id, c.descriptor_name, c.descriptor_short_desc, c.provider_id,
		       COALESCE(p.descriptor_name, c.provider_id),
		       c.catalog_type, c.crc, c.validity_start, c.validity_end, c.created_at,
		       c.network_published_at, c.is_active
		FROM catalogs c
		LEFT JOIN providers p ON p.id = c.provider_id AND p.bpp_id = c.bpp_id
		WHERE c.id = $1 AND c.bpp_id = $2 AND c.provider_id = $3 AND c.deleted_at IS NULL`,
		catalogID, h.cfg.BppID, auth.ProviderIDFromContext(c),
	).Scan(&cat.ID, &cat.Name, &sd, &cat.ProviderID, &cat.ProviderName,
		&catType, &cat.CRC, &vs, &ve, &ca, &npAt, &cat.IsActive)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "catalog not found"})
		return
	}
	cat.ShortDesc = ptrStr(sd)
	cat.CatalogType = catType
	if vs.Valid {
		s := vs.Time.UTC().Format(time.RFC3339)
		cat.ValidityStart = &s
	}
	if ve.Valid {
		s := ve.Time.UTC().Format(time.RFC3339)
		cat.ValidityEnd = &s
	}
	if ca.Valid {
		cat.CreatedAt = ca.Time.UTC().Format(time.RFC3339)
	}
	if npAt.Valid {
		s := npAt.Time.UTC().Format(time.RFC3339)
		cat.NetworkPublishedAt = &s
		cat.IsPublished = true
	}

	// Resources.
	resRows, err := h.pool.Query(ctx, `
		SELECT r.id, r.descriptor_name, COALESCE(r.descriptor_short_desc,''),
		       r.descriptor_media_files, r.resource_attributes, r.created_at
		FROM resources r
		WHERE r.catalog_id = $1 AND r.bpp_id = $2 AND r.deleted_at IS NULL
		ORDER BY r.created_at`,
		catalogID, h.cfg.BppID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer resRows.Close()

	type resItem struct {
		ID         string          `json:"id"`
		Name       string          `json:"name"`
		ShortDesc  string          `json:"shortDesc"`
		MediaFiles json.RawMessage `json:"mediaFiles"`
		Attributes json.RawMessage `json:"resourceAttributes"`
		CreatedAt  string          `json:"createdAt"`
	}
	resources := make([]resItem, 0)
	for resRows.Next() {
		var r resItem
		var rca pgtype.Timestamptz
		if err := resRows.Scan(&r.ID, &r.Name, &r.ShortDesc, &r.MediaFiles, &r.Attributes, &rca); err != nil {
			continue
		}
		if rca.Valid {
			r.CreatedAt = rca.Time.UTC().Format(time.RFC3339)
		}
		resources = append(resources, r)
	}
	resRows.Close()

	// Offers with consideration price.
	offerRows, err := h.pool.Query(ctx, `
		SELECT o.id, o.descriptor_name, COALESCE(o.descriptor_short_desc,''),
		       o.resource_ids, o.validity_start, o.validity_end,
		       oc.consideration_attributes
		FROM offers o
		LEFT JOIN LATERAL (
		    SELECT oc2.consideration_attributes FROM offer_considerations oc2
		    WHERE oc2.offer_id = o.id AND oc2.bpp_id = o.bpp_id LIMIT 1
		) oc ON TRUE
		WHERE o.catalog_id = $1 AND o.bpp_id = $2 AND o.deleted_at IS NULL
		ORDER BY o.created_at`,
		catalogID, h.cfg.BppID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer offerRows.Close()

	type offerItem struct {
		ID                      string          `json:"id"`
		Name                    string          `json:"name"`
		ShortDesc               string          `json:"shortDesc"`
		ResourceIDs             []string        `json:"resourceIds"`
		ValidityStart           *string         `json:"validityStart"`
		ValidityEnd             *string         `json:"validityEnd"`
		ConsiderationAttributes json.RawMessage `json:"considerationAttributes"`
	}
	offers := make([]offerItem, 0)
	for offerRows.Next() {
		var o offerItem
		var ovs, ove pgtype.Timestamptz
		var rids []string
		var consAttrs json.RawMessage
		if err := offerRows.Scan(&o.ID, &o.Name, &o.ShortDesc, &rids, &ovs, &ove, &consAttrs); err != nil {
			continue
		}
		o.ResourceIDs = rids
		o.ConsiderationAttributes = consAttrs
		if ovs.Valid {
			s := ovs.Time.UTC().Format(time.RFC3339)
			o.ValidityStart = &s
		}
		if ove.Valid {
			s := ove.Time.UTC().Format(time.RFC3339)
			o.ValidityEnd = &s
		}
		offers = append(offers, o)
	}

	c.JSON(http.StatusOK, gin.H{"catalog": cat, "resources": resources, "offers": offers})
}

// ---------------------------------------------------------------------------
// PATCH /api/v1/catalogs/:id/active  — pause/resume a catalog
//
// isActive is a freely-reversible ACTIVE<->PAUSED toggle (Beckn protocol
// NFH-014 Catalog_Publishing_and_Discovery.md §10.4), distinct from
// retiring a catalog: pausing never deletes or locks its data, it only
// changes what's advertised on the network. If the catalog has already
// been published, this handler auto-republishes right away (via
// publishCatalogToCDS) so the toggle takes effect immediately without a
// separate manual Publish click -- a republish failure is reported back
// as published:false/publishError rather than failing the toggle itself,
// since the local state change must never be blocked by a network hiccup.
// A catalog that has never been published is left as a DB-only change, the
// same as before.
// ---------------------------------------------------------------------------

type SetCatalogActiveRequest struct {
	IsActive *bool `json:"isActive" binding:"required"`
}

func (h *Handler) HandleSetCatalogActive(c *gin.Context) {
	catalogID := c.Param("id")
	var req SetCatalogActiveRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.IsActive == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "isActive (boolean) is required"})
		return
	}
	if !h.ownedCatalog(c, catalogID) {
		return
	}
	ctx := c.Request.Context()
	var publishedAt pgtype.Timestamptz
	err := h.pool.QueryRow(ctx, `
		UPDATE catalogs SET is_active = $1, updated_at = NOW()
		 WHERE id = $2 AND bpp_id = $3 AND deleted_at IS NULL
		 RETURNING network_published_at`,
		*req.IsActive, catalogID, h.cfg.BppID,
	).Scan(&publishedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "catalog not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{"status": "updated", "isActive": *req.IsActive}
	if publishedAt.Valid {
		res := h.publishCatalogToCDS(ctx, catalogID, false)
		resp["published"] = res.ok()
		if !res.ok() {
			resp["publishError"] = res.errMessage()
		}
	}
	c.JSON(http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// POST /api/v1/catalogs/:id/products  — add a single resource, and optionally
// an offer for it (the dashboard adds offers separately via /offers)
// ---------------------------------------------------------------------------

type AddProductRequest struct {
	Resource catalog.Resource `json:"resource" binding:"required"`
	Offer    *catalog.Offer   `json:"offer,omitempty"`
}

func (h *Handler) HandleAddProduct(c *gin.Context) {
	catalogID := c.Param("id")
	var req AddProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	providerID := auth.ProviderIDFromContext(c)

	if owned, err := h.catalogOwnedByProvider(ctx, catalogID, providerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	} else if !owned {
		c.JSON(http.StatusNotFound, gin.H{"error": "catalog not found"})
		return
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := dbsqlc.New(tx)
	bppID := h.cfg.BppID

	// Insert resource.
	mediaJSON, _ := json.Marshal(req.Resource.Descriptor.MediaFile)
	attrsCtx, attrsType := extractContextType(req.Resource.ResourceAttributes)
	if err := q.InsertResource(ctx, dbsqlc.InsertResourceParams{
		ID:                        req.Resource.ID,
		CatalogID:                 catalogID,
		BppID:                     bppID,
		DescriptorName:            strPtr(req.Resource.Descriptor.Name),
		DescriptorCode:            strPtr(req.Resource.Descriptor.Code),
		DescriptorShortDesc:       strPtr(req.Resource.Descriptor.ShortDesc),
		DescriptorLongDesc:        strPtr(req.Resource.Descriptor.LongDesc),
		DescriptorDocs:            json.RawMessage(`[]`),
		DescriptorMediaFiles:      mediaJSON,
		ResourceAttributes:        req.Resource.ResourceAttributes,
		ResourceAttributesContext: strPtr(attrsCtx),
		ResourceAttributesType:    strPtr(attrsType),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "insert resource: " + err.Error()})
		return
	}

	if req.Offer != nil {
		if err := h.insertOffer(ctx, q, catalogID, providerID, req.Offer); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// insertOffer stores an offer, its provider link, and its considerations
// within the caller's transaction.
func (h *Handler) insertOffer(ctx context.Context, q *dbsqlc.Queries, catalogID, providerID string, offer *catalog.Offer) error {
	bppID := h.cfg.BppID

	// Upsert offer provider if provided -- forced to the caller's own
	// provider regardless of what ID was submitted. UpsertProvider is an
	// upsert keyed only on (id, bpp_id); trusting a client-supplied ID here
	// would let any seller silently rename another seller's provider.
	var offerProviderID *string
	if offer.Provider != nil {
		if err := q.UpsertProvider(ctx, dbsqlc.UpsertProviderParams{
			ID:             providerID,
			BppID:          bppID,
			DescriptorName: offer.Provider.Descriptor.Name,
		}); err != nil {
			return fmt.Errorf("upsert offer provider: %w", err)
		}
		offerProviderID = strPtr(providerID)
	}

	// Insert offer, with this seller's default terms where it has none.
	d := offerterms.FromConfig(h.cfg)
	origAttrs := offer.OfferAttributes
	offer.OfferAttributes = offerterms.FillAttributes(offer.OfferAttributes, d)
	oAttrsCtx, oAttrsType := extractContextType(offer.OfferAttributes)
	var validityStart, validityEnd pgtype.Timestamptz
	if offer.Validity != nil {
		validityStart = parseTimestampStr(offer.Validity.StartDate)
		validityEnd = parseTimestampStr(offer.Validity.EndDate)
	}
	if err := q.InsertOffer(ctx, dbsqlc.InsertOfferParams{
		ID:                     offer.ID,
		CatalogID:              catalogID,
		BppID:                  bppID,
		ProviderID:             offerProviderID,
		DescriptorName:         strPtr(offer.Descriptor.Name),
		DescriptorCode:         strPtr(offer.Descriptor.Code),
		DescriptorShortDesc:    strPtr(offer.Descriptor.ShortDesc),
		ResourceIds:            offer.ResourceIDs,
		OfferAttributes:        offer.OfferAttributes,
		OfferAttributesContext: strPtr(oAttrsCtx),
		OfferAttributesType:    strPtr(oAttrsType),
		ValidityStart:          validityStart,
		ValidityEnd:            validityEnd,
	}); err != nil {
		return fmt.Errorf("insert offer: %w", err)
	}

	// Insert considerations.
	for _, consid := range offer.Considerations {
		consid.ConsiderationAttributes = offerterms.FillFee(consid.ConsiderationAttributes, origAttrs, d)
		cAttrsCtx, cAttrsType := extractContextType(consid.ConsiderationAttributes)
		if err := q.InsertOfferConsideration(ctx, dbsqlc.InsertOfferConsiderationParams{
			OfferID:                        offer.ID,
			CatalogID:                      catalogID,
			BppID:                          bppID,
			ConsiderationID:                consid.ID,
			StatusCode:                     considerationStatusCode(consid.Status),
			StatusName:                     strPtr(consid.Status.Name),
			ConsiderationAttributes:        consid.ConsiderationAttributes,
			ConsiderationAttributesContext: strPtr(cAttrsCtx),
			ConsiderationAttributesType:    strPtr(cAttrsType),
		}); err != nil {
			return fmt.Errorf("insert consideration: %w", err)
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// POST /api/v1/catalogs/:id/publish  — read from DB and forward to CDS
// ---------------------------------------------------------------------------

func (h *Handler) HandlePublishCatalog(c *gin.Context) {
	catalogID := c.Param("id")
	ctx := c.Request.Context()

	// Optional body -- {"forceBaseline": true} skips onix-catalog-publish's
	// diff-against-prior-state and always writes a fresh baseline for this
	// catalog (manual compaction). Every existing caller sends no body at
	// all, so a missing/empty body is not an error, just forceBaseline=false.
	var publishReq struct {
		ForceBaseline bool `json:"forceBaseline"`
	}
	_ = c.ShouldBindJSON(&publishReq)

	if owned, err := h.catalogOwnedByProvider(ctx, catalogID, auth.ProviderIDFromContext(c)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	} else if !owned {
		c.JSON(http.StatusNotFound, gin.H{"error": "catalog not found"})
		return
	}

	res := h.publishCatalogToCDS(ctx, catalogID, publishReq.ForceBaseline)
	c.JSON(res.status, res.body)
}

// publishResult is the outcome of an attempt to publish a catalog to CDS.
// Used directly as HandlePublishCatalog's HTTP response, and folded into
// HandleSetCatalogActive's own response (as published/publishError) when a
// pause/resume toggle auto-republishes -- a network-side publish failure
// there must never fail the toggle itself, just be reported alongside it.
type publishResult struct {
	status int
	body   gin.H
}

func (r publishResult) ok() bool { return r.status >= 200 && r.status < 300 }

// errMessage returns r.body's "error" field, or "" if there is none.
func (r publishResult) errMessage() string {
	msg, _ := r.body["error"].(string)
	return msg
}

// publishCatalogToCDS builds catalogID's current state from the DB,
// forwards it to onix-catalog-publish, and stamps network_published_at on
// success. Ownership must already be checked by the caller -- this is just
// the publishing mechanics, shared by HandlePublishCatalog (the explicit
// Publish/Manual Compaction buttons) and HandleSetCatalogActive
// (auto-republish after a pause/resume toggle).
func (h *Handler) publishCatalogToCDS(ctx context.Context, catalogID string, forceBaseline bool) publishResult {
	if h.cfg.CDSPublishURL == "" {
		return publishResult{http.StatusBadRequest, gin.H{"error": "CDS_PUBLISH_URL not configured"}}
	}

	// Read catalog.
	var cat struct {
		DescriptorName      string
		DescriptorShortDesc *string
		DescriptorLongDesc  *string
		ProviderID          string
		ProviderName        string
		CatalogType         *string
		ValidityStart       pgtype.Timestamptz
		ValidityEnd         pgtype.Timestamptz
		IsActive            bool
	}
	err := h.pool.QueryRow(ctx, `
		SELECT c.descriptor_name, c.descriptor_short_desc, c.descriptor_long_desc,
		       c.provider_id, COALESCE(p.descriptor_name, c.provider_id), c.catalog_type,
		       c.validity_start, c.validity_end, c.is_active
		FROM catalogs c
		LEFT JOIN providers p ON p.id = c.provider_id AND p.bpp_id = c.bpp_id
		WHERE c.id = $1 AND c.bpp_id = $2 AND c.deleted_at IS NULL`,
		catalogID, h.cfg.BppID,
	).Scan(&cat.DescriptorName, &cat.DescriptorShortDesc, &cat.DescriptorLongDesc,
		&cat.ProviderID, &cat.ProviderName, &cat.CatalogType, &cat.ValidityStart, &cat.ValidityEnd, &cat.IsActive)
	if err != nil {
		return publishResult{http.StatusNotFound, gin.H{"error": "catalog not found"}}
	}

	// Build catalog.Catalog from DB rows.
	becknCat := catalog.Catalog{
		ID: catalogID,
		Descriptor: catalog.Descriptor{
			Name:      cat.DescriptorName,
			ShortDesc: ptrStr(cat.DescriptorShortDesc),
			LongDesc:  ptrStr(cat.DescriptorLongDesc),
		},
		Provider: catalog.Provider{
			ID:         cat.ProviderID,
			Descriptor: catalog.Descriptor{Name: cat.ProviderName},
		},
		IsActive: cat.IsActive,
	}
	if cat.CatalogType != nil && *cat.CatalogType != "" {
		becknCat.PublishDirectives = &catalog.PublishDirectives{CatalogType: *cat.CatalogType}
	}
	if cat.ValidityStart.Valid && cat.ValidityEnd.Valid {
		becknCat.Validity = &catalog.TimePeriod{
			StartDate: cat.ValidityStart.Time.UTC().Format(time.RFC3339),
			EndDate:   cat.ValidityEnd.Time.UTC().Format(time.RFC3339),
		}
	}

	// Read resources.
	resRows, err := h.pool.Query(ctx, `
		SELECT r.id, r.descriptor_name, COALESCE(r.descriptor_short_desc,''),
		       COALESCE(r.descriptor_long_desc,''), r.descriptor_media_files, r.resource_attributes
		FROM resources r
		WHERE r.catalog_id = $1 AND r.bpp_id = $2 AND r.deleted_at IS NULL`,
		catalogID, h.cfg.BppID)
	if err != nil {
		return publishResult{http.StatusInternalServerError, gin.H{"error": err.Error()}}
	}
	defer resRows.Close()

	for resRows.Next() {
		var r catalog.Resource
		var mediaRaw json.RawMessage
		if err := resRows.Scan(&r.ID, &r.Descriptor.Name, &r.Descriptor.ShortDesc,
			&r.Descriptor.LongDesc, &mediaRaw, &r.ResourceAttributes); err != nil {
			continue
		}
		if len(mediaRaw) > 0 && string(mediaRaw) != "[]" && string(mediaRaw) != "null" {
			_ = json.Unmarshal(mediaRaw, &r.Descriptor.MediaFile)
		}
		// Drop entries with a blank uri — the CDS schema requires a valid URI
		// format, and an empty string (no image uploaded yet) fails validation.
		validMedia := r.Descriptor.MediaFile[:0]
		for _, m := range r.Descriptor.MediaFile {
			if strings.TrimSpace(m.URI) != "" {
				validMedia = append(validMedia, m)
			}
		}
		r.Descriptor.MediaFile = validMedia
		becknCat.Resources = append(becknCat.Resources, r)
	}
	resRows.Close()

	// Read offers + considerations.
	offerRows, err := h.pool.Query(ctx, `
		SELECT o.id, o.descriptor_name, COALESCE(o.descriptor_short_desc,''),
		       o.resource_ids, o.offer_attributes, o.validity_start, o.validity_end,
		       COALESCE(op.descriptor_name,''), COALESCE(op.id,'')
		FROM offers o
		LEFT JOIN providers op ON op.id = o.provider_id AND op.bpp_id = o.bpp_id
		WHERE o.catalog_id = $1 AND o.bpp_id = $2 AND o.deleted_at IS NULL`,
		catalogID, h.cfg.BppID)
	if err != nil {
		return publishResult{http.StatusInternalServerError, gin.H{"error": err.Error()}}
	}
	defer offerRows.Close()

	type rawOffer struct {
		offer catalog.Offer
	}
	offerMap := map[string]*rawOffer{}
	// The offers' attributes as stored, for a fee stored the earlier way
	// (policies.commission) that FillFee carries over to the considerations.
	origAttrs := map[string]json.RawMessage{}
	offerOrder := []string{}

	for offerRows.Next() {
		var o catalog.Offer
		var rids []string
		var vs, ve pgtype.Timestamptz
		var provName, provID string
		if err := offerRows.Scan(&o.ID, &o.Descriptor.Name, &o.Descriptor.ShortDesc,
			&rids, &o.OfferAttributes, &vs, &ve, &provName, &provID); err != nil {
			continue
		}
		origAttrs[o.ID] = o.OfferAttributes
		o.OfferAttributes = offerterms.FillAttributes(o.OfferAttributes, offerterms.FromConfig(h.cfg))
		o.ResourceIDs = rids
		if provID != "" {
			o.Provider = &catalog.Provider{
				ID:         provID,
				Descriptor: catalog.Descriptor{Name: provName},
			}
		}
		if vs.Valid && ve.Valid {
			o.Validity = &catalog.TimePeriod{
				StartDate: vs.Time.UTC().Format(time.RFC3339),
				EndDate:   ve.Time.UTC().Format(time.RFC3339),
			}
		}
		offerMap[o.ID] = &rawOffer{offer: o}
		offerOrder = append(offerOrder, o.ID)
	}
	offerRows.Close()

	// Read considerations per offer.
	considRows, err := h.pool.Query(ctx, `
		SELECT offer_id, consideration_id, status_code, COALESCE(status_name,''),
		       consideration_attributes
		FROM offer_considerations
		WHERE catalog_id = $1 AND bpp_id = $2`,
		catalogID, h.cfg.BppID)
	if err == nil {
		defer considRows.Close()
		for considRows.Next() {
			var offerID, considID, statusCode, statusName string
			var attrs json.RawMessage
			if err := considRows.Scan(&offerID, &considID, &statusCode, &statusName, &attrs); err != nil {
				continue
			}
			if ro, ok := offerMap[offerID]; ok {
				ro.offer.Considerations = append(ro.offer.Considerations, catalog.Consideration{
					ID:                      considID,
					Status:                  catalog.Status{Code: statusCode, Name: statusName},
					ConsiderationAttributes: offerterms.FillFee(attrs, origAttrs[offerID], offerterms.FromConfig(h.cfg)),
				})
			}
		}
	}

	for _, oid := range offerOrder {
		if ro, ok := offerMap[oid]; ok {
			becknCat.Offers = append(becknCat.Offers, ro.offer)
		}
	}

	// The Beckn catalog schema requires at least one of resources/offers --
	// reject here with a clear message instead of forwarding an empty
	// catalog and relaying the CDS's own cryptic schema-validation error.
	if len(becknCat.Resources) == 0 && len(becknCat.Offers) == 0 {
		return publishResult{http.StatusBadRequest, gin.H{"error": "catalog has no products or offers to publish"}}
	}

	// Forward to CDS using the same CDSCatalog stripping logic.
	start := time.Now()
	txID := uuid.New()
	msgID := uuid.New()

	becknReq := catalog.OnixPublishRequest{
		Context: catalog.BecknContext{
			Version:       "2.0.0",
			Action:        "catalog/publish",
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			TransactionID: txID.String(),
			MessageID:     msgID.String(),
			BppID:         h.cfg.BppID,
			BppURI:        h.cfg.PublishedBppURI(),
			NetworkID:     h.cfg.NetworkID,
		},
		Message: catalog.OnixPublishMessage{
			Catalogs:          []catalog.OnixCatalog{catalog.ToOnixCatalog(becknCat, h.cfg.BppID, h.cfg.PublishedBppURI())},
			PublishDirectives: []catalog.OnixPublishDirective{catalog.ToOnixPublishDirective(becknCat, h.cfg.BppID)},
		},
		ForceBaseline: forceBaseline,
	}

	body, _ := json.Marshal(becknReq)

	// respBody and callErr are captured by the deferred audit log below,
	// regardless of which branch returns.
	var respBody []byte
	var callErr error
	defer func() {
		var errMsg *string
		if callErr != nil {
			errMsg = strPtr(callErr.Error())
		}
		ackStatus := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
		if callErr != nil {
			ackStatus = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
		}
		dur := int32(time.Since(start).Milliseconds())
		if err := dbsqlc.New(h.pool).InsertMessageLog(context.Background(), dbsqlc.InsertMessageLogParams{
			TransactionID:        pgtype.UUID{Bytes: txID, Valid: true},
			MessageID:            msgID,
			Action:               dbsqlc.BecknActionCatalogPublish,
			Direction:            dbsqlc.MessageDirectionOUTBOUND,
			Url:                  strPtr(h.cfg.CDSPublishURL),
			BppID:                strPtr(h.cfg.BppID),
			BppUri:               strPtr(h.cfg.BppURI),
			NetworkID:            strPtr(h.cfg.NetworkID),
			AckStatus:            ackStatus,
			RequestPayload:       body,
			ResponsePayload:      json.RawMessage(respBody),
			ErrorMessage:         errMsg,
			ProcessingDurationMs: &dur,
		}); err != nil && h.lh != nil {
			h.lh.WithModule("dashboardsvc").Warn().Error(err).Log("failed to write beckn_message_log")
		}
	}()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.CDSPublishURL, bytes.NewReader(body))
	if err != nil {
		callErr = fmt.Errorf("build CDS request: %w", err)
		return publishResult{http.StatusBadGateway, gin.H{"error": callErr.Error()}}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if h.cfg.BppPrivateKey != "" {
		authHeader, authErr := catalog.BuildAuthHeader(body, h.cfg.BppKeyID, h.cfg.BppPrivateKey)
		if authErr != nil {
			callErr = fmt.Errorf("build auth header: %w", authErr)
			return publishResult{http.StatusInternalServerError, gin.H{"error": callErr.Error()}}
		}
		httpReq.Header.Set("Authorization", authHeader)
	}

	httpClient := httpclient.New(30 * time.Second)
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		callErr = fmt.Errorf("CDS call failed: %w", err)
		return publishResult{http.StatusBadGateway, gin.H{"error": callErr.Error()}}
	}
	defer resp.Body.Close() //nolint:errcheck
	respBody, _ = io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		callErr = fmt.Errorf("CDS returned %d: %s", resp.StatusCode, string(respBody))
		return publishResult{http.StatusBadGateway, gin.H{"error": callErr.Error()}}
	}

	// Stamp the successful publish time.
	_, _ = h.pool.Exec(ctx,
		`UPDATE catalogs SET network_published_at = NOW() WHERE id = $1 AND bpp_id = $2`,
		catalogID, h.cfg.BppID)

	return publishResult{http.StatusOK, gin.H{"status": "ACK", "message": "Catalog published to CDS"}}
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/catalogs/:id  — retire (if ever published) and soft-delete
//
// Retirement is one-way (NFH-014 §10.4): once a catalog has been published,
// deleting it tells onix-catalog-publish's catalog-publisher plugin to
// write a tombstone entry (a "retire" sibling of catalogs/publishDirectives
// on its own publish request, not a beckn.yaml field) so discovery-service
// stops serving it -- and this catalog id can never be reused afterwards
// (see HandleCreateCatalog's duplicate-id check, which deliberately ignores
// deleted_at so a retired id stays blocked forever).
// ---------------------------------------------------------------------------

func (h *Handler) HandleDeleteCatalog(c *gin.Context) {
	catalogID := c.Param("id")
	ctx := c.Request.Context()

	if owned, err := h.catalogOwnedByProvider(ctx, catalogID, auth.ProviderIDFromContext(c)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	} else if !owned {
		c.JSON(http.StatusNotFound, gin.H{"error": "catalog not found"})
		return
	}

	var publishedAt pgtype.Timestamptz
	if err := h.pool.QueryRow(ctx,
		`SELECT network_published_at FROM catalogs WHERE id = $1 AND bpp_id = $2 AND deleted_at IS NULL`,
		catalogID, h.cfg.BppID,
	).Scan(&publishedAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "catalog not found"})
		return
	}

	if publishedAt.Valid {
		if h.cfg.CDSPublishURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "CDS_PUBLISH_URL not configured"})
			return
		}

		start := time.Now()
		txID := uuid.New()
		msgID := uuid.New()

		becknReq := catalog.OnixPublishRequest{
			Context: catalog.BecknContext{
				Version:       "2.0.0",
				Action:        "catalog/publish",
				Timestamp:     time.Now().UTC().Format(time.RFC3339),
				TransactionID: txID.String(),
				MessageID:     msgID.String(),
				BppID:         h.cfg.BppID,
				BppURI:        h.cfg.PublishedBppURI(),
				NetworkID:     h.cfg.NetworkID,
			},
			Retire: []string{h.cfg.BppID + "/" + catalogID},
		}

		body, _ := json.Marshal(becknReq)

		var respBody []byte
		var callErr error
		defer func() {
			var errMsg *string
			if callErr != nil {
				errMsg = strPtr(callErr.Error())
			}
			ackStatus := dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusACK, Valid: true}
			if callErr != nil {
				ackStatus = dbsqlc.NullAckStatus{AckStatus: dbsqlc.AckStatusNACK, Valid: true}
			}
			dur := int32(time.Since(start).Milliseconds())
			if err := dbsqlc.New(h.pool).InsertMessageLog(context.Background(), dbsqlc.InsertMessageLogParams{
				TransactionID:        pgtype.UUID{Bytes: txID, Valid: true},
				MessageID:            msgID,
				Action:               actionCatalogRetire,
				Direction:            dbsqlc.MessageDirectionOUTBOUND,
				Url:                  strPtr(h.cfg.CDSPublishURL),
				BppID:                strPtr(h.cfg.BppID),
				BppUri:               strPtr(h.cfg.BppURI),
				NetworkID:            strPtr(h.cfg.NetworkID),
				AckStatus:            ackStatus,
				RequestPayload:       body,
				ResponsePayload:      json.RawMessage(respBody),
				ErrorMessage:         errMsg,
				ProcessingDurationMs: &dur,
			}); err != nil && h.lh != nil {
				h.lh.WithModule("dashboardsvc").Warn().Error(err).Log("failed to write beckn_message_log")
			}
		}()

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.cfg.CDSPublishURL, bytes.NewReader(body))
		if err != nil {
			callErr = fmt.Errorf("build CDS request: %w", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": callErr.Error()})
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if h.cfg.BppPrivateKey != "" {
			authHeader, authErr := catalog.BuildAuthHeader(body, h.cfg.BppKeyID, h.cfg.BppPrivateKey)
			if authErr != nil {
				callErr = fmt.Errorf("build auth header: %w", authErr)
				c.JSON(http.StatusInternalServerError, gin.H{"error": callErr.Error()})
				return
			}
			httpReq.Header.Set("Authorization", authHeader)
		}

		httpClient := httpclient.New(30 * time.Second)
		resp, err := httpClient.Do(httpReq)
		if err != nil {
			callErr = fmt.Errorf("CDS call failed: %w", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": callErr.Error()})
			return
		}
		defer resp.Body.Close() //nolint:errcheck
		respBody, _ = io.ReadAll(resp.Body)

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			callErr = fmt.Errorf("CDS returned %d: %s", resp.StatusCode, string(respBody))
			c.JSON(http.StatusBadGateway, gin.H{"error": callErr.Error()})
			return
		}
	}

	if _, err := h.pool.Exec(ctx,
		`UPDATE catalogs SET deleted_at = NOW() WHERE id = $1 AND bpp_id = $2`,
		catalogID, h.cfg.BppID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete catalog: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ---------------------------------------------------------------------------
// GET /api/v1/catalog-index  — proxy the published Beckn catalog index
// GET /api/v1/catalogs/:id/latest-file  — proxy one catalog's latest published file
//
// Both read from h.cfg.CatalogFilesURL(), not directly from the browser:
// locally that's catalog-files, an internal-network-only nginx container
// with no host port, no CORS headers, and no Content-Encoding: gzip on its
// .json.gz files -- a browser fetch() can't reach it at all. Proxying here
// also means the exact same frontend code works unchanged once deployed,
// where CatalogFilesURL() falls back to the real public domain.
// ---------------------------------------------------------------------------

func (h *Handler) HandleGetCatalogIndex(c *gin.Context) {
	base := h.cfg.CatalogFilesURL()
	if base == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CATALOG_FILES_BASE_URL/CATALOG_BASE_URL not configured"})
		return
	}
	body, status, err := fetchCatalogFile(c.Request.Context(), base+"/index/becknCatalogs.index.json", false)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if status == http.StatusNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "no catalog has been published yet"})
		return
	}
	if status < 200 || status >= 300 {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("catalog file store returned %d", status)})
		return
	}
	c.Data(http.StatusOK, "application/json", body)
}

func (h *Handler) HandleGetCatalogLatestFile(c *gin.Context) {
	catalogID := c.Param("id")
	ctx := c.Request.Context()

	if !h.ownedCatalog(c, catalogID) {
		return
	}

	base := h.cfg.CatalogFilesURL()
	if base == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CATALOG_FILES_BASE_URL/CATALOG_BASE_URL not configured"})
		return
	}
	body, status, err := fetchCatalogFile(ctx, base+"/catalogs/"+catalogID+".latest.json.gz", true)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if status == http.StatusNotFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "this catalog has not been published yet"})
		return
	}
	if status < 200 || status >= 300 {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("catalog file store returned %d", status)})
		return
	}
	c.Data(http.StatusOK, "application/json", body)
}

// fetchCatalogFile GETs url and, if gunzip is true, decompresses the body
// (the catalog-publisher plugin's .json.gz files carry no Content-Encoding
// header, so this is always a manual gunzip, never something net/http's
// client would do for us). Returns the raw status code uninterpreted so
// callers can turn a 404 into a friendly "not published yet" message.
func fetchCatalogFile(ctx context.Context, url string, gunzip bool) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	httpClient := httpclient.New(15 * time.Second)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, nil
	}

	if !gunzip {
		body, err := io.ReadAll(resp.Body)
		return body, resp.StatusCode, err
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close() //nolint:errcheck
	body, err := io.ReadAll(gz)
	return body, resp.StatusCode, err
}

// ---------------------------------------------------------------------------
// GET /api/v1/inventory/items  — resources grouped with catalog + provider info + optional filters
// ---------------------------------------------------------------------------

// HandleInventoryItems is always scoped to the authenticated seller's own
// provider -- provider_id is no longer a client-chosen filter. catalog_id
// stays client-choosable as a narrowing filter within that scope; a
// catalog_id belonging to a different provider just yields zero rows
// (the provider_id condition below still applies), not another seller's
// data.
func (h *Handler) HandleInventoryItems(c *gin.Context) {
	page, limit := parsePagination(c)
	catalogFilter := c.DefaultQuery("catalog_id", "")
	providerFilter := auth.ProviderIDFromContext(c)
	search := c.DefaultQuery("search", "")
	ctx := c.Request.Context()

	countSQL := `
		SELECT COUNT(*)
		FROM resources r
		JOIN catalogs c ON c.id = r.catalog_id AND c.bpp_id = r.bpp_id
		WHERE r.bpp_id = $1 AND r.deleted_at IS NULL
		  AND ($2 = '' OR r.catalog_id = $2)
		  AND ($3 = '' OR c.provider_id = $3)
		  AND ($4 = '' OR r.descriptor_name ILIKE '%' || $4 || '%' OR r.id ILIKE '%' || $4 || '%')`

	var total int64
	if err := h.pool.QueryRow(ctx, countSQL, h.cfg.BppID, catalogFilter, providerFilter, search).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	listSQL := `
		SELECT r.id, r.descriptor_name, COALESCE(r.descriptor_short_desc,''),
		       r.catalog_id, c.descriptor_name AS catalog_name,
		       c.provider_id, COALESCE(p.descriptor_name, c.provider_id),
		       COALESCE(r.resource_attributes->'availability'->>'status', ''),
		       r.created_at
		FROM resources r
		JOIN catalogs c ON c.id = r.catalog_id AND c.bpp_id = r.bpp_id
		LEFT JOIN providers p ON p.id = c.provider_id AND p.bpp_id = r.bpp_id
		WHERE r.bpp_id = $1 AND r.deleted_at IS NULL
		  AND ($2 = '' OR r.catalog_id = $2)
		  AND ($3 = '' OR c.provider_id = $3)
		  AND ($4 = '' OR r.descriptor_name ILIKE '%' || $4 || '%' OR r.id ILIKE '%' || $4 || '%')
		ORDER BY c.provider_id, r.catalog_id, r.descriptor_name
		LIMIT $5 OFFSET $6`

	rows, err := h.pool.Query(ctx, listSQL, h.cfg.BppID, catalogFilter, providerFilter, search, limit, (page-1)*limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type invItem struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		ShortDesc    string `json:"shortDesc"`
		CatalogID    string `json:"catalogId"`
		CatalogName  string `json:"catalogName"`
		ProviderID   string `json:"providerId"`
		ProviderName string `json:"providerName"`
		// Availability is the published availability.status (IN_STOCK, ...).
		Availability string `json:"availability"`
		CreatedAt    string `json:"createdAt"`
	}

	items := make([]invItem, 0)
	for rows.Next() {
		var item invItem
		var ca pgtype.Timestamptz
		if err := rows.Scan(&item.ID, &item.Name, &item.ShortDesc,
			&item.CatalogID, &item.CatalogName,
			&item.ProviderID, &item.ProviderName,
			&item.Availability, &ca); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if ca.Valid {
			item.CreatedAt = ca.Time.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}

	// Products per availability status, over everything the filters match
	// (not only this page). A product without one counts as "".
	counts := map[string]int64{}
	crows, err := h.pool.Query(ctx, `
		SELECT COALESCE(r.resource_attributes->'availability'->>'status', ''), COUNT(*)
		FROM resources r
		JOIN catalogs c ON c.id = r.catalog_id AND c.bpp_id = r.bpp_id
		WHERE r.bpp_id = $1 AND r.deleted_at IS NULL
		  AND ($2 = '' OR r.catalog_id = $2)
		  AND ($3 = '' OR c.provider_id = $3)
		  AND ($4 = '' OR r.descriptor_name ILIKE '%' || $4 || '%' OR r.id ILIKE '%' || $4 || '%')
		GROUP BY 1`, h.cfg.BppID, catalogFilter, providerFilter, search)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for crows.Next() {
		var st string
		var n int64
		if crows.Scan(&st, &n) == nil {
			counts[st] = n
		}
	}
	crows.Close()

	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "page": page, "limit": limit, "availability": counts})
}

// ---------------------------------------------------------------------------
// GET /api/v1/catalogs/:id/resources  — list resources for a specific catalog (for offer picker)
// ---------------------------------------------------------------------------

func (h *Handler) HandleGetCatalogResources(c *gin.Context) {
	catalogID := c.Param("id")
	ctx := c.Request.Context()
	rows, err := h.pool.Query(ctx,
		`SELECT id, descriptor_name FROM resources
		 WHERE catalog_id = $1 AND bpp_id = $2 AND provider_id = $3 AND deleted_at IS NULL ORDER BY descriptor_name`,
		catalogID, h.cfg.BppID, auth.ProviderIDFromContext(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	type r struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	items := make([]r, 0)
	for rows.Next() {
		var x r
		if err := rows.Scan(&x.ID, &x.Name); err != nil {
			continue
		}
		items = append(items, x)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// ---------------------------------------------------------------------------
// POST /api/v1/catalogs/:id/offers  — add a standalone offer to an existing catalog
// ---------------------------------------------------------------------------

func (h *Handler) HandleAddOffer(c *gin.Context) {
	catalogID := c.Param("id")
	var req catalog.Offer
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "offer id is required"})
		return
	}
	ctx := c.Request.Context()
	providerID := auth.ProviderIDFromContext(c)

	if owned, err := h.catalogOwnedByProvider(ctx, catalogID, providerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	} else if !owned {
		c.JSON(http.StatusNotFound, gin.H{"error": "catalog not found"})
		return
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	qt := dbsqlc.New(tx)

	// Forced to the caller's own provider regardless of what ID was
	// submitted -- see the identical note in HandleAddProduct.
	var offerProviderID *string
	if req.Provider != nil && req.Provider.ID != "" {
		if err := qt.UpsertProvider(ctx, dbsqlc.UpsertProviderParams{
			ID:                   providerID,
			BppID:                h.cfg.BppID,
			DescriptorName:       req.Provider.Descriptor.Name,
			DescriptorDocs:       json.RawMessage(`[]`),
			DescriptorMediaFiles: json.RawMessage(`[]`),
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "upsert provider: " + err.Error()})
			return
		}
		offerProviderID = strPtr(providerID)
	}

	d := offerterms.FromConfig(h.cfg)
	origAttrs := req.OfferAttributes
	req.OfferAttributes = offerterms.FillAttributes(req.OfferAttributes, d)
	oAttrsCtx, oAttrsType := extractContextType(req.OfferAttributes)
	var validityStart, validityEnd pgtype.Timestamptz
	if req.Validity != nil {
		validityStart = parseTimestampStr(req.Validity.StartDate)
		validityEnd = parseTimestampStr(req.Validity.EndDate)
	}

	if err := qt.InsertOffer(ctx, dbsqlc.InsertOfferParams{
		ID:                     req.ID,
		CatalogID:              catalogID,
		BppID:                  h.cfg.BppID,
		ProviderID:             offerProviderID,
		DescriptorName:         strPtr(req.Descriptor.Name),
		DescriptorCode:         strPtr(req.Descriptor.Code),
		DescriptorShortDesc:    strPtr(req.Descriptor.ShortDesc),
		ResourceIds:            req.ResourceIDs,
		OfferAttributes:        req.OfferAttributes,
		OfferAttributesContext: strPtr(oAttrsCtx),
		OfferAttributesType:    strPtr(oAttrsType),
		ValidityStart:          validityStart,
		ValidityEnd:            validityEnd,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "insert offer: " + err.Error()})
		return
	}

	for _, consid := range req.Considerations {
		consid.ConsiderationAttributes = offerterms.FillFee(consid.ConsiderationAttributes, origAttrs, d)
		cAttrsCtx, cAttrsType := extractContextType(consid.ConsiderationAttributes)
		if err := qt.InsertOfferConsideration(ctx, dbsqlc.InsertOfferConsiderationParams{
			OfferID:                        req.ID,
			CatalogID:                      catalogID,
			BppID:                          h.cfg.BppID,
			ConsiderationID:                consid.ID,
			StatusCode:                     considerationStatusCode(consid.Status),
			StatusName:                     strPtr(consid.Status.Name),
			ConsiderationAttributes:        consid.ConsiderationAttributes,
			ConsiderationAttributesContext: strPtr(cAttrsCtx),
			ConsiderationAttributesType:    strPtr(cAttrsType),
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "insert consideration: " + err.Error()})
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func parseTS(v *struct {
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}, isStart bool) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	s := v.EndDate
	if isStart {
		s = v.StartDate
	}
	return parseTimestampStr(s)
}

func parseTimestampStr(s string) pgtype.Timestamptz {
	if s == "" {
		return pgtype.Timestamptz{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func extractContextType(raw json.RawMessage) (contextURL, typeName string) {
	if len(raw) == 0 {
		return "", ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", ""
	}
	if v, ok := m["@context"]; ok {
		_ = json.Unmarshal(v, &contextURL)
	}
	if v, ok := m["@type"]; ok {
		_ = json.Unmarshal(v, &typeName)
	}
	return
}

func considerationStatusCode(s catalog.Status) string {
	if s.Code != "" {
		return s.Code
	}
	return s.Name
}
