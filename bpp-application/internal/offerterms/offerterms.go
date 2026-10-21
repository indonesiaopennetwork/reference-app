// Package offerterms puts the seller's settlement terms on its offers (ION
// Release 1; see package settlement):
//
//   - the commission the seller pays the buyer app,
//     offerAttributes.policies.commission (IONTradeOffer): named entries
//     that add up (feeName), each feeType PERCENT or AMOUNT (per unit),
//     feeValue, and for PERCENT appliesTo ITEM_TOTAL | ORDER_TOTAL;
//   - the payout term, offerAttributes.policies.settlement (IONTradeOffer) --
//     settlementBasis and settlementWindow.
//
// An offer published without them gets this BPP's defaults (Defaults). The
// offer's considerations carry no commission (StripCommission); only the
// contract's price does (see package settlement). At select, init and
// confirm the order's offers get the terms stored for them here (Apply), so
// what the buyer app sends back cannot change them.
package offerterms

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/settlement"
)

// Defaults are the terms an offer without its own gets.
type Defaults struct {
	CommissionType      string
	CommissionValue     float64
	CommissionAppliesTo string
	Basis, Window       string
}

// FromConfig is this BPP's defaults (COMMISSION_*, SETTLEMENT_BASIS,
// SETTLEMENT_WINDOW).
func FromConfig(cfg *config.Config) Defaults {
	return Defaults{
		CommissionType: cfg.CommissionType, CommissionValue: cfg.CommissionValue,
		CommissionAppliesTo: cfg.CommissionAppliesTo,
		Basis:               cfg.SettlementBasis, Window: cfg.SettlementWindow,
	}
}

// DefaultCommissionName names the default commission entry.
const DefaultCommissionName = "Finder fee"

// Commission is d's policies.commission: one entry, the finder fee.
func (d Defaults) Commission() []any {
	c := map[string]any{"feeName": DefaultCommissionName, "feeType": d.CommissionType, "feeValue": d.CommissionValue}
	if d.CommissionType == "PERCENT" {
		c["appliesTo"] = d.CommissionAppliesTo
	}
	return []any{c}
}

// Policy is d's policies.settlement.
func (d Defaults) Policy() map[string]any {
	return map[string]any{"settlementBasis": d.Basis, "settlementWindow": d.Window}
}

// FillAttributes gives offer attributes without policies.settlement or
// policies.commission d's, and renames commission entries stored under the
// earlier field names. An empty or unreadable raw is returned unchanged.
func FillAttributes(raw json.RawMessage, d Defaults) json.RawMessage {
	var attrs map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &attrs) != nil || attrs == nil {
		return raw
	}
	if !fillPolicy(attrs, d) {
		return raw
	}
	out, err := json.Marshal(attrs)
	if err != nil {
		return raw
	}
	return out
}

// StripCommission removes a commission from an offer consideration's
// attributes: the offer states it in policies.commission.
func StripCommission(raw json.RawMessage) json.RawMessage {
	var attrs map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &attrs) != nil || attrs == nil {
		return raw
	}
	if _, ok := attrs["commission"]; !ok {
		return raw
	}
	delete(attrs, "commission")
	out, err := json.Marshal(attrs)
	if err != nil {
		return raw
	}
	return out
}

func fillPolicy(attrs map[string]any, d Defaults) bool {
	policies, _ := attrs["policies"].(map[string]any)
	if policies == nil {
		policies = map[string]any{}
		attrs["policies"] = policies
	}
	changed := false
	if c, _ := policies["commission"].([]any); len(c) == 0 {
		policies["commission"], changed = d.Commission(), true
	} else if renamed := entries(c); !reflect.DeepEqual(renamed, c) {
		policies["commission"], changed = renamed, true // stored under the earlier field names
	}
	if p, ok := policies["settlement"].(map[string]any); ok && (p["settlementBasis"] != nil || p["settlementWindow"] != nil) {
		if p["settlementBasis"] == nil {
			p["settlementBasis"], changed = d.Basis, true
		}
		if p["settlementWindow"] == nil {
			p["settlementWindow"], changed = d.Window, true
		}
		return changed
	}
	policies["settlement"] = d.Policy()
	return true
}

// Store is where Apply finds an offer's stored attributes and considerations.
type Store interface {
	GetOffer(ctx context.Context, arg sqlc.GetOfferParams) (sqlc.Offer, error)
	GetOfferConsiderations(ctx context.Context, arg sqlc.GetOfferConsiderationsParams) ([]sqlc.OfferConsideration, error)
}

// Apply sets the terms of each commitment's offer in contract: those stored
// for the offer in this BPP, with d where the offer has none (an offer
// published before the terms, or no longer stored). An offer stored with its
// commission on its price consideration (published before the commission
// moved to policies.commission) gives that as its one entry.
func Apply(ctx context.Context, store Store, bppID string, contract map[string]any, d Defaults) {
	for _, raw := range slice(contract["commitments"]) {
		offer := asMap(asMap(raw)["offer"])
		if offer == nil {
			continue
		}
		policy, commission := d.Policy(), d.Commission()
		if id, _ := offer["id"].(string); id != "" {
			stored := false
			if row, err := store.GetOffer(ctx, sqlc.GetOfferParams{ID: id, BppID: bppID}); err == nil {
				var attrs map[string]any
				if json.Unmarshal(row.OfferAttributes, &attrs) == nil {
					if c, _ := asMap(attrs["policies"])["commission"].([]any); len(c) > 0 {
						commission, stored = entries(c), true
					}
				}
				if json.Unmarshal(FillAttributes(row.OfferAttributes, d), &attrs) == nil {
					if p := asMap(asMap(attrs["policies"])["settlement"]); p != nil {
						policy = p
					}
				}
			}
			if !stored {
				if c := priceCommission(ctx, store, id, bppID); c != nil {
					c = settlement.CommissionEntry(c)
					c["feeName"] = DefaultCommissionName
					commission = []any{c}
				}
			}
		}
		oa := asMap(offer["offerAttributes"])
		if oa == nil {
			oa = map[string]any{}
			offer["offerAttributes"] = oa
		}
		policies := asMap(oa["policies"])
		if policies == nil {
			policies = map[string]any{}
			oa["policies"] = policies
		}
		policies["settlement"] = policy
		policies["commission"] = commission
		for _, c := range slice(offer["considerations"]) {
			delete(asMap(asMap(c)["considerationAttributes"]), "commission")
		}
	}
}

// entries is a stored policies.commission under ION's field names: an entry
// stored under the earlier ones (name, type, value) is renamed.
func entries(stored []any) []any {
	out := make([]any, 0, len(stored))
	for _, e := range stored {
		if m := asMap(e); m != nil {
			out = append(out, settlement.CommissionEntry(m))
		}
	}
	return out
}

// priceCommission is the commission stored on an offer's price
// consideration, from before the commission moved to policies.commission.
func priceCommission(ctx context.Context, store Store, offerID, bppID string) map[string]any {
	rows, err := store.GetOfferConsiderations(ctx, sqlc.GetOfferConsiderationsParams{OfferID: offerID, BppID: bppID})
	if err != nil {
		return nil
	}
	for _, r := range rows {
		var attrs map[string]any
		if json.Unmarshal(r.ConsiderationAttributes, &attrs) == nil {
			if c := asMap(attrs["commission"]); c != nil {
				return copyMap(c)
			}
		}
	}
	return nil
}

// ApplyTo applies the terms (Apply) to a typed contract v (a pointer to a
// struct that marshals as a Beckn contract), then calls with on the contract
// as a map, and decodes the result back into v.
func ApplyTo(ctx context.Context, store Store, bppID string, d Defaults, v any, with func(map[string]any)) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var contract map[string]any
	if err := json.Unmarshal(raw, &contract); err != nil {
		return err
	}
	Apply(ctx, store, bppID, contract, d)
	if with != nil {
		with(contract)
	}
	out, err := json.Marshal(contract)
	if err != nil {
		return err
	}
	// Decode into a zeroed v, so no element of its old slices survives.
	target := reflect.ValueOf(v).Elem()
	target.Set(reflect.Zero(target.Type()))
	return json.Unmarshal(out, v)
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func slice(v any) []any          { s, _ := v.([]any); return s }
