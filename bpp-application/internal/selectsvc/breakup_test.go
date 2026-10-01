package selectsvc

import "testing"

func TestUnitBreakupCarriesTaxLine(t *testing.T) {
	attrs := dbConsiderationAttrs{
		TotalAmount: 49950,
		Breakup: []breakupItem{
			{Title: "Bolu Pisang", Amount: 45000, Type: "BASE_PRICE"},
			{Title: "PPN 12%", Amount: 4950, Type: "TAX", TaxDetail: map[string]any{
				"rate": 0.12, "included": false, "taxableBase": 41250.0, "taxRegime": "PPN", "taxCategory": "BKP",
			}},
		},
	}

	lines := unitBreakup(attrs)
	if len(lines) != 2 || lines[0].Type != "BASE_PRICE" || lines[1].Type != "TAX" {
		t.Fatalf("unitBreakup = %+v, want BASE_PRICE + TAX", lines)
	}

	tax := lines[1].scaled(3)
	if tax.Amount != 14850 {
		t.Errorf("scaled tax amount = %v, want 14850", tax.Amount)
	}
	if got := tax.TaxDetail["taxableBase"]; got != 123750.0 {
		t.Errorf("scaled taxableBase = %v, want 123750", got)
	}
	if got := tax.TaxDetail["rate"]; got != 0.12 {
		t.Errorf("scaled rate = %v, want unchanged 0.12", got)
	}
	if got := lines[1].TaxDetail["taxableBase"]; got != 41250.0 {
		t.Errorf("scaling mutated the unit line's taxableBase: %v", got)
	}
}

func TestUnitBreakupFallsBackToTotalAmount(t *testing.T) {
	// Offers priced before breakups carried tax: only totalAmount is reliable.
	lines := unitBreakup(dbConsiderationAttrs{TotalAmount: 25000})
	if len(lines) != 1 || lines[0].Type != "BASE_PRICE" || lines[0].Amount != 25000 {
		t.Fatalf("unitBreakup = %+v, want single BASE_PRICE of 25000", lines)
	}
}
