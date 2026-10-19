import { useCallback, useEffect, useState } from 'react';
import { Landmark, RefreshCw, ChevronDown, Loader2, Send } from 'lucide-react';
import { useI18n, LanguageToggle, localeNow } from './i18n.jsx';
import { basePath } from './auth.js';
import { useReconSelection, inDateRange } from './reconSelection.js';
import { useLive } from './live.js';

// Operator view: settlement reconciliation with each seller (BPP), per ION
// Release 1 core/api/v2.0.0/ion-additional.yaml in ion-specs. The buyer's payment
// is held at the PJP; either side sends /reconcile with its figures and the
// other answers with accord per contract. Once both agree, SEAM has the PJP
// pay the seller its net payable and this buyer app its finder fee. Served at
// /reconciliation, not linked from the shop; open to anyone (no login or key).

const rupiah = n => (n == null ? '—' : `Rp ${Number(n).toLocaleString('id-ID')}`);
const fmtWhen = ts => (ts ? new Date(ts).toLocaleString(localeNow(), { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' }) : '—');
const parse = v => {
  if (typeof v !== 'string') return v;
  try { return JSON.parse(v); } catch { return v; }
};
// A contract's dispute as "CODE: description" (just the code without one).
const disputeText = d => (d.description ? `${d.code}: ${d.description}` : `${d.code}`);

const STATUS = {
  AGREED: 'bg-emerald-500/15 text-emerald-300 border-emerald-500/30',
  PARTIALLY_AGREED: 'bg-violet-500/15 text-violet-300 border-violet-500/30',
  DISPUTED: 'bg-rose-500/15 text-rose-300 border-rose-500/30',
  PENDING: 'bg-amber-500/15 text-amber-300 border-amber-500/30',
  SENT: 'bg-amber-500/15 text-amber-300 border-amber-500/30',
  SENDING: 'bg-amber-500/15 text-amber-300 border-amber-500/30',
  FAILED: 'bg-rose-500/15 text-rose-300 border-rose-500/30',
};
// Batch statuses (the protocol values, plus delivery states of a sent batch) as shown.
const STATUS_LABELS = {
  AGREED: 'Agreed', PARTIALLY_AGREED: 'Partially agreed', DISPUTED: 'Disputed', PENDING: 'Not due yet',
  SENT: 'Awaiting answer', SENDING: 'Sending', FAILED: 'Not delivered',
};
// Order (BAP transaction) statuses as shown; other codes as they are.
const ORDER_STATUS_LABELS = {
  CONFIRMED: 'Confirmed', CANCELLED: 'Cancelled', COMPLETE: 'Completed',
  UPDATE_SENT: 'Updating', CANCEL_SENT: 'Cancelling', STATUS_SENT: 'Checking status',
};
const label = (t, table, code) => (table[code] ? t(table[code]) : code);

// In a disputed batch only the contracts with accord false are in dispute;
// last_result says which, and the figures that differ.
const isDisputed = c => c.reconciliation_status === 'DISPUTED' && !c.last_result?.accord;
// Sent by the seller before its settlement window: answered accord false; it
// waits until it is due (no reconciliation status).
const sentEarly = c => !c.reconciliation_status && c.last_result?.code === 'SETTLEMENT_WINDOW_NOT_REACHED';
// Matched, but its batch was PENDING (another order wasn't due): not agreed,
// sent again with the rest.
const matchedPending = c => !c.reconciliation_status && c.last_result?.accord === true;
const diffOf = (c, field) => c.reconciliation_status === 'DISPUTED' && c.last_result?.diffs?.find(d => d.field === field);

function DiffNote({ d }) {
  const { t } = useI18n();
  if (!d) return null;
  return (
    <div className="text-[10px] font-semibold text-rose-300" title={t('Your figure: {ours}', { ours: rupiah(d.ours) })}>
      {t('Seller: {amount}', { amount: rupiah(d.theirs) })} ({d.diff > 0 ? '+' : '−'}{rupiah(Math.abs(d.diff))})
    </div>
  );
}

function Badge({ status }) {
  const { t } = useI18n();
  return (
    <span className={`inline-flex px-2 py-0.5 rounded-full text-[11px] font-bold border ${STATUS[status] || 'bg-slate-500/15 text-slate-300 border-slate-500/30'}`}>
      {STATUS_LABELS[status] ? t(STATUS_LABELS[status]) : status || '—'}
    </span>
  );
}

function Accord({ accord }) {
  const { t } = useI18n();
  return accord
    ? <span className="text-emerald-300 font-semibold">{t('Agreed')}</span>
    : <span className="text-rose-300 font-semibold">{t('Disputed')}</span>;
}

function Json({ value }) {
  const { t } = useI18n();
  return (
    <pre className="max-h-96 overflow-auto p-3 text-[11px] leading-relaxed font-mono text-slate-300 whitespace-pre">
      {value == null ? t('Nothing yet') : JSON.stringify(parse(value), null, 2)}
    </pre>
  );
}

function BatchRow({ b }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const sent = b.direction === 'OUTBOUND';
  const claimed = parse(b.claimed_summary) || {};
  const answered = parse(b.computed_summary) || {};
  const verdicts = parse(b.contracts) || [];
  const count = verdicts.length || claimed.contractCount || 0;
  return (
    <div className="border-t border-white/[0.05]">
      <button onClick={() => setOpen(o => !o)} className="w-full text-left px-5 py-3 flex flex-wrap items-center gap-3 hover:bg-white/[0.02]">
        <ChevronDown className={`w-4 h-4 text-slate-500 transition-transform ${open ? 'rotate-180 text-cyan-400' : ''}`} />
        <span className={`text-[10px] font-bold uppercase tracking-widest ${sent ? 'text-cyan-300' : 'text-slate-400'}`}>{sent ? t('Sent') : t('Received')}</span>
        <span className="font-mono text-xs text-slate-300 truncate max-w-[20rem]" title={b.id}>{b.id}</span>
        <Badge status={b.status} />
        <span className="text-xs text-slate-500">{count === 1 ? t('1 contract') : t('{count} contracts', { count })}</span>
        {answered.agreedCount != null && (
          <span className="text-xs">
            <span className="text-emerald-300">{t('{count} agreed', { count: answered.agreedCount })}</span>
            {answered.disputedCount > 0 && <span className="text-rose-300">{' · '}{t('{count} disputed', { count: answered.disputedCount })}</span>}
          </span>
        )}
        <span className="ml-auto text-xs text-slate-400">
          {t('to seller')} <span className="tabular-nums text-slate-200">{rupiah(claimed.totalNetPayable)}</span>
          {' · '}{t('finder fee')} <span className="tabular-nums text-slate-200">{rupiah(claimed.totalCommission)}</span>
        </span>
        <span className="text-[11px] text-slate-500 whitespace-nowrap">{fmtWhen(b.created_at)}</span>
      </button>
      {b.error_message && <p className="px-5 pb-2 text-xs text-rose-300 break-words">{t('Not delivered: {error}', { error: b.error_message })}</p>}
      {open && (
        <div className="px-5 pb-5 space-y-3">
          {verdicts.length > 0 && (
            <div className="overflow-x-auto rounded-xl border border-white/[0.07]">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-[10px] uppercase tracking-widest text-slate-500 border-b border-white/[0.06]">
                    <th className="text-left px-3 py-2">{t('Contract')}</th>
                    <th className="text-left px-3 py-2">{t('Accord')}</th>
                    <th className="text-right px-3 py-2">{t('To seller')}</th>
                    <th className="text-right px-3 py-2">{t('Finder fee')}</th>
                    <th className="text-left px-3 py-2">{t('Dispute')}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-white/[0.04]">
                  {verdicts.map(v => (
                    <tr key={v.contractId}>
                      <td className="px-3 py-2 font-mono text-slate-400" title={v.contractId}>{String(v.contractId).slice(0, 8)}…</td>
                      <td className="px-3 py-2"><Accord accord={v.accord} /></td>
                      <td className="px-3 py-2 text-right tabular-nums whitespace-nowrap text-slate-200">{rupiah(v.netPayable)}</td>
                      <td className="px-3 py-2 text-right tabular-nums whitespace-nowrap text-slate-200">{rupiah(v.commission)}</td>
                      <td className="px-3 py-2 text-slate-400">{v.dispute ? disputeText(v.dispute) : '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {answered.totalNetPayable != null && answered.totalNetPayable !== claimed.totalNetPayable && (
            <p className="text-xs text-rose-300">
              {t('The answer computes {amount} to the seller', { amount: rupiah(answered.totalNetPayable) })}
            </p>
          )}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
            {[[sent ? t('/reconcile sent') : t('/reconcile received'), b.request_payload],
              [sent ? t('/on_reconcile received') : t('/on_reconcile sent'), b.response_payload]].map(([title, p]) => (
              <div key={title} className="rounded-xl border border-white/[0.07] bg-slate-950/60 overflow-hidden">
                <p className="px-3 py-2 text-[11px] font-semibold text-slate-400 border-b border-white/[0.06]">{title}</p>
                <Json value={p} />
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function BppCard({ v, onSend, sending, visible }) {
  const { t } = useI18n();
  const [showOpen, setShowOpen] = useState(true);
  // What the next /reconcile carries: the ticked contracts -- one disputed
  // batch this buyer app sent, in full, or contracts not yet in a batch.
  const sel = useReconSelection(v.contracts, visible);
  const dueCount = sel.ticked.length;
  const shown = v.contracts.filter(visible);
  return (
    <section className="rounded-2xl border border-white/10 bg-white/[0.02] overflow-hidden">
      <div className="px-5 py-3 border-b border-white/[0.06] flex flex-wrap items-center gap-3">
        <div>
          <p className="text-[10px] font-bold uppercase tracking-widest text-slate-500">{t('Seller (BPP)')}</p>
          <p className="text-sm font-mono text-white break-all">{v.bpp_id || t('(unknown)')}</p>
        </div>
        <button
          onClick={() => onSend(v.bpp_id, sel.ticked.map(c => c.contract_id))}
          disabled={!dueCount || sending}
          className="ml-auto flex items-center gap-1.5 text-xs font-semibold px-3 py-1.5 rounded-lg border border-cyan-500/40 text-cyan-300 hover:bg-cyan-500/10 disabled:opacity-40 disabled:cursor-not-allowed"
          title={dueCount ? '' : v.contracts.some(c => c.selectable) ? t('No contract selected') : t('No contract is due yet')}
        >
          {sending ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Send className="w-3.5 h-3.5" />}
          {dueCount < sel.total ? t('Send /reconcile ({count} of {total})', { count: dueCount, total: sel.total })
            : t('Send /reconcile ({count})', { count: dueCount })}
        </button>
      </div>
      <div className="grid grid-cols-2 sm:grid-cols-5 gap-px bg-white/[0.04]">
        {[
          [t('Held at PJP'), rupiah(v.held_amount), 'text-amber-300'],
          [t('Open contracts'), v.contracts.length, 'text-white'],
          [t('Agreed to seller'), rupiah(v.agreed_net_payable), 'text-emerald-300'],
          [t('Agreed finder fee'), rupiah(v.agreed_commission), 'text-emerald-300'],
          [t('Disputed batches'), v.disputed_batches, v.disputed_batches ? 'text-rose-300' : 'text-white'],
        ].map(([title, value, cls]) => (
          <div key={title} className="bg-slate-950/40 px-4 py-3">
            <p className="text-[10px] text-slate-500">{title}</p>
            <p className={`text-sm font-bold tabular-nums ${cls}`}>{value}</p>
          </div>
        ))}
      </div>
      {v.refunded_after_payout > 0 && (
        <p className="px-5 py-2 text-[11px] text-slate-400 border-t border-white/[0.06]">
          {t('Refunded by the seller after payout (outside reconciliation): {amount}', { amount: rupiah(v.refunded_after_payout) })}
        </p>
      )}

      {v.contracts.length > 0 && (
        <div>
          <button onClick={() => setShowOpen(s => !s)} className="w-full text-left px-5 py-2.5 text-[11px] font-bold uppercase tracking-widest text-amber-300 flex items-center gap-2 hover:bg-white/[0.02] border-t border-white/[0.06]">
            <ChevronDown className={`w-3.5 h-3.5 transition-transform ${showOpen ? 'rotate-180' : ''}`} /> {t('Contracts to reconcile')}
          </button>
          {showOpen && (
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-[10px] uppercase tracking-widest text-slate-500 border-b border-white/[0.06]">
                    <th className="pl-5 pr-1 py-2 w-6">
                      <input type="checkbox" className="accent-cyan-500 cursor-pointer disabled:cursor-not-allowed" disabled={sel.header.disabled}
                        checked={sel.header.checked} ref={el => { if (el) el.indeterminate = sel.header.indeterminate; }}
                        onChange={sel.toggleAll} title={t('Select all due and disputed contracts')} />
                    </th>
                    <th className="text-left px-3 py-2">{t('Order')}</th>
                    <th className="text-left px-3 py-2">{t('Created')}</th>
                    <th className="text-left px-3 py-2">{t('Status')}</th>
                    <th className="text-right px-3 py-2">{t('Paid')}</th>
                    <th className="text-right px-3 py-2">{t('Refund')}</th>
                    <th className="text-right px-3 py-2">{t('Finder fee')}</th>
                    <th className="text-right px-3 py-2">{t('To seller')}</th>
                    <th className="text-left px-3 py-2">{t('Due')}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-white/[0.04]">
                  {shown.length === 0 && <tr><td colSpan={9} className="px-5 py-3 text-slate-500">{t('No contract matches the filters.')}</td></tr>}
                  {shown.map(c => (
                    <tr key={c.contract_id}>
                      <td className="pl-5 pr-1 py-2">
                        {c.selectable && (
                          <input type="checkbox" className="accent-cyan-500 cursor-pointer"
                            checked={sel.isTicked(c)} onChange={() => sel.toggle(c)}
                            title={t('Include in the next /reconcile')} />
                        )}
                      </td>
                      <td className="px-3 py-2 font-mono text-slate-400" title={c.transaction_id}>{c.transaction_id.slice(0, 8)}…</td>
                      <td className="px-3 py-2 text-slate-400 whitespace-nowrap">{fmtWhen(c.created_at)}</td>
                      <td className="px-3 py-2 text-slate-400">
                        {label(t, ORDER_STATUS_LABELS, c.status)}
                        {isDisputed(c) && <span className="ml-1.5 text-rose-300 font-semibold">· {t('Disputed')}</span>}
                        {c.reconciliation_status === 'DISPUTED' && !isDisputed(c) && <span className="ml-1.5 text-slate-500">· {t('Matched — batch disputed')}</span>}
                        {sentEarly(c) && <span className="ml-1.5 text-amber-300 font-semibold" title={c.last_result.reason}>· {t('Sent before settlement window')}</span>}
                        {matchedPending(c) && <span className="ml-1.5 text-slate-500">· {t('Matched — batch pending')}</span>}
                        {sentEarly(c) && <div className="mt-0.5 text-[10px] text-amber-300/80 max-w-[18rem]">{c.last_result.reason}</div>}
                        {isDisputed(c) && !c.last_result?.diffs?.length && c.last_result?.reason && <div className="mt-0.5 text-[10px] text-rose-300/80 max-w-[18rem]">{c.last_result.reason}</div>}
                      </td>
                      <td className="px-3 py-2 text-right tabular-nums whitespace-nowrap text-slate-300">{rupiah(c.order_amount)}<DiffNote d={diffOf(c, 'orderAmount')} /></td>
                      <td className="px-3 py-2 text-right tabular-nums whitespace-nowrap text-rose-300">{c.refund ? `− ${rupiah(c.refund)}` : '—'}<DiffNote d={diffOf(c, 'refundAmount')} /></td>
                      <td className="px-3 py-2 text-right tabular-nums whitespace-nowrap text-slate-300">{rupiah(c.commission)}<DiffNote d={diffOf(c, 'commission')} /></td>
                      <td className="px-3 py-2 text-right tabular-nums whitespace-nowrap font-semibold text-amber-300">{rupiah(c.net_payable)}<DiffNote d={diffOf(c, 'netPayable')} /></td>
                      <td className="px-3 py-2 text-slate-400 whitespace-nowrap">{c.selectable ? <span className="text-cyan-300 font-semibold">{t('Due now')}</span> : c.due_at ? fmtWhen(c.due_at) : t(c.basis_event)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      <p className="px-5 pt-3 pb-2 text-[11px] font-bold uppercase tracking-widest text-slate-500 border-t border-white/[0.06]">{t('Batches')}</p>
      {v.batches.length === 0
        ? <p className="px-5 pb-4 text-xs text-slate-500">{t('No /reconcile exchanged with this seller yet.')}</p>
        : v.batches.map(b => <BatchRow key={b.id} b={b} />)}
    </section>
  );
}

function MessageRow({ m }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const sent = m.direction === 'OUTBOUND';
  const nack = m.ack_status === 'NACK';
  const batch = parse(m.request_payload)?.message?.reconciliationId;
  return (
    <div className="border-t border-white/[0.05]">
      <button onClick={() => setOpen(o => !o)} className="w-full text-left px-5 py-2.5 flex flex-wrap items-center gap-3 hover:bg-white/[0.02]">
        <ChevronDown className={`w-4 h-4 text-slate-500 transition-transform ${open ? 'rotate-180 text-cyan-400' : ''}`} />
        <span className="text-[11px] text-slate-500 whitespace-nowrap w-28">{fmtWhen(m.created_at)}</span>
        <span className={`text-[10px] font-bold uppercase tracking-widest w-16 ${sent ? 'text-cyan-300' : 'text-slate-400'}`}>{sent ? t('Sent') : t('Received')}</span>
        <span className="font-mono text-xs text-white w-24">{m.action}</span>
        <span className={`inline-flex px-2 py-0.5 rounded-full text-[11px] font-bold border ${nack ? STATUS.FAILED : STATUS.AGREED}`}>{m.ack_status || '—'}</span>
        <span className="font-mono text-[11px] text-slate-500 truncate max-w-[22rem]" title={batch}>{batch || '—'}</span>
      </button>
      {m.error_message && <p className="px-5 pb-2 text-xs text-rose-300 break-words">{m.error_message}</p>}
      {open && (
        <div className="px-5 pb-4 grid grid-cols-1 lg:grid-cols-2 gap-3">
          {[[t('Request'), m.request_payload], [t('Response'), m.response_payload]].map(([title, p]) => (
            <div key={title} className="rounded-xl border border-white/[0.07] bg-slate-950/60 overflow-hidden">
              <p className="px-3 py-2 text-[11px] font-semibold text-slate-400 border-b border-white/[0.06]">{title}</p>
              <Json value={p} />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export default function Reconciliation() {
  const { t } = useI18n();
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [notice, setNotice] = useState(null);
  const [loading, setLoading] = useState(false);
  const [sendingTo, setSendingTo] = useState(null);
  // Filters: one seller (each gets its own /reconcile) and the contracts'
  // creation date.
  const [np, setNp] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const visible = useCallback(c => inDateRange(c, from, to), [from, to]);

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    try {
      const r = await fetch(`${basePath}api/v1/reconciliation`);
      const body = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`);
      setData(body); setError(null);
    } catch (e) {
      if (!silent) setError(e.message);
    } finally {
      if (!silent) setLoading(false);
    }
  }, []);

  const send = useCallback(async (bppId, contractIds) => {
    setSendingTo(bppId); setNotice(null);
    try {
      const r = await fetch(`${basePath}api/v1/reconciliation/send`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ bpp_id: bppId, contract_ids: contractIds }),
      });
      const body = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`);
      setNotice(t('Sent {id}', { id: body.batch?.id }));
    } catch (e) {
      setError(e.message);
    } finally {
      setSendingTo(null);
      void load(); // load reports its own errors
    }
  }, [load, t]);

  useEffect(() => { void load(); }, [load]);
  useLive(() => load(true));

  const allBpps = data?.bpps || [];
  const bpps = np ? allBpps.filter(v => v.bpp_id === np) : allBpps;
  return (
    <div className="min-h-screen max-w-6xl mx-auto px-4 py-6 space-y-5">
      <header className="flex flex-wrap items-center gap-3">
        <Landmark className="w-6 h-6 text-cyan-400" />
        <div>
          <h1 className="text-xl font-bold text-white">{t('Reconciliation')}</h1>
          <p className="text-xs text-slate-400">{t('Settlement with each seller: payments held at the PJP, /reconcile batches either way, and what agreed contracts release')}</p>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <LanguageToggle />
          <button onClick={() => load()} className="flex items-center gap-1.5 text-xs text-slate-300 hover:text-white px-3 py-1.5 rounded-lg border border-white/10">
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} /> {t('Refresh')}
          </button>
        </div>
      </header>

      {allBpps.length > 0 && (
        <div className="flex flex-wrap items-end gap-3 rounded-2xl border border-white/10 bg-white/[0.02] px-4 py-3">
          <label className="text-[11px] text-slate-400 flex flex-col gap-1">
            {t('Seller (BPP)')}
            <select value={np} onChange={e => setNp(e.target.value)} className="bg-slate-900 border border-white/10 rounded-lg px-2 py-1.5 text-xs text-white">
              <option value="">{t('All sellers ({count})', { count: allBpps.length })}</option>
              {allBpps.map(v => <option key={v.bpp_id} value={v.bpp_id}>{v.bpp_id}</option>)}
            </select>
          </label>
          <label className="text-[11px] text-slate-400 flex flex-col gap-1">
            {t('Created from')}
            <input type="date" value={from} max={to || undefined} onChange={e => setFrom(e.target.value)} className="bg-slate-900 border border-white/10 rounded-lg px-2 py-1.5 text-xs text-white [color-scheme:dark]" />
          </label>
          <label className="text-[11px] text-slate-400 flex flex-col gap-1">
            {t('Created to')}
            <input type="date" value={to} min={from || undefined} onChange={e => setTo(e.target.value)} className="bg-slate-900 border border-white/10 rounded-lg px-2 py-1.5 text-xs text-white [color-scheme:dark]" />
          </label>
          {(np || from || to) && (
            <button onClick={() => { setNp(''); setFrom(''); setTo(''); }} className="text-xs text-slate-400 hover:text-white px-3 py-1.5 rounded-lg border border-white/10">{t('Clear filters')}</button>
          )}
          <p className="text-[11px] text-slate-500 basis-full">{t('Each seller gets its own /reconcile. Tick any due or disputed contracts that match the filters; they go out together as a new batch.')}</p>
        </div>
      )}

      {error && <p className="rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">{error}</p>}
      {notice && <p className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 px-4 py-3 text-sm text-emerald-300 break-all">{notice}</p>}
      {!data && loading && <div className="flex justify-center py-16"><Loader2 className="w-8 h-8 text-cyan-400 animate-spin" /></div>}
      {data && bpps.length === 0 && <p className="text-sm text-slate-500 text-center py-16">{t('No settlements yet.')}</p>}
      {bpps.map(v => <BppCard key={v.bpp_id} v={v} onSend={send} sending={sendingTo === v.bpp_id} visible={visible} />)}

      {data && (
        <section className="rounded-2xl border border-white/10 bg-white/[0.02] overflow-hidden">
          <p className="px-5 pt-3 pb-2 text-[11px] font-bold uppercase tracking-widest text-slate-500">{t('Reconcile message log')}</p>
          {(data.messages || []).length === 0
            ? <p className="px-5 pb-4 text-xs text-slate-500">{t('No /reconcile or /on_reconcile sent or received yet.')}</p>
            : data.messages.map(m => <MessageRow key={m.id} m={m} />)}
        </section>
      )}
    </div>
  );
}
