# ION network extension: additional endpoints

The reconciliation endpoints, `/reconcile` and `/on_reconcile`, are ION
Release 1 `core/api/v2.0.0/ion-additional.yaml` in
[ion-specs](https://github.com/indonesiaopennetwork/ion-specs): every ION
endpoint that Beckn v2.0.0 does not define. Their settlement object,
`IONSettlementReconciliation`, is the ion-specs `common/Settlement/v1`
attribute pack.

This repository keeps no copy of either. Both ONIX adapters load the API from
ion-specs `main`
(`auxiliaryTypes: "url"`, `auxiliaryLocations` in the ONIX configs):

```text
https://raw.githubusercontent.com/indonesiaopennetwork/ion-specs/refs/heads/main/releases/release1/schema/core/api/v2.0.0/ion-additional.yaml
```

and fetch the settlement pack, like every other ION `@context` type, from
schema.ion.id. A change to either belongs in ion-specs; restart the adapters
to pick it up.

## Money model: the PJP holds the payment

The buyer pays into the PJP (Penyelenggara Jasa Pembayaran), which **holds**
the funds; neither the BAP nor the BPP receives them. Once both participants
agree a contract's figures, SEAM (Settlement Assurance Mechanism) has the PJP
pay it out:

| Recipient | Receives |
|---|---|
| BPP | `netPayable` = `orderAmount − commission − withholdingAmount − refundAmount` |
| BAP | `commission` — its finder fee |
| Buyer | `refundAmount` — refunds made before the payout |
| Stays at the PJP | `withholdingAmount` (0 in this app) |

No tax is withheld from payouts. SEAM reads the agreement from the message
log; payout status comes back through the PJP's webhook, outside this API.

## One settlement object, from init to reconcile

The order's settlement is one `IONSettlementReconciliation` object, the
`settlementAttributes` of the contract's payment settlement. It is built up
through the order and sent as it stands in `/reconcile`:

| Step | Who | Adds or changes |
|---|---|---|
| `init` | BAP | `finderFee` terms (env `FINDER_FEE_*`, default 3% of the item total), its `settlementAccounts` entry (`holderRole: BAP`), `method` / `paymentRail` QRIS, `collectedBy: BAP`, `timing`, `status: PENDING`, figures estimated on the quoted price |
| `on_init` | BPP | `settlementBasis`, `settlementWindow` (env `SETTLEMENT_BASIS`, `SETTLEMENT_WINDOW`), `finderFee.feeAmount` on its final price, its account (`holderRole: BPP`), the figures; the BAP checks the fee and logs a mismatch |
| `confirm` | BAP | `status: PAID`, `paidAt`, `gatewayRef`; `orderAmount` is the amount paid |
| `on_cancel`, `on_update` | BPP | `finderFee.feeAmount`, `commission`, `refundAmount`, `netPayable` follow cancellations and returns |
| `/reconcile`, `/on_reconcile` | both | each participant's own figures, with `accord` in the answer |

Until reconciliation the figures are estimates. Both apps keep this logic in
one package, `internal/settlement` (the two copies are kept identical). Refund
settlements stay ION TradeSettlement records.

The settlement object, with the payment fields (`status`, `paidAt`,
`gatewayRef`, `method`, `paymentRail`, `paymentGatewayProvider`,
`methodDetail`, `timing`), comes from ion-specs PR #18 (`feat/core-reconcile`),
which is not yet merged: on ion-specs `main` the payment is an `IONPayment` and
the terms are on `IONTradeContract`. Both apps still accept a contract that
declares an `IONPayment` and convert it.

## When a contract is due

A contract is due at `basis event + settlementWindow`: delivery for
`AFTER_DELIVERY` / `AFTER_SHIPMENT` (shipment isn't recorded separately),
confirmation for `ON_CONFIRMATION`, the close of the longest return window for
`AFTER_RETURN_WINDOW`. A contract cancelled before delivery is due at the
cancellation plus the window.

A contract sent before it is due on the answering side is answered
`accord: false` with the dispute code `SETTLEMENT_WINDOW_NOT_REACHED` and the
reason (when it falls due, or that the order isn't delivered yet). The batch
is then `PENDING` -- unless another contract in it is disputed, which makes it
`DISPUTED` -- and no contract in it is agreed, not even the ones that match:
all of them are sent again, the early ones once due.
The BAP always checks this against the real settlement window;
`RECONCILE_WAIT_FOR_SETTLEMENT_WINDOW=false` only lets a participant send
early.

Each contract is reconciled **once**, through its payment settlement, together
with the refunds made before the payout. A refund the BPP makes after the
payout (`refund.source: PROVIDER`) is outside reconciliation in ION Release 1:
the BPP pays the buyer itself and the dashboards report it. Sellers of
returnable goods can avoid this with `AFTER_RETURN_WINDOW`.

## Messages

Either participant sends `/reconcile`; the other answers `/on_reconcile`.
`context.bapId` / `bppId` are always the pair; `senderId` / `receiverId` say who
sent it. `reconciliationId` is `recon-<date>T<HHMMSS>Z-<senderId>`.

`message.contracts[]` are Beckn Contract snapshots: the contract `id`, the
minimal `commitments` Beckn requires, and the payment settlement (its `id`)
whose `settlementAttributes` is an `IONSettlementReconciliation`:

| Field | |
|---|---|
| `collectedBy`, `currency`, `status`, `paidAt`, `gatewayRef`, ... | the payment |
| `settlementBasis`, `settlementWindow`, `finderFee` | the agreed terms |
| `orderAmount`, `commission`, `withholdingAmount`, `refundAmount`, `netPayable` | `{value}`, plus `diffValue` in a disagreeing answer |
| `settlementAccounts[]` | the BPP's always; the BAP's when `commission` > 0 |
| `accord` | answer only: `true` agrees, `false` disagrees |
| `dueDate` / `dispute` | answer only: when `accord` is `true` / `false` |

A batch is agreed as a whole or not at all. The answer's
`reconciliationStatus` is `AGREED` only when every contract in it is due and
has `accord: true`; then all of them are paid out. Otherwise no contract in it
is agreed, and it is `DISPUTED` (a contract that was due is disputed) or
`PENDING` (the others match, but a contract was sent before its window). On the reconciliation pages the operator ticks
what the next `/reconcile` carries -- any contracts that are due or disputed,
for one counterparty at a time, filtered by the contracts' creation date --
and they go out as a new batch; a disputed contract moves to it from its old
batch. (Called without a selection, Send resends the disputed batch the
participant sent, in full under the same `reconciliationId`, or else sends
everything due.) The answer's `settlementSummary` counts the contracts
(`agreedCount`, `disputedCount`). Escalation to `/raise` comes later.

The finder fee is computed from the contract: a `PERCENT` fee applies to the
`BASE_PRICE` lines of the price considerations still in force (`ITEM_TOTAL`) or
to the order amount (`ORDER_TOTAL`); a fully cancelled order has no finder fee.
The result is `finderFee.feeAmount`, and a settlement's `commission` equals it,
so the two apps compare the fee each copy of the contract states. A contract
agreed before `feeAmount` existed has it computed by the same rule.

## Examples

Built by the apps' own code and validated against ion-specs `ion-additional.yaml` and
`ion-beckn.yaml`: `examples/reconcile.json` (the BPP's batch for
2026-09-30) and `examples/on_reconcile.json` (the BAP agrees with every
contract). Each order paid 90,000 (80,000 of items plus PPN) with a 3% finder
fee on the item total:

| Contract | Paid | Refund | Finder fee (BAP) | Net payable (BPP) |
|---|---|---|---|---|
| no changes | 90,000 | 0 | 2,400 | 87,600 |
| 1 of 2 units cancelled | 90,000 | 45,000 | 1,200 | 43,800 |
| returned in full (Rp 200 restocking fee) | 90,000 | 89,800 | 0 | 200 |
| **Summary** | **270,000** | **134,800** | **3,600** | **131,600** |
