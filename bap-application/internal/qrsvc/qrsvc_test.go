package qrsvc

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ion/winroom/bap/internal/clientsvc"
)

// catalogJSON builds a one-provider discover catalog. geo nil = no location.
func catalogJSON(bpp, providerID, providerName string, geo []float64, resources []map[string]any, offers []map[string]any) map[string]any {
	prov := map[string]any{"id": providerID, "descriptor": map[string]any{"name": providerName}}
	if geo != nil {
		prov["availableAt"] = []any{map[string]any{
			"geo":     map[string]any{"type": "Point", "coordinates": geo},
			"address": map[string]any{"streetAddress": "Jl. " + providerName},
		}}
	}
	return map[string]any{"bppId": bpp, "bppUri": "http://" + bpp, "provider": prov, "resources": resources, "offers": offers}
}

func res(id, name string, attrs map[string]any) map[string]any {
	r := map[string]any{"id": id, "descriptor": map[string]any{"name": name}}
	if attrs != nil {
		r["resourceAttributes"] = attrs
	}
	return r
}

func off(id, resourceID string, price, deliveryFee float64) map[string]any {
	ca := map[string]any{"totalAmount": price, "currency": "IDR"}
	if deliveryFee > 0 {
		ca["deliveryFee"] = map[string]any{"amount": deliveryFee}
	}
	return map[string]any{"id": id, "resourceIds": []string{resourceID},
		"considerations": []any{map[string]any{"considerationAttributes": ca}}}
}

func decode(t *testing.T, catalogs ...map[string]any) discoverResponse {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"message": map[string]any{"catalogs": catalogs}})
	var r discoverResponse
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// Node at Kos Melati, Depok.
func testPacket() *Packet {
	return &Packet{
		ID: "k8x2m9pq7r4w", Lat: -6.3625, Lon: 106.8295, RadiusMeters: 3000, Version: 1,
		Items: []PacketItem{
			{ID: "i1", MatchType: "GTIN", MatchValue: "8998866200301", DisplayName: "Indomie Goreng"},
			{ID: "i2", MatchType: "TEXT_QUERY", MatchValue: "aqua 600ml", DisplayName: "Aqua 600ml"},
			{ID: "i3", MatchType: "TEXT_QUERY", MatchValue: "chitato", DisplayName: "Chitato"},
		},
	}
}

func TestMatchRanksNearestCompleteMerchant(t *testing.T) {
	p := testPacket()
	near := []float64{106.8300, -6.3630} // ~80 m
	far := []float64{106.8400, -6.3700}  // ~1.4 km
	tooFar := []float64{106.9500, -6.3625}

	codes := decode(t,
		catalogJSON("bpp1", "warung", "Warung Bu Sri", near,
			[]map[string]any{res("r-indomie", "Indomie Goreng 85g", map[string]any{"gtin": "8998866200301"})},
			[]map[string]any{off("o-indomie", "r-indomie", 3500, 3000)}),
		catalogJSON("bpp1", "toko", "Toko Pak Budi", far,
			[]map[string]any{res("r-indomie2", "Mie Goreng", map[string]any{"identifiers": []any{map[string]any{"value": "8998866200301"}}})},
			[]map[string]any{off("o-indomie2", "r-indomie2", 3000, 5000)}),
		catalogJSON("bpp2", "jauh", "Toko Jauh", tooFar,
			[]map[string]any{res("r-indomie3", "Indomie", map[string]any{"gtin": "8998866200301"})},
			[]map[string]any{off("o-indomie3", "r-indomie3", 1000, 0)}),
	)
	aqua := decode(t,
		catalogJSON("bpp1", "warung", "Warung Bu Sri", near,
			[]map[string]any{res("r-aqua", "Aqua 600ml Botol", nil)},
			[]map[string]any{off("o-aqua", "r-aqua", 4000, 0)}),
		catalogJSON("bpp1", "toko", "Toko Pak Budi", far,
			[]map[string]any{res("r-aqua2", "AQUA 600ML", nil)},
			[]map[string]any{off("o-aqua2", "r-aqua2", 3800, 0)}),
	)
	chitato := decode(t,
		catalogJSON("bpp1", "warung", "Warung Bu Sri", near,
			[]map[string]any{res("r-chitato0", "Chitato Rasa Sapi", nil)},
			[]map[string]any{off("o-chitato0", "r-chitato0", 10000, 0)}),
		catalogJSON("bpp1", "toko", "Toko Pak Budi", far,
			[]map[string]any{res("r-chitato", "Chitato Sapi Panggang", nil)},
			[]map[string]any{off("o-chitato", "r-chitato", 11000, 0)}),
	)

	m := newMatcher(p, true)
	m.add(0, codes)
	m.add(1, aqua)
	m.add(2, chitato)
	sum := m.summarize()

	if len(sum.eligible) != 2 {
		t.Fatalf("want 2 eligible merchants (the far one is out of radius), got %d", len(sum.eligible))
	}
	first, second := sum.eligible[0], sum.eligible[1]
	if first.m.providerID != "warung" || second.m.providerID != "toko" {
		t.Fatalf("want warung (nearest) before toko, got %s, %s", first.m.providerID, second.m.providerID)
	}
	// One of each: the buyer picks quantities at quote.
	if first.subtotal != 3500+4000+10000 {
		t.Errorf("warung subtotal = %v", first.subtotal)
	}
	if first.deliveryFee == nil || *first.deliveryFee != 3000 {
		t.Errorf("warung delivery fee = %v", first.deliveryFee)
	}
	if second.covered != 3 || len(second.lines) != 3 {
		t.Errorf("toko should cover all 3 items, got covered=%d lines=%d", second.covered, len(second.lines))
	}
	if l := first.lines[0].Select; l.BppID != "bpp1" || l.ProviderID != "warung" || l.Quantity != 1 || l.OfferID != "o-indomie" {
		t.Errorf("select item not filled from the catalog: %+v", l)
	}
}

func TestMissingItemAndUnlocated(t *testing.T) {
	p := testPacket()
	p.Items = p.Items[:2]
	aqua := decode(t, catalogJSON("bpp1", "nogeo", "Toko Tanpa Lokasi", nil,
		[]map[string]any{res("r-aqua", "Aqua 600ml", nil), res("r-indomie", "Indomie", map[string]any{"gtin": "8998866200301"})},
		[]map[string]any{off("o-aqua", "r-aqua", 4000, 0), off("o-indomie", "r-indomie", 3500, 0)}))

	strict := newMatcher(p, false)
	strict.add(0, aqua)
	strict.add(1, aqua)
	if sum := strict.summarize(); len(sum.eligible) != 0 {
		t.Fatalf("unlocated merchant must be excluded when not allowed")
	}

	loose := newMatcher(p, true)
	loose.add(0, aqua)
	loose.add(1, aqua)
	sum := loose.summarize()
	if len(sum.eligible) != 1 || sum.eligible[0].m.distance != nil {
		t.Fatalf("want 1 eligible unlocated merchant, got %+v", sum.eligible)
	}

	onlyAqua := newMatcher(p, true)
	onlyAqua.add(1, aqua)
	sum = onlyAqua.summarize()
	if len(sum.eligible) != 0 || len(sum.missing) != 1 || sum.missing[0] != "Indomie Goreng" {
		t.Fatalf("want Indomie Goreng missing, got %+v", sum.missing)
	}
}

func TestChosenLines(t *testing.T) {
	lines := []candidateLine{
		{PacketItemIDs: []string{"i1"}, Name: "Indomie", UnitPrice: 3500, Select: clientsvc.SelectItem{ResourceID: "r1", Quantity: 1}},
		{PacketItemIDs: []string{"i2"}, Name: "Aqua", UnitPrice: 4000, Select: clientsvc.SelectItem{ResourceID: "r2", Quantity: 1}},
	}
	got, err := chosenLines(lines, []QuoteItem{{PacketItemID: "i2", Quantity: 3}}, 10000)
	if err != nil || len(got) != 1 || got[0].Name != "Aqua" || got[0].Select.Quantity != 3 {
		t.Fatalf("chosen = %+v, err %v", got, err)
	}
	if lines[1].Select.Quantity != 1 {
		t.Fatal("the candidate's own lines must not change")
	}
	var ae *apiError
	if _, err := chosenLines(lines, []QuoteItem{{PacketItemID: "i1", Quantity: 2}}, 10000); !errors.As(err, &ae) || ae.code != "BELOW_MIN_BASKET" {
		t.Fatalf("7000 under a 10000 minimum: %v", err)
	}
	if _, err := chosenLines(lines, []QuoteItem{{PacketItemID: "nope", Quantity: 1}}, 0); !errors.As(err, &ae) || ae.code != "INVALID_ITEMS" {
		t.Fatalf("unknown item: %v", err)
	}
}

func TestTextQueryNeedsEveryWordInTheProduct(t *testing.T) {
	item := PacketItem{MatchType: "TEXT_QUERY", MatchValue: "telur ayam"}
	if !resourceMatches(item, resource{Descriptor: descriptor{Name: "Telur Ayam Negeri 1kg"}}) {
		t.Error("should match")
	}
	if resourceMatches(item, resource{Descriptor: descriptor{Name: "Ayam Goreng"}}) {
		t.Error("must not match on one word")
	}
	alts := PacketItem{MatchType: "TEXT_QUERY", MatchValue: "indomie goreng | mie sedaap goreng"}
	if !resourceMatches(alts, resource{Descriptor: descriptor{Name: "Mie Sedaap Goreng 90g"}}) {
		t.Error("any alternative name should match")
	}
	if resourceMatches(alts, resource{Descriptor: descriptor{Name: "Indomie Soto"}}) {
		t.Error("an alternative still needs every one of its words")
	}
	sku := PacketItem{MatchType: "ION_SKU", MatchValue: "res-biscuit-roma-kelapa-002"}
	if !resourceMatches(sku, resource{ID: "res-biscuit-roma-kelapa-002"}) {
		t.Error("ION_SKU should match the resource id")
	}
}

func TestNormalizeIDPhone(t *testing.T) {
	for in, want := range map[string]string{
		"0812 3456 7890":   "+6281234567890",
		"+62 812-3456-789": "+628123456789",
		"6281234567890":    "+6281234567890",
	} {
		if got, ok := normalizeIDPhone(in); !ok || got != want {
			t.Errorf("%q → %q %v, want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "12345", "0212345678", "+1 555 0100", "08abc"} {
		if _, ok := normalizeIDPhone(bad); ok {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestPricingOf(t *testing.T) {
	var contract map[string]any
	_ = json.Unmarshal([]byte(`{"consideration":[{"considerationAttributes":{"currency":"IDR","totalAmount":19580,
		"breakup":[{"title":"Indomie","type":"BASE_PRICE","amount":7000},{"title":"Aqua","type":"BASE_PRICE","amount":8000},
		{"title":"PPN 11%","type":"TAX","amount":1580},{"title":"Delivery fee","type":"DELIVERY_CHARGE","amount":3000}]}}],
		"commitments":[{"status":{"descriptor":{"code":"CLOSED"}},"resources":[{"id":"r-chitato"}]}]}`), &contract)
	p, ok := pricingOf(contract)
	if !ok || p.GrandTotal != 19580 || p.ItemsSubtotal != 15000 || p.Tax != 1580 || p.DeliveryFee != 3000 {
		t.Fatalf("pricing = %+v", p)
	}
	if c := closedResources(contract); !c["r-chitato"] || len(c) != 1 {
		t.Fatalf("closed = %v", c)
	}
}

func TestDemoPricingAndStages(t *testing.T) {
	p := testPacket()
	rc := demoCandidate(p, demoMerchants[0])
	if rc.covered != 3 || len(rc.lines) != 3 || rc.m.distance == nil || *rc.m.distance != 350 {
		t.Fatalf("demo merchant should stock every item at 350 m: %+v", rc)
	}
	again := demoCandidate(p, demoMerchants[0])
	if again.subtotal != rc.subtotal {
		t.Fatal("demo prices must be deterministic")
	}
	fee := 3000.0
	full := demoPricing(rc.lines, &fee)
	if full.Currency != "IDR" || full.ItemsSubtotal != rc.subtotal || full.DeliveryFee != 3000 || full.GrandTotal != rc.subtotal+3000 {
		t.Fatalf("pricing = %+v", full)
	}

	t0 := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	for after, want := range map[time.Duration]string{0: "PENDING", 12 * time.Second: "PACKED", 30 * time.Second: "OUT_FOR_DELIVERY", time.Minute: "DELIVERED"} {
		if got := demoFulfillment(t0, t0.Add(after)); got != want {
			t.Errorf("after %s: %s, want %s", after, got, want)
		}
	}
	if !validDemoScenario(DemoMulti) || validDemoScenario("multi") {
		t.Error("scenario names are exact")
	}
}

func TestProductSuggestions(t *testing.T) {
	if got := displayName("Keripik Singkong Balado (Spicy Cassava Chips)"); got != "Keripik Singkong Balado" {
		t.Errorf("displayName = %q", got)
	}
	if got := nameQuery("Aqua Botol 600ml, Mineral Water (Bottled) Extra Long Name"); got != "aqua botol 600ml mineral water" {
		t.Errorf("nameQuery = %q", got)
	}
	if got := findGTIN([]byte(`{"identity":{"brand":"X","gtin":"8998866200301"}}`)); got != "8998866200301" {
		t.Errorf("findGTIN nested = %q", got)
	}
	if got := findGTIN([]byte(`{"ean13": 8992388101015}`)); got != "8992388101015" {
		t.Errorf("findGTIN number = %q", got)
	}
	if got := findGTIN([]byte(`{"crc":"TRC-arts","weight":{"unitQuantity":150}}`)); got != "" {
		t.Errorf("findGTIN none = %q", got)
	}

	cat := decode(t, catalogJSON("bpp1", "warung", "Warung", []float64{106.8300, -6.3630},
		[]map[string]any{res("r1", "Keripik Singkong Balado (Spicy)", nil)},
		[]map[string]any{off("o1", "r1", 25000, 0), off("o2", "r1", 24000, 0)})).Message.Catalogs[0]
	lat, lon := -6.3625, 106.8295
	pr, ok := productResult(cat, cat.Resources[0], &lat, &lon)
	if !ok || pr.Price != 24000 || pr.DistanceMeters <= 0 || pr.DisplayName != "Keripik Singkong Balado" ||
		pr.SuggestedMatch.MatchType != "TEXT_QUERY" || pr.SuggestedMatch.MatchValue != "keripik singkong balado" {
		t.Fatalf("productResult = %+v", pr)
	}
	// The suggested rule finds the product again.
	item := PacketItem{MatchType: pr.SuggestedMatch.MatchType, MatchValue: pr.SuggestedMatch.MatchValue}
	if !resourceMatches(item, cat.Resources[0]) {
		t.Error("suggested match must match its own product")
	}
}
