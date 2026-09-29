package orderchange

import (
	"encoding/json"
	"testing"
)

// The decision must land exactly where IONTradeContract declares itself
// attachable (Contract.contractAttributes) and must carry the JSON-LD type,
// or onix has no schema to validate it against and rejects the callback.
func TestAttachDecision_LandsInContractAttributesWithType(t *testing.T) {
	contract := json.RawMessage(`{"id":"c1","performance":[{"performanceAttributes":{}}]}`)
	out := AttachDecision(contract, RejectDecision("ALREADY_DISPATCHED", "courier already collected it"))

	var got struct {
		ID                 string `json:"id"`
		ContractAttributes struct {
			Type           string `json:"@type"`
			Context        string `json:"@context"`
			UpdateDecision struct {
				Outcome string `json:"outcome"`
				Reason  struct {
					Code     string `json:"code"`
					Name     string `json:"name"`
					LongDesc string `json:"longDesc"`
				} `json:"reason"`
				DecidedAt string `json:"decidedAt"`
			} `json:"updateDecision"`
		} `json:"contractAttributes"`
		Performance []any `json:"performance"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	if got.ID != "c1" || len(got.Performance) != 1 {
		t.Error("attaching the decision must not disturb the rest of the contract")
	}
	if got.ContractAttributes.Type != "ion:IONTradeContract" {
		t.Errorf("@type missing/wrong: %q", got.ContractAttributes.Type)
	}
	if got.ContractAttributes.Context == "" {
		t.Error("@context must be set so the extension resolves")
	}
	d := got.ContractAttributes.UpdateDecision
	if d.Outcome != OutcomeRejected {
		t.Errorf("outcome = %q", d.Outcome)
	}
	if d.Reason.Code != "ALREADY_DISPATCHED" || d.Reason.Name != "Already dispatched" {
		t.Errorf("reason code/label wrong: %+v", d.Reason)
	}
	if d.Reason.LongDesc != "courier already collected it" {
		t.Errorf("seller's free text lost: %q", d.Reason.LongDesc)
	}
	if d.DecidedAt == "" {
		t.Error("decidedAt should be stamped automatically")
	}
}

// The common real case: contract_attributes is null in this app, so the
// decision still needs somewhere valid to live.
func TestAttachDecision_NullAttributesGetsAValidObject(t *testing.T) {
	out := AttachDecision(json.RawMessage(`{"id":"c1","contractAttributes":null}`), ApproveDecision())
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	attrs, ok := got["contractAttributes"].(map[string]any)
	if !ok {
		t.Fatalf("contractAttributes should be an object, got %T", got["contractAttributes"])
	}
	if attrs["@type"] != "ion:IONTradeContract" {
		t.Errorf("@type not set on a previously-null bag: %v", attrs["@type"])
	}
	d, ok := attrs["updateDecision"].(map[string]any)
	if !ok || d["outcome"] != OutcomeApproved {
		t.Errorf("approval not recorded: %v", attrs["updateDecision"])
	}
	if _, hasReason := d["reason"]; hasReason {
		t.Error("an approval needs no reason attached")
	}
}

func TestAttachDecision_PreservesExistingAttributes(t *testing.T) {
	contract := json.RawMessage(`{"id":"c1","contractAttributes":{"@type":"ion:IONTradeContract","purchaseOrderReference":"PO-1"}}`)
	out := AttachDecision(contract, ApproveDecision())
	var got struct {
		ContractAttributes map[string]any `json:"contractAttributes"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.ContractAttributes["purchaseOrderReference"] != "PO-1" {
		t.Error("existing attributes must survive")
	}
	if _, ok := got.ContractAttributes["updateDecision"]; !ok {
		t.Error("decision should have been added alongside")
	}
}

// An unknown code must degrade to OTHER rather than shipping a code the
// buyer's app cannot label.
func TestRejectDecision_UnknownCodeFallsBackToOther(t *testing.T) {
	d := RejectDecision("NOT_A_REAL_CODE", "some note")
	if d.Reason.Code != "OTHER" || d.Reason.Name != RejectReason["OTHER"] {
		t.Fatalf("expected fallback to OTHER, got %+v", d.Reason)
	}
	if d.Reason.LongDesc != "some note" {
		t.Error("the seller's note should survive the fallback")
	}
}

func TestRejectDecision_EmptyCodeFallsBackToOther(t *testing.T) {
	if d := RejectDecision("", ""); d.Reason.Code != "OTHER" {
		t.Fatalf("expected OTHER for an empty code, got %q", d.Reason.Code)
	}
}
