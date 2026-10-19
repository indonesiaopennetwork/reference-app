package initsvc

import (
	"context"

	"github.com/ion/winroom/bpp/internal/config"
	"github.com/ion/winroom/bpp/internal/offerterms"
	"github.com/ion/winroom/bpp/internal/settlement"
)

// applySellerTerms puts this seller's terms on the contract the BAP sent in
// init: each commitment's offer gets the commission and payout term stored
// for it (offerterms.Apply), the price consideration the commission the
// order comes to, and the settlement object the BAP declared (ION
// IONSettlementReconciliation, see package settlement) this BPP's account
// (holderRole BPP, where the PJP pays the order proceeds) and the figures on
// this BPP's final price. A contract without a settlement only gets the
// terms and the commission.
func applySellerTerms(ctx context.Context, store offerterms.Store, c *InitContract, cfg *config.Config) error {
	return offerterms.ApplyTo(ctx, store, cfg.BppID, offerterms.FromConfig(cfg), c, func(contract map[string]any) {
		if attrs := settlement.Normalize(contract); attrs != nil {
			settlement.SetAccount(attrs, map[string]any{
				"holderRole":    "BPP",
				"bankCode":      cfg.SettlementBankCode,
				"accountNumber": cfg.SettlementAccountNumber,
				"accountName":   cfg.SettlementAccountName,
			})
		}
		settlement.Refresh(contract)
	})
}

func asSlice(v any) []any { s, _ := v.([]any); return s }
