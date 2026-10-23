import { useState } from 'react'
import { ChevronDown } from 'lucide-react'
import { useI18n } from './i18n.jsx'

// Shared with bpp-frontend/src/ReconDetails.jsx: keep the two copies in step.
//
// What a /reconcile and its /on_reconcile say about each settlement and the
// batch (ION Release 1 ion-additional.yaml, common/Settlement/v1), read from
// the messages themselves: agreement is per settlement; a settlement with
// accord false is disputed, or pending for SETTLEMENT_WINDOW_NOT_REACHED and
// AWAITING_INFORMATION. self is 'BAP' or 'BPP': whose app this is, for
// labelling "you" and "the other side".

const rupiah = n => (n == null ? '—' : `Rp ${Number(n).toLocaleString('id-ID')}`)
const parse = v => {
  if (typeof v !== 'string') return v
  try { return JSON.parse(v) } catch { return v }
}

const PENDING_CODES = ['SETTLEMENT_WINDOW_NOT_REACHED', 'AWAITING_INFORMATION']
const OUTCOME_CLS = {
  AGREED: 'bg-emerald-500/15 text-emerald-300 border-emerald-500/30',
  DISPUTED: 'bg-rose-500/15 text-rose-300 border-rose-500/30',
  PENDING: 'bg-amber-500/15 text-amber-300 border-amber-500/30',
}
const OUTCOME_LABEL = { AGREED: 'Agreed', DISPUTED: 'Disputed', PENDING: 'Pending' }

// A settlement's outcome in an answer: AGREED, DISPUTED or PENDING; null
// before it is answered.
function outcomeOf(bag) {
  if (!bag || typeof bag.accord !== 'boolean') return null
  if (bag.accord) return 'AGREED'
  return PENDING_CODES.includes(bag.dispute?.code) ? 'PENDING' : 'DISPUTED'
}

export function Outcome({ outcome }) {
  const { t } = useI18n()
  if (!outcome) return <span className="text-slate-500">—</span>
  return (
    <span className={`inline-flex px-2 py-0.5 rounded-full text-[11px] font-bold border ${OUTCOME_CLS[outcome]}`}>
      {t(OUTCOME_LABEL[outcome])}
    </span>
  )
}

const COLLECTION_METHOD = { PREPAID: 'Prepaid', COD: 'Cash on delivery', BANK_TRANSFER: 'Bank transfer', CREDIT: 'Credit', MIXED: 'Mixed', OTHER: 'Other' }
const COLLECTION_STATUS = { NOT_COLLECTED: 'not collected', PARTIALLY_COLLECTED: 'partly collected', COLLECTED: 'collected', REVERSED: 'reversed', UNKNOWN: 'unknown' }
const BREAKUP_CODE = {
  PPN: 'PPN (VAT)', PPH: 'PPh (income tax)', PAYMENT_FEE: 'Payment fee', SELLER_APP_FEE: 'Seller app fee', NETWORK_FEE: 'Network fee',
  LOGISTICS: 'Logistics', SELLER_FUNDED_DISCOUNT: 'Seller-funded discount', BUYER_APP_SUBSIDY: 'Buyer app subsidy', PENALTY: 'Penalty',
  COMPENSATION: 'Compensation', CANCELLATION: 'Cancellation', RETURN: 'Return', FORCED_REFUND: 'Forced refund', CHARGEBACK: 'Chargeback',
  CORRECTION: 'Correction', OFFSET: 'Offset', ROUNDING: 'Rounding', OTHER: 'Other',
}
const INCLUDED_IN = { COMMISSION: 'in commission', WITHHOLDING: 'in withholding', REFUND: 'in refund', ADDITIONAL: 'additional' }
const FIGURES = [['orderAmount', 'Order amount'], ['commission', 'Commission'], ['withholdingAmount', 'Withholding'], ['refundAmount', 'Refund'], ['netPayable', 'Net payable']]
const mask = n => (n ? `••${String(n).slice(-4)}` : '—')

function Row({ label, children }) {
  return (
    <div className="flex gap-3 py-1">
      <span className="w-32 shrink-0 text-[10px] uppercase tracking-widest text-slate-500 pt-0.5">{label}</span>
      <div className="min-w-0 flex-1 text-slate-300 break-words">{children}</div>
    </div>
  )
}

// SettlementDetails shows one settlement's attributes: dates, figures (with
// the other side's diffValue), collections, breakup, payout accounts and
// their allocations, obligations, and the dispute.
export function SettlementDetails({ bag }) {
  const { t } = useI18n()
  if (!bag) return null
  const dispute = bag.dispute
  return (
    <div className="text-xs space-y-0.5">
      <Row label={t('Order date')}>{bag.orderDate || '—'}</Row>
      <Row label={t('Due date')}>{bag.dueDate || '—'}</Row>
      <Row label={t('Figures')}>
        <div className="flex flex-wrap gap-x-4 gap-y-1">
          {FIGURES.map(([k, label]) => bag[k] && (
            <span key={k} className="whitespace-nowrap">
              <span className="text-slate-500">{t(label)} </span>
              <span className="tabular-nums text-slate-200">{rupiah(bag[k].value)}</span>
              {bag[k].diffValue != null && bag[k].diffValue !== 0 && (
                <span className="ml-1 font-semibold text-rose-300" title={t('Answering side minus the figure it answers')}>
                  ({bag[k].diffValue > 0 ? '+' : '−'}{rupiah(Math.abs(bag[k].diffValue))})
                </span>
              )}
            </span>
          ))}
          {bag.recoveryAmount && <span className="whitespace-nowrap text-amber-300">{t('Recovery')} {rupiah(bag.recoveryAmount.value)}</span>}
        </div>
      </Row>
      {(bag.collections || []).length > 0 && (
        <Row label={t('Collections')}>
          {bag.collections.map((c, i) => (
            <div key={i}>
              {t(COLLECTION_METHOD[c.method] || c.method)} · {t('by {who}', { who: c.collectorId || c.collectorRole || '—' })} · {t(COLLECTION_STATUS[c.status] || c.status)}
              {c.reportedCollectedAmount != null && <> · <span className="tabular-nums">{rupiah(c.reportedCollectedAmount)}</span></>}
              {c.referenceId && <span className="ml-1 font-mono text-[10px] text-slate-500 break-all">{c.referenceId}</span>}
              {c.dueDate && <span className="ml-1 text-slate-500">{t('due {date}', { date: c.dueDate })}</span>}
            </div>
          ))}
        </Row>
      )}
      {(bag.breakup || []).length > 0 && (
        <Row label={t('Breakup')}>
          {bag.breakup.map((b, i) => (
            <div key={i}>
              {t(BREAKUP_CODE[b.code] || b.code)} <span className="tabular-nums text-slate-200">{rupiah(b.amount)}</span>
              <span className="ml-1 text-slate-500">{t(INCLUDED_IN[b.includedIn] || b.includedIn)}</span>
              {b.effectOnNetPayable != null && <span className="ml-1 tabular-nums">({b.effectOnNetPayable > 0 ? '+' : '−'}{rupiah(Math.abs(b.effectOnNetPayable))} {t('to net payable')})</span>}
              {b.referenceId && <span className="ml-1 font-mono text-[10px] text-slate-500">{b.referenceId}</span>}
            </div>
          ))}
        </Row>
      )}
      {(bag.settlementAccounts || []).length > 0 && (
        <Row label={t('Payout accounts')}>
          {bag.settlementAccounts.map((a, i) => (
            <div key={i} className="break-words">
              <span className="font-semibold text-slate-200">{a.holderRole}</span>
              {a.holderId && <span className="ml-1 font-mono text-[10px] text-slate-500 break-all">{a.holderId}</span>}
              <span className="ml-2">{a.bankCode || '—'} {mask(a.accountNumber)}</span>
              {a.accountName && <span className="ml-1 text-slate-500">{a.accountName}</span>}
              {a.allocatedAmount != null && (
                <span className="ml-2 tabular-nums text-emerald-300">{rupiah(a.allocatedAmount)} <span className="text-slate-500">{t('of the {who} share', { who: a.allocationFor })}</span></span>
              )}
            </div>
          ))}
        </Row>
      )}
      {(bag.settlementObligations || []).length > 0 && (
        <Row label={t('Obligations')}>
          {bag.settlementObligations.map((o, i) => (
            <div key={i}>
              <span className="font-mono text-[11px]">{o.payerId}</span> → <span className="font-mono text-[11px]">{o.payeeId}</span>
              <span className="ml-2 tabular-nums text-slate-200">{rupiah(o.amount)}</span>
              {o.purpose && <span className="ml-1 text-slate-500">{o.purpose}</span>}
            </div>
          ))}
        </Row>
      )}
      {bag.relatedSettlementId && <Row label={t('Corrects')}>{bag.adjustmentType} <span className="font-mono">{bag.relatedSettlementId}</span></Row>}
      {dispute && (
        <Row label={PENDING_CODES.includes(dispute.code) ? t('Pending') : t('Dispute')}>
          <span className={`font-semibold ${PENDING_CODES.includes(dispute.code) ? 'text-amber-300' : 'text-rose-300'}`}>{dispute.code}</span>
          {dispute.field && <span className="ml-1 text-slate-500">{t('on {field}', { field: dispute.field })}</span>}
          {dispute.description && <div className="text-slate-400">{dispute.description}</div>}
        </Row>
      )}
    </div>
  )
}

// bags maps each contract id of a message to its first settlement's id and
// attributes.
function bags(payload) {
  const out = {}
  for (const c of parse(payload)?.message?.contracts || []) {
    const st = (c.settlements || [])[0] || {}
    out[c.id] = { settlementId: st.id, bag: st.settlementAttributes }
  }
  return out
}

// BatchSettlements lists a batch's settlements -- the /reconcile's figures
// and, once answered, each settlement's outcome -- each expandable to its
// details: the sender's and the answer's.
export function BatchSettlements({ request, response, sent }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(null)
  const req = bags(request), resp = bags(response)
  const ids = Object.keys(req).length ? Object.keys(req) : Object.keys(resp)
  if (!ids.length) return null
  return (
    <div className="overflow-x-auto rounded-xl border border-white/[0.07]">
      <table className="w-full text-xs">
        <thead>
          <tr className="text-[10px] uppercase tracking-widest text-slate-500 border-b border-white/[0.06]">
            <th className="text-left px-3 py-2">{t('Contract')}</th>
            <th className="text-left px-3 py-2">{t('Order date')}</th>
            <th className="text-left px-3 py-2">{t('Outcome')}</th>
            <th className="text-right px-3 py-2">{t('Commission')}</th>
            <th className="text-right px-3 py-2">{t('Net payable')}</th>
            <th className="text-left px-3 py-2">{t('Reason')}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-white/[0.04]">
          {ids.map(id => {
            const q = req[id]?.bag, a = resp[id]?.bag
            const shown = a || q || {}
            const expanded = open === id
            return [
              <tr key={id} onClick={() => setOpen(expanded ? null : id)} className="cursor-pointer hover:bg-white/[0.02]">
                <td className="px-3 py-2 font-mono text-slate-400 whitespace-nowrap" title={id}>
                  <ChevronDown size={12} className={`inline mr-1 transition-transform ${expanded ? 'rotate-180 text-cyan-400' : 'text-slate-600'}`} />
                  {String(id).slice(0, 8)}…
                </td>
                <td className="px-3 py-2 text-slate-400 whitespace-nowrap">{shown.orderDate || '—'}</td>
                <td className="px-3 py-2"><Outcome outcome={outcomeOf(a)} /></td>
                <td className="px-3 py-2 text-right tabular-nums text-slate-200 whitespace-nowrap">{rupiah(shown.commission?.value)}</td>
                <td className="px-3 py-2 text-right tabular-nums text-slate-200 whitespace-nowrap">{rupiah(shown.netPayable?.value)}</td>
                <td className="px-3 py-2 text-slate-400">
                  {a?.dispute ? <>{a.dispute.code}{a.dispute.field ? ` · ${a.dispute.field}` : ''}</> : '—'}
                </td>
              </tr>,
              expanded && (
                <tr key={`${id}-d`}>
                  <td colSpan={6} className="px-3 pb-3">
                    <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
                      {[[sent ? t('Your /reconcile') : t('Their /reconcile'), q], [sent ? t('Their answer') : t('Your answer'), a]].map(([title, bag]) => (
                        <div key={title} className="rounded-lg border border-white/[0.06] bg-slate-950/40 px-3 py-2">
                          <p className="mb-1 text-[11px] font-semibold text-slate-400">{title}</p>
                          {bag ? <SettlementDetails bag={bag} /> : <p className="text-slate-500">{t('Nothing yet')}</p>}
                        </div>
                      ))}
                    </div>
                  </td>
                </tr>
              ),
            ]
          })}
        </tbody>
      </table>
    </div>
  )
}

// AnswerSummary shows an /on_reconcile's settlementSummary: the outcome
// counts and the agreed and unreconciled entitlements; and any message's
// order-day counts.
export function AnswerSummary({ request, response }) {
  const { t } = useI18n()
  const sm = parse(response)?.message?.settlementSummary
  const days = parse(request)?.message?.settlementSummary?.orderDayCounts || []
  const theirDays = sm?.orderDayCounts || []
  if (!sm?.agreedCount && sm?.agreedCount !== 0 && !days.length) return null
  return (
    <div className="space-y-2">
      {sm && sm.agreedCount != null && (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-px rounded-xl overflow-hidden border border-white/[0.07] bg-white/[0.04]">
          {[
            [t('Contracts agreed / disputed / pending'), `${sm.agreedCount} / ${sm.disputedCount ?? 0} / ${sm.pendingCount ?? 0}`, 'text-white'],
            [t('Settlements agreed / disputed / pending'), sm.agreedSettlementCount != null ? `${sm.agreedSettlementCount} / ${sm.disputedSettlementCount ?? 0} / ${sm.pendingSettlementCount ?? 0}` : '—', 'text-white'],
            [t('Agreed: commission · net payable'), `${rupiah(sm.totalAgreedCommission)} · ${rupiah(sm.totalAgreedNetPayable)}`, 'text-emerald-300'],
            [t('Unreconciled: commission · net payable'), `${rupiah(sm.totalUnreconciledCommission)} · ${rupiah(sm.totalUnreconciledNetPayable)}`, 'text-amber-300'],
          ].map(([label, value, cls]) => (
            <div key={label} className="bg-slate-950/40 px-3 py-2">
              <p className="text-[10px] text-slate-500">{label}</p>
              <p className={`text-xs font-bold tabular-nums ${cls}`}>{value}</p>
            </div>
          ))}
        </div>
      )}
      {(days.length > 0 || theirDays.length > 0) && (
        <div className="text-[11px] text-slate-400">
          <span className="text-[10px] uppercase tracking-widest text-slate-500 mr-2">{t('Orders per order day')}</span>
          {[...new Set([...days, ...theirDays].map(d => d.orderDate))].sort().map(day => {
            const s = days.find(d => d.orderDate === day), r = theirDays.find(d => d.orderDate === day)
            return (
              <span key={day} className="mr-3 whitespace-nowrap" title={t('created that day · not yet reconciled')}>
                {day}: {t('sender')} {s ? `${s.reportedOrderCount} · ${s.remainingOrderCount}` : '—'}
                {r && <>, {t('answer')} {r.reportedOrderCount} · {r.remainingOrderCount}</>}
              </span>
            )
          })}
        </div>
      )}
    </div>
  )
}

// CollectionChip summarizes a contract's collections (from its figures):
// method, who holds the money, and whether it is collected.
export function CollectionChip({ collections }) {
  const { t } = useI18n()
  const c = (collections || [])[0]
  if (!c) return null
  const done = c.status === 'COLLECTED'
  return (
    <span className={`inline-flex px-1.5 py-0.5 rounded text-[10px] border ${done ? 'border-emerald-500/30 text-emerald-300' : 'border-amber-500/30 text-amber-300'}`}
      title={t('{method}, by {who}: {status}', { method: t(COLLECTION_METHOD[c.method] || c.method), who: c.collectorRole, status: t(COLLECTION_STATUS[c.status] || c.status) })}>
      {t(COLLECTION_METHOD[c.method] || c.method)} · {c.collectorRole} · {t(COLLECTION_STATUS[c.status] || c.status)}
    </span>
  )
}
