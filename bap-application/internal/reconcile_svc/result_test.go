package reconcilesvc

import (
	"encoding/json"
	"testing"
)

func TestBatchResults(t *testing.T) {
	contract := func(id string, commission float64, extra string) string {
		return `{"id":"` + id + `","settlements":[{"id":"s-` + id + `","settlementAttributes":{` +
			`"orderAmount":{"value":1000},"commission":{"value":` + jsonNum(commission) + `},` +
			`"netPayable":{"value":` + jsonNum(1000-commission) + `}` + extra + `}}]}`
	}
	request := json.RawMessage(`{"message":{"contracts":[` + contract("a", 30, "") + `,` + contract("b", 30, "") + `]}}`)
	response := json.RawMessage(`{"message":{"contracts":[` +
		contract("a", 30, `,"accord":true`) + `,` +
		contract("b", 50, `,"accord":false,"dispute":{"code":"COMMISSION_MISMATCH","description":"commission 50 here, 30 received"}`) + `]}}`)

	for _, outbound := range []bool{true, false} {
		got := batchResults(request, response, outbound)
		if a := got["a"]; a == nil || !a.Accord || len(a.Diffs) != 0 {
			t.Fatalf("outbound=%v: a = %+v, want accord without diffs", outbound, a)
		}
		b := got["b"]
		if b == nil || b.Accord || b.Code != "COMMISSION_MISMATCH" || len(b.Diffs) != 2 {
			t.Fatalf("outbound=%v: b = %+v, want a commission and net payable dispute", outbound, b)
		}
		want := Diff{Field: "commission", Ours: 30, Theirs: 50, Diff: 20}
		if !outbound {
			want = Diff{Field: "commission", Ours: 50, Theirs: 30, Diff: -20}
		}
		if b.Diffs[0] != want {
			t.Errorf("outbound=%v: commission diff = %+v, want %+v", outbound, b.Diffs[0], want)
		}
	}
}

func jsonNum(f float64) string { b, _ := json.Marshal(f); return string(b) }
