package qrsvc

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/ion/winroom/bap/internal/clientsvc"
)

// Discover response, decoded only as far as matching needs. Resources and
// offers keep their raw JSON too, since select sends them on unchanged.

type discoverResponse struct {
	Message struct {
		Catalogs []catalog `json:"catalogs"`
	} `json:"message"`
}

type catalog struct {
	BppID      string     `json:"bppId"`
	BppURI     string     `json:"bppUri"`
	Descriptor descriptor `json:"descriptor"`
	Provider   *provider  `json:"provider"`
	Resources  []resource `json:"resources"`
	Offers     []offer    `json:"offers"`
}

type descriptor struct {
	Name      string `json:"name"`
	Code      string `json:"code"`
	ShortDesc string `json:"shortDesc"`
	LongDesc  string `json:"longDesc"`
	MediaFile []struct {
		URI string `json:"uri"`
	} `json:"mediaFile"`
}

type provider struct {
	ID          string     `json:"id"`
	Descriptor  descriptor `json:"descriptor"`
	AvailableAt []location `json:"availableAt"`
}

type location struct {
	Geo struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geo"`
	Address map[string]any `json:"address"`
}

type resource struct {
	ID                 string          `json:"id"`
	Descriptor         descriptor      `json:"descriptor"`
	RawDescriptor      json.RawMessage `json:"-"`
	ResourceAttributes json.RawMessage `json:"resourceAttributes"`
}

func (r *resource) UnmarshalJSON(b []byte) error {
	type plain resource
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var raw struct {
		Descriptor json.RawMessage `json:"descriptor"`
	}
	_ = json.Unmarshal(b, &raw)
	*r = resource(p)
	r.RawDescriptor = raw.Descriptor
	return nil
}

type offer struct {
	ID              string          `json:"id"`
	Descriptor      descriptor      `json:"descriptor"`
	Provider        *provider       `json:"provider"`
	ResourceIDs     []string        `json:"resourceIds"`
	Considerations  json.RawMessage `json:"considerations"`
	OfferAttributes json.RawMessage `json:"offerAttributes"`
}

// price is an offer's unit price, currency and per-order delivery fee:
// considerations[0].considerationAttributes first, then
// offerAttributes.price (as bap-frontend's parseCatalogs reads it).
func (o offer) price() (unit float64, currency string, deliveryFee *float64, ok bool) {
	var cons []struct {
		Attrs struct {
			TotalAmount *float64 `json:"totalAmount"`
			Currency    string   `json:"currency"`
			DeliveryFee *struct {
				Amount *float64 `json:"amount"`
			} `json:"deliveryFee"`
		} `json:"considerationAttributes"`
	}
	if json.Unmarshal(o.Considerations, &cons) == nil && len(cons) > 0 && cons[0].Attrs.TotalAmount != nil {
		a := cons[0].Attrs
		if a.DeliveryFee != nil {
			deliveryFee = a.DeliveryFee.Amount
		}
		return *a.TotalAmount, a.Currency, deliveryFee, true
	}
	var oa struct {
		Price struct {
			Value    *float64 `json:"value"`
			Currency string   `json:"currency"`
		} `json:"price"`
	}
	if json.Unmarshal(o.OfferAttributes, &oa) == nil && oa.Price.Value != nil {
		return *oa.Price.Value, oa.Price.Currency, nil, true
	}
	return 0, "", nil, false
}

// merchant is one provider found in discover results, with the packet
// items it can supply.
type merchant struct {
	key          string
	bppID        string
	bppURI       string
	providerID   string
	providerName string
	distance     *float64
	address      string
	// lines[i] is the best offer for packet item i; nil when not stocked.
	lines []*candidateLine
}

// matcher collects, per merchant, the cheapest priced offer for each packet item.
type matcher struct {
	packet         *Packet
	allowUnlocated bool
	merchants      map[string]*merchant
}

func newMatcher(p *Packet, allowUnlocated bool) *matcher {
	return &matcher{packet: p, allowUnlocated: allowUnlocated, merchants: map[string]*merchant{}}
}

// add matches packet item idx against one discover response.
func (m *matcher) add(idx int, resp discoverResponse) {
	item := m.packet.Items[idx]
	for _, cat := range resp.Message.Catalogs {
		for _, res := range cat.Resources {
			if res.ID == "" || !resourceMatches(item, res) {
				continue
			}
			m.offerFor(idx, item, cat, res)
		}
		// ION_SKU may name the offer rather than the resource.
		if item.MatchType == "ION_SKU" {
			for _, o := range cat.Offers {
				if o.ID != item.MatchValue {
					continue
				}
				for _, rid := range o.ResourceIDs {
					for _, res := range cat.Resources {
						if res.ID == rid {
							m.offerFor(idx, item, cat, res)
						}
					}
				}
			}
		}
	}
}

// offerFor records the cheapest priced offer covering res as merchant's line
// for packet item idx.
func (m *matcher) offerFor(idx int, item PacketItem, cat catalog, res resource) {
	for _, o := range cat.Offers {
		if !contains(o.ResourceIDs, res.ID) {
			continue
		}
		unit, currency, fee, ok := o.price()
		if !ok {
			continue
		}
		if currency == "" {
			currency = "IDR"
		}
		if !strings.EqualFold(currency, currencyIDR) {
			continue
		}
		prov := o.Provider
		if prov == nil || prov.ID == "" {
			prov = cat.Provider
		}
		if prov == nil || prov.ID == "" {
			continue
		}
		mc := m.merchant(cat, prov)
		if mc == nil {
			continue
		}
		if cur := mc.lines[idx]; cur != nil && cur.UnitPrice <= unit {
			continue
		}
		image := ""
		if len(res.Descriptor.MediaFile) > 0 {
			image = res.Descriptor.MediaFile[0].URI
		}
		resourceDetail, _ := json.Marshal(map[string]json.RawMessage{
			"descriptor":         res.RawDescriptor,
			"resourceAttributes": res.ResourceAttributes,
		})
		offerName := o.Descriptor.Name
		if offerName == "" {
			offerName = res.Descriptor.Name
		}
		mc.lines[idx] = &candidateLine{
			PacketItemIDs: []string{item.ID},
			Name:          item.DisplayName,
			Image:         image,
			UnitPrice:     unit,
			DeliveryFee:   fee,
			Select: clientsvc.SelectItem{
				ResourceID:          res.ID,
				OfferID:             o.ID,
				OfferName:           offerName,
				Quantity:            1, // a price list: the buyer picks quantities
				ProviderID:          prov.ID,
				ProviderName:        prov.Descriptor.Name,
				OfferAttributes:     o.OfferAttributes,
				OfferConsiderations: o.Considerations,
				Resource:            resourceDetail,
				BppID:               cat.BppID,
				BppURI:              cat.BppURI,
			},
		}
	}
}

// merchant returns (creating) the merchant for prov, or nil when it is out
// of the packet's radius (or has no location and those aren't allowed).
func (m *matcher) merchant(cat catalog, prov *provider) *merchant {
	key := cat.BppID + "|" + prov.ID
	if mc, ok := m.merchants[key]; ok {
		return mc
	}
	dist, addr, located := nearest(prov.AvailableAt, m.packet.Lat, m.packet.Lon)
	if located && dist > float64(m.packet.RadiusMeters) {
		return nil
	}
	if !located && !m.allowUnlocated {
		return nil
	}
	mc := &merchant{
		key: key, bppID: cat.BppID, bppURI: cat.BppURI,
		providerID: prov.ID, providerName: prov.Descriptor.Name,
		address: addr, lines: make([]*candidateLine, len(m.packet.Items)),
	}
	if mc.providerName == "" {
		mc.providerName = prov.ID
	}
	if located {
		d := dist
		mc.distance = &d
	}
	m.merchants[key] = mc
	return mc
}

// rankedCandidate is a merchant that stocks every packet item.
type rankedCandidate struct {
	m           *merchant
	lines       []candidateLine
	covered     int
	subtotal    float64
	deliveryFee *float64
}

// summary is the outcome of matching.
type summary struct {
	eligible []rankedCandidate
	// The items no single merchant can supply along with the rest.
	missing []string
}

// summarize keeps the merchants stocking every packet item and ranks them:
// located before unlocated, nearest first, then cheapest.
func (m *matcher) summarize() summary {
	var out summary
	stockedAnywhere := make([]bool, len(m.packet.Items))
	for _, mc := range m.merchants {
		rc := rankedCandidate{m: mc}
		complete := true
		byResource := map[string]int{}
		for i := range m.packet.Items {
			line := mc.lines[i]
			if line == nil {
				complete = false
				continue
			}
			stockedAnywhere[i] = true
			rc.covered++
			rc.subtotal += line.UnitPrice * float64(line.Select.Quantity)
			if line.DeliveryFee != nil && (rc.deliveryFee == nil || *line.DeliveryFee > *rc.deliveryFee) {
				f := *line.DeliveryFee
				rc.deliveryFee = &f
			}
			// Two packet items served by the same resource become one line
			// (select keys its commitments by resource id).
			if j, ok := byResource[line.Select.ResourceID]; ok {
				merged := &rc.lines[j]
				merged.Select.Quantity += line.Select.Quantity
				merged.PacketItemIDs = append(merged.PacketItemIDs, line.PacketItemIDs...)
				merged.Name += " + " + line.Name
				continue
			}
			byResource[line.Select.ResourceID] = len(rc.lines)
			rc.lines = append(rc.lines, *line)
		}
		if !complete {
			continue
		}
		// The minimum basket is checked at quote, on what the buyer chose.
		rc.subtotal = round2(rc.subtotal)
		out.eligible = append(out.eligible, rc)
	}
	for i, it := range m.packet.Items {
		if !stockedAnywhere[i] {
			out.missing = append(out.missing, it.DisplayName)
		}
	}
	if len(out.eligible) == 0 && len(out.missing) == 0 {
		// Every item is stocked somewhere, but no single merchant has all.
		for _, it := range m.packet.Items {
			out.missing = append(out.missing, it.DisplayName)
		}
	}
	sort.SliceStable(out.eligible, func(a, b int) bool {
		ca, cb := out.eligible[a], out.eligible[b]
		da, db := ca.m.distance, cb.m.distance
		if (da == nil) != (db == nil) {
			return da != nil
		}
		if da != nil && math.Abs(*da-*db) >= 1 {
			return *da < *db
		}
		if ca.subtotal != cb.subtotal {
			return ca.subtotal < cb.subtotal
		}
		return ca.m.key < cb.m.key
	})
	return out
}

// resourceMatches decides whether res is the packet item.
//   - TEXT_QUERY: every word of the query appears in the resource's name or
//     descriptions (discover also matches the provider name; a packet item
//     must name the product itself).
//   - GTIN: the resource id or descriptor code is the GTIN, or any string
//     value in resourceAttributes equals it (e.g. gtin, barcode, ean).
//   - ION_SKU: the resource id or descriptor code is the SKU.
func resourceMatches(item PacketItem, res resource) bool {
	v := strings.TrimSpace(item.MatchValue)
	switch item.MatchType {
	case "TEXT_QUERY":
		hay := strings.ToLower(res.Descriptor.Name + " " + res.Descriptor.ShortDesc + " " + res.Descriptor.LongDesc)
		terms := strings.Fields(strings.ToLower(v))
		if len(terms) == 0 {
			return false
		}
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				return false
			}
		}
		return true
	case "GTIN":
		if res.ID == v || res.Descriptor.Code == v {
			return true
		}
		var attrs any
		if json.Unmarshal(res.ResourceAttributes, &attrs) != nil {
			return false
		}
		return containsString(attrs, v)
	case "ION_SKU":
		return res.ID == v || res.Descriptor.Code == v
	}
	return false
}

func containsString(v any, want string) bool {
	switch t := v.(type) {
	case string:
		return t == want
	case map[string]any:
		for _, x := range t {
			if containsString(x, want) {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if containsString(x, want) {
				return true
			}
		}
	}
	return false
}

// nearest is the distance in meters from (lat, lon) to the nearest Point
// location, with that location's address; located is false without one.
func nearest(locs []location, lat, lon float64) (dist float64, address string, located bool) {
	dist = math.Inf(1)
	for _, loc := range locs {
		if !strings.EqualFold(loc.Geo.Type, "Point") {
			continue
		}
		var c []float64
		if json.Unmarshal(loc.Geo.Coordinates, &c) != nil || len(c) < 2 {
			continue
		}
		if d := haversineMeters(lat, lon, c[1], c[0]); d < dist {
			dist, address, located = d, formatAddress(loc.Address), true
		}
	}
	if !located && len(locs) > 0 {
		address = formatAddress(locs[0].Address)
	}
	return dist, address, located
}

func formatAddress(a map[string]any) string {
	var parts []string
	for _, k := range []string{"streetAddress", "extendedAddress", "addressLocality", "addressRegion"} {
		if s, _ := a[k].(string); strings.TrimSpace(s) != "" {
			parts = append(parts, strings.TrimSpace(s))
		}
	}
	return strings.Join(parts, ", ")
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLon := rad(lat2-lat1), rad(lon2-lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }
