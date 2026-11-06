package cancellation

import (
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

const performanceContext = "https://schema.ion.id/release1/schema/extension/trade/TradePerformance/v1/context.jsonld"

// GrievanceRefund carries out the refund of a grievance action (key
// identifies it, e.g. "<grievanceId>/<actionId>") on contract c, and
// returns the payout's gateway reference: the action's executionReference.
//
//   - When c already has settlement id -- a refund agreed in the order flow
//     that never arrived -- that refund is paid out again on the same
//     settlement, so SEAM and reconcile count it once.
//   - Otherwise a new refund settlement id is added, pointing at the latest
//     earlier settlement (relatedSettlementId).
//
// Running it again for the same key changes nothing and returns the same
// reference.
func GrievanceRefund(c map[string]any, id, key string, amount float64, now time.Time) string {
	settlements := slice(c["settlements"])
	for _, s := range settlements {
		sm := asMap(s)
		if str(sm["id"]) != id {
			continue
		}
		attrs := asMap(sm["settlementAttributes"])
		refund := asMap(attrs["refund"])
		if refund == nil {
			refund = map[string]any{"amount": amount}
			attrs["refund"] = refund
		}
		if str(refund["grievanceKey"]) == key {
			return str(asMap(attrs["gateway"])["transactionId"])
		}
		ref := uuid.New().String()
		attrs["gateway"] = map[string]any{"name": mockGatewayName, "transactionId": ref}
		refund["status"] = "INITIATED"
		refund["grievanceKey"] = key
		refund["retriedAt"] = now.UTC().Format(time.RFC3339)
		return ref
	}

	payment := paymentSettlement(settlements)
	currency, method, related := "IDR", "ONLINE", ""
	settledBefore := 0.0
	if payment != nil {
		pa := asMap(payment["settlementAttributes"])
		if cur := str(pa["currency"]); cur != "" {
			currency = cur
		}
		related = str(payment["id"])
		settledBefore = settledAmount(pa)
	}
	if n := len(settlements); n > 0 {
		last := asMap(settlements[n-1])
		related = str(last["id"])
		settledBefore = settledAmount(asMap(last["settlementAttributes"]))
	}
	considerationID := ""
	if price := currentPrice(slice(c["consideration"])); price != nil {
		considerationID = str(price["id"])
	}
	ref := uuid.New().String()
	attrs := map[string]any{
		"@context": settlementContext, "@type": "ion:IONTradeSettlement",
		"method":        method,
		"settledAt":     now.UTC().Format(time.RFC3339),
		"settledAmount": round2(math.Max(0, settledBefore-amount)),
		"currency":      currency,
		"gateway":       map[string]any{"name": mockGatewayName, "transactionId": ref},
		"refund": map[string]any{
			"amount": round2(amount), "method": method, "timelineDays": refundTimelineDays,
			"status": "INITIATED", "source": RefundSource(payment), "grievanceKey": key,
		},
	}
	if related != "" {
		attrs["relatedSettlementId"] = related
	}
	c["settlements"] = append(settlements, map[string]any{
		"id": id, "considerationId": considerationID, "status": "COMMITTED", "settlementAttributes": attrs,
	})
	return ref
}

// GrievancePerformance adds the redelivery or replacement (kind) a
// grievance action agreed for commitmentIDs to contract c, as a Pending
// performance with id, and returns id. An existing performance id is left
// as it is.
func GrievancePerformance(c map[string]any, id, kind string, commitmentIDs []string) string {
	for _, p := range slice(c["performance"]) {
		if str(asMap(p)["id"]) == id {
			return id
		}
	}
	ids := make([]any, len(commitmentIDs))
	for i, cid := range commitmentIDs {
		ids[i] = cid
	}
	upsertPerformance(c, map[string]any{
		"id":            id,
		"status":        map[string]any{"code": "Pending", "name": strings.ToUpper(kind[:1]) + strings.ToLower(kind[1:]) + " pending"},
		"commitmentIds": ids,
		"performanceAttributes": map[string]any{
			"@context": performanceContext, "@type": "ion:IONTradePerformance",
			"type": kind, "supportedPerformanceModes": []any{"DELIVERY"},
		},
	})
	return id
}
