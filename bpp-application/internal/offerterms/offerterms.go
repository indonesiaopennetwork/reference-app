// Package offerterms puts the seller's settlement terms on its offers (ION
// Release 1; see package settlement):
//
//   - the commission (finder fee) the seller pays the buyer app, on each of
//     the offer's considerations: considerationAttributes.commission
//     (IONTradeConsideration) -- type PERCENT or AMOUNT (per unit), value,
//     and for PERCENT appliesTo ITEM_TOTAL | ORDER_TOTAL;
//   - the payout term, offerAttributes.policies.settlement (IONTradeOffer) --
//     settlementBasis and settlementWindow.
//
// An offer published without them gets this BPP's defaults (Defaults). At
// select, init and confirm the order's offers get the terms stored for them
// here (Apply), so what the buyer app sends back cannot change them.
package offerterms

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/ion/winroom/bpp/db/sqlc"
	"github.com/ion/winroom/bpp/internal/config"
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

// Commission is d's commission object.
func (d Defaults) Commission() map[string]any {
	c := map[string]any{"type": d.CommissionType, "value": d.CommissionValue}
	if d.CommissionType == "PERCENT" {
		c["appliesTo"] = d.CommissionAppliesTo
	}
	return c
}

// Policy is d's policies.settlement.
func (d Defaults) Policy() map[string]any {
	return map[string]any{"settlementBasis": d.Basis, "settlementWindow": d.Window}
}

// FillAttributes gives offer attributes without policies.settlement d's. An
// empty or unreadable raw is returned unchanged.
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

// FillConsideration gives consideration attributes without a commission d's.
func FillConsideration(raw json.RawMessage, d Defaults) json.RawMessage {
	var attrs map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &attrs) != nil || attrs == nil {
		return raw
	}
	if _, ok := attrs["commission"].(map[string]any); ok {
		return raw
	}
	attrs["commission"] = d.Commission()
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
	if p, ok := policies["settlement"].(map[string]any); ok && (p["settlementBasis"] != nil || p["settlementWindow"] != nil) {
		changed := false
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
// published before the terms, or no longer stored).
func Apply(ctx context.Context, store Store, bppID string, contract map[string]any, d Defaults) {
	for _, raw := range slice(contract["commitments"]) {
		offer := asMap(asMap(raw)["offer"])
		if offer == nil {
			continue
		}
		policy, commission := d.Policy(), d.Commission()
		if id, _ := offer["id"].(string); id != "" {
			if row, err := store.GetOffer(ctx, sqlc.GetOfferParams{ID: id, BppID: bppID}); err == nil {
				var attrs map[string]any
				if json.Unmarshal(FillAttributes(row.OfferAttributes, d), &attrs) == nil {
					if p := asMap(asMap(attrs["policies"])["settlement"]); p != nil {
						policy = p
					}
				}
			}
			if rows, err := store.GetOfferConsiderations(ctx, sqlc.GetOfferConsiderationsParams{OfferID: id, BppID: bppID}); err == nil {
				for _, r := range rows {
					var attrs map[string]any
					if json.Unmarshal(r.ConsiderationAttributes, &attrs) == nil {
						if c := asMap(attrs["commission"]); c != nil {
							commission = c
							break
						}
					}
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
		for _, c := range slice(offer["considerations"]) {
			cm := asMap(c)
			ca := asMap(cm["considerationAttributes"])
			if ca == nil {
				ca = map[string]any{}
				cm["considerationAttributes"] = ca
			}
			ca["commission"] = copyMap(commission)
		}
	}
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
