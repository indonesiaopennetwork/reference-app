# ION Seller Catalog Checklist

Nov 12, 2026 · Samarth Kolapkar

## Who this is for

Use this checklist before every catalog publish on the ION network. It is for sellers and seller platforms (BPPs) that build their own catalog files, or that publish through the ION reference app.

A catalog that fails these checks may still return HTTP 200 when you publish it. Discovery Services can then reject it silently and keep showing your old catalog, so check before you publish, not after.

Reference: ION release 1 schemas (Beckn 2.0.0 core plus the ION TradeOffer, TradeResource, TradeProvider and TradeConsideration v1 packs) and NFH-014 catalog publishing.

The examples below use a made-up store, "Toko Contoh", with subscriber id `bpp.yourstore.com`. Replace these with your own values.

## 1. Before you start (one time)

These must be in place before any catalog can be found by buyers.

- [ ] Your node is registered on the ION registry with its subscriber id (for example `bpp.yourstore.com`).
- [ ] Your registry record has `catalog_index_urls` pointing at your catalog index file.
- [ ] Your signing key pair is set up. The key id in every file's `signature.keyId` matches the key in your registry record.
- [ ] The index file and all catalog files are reachable over HTTPS by Discovery Services, without login.
- [ ] Your seller account has a provider profile. In the reference app this is created at registration; without it the dashboard cannot fill the provider id.
- [ ] One person or team owns publishing and watches for rejections.

**Example: how the pieces point at each other**

```
Registry record for bpp.yourstore.com
  catalog_index_urls → https://bpp.yourstore.com/beckn/index/becknCatalogs.index.json
                                 │
                                 ▼
Index file (one per seller node)
  catalogs[ ] → entry for "bpp.yourstore.com/cat-groceries"
                  baseline → https://bpp.yourstore.com/beckn/catalogs/cat-groceries.v1.json.gz
                  changes  → https://bpp.yourstore.com/beckn/catalogs/changes/cat-groceries.v2.changes.json.gz
                  latest   → https://bpp.yourstore.com/beckn/catalogs/cat-groceries.latest.json.gz
```

Quick test: open each of these URLs in a private browser window. If any of them asks for a login or returns 404, Discovery Services cannot read it either.

## 2. Catalog structure checklist

These checks decide whether your file is valid at all. A failure here means the catalog is rejected.

**Catalog**

- [ ] `id`, `descriptor` and `provider` are present (required).
- [ ] Only these top-level keys are used: `bppId`, `bppUri`, `descriptor`, `id`, `isActive`, `offers`, `provider`, `resources`, `validity`. Any other key makes the catalog invalid.

**Provider**

- [ ] `id` and `descriptor.name` are present (required).
- [ ] Only `id`, `descriptor`, `availableAt` and `providerAttributes` are used.
- [ ] Fields with no value are left out, not sent as `null`. For example `operatingHours: null` fails; send an array or omit it.

**Resources (products)**

- [ ] Every resource has a unique `id`.
- [ ] Name, descriptions and images are in `descriptor` (`name`, `shortDesc`, `longDesc`, `mediaFile`).
- [ ] ION fields are in `resourceAttributes` with `@type: ion:IONTradeResource`.

**Offers (what the buyer pays)**

- [ ] Every offer has a unique `id` and `resourceIds` pointing at the right product.
- [ ] Each offer points at its own product, not another one. Check that the offer name and the product name match.
- [ ] The price is in `considerations[]`, not in an `offer.price` field. Beckn 2.0 has no `price` on Offer, so buyer apps will show no price.
- [ ] ION offer fields are in `offerAttributes` with `@type: ion:IONTradeOffer`. Policies go in `policies` (returns, cancellation), not in separate fields such as `returnPolicy` or `cancellationPolicy`.

A price in the right place looks like this:

```json
"considerations": [{
  "id": "offer-123-price",
  "status": { "code": "ACTIVE" },
  "considerationAttributes": {
    "@type": "ion:IONTradeConsideration",
    "@context": "https://schema.ion.id/release1/schema/extension/trade/TradeConsideration/v1/context.jsonld",
    "currency": "IDR",
    "totalAmount": 10000,
    "breakup": [{ "type": "BASE_PRICE", "title": "Product name", "amount": 10000 }]
  }
}]
```

**`@context` URLs**

- [ ] Every `@context` uses the release 1 path below. A wrong path means the ION attributes are not checked or understood.

| Object | `@type` | `@context` |
| --- | --- | --- |
| Provider | `ion:IONTradeProvider` | `https://schema.ion.id/release1/schema/extension/trade/TradeProvider/v1/context.jsonld` |
| Resource | `ion:IONTradeResource` | `https://schema.ion.id/release1/schema/extension/trade/TradeResource/v1/context.jsonld` |
| Offer | `ion:IONTradeOffer` | `https://schema.ion.id/release1/schema/extension/trade/TradeOffer/v1/context.jsonld` |
| Consideration | `ion:IONTradeConsideration` | `https://schema.ion.id/release1/schema/extension/trade/TradeConsideration/v1/context.jsonld` |

Wrong: `https://schema.ion.id/extensions/trade/offer/v1/context.jsonld` (missing `release1/schema`, lower-case pack name).

### Example: a complete, correct catalog with one product

```json
{
  "id": "bpp.yourstore.com/cat-groceries",
  "bppId": "bpp.yourstore.com",
  "bppUri": "https://bpp.yourstore.com/bpp/receiver",
  "descriptor": { "name": "Toko Contoh Groceries", "shortDesc": "Daily groceries delivered in Jakarta Selatan" },
  "isActive": true,
  "provider": {
    "id": "provider-toko-contoh",
    "descriptor": { "name": "Toko Contoh" },
    "availableAt": [{
      "address": {
        "streetAddress": "Jl. Contoh No. 1",
        "addressLocality": "Jakarta Selatan",
        "addressRegion": "DKI Jakarta",
        "postalCode": "12345",
        "addressCountry": "ID"
      },
      "geo": { "type": "Point", "coordinates": [106.8166, -6.2615] }
    }],
    "providerAttributes": {
      "@type": "ion:IONTradeProvider",
      "@context": "https://schema.ion.id/release1/schema/extension/trade/TradeProvider/v1/context.jsonld"
    }
  },
  "resources": [{
    "id": "item-keripik-balado-150g",
    "descriptor": {
      "name": "Keripik Singkong Balado 150 g",
      "shortDesc": "Crispy cassava chips with sweet-spicy balado seasoning",
      "longDesc": "Made from fresh cassava, no artificial preservatives. Net weight 150 g, ziplock pack.",
      "mediaFile": [{ "uri": "https://cdn.yourstore.com/img/keripik-balado.jpg", "mimeType": "image/jpeg" }]
    },
    "resourceAttributes": {
      "@type": "ion:IONTradeResource",
      "@context": "https://schema.ion.id/release1/schema/extension/trade/TradeResource/v1/context.jsonld",
      "crc": "TRC-food-bev",
      "availability": { "status": "IN_STOCK" },
      "identity": { "brand": "Toko Contoh", "originCountry": "ID" }
    }
  }],
  "offers": [{
    "id": "offer-keripik-balado-150g",
    "descriptor": { "name": "Keripik Singkong Balado 150 g" },
    "resourceIds": ["item-keripik-balado-150g"],
    "considerations": [{
      "id": "offer-keripik-balado-150g-price",
      "status": { "code": "ACTIVE" },
      "considerationAttributes": {
        "@type": "ion:IONTradeConsideration",
        "@context": "https://schema.ion.id/release1/schema/extension/trade/TradeConsideration/v1/context.jsonld",
        "currency": "IDR",
        "totalAmount": 15000,
        "breakup": [{ "type": "BASE_PRICE", "title": "Keripik Singkong Balado 150 g", "amount": 15000 }]
      }
    }],
    "offerAttributes": {
      "@type": "ion:IONTradeOffer",
      "@context": "https://schema.ion.id/release1/schema/extension/trade/TradeOffer/v1/context.jsonld",
      "timeToShip": "PT2H",
      "policies": {
        "returns": { "allowed": false },
        "cancellation": { "allowed": true, "window": "PT1H", "cutoffEvent": "BEFORE_PACKING" }
      }
    }
  }]
}
```

How the parts connect: the offer's `resourceIds` names the product's `id`, the offer's `considerations` carries the price, and the offer and product names match.

### Example: wrong vs right

**Price**

```json
// Wrong: "price" is not an Offer field in Beckn 2.0, buyer apps show no price
"offers": [{ "id": "offer-1", "price": { "currency": "IDR", "value": 15000 }, "resourceIds": ["item-1"] }]

// Right: price inside considerations
"offers": [{ "id": "offer-1", "resourceIds": ["item-1"],
  "considerations": [{ "id": "offer-1-price", "considerationAttributes": {
    "@type": "ion:IONTradeConsideration", "currency": "IDR", "totalAmount": 15000 } }] }]
```

**Offer pointing at the wrong product**

```json
// Wrong: the "Bolu Pisang" offer points at the chips product
{ "id": "offer-bolu-pisang", "descriptor": { "name": "Bolu Pisang Cokelat" }, "resourceIds": ["item-keripik-balado-150g"] }

// Right: each offer points at its own product
{ "id": "offer-bolu-pisang", "descriptor": { "name": "Bolu Pisang Cokelat" }, "resourceIds": ["item-bolu-pisang"] }
```

**Empty and null fields**

```json
// Wrong
"providerAttributes": { "@type": "ion:IONTradeProvider", "operatingHours": null }
"resourceAttributes": { "@type": "ion:IONTradeResource", "identity": {}, "images": null }

// Right: leave out what you do not have
"providerAttributes": { "@type": "ion:IONTradeProvider" }
"resourceAttributes": { "@type": "ion:IONTradeResource" }
```

**Policies as separate made-up fields**

```json
// Wrong: these fields are not in the ION offer pack, nobody reads them
"offerAttributes": { "returnPolicy": "7 days", "cancellationPolicy": "free", "returnable": false }

// Right
"offerAttributes": {
  "@type": "ion:IONTradeOffer",
  "policies": {
    "returns": { "allowed": false },
    "cancellation": { "allowed": true, "window": "PT1H", "cutoffEvent": "BEFORE_PACKING" }
  }
}
```

## 3. Publishing and updating

Every publish follows the same steps. The publishing library (or the reference app) does steps 3 to 6 for you; if you built your own, check each one.

1. Make your changes: add, edit or remove products and offers.
2. Run section 2 of this checklist on the changed products.
3. A new version number is set: always the previous version + 1.
4. A change file is written with only what changed (`changes/<catalogId>.v<N>.changes.json.gz`), or a new full baseline (`<catalogId>.v<N>.json.gz`) when there are many changes.
5. `<catalogId>.latest.json.gz` is rewritten with the full current catalog.
6. Your entry in the catalog index is updated and signed again; the index file is written last.
7. Confirm the publish (section 4).

**Example: changing one price**

You change the price of "Keripik Singkong Balado 150 g" from IDR 15,000 to IDR 17,000. The catalog was at version 3.

| File | Before | After |
| --- | --- | --- |
| `cat-groceries.v1.json.gz` (baseline) | full catalog at v1 | unchanged |
| `changes/cat-groceries.v4.changes.json.gz` | did not exist | new: only the changed offer, `fromVersion: 3`, `toVersion: 4` |
| `cat-groceries.latest.json.gz` | full catalog at v3 | full catalog at v4 |
| Index entry for `cat-groceries` | `latest.version: 3` | `latest.version: 4`, new change listed, signed again |

The change file holds only the difference:

```json
{
  "catalogId": "bpp.yourstore.com/cat-groceries",
  "fromVersion": 3,
  "toVersion": 4,
  "offers": {
    "upserts": [{
      "id": "offer-keripik-balado-150g",
      "resourceIds": ["item-keripik-balado-150g"],
      "considerations": [{ "id": "offer-keripik-balado-150g-price",
        "considerationAttributes": { "@type": "ion:IONTradeConsideration", "currency": "IDR", "totalAmount": 17000 } }]
    }]
  },
  "signature": { "keyId": "<your key id>", "canonicalization": "JCS", "value": "<signature>" }
}
```

**Rules you must never break**

- [ ] Versions only go up. Never restore a backup that lowers the version: crawlers treat a lower version as a possible rollback attack and refuse it.
- [ ] Never delete or rename a change file that is still listed in your index. A crawler needs every step of the chain (v10 to v11 to v12).
- [ ] Keep old change files available after a new baseline, for at least one `next_update` period. Some crawlers are still behind.
- [ ] Sign the exact bytes you publish, using JCS canonical JSON. A JSON library that reorders keys or changes number formats breaks the digest, and the file is discarded as tampered.
- [ ] Every catalog you publish has an entry in the index. Files on your server that are not in the index are never found.

Example of breaking the version rule:

```
Mon  publish v15            crawlers now hold v15
Tue  server restored from Sunday's backup → your catalog says v14
Tue  publish v14            crawlers see 15 → 14, refuse it as a possible rollback
                            buyers keep seeing Monday's catalog
Fix: publish as v16 (higher than anything ever published)
```

Example of breaking the chain:

```
Index lists: v10→v11, v11→v12, v12→v13
You delete changes/...v12.changes.json.gz to save space
A crawler at v11 cannot get to v12, so it is stuck at v11
```

**Pausing and removing a catalog**

| You want to | Do this | Do not |
| --- | --- | --- |
| Stop sales for a while (holiday, stock-take) | Set the catalog's `isActive` to `false` and publish; set it back to `true` to resume | Delete the files |
| Stop one product | Mark it `OUT_OF_STOCK` or `DISCONTINUED`, or remove it, and publish | Leave it `IN_STOCK` |
| Close the catalog for good | Retire it, so the index keeps a tombstone entry with `retiredAt` | Just delete its index entry |

Example: closing for Lebaran from 30 March to 6 April.

```
29 Mar  set "isActive": false → publish   (catalog hidden from search)
7 Apr   set "isActive": true  → publish   (catalog back in search)
```

## 4. After publishing

An HTTP 200 only means your server accepted the file. Check that the network accepted it too.

- [ ] Open your index file and confirm your catalog's entry shows the new `entryVersion` and `latest.version`.
- [ ] Open the `latest` file from that entry and confirm your change is in it.
- [ ] Search for one changed product from a buyer app after the next crawl. If the old data still shows, assume the catalog was rejected and recheck sections 2 and 3.
- [ ] Check prices and images on that product page, not only in search results.

**Example: what to look for in your index entry after the price change**

```json
{
  "catalogId": "bpp.yourstore.com/cat-groceries",
  "entryVersion": 4,
  "isActive": true,
  "baseline": { "version": 1, "url": "https://bpp.yourstore.com/beckn/catalogs/cat-groceries.v1.json.gz" },
  "changes": [
    { "fromVersion": 1, "toVersion": 2, "url": ".../changes/cat-groceries.v2.changes.json.gz" },
    { "fromVersion": 2, "toVersion": 3, "url": ".../changes/cat-groceries.v3.changes.json.gz" },
    { "fromVersion": 3, "toVersion": 4, "url": ".../changes/cat-groceries.v4.changes.json.gz" }
  ],
  "latest": { "version": 4, "url": "https://bpp.yourstore.com/beckn/catalogs/cat-groceries.latest.json.gz" },
  "signature": { "keyId": "<your key id>", "value": "<signature>" }
}
```

Check: `latest.version` is 4, the last change is `3 → 4`, and every listed URL opens. Then open the latest file and search for `17000`.

**Common mistakes**

| Mistake | What happens | Fix |
| --- | --- | --- |
| `@context` uses `extensions/trade/offer/v1` | ION attributes are not checked or understood | Use the release 1 URLs in section 2 |
| Price sent as `offer.price` | Buyer apps show no price | Put it in `considerations[]` |
| An offer's `resourceIds` points at a different product | Buyers see one product with another product's name and price | Each offer points at its own product |
| `operatingHours: null`, `images: null`, `identity: {}` | Schema error, or empty data | Omit fields with no value |
| Version went down after a restore | The whole catalog update is refused | Publish with a version above the highest ever published |
| Change files on the server but no index entry | Catalog is never found | Make sure every catalog is in the index |
