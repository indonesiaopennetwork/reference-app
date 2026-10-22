package dashboardsvc

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ion/winroom/bpp/internal/auth"
	"github.com/ion/winroom/bpp/internal/catalog"
	"github.com/ion/winroom/bpp/internal/offerterms"
)

// Editing a catalog's products (resources) and offers. Each handler loads or
// saves one item of a catalog the caller's provider owns. Publishing
// (catalog_handlers.go's HandlePublishCatalog) doesn't lock a catalog -- it
// can be edited and republished freely; onix-catalog-publish's own
// catalog-publisher plugin diffs against what was last published, so a later
// publish just sends the incremental change.

// ownedCatalog aborts with 404 / 500 unless the caller's provider owns catalogID.
func (h *Handler) ownedCatalog(c *gin.Context, catalogID string) bool {
	owned, err := h.catalogOwnedByProvider(c.Request.Context(), catalogID, auth.ProviderIDFromContext(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}
	if !owned {
		c.JSON(http.StatusNotFound, gin.H{"error": "catalog not found"})
		return false
	}
	return true
}

// editableCatalog aborts unless the caller's provider owns catalogID (404).
func (h *Handler) editableCatalog(c *gin.Context, catalogID string) bool {
	return h.ownedCatalog(c, catalogID)
}

// HandleGetResource (GET /catalogs/:id/resources/:rid) returns one resource
// in its publish shape.
func (h *Handler) HandleGetResource(c *gin.Context) {
	catalogID, rid := c.Param("id"), c.Param("rid")
	if !h.ownedCatalog(c, catalogID) {
		return
	}
	var r catalog.Resource
	var name, code, short, long *string
	var media, attrs json.RawMessage
	err := h.pool.QueryRow(c.Request.Context(), `
		SELECT r.id, r.descriptor_name, r.descriptor_code, r.descriptor_short_desc, r.descriptor_long_desc,
		       r.descriptor_media_files, r.resource_attributes
		  FROM resources r
		 WHERE r.id = $1 AND r.catalog_id = $2 AND r.bpp_id = $3 AND r.deleted_at IS NULL`,
		rid, catalogID, h.cfg.BppID).Scan(&r.ID, &name, &code, &short, &long, &media, &attrs)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	r.Descriptor = catalog.Descriptor{Name: strVal(name), Code: strVal(code), ShortDesc: strVal(short), LongDesc: strVal(long)}
	_ = json.Unmarshal(media, &r.Descriptor.MediaFile)
	r.ResourceAttributes = attrs
	c.JSON(http.StatusOK, gin.H{"resource": r})
}

// HandleUpdateResource (PUT /catalogs/:id/resources/:rid) replaces a
// resource's descriptor and attributes (its availability among them). The id
// can't change.
func (h *Handler) HandleUpdateResource(c *gin.Context) {
	catalogID, rid := c.Param("id"), c.Param("rid")
	var req catalog.Resource
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Descriptor.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "resource name is required"})
		return
	}
	if !h.editableCatalog(c, catalogID) {
		return
	}
	ctx := c.Request.Context()
	media, _ := json.Marshal(req.Descriptor.MediaFile)
	if len(req.Descriptor.MediaFile) == 0 {
		media = []byte(`[]`)
	}
	attrsCtx, attrsType := extractContextType(req.ResourceAttributes)
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `
		UPDATE resources SET descriptor_name = $4, descriptor_code = $5, descriptor_short_desc = $6,
		       descriptor_long_desc = $7, descriptor_media_files = $8, resource_attributes = $9,
		       resource_attributes_context = $10, resource_attributes_type = $11, updated_at = NOW()
		 WHERE id = $1 AND catalog_id = $2 AND bpp_id = $3 AND deleted_at IS NULL`,
		rid, catalogID, h.cfg.BppID, req.Descriptor.Name, strPtr(req.Descriptor.Code), strPtr(req.Descriptor.ShortDesc),
		strPtr(req.Descriptor.LongDesc), media, rawOrNil(req.ResourceAttributes), strPtr(attrsCtx), strPtr(attrsType))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update resource: " + err.Error()})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource not found"})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

// HandleDeleteOffer (DELETE /catalogs/:id/offers/:oid) soft-deletes an offer.
func (h *Handler) HandleDeleteOffer(c *gin.Context) {
	catalogID, oid := c.Param("id"), c.Param("oid")
	if !h.ownedCatalog(c, catalogID) {
		return
	}
	tag, err := h.pool.Exec(c.Request.Context(), `
		UPDATE offers SET deleted_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND catalog_id = $2 AND bpp_id = $3 AND deleted_at IS NULL`,
		oid, catalogID, h.cfg.BppID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete offer: " + err.Error()})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "offer not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// HandleGetOffer (GET /catalogs/:id/offers/:oid) returns one offer in its
// publish shape, with its considerations (prices).
func (h *Handler) HandleGetOffer(c *gin.Context) {
	catalogID, oid := c.Param("id"), c.Param("oid")
	if !h.ownedCatalog(c, catalogID) {
		return
	}
	ctx := c.Request.Context()
	var o catalog.Offer
	var name, code, short *string
	var attrs json.RawMessage
	var vs, ve pgtype.Timestamptz
	err := h.pool.QueryRow(ctx, `
		SELECT id, descriptor_name, descriptor_code, descriptor_short_desc, resource_ids, offer_attributes,
		       validity_start, validity_end
		  FROM offers WHERE id = $1 AND catalog_id = $2 AND bpp_id = $3 AND deleted_at IS NULL`,
		oid, catalogID, h.cfg.BppID).Scan(&o.ID, &name, &code, &short, &o.ResourceIDs, &attrs, &vs, &ve)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "offer not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	o.Descriptor = catalog.Descriptor{Name: strVal(name), Code: strVal(code), ShortDesc: strVal(short)}
	o.OfferAttributes = attrs
	if vs.Valid || ve.Valid {
		o.Validity = &catalog.TimePeriod{}
		if vs.Valid {
			o.Validity.StartDate = vs.Time.UTC().Format(time.RFC3339)
		}
		if ve.Valid {
			o.Validity.EndDate = ve.Time.UTC().Format(time.RFC3339)
		}
	}
	rows, err := h.pool.Query(ctx, `
		SELECT consideration_id, status_code, COALESCE(status_name, ''), consideration_attributes
		  FROM offer_considerations WHERE offer_id = $1 AND catalog_id = $2 AND bpp_id = $3`, oid, catalogID, h.cfg.BppID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	for rows.Next() {
		var cons catalog.Consideration
		if err := rows.Scan(&cons.ID, &cons.Status.Code, &cons.Status.Name, &cons.ConsiderationAttributes); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		o.Considerations = append(o.Considerations, cons)
	}
	c.JSON(http.StatusOK, gin.H{"offer": o})
}

// HandleUpdateOffer (PUT /catalogs/:id/offers/:oid) replaces an offer's
// descriptor, resources, attributes, validity and considerations (prices).
func (h *Handler) HandleUpdateOffer(c *gin.Context) {
	catalogID, oid := c.Param("id"), c.Param("oid")
	var req catalog.Offer
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Descriptor.Name == "" || len(req.ResourceIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "offer name and at least one resource are required"})
		return
	}
	if !h.editableCatalog(c, catalogID) {
		return
	}
	ctx := c.Request.Context()
	d := offerterms.FromConfig(h.cfg)
	origAttrs := req.OfferAttributes
	req.OfferAttributes = offerterms.FillAttributes(req.OfferAttributes, d)
	oAttrsCtx, oAttrsType := extractContextType(req.OfferAttributes)
	var vs, ve pgtype.Timestamptz
	if req.Validity != nil {
		vs = parseTimestampStr(req.Validity.StartDate)
		ve = parseTimestampStr(req.Validity.EndDate)
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tag, err := tx.Exec(ctx, `
		UPDATE offers SET descriptor_name = $4, descriptor_code = $5, descriptor_short_desc = $6, resource_ids = $7,
		       offer_attributes = $8, offer_attributes_context = $9, offer_attributes_type = $10,
		       validity_start = $11, validity_end = $12, updated_at = NOW()
		 WHERE id = $1 AND catalog_id = $2 AND bpp_id = $3 AND deleted_at IS NULL`,
		oid, catalogID, h.cfg.BppID, req.Descriptor.Name, strPtr(req.Descriptor.Code), strPtr(req.Descriptor.ShortDesc),
		req.ResourceIDs, rawOrNil(req.OfferAttributes), strPtr(oAttrsCtx), strPtr(oAttrsType), vs, ve)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "update offer: " + err.Error()})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "offer not found"})
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM offer_considerations WHERE offer_id = $1 AND catalog_id = $2 AND bpp_id = $3`,
		oid, catalogID, h.cfg.BppID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "replace considerations: " + err.Error()})
		return
	}
	for _, cons := range req.Considerations {
		cons.ConsiderationAttributes = offerterms.FillFee(cons.ConsiderationAttributes, origAttrs, d)
		cCtx, cType := extractContextType(cons.ConsiderationAttributes)
		if _, err := tx.Exec(ctx, `
			INSERT INTO offer_considerations (offer_id, catalog_id, bpp_id, consideration_id, status_code, status_name,
			       consideration_attributes, consideration_attributes_context, consideration_attributes_type)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			oid, catalogID, h.cfg.BppID, cons.ID, considerationStatusCode(cons.Status), strPtr(cons.Status.Name),
			cons.ConsiderationAttributes, strPtr(cCtx), strPtr(cType)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "insert consideration: " + err.Error()})
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func rawOrNil(b json.RawMessage) json.RawMessage {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}
