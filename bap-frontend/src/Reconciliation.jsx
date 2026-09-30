import { useCallback, useEffect, useState } from 'react';
import { Landmark, RefreshCw, ChevronDown, Loader2 } from 'lucide-react';
import { useI18n, LanguageToggle, localeNow } from './i18n.jsx';

// Operator view: settlement reconciliation with each seller (BPP) -- what the
// payment gateway still holds for them, what has been released, and every
// /reconcile batch received with this buyer app's verdicts and payout
// (Beckn extension, docs/specs/reconcile.yaml). Served at /reconciliation,
// not linked from the shop; open to anyone (no login or key).

const rupiah = n => (n == null ? '—' : `Rp ${Number(n).toLocaleString('id-ID')}`);
const fmtWhen = ts => (ts ? new Date(ts).toLocaleString(localeNow(), { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' }) : '—');
const parse = v => { if (typeof v !== 'string') return v; try { return JSON.parse(v); } catch { return v; } };

const STATUS = {
  AGREED: 'bg-emerald-500/15 text-emerald-300 border-emerald-500/30',
  DISPUTED: 'bg-rose-500/15 text-rose-300 border-rose-500/30',
  ADJUSTED: 'bg-violet-500/15 text-violet-300 border-violet-500/30',
  PENDING: 'bg-amber-500/15 text-amber-300 border-amber-500/30',
};
// Verdict / batch statuses (the protocol values) as shown.
const STATUS_LABELS = { AGREED: 'Agreed', DISPUTED: 'Disputed', ADJUSTED: 'Adjusted', PENDING: 'Pending' };
// Order (BAP transaction) and payout statuses as shown; other codes as they are.
const ORDER_STATUS_LABELS = {
  CONFIRMED: 'Confirmed', CANCELLED: 'Cancelled', COMPLETE: 'Completed',
  UPDATE_SENT: 'Updating', CANCEL_SENT: 'Cancelling', STATUS_SENT: 'Checking status',
};
const PAYOUT_STATUS_LABELS = { COMMITTED: 'committed', COMPLETE: 'completed' };
const label = (t, table, code) => (table[code] ? t(table[code]) : code);

function Badge({ status }) {
  const { t } = useI18n();
  return (
    <span className={`inline-flex px-2 py-0.5 rounded-full text-[11px] font-bold border ${STATUS[status] || 'bg-slate-500/15 text-slate-300 border-slate-500/30'}`}>
      {STATUS_LABELS[status] ? t(STATUS_LABELS[status]) : status || '—'}
    </span>
  );
}

function Json({ value }) {
  const { t } = useI18n();
  return (
    <pre className="max-h-96 overflow-auto p-3 text-[11px] leading-relaxed font-mono text-slate-300 whitespace-pre">
      {value == null ? t('Nothing yet') : JSON.stringify(parse(value), null, 2)}
    </pre>
  );
}

function BatchRow({ b, currency }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const claimed = parse(b.claimed_summary) || {};
  const computed = parse(b.computed_summary) || {};
  const payout = parse(b.payout);
  const contracts = parse(b.contracts) || [];
  const matches = claimed.netPayable === computed.netPayable;
  return (
    <div className="border-t border-white/[0.05]">
      <button onClick={() => setOpen(o => !o)} className="w-full text-left px-5 py-3 flex flex-wrap items-center gap-3 hover:bg-white/[0.02]">
        <ChevronDown className={`w-4 h-4 text-slate-500 transition-transform ${open ? 'rotate-180 text-cyan-400' : ''}`} />
        <span className="font-mono text-xs text-slate-300 truncate max-w-[20rem]" title={b.id}>{b.id}</span>
        <Badge status={b.status} />
        <span className="text-xs text-slate-500">{contracts.length === 1 ? t('1 contract') : t('{count} contracts', { count: contracts.length })}</span>
        <span className="ml-auto text-xs text-slate-400">
          {t('claimed')} <span className="tabular-nums text-slate-200">{rupiah(claimed.netPayable)}</span>
          {' · '}{t('ours')} <span className={`tabular-nums ${matches ? 'text-slate-200' : 'text-rose-300'}`}>{rupiah(computed.netPayable)}</span>
        </span>
        <span className="text-xs font-semibold tabular-nums text-emerald-300 w-28 text-right">{payout ? rupiah(payout.amount) : t('not paid')}</span>
        <span className="text-[11px] text-slate-500 whitespace-nowrap">{fmtWhen(b.created_at)}</span>
      </button>
      {b.error_message && <p className="px-5 pb-2 text-xs text-rose-300 break-words">{t('on_reconcile not delivered: {error}', { error: b.error_message })}</p>}
      {open && (
        <div className="px-5 pb-5 space-y-3">
          <div className="overflow-x-auto rounded-xl border border-white/[0.07]">
            <table className="w-full text-xs">
              <thead>
                <tr className="text-[10px] uppercase tracking-widest text-slate-500 border-b border-white/[0.06]">
                  <th className="text-left px-3 py-2">{t('Contract')}</th>
                  <th className="text-left px-3 py-2">{t('Verdict')}</th>
                  <th className="text-right px-3 py-2">{t('Claimed')}</th>
                  <th className="text-left px-3 py-2">{t('Dispute')}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/[0.04]">
                {contracts.map(v => (
                  <tr key={v.contractId}>
                    <td className="px-3 py-2 font-mono text-slate-400" title={v.contractId}>{String(v.contractId).slice(0, 8)}…</td>
                    <td className="px-3 py-2"><Badge status={v.reconciliationStatus} /></td>
                    <td className="px-3 py-2 text-right tabular-nums whitespace-nowrap text-slate-200">{rupiah(v.claimedAmount)}</td>
                    <td className="px-3 py-2 text-slate-400">{v.dispute ? `${v.dispute.code}: ${v.dispute.name}` : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {payout && (
            <p className="text-xs text-slate-400">
              {t('Payout {status} · {amount} {currency} via {method}', { status: label(t, PAYOUT_STATUS_LABELS, payout.status), amount: rupiah(payout.amount), currency: payout.currency || currency, method: payout.method })} ·
              {' '}{t('gateway')} <span className="font-mono text-slate-300">{payout.gateway?.name}</span> {t('ref')}{' '}
              <span className="font-mono text-slate-300">{payout.gateway?.transactionId}</span> · {fmtWhen(payout.paidAt)}
            </p>
          )}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
            {[[t('/reconcile received'), b.request_payload], [t('/on_reconcile sent'), b.response_payload]].map(([label, p]) => (
              <div key={label} className="rounded-xl border border-white/[0.07] bg-slate-950/60 overflow-hidden">
                <p className="px-3 py-2 text-[11px] font-semibold text-slate-400 border-b border-white/[0.06]">{label}</p>
                <Json value={p} />
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function BppCard({ v }) {
  const { t } = useI18n();
  const [showHeld, setShowHeld] = useState(false);
  return (
    <section className="rounded-2xl border border-white/10 bg-white/[0.02] overflow-hidden">
      <div className="px-5 py-3 border-b border-white/[0.06]">
        <p className="text-[10px] font-bold uppercase tracking-widest text-slate-500">{t('Seller (BPP)')}</p>
        <p className="text-sm font-mono text-white break-all">{v.bpp_id || t('(unknown)')}</p>
      </div>
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-px bg-white/[0.04]">
        {[
          [t('Held at gateway'), rupiah(v.held_amount), 'text-amber-300'],
          [t('Orders held'), v.held_orders.length, 'text-white'],
          [t('Released to seller'), rupiah(v.released), 'text-emerald-300'],
          [t('Disputed batches'), v.disputed_batches, v.disputed_batches ? 'text-rose-300' : 'text-white'],
        ].map(([label, value, cls]) => (
          <div key={label} className="bg-slate-950/40 px-4 py-3">
            <p className="text-[10px] text-slate-500">{label}</p>
            <p className={`text-sm font-bold tabular-nums ${cls}`}>{value}</p>
          </div>
        ))}
      </div>

      {v.held_orders.length > 0 && (
        <div>
          <button onClick={() => setShowHeld(s => !s)} className="w-full text-left px-5 py-2.5 text-[11px] font-bold uppercase tracking-widest text-amber-300 flex items-center gap-2 hover:bg-white/[0.02]">
            <ChevronDown className={`w-3.5 h-3.5 transition-transform ${showHeld ? 'rotate-180' : ''}`} /> {t('Held orders')}
          </button>
          {showHeld && (
            <div className="overflow-x-auto">
              <table className="w-full text-xs">
                <thead>
                  <tr className="text-[10px] uppercase tracking-widest text-slate-500 border-b border-white/[0.06]">
                    <th className="text-left px-5 py-2">{t('Order')}</th>
                    <th className="text-left px-3 py-2">{t('Status')}</th>
                    <th className="text-right px-3 py-2">{t('Paid')}</th>
                    <th className="text-right px-3 py-2">{t('Gateway refund')}</th>
                    <th className="text-right px-3 py-2">{t('Held')}</th>
                    <th className="text-left px-3 py-2">{t('In batch')}</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-white/[0.04]">
                  {v.held_orders.map(o => (
                    <tr key={o.transaction_id}>
                      <td className="px-5 py-2 font-mono text-slate-400" title={o.transaction_id}>{o.transaction_id.slice(0, 8)}…</td>
                      <td className="px-3 py-2 text-slate-400">{label(t, ORDER_STATUS_LABELS, o.status)}</td>
                      <td className="px-3 py-2 text-right tabular-nums whitespace-nowrap text-slate-300">{rupiah(o.paid)}</td>
                      <td className="px-3 py-2 text-right tabular-nums whitespace-nowrap text-rose-300">{o.gateway_refund ? `− ${rupiah(o.gateway_refund)}` : '—'}</td>
                      <td className="px-3 py-2 text-right tabular-nums whitespace-nowrap font-semibold text-amber-300">{rupiah(o.held)}</td>
                      <td className="px-3 py-2 font-mono text-slate-500 truncate max-w-[14rem]">{o.in_batch || t('not yet')}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      <p className="px-5 pt-3 pb-2 text-[11px] font-bold uppercase tracking-widest text-slate-500 border-t border-white/[0.06]">{t('Batches received')}</p>
      {v.batches.length === 0
        ? <p className="px-5 pb-4 text-xs text-slate-500">{t('No /reconcile received from this seller yet.')}</p>
        : v.batches.map(b => <BatchRow key={b.id} b={b} currency={v.currency} />)}
    </section>
  );
}

export default function Reconciliation() {
  const { t } = useI18n();
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const r = await fetch('/api/v1/reconciliation');
      const body = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(body.error || `HTTP ${r.status}`);
      setData(body); setError(null);
    } catch (e) {
      setError(e.message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }, [load]);

  const bpps = data?.bpps || [];
  return (
    <div className="min-h-screen max-w-6xl mx-auto px-4 py-6 space-y-5">
      <header className="flex flex-wrap items-center gap-3">
        <Landmark className="w-6 h-6 text-cyan-400" />
        <div>
          <h1 className="text-xl font-bold text-white">{t('Reconciliation')}</h1>
          <p className="text-xs text-slate-400">{t('Settlement with each seller: funds held by the payment gateway, releases, and /reconcile batches')}</p>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <LanguageToggle />
          <button onClick={load} className="flex items-center gap-1.5 text-xs text-slate-300 hover:text-white px-3 py-1.5 rounded-lg border border-white/10">
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} /> {t('Refresh')}
          </button>
        </div>
      </header>

      {error && <p className="rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-300">{error}</p>}
      {!data && loading && <div className="flex justify-center py-16"><Loader2 className="w-8 h-8 text-cyan-400 animate-spin" /></div>}
      {data && bpps.length === 0 && <p className="text-sm text-slate-500 text-center py-16">{t('No settlements yet.')}</p>}
      {bpps.map(v => <BppCard key={v.bpp_id} v={v} />)}
    </div>
  );
}
