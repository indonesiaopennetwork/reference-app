# Local ION schema packs

The onix adapters (`onix-bap`, `onix-bpp`) validate each ION `@type` against
the pack published on schema.ion.id, unless a local copy exists here as
`<TypeName>/attributes.yaml` (`extendedSchema_localSchemaPath` in
`config/local-simple-{bap,bpp}.yaml`). Use this only for a pack that is changed
in ion-specs but not yet published.

| Pack | Source | Why |
|---|---|---|
| `IONTradeResource` | ion-specs branch `feat/trade-resource-crc-attributes`, `releases/release1/schema/extension/trade/TradeResource/v1/attributes.yaml` | Extended `fashion` and `electronics` attributes and `additionalAttributes[]`, used by the seller app's CRC forms. schema.ion.id's version rejects them (`fashion` and `electronics` are closed objects). |

Refresh a copy after changing the pack in ion-specs, and delete it once the
change is published on schema.ion.id:

```bash
cp ../ion-specs/releases/release1/schema/extension/trade/TradeResource/v1/attributes.yaml \
   schemas/IONTradeResource/attributes.yaml
docker compose restart onix-bap onix-bpp
```

Then regenerate the seller app's CRC fields:
`python3 scripts/gen_crc_fields.py ../ion-specs bpp-frontend/src/crc.json`.
