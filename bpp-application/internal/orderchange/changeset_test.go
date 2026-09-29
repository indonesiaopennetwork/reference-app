package orderchange

import (
	"encoding/json"
	"testing"
)

// A realistic seller-side performance-attributes object, matching what
// contract_performances actually stores after init.
const currentPerf = `{
  "@type": "ion:IONTradePerformance",
  "@context": "https://schema.ion.id/release1/schema/extension/trade/TradePerformance/v1/context.jsonld",
  "operatingHours": [{"timeRange": {"end": "21:00", "start": "09:00"}, "daysOfWeek": [1,2,3]}],
  "deliveryDetails": {
    "address": {
      "streetAddress": "42 MG Road, Koramangala",
      "addressLocality": "Bengaluru",
      "addressRegion": "Karnataka",
      "postalCode": "560034",
      "addressCountry": "IN"
    },
    "contact": {"name": "Arjun Sharma", "phone": "+919876543210"}
  },
  "supportedPerformanceModes": ["DELIVERY"]
}`

// proposal builds a buyer contract carrying the given deliveryDetails,
// plus the protocol noise a real proposal always has -- none of which must
// ever surface as a change.
func proposal(deliveryDetails string) json.RawMessage {
	return json.RawMessage(`{
      "id": "c1",
      "commitments": [{
        "id": "commitment-1",
        "commitmentAttributes": {
          "@type": "ion:IONTradeCommitment",
          "@context": "https://schema.ion.id/x/context.jsonld",
          "price": {"currency": "IDR", "value": 45000}
        },
        "offer": {"id": "offer-1", "descriptor": {"name": "Bolu Pisang Cokelat"}}
      }],
      "performance": [{
        "performanceAttributes": {
          "@type": "ion:IONTradePerformance",
          "operatingHours": [{"timeRange": {"end": "18:00", "start": "08:00"}}],
          "deliveryDetails": ` + deliveryDetails + `
        }
      }]
    }`)
}

func TestSummarize_OnlyChangedWhitelistedFields(t *testing.T) {
	// Buyer changed city + postal code only; everything else identical.
	p := proposal(`{
      "address": {
        "streetAddress": "42 MG Road, Koramangala",
        "addressLocality": "Pune",
        "addressRegion": "Karnataka",
        "postalCode": "411001",
        "addressCountry": "IN"
      },
      "contact": {"name": "Arjun Sharma", "phone": "+919876543210"}
    }`)

	changes := Summarize(json.RawMessage(currentPerf), p)
	if len(changes) != 2 {
		t.Fatalf("expected exactly 2 changes, got %d: %+v", len(changes), changes)
	}
	if changes[0].Label != "City" || changes[0].Before != "Bengaluru" || changes[0].After != "Pune" {
		t.Errorf("city change wrong: %+v", changes[0])
	}
	if changes[1].Label != "Postal code" || changes[1].Before != "560034" || changes[1].After != "411001" {
		t.Errorf("postal change wrong: %+v", changes[1])
	}

	// The single most important assertion in this file: none of the
	// protocol scaffolding the old screen drowned in may appear.
	for _, c := range changes {
		for _, banned := range []string{"@context", "@type", "policies", "operatingHours", "commitment", "offer"} {
			if containsFold(c.Label, banned) {
				t.Errorf("protocol noise leaked into seller view: %+v", c)
			}
		}
	}
}

func TestSummarize_NoChangesWhenIdentical(t *testing.T) {
	p := proposal(`{
      "address": {
        "streetAddress": "42 MG Road, Koramangala",
        "addressLocality": "Bengaluru",
        "addressRegion": "Karnataka",
        "postalCode": "560034",
        "addressCountry": "IN"
      },
      "contact": {"name": "Arjun Sharma", "phone": "+919876543210"}
    }`)
	if changes := Summarize(json.RawMessage(currentPerf), p); len(changes) != 0 {
		t.Fatalf("expected no changes, got %+v", changes)
	}
}

// The bug that produced the unreadable screen: current side null/absent.
// It must degrade to "here is what the buyer asked for, we hold nothing"
// -- a handful of labelled rows, not hundreds of dotted paths.
func TestSummarize_MissingCurrentSideStillReadable(t *testing.T) {
	p := proposal(`{
      "address": {"streetAddress": "9 New Road", "addressLocality": "Pune", "postalCode": "411001"},
      "contact": {"phone": "+911112223333"}
    }`)

	changes := Summarize(json.RawMessage(`null`), p)
	if len(changes) != 4 {
		t.Fatalf("expected 4 changes (phone, street, city, postal), got %d: %+v", len(changes), changes)
	}
	for _, c := range changes {
		if c.Before != "" {
			t.Errorf("expected empty Before when seller holds nothing, got %+v", c)
		}
		if c.After == "" {
			t.Errorf("expected a proposed value, got %+v", c)
		}
	}
}

func TestSummarize_AbsentProposedLeafIsNotABlankingRequest(t *testing.T) {
	// Proposal omits phone entirely -- must not be read as "clear the phone".
	p := proposal(`{
      "address": {"addressLocality": "Pune"},
      "contact": {"name": "Arjun Sharma"}
    }`)
	for _, c := range Summarize(json.RawMessage(currentPerf), p) {
		if c.Label == "Phone" {
			t.Fatalf("absent phone treated as a change: %+v", c)
		}
	}
}

func TestSummarize_NoDeliveryDetailsInProposal(t *testing.T) {
	p := json.RawMessage(`{"id":"c1","commitments":[]}`)
	if changes := Summarize(json.RawMessage(currentPerf), p); changes != nil {
		t.Fatalf("expected nil for a proposal with no delivery details, got %+v", changes)
	}
	if HasDeliveryProposal(p) {
		t.Error("HasDeliveryProposal should be false")
	}
}

func TestSummarize_NumericPostalCodeRendersWithoutDecimal(t *testing.T) {
	p := proposal(`{"address": {"postalCode": 411001}}`)
	changes := Summarize(json.RawMessage(currentPerf), p)
	if len(changes) != 1 || changes[0].After != "411001" {
		t.Fatalf("expected postal code 411001 with no decimal point, got %+v", changes)
	}
}

func TestHeadline(t *testing.T) {
	tests := []struct {
		name        string
		changes     []Change
		hasProposal bool
		want        string
	}{
		{"no proposal", nil, false, "No delivery details in this request"},
		{"nothing changed", nil, true, "No changes to delivery details"},
		{
			"address leaves collapse to one thing",
			[]Change{{Label: "City", Group: "Address"}, {Label: "Postal code", Group: "Address"}},
			true, "Delivery address",
		},
		{
			"phone alone",
			[]Change{{Label: "Phone", Group: "Contact"}},
			true, "Phone",
		},
		{
			"address plus phone",
			[]Change{{Label: "City", Group: "Address"}, {Label: "Phone", Group: "Contact"}},
			true, "Delivery address and phone",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Headline(tt.changes, tt.hasProposal); got != tt.want {
				t.Fatalf("Headline() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWarnings_SilentUntilPhaseTrackingExists(t *testing.T) {
	phase, err := NoPhaseTracking{}.PhaseFor(nil, "c1") //nolint:staticcheck // nil ctx is fine for the no-op
	if err != nil {
		t.Fatalf("no-op reporter errored: %v", err)
	}
	if phase.Tracked {
		t.Error("no-op reporter must report untracked")
	}
	addressChange := []Change{{Label: "City", Group: "Address"}}
	if w := Warnings(phase, addressChange); len(w) != 0 {
		t.Fatalf("expected no warnings while phase is untracked, got %+v", w)
	}
}

// Proves the extension point actually works: implement PhaseReporter and
// the warning appears, with no change to anything else.
func TestWarnings_AppearOncePhaseIsTracked(t *testing.T) {
	dispatched := Phase{Code: "DISPATCHED", Label: "Dispatched", Tracked: true, LeftSeller: true}

	w := Warnings(dispatched, []Change{{Label: "City", Group: "Address"}})
	if len(w) != 1 || w[0].Severity != "danger" {
		t.Fatalf("expected one danger warning for an address change after dispatch, got %+v", w)
	}

	// A phone change after dispatch is still perfectly doable.
	if w := Warnings(dispatched, []Change{{Label: "Phone", Group: "Contact"}}); len(w) != 0 {
		t.Fatalf("phone change after dispatch should not warn, got %+v", w)
	}

	// Still with the seller: no warning.
	packed := Phase{Code: "PACKED", Label: "Packed", Tracked: true, LeftSeller: false}
	if w := Warnings(packed, []Change{{Label: "City", Group: "Address"}}); len(w) != 0 {
		t.Fatalf("address change before dispatch should not warn, got %+v", w)
	}
}

func containsFold(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) &&
		stringsContainsFold(s, substr)
}

func stringsContainsFold(s, substr string) bool {
	ls, lsub := lower(s), lower(substr)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return true
		}
	}
	return false
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}

// Regression: once a request is APPROVED the live contract holds the
// requested values, so diffing against it yields nothing and the seller
// loses all record of what they approved. The before-snapshot taken at
// queue time must keep the change visible after the decision.
func TestSummarizeFromDeliveryDetails_SurvivesApproval(t *testing.T) {
	before := json.RawMessage(`{
      "address": {"streetAddress": "42 MG Road", "addressLocality": "Bengaluru", "postalCode": "560034"},
      "contact": {"name": "Arjun Sharma", "phone": "+917012345678"}
    }`)
	requested := proposal(`{
      "address": {"streetAddress": "42 MG Road", "addressLocality": "Bengaluru", "postalCode": "560034"},
      "contact": {"name": "Arjun Sharma", "phone": "+917012345679"}
    }`)

	// What the OLD code did: compare the live contract (already updated by
	// the approval) against the request. Collapses to nothing.
	liveAfterApproval := json.RawMessage(`{"deliveryDetails": {
      "address": {"streetAddress": "42 MG Road", "addressLocality": "Bengaluru", "postalCode": "560034"},
      "contact": {"name": "Arjun Sharma", "phone": "+917012345679"}
    }}`)
	if got := Summarize(liveAfterApproval, requested); len(got) != 0 {
		t.Fatalf("precondition: a live diff after approval should collapse, got %+v", got)
	}

	// What the fix does: compare the stored before-snapshot instead.
	changes := SummarizeFromDeliveryDetails(before, requested)
	if len(changes) != 1 {
		t.Fatalf("expected the approved change to remain visible, got %d: %+v", len(changes), changes)
	}
	if changes[0].Label != "Phone" || changes[0].Before != "+917012345678" || changes[0].After != "+917012345679" {
		t.Errorf("wrong change recorded: %+v", changes[0])
	}
}

func TestSummarizeFromDeliveryDetails_NoBeforeSnapshotStillLists(t *testing.T) {
	// Rows predating migration 030 have no snapshot; the requested values
	// must still be listed (with empty Before) rather than vanishing.
	changes := SummarizeFromDeliveryDetails(nil, proposal(`{"contact": {"phone": "+911112223333"}}`))
	if len(changes) != 1 || changes[0].Before != "" || changes[0].After != "+911112223333" {
		t.Fatalf("expected one change with an empty before, got %+v", changes)
	}
}
