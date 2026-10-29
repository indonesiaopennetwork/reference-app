package settlement

import (
	"reflect"
	"testing"
)

// offerOrder is an order of two offers with their own buyer finder fee (on
// the offer's consideration) and payout terms: 2 x keripik at 25000 (+ 2750
// tax) and 3 x teh at 5000 (+ 550 tax).
func offerOrder(t *testing.T, kerFee, tehFee, tehPolicy string) map[string]any {
	t.Helper()
	return contract(t, `{
  "commitments": [
    {"id": "c-ker", "status": {"descriptor": {"code": "ACTIVE"}},
     "resources": [{"id": "ker", "quantity": {"unitQuantity": 2}}],
     "commitmentAttributes": {"price": {"value": 25000}},
     "offer": {"id": "offer-ker",
       "considerations": [{"id": "p", "considerationAttributes": {"totalAmount": 27750, "buyerFinderFee": `+kerFee+`,
         "breakup": [{"type": "BASE_PRICE", "amount": 25000}, {"type": "TAX", "amount": 2750}]}}],
       "offerAttributes": {"policies": {
         "settlement": {"settlementBasis": "AFTER_DELIVERY", "settlementWindow": "P1D"}}}}},
    {"id": "c-teh", "status": {"descriptor": {"code": "ACTIVE"}},
     "resources": [{"id": "teh", "quantity": {"unitQuantity": 3}}],
     "commitmentAttributes": {"price": {"value": 5000}},
     "offer": {"id": "offer-teh",
       "considerations": [{"id": "p", "considerationAttributes": {"totalAmount": 5550, "buyerFinderFee": `+tehFee+`,
         "breakup": [{"type": "BASE_PRICE", "amount": 5000}, {"type": "TAX", "amount": 550}]}}],
       "offerAttributes": {"policies": {"settlement": `+tehPolicy+`}}}}],
  "consideration": [{"id": "price-1", "status": {"code": "QUOTED"}, "considerationAttributes": {"totalAmount": 72150,
    "breakup": [{"type": "BASE_PRICE", "amount": 50000}, {"type": "TAX", "amount": 5500},
                {"type": "BASE_PRICE", "amount": 15000}, {"type": "TAX", "amount": 1650}]}}],
  "settlements": [{"id": "settlement-1", "settlementAttributes": {
    "@context": "https://schema.ion.id/release1/schema/common/Settlement/v1/context.jsonld",
    "@type": "ion:IONSettlementReconciliation", "status": "PENDING", "currency": "IDR",
    "settlementAccounts": [{"holderRole": "BAP"}]}}]}`)
}

const (
	pct3   = `{"feeType": "PERCENT", "feeValue": 3, "appliesTo": "ITEM_TOTAL"}`
	pct3OT = `{"feeType": "PERCENT", "feeValue": 3, "appliesTo": "ORDER_TOTAL"}`
	amt200 = `{"feeType": "AMOUNT", "feeValue": 200}`
	pol1D  = `{"settlementBasis": "AFTER_DELIVERY", "settlementWindow": "P1D"}`
	pol3D  = `{"settlementBasis": "AFTER_RETURN_WINDOW", "settlementWindow": "P3D"}`
)

func TestTheContractStatesTheSumOfTheOffersFees(t *testing.T) {
	c := offerOrder(t, pct3, pct3, pol1D)
	got, ok := OfferCommissionOf(c)
	want := map[string]any{"feeAmount": 1950.0} // 3% of 50000 + 15000
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("commission = %v, %v; want %v", got, ok, want)
	}
}

func TestEachOfferFeeIsOnItsOwnItems(t *testing.T) {
	c := offerOrder(t, pct3OT, amt200, pol1D)
	got, _ := OfferCommissionOf(c)
	// 3% of 2 x 27750 = 1665, plus 200 per unit x 3 = 600.
	want := map[string]any{"feeAmount": 2265.0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commission = %v, want %v", got, want)
	}
}

func TestRefreshPutsTheCommissionOnThePriceAndDropsLegacyTerms(t *testing.T) {
	c := offerOrder(t, pct3, pct3, pol1D)
	_, attrs := Of(c)
	attrs["finderFee"] = map[string]any{"feeType": "PERCENT", "feeValue": 10.0}
	attrs["settlementBasis"] = "ON_CONFIRMATION"
	a := Refresh(c)
	if a.Commission != 1950 || a.Net != 72150-1950 {
		t.Errorf("figures = %+v", a)
	}
	if got, ok := StatedCommission(c); !ok || got != 1950 {
		t.Errorf("stated commission = %v, %v", got, ok)
	}
	for _, k := range termFields {
		if _, ok := attrs[k]; ok {
			t.Errorf("settlement object still carries %s", k)
		}
	}
	if value(attrs, "commission") != 1950.0 {
		t.Errorf("settlement commission = %v", value(attrs, "commission"))
	}
}

func TestCommissionFollowsAPartialCancellation(t *testing.T) {
	c := offerOrder(t, pct3, amt200, pol1D)
	Refresh(c)
	// The teh line is cancelled: its commitment closes, the price is replaced.
	cms := slice(c["commitments"])
	asMap(cms[1])["status"] = map[string]any{"descriptor": map[string]any{"code": "CLOSED"}}
	cons := slice(c["consideration"])
	asMap(cons[0])["status"] = map[string]any{"code": "CANCELLED"}
	c["consideration"] = append(cons, map[string]any{"id": "price-1-a1", "status": map[string]any{"code": "ACTIVE"},
		"considerationAttributes": map[string]any{"totalAmount": 55500.0,
			"breakup": []any{map[string]any{"type": "BASE_PRICE", "amount": 50000.0}, map[string]any{"type": "TAX", "amount": 5500.0}}}})
	a := Refresh(c)
	if a.Commission != 1500 { // 3% of 50000; the teh commission went with its line
		t.Errorf("commission = %v, want 1500", a.Commission)
	}
	replacement := asMap(asMap(slice(c["consideration"])[1])["considerationAttributes"])["buyerFinderFee"]
	want := map[string]any{"feeAmount": 1500.0}
	if !reflect.DeepEqual(replacement, want) {
		t.Errorf("replacement commission = %v, want %v", replacement, want)
	}
}

func TestComputeKeepsTheStatedCommission(t *testing.T) {
	c := offerOrder(t, pct3, pct3, pol1D)
	Refresh(c)
	price := asMap(asMap(slice(c["consideration"])[0])["considerationAttributes"])
	price["buyerFinderFee"].(map[string]any)["feeAmount"] = 1900.0
	if a := Compute(c, false); a.Commission != 1900 {
		t.Errorf("commission = %v, want the stated 1900", a.Commission)
	}
	// A total stated the earlier way (commission, amount) still counts.
	delete(price, "buyerFinderFee")
	price["commission"] = map[string]any{"type": "AMOUNT", "value": 1800.0, "amount": 1800.0}
	if a := Compute(c, false); a.Commission != 1800 {
		t.Errorf("commission = %v, want the stated 1800 (earlier field names)", a.Commission)
	}
	if a := Compute(c, true); a.Commission != 1950 {
		t.Errorf("recomputed commission = %v, want 1950", a.Commission)
	}
}

func TestPayoutTermsAreTheOffersOrTheLegacyOne(t *testing.T) {
	c := offerOrder(t, pct3, pct3, pol3D)
	want := []Term{{"AFTER_DELIVERY", "P1D"}, {"AFTER_RETURN_WINDOW", "P3D"}}
	if got := PayoutTerms(c); !reflect.DeepEqual(got, want) {
		t.Errorf("terms = %v, want %v", got, want)
	}
	if got := PayoutTerms(offerOrder(t, pct3, pct3, pol1D)); len(got) != 1 {
		t.Errorf("identical terms should appear once: %v", got)
	}
	legacy := contract(t, declared)
	_, attrs := Of(legacy)
	attrs["settlementBasis"], attrs["settlementWindow"] = "AFTER_DELIVERY", "P2D"
	if got := PayoutTerms(legacy); !reflect.DeepEqual(got, []Term{{"AFTER_DELIVERY", "P2D"}}) {
		t.Errorf("legacy terms = %v", got)
	}
}

func TestALegacyOrderKeepsItsFinderFee(t *testing.T) {
	c := contract(t, declared)
	if _, ok := OfferCommissionOf(c); ok {
		t.Fatal("an order without offer commission should not get one")
	}
	a := Refresh(c)
	if a.Commission != 2670 { // 3% of 89000, from finderFee
		t.Errorf("commission = %v, want 2670", a.Commission)
	}
	_, attrs := Of(c)
	if asMap(attrs["finderFee"])["feeAmount"] != 2670.0 {
		t.Errorf("finderFee = %v", attrs["finderFee"])
	}
}

func TestEarlierPolicyEntriesStillAddUp(t *testing.T) {
	// Keripik stored the earlier way: a 3% finder fee and a 200-per-unit
	// platform fee in policies.commission; teh: 3% on its consideration.
	c := offerOrder(t, pct3, pct3, pol1D)
	ker := asMap(asMap(slice(c["commitments"])[0])["offer"])
	delete(asMap(asMap(slice(ker["considerations"])[0])["considerationAttributes"]), "buyerFinderFee")
	asMap(asMap(ker["offerAttributes"])["policies"])["commission"] = []any{
		map[string]any{"feeName": "Finder fee", "feeType": "PERCENT", "feeValue": 3.0, "appliesTo": "ITEM_TOTAL"},
		map[string]any{"name": "Platform fee", "type": "AMOUNT", "value": 200.0},
	}
	got, _ := OfferCommissionOf(c)
	// 3% of 50000 + 200 x 2 + 3% of 15000 = 1500 + 400 + 450.
	want := map[string]any{"feeAmount": 2350.0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commission = %v, want %v", got, want)
	}
}

func TestACommissionOnTheOfferPriceStillCounts(t *testing.T) {
	// An offer published while the commission sat on its price consideration.
	offer := map[string]any{"considerations": []any{map[string]any{"considerationAttributes": map[string]any{
		"commission": map[string]any{"type": "PERCENT", "value": 4.0}}}}}
	if got := OfferCommission(offer); len(got) != 1 || got[0]["feeValue"] != 4.0 {
		t.Errorf("commission = %v, want the price consideration's", got)
	}
	offer["offerAttributes"] = map[string]any{"policies": map[string]any{"commission": []any{
		map[string]any{"feeName": "Finder fee", "feeType": "PERCENT", "feeValue": 5.0}}}}
	if got := OfferCommission(offer); len(got) != 1 || got[0]["feeValue"] != 5.0 {
		t.Errorf("commission = %v, want the policy's", got)
	}
	// The consideration's buyerFinderFee wins over both earlier forms.
	asMap(asMap(slice(offer["considerations"])[0])["considerationAttributes"])["buyerFinderFee"] = map[string]any{"feeType": "AMOUNT", "feeValue": 6.0}
	if got := OfferCommission(offer); len(got) != 1 || got[0]["feeValue"] != 6.0 {
		t.Errorf("fee = %v, want the consideration's buyerFinderFee", got)
	}
}
