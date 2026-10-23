// Package offerterms puts the seller's settlement terms on its offers (ION
// Release 1; see package settlement):
//
//   - the buyer finder fee the seller pays the buyer app, on the offer's
//     consideration: considerationAttributes.buyerFinderFee
//     (IONTradeConsideration) -- feeType PERCENT or AMOUNT (per unit),
//     feeValue, and for PERCENT appliesTo ITEM_TOTAL | ORDER_TOTAL;
//   - the payout term, offerAttributes.policies.settlement (IONTradeOffer) --
//     settlementBasis and settlementWindow.
//
// An offer published without them gets this BPP's defaults (Defaults):
// FillAttributes for the offer, FillFee for its considerations. Only the
// contract's price consideration carries the order's total fee (see package
// settlement). At select, init and confirm the order's offers get the terms
// stored for them here (Apply), so what the buyer app sends back cannot change
// them.
//
// Offers stored the earlier ways -- the fee as policies.commission entries,
// or as the price consideration's commission, under the field names name,
// type, value -- are read and rewritten as buyerFinderFee.
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

// Fee is d's buyer finder fee terms.
func (d Defaults) Fee() map[string]any {
	return feeTerms(map[string]any{"feeType": d.CommissionType, "feeValue": d.CommissionValue, "appliesTo": d.CommissionAppliesTo})
}

// Policy is d's policies.settlement.
func (d Defaults) Policy() map[string]any {
	return map[string]any{"settlementBasis": d.Basis, "settlementWindow": d.Window}
}

// feeTerms is a fee's terms (feeType, feeValue, and appliesTo for PERCENT)
// under ION's field names, from a buyerFinderFee or an earlier commission
// entry (name, type, value).
func feeTerms(m map[string]any) map[string]any {
	e := settlement.CommissionEntry(m)
	out := map[string]any{"feeType": e["feeType"], "feeValue": e["feeValue"]}
	if e["feeType"] == "PERCENT" {
		applies, _ := e["appliesTo"].(string)
		if applies == "" {
			applies = "ITEM_TOTAL"
		}
		out["appliesTo"] = applies
	}
	return out
}

// legacyFee is the fee an offer stored the earlier way states: the first of
// its policies.commission entries, in feeTerms form; nil when none.
func legacyFee(offerAttrs map[string]any) map[string]any {
	for _, e := range slice(asMap(offerAttrs["policies"])["commission"]) {
		if m := asMap(e); m != nil {
			return feeTerms(m)
		}
	}
	return nil
}

// FillAttributes gives offer attributes without policies.settlement d's,
// and drops policies.commission (the fee is on the offer's consideration
// now; FillFee carries an earlier one over). An empty or unreadable raw is
// returned unchanged.
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

// FillFee gives an offer consideration's attributes its buyer finder fee:
// the one it states (renamed when stored under the earlier field names), or
// one stated the earlier way -- as its commission, or in the offer's
// policies.commission (offerAttrs, the offer's attributes as received) --
// or else d's. An empty or unreadable raw is returned unchanged.
func FillFee(raw, offerAttrs json.RawMessage, d Defaults) json.RawMessage {
	var attrs, offer map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &attrs) != nil || attrs == nil {
		return raw
	}
	_ = json.Unmarshal(offerAttrs, &offer)
	fee := d.Fee()
	switch {
	case asMap(attrs["buyerFinderFee"]) != nil && asMap(attrs["buyerFinderFee"])["feeType"] != nil:
		fee = feeTerms(asMap(attrs["buyerFinderFee"]))
	case asMap(attrs["commission"]) != nil:
		fee = feeTerms(asMap(attrs["commission"]))
	case legacyFee(offer) != nil:
		fee = legacyFee(offer)
	}
	if reflect.DeepEqual(attrs["buyerFinderFee"], fee) && attrs["commission"] == nil {
		return raw
	}
	attrs["buyerFinderFee"] = fee
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
	if _, ok := policies["commission"]; ok {
		delete(policies, "commission")
		changed = true
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
// published before the terms, or no longer stored). The fee goes on each of
// the offer's considerations as buyerFinderFee; policies.commission and a
// consideration's commission (earlier forms) are removed.
func Apply(ctx context.Context, store Store, bppID string, contract map[string]any, d Defaults) {
	for _, raw := range slice(contract["commitments"]) {
		offer := asMap(asMap(raw)["offer"])
		if offer == nil {
			continue
		}
		policy, fee := d.Policy(), d.Fee()
		if id, _ := offer["id"].(string); id != "" {
			var stored map[string]any
			if row, err := store.GetOffer(ctx, sqlc.GetOfferParams{ID: id, BppID: bppID}); err == nil {
				_ = json.Unmarshal(row.OfferAttributes, &stored)
				var filled map[string]any
				if json.Unmarshal(FillAttributes(row.OfferAttributes, d), &filled) == nil {
					if p := asMap(asMap(filled["policies"])["settlement"]); p != nil {
						policy = p
					}
				}
			}
			if f := storedFee(ctx, store, id, bppID); f != nil {
				fee = f
			} else if f := legacyFee(stored); f != nil {
				fee = f
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
		delete(policies, "commission")
		for _, c := range slice(offer["considerations"]) {
			cm := asMap(c)
			ca := asMap(cm["considerationAttributes"])
			if ca == nil {
				continue
			}
			delete(ca, "commission")
			ca["buyerFinderFee"] = copyMap(fee)
		}
	}
}

// storedFee is the buyer finder fee stored on an offer's considerations: its
// buyerFinderFee, or (stored before the fee had that name) its commission.
func storedFee(ctx context.Context, store Store, offerID, bppID string) map[string]any {
	rows, err := store.GetOfferConsiderations(ctx, sqlc.GetOfferConsiderationsParams{OfferID: offerID, BppID: bppID})
	if err != nil {
		return nil
	}
	for _, r := range rows {
		var attrs map[string]any
		if json.Unmarshal(r.ConsiderationAttributes, &attrs) != nil {
			continue
		}
		if f := asMap(attrs["buyerFinderFee"]); f != nil && f["feeType"] != nil {
			return feeTerms(f)
		}
		if c := asMap(attrs["commission"]); c != nil {
			return feeTerms(c)
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
