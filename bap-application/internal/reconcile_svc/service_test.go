package reconcilesvc

import (
	"encoding/json"
	"testing"
)

const batch = "recon-2026-09-29-bap"

func obj(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// ours: the buyer app's copy -- paid 90,000, then a gateway refund of 45,000.
const ours = `{"id": "c-1", "settlements": [
  {"id": "settlement-a", "settlementAttributes": {"currency": "IDR", "settledAmount": 90000}},
  {"id": "refund-a-c1", "settlementAttributes": {"currency": "IDR", "settledAmount": 45000, "refund": {"amount": 45000, "source": "PAYMENT_GATEWAY"}}}]}`

// claimed builds the BPP's version of the same contract, with both settlements in the batch.
func claimed(t *testing.T, paid, refund float64, source string) map[string]any {
	c := obj(t, ours)
	sts := slice(c["settlements"])
	pa := asMap(asMap(sts[0])["settlementAttributes"])
	ra := asMap(asMap(sts[1])["settlementAttributes"])
	pa["settledAmount"], pa["reconciliationId"] = paid, batch
	ra["reconciliationId"] = batch
	asMap(ra["refund"])["amount"], asMap(ra["refund"])["source"] = refund, source
	return c
}

func TestMatchingContractIsAgreed(t *testing.T) {
	v := Verdict{ReconciliationStatus: "AGREED"}
	paid, gw, prov, cur := checkContract(&v, claimed(t, 90000, 45000, "PAYMENT_GATEWAY"), obj(t, ours), batch)
	if v.ReconciliationStatus != "AGREED" || paid != 90000 || gw != 45000 || prov != 0 || cur != "IDR" {
		t.Errorf("status=%s paid=%v gw=%v prov=%v cur=%s", v.ReconciliationStatus, paid, gw, prov, cur)
	}
	if len(v.settlements) != 2 {
		t.Errorf("batch settlements = %v", v.settlements)
	}
}

func TestMismatchesAreDisputed(t *testing.T) {
	cases := map[string]map[string]any{
		"AMOUNT_MISMATCH":        claimed(t, 99000, 45000, "PAYMENT_GATEWAY"),
		"REFUND_SOURCE_MISMATCH": claimed(t, 90000, 45000, "PROVIDER"),
	}
	unknown := claimed(t, 90000, 45000, "PAYMENT_GATEWAY")
	asMap(slice(unknown["settlements"])[1])["id"] = "refund-zzz"
	cases["UNKNOWN_SETTLEMENT"] = unknown

	for want, c := range cases {
		v := Verdict{ReconciliationStatus: "AGREED"}
		checkContract(&v, c, obj(t, ours), batch)
		if v.ReconciliationStatus != "DISPUTED" || str(v.Dispute["code"]) != want {
			t.Errorf("%s: status=%s dispute=%v", want, v.ReconciliationStatus, v.Dispute)
		}
	}
}

func TestOnlyThisBatchsSettlementsCount(t *testing.T) {
	c := claimed(t, 90000, 45000, "PAYMENT_GATEWAY")
	// The payment was reconciled in an earlier batch; only the refund is in this one.
	asMap(asMap(slice(c["settlements"])[0])["settlementAttributes"])["reconciliationId"] = "recon-earlier"
	v := Verdict{ReconciliationStatus: "AGREED"}
	paid, gw, _, _ := checkContract(&v, c, obj(t, ours), batch)
	if paid != 0 || gw != 45000 || len(v.settlements) != 1 {
		t.Errorf("paid=%v gw=%v settlements=%v, want only the refund", paid, gw, v.settlements)
	}

	none := claimed(t, 90000, 45000, "PAYMENT_GATEWAY")
	for _, st := range slice(none["settlements"]) {
		asMap(asMap(st)["settlementAttributes"])["reconciliationId"] = "recon-earlier"
	}
	v = Verdict{ReconciliationStatus: "AGREED"}
	checkContract(&v, none, obj(t, ours), batch)
	if str(v.Dispute["code"]) != "NO_SETTLEMENTS" {
		t.Errorf("no batch settlements: dispute=%v", v.Dispute)
	}
}
