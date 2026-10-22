# Local ION schema packs

The onix adapters (`onix-bap`, `onix-bpp`) validate each ION `@type` against
the pack published on schema.ion.id, unless a local copy exists here as
`<TypeName>/attributes.yaml` (`extendedSchema_localSchemaPath` in
`config/local-simple-{bap,bpp}.yaml`). Use this only for a pack that is changed
in ion-specs but not yet published.

| Pack | Source | Why |
|---|---|---|
| `IONTradeResource` | ion-specs branch `feat/trade-resource-crc-attributes`, `releases/release1/schema/extension/trade/TradeResource/v1/attributes.yaml` | Extended `fashion` and `electronics` attributes and `additionalAttributes[]`, used by the seller app's CRC forms. schema.ion.id's version rejects them (`fashion` and `electronics` are closed objects). |
| `IONSettlementReconciliation` | ion-specs PR #25 (`feat/recon-per-settlement`), `schema/common/Settlement/v1/attributes.yaml`, **bundled** | Per-settlement reconciliation: `orderDate`, `breakup`, `collections`, `settlementObligations`, dispute codes, and `SettlementAccount`'s `holderId` / `allocationFor` / `allocatedAmount` (inlined from the PR's Payment pack). |
| `IONTradeOffer`, `IONTradeConsideration` | ion-specs PR #24 (`feat/offer-settlement-policy`, included in #25), **bundled** | `policies.settlement` on the offer; the buyer finder fee on the offer's consideration (`buyerFinderFee`: `feeType`, `feeValue`, `appliesTo`) and its total on the contract's (`feeAmount`). |
| `ion-additional.yaml` (not a pack: the onix `auxiliaryLocations`) | ion-specs PR #25, `schema/core/api/v2.0.0/ion-additional.yaml`, **bundled** | `/reconcile` and `/on_reconcile` without `reconciliationId`, agreed per settlement, with the new summary fields. |

Refresh a copy after changing the pack in ion-specs, and delete it once the
change is published on schema.ion.id:

```bash
cp ../ion-specs/releases/release1/schema/extension/trade/TradeResource/v1/attributes.yaml \
   schemas/IONTradeResource/attributes.yaml
docker compose restart onix-bap onix-bpp
```

Then regenerate the seller app's CRC fields:
`python3 scripts/gen_crc_fields.py ../ion-specs bpp-frontend/src/crc.json`.

A **bundled** copy has the components it reaches in other ION packs inlined
from the local ion-specs tree, so they don't resolve to their published
versions; build it with `scripts/bundle_ion_pack.py` from the PR #25 checkout:

```bash
S=../ion-specs-recon/releases/release1   # feat/recon-per-settlement
python3 scripts/bundle_ion_pack.py $S schema/common/Settlement/v1/attributes.yaml schemas/IONSettlementReconciliation/attributes.yaml
python3 scripts/bundle_ion_pack.py $S schema/extension/trade/TradeOffer/v1/attributes.yaml schemas/IONTradeOffer/attributes.yaml
python3 scripts/bundle_ion_pack.py $S schema/extension/trade/TradeConsideration/v1/attributes.yaml schemas/IONTradeConsideration/attributes.yaml
python3 scripts/bundle_ion_pack.py $S schema/core/api/v2.0.0/ion-additional.yaml schemas/ion-additional.yaml
docker compose restart onix-bap onix-bpp
```

Once #24 and #25 are merged and published, delete these copies and point
`auxiliaryLocations` in `config/local-simple-{bap,bpp}.yaml` back at
`https://schema.ion.id/release1/schema/core/api/v2.0.0/ion-additional.yaml`
(`auxiliaryTypes: "url"`). The cloud adapters (onix-*/templates) validate
against the published documents only.
