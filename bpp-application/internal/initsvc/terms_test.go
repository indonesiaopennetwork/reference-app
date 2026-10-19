package initsvc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/config"
)

// store holds one offer, "offer-1", with a 5% commission and a P2D payout
// after delivery.
type store struct{}

func (store) GetOffer(_ context.Context, arg sqlc.GetOfferParams) (sqlc.Offer, error) {
	if arg.ID != "offer-1" {
		return sqlc.Offer{}, errors.New("no rows")
	}
	return sqlc.Offer{ID: "offer-1", OfferAttributes: json.RawMessage(
		`{"policies": {"settlement": {"settlementBasis": "AFTER_DELIVERY", "settlementWindow": "P2D"}}}`)}, nil
}

func (store) GetOfferConsiderations(_ context.Context, arg sqlc.GetOfferConsiderationsParams) ([]sqlc.OfferConsideration, error) {
	if arg.OfferID != "offer-1" {
		return nil, nil
	}
	return []sqlc.OfferConsideration{{ConsiderationAttributes: json.RawMessage(
		`{"totalAmount": 98900, "commission": {"type": "PERCENT", "value": 5, "appliesTo": "ITEM_TOTAL"}}`)}}, nil
}

var cfg = &config.Config{BppID: "bpp", SettlementBasis: "AFTER_DELIVERY", SettlementWindow: "P1D",
	CommissionType: "PERCENT", CommissionValue: 3, CommissionAppliesTo: "ITEM_TOTAL",
	SettlementBankCode: "BCA", SettlementAccountNumber: "2", SettlementAccountName: "bpp"}

// initContract is an init for one unit of offerID at 89,000 + 9,900 tax, as a
// BAP sends it -- here claiming a 1% commission and the earlier finderFee.
func initContract(offerID string) InitContract {
	var c InitContract
	_ = json.Unmarshal([]byte(`{
	  "commitments": [{"id": "c1", "status": {"descriptor": {"code": "ACTIVE"}},
	    "resources": [{"id": "r1", "quantity": {"unitQuantity": 1}}],
	    "commitmentAttributes": {"price": {"value": 89000}},
	    "offer": {"id": "`+offerID+`", "considerations": [{"id": "p", "considerationAttributes": {"totalAmount": 98900,
	      "commission": {"type": "PERCENT", "value": 1, "appliesTo": "ITEM_TOTAL"}}}]}}],
	  "consideration": [{"id": "price-1", "considerationAttributes": {"totalAmount": 98900,
	    "breakup": [{"type": "BASE_PRICE", "amount": 89000}, {"type": "TAX", "amount": 9900}]}}],
	  "settlements": [{"id": "settlement-1", "settlementAttributes": {
	    "@type": "ion:IONSettlementReconciliation", "status": "PENDING",
	    "finderFee": {"feeType": "PERCENT", "feeValue": 3, "appliesTo": "ITEM_TOTAL"},
	    "settlementAccounts": [{"holderRole": "BAP", "bankCode": "MANDIRI", "accountNumber": "1"}]}}]}`), &c)
	return c
}

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestApplySellerTermsUsesTheStoredOfferTerms(t *testing.T) {
	c := initContract("offer-1")
	for i := 0; i < 2; i++ { // a resent init must not add the account twice
		if err := applySellerTerms(context.Background(), store{}, &c, cfg); err != nil {
			t.Fatal(err)
		}
	}
	offer := decode(t, mustJSON(t, c.Commitments[0]))["offer"].(map[string]any)
	policy := offer["offerAttributes"].(map[string]any)["policies"].(map[string]any)["settlement"].(map[string]any)
	if policy["settlementWindow"] != "P2D" {
		t.Errorf("offer policy = %v, want the stored P2D", policy)
	}
	offerFee := offer["considerations"].([]any)[0].(map[string]any)["considerationAttributes"].(map[string]any)["commission"].(map[string]any)
	if offerFee["value"] != 5.0 {
		t.Errorf("offer commission = %v, want the stored 5%% (not the BAP's 1%%)", offerFee)
	}
	price := decode(t, c.Consideration[0].ConsiderationAttributes)
	want := map[string]any{"type": "PERCENT", "value": 5.0, "appliesTo": "ITEM_TOTAL", "amount": 4450.0}
	if got := price["commission"].(map[string]any); !equal(got, want) {
		t.Errorf("price commission = %v, want %v", got, want)
	}
	attrs := decode(t, c.Settlements[0])["settlementAttributes"].(map[string]any)
	for _, k := range []string{"finderFee", "settlementBasis", "settlementWindow"} {
		if _, ok := attrs[k]; ok {
			t.Errorf("settlement still carries %s", k)
		}
	}
	if net := attrs["netPayable"].(map[string]any)["value"]; net != 94450.0 {
		t.Errorf("netPayable = %v, want 98,900 - 4,450", net)
	}
	accounts := asSlice(attrs["settlementAccounts"])
	if len(accounts) != 2 || accounts[1].(map[string]any)["holderRole"] != "BPP" || accounts[1].(map[string]any)["accountNumber"] != "2" {
		t.Errorf("settlementAccounts = %v", accounts)
	}
}

func TestAnOfferWithoutStoredTermsGetsTheDefaults(t *testing.T) {
	c := initContract("offer-gone")
	if err := applySellerTerms(context.Background(), store{}, &c, cfg); err != nil {
		t.Fatal(err)
	}
	price := decode(t, c.Consideration[0].ConsiderationAttributes)
	if got := price["commission"].(map[string]any)["amount"]; got != 2670.0 {
		t.Errorf("commission = %v, want the default 3%% of 89,000", got)
	}
}

func TestApplySellerTermsConvertsAnIONPayment(t *testing.T) {
	c := InitContract{
		ContractAttributes: json.RawMessage(`{"finderFee": {"feeType": "AMOUNT", "feeValue": 1000}}`),
		Consideration:      []Consideration{{ID: "price-1", ConsiderationAttributes: json.RawMessage(`{"totalAmount": 5000}`)}},
		Settlements:        []json.RawMessage{json.RawMessage(`{"id": "s", "settlementAttributes": {"@type": "ion:IONPayment", "settlementAccounts": []}}`)},
	}
	if err := applySellerTerms(context.Background(), store{}, &c, cfg); err != nil {
		t.Fatal(err)
	}
	attrs := decode(t, c.Settlements[0])["settlementAttributes"].(map[string]any)
	if attrs["@type"] != "ion:IONSettlementReconciliation" || attrs["commission"].(map[string]any)["value"] != 1000.0 {
		t.Errorf("settlement = %v", attrs)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func equal(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
