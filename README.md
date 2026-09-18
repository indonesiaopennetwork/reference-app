# ION Reference App

A reference implementation for a **Network Participant (NP)** on
**ION (Indonesia Open Network)** — covering both roles an NP can take on the
network: **BAP** (buyer-side) and **BPP** (seller-side). It demonstrates a
complete, working integration end-to-end: registration, signed
network-protocol messaging via the Beckn network adapters (`onix`), and both
the buyer and seller applications built on top of it.

For background on ION itself, see
[github.com/indonesiaopennetwork](https://github.com/indonesiaopennetwork).

## What you can do with it

### Register as a Network Participant

- **Self-service registration/login via OIDC (Zitadel)** — no manual
  approval needed; you're active as soon as you complete the login flow.
- **One identity works for either role, or both** — there's no separate
  signup for buying versus selling.

### As a BAP (buyer)

- **Search a seller's published catalog** — results come from a live
  discover call against the network, not a local cache.
- **Select items and check out with billing details** — this kicks off the
  select → init → confirm order lifecycle with the seller.
- **Track an order through to confirmation** — status updates arrive
  asynchronously as the seller's app responds.
- **View order history and any past order's detail** — every order shown is
  scoped to your own logged-in identity.
- **Cancel an order** — a cancellation request is sent to the seller the
  same way the original order was.
- **Rate a completed order** — feedback is recorded against that
  transaction once it's marked complete.

### As a BPP (seller)

- **Overview** — stats and insights at a glance: active orders, pending
  orders, and today's order volume in one view.
- **Catalogs** — build and publish your catalog to the network; this is
  what makes you discoverable to other participants searching for products.
- **Inventory** — manage your resources and offers, including what you're
  selling and at what price.
- **Orders** — manage incoming contracts and respond to buyer actions,
  scoped to your own seller profile.

See [TECHNICAL_ARTIFACT.md](TECHNICAL_ARTIFACT.md) for exactly how a request
moves through the system for each of these.

| Service | What it is | Source |
|---|---|---|
| `bap` | Buyer-side backend (Go) | `bap-application/` |
| `bpp` | Seller-side backend (Go) | `bpp-application/` |
| `bap-frontend` | Buyer web UI | `bap-frontend/` |
| `bpp-frontend` | Seller/admin dashboard | `bpp-frontend/` |
| `landing-page` | Entry point + NP registration/login | `landing-page/` |
| `onix-bap` / `onix-bpp` | Beckn network adapters — sign, route, and validate traffic for `bap`/`bpp` | `onix-bap/`, `onix-bpp/` |

## Docs map

- **Stack, architecture, and request flow** (what's built with what, how a
  request moves from `bap-frontend` through to the network and back) →
  [TECHNICAL_ARTIFACT.md](TECHNICAL_ARTIFACT.md)
- **Local development** (Docker Compose, fastest way to get a working
  end-to-end system) → [INFRA_SETUP.md](INFRA_SETUP.md#1-local-development-docker-compose)
- **Deploy to your own fresh GCP project** →
  [terraform/README.md](terraform/README.md#deploy-to-your-fresh-gcp-project)
