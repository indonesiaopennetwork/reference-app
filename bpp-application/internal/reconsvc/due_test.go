package reconsvc

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ion/winroom/bpp/internal/config"
)

func testService() *Service {
	return &Service{cfg: &config.Config{BppID: "bpp-1", SettlementBasis: "AFTER_DELIVERY", SettlementWindow: "P1D"}}
}

// contract: paid 90,000 with DELIVERY / PT2M terms (the pre-ION name for
// AFTER_DELIVERY, as older stored settlements have it), then 45,000 refunded
// from the gateway (cancellation of 1 of 2 units).
func testContract(t *testing.T) map[string]any {
	var c map[string]any
	err := json.Unmarshal([]byte(`{
	  "id": "c-1",
	  "consideration": [{"id": "cancellation-x-c1", "considerationAttributes": {"totalAmount": 0}}],
	  "settlements": [
	    {"id": "settlement-a", "status": "COMPLETE", "settlementAttributes": {"currency": "IDR", "settledAmount": 90000,
	      "settlementBasis": "DELIVERY", "settlementWindow": "PT2M", "settledAt": "2026-09-29T10:00:00Z"}},
	    {"id": "refund-a-c1", "status": "COMMITTED", "settlementAttributes": {"currency": "IDR", "settledAmount": 45000,
	      "settledAt": "2026-09-29T10:05:00Z", "refund": {"amount": 45000, "source": "PAYMENT_GATEWAY"}}}
	  ]}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDueAfterDeliveryPlusWindow(t *testing.T) {
	s := testService()
	delivered := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	d := DueContract{contract: testContract(t)}
	if !s.assess(&d, &delivered, nil, delivered.Add(time.Minute)) {
		t.Fatal("contract with unreconciled settlements must be listed")
	}
	if d.Due || d.DueAt == nil || !d.DueAt.Equal(delivered.Add(2*time.Minute)) {
		t.Fatalf("1 min after delivery: due=%v dueAt=%v, want not due, due at delivery+2m", d.Due, d.DueAt)
	}
	if d.Paid != 90000 || d.GatewayRefund != 45000 || d.Window != "PT2M" {
		t.Errorf("paid=%v gatewayRefund=%v window=%s", d.Paid, d.GatewayRefund, d.Window)
	}

	d = DueContract{contract: testContract(t)}
	s.assess(&d, &delivered, nil, delivered.Add(3*time.Minute))
	if !d.Due {
		t.Error("3 min after delivery with a PT2M window: must be due")
	}
}

func TestNotDueBeforeDelivery(t *testing.T) {
	d := DueContract{contract: testContract(t)}
	testService().assess(&d, nil, nil, time.Now())
	if d.Due || d.DueAt != nil || d.BasisEvent != "awaiting delivery" {
		t.Errorf("undelivered: due=%v dueAt=%v event=%s", d.Due, d.DueAt, d.BasisEvent)
	}
}

func TestCancelledBeforeDeliveryCountsFromCancellation(t *testing.T) {
	cancelled := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	d := DueContract{contract: testContract(t)}
	testService().assess(&d, nil, &cancelled, cancelled.Add(5*time.Minute))
	if !d.Due || d.BasisEvent != "cancelled" {
		t.Errorf("cancelled: due=%v event=%s, want due from the cancellation", d.Due, d.BasisEvent)
	}
}

func TestReconciledSettlementsAreSkipped(t *testing.T) {
	c := testContract(t)
	for _, st := range slice(c["settlements"]) {
		asMap(asMap(st)["settlementAttributes"])["reconciliationId"] = "recon-1"
	}
	d := DueContract{contract: c}
	if testService().assess(&d, nil, nil, time.Now()) {
		t.Error("a fully reconciled contract has nothing to reconcile")
	}
}

func TestProviderRefundAfterSettlementIsDueFromTheRefund(t *testing.T) {
	c := testContract(t)
	sts := slice(c["settlements"])
	pay := asMap(asMap(sts[0])["settlementAttributes"])
	pay["reconciliationId"], pay["reconciliationStatus"] = "recon-1", "AGREED"
	asMap(asMap(asMap(sts[1])["settlementAttributes"])["refund"])["source"] = "PROVIDER"
	delivered := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)

	d := DueContract{contract: c}
	testService().assess(&d, &delivered, nil, time.Date(2026, 9, 29, 10, 10, 0, 0, time.UTC))
	if d.BasisEvent != "refund" || d.Paid != 0 || d.ProviderRefund != 45000 || !d.Due {
		t.Errorf("event=%s paid=%v providerRefund=%v due=%v", d.BasisEvent, d.Paid, d.ProviderRefund, d.Due)
	}
}

func TestSummaryAndStamp(t *testing.T) {
	s := testService()
	delivered := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	d := DueContract{contract: testContract(t)}
	s.assess(&d, &delivered, nil, delivered.Add(time.Hour))
	sm := s.summarize("bap-1", []DueContract{d}, "2026-09-29")
	if sm.TotalPaidAmount != 90000 || sm.TotalGatewayRefundAmount != 45000 || sm.NetPayable != 45000 || sm.ContractCount != 1 {
		t.Errorf("summary = %+v", sm)
	}

	c, err := stamp(d, "recon-x")
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range slice(c["settlements"]) {
		a := asMap(asMap(st)["settlementAttributes"])
		if str(a["reconciliationId"]) != "recon-x" || str(a["reconciliationStatus"]) != "PENDING" {
			t.Errorf("settlement %v not stamped: %v", asMap(st)["id"], a)
		}
	}
	if str(asMap(asMap(slice(d.contract["settlements"])[0])["settlementAttributes"])["reconciliationId"]) != "" {
		t.Error("stamp must not modify the source contract")
	}
}

func TestReconciliationIDIncludesTime(t *testing.T) {
	at := time.Date(2026, 9, 29, 18, 32, 5, 0, time.FixedZone("IST", 19800))
	none := func(string) bool { return false }
	if got, want := reconciliationID(at, "bap.example.com", none), "recon-2026-09-29T130205Z-bap.example.com"; got != want {
		t.Errorf("id = %s, want %s (UTC time)", got, want)
	}
	if a, b := reconciliationID(at, "bap", none), reconciliationID(at.Add(time.Second), "bap", none); a == b {
		t.Errorf("batches a second apart share id %s", a)
	}
	taken := map[string]bool{"recon-2026-09-29T130205Z-bap": true}
	if got := reconciliationID(at, "bap", func(id string) bool { return taken[id] }); got != "recon-2026-09-29T130205Z-bap-2" {
		t.Errorf("same-second id = %s, want the -2 suffix", got)
	}
}

func TestLegacyBasisReadAsION(t *testing.T) {
	s := testService()
	delivered := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	d := DueContract{contract: testContract(t)}
	s.assess(&d, &delivered, nil, delivered)
	if d.Basis != "AFTER_DELIVERY" || d.BasisEvent != "delivered" {
		t.Fatalf("basis = %q (%s), want AFTER_DELIVERY from delivery", d.Basis, d.BasisEvent)
	}
}

func TestDueOnConfirmation(t *testing.T) {
	s := testService()
	c := testContract(t)
	attrs := c["settlements"].([]any)[0].(map[string]any)["settlementAttributes"].(map[string]any)
	attrs["settlementBasis"] = "ON_CONFIRMATION"
	paid := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)

	d := DueContract{contract: c}
	s.assess(&d, nil, nil, paid.Add(time.Minute))
	if d.BasisEvent != "confirmed" || d.DueAt == nil || !d.DueAt.Equal(paid.Add(2*time.Minute)) {
		t.Fatalf("ON_CONFIRMATION: event %q, due %v; want confirmed, due 2 minutes after payment", d.BasisEvent, d.DueAt)
	}
	if d.Due {
		t.Fatal("not due before the window has passed")
	}
}
