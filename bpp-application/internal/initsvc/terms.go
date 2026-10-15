package initsvc

import (
	"encoding/json"

	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/settlement"
)

// applySellerTerms answers the settlement object the BAP declared in init
// (ION IONSettlementReconciliation, see package settlement): it sets this
// seller's settlementBasis / settlementWindow, adds this BPP's account
// (holderRole BPP, where the PJP pays the order proceeds), and works out the
// finder fee and the figures on this BPP's final price. A contract without a
// settlement is left unchanged.
func applySellerTerms(c *InitContract, cfg *config.Config) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	var contract map[string]any
	if err := json.Unmarshal(raw, &contract); err != nil {
		return err
	}
	attrs := settlement.Normalize(contract)
	if attrs == nil {
		return nil
	}
	attrs["settlementBasis"] = cfg.SettlementBasis
	attrs["settlementWindow"] = cfg.SettlementWindow
	settlement.SetAccount(attrs, map[string]any{
		"holderRole":    "BPP",
		"bankCode":      cfg.SettlementBankCode,
		"accountNumber": cfg.SettlementAccountNumber,
		"accountName":   cfg.SettlementAccountName,
	})
	settlement.Refresh(contract)

	settlements := make([]json.RawMessage, 0, len(asSlice(contract["settlements"])))
	for _, st := range asSlice(contract["settlements"]) {
		b, err := json.Marshal(st)
		if err != nil {
			return err
		}
		settlements = append(settlements, b)
	}
	c.Settlements = settlements
	return nil
}

func asSlice(v any) []any { s, _ := v.([]any); return s }
