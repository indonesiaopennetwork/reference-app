// Package cancellation applies full or partial cancellation to a contract.
//
// Records are never edited in place -- a cancelled record is marked and its
// replacement is issued alongside it:
//
//   - Commitments: the cancelled commitment is CLOSED (name "Cancelled") with
//     its original quantity; if units remain, a replacement "-aN" commitment
//     is ACTIVE with the remaining quantity. (Commitment.status only allows
//     DRAFT | ACTIVE | CLOSED.)
//   - Considerations: the current price consideration becomes CANCELLED; if
//     any commitment is still active, a replacement "-aN" consideration is
//     ACTIVE with the price of what remains. A cancellation fee, when the
//     offer's policies.cancellation.fee charges one, is its own ACTIVE
//     "cancellation-…-cN" consideration with a CANCELLATION_FEE breakup line.
//   - Settlements are append-only (Settlement.status has no CANCELLED): the
//     payment stays COMPLETE and a COMMITTED refund settlement is added.
//   - The contract becomes CANCELLED once no commitment is active.
package cancellation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	considerationContext = "https://schema.ion.id/release1/schema/extension/trade/TradeConsideration/v1/context.jsonld"
	settlementContext    = "https://schema.ion.id/release1/schema/extension/trade/TradeSettlement/v1/context.jsonld"

	// mockGatewayName matches the BAP's stand-in gateway: there is no real
	// payment gateway, so a refund reference is generated here.
	mockGatewayName = "mock-gateway"
	// refundTimelineDays follows ION's cancel.standard policy (P3D).
	refundTimelineDays = 3
)

// ErrNotAllowed is returned when an offer's cancellation policy forbids it.
var ErrNotAllowed = errors.New("cancellation not allowed by the offer's cancellation policy")

const contractContext = "https://schema.ion.id/release1/schema/extension/trade/TradeContract/v1/context.jsonld"

// Request is the cancellation the CN declared in
// contract.contractAttributes.cancellation (ION TradeContract): a reason
// (required by network policy) and, for a partial cancellation, the affected
// commitment IDs.
type Request struct {
	ReasonCode    string
	Reason        map[string]any // the full reason Descriptor, echoed back
	CommitmentIDs []string
	// FulfillmentStatus is the fulfillment state the BPP has recorded (e.g.
	// contract_performances), checked alongside the contract's own performance.
	FulfillmentStatus string
}

// RequestFrom reads contractAttributes.cancellation from a CN contract.
func RequestFrom(contract map[string]any) Request {
	c := asMap(asMap(contract["contractAttributes"])["cancellation"])
	reason := asMap(c["reason"])
	req := Request{ReasonCode: str(reason["code"]), Reason: reason}
	for _, id := range slice(c["commitmentIds"]) {
		if s := str(id); s != "" {
			req.CommitmentIDs = append(req.CommitmentIDs, s)
		}
	}
	return req
}

// Item selects units of one commitment to cancel. Quantity <= 0 means all of it.
type Item struct {
	CommitmentID string
	Quantity     float64
}

// Result is the cancelled contract plus the figures callers persist.
type Result struct {
	Contract map[string]any
	// Full is true when no commitment remains active (contract CANCELLED).
	Full   bool
	Fee    float64
	Refund float64
	// SettledAfter is the amount that stays settled once Refund is paid back.
	SettledAfter float64
	Currency     string
	// RefundSettlement is the settlement added to the contract, nil when
	// nothing is refunded.
	RefundSettlement map[string]any
	// RestoredUnits is the cancelled quantity per resource ID, for stock.
	RestoredUnits map[string]float64
	// Closed maps each cancelled commitment ID to its ACTIVE replacement's ID
	// ("" when nothing of it remains).
	Closed map[string]string
}

// Apply cancels items (all active commitments when items is empty) for req's
// reason on a copy of contract and returns the result, with the cancellation
// recorded in contractAttributes.cancellation. contract is not modified.
func Apply(contract map[string]any, items []Item, req Request, now time.Time) (*Result, error) {
	if req.ReasonCode == "" {
		return nil, errors.New("cancellation reason is required (contractAttributes.cancellation.reason.code)")
	}
	c, err := deepCopy(contract)
	if err != nil {
		return nil, err
	}
	commitments := slice(c["commitments"])
	active := map[string]map[string]any{}
	for _, cm := range commitments {
		if m := asMap(cm); m != nil && commitmentCode(m) != "CLOSED" {
			active[str(m["id"])] = m
		}
	}
	if len(active) == 0 {
		return nil, errors.New("contract has no active commitments to cancel")
	}

	targets, err := resolveTargets(commitments, active, items)
	if err != nil {
		return nil, err
	}

	stage, stageName := FulfillmentStage(c)
	if r := StageRank(req.FulfillmentStatus); r > stage {
		stage, stageName = r, req.FulfillmentStatus
	}
	res := &Result{RestoredUnits: map[string]float64{}, Closed: map[string]string{}}
	ids := collectIDs(c)
	var fee float64
	for _, t := range targets {
		cm := t.commitment
		if cancellationForbidden(cm) {
			return nil, fmt.Errorf("%w (commitment %s)", ErrNotAllowed, str(cm["id"]))
		}
		if stage >= stageDelivered {
			return nil, fmt.Errorf("%w: the order is already %s -- a delivered item is returned, not cancelled (commitment %s)", ErrNotAllowed, stageName, str(cm["id"]))
		}
		if cutoff := str(offerCancellationPolicy(cm)["cutoffEvent"]); CutoffPassed(cutoff, stage) {
			return nil, fmt.Errorf("%w: cutoff %s has passed (order is %s) (commitment %s)", ErrNotAllowed, cutoff, stageName, str(cm["id"]))
		}
		if allowed := allowedReasons(cm); len(allowed) > 0 && !allowed[req.ReasonCode] {
			return nil, fmt.Errorf("%w: reason %s is not in the offer's allowedReasons (commitment %s)", ErrNotAllowed, req.ReasonCode, str(cm["id"]))
		}
		qty := commitmentQty(cm)
		fee += cancellationFee(cm, unitTotal(cm)*t.qty)
		for _, r := range slice(cm["resources"]) {
			if id := str(asMap(r)["id"]); id != "" {
				res.RestoredUnits[id] += t.qty
			}
		}

		setCommitmentStatus(cm, "CLOSED", "Cancelled")
		res.Closed[str(cm["id"])] = ""
		if remaining := qty - t.qty; remaining > 0 {
			repl, _ := deepCopyMap(cm)
			replID := nextID(rootID(str(cm["id"])), "a", ids)
			res.Closed[str(cm["id"])] = replID
			repl["id"] = replID
			setCommitmentStatus(repl, "ACTIVE", "")
			setCommitmentQty(repl, remaining)
			commitments = append(commitments, repl)
			linkPerformance(c, str(cm["id"]), replID)
		}
	}
	c["commitments"] = commitments

	// Considerations: cancel the current price, issue the replacement and fee.
	considerations := slice(c["consideration"])
	price := currentPrice(considerations)
	if price == nil {
		return nil, errors.New("contract has no active price consideration")
	}
	priceAttrs := asMap(price["considerationAttributes"])
	oldTotal := num(priceAttrs["totalAmount"])
	res.Currency = str(priceAttrs["currency"])
	price["status"] = map[string]any{"code": "CANCELLED"}

	remaining := activeCommitments(commitments)
	var newTotal float64
	activePriceID := str(price["id"])
	if len(remaining) > 0 {
		breakup, total := priceBreakup(remaining)
		newTotal = total
		attrs := map[string]any{
			"@context": priceAttrs["@context"], "@type": priceAttrs["@type"],
			"currency": res.Currency, "breakup": breakup, "totalAmount": total,
		}
		if pm, ok := priceAttrs["paymentMethods"]; ok {
			attrs["paymentMethods"] = pm
		}
		activePriceID = nextID(rootID(str(price["id"])), "a", ids)
		considerations = append(considerations, map[string]any{
			"id":                      activePriceID,
			"status":                  map[string]any{"code": "ACTIVE"},
			"considerationAttributes": attrs,
		})
	}
	res.Fee = round2(math.Min(fee, math.Max(0, oldTotal-newTotal)))
	res.Refund = round2(oldTotal - newTotal - res.Fee)
	if res.Fee > 0 {
		considerations = append(considerations, map[string]any{
			"id":     nextID("cancellation-"+strings.TrimPrefix(rootID(str(price["id"])), "consideration-"), "c", ids),
			"status": map[string]any{"code": "ACTIVE"},
			"considerationAttributes": map[string]any{
				"@context": considerationContext, "@type": "ion:IONTradeConsideration",
				"currency":    res.Currency,
				"breakup":     []any{map[string]any{"type": "CANCELLATION_FEE", "title": "Cancellation fee", "amount": res.Fee}},
				"totalAmount": res.Fee,
			},
		})
	}
	c["consideration"] = considerations

	// Settlements: append a settlement recording the refund. Its settledAmount
	// is what stays settled afterwards -- the latest settled amount minus this
	// refund (i.e. what is still owed plus any retained fees) -- and it
	// discharges the ACTIVE price consideration (the original one once nothing
	// remains active).
	if res.Refund > 0 {
		settlements := slice(c["settlements"])
		payment := paymentSettlement(settlements)
		settledBefore := oldTotal
		if len(settlements) > 0 {
			settledBefore = num(asMap(asMap(settlements[len(settlements)-1])["settlementAttributes"])["settledAmount"])
		}
		res.SettledAfter = round2(math.Max(0, settledBefore-res.Refund))
		method, paymentID := "ONLINE", "settlement"
		if payment != nil {
			paymentID = str(payment["id"])
			if m := str(asMap(payment["settlementAttributes"])["method"]); m != "" {
				method = m
			}
		}
		res.RefundSettlement = map[string]any{
			"id":              nextID("refund-"+strings.TrimPrefix(paymentID, "settlement-"), "c", ids),
			"considerationId": activePriceID,
			"status":          "COMMITTED",
			"settlementAttributes": map[string]any{
				"@context": settlementContext, "@type": "ion:IONTradeSettlement",
				"method":        method,
				"settledAt":     now.UTC().Format(time.RFC3339),
				"settledAmount": res.SettledAfter,
				"currency":      res.Currency,
				"gateway":       map[string]any{"name": mockGatewayName, "transactionId": uuid.New().String()},
				"refund": map[string]any{
					"amount": res.Refund, "method": method,
					"timelineDays": refundTimelineDays, "status": "INITIATED",
				},
			},
		}
		c["settlements"] = append(settlements, res.RefundSettlement)
	}

	res.Full = len(remaining) == 0
	if res.Full {
		c["status"] = map[string]any{"code": "CANCELLED"}
	}
	recordCancellation(c, req, targets)
	res.Contract = c
	return res, nil
}

// ItemsFromUpdate compares a requested contract with the stored one and returns
// the cancellations it asks for: an active stored commitment that the request
// marks CLOSED (all of it) or lists with a lower quantity (the difference).
// It returns nil when the update requests no cancellation (e.g. a return).
//
// A commitment listed in contractAttributes.cancellation.commitmentIds with
// neither change is cancelled in full.
func ItemsFromUpdate(stored, requested map[string]any) []Item {
	storedActive := map[string]map[string]any{}
	for _, cm := range slice(stored["commitments"]) {
		if m := asMap(cm); m != nil && commitmentCode(m) != "CLOSED" {
			storedActive[str(m["id"])] = m
		}
	}
	var items []Item
	for _, cm := range slice(requested["commitments"]) {
		req := asMap(cm)
		st, ok := storedActive[str(req["id"])]
		if req == nil || !ok {
			continue
		}
		have, want := commitmentQty(st), commitmentQty(req)
		switch {
		case commitmentCode(req) == "CLOSED":
			items = append(items, Item{CommitmentID: str(req["id"]), Quantity: have})
		case want < have:
			items = append(items, Item{CommitmentID: str(req["id"]), Quantity: have - want})
		}
	}
	listed := map[string]bool{}
	for _, it := range items {
		listed[it.CommitmentID] = true
	}
	for _, id := range RequestFrom(requested).CommitmentIDs {
		if st, ok := storedActive[id]; ok && !listed[id] {
			items = append(items, Item{CommitmentID: id, Quantity: commitmentQty(st)})
			listed[id] = true
		}
	}
	return items
}

// ---------------------------------------------------------------------------

type target struct {
	commitment map[string]any
	qty        float64
}

func resolveTargets(commitments []any, active map[string]map[string]any, items []Item) ([]target, error) {
	if len(items) == 0 {
		var out []target
		for _, cm := range commitments {
			if m := asMap(cm); m != nil && active[str(m["id"])] != nil {
				out = append(out, target{m, commitmentQty(m)})
			}
		}
		return out, nil
	}
	var out []target
	for _, it := range items {
		cm, ok := active[it.CommitmentID]
		if !ok {
			return nil, fmt.Errorf("commitment %q is not an active commitment of this contract", it.CommitmentID)
		}
		have, q := commitmentQty(cm), it.Quantity
		if q <= 0 {
			q = have
		}
		if q > have {
			return nil, fmt.Errorf("cannot cancel %v of commitment %q: only %v active", q, it.CommitmentID, have)
		}
		out = append(out, target{cm, q})
	}
	return out, nil
}

func commitmentCode(cm map[string]any) string {
	return str(asMap(asMap(cm["status"])["descriptor"])["code"])
}

func setCommitmentStatus(cm map[string]any, code, name string) {
	d := map[string]any{"code": code}
	if name != "" {
		d["name"] = name
	}
	cm["status"] = map[string]any{"descriptor": d}
}

// commitmentQty reads resources[0].quantity.unitQuantity, falling back to
// commitmentAttributes.quantity.unitQuantity, then 1.
func commitmentQty(cm map[string]any) float64 {
	if rs := slice(cm["resources"]); len(rs) > 0 {
		if q := num(asMap(asMap(rs[0])["quantity"])["unitQuantity"]); q > 0 {
			return q
		}
	}
	if q := num(asMap(asMap(cm["commitmentAttributes"])["quantity"])["unitQuantity"]); q > 0 {
		return q
	}
	return 1
}

func setCommitmentQty(cm map[string]any, q float64) {
	for _, r := range slice(cm["resources"]) {
		if qm := asMap(asMap(r)["quantity"]); qm != nil {
			qm["unitQuantity"] = q
		}
	}
	if qm := asMap(asMap(cm["commitmentAttributes"])["quantity"]); qm != nil {
		qm["unitQuantity"] = q
	}
}

func activeCommitments(commitments []any) []map[string]any {
	var out []map[string]any
	for _, cm := range commitments {
		if m := asMap(cm); m != nil && commitmentCode(m) != "CLOSED" {
			out = append(out, m)
		}
	}
	return out
}

// unitLines is a commitment's per-unit price: its offer consideration's
// BASE_PRICE and TAX lines, or a single BASE_PRICE from the commitment price.
func unitLines(cm map[string]any) []map[string]any {
	offer := asMap(cm["offer"])
	name := str(asMap(offer["descriptor"])["name"])
	var lines []map[string]any
	if cons := slice(offer["considerations"]); len(cons) > 0 {
		attrs := asMap(asMap(cons[0])["considerationAttributes"])
		for _, b := range slice(attrs["breakup"]) {
			bm := asMap(b)
			if t := str(bm["type"]); t == "BASE_PRICE" || t == "TAX" {
				line, _ := deepCopyMap(bm)
				if t == "BASE_PRICE" && name != "" {
					line["title"] = name
				}
				lines = append(lines, line)
			}
		}
		if len(lines) == 0 && num(attrs["totalAmount"]) > 0 {
			lines = append(lines, map[string]any{"type": "BASE_PRICE", "title": name, "amount": num(attrs["totalAmount"])})
		}
	}
	if len(lines) == 0 {
		price := num(asMap(asMap(cm["commitmentAttributes"])["price"])["value"])
		lines = append(lines, map[string]any{"type": "BASE_PRICE", "title": name, "amount": price})
	}
	return lines
}

func unitTotal(cm map[string]any) float64 {
	var t float64
	for _, l := range unitLines(cm) {
		t += num(l["amount"])
	}
	return t
}

// priceBreakup is the breakup and total for the given commitments' quantities.
func priceBreakup(cms []map[string]any) ([]any, float64) {
	var out []any
	var total float64
	for _, cm := range cms {
		qty := commitmentQty(cm)
		for _, l := range unitLines(cm) {
			l["amount"] = round2(num(l["amount"]) * qty)
			if td := asMap(l["taxDetail"]); td != nil {
				if base, ok := td["taxableBase"].(float64); ok {
					td["taxableBase"] = round2(base * qty)
				}
			}
			total += num(l["amount"])
			out = append(out, l)
		}
	}
	return out, round2(total)
}

func offerCancellationPolicy(cm map[string]any) map[string]any {
	return asMap(asMap(asMap(asMap(cm["offer"])["offerAttributes"])["policies"])["cancellation"])
}

// Fulfillment stages, in order. Performance status codes set by the seller
// (PENDING → Packed → Shipped → Delivered; returns only follow delivery).
const (
	stagePending = iota
	stagePacked
	stageShipped
	stageDelivered
)

// StageRank maps a performance status code to its fulfillment stage.
func StageRank(code string) int {
	switch c := strings.ToUpper(code); {
	case c == "PACKED":
		return stagePacked
	case c == "DISPATCHED" || c == "SHIPPED" || c == "IN_TRANSIT" || c == "OUT_FOR_DELIVERY":
		return stageShipped
	case c == "DELIVERED" || strings.HasPrefix(c, "RETURN"):
		return stageDelivered
	}
	return stagePending
}

// FulfillmentStage is the furthest stage any of the contract's performances
// has reached, with that status code.
func FulfillmentStage(contract map[string]any) (int, string) {
	best, name := stagePending, ""
	for _, p := range slice(contract["performance"]) {
		code := str(asMap(asMap(p)["status"])["code"])
		if r := StageRank(code); r > best || name == "" {
			best, name = max(best, r), code
		}
	}
	return best, name
}

// CutoffPassed reports whether an offer's policies.cancellation.cutoffEvent
// has passed for a confirmed order at the given fulfillment stage.
func CutoffPassed(cutoffEvent string, stage int) bool {
	switch cutoffEvent {
	case "BEFORE_CONFIRMATION":
		return true // the order is already confirmed
	case "BEFORE_PACKING":
		return stage >= stagePacked
	case "BEFORE_DISPATCH", "BEFORE_SHIPMENT":
		return stage >= stageShipped
	}
	return false
}

// allowedReasons is the offer's policies.cancellation.allowedReasons as a set.
func allowedReasons(cm map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, r := range slice(offerCancellationPolicy(cm)["allowedReasons"]) {
		out[str(r)] = true
	}
	return out
}

// recordCancellation writes contractAttributes.cancellation (ION TradeContract):
// the reason and the commitments this cancellation affected.
func recordCancellation(c map[string]any, req Request, targets []target) {
	attrs := asMap(c["contractAttributes"])
	if attrs == nil {
		attrs = map[string]any{"@context": contractContext, "@type": "ion:IONTradeContract"}
		c["contractAttributes"] = attrs
	}
	ids := make([]any, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, str(t.commitment["id"]))
	}
	reason := req.Reason
	if reason == nil {
		reason = map[string]any{}
	}
	reason["code"] = req.ReasonCode
	attrs["cancellation"] = map[string]any{"reason": reason, "commitmentIds": ids}
}

func cancellationForbidden(cm map[string]any) bool {
	allowed, ok := offerCancellationPolicy(cm)["allowed"].(bool)
	return ok && !allowed
}

// cancellationFee applies the offer's policies.cancellation.fee to the amount
// paid for the cancelled units: PERCENTAGE of that amount, or FIXED.
func cancellationFee(cm map[string]any, cancelledAmount float64) float64 {
	fee := asMap(offerCancellationPolicy(cm)["fee"])
	switch str(fee["type"]) {
	case "PERCENTAGE":
		return round2(cancelledAmount * num(fee["value"]) / 100)
	case "FIXED":
		return num(fee["value"])
	}
	return 0
}

func currentPrice(considerations []any) map[string]any {
	for _, c := range considerations {
		m := asMap(c)
		if m == nil || strings.HasPrefix(str(m["id"]), "cancellation-") {
			continue
		}
		if str(asMap(m["status"])["code"]) != "CANCELLED" {
			return m
		}
	}
	return nil
}

// paymentSettlement is the first settlement that is not itself a refund.
func paymentSettlement(settlements []any) map[string]any {
	for _, s := range settlements {
		m := asMap(s)
		if m != nil && asMap(m["settlementAttributes"])["refund"] == nil {
			return m
		}
	}
	return nil
}

// linkPerformance adds the replacement commitment wherever the old one is referenced.
func linkPerformance(c map[string]any, oldID, newID string) {
	for _, p := range slice(c["performance"]) {
		pm := asMap(p)
		ids := slice(pm["commitmentIds"])
		for _, id := range ids {
			if str(id) == oldID {
				pm["commitmentIds"] = append(ids, newID)
				break
			}
		}
	}
}

// rootID strips a trailing "-aN" / "-cN" replacement suffix.
func rootID(id string) string {
	if i := strings.LastIndex(id, "-"); i > 0 && len(id) > i+2 {
		suffix := id[i+1:]
		if (suffix[0] == 'a' || suffix[0] == 'c') && strings.Trim(suffix[1:], "0123456789") == "" {
			return id[:i]
		}
	}
	return id
}

// nextID returns base-<letter>N for the smallest N >= 1 not already used, and
// reserves it.
func nextID(base, letter string, used map[string]bool) string {
	for n := 1; ; n++ {
		id := fmt.Sprintf("%s-%s%d", base, letter, n)
		if !used[id] {
			used[id] = true
			return id
		}
	}
}

func collectIDs(c map[string]any) map[string]bool {
	ids := map[string]bool{}
	for _, key := range []string{"commitments", "consideration", "settlements"} {
		for _, v := range slice(c[key]) {
			ids[str(asMap(v)["id"])] = true
		}
	}
	return ids
}

// ---------------------------------------------------------------------------

func deepCopy(v map[string]any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(b, &out)
}

func deepCopyMap(v map[string]any) (map[string]any, error) { return deepCopy(v) }

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func slice(v any) []any          { s, _ := v.([]any); return s }
func str(v any) string           { s, _ := v.(string); return s }

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }
