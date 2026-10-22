package confirmsvc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/config"
)

// noOffers stores no offers, so the defaults apply.
type noOffers struct{}

func (noOffers) GetOffer(context.Context, sqlc.GetOfferParams) (sqlc.Offer, error) {
	return sqlc.Offer{}, context.Canceled
}
func (noOffers) GetOfferConsiderations(context.Context, sqlc.GetOfferConsiderationsParams) ([]sqlc.OfferConsideration, error) {
	return nil, nil
}

var cfg = &config.Config{BppID: "bpp", SettlementBasis: "AFTER_DELIVERY", SettlementWindow: "P1D",
	CommissionType: "PERCENT", CommissionValue: 3, CommissionAppliesTo: "ITEM_TOTAL"}

func settlementAttrs(t *testing.T, c ConfirmContract) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(c.Settlements[0].SettlementAttributes, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestALegacyOrderKeepsTheTermsItAgreed(t *testing.T) {
	c := ConfirmContract{Settlements: []Settlement{{ID: "s", SettlementAttributes: json.RawMessage(
		`{"@type": "ion:IONSettlementReconciliation", "settlementBasis": "ON_CONFIRMATION", "settlementWindow": "PT1H",
		  "finderFee": {"feeType": "PERCENT", "feeValue": 2}}`)}}}
	if err := withSettlementTerms(context.Background(), noOffers{}, &c, cfg); err != nil {
		t.Fatal(err)
	}
	if got := settlementAttrs(t, c); got["settlementBasis"] != "ON_CONFIRMATION" || got["settlementWindow"] != "PT1H" || got["finderFee"] == nil {
		t.Errorf("agreed terms changed: %v", got)
	}
}

func TestALegacyOrderGetsMissingTerms(t *testing.T) {
	c := ConfirmContract{Settlements: []Settlement{{ID: "s", SettlementAttributes: json.RawMessage(
		`{"@type": "ion:IONPayment", "status": "PAID", "amount": 100, "finderFee": {"feeType": "AMOUNT", "feeValue": 1}}`)}}}
	if err := withSettlementTerms(context.Background(), noOffers{}, &c, cfg); err != nil {
		t.Fatal(err)
	}
	got := settlementAttrs(t, c)
	if got["settlementBasis"] != "AFTER_DELIVERY" || got["settlementWindow"] != "P1D" || got["@type"] != "ion:IONSettlementReconciliation" {
		t.Errorf("missing terms not filled in: %v", got)
	}
}

func TestConfirmRestatesTheSellersCommission(t *testing.T) {
	var c ConfirmContract
	_ = json.Unmarshal([]byte(`{
	  "commitments": [{"id": "c1", "status": {"descriptor": {"code": "ACTIVE"}},
	    "resources": [{"id": "r1", "quantity": {"unitQuantity": 2}}],
	    "commitmentAttributes": {"price": {"value": 10000}},
	    "offer": {"id": "o1", "considerations": [{"id": "p", "considerationAttributes": {"totalAmount": 11000,
	      "commission": {"type": "PERCENT", "value": 50}}}]}}],
	  "consideration": [{"id": "price-1", "considerationAttributes": {"totalAmount": 22000,
	    "breakup": [{"type": "BASE_PRICE", "amount": 20000}, {"type": "TAX", "amount": 2000}],
	    "commission": {"type": "PERCENT", "value": 50, "amount": 10000}}}],
	  "settlements": [{"id": "s", "considerationId": "price-1", "settlementAttributes": {
	    "@type": "ion:IONSettlementReconciliation", "status": "PAID", "paidAt": "2026-10-19T00:00:00Z",
	    "orderAmount": {"value": 22000}, "settlementAccounts": []}}]}`), &c)
	if err := withSettlementTerms(context.Background(), noOffers{}, &c, cfg); err != nil {
		t.Fatal(err)
	}
	var price map[string]any
	_ = json.Unmarshal(c.Consideration[0].ConsiderationAttributes, &price)
	if got := price["buyerFinderFee"].(map[string]any)["feeAmount"]; got != 600.0 {
		t.Errorf("commission = %v, want the seller's 3%% of 20,000, not the buyer app's 50%%", got)
	}
	if got := settlementAttrs(t, c)["netPayable"].(map[string]any)["value"]; got != 21400.0 {
		t.Errorf("netPayable = %v, want 22,000 - 600", got)
	}
}
