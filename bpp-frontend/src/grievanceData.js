import { useCallback, useEffect, useState } from 'react'
import { AlertCircle, Hourglass, CheckCircle2, Lock } from 'lucide-react'
import { apiFetch } from './api.js'
import { useLive } from './live.js'

// Words, formatting and data helpers for the seller's grievance (complaint)
// pages (Grievances.jsx). Labels are the English keys of locales/id.js; the
// pages pass them through t(). No protocol words reach the screen: every
// code the API returns is mapped to plain text here.

// Case status -> label + icon (status is shown by text and icon, not colour).
export const STATUS = {
  OPEN: { label: 'New – needs a reply', icon: AlertCircle, cls: 'bg-amber-500/15 text-amber-300 border-amber-500/30' },
  IN_PROGRESS: { label: 'In progress', icon: Hourglass, cls: 'bg-sky-500/15 text-sky-300 border-sky-500/30' },
  RESOLVED: { label: 'Resolved – awaiting confirmation', icon: CheckCircle2, cls: 'bg-emerald-500/15 text-emerald-300 border-emerald-500/30' },
  CLOSED: { label: 'Closed', icon: Lock, cls: 'bg-slate-500/15 text-slate-300 border-slate-500/30' },
}
export const statusOf = g => STATUS[g?.deemedClosed ? 'CLOSED' : g?.caseStatus] || STATUS.OPEN

export const CATEGORIES = {
  DAMAGED_ITEM: 'Damaged item',
  WRONG_ITEM: 'Wrong item',
  MISSING_ITEM: 'Missing item',
  LATE_DELIVERY: 'Late delivery',
  RETURN_REJECTED: 'Return rejected',
  RETURN_PICKUP_FAILED: 'Pickup did not come',
  RETURN_QC_DISPUTED: 'Return check result disputed',
  REFUND_NOT_RECEIVED: 'Money not received yet',
  WRONG_RETURN: 'Wrong item returned',
  OTHER: 'Other problem',
}
export const categoryLabel = c => CATEGORIES[c] || CATEGORIES.OTHER
// What the seller can report about an order (POST /orders/:id/grievances).
export const SELLER_CATEGORIES = ['WRONG_RETURN', 'RETURN_QC_DISPUTED', 'OTHER']

export const AUTHORIZATION = {
  EXPLICIT_AGREEMENT: 'Waiting for the buyer to agree',
  POLICY_AUTHORIZED: 'Within the return policy (no approval needed)',
  UNILATERAL: 'Covered by you',
}

export const RAISED_BY = { BUYER: 'Buyer', SELLER: 'Seller' }

export const RETURN_STATUS = {
  Return_Initiated: 'Return requested',
  Return_Approved: 'Return approved',
  Return_Received: 'Returned item received',
  Return_Rejected: 'Return rejected',
}
export const returnStatusLabel = s => RETURN_STATUS[s] || 'Return in progress'

export const ORDER_STATUS = {
  ACTIVE: 'Order active',
  COMPLETE: 'Order completed',
  COMPLETED: 'Order completed',
  CANCELLED: 'Order cancelled',
  DRAFT: 'Order not confirmed yet',
}

export const refundStatusLabel = r => {
  if (r.settledAt) return 'Paid out'
  const s = String(r.status || '').toUpperCase()
  if (['COMPLETED', 'COMPLETE', 'SUCCESS', 'SETTLED', 'PAID'].includes(s)) return 'Paid out'
  if (['FAILED', 'REJECTED'].includes(s)) return 'Failed'
  return 'Being paid out'
}

// The ONE thing that happens next, for the banner: what to say when it waits
// on the seller, and when it waits on the buyer app.
export const NEXT_MINE = {
  ACKNOWLEDGE: 'The buyer is waiting for your reply.',
  PROPOSE: 'Offer the buyer a solution.',
  DECIDE: 'The buyer answered your report. Accept or reject their answer.',
  EXECUTE: 'The solution is agreed. Carry it out now.',
  RESOLVE: 'Everything is done. Mark the complaint as resolved.',
  CONFIRM: 'The buyer marked it resolved. Close it, or tell them it is still a problem.',
}
export const NEXT_THEIRS = {
  ACKNOWLEDGE: "Waiting for the buyer's reply",
  PROPOSE: "Waiting for the buyer's reply",
  DECIDE: 'Waiting for the buyer to agree',
  EXECUTE: 'Waiting for the buyer to carry out the solution',
  RESOLVE: 'Waiting for the buyer to mark it resolved',
  CONFIRM: 'Waiting for the buyer to confirm',
}
// Short text for the inbox row.
export function whoText(g) {
  if (g.deemedClosed || g.caseStatus === 'CLOSED' || g.next?.action === 'NONE' || !g.next?.waitingOn) return 'Nothing more to do'
  return g.next.waitingOn === 'ME' ? 'Waiting on you' : 'Waiting on the buyer'
}

// ─── formatting ──────────────────────────────────────────────────────────────

export const rupiah = n => n == null ? '—' : `Rp ${Math.round(Number(n)).toLocaleString('id-ID')}`

// Times are shown in WIB (Asia/Jakarta) whatever the browser's zone.
export function fmtWib(ts, locale) {
  if (!ts) return '—'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString(locale, {
    timeZone: 'Asia/Jakarta', day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit',
  }) + ' WIB'
}

// "2 d 3 h", "4 h 10 min", "12 min" -- t translates the units.
export function durationText(ms, t) {
  const m = Math.max(1, Math.round(Math.abs(ms) / 60000))
  const d = Math.floor(m / 1440), h = Math.floor(m % 1440 / 60), mm = m % 60
  if (d) return `${t('{n} d', { n: d })} ${t('{n} h', { n: h })}`
  if (h) return `${t('{n} h', { n: h })} ${t('{n} min', { n: mm })}`
  return t('{n} min', { n: mm })
}

// SLA state of a due time: level 'ok' | 'soon' (< 2 hours) | 'late'.
export function slaOf(dueAt, now) {
  if (!dueAt) return null
  const ms = new Date(dueAt).getTime() - now
  if (Number.isNaN(ms)) return null
  return { ms, level: ms <= 0 ? 'late' : ms < 2 * 3600 * 1000 ? 'soon' : 'ok' }
}

// "1× Pizza" for an order line and quantity.
export const itemText = (line, qty) => `${qty ?? line?.quantity ?? 1}× ${line?.name || '—'}`
export const lineOf = (order, commitmentId) => order?.lines?.find(l => l.commitmentId === commitmentId)
export const linesText = (order, affected) => (affected || [])
  .map(a => itemText(lineOf(order, a.commitmentId), a.quantity || lineOf(order, a.commitmentId)?.quantity))
  .join(', ')
export const returnItems = (order, r) => (r?.commitmentIds || [])
  .map(cid => itemText(lineOf(order, cid))).join(', ')

// What the buyer paid for the whole order.
export const orderPaid = order => (order?.lines || []).reduce((s, l) => s + (l.unitPrice || 0) * (l.quantity || 0), 0)

// One action of an offer as a phrase ("a refund of Rp 30.000 for 1× Pizza").
// a is an OfferView action; returns [key, vars] for t().
export function actionPhrase(a, order) {
  const items = linesText(order, a.affected) || '—'
  switch (a.type) {
    case 'REFUND': return ['a refund of {amount} for {items}', { amount: rupiah(a.amount), items }]
    case 'REPLACEMENT': return ['a replacement for {items}', { items }]
    case 'REDELIVERY': return ['sending {items} again', { items }]
    case 'INFORMATION_PROVIDED': return ['an explanation: "{text}"', { text: a.description || '' }]
    case 'NO_ACTION': return ['no further action', {}]
    default: return ['{text}', { text: a.description || '' }]
  }
}

// A draft action (resolution builder) in the same OfferView shape.
export function draftToAction(d, order) {
  const line = lineOf(order, d.commitmentId)
  const qty = Number(d.quantity) || line?.quantity || 1
  const affected = d.commitmentId ? [{ commitmentId: d.commitmentId, quantity: qty }] : []
  switch (d.kind) {
    case 'REFUND_FULL': return { type: 'REFUND', affected, amount: (line?.unitPrice || 0) * qty }
    case 'REFUND_PARTIAL': return { type: 'REFUND', affected, amount: Number(d.amount) || 0 }
    case 'REPLACEMENT': return { type: 'REPLACEMENT', affected }
    case 'REDELIVERY': return { type: 'REDELIVERY', affected }
    case 'INFORMATION': return { type: 'INFORMATION_PROVIDED', affected, description: d.description }
    default: return { type: 'NO_ACTION', affected }
  }
}

// Prefill for the next version from an earlier offer's actions.
export function offerToDrafts(offer, order) {
  return (offer?.actions || []).map((a, i) => {
    const aff = a.affected?.[0]
    const line = lineOf(order, aff?.commitmentId)
    const qty = aff?.quantity || line?.quantity || 1
    const base = { key: `v${i}-${Date.now()}`, commitmentId: aff?.commitmentId || '', quantity: qty, amount: '', description: '' }
    switch (a.type) {
      case 'REFUND': {
        const full = (line?.unitPrice || 0) * qty
        return Math.abs((a.amount || 0) - full) < 0.5
          ? { ...base, kind: 'REFUND_FULL' }
          : { ...base, kind: 'REFUND_PARTIAL', amount: String(Math.round(a.amount || 0)) }
      }
      case 'REPLACEMENT': return { ...base, kind: 'REPLACEMENT' }
      case 'REDELIVERY': return { ...base, kind: 'REDELIVERY' }
      case 'INFORMATION_PROVIDED': return { ...base, kind: 'INFORMATION', description: a.description || '' }
      case 'NO_ACTION': return { ...base, kind: 'NO_ACTION' }
      default: return null
    }
  }).filter(Boolean)
}

export const ROUTE_OF_AUTH = { EXPLICIT_AGREEMENT: 'EXPLICIT', POLICY_AUTHORIZED: 'POLICY', UNILATERAL: 'UNILATERAL' }

// The pending (not yet carried out) actions of the agreed offer, and the
// label of the button that carries them out.
export function executeLabel(offer) {
  const pending = (offer?.actions || []).filter(a => !a.executed && a.executionMethod !== 'NONE')
  if (pending.some(a => a.type === 'REFUND')) return 'Process refund'
  if (pending.some(a => a.type === 'REDELIVERY' || a.type === 'REPLACEMENT')) return 'Send again'
  return 'Mark as done'
}
export const pendingRefundTotal = offer => (offer?.actions || [])
  .filter(a => !a.executed && a.type === 'REFUND').reduce((s, a) => s + (a.amount || 0), 0)

// Timeline entry -> plain sentence key + vars.
export function timelineText(e) {
  const me = e.by === 'ME'
  const v = { v: e.version || 1 }
  switch (e.eventType) {
    case 'OPENED': return [me ? 'You reported the problem' : 'The buyer raised the complaint', v]
    case 'ACKNOWLEDGED': return [me ? 'You responded' : 'The buyer responded', v]
    case 'STATUS_CHANGED': return [me ? 'You updated the status' : 'The buyer updated the status', v]
    case 'RESOLUTION_PROPOSED': return [me ? 'You offered a solution (version {v})' : 'The buyer offered a solution (version {v})', v]
    case 'RESOLUTION_DECIDED':
      if (e.result === 'ACCEPTED') return [me ? 'You accepted the solution (version {v})' : 'The buyer accepted your offer (version {v})', v]
      return [me ? 'You rejected the solution (version {v})' : 'The buyer rejected your offer (version {v})', v]
    case 'ACTION_COMPLETED': return [me ? 'You carried out the solution' : 'The buyer carried out the solution', v]
    case 'RESOLVED': return [me ? 'You marked it resolved' : 'The buyer marked it resolved', v]
    case 'OBJECTION': return [me ? 'You said it is still a problem' : 'The buyer said it is still a problem', v]
    case 'CLOSED': return [me ? 'You closed the complaint' : 'The buyer closed the complaint', v]
    default: return [me ? 'You updated the complaint' : 'The buyer updated the complaint', v]
  }
}

// Error from an API call -> what to show. SEND_FAILED means nothing was
// recorded: the form keeps its input and offers a retry.
export function errorText(e, t) {
  if (e?.code === 'SEND_FAILED') return t('Could not be sent, try again')
  if (e?.code === 'NOT_FOUND') return t('This complaint or order was not found.')
  if (e?.code === 'INVALID' && e.detail) return e.detail
  return e?.detail || e?.message || t('Something went wrong')
}

// ─── hooks ───────────────────────────────────────────────────────────────────

// now, ticking every `every` ms (for SLA timers).
export function useNow(every = 30000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), every)
    return () => clearInterval(timer)
  }, [every])
  return now
}

// How many grievances wait on the seller (nav badge, dashboard card); null
// until known. Live.
// Not fetched until active (the app has its login token).
export function useNeedsAction(active = true) {
  const [count, setCount] = useState(null)
  const refresh = useCallback(() => {
    if (!active) return
    apiFetch('/grievances?only=action').then(d => setCount(d?.needsAction ?? 0)).catch(() => { })
  }, [active])
  useEffect(() => { refresh() }, [refresh])
  useLive(refresh, active)
  return count
}
