# Beckn protocol extension: Reconcile

`reconcile.yaml` adds `/reconcile` and `/on_reconcile` to Beckn v2.0.0 for
settling money between a seller (BPP) and a buyer app (BAP). Core schemas
(Context, Contract, Settlement, Descriptor, Ack/Nack) are referenced from the
core spec, not redefined.

## Money model: escrow at the payment gateway

The buyer pays through the payment gateway, which **holds** the funds. The
seller is paid only when a reconciliation is agreed:

```
end of day, per BAP
BPP ── /reconcile ─────▶ BAP     "these contracts are due today; release netPayable"
BPP ◀── /on_reconcile ── BAP     AGREED → the gateway releases netPayable to the BPP (payout)
```

Who pays a refund depends on whether the contract has been settled yet:

| Refund happens | Paid by | `refund.source` | Effect on the gateway release |
|---|---|---|---|
| before the contract is reconciled (funds still held) | payment gateway, from the held funds | `PAYMENT_GATEWAY` | deducted from `netPayable` |
| after the contract is reconciled (funds already released) | BPP, from its own account | `PROVIDER` | none, reported only |

The BPP sets `settlementAttributes.refund.source` when it creates a refund
settlement. If the contract's payment settlement is not yet `AGREED` /
`ADJUSTED` in a reconciliation, the refund comes from the gateway; otherwise
the provider pays it. `source` extends RetailSettlement's `refund` object,
which allows extra fields.

With `DELIVERY` / `P1D` and a longer return window, returns made after day 1
are provider-paid. `RETURN_WINDOW_EXPIRY` as the basis keeps funds held until
returns are no longer possible.

## When a contract is due

Each settlement carries the seller's payout terms,
`settlementAttributes.settlementBasis` and `settlementWindow`. A contract is
due at `basis event time + settlementWindow`, for example one day after
delivery for `DELIVERY` / `P1D`. A contract cancelled before delivery is due
at `cancellation time + settlementWindow`.

A contract is reconciled once. A settlement added later (for example a
return's refund) makes it due again in a later batch, which counts only the
settlements not yet reconciled.

Each batch has its own `reconciliationId`: the settlement date and UTC send
time plus the BAP, for example
`recon-2026-09-30T170000Z-onix-bap-x7m2wmy7nq-et.a.run.app`. It is written
to each included settlement's `settlementAttributes.reconciliationId`, with
`reconciliationStatus` `PENDING`; both fields already exist in
RetailSettlement. `context.transactionId` identifies the batch, not any one
order.

## Messages

`/reconcile` message:

| Field | |
|---|---|
| `reconciliationId` | batch id |
| `settlementDate` | the day being reconciled |
| `contracts[]` | the due contracts, as full Contract objects with their settlement history, the same shape as in `/confirm` / `/on_update` |
| `settlementSummary` | totals for this BAP (below) |

`settlementSummary` fields:

| Field | Meaning |
|---|---|
| `totalPaidAmount` | buyer payments in this batch, held by the gateway |
| `totalGatewayRefundAmount` | refunds paid from the held funds (`PAYMENT_GATEWAY`) |
| `totalProviderRefundAmount` | refunds the BPP paid itself after an earlier release (`PROVIDER`), informational |
| `totalFeesRetained` | cancellation and restocking fees, part of `totalSettledAmount` |
| `totalSettledAmount` | `totalPaidAmount − totalGatewayRefundAmount` |
| `netPayable` | what the gateway releases to the BPP (`= totalSettledAmount`) |

The summary also carries `bapId`, `bppId`, `currency`, `settlementDate`,
`settlementBasis`, `settlementWindow` and `contractCount`.

`/on_reconcile` message:

| Field | |
|---|---|
| `reconciliationId` | the batch answered |
| `reconciliationStatus` | `PENDING` · `AGREED` · `DISPUTED` · `ADJUSTED` |
| `contracts[]` | `{contractId, reconciliationStatus, claimedAmount?, adjustedAmount?, dispute?}` |
| `settlementSummary` | the BAP's own computation (optional) |
| `payout` | the gateway release: `{status: COMMITTED | COMPLETE, amount, currency, paidAt, method, gateway}` |

## Examples

These are built from real local test orders and validate against
`reconcile.yaml` together with the core `beckn.yaml`.

**Batch 1:** `examples/reconcile.json` / `examples/on_reconcile.json`
(2026-09-30). Every refund happened before settlement, so all are
gateway-paid.

| Contract | Paid | Gateway refund | Released |
|---|---|---|---|
| returned in full (Rp 100 restocking fee ×2) | 90,000 | 89,800 | 200 |
| 1 of 2 units cancelled | 90,000 | 45,000 | 45,000 |
| no changes | 90,000 | 0 | 90,000 |
| **Summary** | **270,000** | **134,800** | **netPayable 135,200** |

**Batch 2:** `examples/reconcile-provider-refund.json` /
`examples/on_reconcile-provider-refund.json` (2026-10-01). The "no changes"
contract was settled in batch 1 (its payment settlement is `AGREED` there),
then 1 unit was returned, so the BPP refunded Rp 44,900 from its own account
(`PROVIDER`).

| Contract | Paid | Gateway refund | Provider refund | Released |
|---|---|---|---|---|
| 1 of 2 returned after settlement | 0 (reconciled in batch 1) | 0 | 44,900 | 0 |
| **Summary** | **0** | **0** | **44,900** | **netPayable 0** |
