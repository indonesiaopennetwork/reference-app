package qrsvc

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Merchant coverage for qr-service.
//
// qr-service checks, before activating a packet, that some merchant near the
// packet's node stocks every item. qr-service is not a network
// participant (it has no Beckn identity), so it asks this BAP: the check runs
// the same Beckn discover -> on_discover exchange through onix-bap, and the
// same matching, as a scan's resolve. Activation and scanning therefore agree
// on which merchants serve a packet.

// CoverageRequest is POST /api/v1/qr/coverage.
type CoverageRequest struct {
	Lat          float64        `json:"lat" binding:"required"`
	Lon          float64        `json:"lon" binding:"required"`
	RadiusMeters int            `json:"radius_meters" binding:"required,min=1"`
	Items        []CoverageItem `json:"items" binding:"required,min=1,dive"`
}

// CoverageItem is one packet item to look for.
type CoverageItem struct {
	MatchType   string `json:"match_type" binding:"required,oneof=GTIN ION_SKU TEXT_QUERY"`
	MatchValue  string `json:"match_value" binding:"required"`
	DisplayName string `json:"display_name" binding:"required"`
}

// CoverageResponse is qr-service's coverage report shape.
type CoverageResponse struct {
	// Merchants stocking every item within the radius, nearest first.
	EligibleProviders []CoverageProvider `json:"eligible_providers"`
	// Empty when some merchant stocks everything.
	MissingItems []string `json:"missing_items"`
}

// CoverageProvider is one eligible merchant.
type CoverageProvider struct {
	BppID        string `json:"bpp_id"`
	ProviderID   string `json:"provider_id"`
	ProviderName string `json:"provider_name"`
	// -1 when the merchant's catalog states no location.
	DistanceMeters int `json:"distance_meters"`
}

// Coverage runs the Beckn discover for a packet's items around its node.
func (s *Service) Coverage(ctx context.Context, req CoverageRequest) (*CoverageResponse, error) {
	p := &Packet{Lat: req.Lat, Lon: req.Lon, RadiusMeters: req.RadiusMeters}
	for i, it := range req.Items {
		p.Items = append(p.Items, PacketItem{
			ID: strconv.Itoa(i), MatchType: it.MatchType, MatchValue: it.MatchValue, DisplayName: it.DisplayName,
		})
	}
	m, err := s.match(ctx, p, nil)
	if err != nil {
		s.log().Warn().Error(err).Log("coverage discover failed")
		return nil, newErr(502, "DISCOVERY_FAILED", "Couldn't search nearby merchants: "+err.Error())
	}
	sum := m.summarize() // the minimum basket is for the buyer's choice, not coverage

	out := &CoverageResponse{EligibleProviders: []CoverageProvider{}, MissingItems: []string{}}
	if len(sum.eligible) == 0 {
		out.MissingItems = nonNil(sum.missing)
	}
	for _, rc := range sum.eligible {
		dist := -1
		if rc.m.distance != nil {
			dist = int(*rc.m.distance + 0.5)
		}
		out.EligibleProviders = append(out.EligibleProviders, CoverageProvider{
			BppID: rc.m.bppID, ProviderID: rc.m.providerID, ProviderName: rc.m.providerName, DistanceMeters: dist,
		})
	}
	return out, nil
}

// HandleCoverage answers qr-service's coverage check.
//
//	@Summary		Merchant coverage for a packet (for qr-service)
//	@ID				qrCoverage
//	@Description	Runs the Beckn discover (through onix-bap unless QR_DISCOVER_METHOD=GET) around a packet's node and reports the merchants that stock every item. Called by qr-service before it activates a packet; authenticated with X-Service-Key = QR_COVERAGE_KEY.
//	@Tags			qr
//	@Accept			json
//	@Produce		json
//	@Param			X-Service-Key	header		string			true	"QR_COVERAGE_KEY"
//	@Param			request			body		CoverageRequest	true	"Request body"
//	@Success		200				{object}	CoverageResponse
//	@Failure		401				{object}	ErrorResponse
//	@Failure		502				{object}	ErrorResponse	"DISCOVERY_FAILED"
//	@Failure		503				{object}	ErrorResponse	"COVERAGE_NOT_CONFIGURED"
//	@Router			/api/v1/qr/coverage [post]
func (h *Handler) HandleCoverage(c *gin.Context) {
	key := h.svc.cfg.QRCoverageKey
	if key == "" {
		fail(c, h.svc, newErr(503, "COVERAGE_NOT_CONFIGURED", "QR_COVERAGE_KEY is not set on this BAP"))
		return
	}
	if subtle.ConstantTimeCompare([]byte(c.GetHeader("X-Service-Key")), []byte(key)) != 1 {
		fail(c, h.svc, newErr(401, "UNAUTHORIZED", "invalid X-Service-Key"))
		return
	}
	var req CoverageRequest
	if !bind(c, &req) {
		return
	}
	resp, err := h.svc.Coverage(c.Request.Context(), req)
	if err != nil {
		fail(c, h.svc, err)
		return
	}
	h.svc.log().Info().LogActivity("coverage checked", map[string]any{
		"items": len(req.Items), "eligible": len(resp.EligibleProviders), "method": h.svc.cfg.QRDiscoverMethod,
	})
	c.JSON(http.StatusOK, resp)
}
