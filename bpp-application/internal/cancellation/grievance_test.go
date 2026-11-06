package cancellation

import (
	"testing"
	"time"
)

func grievanceContract() map[string]any {
	return map[string]any{
		"consideration": []any{map[string]any{"id": "consideration-1", "status": map[string]any{"code": "ACTIVE"},
			"considerationAttributes": map[string]any{"currency": "IDR", "totalAmount": 100000.0}}},
		"settlements": []any{
			map[string]any{"id": "settlement-1", "settlementAttributes": map[string]any{"currency": "IDR", "settledAmount": 100000.0}},
			map[string]any{"id": "refund-1-c1", "settlementAttributes": map[string]any{
				"currency": "IDR", "settledAmount": 11000.0,
				"gateway": map[string]any{"name": "mock-gateway", "transactionId": "first-payout"},
				"refund":  map[string]any{"amount": 89000.0, "status": "FAILED"},
			}},
		},
	}
}

// A new grievance refund is a new settlement linked to the latest one;
// running it again for the same action adds nothing.
func TestGrievanceRefundNewIsIdempotent(t *testing.T) {
	c := grievanceContract()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	ref := GrievanceRefund(c, "refund-grv-x-v1-1", "GRV-X/ACT-001", 30000, now)
	again := GrievanceRefund(c, "refund-grv-x-v1-1", "GRV-X/ACT-001", 30000, now)
	settlements := slice(c["settlements"])
	if len(settlements) != 3 || ref == "" || again != ref {
		t.Fatalf("want one new settlement and the same reference twice, got %d settlements, %q / %q", len(settlements), ref, again)
	}
	attrs := asMap(asMap(settlements[2])["settlementAttributes"])
	if attrs["relatedSettlementId"] != "refund-1-c1" || num(asMap(attrs["refund"])["amount"]) != 30000 {
		t.Errorf("unexpected settlement: %v", attrs)
	}
}

// A refund agreed in the order flow that never arrived is paid out again
// on its own settlement, not added a second time.
func TestGrievanceRefundRetriesLinkedSettlement(t *testing.T) {
	c := grievanceContract()
	ref := GrievanceRefund(c, "refund-1-c1", "GRV-Y/ACT-001", 89000, time.Now())
	if len(slice(c["settlements"])) != 2 {
		t.Fatal("a retry must not add a settlement")
	}
	attrs := asMap(asMap(slice(c["settlements"])[1])["settlementAttributes"])
	if ref == "first-payout" || str(asMap(attrs["gateway"])["transactionId"]) != ref || str(asMap(attrs["refund"])["status"]) != "INITIATED" {
		t.Errorf("want a new payout reference on the same settlement, got %q: %v", ref, attrs)
	}
	if again := GrievanceRefund(c, "refund-1-c1", "GRV-Y/ACT-001", 89000, time.Now()); again != ref {
		t.Errorf("a second run must not pay out again: %q vs %q", again, ref)
	}
}

func TestGrievancePerformance(t *testing.T) {
	c := map[string]any{"performance": []any{map[string]any{"id": "f1"}}}
	id := GrievancePerformance(c, "redelivery-grv-1", "REDELIVERY", []string{"L01"})
	GrievancePerformance(c, "redelivery-grv-1", "REDELIVERY", []string{"L01"})
	if id != "redelivery-grv-1" || len(slice(c["performance"])) != 2 {
		t.Fatalf("want one redelivery performance, got %v", c["performance"])
	}
}
