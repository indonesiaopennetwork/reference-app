package qrsvc

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Product search for the packet console's basket: instead of typing a match
// rule, the admin searches the network (a Beckn discover, through onix-bap
// like the rest of the QR flow) and picks products. Each result carries a
// suggested match rule that finds the same product at any merchant: its
// barcode (GTIN) when the catalog states one, otherwise the main words of
// its name (TEXT_QUERY).

const maxProductResults = 60

// ProductResult is one product a merchant offers.
type ProductResult struct {
	ResourceID   string  `json:"resource_id"`
	Name         string  `json:"name"`
	ShortDesc    string  `json:"short_desc,omitempty"`
	Image        string  `json:"image,omitempty"`
	GTIN         string  `json:"gtin,omitempty"`
	ProviderID   string  `json:"provider_id"`
	ProviderName string  `json:"provider_name"`
	BppID        string  `json:"bpp_id"`
	Price        float64 `json:"price"`
	Currency     string  `json:"currency"`
	// -1 when the merchant's catalog states no location, or no location was given.
	DistanceMeters int `json:"distance_meters"`
	// The packet item this product becomes when picked.
	DisplayName    string `json:"display_name"`
	SuggestedMatch struct {
		MatchType  string `json:"match_type" enums:"GTIN,TEXT_QUERY"`
		MatchValue string `json:"match_value"`
	} `json:"suggested_match"`
}

// ProductSearchResponse answers GET /api/v1/qr/products.
type ProductSearchResponse struct {
	Products []ProductResult `json:"products"`
	// True when results were limited to the radius around lat/lon.
	NearLocation bool `json:"near_location"`
}

// SearchProducts runs a Beckn discover for q, around (lat, lon) when given.
func (s *Service) SearchProducts(ctx context.Context, q string, lat, lon *float64, radius int) (*ProductSearchResponse, error) {
	q = strings.TrimSpace(q)
	if len(q) < 2 {
		return nil, newErr(400, "QUERY_TOO_SHORT", "Type at least 2 characters to search")
	}
	if s.cfg.QRDiscoverMethod == "GET" && s.cfg.CDSDiscoverURL == "" {
		return nil, newErr(503, "DISCOVERY_NOT_CONFIGURED", "This BAP has no discovery service configured (CDS_DISCOVER_URL)")
	}
	var spatial []map[string]any
	near := lat != nil && lon != nil && radius > 0
	if near {
		spatial = spatialWithin(*lat, *lon, radius)
	}
	resp, err := s.discover(ctx, q, spatial, nil)
	if err != nil {
		s.log().Warn().Error(err).Log("product search discover failed")
		return nil, newErr(502, "DISCOVERY_FAILED", "Couldn't search the network, try again shortly")
	}

	out := &ProductSearchResponse{Products: []ProductResult{}, NearLocation: near}
	seen := map[string]bool{}
	terms := strings.Fields(strings.ToLower(q))
	for _, cat := range resp.Message.Catalogs {
		for _, res := range cat.Resources {
			if res.ID == "" || seen[cat.BppID+"|"+res.ID] || !hasAllTerms(res, terms) {
				continue
			}
			pr, ok := productResult(cat, res, lat, lon)
			if !ok {
				continue
			}
			if near && pr.DistanceMeters > radius {
				continue
			}
			seen[cat.BppID+"|"+res.ID] = true
			out.Products = append(out.Products, pr)
		}
	}
	sort.SliceStable(out.Products, func(a, b int) bool {
		pa, pb := out.Products[a], out.Products[b]
		if (pa.DistanceMeters < 0) != (pb.DistanceMeters < 0) {
			return pa.DistanceMeters >= 0
		}
		if pa.DistanceMeters != pb.DistanceMeters {
			return pa.DistanceMeters < pb.DistanceMeters
		}
		return pa.Name < pb.Name
	})
	if len(out.Products) > maxProductResults {
		out.Products = out.Products[:maxProductResults]
	}
	return out, nil
}

// hasAllTerms: every search word is in the product's own name/descriptions
// (discover also matches on the provider name).
func hasAllTerms(res resource, terms []string) bool {
	hay := strings.ToLower(res.Descriptor.Name + " " + res.Descriptor.ShortDesc + " " + res.Descriptor.LongDesc)
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

// productResult builds a result from a resource and its cheapest priced offer.
func productResult(cat catalog, res resource, lat, lon *float64) (ProductResult, bool) {
	var best *offer
	var bestPrice float64
	var currency string
	for i, o := range cat.Offers {
		if !contains(o.ResourceIDs, res.ID) {
			continue
		}
		if p, cur, _, ok := o.price(); ok && (best == nil || p < bestPrice) {
			best, bestPrice, currency = &cat.Offers[i], p, cur
		}
	}
	if best == nil {
		return ProductResult{}, false
	}
	prov := best.Provider
	if prov == nil || prov.ID == "" {
		prov = cat.Provider
	}
	if prov == nil {
		prov = &provider{}
	}
	pr := ProductResult{
		ResourceID: res.ID, Name: res.Descriptor.Name, ShortDesc: res.Descriptor.ShortDesc,
		ProviderID: prov.ID, ProviderName: prov.Descriptor.Name, BppID: cat.BppID,
		Price: bestPrice, Currency: currency, DistanceMeters: -1,
	}
	if pr.ProviderName == "" {
		pr.ProviderName = prov.ID
	}
	if pr.Currency == "" {
		pr.Currency = currencyIDR
	}
	if len(res.Descriptor.MediaFile) > 0 {
		pr.Image = res.Descriptor.MediaFile[0].URI
	}
	if lat != nil && lon != nil {
		if d, _, ok := nearest(prov.AvailableAt, *lat, *lon); ok {
			pr.DistanceMeters = int(d + 0.5)
		}
	}
	pr.DisplayName = displayName(res.Descriptor.Name)
	if gtin := findGTIN(res.ResourceAttributes); gtin != "" {
		pr.GTIN = gtin
		pr.SuggestedMatch.MatchType, pr.SuggestedMatch.MatchValue = "GTIN", gtin
	} else {
		pr.SuggestedMatch.MatchType, pr.SuggestedMatch.MatchValue = "TEXT_QUERY", nameQuery(res.Descriptor.Name)
	}
	return pr, true
}

var (
	parenRE   = regexp.MustCompile(`\s*\([^)]*\)`)
	nonWordRE = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	digitsRE  = regexp.MustCompile(`^\d{8,14}$`)
)

// displayName is the product name without a bracketed translation:
// "Keripik Singkong Balado (Spicy Cassava Chips)" -> "Keripik Singkong Balado".
func displayName(name string) string {
	if d := strings.TrimSpace(parenRE.ReplaceAllString(name, "")); d != "" {
		return d
	}
	return strings.TrimSpace(name)
}

// nameQuery is a TEXT_QUERY that finds the product by name at any merchant:
// the display name's first words, lowercased, punctuation removed.
func nameQuery(name string) string {
	words := strings.Fields(strings.ToLower(nonWordRE.ReplaceAllString(displayName(name), " ")))
	if len(words) > 5 {
		words = words[:5]
	}
	return strings.Join(words, " ")
}

// findGTIN returns a barcode stated in resourceAttributes: the first value
// of 8-14 digits under a key naming a GTIN / EAN / UPC / barcode.
func findGTIN(raw []byte) string {
	var attrs any
	if len(raw) == 0 || json.Unmarshal(raw, &attrs) != nil {
		return ""
	}
	var walk func(v any) string
	walk = func(v any) string {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				lk := strings.ToLower(k)
				if strings.Contains(lk, "gtin") || strings.Contains(lk, "ean") || strings.Contains(lk, "upc") || strings.Contains(lk, "barcode") {
					switch val := x.(type) {
					case string:
						if digitsRE.MatchString(val) {
							return val
						}
					case float64:
						if s := strconv.FormatFloat(val, 'f', 0, 64); digitsRE.MatchString(s) {
							return s
						}
					}
				}
				if g := walk(x); g != "" {
					return g
				}
			}
		case []any:
			for _, x := range t {
				if g := walk(x); g != "" {
					return g
				}
			}
		}
		return ""
	}
	return walk(attrs)
}

// HandleProducts searches products for the packet console's basket.
//
//	@Summary		Search products for a packet's basket (Beckn discover)
//	@ID				qrProducts
//	@Description	Runs a Beckn discover for q (through onix-bap unless QR_DISCOVER_METHOD=GET), around lat/lon within radius_meters when given, and returns the matching products with the match rule each becomes as a packet item. Login required.
//	@Tags			qr
//	@Produce		json
//	@Param			q				query		string	true	"Search words"
//	@Param			lat				query		number	false	"Packet latitude"
//	@Param			lon				query		number	false	"Packet longitude"
//	@Param			radius_meters	query		int		false	"Packet radius"
//	@Success		200				{object}	ProductSearchResponse
//	@Failure		401				{object}	ErrorResponse
//	@Failure		502				{object}	ErrorResponse	"DISCOVERY_FAILED"
//	@Security		BearerAuth
//	@Router			/api/v1/qr/products [get]
func (h *Handler) HandleProducts(c *gin.Context) {
	if subject(c) == nil {
		fail(c, h.svc, newErr(401, "LOGIN_REQUIRED", "Log in to search products"))
		return
	}
	var lat, lon *float64
	if v, err := strconv.ParseFloat(c.Query("lat"), 64); err == nil && v >= -90 && v <= 90 {
		lat = &v
	}
	if v, err := strconv.ParseFloat(c.Query("lon"), 64); err == nil && v >= -180 && v <= 180 {
		lon = &v
	}
	radius, _ := strconv.Atoi(c.Query("radius_meters"))
	resp, err := h.svc.SearchProducts(c.Request.Context(), c.Query("q"), lat, lon, radius)
	if err != nil {
		fail(c, h.svc, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}
