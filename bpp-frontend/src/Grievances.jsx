import { useCallback, useEffect, useState } from 'react'
import {
  AlertCircle, AlertTriangle, ArrowLeft, CheckCircle2, ChevronRight, Clock, Flag, History, Inbox,
  Info, Lock, MessageSquareWarning, Plus, RefreshCw, RotateCcw, Send, ShieldCheck, ThumbsDown,
  ThumbsUp, Trash2, Truck, Wallet, X, Hourglass, CircleSlash,
} from 'lucide-react'
import { apiFetch } from './api.js'
import { useLive } from './live.js'
import { useI18n } from './i18n.jsx'
import {
  statusOf, STATUS, CATEGORIES, categoryLabel, SELLER_CATEGORIES, AUTHORIZATION, RAISED_BY,
  returnStatusLabel, ORDER_STATUS, refundStatusLabel, NEXT_MINE, NEXT_THEIRS, whoText,
  rupiah, fmtWib, durationText, slaOf, itemText, lineOf, linesText, returnItems, orderPaid,
  actionPhrase, draftToAction, offerToDrafts, ROUTE_OF_AUTH, executeLabel, pendingRefundTotal,
  timelineText, errorText, useNow,
} from './grievanceData.js'

// The seller's grievance (complaint) pages: the inbox that replaced the
// support-ticket list, a grievance's detail with its one next step, the
// resolution builder, and the order drawer's "report a problem" section.
// Backend: /api/v1/grievances and /api/v1/orders/:id/grievances.

// ─── small pieces ────────────────────────────────────────────────────────────

const btnPrimary = 'inline-flex items-center justify-center gap-2 px-4 py-2 rounded-xl text-sm font-semibold text-white disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 transition-all'
const btnSecondary = 'inline-flex items-center justify-center gap-2 px-4 py-2 rounded-xl text-sm font-semibold text-slate-200 border border-white/10 hover:border-white/25 disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 transition-all'
const BRAND = 'linear-gradient(135deg,#00b8e6 0%,#1e2fa0 100%)'
const inputCls = 'w-full bg-slate-900 border border-white/[0.1] rounded-xl px-3 py-2 text-sm text-slate-200 placeholder-slate-500 focus:outline-none focus:border-cyan-500/50'
const labelCls = 'block text-[11px] font-semibold text-slate-400 mb-1'

function Card({ children, className = '' }) {
  return <div className={`bg-[#111827] border border-white/[0.07] rounded-2xl ${className}`}>{children}</div>
}

function Spinner({ label }) {
  return (
    <div className="flex flex-col items-center justify-center py-14 gap-3" role="status">
      <div className="w-8 h-8 rounded-full border-2 border-slate-700 border-t-cyan-400 animate-spin" />
      {label && <p className="text-xs text-slate-500">{label}</p>}
    </div>
  )
}

function ErrorNote({ message, onRetry }) {
  const { t } = useI18n()
  return (
    <div role="alert" className="flex items-center gap-3 bg-red-500/10 border border-red-500/25 rounded-xl px-4 py-3 text-sm text-red-300">
      <AlertCircle size={16} className="shrink-0" aria-hidden="true" />
      <span className="flex-1 text-xs">{message}</span>
      {onRetry && (
        <button type="button" onClick={onRetry} className="inline-flex items-center gap-1 text-xs font-semibold underline underline-offset-2 shrink-0">
          <RotateCcw size={12} aria-hidden="true" /> {t('Retry')}
        </button>
      )}
    </div>
  )
}

function Title({ children }) {
  return <h2 className="text-[10px] font-bold uppercase tracking-widest text-slate-500 mb-3">{children}</h2>
}

export function StatusChip({ g }) {
  const { t } = useI18n()
  const s = statusOf(g)
  const Icon = s.icon
  return (
    <span className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-semibold border whitespace-nowrap ${s.cls}`}>
      <Icon size={12} aria-hidden="true" /> {t(s.label)}
    </span>
  )
}

function CategoryChip({ category }) {
  const { t } = useI18n()
  return (
    <span className="inline-flex px-2 py-0.5 rounded-md text-[11px] font-semibold bg-violet-500/15 text-violet-200 border border-violet-500/25 whitespace-nowrap">
      {t(categoryLabel(category))}
    </span>
  )
}

// Time left to reply / resolve. Amber under two hours, red when past --
// always with text and an icon too.
function SlaTimer({ due, kind, now }) {
  const { t } = useI18n()
  const sla = slaOf(due, now)
  if (!sla) return <span className="text-xs text-slate-600">—</span>
  const time = durationText(sla.ms, t)
  if (sla.level === 'late') {
    return (
      <span className="inline-flex items-center gap-1 text-xs font-semibold text-red-300">
        <AlertTriangle size={12} aria-hidden="true" /> {t('Overdue by {time}', { time })}
      </span>
    )
  }
  const text = kind === 'reply' ? t('Reply within {time}', { time }) : t('Resolve within {time}', { time })
  return (
    <span className={`inline-flex items-center gap-1 text-xs ${sla.level === 'soon' ? 'font-semibold text-amber-300' : 'text-slate-400'}`}>
      <Clock size={12} aria-hidden="true" /> {text}{sla.level === 'soon' && <span className="sr-only"> ({t('almost due')})</span>}
    </span>
  )
}
const slaKind = g => (!g.acknowledgedAt && g.caseStatus === 'OPEN' ? 'reply' : 'resolve')
const slaVisible = g => !g.deemedClosed && (g.caseStatus === 'OPEN' || g.caseStatus === 'IN_PROGRESS')

function Modal({ title, onClose, children, wide = false }) {
  const { t } = useI18n()
  useEffect(() => {
    const onKey = e => { if (e.key === 'Escape') onClose() }
    globalThis.addEventListener('keydown', onKey)
    return () => globalThis.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center p-4" role="dialog" aria-modal="true" aria-label={title}>
      <div className="absolute inset-0 bg-black/60" onClick={onClose} role="presentation" />
      <div className={`relative w-full ${wide ? 'max-w-2xl' : 'max-w-md'} max-h-[90vh] flex flex-col rounded-2xl border border-white/[0.08] bg-slate-900 shadow-2xl`}>
        <div className="flex items-center justify-between gap-3 px-5 py-4 border-b border-white/[0.06]">
          <p className="text-sm font-bold text-white">{title}</p>
          <button type="button" onClick={onClose} aria-label={t('Close window')} className="p-1.5 rounded-lg text-slate-400 hover:text-white">
            <X size={16} aria-hidden="true" />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto p-5">{children}</div>
      </div>
    </div>
  )
}

function ConfirmDialog({ title, body, confirmLabel, onConfirm, onCancel }) {
  const { t } = useI18n()
  return (
    <Modal title={title} onClose={onCancel}>
      <div className="space-y-5">
        <div className="text-sm text-slate-300 space-y-2">{body}</div>
        <div className="flex flex-wrap justify-end gap-2">
          <button type="button" onClick={onCancel} className={btnSecondary}>{t('Cancel')}</button>
          <button type="button" autoFocus onClick={onConfirm} className={btnPrimary} style={{ background: BRAND }}>{confirmLabel}</button>
        </div>
      </div>
    </Modal>
  )
}

// GET path, refreshed quietly every few seconds (useLive). set() replaces the
// data with an action's answer.
function useResource(path) {
  const [state, setState] = useState({ path: null, data: null, error: null })
  const load = useCallback(() => {
    if (!path) return Promise.resolve()
    return apiFetch(path).then(
      data => setState({ path, data, error: null }),
      error => setState(s => ({ path, data: s.path === path ? s.data : null, error })))
  }, [path])
  useEffect(() => { load() }, [load])
  useLive(() => {
    if (path) apiFetch(path).then(data => setState({ path, data, error: null })).catch(() => { })
  }, !!path)
  const mine = state.path === path
  return {
    data: mine ? state.data : null,
    error: mine ? state.error : null,
    loading: !!path && !mine,
    reload: load,
    set: data => setState({ path, data, error: null }),
  }
}

// ─── page ────────────────────────────────────────────────────────────────────

// GrievancesPage is the "support" page. grievanceId opens one grievance
// (from the order drawer); only preselects the action/SLA filter.
export function GrievancesPage({ grievanceId, only }) {
  return <GrievancesView key={`${grievanceId || ''}|${only || ''}`} grievanceId={grievanceId} only={only} />
}

function GrievancesView({ grievanceId, only }) {
  const [openId, setOpenId] = useState(grievanceId || null)
  const [filters, setFilters] = useState({ status: '', category: '', only: only || '' })
  if (openId) return <GrievanceDetail id={openId} onBack={() => setOpenId(null)} />
  return <GrievanceInbox filters={filters} setFilters={setFilters} onOpen={setOpenId} />
}

// ─── inbox ───────────────────────────────────────────────────────────────────

function GrievanceInbox({ filters, setFilters, onOpen }) {
  const { t, locale } = useI18n()
  const now = useNow()
  const qs = new URLSearchParams(Object.entries(filters).filter(([, v]) => v)).toString()
  const { data, loading, error, reload } = useResource(`/grievances${qs ? `?${qs}` : ''}`)
  const items = data?.items || []
  const needsAction = data?.needsAction ?? 0
  const filtered = !!qs
  const setF = (k, v) => setFilters(f => ({ ...f, [k]: v }))

  const quick = [
    ['', t('All')],
    ['action', t('Needs action')],
    ['sla', t('Time limit nearly up')],
  ]

  return (
    <div>
      <div className="flex items-start justify-between gap-3 mb-6">
        <div className="min-w-0">
          <p className="text-[10px] font-semibold uppercase tracking-widest mb-1.5" style={{ color: '#00b8e6' }}>{t('Grievances')}</p>
          <h1 className="text-2xl font-bold text-white leading-tight">{t('Complaints')}</h1>
          <p className="text-sm text-slate-400 mt-1">{t('Problems buyers raised about their orders, and problems you reported. What needs you comes first.')}</p>
        </div>
        <button type="button" onClick={() => reload()}
          className="shrink-0 whitespace-nowrap flex items-center gap-1.5 text-xs text-slate-400 hover:text-white transition-colors px-3 py-1.5 rounded-lg border border-white/[0.07]">
          <RefreshCw size={12} aria-hidden="true" /> {t('Refresh')}
        </button>
      </div>

      {data && needsAction > 0 && (
        <button type="button" onClick={() => setF('only', 'action')}
          className="mb-4 inline-flex items-center gap-2 text-sm font-semibold px-4 py-2 rounded-xl bg-amber-500/10 border border-amber-500/30 text-amber-200 hover:bg-amber-500/15">
          <MessageSquareWarning size={14} aria-hidden="true" />
          {t(needsAction === 1 ? '{count} complaint needs your action' : '{count} complaints need your action', { count: needsAction })}
        </button>
      )}

      <div data-tour="grievance-filters" className="flex flex-wrap items-end gap-3 mb-4">
        <div role="group" aria-label={t('Show')} className="inline-flex rounded-xl border border-white/10 overflow-hidden">
          {quick.map(([v, label]) => (
            <button key={v || 'all'} type="button" aria-pressed={filters.only === v} onClick={() => setF('only', v)}
              className={`px-3 py-2 text-xs font-semibold transition-colors ${filters.only === v ? 'bg-cyan-500/20 text-cyan-200' : 'text-slate-400 hover:text-slate-200'}`}>
              {label}
            </button>
          ))}
        </div>
        <label className="text-xs">
          <span className="sr-only">{t('Status')}</span>
          <select value={filters.status} onChange={e => setF('status', e.target.value)} className={`${inputCls} !w-auto !py-1.5 text-xs`}>
            <option value="">{t('All statuses')}</option>
            {Object.entries(STATUS).map(([k, s]) => <option key={k} value={k}>{t(s.label)}</option>)}
          </select>
        </label>
        <label className="text-xs">
          <span className="sr-only">{t('Problem')}</span>
          <select value={filters.category} onChange={e => setF('category', e.target.value)} className={`${inputCls} !w-auto !py-1.5 text-xs`}>
            <option value="">{t('All problems')}</option>
            {Object.entries(CATEGORIES).map(([k, label]) => <option key={k} value={k}>{t(label)}</option>)}
          </select>
        </label>
        {filtered && (
          <button type="button" onClick={() => setFilters({ status: '', category: '', only: '' })} className="text-xs font-semibold text-cyan-300 underline underline-offset-2 py-2">
            {t('Clear filters')}
          </button>
        )}
      </div>

      {error && <div className="mb-4"><ErrorNote message={errorText(error, t)} onRetry={() => reload()} /></div>}

      <Card>
        <div data-tour="grievance-list">
          {loading ? <Spinner label={t('Loading complaints…')} /> : !items.length ? (
            <div className="flex flex-col items-center text-center py-14 px-6 gap-2">
              <Inbox size={28} className="text-slate-600" aria-hidden="true" />
              <p className="text-sm font-semibold text-slate-300">{filtered ? t('Nothing matches these filters') : t('No complaints yet')}</p>
              <p className="text-xs text-slate-500 max-w-sm">
                {filtered ? t('Try another filter, or clear them to see every complaint.') : t('When a buyer has a problem with an order, it shows here with what to do next.')}
              </p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm min-w-[880px]">
                <thead>
                  <tr className="text-[10px] font-bold text-slate-500 uppercase tracking-widest border-b border-white/[0.06]">
                    <th scope="col" className="text-left px-4 py-3">{t('No.')}</th>
                    <th scope="col" className="text-left px-4 py-3">{t('Order')}</th>
                    <th scope="col" className="text-left px-4 py-3">{t('Problem')}</th>
                    <th scope="col" className="text-left px-4 py-3">{t('Raised by')}</th>
                    <th scope="col" className="text-left px-4 py-3">{t('Status')}</th>
                    <th scope="col" className="text-left px-4 py-3">{t('Time limit')}</th>
                    <th scope="col" className="text-left px-4 py-3">{t('Last update')}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-white/[0.04]">
                  {items.map(g => (
                    <tr key={g.id} onClick={() => onOpen(g.id)}
                      className={`cursor-pointer hover:bg-white/[0.03] transition-colors ${g.needsAction ? 'bg-amber-500/[0.04]' : ''}`}>
                      <td className="px-4 py-3 align-top">
                        <button type="button" onClick={e => { e.stopPropagation(); onOpen(g.id) }}
                          className="inline-flex items-center gap-2 font-mono text-xs text-slate-300 hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 rounded">
                          {g.unread
                            ? <span className="w-2 h-2 rounded-full bg-cyan-400 shrink-0" aria-hidden="true" />
                            : <span className="w-2 h-2 shrink-0" aria-hidden="true" />}
                          {g.unread && <span className="sr-only">{t('Unread')}: </span>}
                          <span className={g.unread ? 'font-bold text-white' : ''}>{g.id}</span>
                        </button>
                      </td>
                      <td className="px-4 py-3 align-top max-w-[220px]">
                        <p className="text-slate-200 truncate" title={g.orderItems}>{g.orderItems || '—'}</p>
                        {g.orderTotal != null && <p className="text-[11px] text-slate-500">{rupiah(g.orderTotal)}</p>}
                      </td>
                      <td className="px-4 py-3 align-top"><CategoryChip category={g.category} /></td>
                      <td className="px-4 py-3 align-top text-xs text-slate-300">{t(RAISED_BY[g.raisedBy] || 'Buyer')}</td>
                      <td className="px-4 py-3 align-top">
                        <StatusChip g={g} />
                        <p className={`mt-1 text-[11px] flex items-center gap-1 ${g.needsAction ? 'text-amber-200 font-semibold' : 'text-slate-500'}`}>
                          {g.needsAction && <ChevronRight size={11} aria-hidden="true" />}{t(whoText(g))}
                        </p>
                      </td>
                      <td className="px-4 py-3 align-top">
                        {slaVisible(g) ? <SlaTimer due={g.dueAt} kind={slaKind(g)} now={now} /> : <span className="text-xs text-slate-600">—</span>}
                      </td>
                      <td className="px-4 py-3 align-top text-xs text-slate-500 whitespace-nowrap">{fmtWib(g.lastEventAt, locale)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </Card>
    </div>
  )
}

// ─── detail ──────────────────────────────────────────────────────────────────

function GrievanceDetail({ id, onBack }) {
  const { t, locale } = useI18n()
  const now = useNow()
  const { data: g, loading, error, reload, set } = useResource(`/grievances/${encodeURIComponent(id)}`)
  const [busy, setBusy] = useState(null)
  const [failure, setFailure] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [builderOpen, setBuilderOpen] = useState(false)
  const [draft, setDraft] = useState(null) // resolution builder input, kept until sent

  // run posts an action; the answer is the updated grievance. On failure
  // the input stays where it is; a send failure (nothing recorded) offers a
  // retry.
  const run = async (key, path, body) => {
    setBusy(key); setFailure(null)
    try {
      const d = await apiFetch(`/grievances/${encodeURIComponent(id)}${path}`, { method: 'POST', ...(body ? { body: JSON.stringify(body) } : {}) })
      set(d)
      return true
    } catch (e) {
      setFailure({ key, message: errorText(e, t), retry: (e.code === 'SEND_FAILED' || !e.status) ? () => run(key, path, body) : null })
      return false
    } finally {
      setBusy(null)
    }
  }

  const openBuilder = () => {
    if (!draft && g) setDraft(initialDraft(g))
    setFailure(null)
    setBuilderOpen(true)
  }

  if (loading) return <div><BackButton onBack={onBack} /><Spinner label={t('Loading complaint…')} /></div>
  if (!g) {
    return (
      <div>
        <BackButton onBack={onBack} />
        <ErrorNote message={error ? errorText(error, t) : t('Something went wrong')} onRetry={() => reload()} />
      </div>
    )
  }

  const order = g.order || {}
  const ackDue = !g.acknowledgedAt && g.caseStatus === 'OPEN'

  return (
    <div>
      <BackButton onBack={onBack} />

      <div className="flex flex-wrap items-start justify-between gap-3 mb-5">
        <div className="min-w-0">
          <p className="text-[11px] text-slate-500 font-mono mb-1">{t('No.')} {g.id}</p>
          <h1 className="text-2xl font-bold text-white leading-tight">{t(categoryLabel(g.category))}</h1>
          <p className="text-sm text-slate-400 mt-1">
            {g.raisedBy === 'SELLER' ? t('You reported this on {time}', { time: fmtWib(g.openedAt, locale) }) : t('The buyer raised this on {time}', { time: fmtWib(g.openedAt, locale) })}
          </p>
        </div>
        <div className="flex flex-col items-end gap-1.5">
          <StatusChip g={g} />
          {slaVisible(g) && <SlaTimer due={ackDue ? (g.firstResponseDue || g.resolutionDue) : g.resolutionDue} kind={ackDue ? 'reply' : 'resolve'} now={now} />}
        </div>
      </div>

      {error && <div className="mb-4"><ErrorNote message={t('Could not refresh. Showing the last known state.')} onRetry={() => reload()} /></div>}

      <NextStep g={g} busy={busy} failure={failure} run={run} openBuilder={openBuilder} setConfirm={setConfirm} />

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-5 mt-5">
        <div className="lg:col-span-2 space-y-5">
          <Card className="p-5">
            <Title>{g.raisedBy === 'SELLER' ? t('What you reported') : t('What the buyer said')}</Title>
            <p className="text-sm text-slate-200 whitespace-pre-wrap">{g.description || '—'}</p>
            {g.affected?.length > 0 && (
              <p className="text-xs text-slate-400 mt-3">{t('Items in question')}: <span className="text-slate-200">{linesText(order, g.affected)}</span></p>
            )}
          </Card>

          {g.offer && (
            <Card className="p-5">
              <Title>{g.offer.proposedBy === 'ME' ? t('Your offer') : t("The buyer's answer")}</Title>
              <OfferCard offer={g.offer} order={order} />
            </Card>
          )}

          {(g.history || []).filter(h => !g.offer || h.version !== g.offer.version || h.proposalId !== g.offer.proposalId).length > 0 && (
            <Card className="p-5">
              <details>
                <summary className="cursor-pointer text-xs font-semibold text-slate-300 flex items-center gap-2">
                  <History size={13} aria-hidden="true" /> {t('Earlier offers')}
                </summary>
                <div className="mt-4 space-y-4">
                  {g.history.filter(h => !g.offer || h.version !== g.offer.version || h.proposalId !== g.offer.proposalId).map(h => (
                    <div key={`${h.proposalId}-${h.version}`} className="border-t border-white/[0.06] pt-4">
                      <OfferCard offer={h} order={order} />
                    </div>
                  ))}
                </div>
              </details>
            </Card>
          )}

          <Card className="p-5">
            <Title>{t('What happened')}</Title>
            <Timeline g={g} />
          </Card>
        </div>

        <div className="space-y-5">
          <OrderSummary g={g} />
        </div>
      </div>

      {builderOpen && draft && (
        <ResolutionBuilder g={g} draft={draft} setDraft={setDraft} busy={busy === 'propose'}
          failure={failure?.key === 'propose' ? failure : null}
          onClose={() => setBuilderOpen(false)}
          onSend={async body => {
            if (await run('propose', '/proposal', body)) { setBuilderOpen(false); setDraft(null) }
          }} />
      )}
      {confirm && (
        <ConfirmDialog {...confirm} onCancel={() => setConfirm(null)}
          onConfirm={() => { const fn = confirm.onYes; setConfirm(null); fn() }} />
      )}
    </div>
  )
}

function BackButton({ onBack }) {
  const { t } = useI18n()
  return (
    <button type="button" onClick={onBack} className="mb-4 inline-flex items-center gap-1.5 text-xs font-semibold text-slate-400 hover:text-white">
      <ArrowLeft size={13} aria-hidden="true" /> {t('All complaints')}
    </button>
  )
}

function initialDraft(g) {
  const order = g.order || {}
  const qc = g.qcRecords || []
  const policyOk = !!order.returnable && qc.length > 0
  const prev = g.offer && g.offer.proposedBy === 'ME' ? g.offer : null
  let route = policyOk ? 'POLICY' : 'EXPLICIT'
  if (prev && ROUTE_OF_AUTH[prev.authorization] && (ROUTE_OF_AUTH[prev.authorization] !== 'POLICY' || policyOk)) route = ROUTE_OF_AUTH[prev.authorization]
  return {
    route,
    qcReference: qc[0]?.performanceId || '',
    actions: prev ? offerToDrafts(prev, order) : [],
  }
}

// ─── next step ───────────────────────────────────────────────────────────────

function NextStep({ g, busy, failure, run, openBuilder, setConfirm }) {
  const { t, locale } = useI18n()
  const [reject, setReject] = useState(null)      // reason text while rejecting
  const [objection, setObjection] = useState(null) // explanation while objecting
  const n = g.next || {}
  const order = g.order || {}
  const closed = g.deemedClosed || g.caseStatus === 'CLOSED' || n.action === 'NONE' || !n.waitingOn
  const mine = !closed && n.waitingOn === 'ME'
  const rejected = g.offer?.proposedBy === 'ME' && g.offer?.decision?.result === 'REJECTED' ? g.offer : null
  const sending = key => busy === key
  const failed = failure && failure.key !== 'propose' ? failure : null

  let icon = Hourglass, tone = 'border-sky-500/25 bg-sky-500/[0.06]', heading, sub = null, buttons = null

  if (closed) {
    icon = Lock; tone = 'border-white/10 bg-white/[0.02]'
    heading = t('This complaint is closed. Nothing more to do.')
    if (g.deemedClosed) sub = t('It was closed automatically because no one objected in time.')
    else if (g.closedAt) sub = t('Closed on {time}', { time: fmtWib(g.closedAt, locale) })
  } else if (!mine) {
    heading = t(NEXT_THEIRS[n.action] || "Waiting for the buyer's reply")
    sub = t('You will see their answer here as soon as it arrives.')
    if (n.action === 'DECIDE' && g.offer?.proposedAt) sub = t('You sent version {v} on {time}. You will see their answer here.', { v: g.offer.version, time: fmtWib(g.offer.proposedAt, locale) })
  } else {
    icon = MessageSquareWarning; tone = 'border-amber-500/30 bg-amber-500/[0.06]'
    heading = t(NEXT_MINE[n.action] || 'This complaint needs your action.')
    switch (n.action) {
      case 'ACKNOWLEDGE':
        buttons = <PrimaryButton busy={sending('ack')} onClick={() => run('ack', '/acknowledge')} icon={Send}>{t('Respond')}</PrimaryButton>
        sub = t('Let the buyer know you are looking into it.')
        break
      case 'PROPOSE':
        if (rejected) {
          heading = t('The buyer rejected your offer. Send a new version.')
          sub = rejected.decision.reason ? <>{t("Buyer's reason")}: <q className="text-slate-200">{rejected.decision.reason}</q></> : t('The buyer gave no reason.')
        }
        buttons = <PrimaryButton onClick={openBuilder} icon={Plus}>{rejected ? t('Propose version {v}', { v: (rejected.version || 1) + 1 }) : t('Propose a solution')}</PrimaryButton>
        break
      case 'EXECUTE': {
        const label = t(executeLabel(g.offer))
        const refund = pendingRefundTotal(g.offer)
        const go = () => run('execute', '/execute')
        buttons = (
          <PrimaryButton busy={sending('execute')} icon={refund > 0 ? Wallet : Truck}
            onClick={() => refund > 0 ? setConfirm({
              title: t('Refund {amount} to the buyer?', { amount: rupiah(refund) }),
              body: <><p>{t('The money goes back to the buyer through the order’s payment. This cannot be undone.')}</p><p className="text-slate-400">{t('The payment reference is added automatically.')}</p></>,
              confirmLabel: t('Yes, refund {amount}', { amount: rupiah(refund) }),
              onYes: go,
            }) : go()}>
            {label}
          </PrimaryButton>
        )
        sub = t('Agreed: {what}.', { what: offerSentence(g.offer, order, t) })
        break
      }
      case 'RESOLVE':
        buttons = <PrimaryButton busy={sending('resolve')} onClick={() => run('resolve', '/resolve')} icon={CheckCircle2}>{t('Mark resolved')}</PrimaryButton>
        sub = t('The buyer is then asked to confirm.')
        break
      case 'DECIDE': {
        const money = (g.offer?.actions || []).some(a => a.type === 'REFUND' && a.liable === 'ME')
        const accept = () => run('decide', '/decision', { accept: true })
        sub = g.offer ? <>{t('The buyer offers')}: <span className="text-slate-200">{offerSentence(g.offer, order, t)}</span></> : null
        buttons = reject === null ? (
          <>
            <PrimaryButton busy={sending('decide')} icon={ThumbsUp}
              onClick={() => money ? setConfirm({
                title: t('Accept the buyer’s answer?'),
                body: <p>{t('This includes money you pay: {what}.', { what: offerSentence(g.offer, order, t) })}</p>,
                confirmLabel: t('Accept'), onYes: accept,
              }) : accept()}>
              {t('Accept')}
            </PrimaryButton>
            <button type="button" className={btnSecondary} disabled={!!busy} onClick={() => setReject('')}>
              <ThumbsDown size={14} aria-hidden="true" /> {t('Reject')}
            </button>
          </>
        ) : null
        break
      }
      case 'CONFIRM':
        sub = g.objectionDeadline ? t('You can say it is still a problem until {time}.', { time: fmtWib(g.objectionDeadline, locale) }) : null
        buttons = objection === null ? (
          <>
            <PrimaryButton busy={sending('close')} onClick={() => run('close', '/close')} icon={CheckCircle2}>{t('Close')}</PrimaryButton>
            <button type="button" className={btnSecondary} disabled={!!busy} onClick={() => setObjection('')}>
              <AlertTriangle size={14} aria-hidden="true" /> {t('Still a problem')}
            </button>
          </>
        ) : null
        break
      default:
        break
    }
  }

  const Icon = icon
  return (
    <section aria-label={t('Next step')} className={`rounded-2xl border p-5 ${tone}`}>
      <div className="flex flex-wrap items-start gap-4">
        <Icon size={20} className={mine ? 'text-amber-300 mt-0.5' : 'text-slate-300 mt-0.5'} aria-hidden="true" />
        <div className="flex-1 min-w-[220px]">
          <p className="text-[10px] font-bold uppercase tracking-widest text-slate-500 mb-1">
            {closed ? t('Status') : mine ? t('Next step · waiting on you') : t('Next step · waiting on the buyer')}
          </p>
          <p className="text-sm font-semibold text-white">{heading}</p>
          {sub && <p className="text-xs text-slate-400 mt-1">{sub}</p>}
        </div>
        {buttons && <div className="flex flex-wrap gap-2">{buttons}</div>}
      </div>

      {reject !== null && (
        <form className="mt-4 space-y-3" onSubmit={async e => {
          e.preventDefault()
          if (await run('decide', '/decision', { accept: false, ...(reject.trim() ? { reason: reject.trim() } : {}) })) setReject(null)
        }}>
          <label className="block">
            <span className={labelCls}>{t('Why do you reject it? (optional)')}</span>
            <textarea value={reject} onChange={e => setReject(e.target.value)} rows={3} maxLength={500} className={inputCls} autoFocus />
          </label>
          <div className="flex flex-wrap gap-2">
            <PrimaryButton type="submit" busy={sending('decide')} icon={ThumbsDown}>{t('Send rejection')}</PrimaryButton>
            <button type="button" className={btnSecondary} onClick={() => setReject(null)}>{t('Cancel')}</button>
          </div>
        </form>
      )}

      {objection !== null && (
        <form className="mt-4 space-y-3" onSubmit={async e => {
          e.preventDefault()
          if (!objection.trim()) return
          if (await run('object', '/objection', { explanation: objection.trim() })) setObjection(null)
        }}>
          <label className="block">
            <span className={labelCls}>{t('What is still wrong?')}</span>
            <textarea value={objection} onChange={e => setObjection(e.target.value)} rows={3} maxLength={1500} required className={inputCls} autoFocus />
          </label>
          <div className="flex flex-wrap gap-2">
            <PrimaryButton type="submit" busy={sending('object')} disabled={!objection.trim()} icon={Send}>{t('Send to buyer')}</PrimaryButton>
            <button type="button" className={btnSecondary} onClick={() => setObjection(null)}>{t('Cancel')}</button>
          </div>
        </form>
      )}

      {failed && <div className="mt-4"><ErrorNote message={failed.message} onRetry={failed.retry} /></div>}
      {g.lastSent && (g.lastSent.delivery === 'NACK' || g.lastSent.delivery === 'FAILED') && !failed && (
        <p className="mt-3 text-xs text-red-300 flex items-center gap-1.5">
          <AlertTriangle size={12} aria-hidden="true" /> {t('Your last update did not reach the buyer app.')}
        </p>
      )}
    </section>
  )
}

function PrimaryButton({ busy, icon: Icon, children, type = 'button', disabled, onClick }) {
  const { t } = useI18n()
  return (
    <button type={type} onClick={onClick} disabled={busy || disabled} className={btnPrimary} style={{ background: BRAND }}>
      {Icon && <Icon size={14} aria-hidden="true" />} {busy ? t('Sending…') : children}
    </button>
  )
}

// "a refund of Rp 30.000 for 1× Pizza, sending 1× Cola again"
function offerSentence(offer, order, t) {
  return (offer?.actions || []).map(a => { const [k, v] = actionPhrase(a, order); return t(k, v) }).join(', ')
}

// ─── offer ───────────────────────────────────────────────────────────────────

const ACTION_ICON = { REFUND: Wallet, REPLACEMENT: RotateCcw, REDELIVERY: Truck, INFORMATION_PROVIDED: Info, NO_ACTION: CircleSlash }

function OfferCard({ offer, order }) {
  const { t, locale } = useI18n()
  const d = offer.decision
  const mineOffer = offer.proposedBy === 'ME'
  let route = t(AUTHORIZATION[offer.authorization] || 'Waiting for the buyer to agree')
  if (offer.authorization === 'EXPLICIT_AGREEMENT') {
    if (d?.result === 'ACCEPTED') route = mineOffer ? t('Accepted by the buyer') : t('You accepted it')
    else if (d?.result === 'REJECTED') route = mineOffer ? t('Rejected by the buyer') : t('You rejected it')
    else if (!mineOffer) route = t('Waiting for your decision')
  }
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
        <span className="font-semibold text-white">{t('Version {v}', { v: offer.version || 1 })}</span>
        {offer.proposedAt && <span className="text-slate-500">{fmtWib(offer.proposedAt, locale)}</span>}
        <span className="inline-flex items-center gap-1 text-slate-300">
          {d?.result === 'REJECTED' ? <X size={12} aria-hidden="true" /> : <ShieldCheck size={12} aria-hidden="true" />} {route}
        </span>
      </div>
      {d?.result === 'REJECTED' && d.reason && (
        <p className="text-xs text-slate-400">{mineOffer ? t("Buyer's reason") : t('Your reason')}: <q className="text-slate-200">{d.reason}</q></p>
      )}
      <ul className="space-y-2">
        {(offer.actions || []).map((a, i) => {
          const [k, v] = actionPhrase(a, order)
          const Icon = ACTION_ICON[a.type] || Info
          return (
            <li key={a.actionId || i} className="flex items-start gap-3 rounded-xl border border-white/[0.06] bg-white/[0.02] px-3 py-2.5">
              <Icon size={15} className="text-cyan-300 mt-0.5 shrink-0" aria-hidden="true" />
              <div className="flex-1 min-w-0 text-sm">
                <p className="text-slate-200 first-letter:uppercase">{t(k, v)}</p>
                {a.type === 'REFUND' && (
                  <p className="text-[11px] text-slate-500 mt-0.5">{a.liable === 'THEM' ? t('Paid by the buyer side') : t('Paid by the seller')}</p>
                )}
                {a.executionMethod !== 'NONE' && (
                  a.executed ? (
                    <p className="text-[11px] text-emerald-300 mt-1 flex flex-wrap items-center gap-x-2">
                      <span className="inline-flex items-center gap-1"><CheckCircle2 size={11} aria-hidden="true" /> {t('Done')}{a.completedAt ? ` · ${fmtWib(a.completedAt, locale)}` : ''}</span>
                      {a.executionReference && <span className="text-slate-400">{t('Reference')}: <span className="font-mono text-slate-300">{a.executionReference}</span></span>}
                    </p>
                  ) : (
                    <p className="text-[11px] text-slate-500 mt-1 inline-flex items-center gap-1"><Hourglass size={11} aria-hidden="true" /> {t('Not done yet')}</p>
                  )
                )}
              </div>
            </li>
          )
        })}
      </ul>
      {offer.total > 0 && <p className="text-xs text-slate-400">{t('Total refund')}: <span className="font-semibold text-white">{rupiah(offer.total)}</span></p>}
    </div>
  )
}

// ─── timeline ────────────────────────────────────────────────────────────────

function Timeline({ g }) {
  const { t, locale } = useI18n()
  const items = g.timeline || []
  if (!items.length) return <p className="text-xs text-slate-500">{t('Nothing has happened yet.')}</p>
  return (
    <ol className="space-y-4">
      {items.map((e, i) => {
        const [k, v] = timelineText(e)
        const failed = e.delivery === 'FAILED' || e.delivery === 'NACK'
        return (
          <li key={i} className="flex gap-3">
            <span className={`mt-1.5 w-2 h-2 rounded-full shrink-0 ${e.by === 'ME' ? 'bg-cyan-400' : 'bg-violet-400'}`} aria-hidden="true" />
            <div className="min-w-0">
              <p className="text-sm text-slate-200">{t(k, v)}</p>
              <p className="text-[11px] text-slate-500">
                {fmtWib(e.at, locale)}
                {e.delivery === 'SENDING' && <> · {t('Sending…')}</>}
              </p>
              {failed && (
                <p className="text-[11px] text-red-300 flex items-center gap-1 mt-0.5">
                  <AlertTriangle size={11} aria-hidden="true" /> {t('Not delivered to the buyer app')}
                </p>
              )}
              {e.note && <p className="text-xs text-slate-400 mt-1"><q>{e.note}</q></p>}
            </div>
          </li>
        )
      })}
    </ol>
  )
}

// ─── order summary ───────────────────────────────────────────────────────────

function OrderSummary({ g }) {
  const { t, locale } = useI18n()
  const order = g.order || {}
  const lines = order.lines || []
  const affected = new Set((g.affected || []).map(a => a.commitmentId))
  const returns = order.returns || []
  const refunds = order.refunds || []

  // The records the grievance points at, in words.
  const linked = (g.related || []).map(r => {
    if (r.refType === 'PERFORMANCE') {
      const ret = returns.find(x => x.performanceId === r.refId)
      if (ret) return t('Return of {items}: {status}', { items: returnItems(order, ret) || t('the order'), status: t(returnStatusLabel(ret.status)) })
      return t('The delivery of this order')
    }
    if (r.refType === 'SETTLEMENT') {
      const rf = refunds.find(x => x.settlementId === r.refId)
      if (rf) return t('Refund of {amount}: {status}', { amount: rupiah(rf.amount), status: t(refundStatusLabel(rf)) })
      return t('A refund for this order')
    }
    if (r.refType === 'CONTRACT') return t('The whole order')
    return null
  }).filter(Boolean)

  return (
    <Card className="p-5 space-y-5">
      <div>
        <Title>{t('Order')}</Title>
        {order.status && <p className="text-xs text-slate-400 mb-3">{t(ORDER_STATUS[order.status] || 'Order active')}</p>}
        {!lines.length ? <p className="text-xs text-slate-500">{t('No items')}</p> : (
          <ul className="space-y-2">
            {lines.map(l => (
              <li key={l.commitmentId} className="flex items-start justify-between gap-3 text-sm">
                <div className="min-w-0">
                  <p className="text-slate-200">{itemText(l)}</p>
                  <p className="text-[11px] text-slate-500">
                    {t('{price} each', { price: rupiah(l.unitPrice) })}
                    {affected.has(l.commitmentId) && <span className="ml-2 text-amber-300 font-semibold">· {t('In this complaint')}</span>}
                  </p>
                </div>
                <span className="text-slate-300 whitespace-nowrap">{rupiah((l.unitPrice || 0) * (l.quantity || 0))}</span>
              </li>
            ))}
          </ul>
        )}
        <div className="flex items-center justify-between border-t border-white/[0.06] mt-3 pt-3 text-sm">
          <span className="text-slate-400">{t('Amount paid')}</span>
          <span className="font-bold text-white">{rupiah(orderPaid(order))}</span>
        </div>
      </div>

      {linked.length > 0 && (
        <div>
          <Title>{t('This complaint is about')}</Title>
          <ul className="space-y-1.5 text-xs text-slate-300 list-disc pl-4">
            {linked.map((s, i) => <li key={i}>{s}</li>)}
          </ul>
        </div>
      )}

      <div>
        <Title>{t('Returns')}</Title>
        {!returns.length ? <p className="text-xs text-slate-500">{t('No returns on this order.')}</p> : (
          <ul className="space-y-2">
            {returns.map(r => (
              <li key={r.performanceId} className="text-xs">
                <p className="text-slate-200">{returnItems(order, r) || t('Returned items')}</p>
                <p className="text-slate-500">{t(returnStatusLabel(r.status))}{r.reason ? ` · ${r.reason}` : ''}</p>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div>
        <Title>{t('Refunds')}</Title>
        {!refunds.length ? <p className="text-xs text-slate-500">{t('No refunds on this order.')}</p> : (
          <ul className="space-y-2">
            {refunds.map(r => (
              <li key={r.settlementId} className="text-xs flex items-start justify-between gap-3">
                <div>
                  <p className="text-slate-200">{t(refundStatusLabel(r))}</p>
                  {r.settledAt && <p className="text-slate-500">{fmtWib(r.settledAt, locale)}</p>}
                  {r.reference && <p className="text-slate-500">{t('Reference')}: <span className="font-mono">{r.reference}</span></p>}
                </div>
                <span className="text-slate-200 font-semibold whitespace-nowrap">{rupiah(r.amount)}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Card>
  )
}

// ─── resolution builder ──────────────────────────────────────────────────────

const KINDS = [
  { kind: 'REFUND_FULL', label: 'Full refund', icon: Wallet, line: true },
  { kind: 'REFUND_PARTIAL', label: 'Partial refund', icon: Wallet, line: true },
  { kind: 'REPLACEMENT', label: 'Replace the item', icon: RotateCcw, line: true },
  { kind: 'REDELIVERY', label: 'Send again', icon: Truck, line: true },
  { kind: 'INFORMATION', label: 'Give an explanation', icon: Info, line: false },
  { kind: 'NO_ACTION', label: 'No action', icon: CircleSlash, line: false },
]
const kindOf = k => KINDS.find(x => x.kind === k) || KINDS[0]
const isRefund = k => k === 'REFUND_FULL' || k === 'REFUND_PARTIAL'

let draftSeq = 0
const newKey = () => `a${++draftSeq}`

// Problems with one draft action, as translated strings.
function draftProblems(d, order, t) {
  const out = []
  const line = lineOf(order, d.commitmentId)
  if (kindOf(d.kind).line) {
    if (!line) out.push(t('Choose an item.'))
    const q = Number(d.quantity)
    if (line && (!Number.isInteger(q) || q < 1 || q > line.quantity)) out.push(t('Quantity must be 1 to {max}.', { max: line.quantity }))
  }
  if (d.kind === 'REFUND_PARTIAL' && line) {
    const paid = line.unitPrice * (Number(d.quantity) || line.quantity)
    const amt = Number(d.amount)
    if (!(amt > 0)) out.push(t('Enter an amount above zero.'))
    else if (amt > paid + 0.005) out.push(t('More than the buyer paid for these items ({max}).', { max: rupiah(paid) }))
  }
  if (d.kind === 'INFORMATION' && !d.description.trim()) out.push(t('Write the explanation.'))
  return out
}

function ResolutionBuilder({ g, draft, setDraft, busy, failure, onClose, onSend }) {
  const { t } = useI18n()
  const [step, setStep] = useState('edit')
  const order = g.order || {}
  const lines = order.lines || []
  const qc = g.qcRecords || []
  const policyOk = !!order.returnable && qc.length > 0
  const version = g.offer && g.offer.proposedBy === 'ME' ? (g.offer.version || 1) + 1 : 1
  const rejected = g.offer?.proposedBy === 'ME' && g.offer?.decision?.result === 'REJECTED' ? g.offer : null

  const setD = patch => setDraft(d => ({ ...d, ...patch }))
  const setAction = (key, patch) => setDraft(d => ({ ...d, actions: d.actions.map(a => a.key === key ? { ...a, ...patch } : a) }))
  const removeAction = key => setDraft(d => ({ ...d, actions: d.actions.filter(a => a.key !== key) }))
  const addKind = kind => {
    const k = kindOf(kind)
    let add
    if (!k.line) add = [{ key: newKey(), kind, commitmentId: '', quantity: '', amount: '', description: '' }]
    else {
      const aff = (g.affected || []).filter(a => lineOf(order, a.commitmentId))
      const picks = aff.length ? aff.map(a => ({ id: a.commitmentId, qty: a.quantity || lineOf(order, a.commitmentId).quantity }))
        : lines.length ? [{ id: lines[0].commitmentId, qty: lines[0].quantity }] : [{ id: '', qty: 1 }]
      add = picks.map(p => ({ key: newKey(), kind, commitmentId: p.id, quantity: String(p.qty), amount: '', description: '' }))
    }
    setDraft(d => ({ ...d, actions: [...d.actions, ...add] }))
  }

  const problems = draft.actions.flatMap(a => draftProblems(a, order, t))
  if (!draft.actions.length) problems.push(t('Add at least one action.'))
  if (draft.route === 'POLICY' && (!policyOk || !draft.qcReference)) problems.push(t('Choose the returned item that passed the check.'))
  const ok = problems.length === 0

  const preview = draft.actions.map(a => draftToAction(a, order))
  const refundTotal = preview.filter(a => a.type === 'REFUND').reduce((s, a) => s + a.amount, 0)
  const sentence = preview.map(a => { const [k, v] = actionPhrase(a, order); return t(k, v) }).join(', ')

  const body = () => ({
    route: draft.route,
    ...(draft.route === 'POLICY' ? { qcReference: draft.qcReference } : {}),
    actions: draft.actions.map(a => {
      const out = { kind: a.kind }
      if (kindOf(a.kind).line && a.commitmentId) { out.commitmentId = a.commitmentId; out.quantity = Number(a.quantity) }
      if (a.kind === 'REFUND_PARTIAL') out.amount = Number(a.amount)
      if (a.kind === 'INFORMATION') out.description = a.description.trim()
      return out
    }),
  })

  const routeText = {
    EXPLICIT: t('The buyer is asked to accept or reject this offer. Nothing happens until they agree.'),
    POLICY: t('This follows your return policy, based on the returned item’s check. The buyer does not need to agree.'),
    UNILATERAL: t('You carry it out at your own cost. The buyer does not need to agree.'),
  }

  return (
    <Modal title={t('Propose a solution · version {v}', { v: version })} onClose={onClose} wide>
      {step === 'edit' ? (
        <div className="space-y-6">
          {rejected && (
            <div className="rounded-xl border border-amber-500/30 bg-amber-500/[0.06] px-4 py-3 text-xs text-amber-100">
              <p className="font-semibold">{t('The buyer rejected version {v}.', { v: rejected.version || 1 })}</p>
              <p className="mt-1">{rejected.decision?.reason ? <>{t("Buyer's reason")}: <q>{rejected.decision.reason}</q></> : t('The buyer gave no reason.')}</p>
              <p className="mt-1 text-amber-200/70">{t('The new version starts from the previous offer. Change what you need.')}</p>
            </div>
          )}

          <fieldset>
            <legend className={labelCls}>{t('What do you offer?')}</legend>
            <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
              {KINDS.map(k => {
                const Icon = k.icon
                return (
                  <button key={k.kind} type="button" onClick={() => addKind(k.kind)}
                    className="flex items-center gap-2 rounded-xl border border-white/10 hover:border-cyan-400/50 hover:bg-cyan-400/5 px-3 py-2.5 text-left text-xs font-semibold text-slate-200 focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300">
                    <Icon size={14} className="text-cyan-300 shrink-0" aria-hidden="true" />
                    <span className="flex-1">{t(k.label)}</span>
                    <Plus size={12} className="text-slate-500" aria-hidden="true" />
                  </button>
                )
              })}
            </div>
          </fieldset>

          {draft.actions.length > 0 && (
            <div className="space-y-3">
              {draft.actions.map(a => (
                <ActionEditor key={a.key} a={a} order={order} onChange={patch => setAction(a.key, patch)} onRemove={() => removeAction(a.key)} />
              ))}
            </div>
          )}

          <fieldset className="space-y-2">
            <legend className={labelCls}>{t('Who approves it?')}</legend>
            <RouteOption value="POLICY" draft={draft} setD={setD} disabled={!policyOk}
              title={t('According to the return policy')}
              desc={policyOk ? routeText.POLICY : t('Only for a returnable order with a returned item that passed the check.')}>
              {policyOk && draft.route === 'POLICY' && (
                <label className="block mt-2">
                  <span className={labelCls}>{t('Returned item that passed the check')}</span>
                  <select value={draft.qcReference} onChange={e => setD({ qcReference: e.target.value })} className={inputCls}>
                    {qc.map(r => (
                      <option key={r.performanceId} value={r.performanceId}>
                        {(returnItems(order, r) || t('Returned items'))} · {t(returnStatusLabel(r.status))}
                      </option>
                    ))}
                  </select>
                </label>
              )}
            </RouteOption>
            <RouteOption value="EXPLICIT" draft={draft} setD={setD} title={t("Needs the buyer's approval")} desc={routeText.EXPLICIT} />
            <RouteOption value="UNILATERAL" draft={draft} setD={setD} secondary title={t('Covered by you (no approval needed)')} desc={routeText.UNILATERAL} />
          </fieldset>

          {problems.length > 0 && draft.actions.length > 0 && (
            <ul className="text-xs text-amber-200 space-y-1" aria-live="polite">
              {[...new Set(problems)].map(p => <li key={p} className="flex items-center gap-1.5"><AlertCircle size={12} aria-hidden="true" /> {p}</li>)}
            </ul>
          )}

          <div className="flex flex-wrap justify-end gap-2">
            <button type="button" onClick={onClose} className={btnSecondary}>{t('Later')}</button>
            <button type="button" disabled={!ok} onClick={() => setStep('preview')} className={btnPrimary} style={{ background: BRAND }}>
              {t('Preview')} <ChevronRight size={14} aria-hidden="true" />
            </button>
          </div>
          <p className="text-[11px] text-slate-500 text-right">{t('Your input is kept if you close this window.')}</p>
        </div>
      ) : (
        <div className="space-y-5">
          <div>
            <p className={labelCls}>{t('What the buyer will see')}</p>
            <div className="rounded-xl border border-cyan-400/30 bg-cyan-400/[0.05] px-4 py-3 text-sm text-white">
              {t('The seller offers {what}.', { what: sentence })}
            </div>
          </div>
          <ul className="text-xs text-slate-300 space-y-1.5">
            <li className="flex items-start gap-2"><ShieldCheck size={13} className="mt-0.5 shrink-0 text-slate-400" aria-hidden="true" /> {routeText[draft.route]}</li>
            {refundTotal > 0 && (
              <li className="flex items-start gap-2"><Wallet size={13} className="mt-0.5 shrink-0 text-slate-400" aria-hidden="true" /> {t('Total refund {amount}, paid by the seller.', { amount: rupiah(refundTotal) })}</li>
            )}
            <li className="flex items-start gap-2"><Lock size={13} className="mt-0.5 shrink-0 text-slate-400" aria-hidden="true" /> {t('Once sent, this offer cannot be changed. You can only send a new version.')}</li>
          </ul>
          {failure && <ErrorNote message={failure.message} onRetry={failure.retry} />}
          <div className="flex flex-wrap justify-end gap-2">
            <button type="button" onClick={() => setStep('edit')} disabled={busy} className={btnSecondary}><ArrowLeft size={14} aria-hidden="true" /> {t('Back to edit')}</button>
            <PrimaryButton busy={busy} icon={Send} onClick={() => onSend(body())}>
              {refundTotal > 0 ? t('Send offer of {amount}', { amount: rupiah(refundTotal) }) : t('Send to buyer')}
            </PrimaryButton>
          </div>
        </div>
      )}
    </Modal>
  )
}

function RouteOption({ value, draft, setD, title, desc, disabled = false, secondary = false, children }) {
  const checked = draft.route === value
  return (
    <div className={`rounded-xl border px-3 py-2.5 ${checked ? 'border-cyan-400/50 bg-cyan-400/[0.05]' : 'border-white/10'} ${disabled ? 'opacity-50' : ''} ${secondary ? 'ml-0 sm:ml-6' : ''}`}>
      <label className={`flex items-start gap-2 ${disabled ? 'cursor-not-allowed' : 'cursor-pointer'}`}>
        <input type="radio" name="route" value={value} checked={checked} disabled={disabled}
          onChange={() => setD({ route: value })} className="mt-1 accent-cyan-400" />
        <span>
          <span className={`block ${secondary ? 'text-xs' : 'text-sm'} font-semibold text-slate-100`}>{title}</span>
          <span className="block text-[11px] text-slate-400">{desc}</span>
        </span>
      </label>
      {children}
    </div>
  )
}

function ActionEditor({ a, order, onChange, onRemove }) {
  const { t } = useI18n()
  const k = kindOf(a.kind)
  const Icon = k.icon
  const lines = order.lines || []
  const line = lineOf(order, a.commitmentId)
  const qty = Number(a.quantity) || line?.quantity || 1
  const paid = line ? line.unitPrice * Math.min(qty, line.quantity) : 0
  const amt = Number(a.amount)
  return (
    <div className="rounded-xl border border-white/[0.08] bg-white/[0.02] p-4 space-y-3">
      <div className="flex items-center gap-2">
        <Icon size={14} className="text-cyan-300" aria-hidden="true" />
        <p className="flex-1 text-sm font-semibold text-white">{t(k.label)}</p>
        <button type="button" onClick={onRemove} aria-label={t('Remove {what}', { what: t(k.label) })}
          className="p-1.5 rounded-lg text-slate-500 hover:text-red-300"><Trash2 size={14} aria-hidden="true" /></button>
      </div>

      {k.line && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <label className="sm:col-span-2">
            <span className={labelCls}>{t('Item')}</span>
            <select value={a.commitmentId} onChange={e => {
              const l = lineOf(order, e.target.value)
              onChange({ commitmentId: e.target.value, quantity: String(l?.quantity || 1) })
            }} className={inputCls}>
              {!a.commitmentId && <option value="">{t('Choose an item')}</option>}
              {lines.map(l => <option key={l.commitmentId} value={l.commitmentId}>{l.name} ({itemText(l)} · {t('{price} each', { price: rupiah(l.unitPrice) })})</option>)}
            </select>
          </label>
          <label>
            <span className={labelCls}>{t('Quantity')}</span>
            <input type="number" min={1} max={line?.quantity || 1} step={1} value={a.quantity}
              onChange={e => onChange({ quantity: e.target.value })} className={inputCls} />
          </label>
        </div>
      )}

      {a.kind === 'REFUND_FULL' && line && (
        <p className="text-xs text-slate-300">
          {t('Amount')}: <span className="font-semibold text-white">{rupiah(paid)}</span>
          <span className="text-slate-500"> ({t('{price} × {qty}', { price: rupiah(line.unitPrice), qty })})</span>
        </p>
      )}

      {a.kind === 'REFUND_PARTIAL' && (
        <div>
          <label className="block">
            <span className={labelCls}>{t('Amount (Rp)')}</span>
            <input type="number" min={1} max={paid || undefined} step={1} inputMode="numeric" value={a.amount}
              onChange={e => onChange({ amount: e.target.value })} className={inputCls} />
          </label>
          {line && <p className="text-[11px] text-slate-500 mt-1">{t('At most {max}, what the buyer paid for these items.', { max: rupiah(paid) })}</p>}
          {line && amt > paid + 0.005 && (
            <p className="text-[11px] text-red-300 mt-1 flex flex-wrap items-center gap-2">
              <AlertTriangle size={11} aria-hidden="true" /> {t('More than the buyer paid for these items ({max}).', { max: rupiah(paid) })}
              <button type="button" className="underline font-semibold" onClick={() => onChange({ amount: String(Math.round(paid)) })}>{t('Use {max}', { max: rupiah(paid) })}</button>
            </p>
          )}
          {line && amt <= paid + 0.005 && amt > line.unitPrice && qty > 1 && (
            <p className="text-[11px] text-amber-300 mt-1 flex items-center gap-1">
              <AlertTriangle size={11} aria-hidden="true" /> {t('More than the price of one item ({price}).', { price: rupiah(line.unitPrice) })}
            </p>
          )}
        </div>
      )}

      {isRefund(a.kind) && <p className="text-[11px] text-slate-400">{t('Paid by the seller')}</p>}

      {a.kind === 'INFORMATION' && (
        <label className="block">
          <span className={labelCls}>{t('Explanation for the buyer')}</span>
          <textarea value={a.description} onChange={e => onChange({ description: e.target.value })} rows={3} maxLength={1000} className={inputCls} />
        </label>
      )}
    </div>
  )
}

// ─── order drawer: complaints about an order ─────────────────────────────────

// OrderGrievances lists an order's complaints and lets the seller report a
// problem with one of its returns. returns: [{performanceId, status}].
export function OrderGrievances({ contractId, returns = [], onOpen }) {
  const { t } = useI18n()
  const { data, loading, error, reload } = useResource(contractId ? `/orders/${encodeURIComponent(contractId)}/grievances` : null)
  const [report, setReport] = useState(null) // form input, kept on failure
  const [sending, setSending] = useState(false)
  const [failure, setFailure] = useState(null)
  const [sent, setSent] = useState(null)
  const items = data?.items || []

  const start = returnId => {
    setSent(null); setFailure(null)
    setReport(r => r && r.returnId === returnId ? r : { category: returnId ? 'WRONG_RETURN' : 'OTHER', returnId: returnId || '', description: '' })
  }
  const submit = async e => {
    e.preventDefault()
    if (!report.description.trim()) return
    setSending(true); setFailure(null)
    try {
      const d = await apiFetch(`/orders/${encodeURIComponent(contractId)}/grievances`, {
        method: 'POST',
        body: JSON.stringify({ category: report.category, description: report.description.trim(), ...(report.returnId ? { returnId: report.returnId } : {}) }),
      })
      setReport(null); setSent(d?.id || null)
      reload()
    } catch (err) {
      setFailure({ message: errorText(err, t), retry: err.code === 'SEND_FAILED' || !err.status })
    } finally {
      setSending(false)
    }
  }

  return (
    <section>
      <Title>{t('Complaints')}</Title>
      <Card className="p-4 space-y-4">
        {loading ? <Spinner /> : error ? <ErrorNote message={errorText(error, t)} onRetry={() => reload()} /> : !items.length ? (
          <p className="text-xs text-slate-500">{t('No complaints about this order.')}</p>
        ) : (
          <ul className="space-y-2">
            {items.map(g => (
              <li key={g.id} className="flex flex-wrap items-center gap-2 text-xs">
                <CategoryChip category={g.category} />
                <StatusChip g={g} />
                <span className="text-slate-500">{t(whoText(g))}</span>
                {onOpen && (
                  <button type="button" onClick={() => onOpen(g.id)} className="ml-auto inline-flex items-center gap-1 font-semibold text-cyan-300">
                    {t('Open')} <ChevronRight size={12} aria-hidden="true" />
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}

        {sent && (
          <p role="status" className="text-xs text-emerald-300 flex flex-wrap items-center gap-2">
            <CheckCircle2 size={12} aria-hidden="true" /> {t('Reported. The buyer app has been told.')}
            {onOpen && <button type="button" onClick={() => onOpen(sent)} className="underline font-semibold">{t('Open')}</button>}
          </p>
        )}

        {returns.length > 0 && (
          <div className="border-t border-white/[0.06] pt-3 space-y-2">
            <p className="text-[11px] font-semibold text-slate-400">{t('Returns')}</p>
            {returns.map((r, i) => (
              <div key={r.performanceId} className="flex flex-wrap items-center gap-2 text-xs">
                <span className="text-slate-300">{t('Return {n}', { n: i + 1 })}</span>
                <span className="text-slate-500">· {t(returnStatusLabel(r.status))}</span>
                <button type="button" onClick={() => start(r.performanceId)}
                  className="ml-auto inline-flex items-center gap-1 px-2.5 py-1 rounded-lg border border-amber-500/30 text-amber-200 hover:bg-amber-500/10 font-semibold">
                  <Flag size={12} aria-hidden="true" /> {t('Report a problem')}
                </button>
              </div>
            ))}
          </div>
        )}

        {report && (
          <form onSubmit={submit} className="border-t border-white/[0.06] pt-3 space-y-3">
            <label className="block">
              <span className={labelCls}>{t('Problem')}</span>
              <select value={report.category} onChange={e => setReport(r => ({ ...r, category: e.target.value }))} className={inputCls}>
                {SELLER_CATEGORIES.map(c => <option key={c} value={c}>{t(categoryLabel(c))}</option>)}
              </select>
            </label>
            {returns.length > 0 && (
              <label className="block">
                <span className={labelCls}>{t('Return')}</span>
                <select value={report.returnId} onChange={e => setReport(r => ({ ...r, returnId: e.target.value }))} className={inputCls}>
                  <option value="">{t('Not about a return')}</option>
                  {returns.map((r, i) => <option key={r.performanceId} value={r.performanceId}>{t('Return {n}', { n: i + 1 })} · {t(returnStatusLabel(r.status))}</option>)}
                </select>
              </label>
            )}
            <label className="block">
              <span className={labelCls}>{t('What is wrong?')}</span>
              <textarea value={report.description} onChange={e => setReport(r => ({ ...r, description: e.target.value }))}
                rows={3} maxLength={1500} required className={inputCls} autoFocus
                placeholder={t('For example: the returned item is not the one we sent.')} />
            </label>
            {failure && <ErrorNote message={failure.message} onRetry={failure.retry ? () => submit({ preventDefault() { } }) : null} />}
            <div className="flex flex-wrap gap-2">
              <PrimaryButton type="submit" busy={sending} disabled={!report.description.trim()} icon={Send}>{t('Send report')}</PrimaryButton>
              <button type="button" className={btnSecondary} onClick={() => { setReport(null); setFailure(null) }}>{t('Cancel')}</button>
            </div>
          </form>
        )}

        {!report && (
          <button type="button" onClick={() => start('')} className="text-xs font-semibold text-slate-400 hover:text-white inline-flex items-center gap-1">
            <Flag size={12} aria-hidden="true" /> {t('Report another problem with this order')}
          </button>
        )}
      </Card>
    </section>
  )
}
