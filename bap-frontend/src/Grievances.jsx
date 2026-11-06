import { useEffect, useLayoutEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  PackageX, Replace, PackageMinus, ClockAlert, Undo2, Truck, ClipboardX, Wallet, CircleHelp,
  Loader2, X, ChevronLeft, ChevronRight, ChevronDown, Minus, Plus, Send, Hourglass, CircleCheck,
  Lock, TriangleAlert, RefreshCw, LifeBuoy, Inbox, MessageCircle, Phone, ExternalLink, Mail,
  CircleX, HandCoins, Store, Package, Info, RotateCcw, Ban, Copy,
} from 'lucide-react';
import { useI18n } from './i18n.jsx';
import { apiJSON, fmtIDR, useGrievances, useNow } from './api.js';
import { useLive } from './live.js';

// Buyer grievances (complaints about an order): raise one in three steps,
// follow it in "My help", and answer the seller's offer. Plain language only:
// no protocol words or raw ids on screen -- the grievance number is the one
// id a buyer may need (to quote it on the phone).

/* ─────────────────────────── vocabulary ─────────────────────────────────── */

const CATEGORIES = [
  { value: 'DAMAGED_ITEM', label: 'Damaged item', hint: 'Arrived broken, spoiled or not working', Icon: PackageX },
  { value: 'WRONG_ITEM', label: 'Wrong item', hint: 'Not what you ordered', Icon: Replace },
  { value: 'MISSING_ITEM', label: 'Missing item', hint: 'Fewer items than you ordered', Icon: PackageMinus },
  { value: 'LATE_DELIVERY', label: 'Late delivery', hint: 'The order has not arrived on time', Icon: ClockAlert },
  { value: 'RETURN_REJECTED', label: 'Return was rejected', hint: 'You disagree with a rejected return', Icon: Undo2 },
  { value: 'RETURN_PICKUP_FAILED', label: 'Pickup did not come', hint: 'Nobody came to collect your return', Icon: Truck },
  { value: 'RETURN_QC_DISPUTED', label: 'Return check result is wrong', hint: 'You disagree with the check of your return', Icon: ClipboardX },
  { value: 'REFUND_NOT_RECEIVED', label: 'Refund not received', hint: 'The money has not reached you', Icon: Wallet },
  { value: 'OTHER', label: 'Other', hint: 'Something else about this order', Icon: CircleHelp },
];
// Raised by the seller (seen, never chosen by the buyer).
const SELLER_CATEGORIES = [
  { value: 'WRONG_RETURN', label: 'Wrong item returned', Icon: Replace },
];
const categoryOf = c => CATEGORIES.find(x => x.value === c) || SELLER_CATEGORIES.find(x => x.value === c)
  || { value: c, label: 'Other', Icon: CircleHelp };

const ITEM_CATEGORIES = ['DAMAGED_ITEM', 'WRONG_ITEM', 'MISSING_ITEM'];
const RETURN_CATEGORIES = ['RETURN_REJECTED', 'RETURN_PICKUP_FAILED', 'RETURN_QC_DISPUTED'];
const REFUND_CATEGORIES = ['REFUND_NOT_RECEIVED'];
const MAX_DESC = 1500;

const STATUS = {
  OPEN: { label: 'Sent – waiting for the seller', Icon: Send, cls: 'bg-amber-500/10 text-amber-300 border-amber-500/30' },
  IN_PROGRESS: { label: 'In progress', Icon: Hourglass, cls: 'bg-cyan-500/10 text-cyan-300 border-cyan-500/30' },
  RESOLVED: { label: 'Resolved', Icon: CircleCheck, cls: 'bg-emerald-500/10 text-emerald-300 border-emerald-500/30' },
  CLOSED: { label: 'Closed', Icon: Lock, cls: 'bg-slate-500/10 text-slate-300 border-slate-500/30' },
};
const statusKey = v => (v.caseStatus === 'CLOSED' || v.deemedClosed ? 'CLOSED' : STATUS[v.caseStatus] ? v.caseStatus : 'OPEN');

const RETURN_STATUS = { Return_Approved: 'Approved', Return_Rejected: 'Rejected', Return_Initiated: 'Requested' };

/* ─────────────────────────── helpers ────────────────────────────────────── */

// Times are shown in Western Indonesian Time.
function fmtWIB(iso, locale) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return `${d.toLocaleString(locale, { timeZone: 'Asia/Jakarta', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' })} WIB`;
}
function fmtDayWIB(iso, locale) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleDateString(locale, { timeZone: 'Asia/Jakarta', day: 'numeric', month: 'short' });
}

function timeLeft(t, iso, now) {
  const ms = new Date(iso).getTime() - now;
  if (!(ms > 0)) return null;
  const hours = Math.ceil(ms / 3600000);
  if (hours < 24) return t('{n} hours left', { n: hours });
  return t('{n} days left', { n: Math.ceil(ms / 86400000) });
}

function responseTime(t, hours) {
  if (!hours) return '';
  if (hours >= 24 && hours % 24 === 0) return t('usually within {n} days', { n: hours / 24 });
  return t('usually within {n} hours', { n: Math.round(hours * 10) / 10 });
}

function errorText(t, e) {
  if (!e) return '';
  if (e.code === 'SEND_FAILED' || e.code === 'NETWORK') return t('Could not be sent, please try again');
  if (e.code === 'NOT_FOUND') return t('We could not find this report or order.');
  if (e.code === 'INVALID') return t('This request could not be processed. Please check what you entered.');
  return t('Something went wrong. Please try again.');
}

const lineOf = (order, commitmentId) => (order?.lines || []).find(l => l.commitmentId === commitmentId);

// "1× Pizza Pepperoni Large, 2× Cola"
function itemsText(t, affected, order) {
  return (affected || []).map(a => {
    const name = lineOf(order, a.commitmentId)?.name || t('an item');
    return a.quantity ? `${a.quantity}× ${name}` : name;
  }).join(', ');
}

function returnLabel(t, r, order) {
  const names = (r.commitmentIds || []).map(id => lineOf(order, id)?.name).filter(Boolean).join(', ');
  const status = RETURN_STATUS[r.status] ? t(RETURN_STATUS[r.status]) : t('In progress');
  return [t('Return'), names, status].filter(Boolean).join(' · ');
}
function refundLabel(t, r, locale) {
  const amount = fmtIDR(r.amount, r.currency);
  const day = fmtDayWIB(r.settledAt, locale);
  return day ? t('Refund {date}, {amount}', { date: day, amount }) : t('Refund, {amount}', { amount });
}
function relatedLabel(t, ref, order, locale) {
  if (ref.refType === 'PERFORMANCE') {
    const r = (order?.returns || []).find(x => x.performanceId === ref.refId);
    return r ? returnLabel(t, r, order) : t('A return on this order');
  }
  if (ref.refType === 'SETTLEMENT') {
    const r = (order?.refunds || []).find(x => x.settlementId === ref.refId);
    return r ? refundLabel(t, r, locale) : t('A refund on this order');
  }
  return null; // the order itself -- already shown
}

// What happens next and who it is waiting on, in plain words.
function nextStep(t, v, locale) {
  const n = v.next || {};
  if (v.deemedClosed) return { mine: false, title: t('Closed automatically'), detail: t('The time to object has ended. No more action is needed.') };
  if (!n.waitingOn || n.action === 'NONE') return { mine: false, title: t('Closed'), detail: t('No more action is needed.') };
  if (n.waitingOn === 'ME') {
    switch (n.action) {
      case 'DECIDE': return { mine: true, title: t('Waiting for your approval'), detail: t('The seller sent an offer. Accept or reject it below.') };
      case 'PROPOSE': return { mine: true, title: t('Waiting for your explanation'), detail: t('The seller reported a problem. Give your side below.') };
      case 'CONFIRM': return { mine: true, title: t('Check the result'), detail: t('Close the report if the problem is solved, or tell us it is not.') };
      default: return { mine: true, title: t('Waiting for you'), detail: t('Please check the report below.') };
    }
  }
  const due = n.action === 'ACKNOWLEDGE' ? v.firstResponseDue : v.resolutionDue;
  const by = due ? t('Expected by {time}', { time: fmtWIB(due, locale) }) : '';
  switch (n.action) {
    case 'ACKNOWLEDGE': return { mine: false, title: t('Waiting for the seller to respond'), detail: by };
    case 'PROPOSE': return { mine: false, title: t('The seller is preparing a solution'), detail: by };
    case 'DECIDE': return { mine: false, title: t('Waiting for the seller to decide'), detail: by };
    case 'EXECUTE': return { mine: false, title: t('The seller is carrying out the solution'), detail: by };
    case 'RESOLVE': return { mine: false, title: t('Waiting for the seller to finish'), detail: by };
    case 'CONFIRM': return { mine: false, title: t('Waiting for the seller to confirm'), detail: '' };
    default: return { mine: false, title: t('Waiting for the seller'), detail: by };
  }
}

// Contact channels: Beckn attributes of unknown shape -- pick out what we
// recognise (WhatsApp, chat/web link, phone, email) and ignore the rest.
function channelLinks(channels) {
  const out = [];
  for (const c of channels || []) {
    const flat = {};
    const walk = o => {
      if (!o || typeof o !== 'object') return;
      for (const [k, v] of Object.entries(o)) {
        if (k.startsWith('@') && k !== '@type') continue;
        if (typeof v === 'string' || typeof v === 'number') { const key = k.toLowerCase(); if (!(key in flat)) flat[key] = String(v); }
        else walk(v);
      }
    };
    walk(c);
    const type = `${flat.type || ''} ${flat['@type'] || ''} ${flat.channel || ''} ${flat.mode || ''}`.toLowerCase();
    const url = [flat.url, flat.uri, flat.link, flat.href].find(u => u && /^https?:\/\//i.test(u));
    const phone = flat.phone || flat.telephone || flat.mobile || flat.whatsapp;
    const email = flat.email;
    const isWa = type.includes('whatsapp') || !!flat.whatsapp || (url && /wa\.me|whatsapp/i.test(url));
    if (isWa && (url || phone)) {
      out.push({ kind: 'whatsapp', href: url || `https://wa.me/${String(phone).replace(/\D/g, '')}` });
      continue;
    }
    if (url) out.push({ kind: type.includes('chat') ? 'chat' : 'web', href: url });
    if (phone) out.push({ kind: 'phone', href: `tel:${String(phone).replace(/[^\d+]/g, '')}`, label: phone });
    if (email && /@/.test(email)) out.push({ kind: 'email', href: `mailto:${email}`, label: email });
  }
  return out;
}

/* ─────────────────────────── small pieces ───────────────────────────────── */

export function StatusChip({ view }) {
  const { t } = useI18n();
  const s = STATUS[statusKey(view)];
  return (
    <span className={`inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full border ${s.cls}`}>
      <s.Icon className="w-3 h-3" aria-hidden="true" /> {t(s.label)}
    </span>
  );
}

function NeedsActionBadge() {
  const { t } = useI18n();
  return (
    <span className="inline-flex items-center gap-1 text-[10px] font-bold px-2 py-0.5 rounded-full bg-rose-500 text-white">
      <TriangleAlert className="w-3 h-3" aria-hidden="true" /> {t('Action needed')}
    </span>
  );
}

function ErrorBox({ error, onRetry, retryLabel, onDismiss, load }) {
  const { t } = useI18n();
  if (!error) return null;
  const text = load && error.code !== 'NOT_FOUND' ? t('Could not load. Please check your connection and try again.') : errorText(t, error);
  return (
    <div role="alert" className="rounded-xl border border-rose-500/30 bg-rose-500/10 px-3 py-2.5 flex items-start gap-2 text-xs text-rose-200">
      <TriangleAlert className="w-4 h-4 shrink-0 mt-0.5" aria-hidden="true" />
      <p className="flex-1">{text}</p>
      {onDismiss && (
        <button type="button" onClick={onDismiss}
          className="shrink-0 px-2.5 py-1 rounded-lg border border-white/15 font-semibold text-slate-200 hover:bg-white/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-rose-300">
          {t('Cancel')}
        </button>
      )}
      {onRetry && (
        <button type="button" onClick={onRetry}
          className="shrink-0 inline-flex items-center gap-1 px-2.5 py-1 rounded-lg bg-rose-500/20 border border-rose-500/40 font-semibold hover:bg-rose-500/30 focus:outline-none focus-visible:ring-2 focus-visible:ring-rose-300">
          <RefreshCw className="w-3 h-3" aria-hidden="true" /> {retryLabel || t('Try again')}
        </button>
      )}
    </div>
  );
}

function Spinner({ label }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-12 text-slate-400" role="status">
      <Loader2 className="w-7 h-7 text-cyan-400 animate-spin" aria-hidden="true" />
      {label && <p className="text-xs">{label}</p>}
    </div>
  );
}

const card = 'rounded-xl border border-white/[0.07] bg-white/[0.02]';
const btnPrimary = 'inline-flex items-center justify-center gap-1.5 px-4 py-2.5 rounded-xl text-sm font-bold text-white bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-emerald-300';
const btnGhost = 'inline-flex items-center justify-center gap-1.5 px-4 py-2.5 rounded-xl text-sm font-semibold text-slate-200 border border-white/10 hover:bg-white/[0.06] disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300';
const btnDanger = 'inline-flex items-center justify-center gap-1.5 px-4 py-2.5 rounded-xl text-sm font-bold text-rose-200 border border-rose-500/40 bg-rose-500/10 hover:bg-rose-500/20 disabled:opacity-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-rose-300';
const inputCls = 'w-full bg-slate-900 border border-white/10 rounded-xl px-3 py-2.5 text-sm text-white placeholder-slate-500 outline-none focus:border-cyan-400/50 focus-visible:ring-2 focus-visible:ring-cyan-400/30 resize-none';

function ConfirmDialog({ title, body, confirmLabel, tone = 'primary', busy, onConfirm, onCancel }) {
  const { t } = useI18n();
  // Portalled: a transformed drawer would otherwise trap position: fixed.
  return createPortal(
    <div className="fixed inset-0 z-[95] flex items-end sm:items-center justify-center p-3 bg-black/70"
      onKeyDown={e => { if (e.key === 'Escape' && !busy) onCancel(); }}>
      <div role="alertdialog" aria-modal="true" aria-labelledby="gv-confirm-title"
        className="w-full max-w-sm rounded-2xl border border-white/10 shadow-2xl p-5 space-y-4" style={{ background: '#0d1426' }}>
        <p id="gv-confirm-title" className="text-base font-bold text-white">{title}</p>
        {body && <div className="text-sm text-slate-300 space-y-2">{body}</div>}
        <div className="grid grid-cols-2 gap-2">
          <button type="button" className={btnGhost} onClick={onCancel} disabled={busy}>{t('Go back')}</button>
          <button type="button" autoFocus className={tone === 'danger' ? btnDanger : btnPrimary} onClick={onConfirm} disabled={busy}>
            {busy && <Loader2 className="w-4 h-4 animate-spin" aria-hidden="true" />} {confirmLabel}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

// Order items as context; the reported ones are marked.
function OrderContext({ order, affected }) {
  const { t } = useI18n();
  if (!order) return null;
  const reported = Object.fromEntries((affected || []).map(a => [a.commitmentId, a.quantity]));
  return (
    <section className={card} aria-label={t('Order')}>
      <div className="px-4 py-2.5 border-b border-white/[0.06] flex items-center gap-2">
        <Store className="w-3.5 h-3.5 text-slate-500" aria-hidden="true" />
        <p className="text-[11px] font-bold uppercase tracking-widest text-slate-500 flex-1">{t('Order')}</p>
        {order.providerName && <span className="text-xs text-slate-300 truncate">{order.providerName}</span>}
      </div>
      <ul className="divide-y divide-white/[0.04]">
        {(order.lines || []).map(l => (
          <li key={l.commitmentId} className="px-4 py-2 flex items-center gap-3 text-sm">
            <Package className="w-4 h-4 text-slate-500 shrink-0" aria-hidden="true" />
            <span className="flex-1 min-w-0 truncate text-slate-200">{l.name}</span>
            <span className="text-xs text-slate-500 tabular-nums">{l.quantity}×</span>
            {reported[l.commitmentId] != null && (
              <span className="text-[10px] font-bold px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-300 border border-amber-500/30">
                {t('Reported: {n}×', { n: reported[l.commitmentId] })}
              </span>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

/* ─────────────────────────── raise a problem ─────────────────────────────── */

const draftKey = txnId => `grievanceDraft:${txnId}`;
const emptyDraft = { category: '', qty: {}, related: '', description: '' };
function loadDraft(txnId) {
  try { return { ...emptyDraft, ...JSON.parse(sessionStorage.getItem(draftKey(txnId)) || '{}') }; } catch { return emptyDraft; }
}
function saveDraft(txnId, d) {
  try { sessionStorage.setItem(draftKey(txnId), JSON.stringify(d)); } catch { /* storage blocked */ }
}
function clearDraft(txnId) {
  try { sessionStorage.removeItem(draftKey(txnId)); } catch { /* storage blocked */ }
}

// Related return/refund choices for a category, newest first.
function relatedChoices(t, category, order, locale) {
  if (RETURN_CATEGORIES.includes(category)) {
    let list = [...(order?.returns || [])].reverse();
    if (category === 'RETURN_REJECTED') list = [...list.filter(r => r.status === 'Return_Rejected'), ...list.filter(r => r.status !== 'Return_Rejected')];
    return list.map(r => ({ key: `PERFORMANCE:${r.performanceId}`, label: returnLabel(t, r, order), ret: r }));
  }
  if (REFUND_CATEGORIES.includes(category)) {
    return [...(order?.refunds || [])].reverse().map(r => ({ key: `SETTLEMENT:${r.settlementId}`, label: refundLabel(t, r, locale) }));
  }
  return [];
}

// The "Problem with this order?" button and its three-step sheet.
// steer: { canReturn, canCancel, onReturn, onCancel } -- the normal return /
// cancellation for this order, offered first when it is the faster way.
export function ReportProblem({ txnId, steer, onViewGrievance, className = '' }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)} data-tour="report-problem"
        className={`w-full flex items-center justify-center gap-1.5 py-2.5 rounded-xl text-xs font-semibold bg-purple-500/10 text-purple-200 border border-purple-500/30 hover:bg-purple-500/20 focus:outline-none focus-visible:ring-2 focus-visible:ring-purple-300 transition-all ${className}`}>
        <LifeBuoy className="w-3.5 h-3.5" aria-hidden="true" /> {t('Problem with this order?')}
      </button>
      {open && <RaiseSheet txnId={txnId} steer={steer} onClose={() => setOpen(false)}
        onViewGrievance={id => { setOpen(false); onViewGrievance?.(id); }} />}
    </>
  );
}

function RaiseSheet({ txnId, steer, onClose, onViewGrievance }) {
  const { t, locale } = useI18n();
  const [opts, setOpts] = useState(null);
  const [loadError, setLoadError] = useState(null);
  const [loadKey, setLoadKey] = useState(0);
  const [draft, setDraftState] = useState(() => loadDraft(txnId));
  const [step, setStep] = useState(() => (loadDraft(txnId).category ? 'items' : 'category'));
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState(null);
  const [created, setCreated] = useState(null);

  const setDraft = upd => setDraftState(d => {
    const next = typeof upd === 'function' ? upd(d) : { ...d, ...upd };
    saveDraft(txnId, next);
    return next;
  });

  useEffect(() => {
    let alive = true;
    apiJSON(`/orders/${encodeURIComponent(txnId)}/grievance-options`)
      .then(d => {
        if (!alive) return;
        setOpts(d);
        // One line: it is the one with the problem.
        const lines = d.order?.lines || [];
        if (lines.length === 1) {
          setDraftState(dr => (Object.keys(dr.qty).length ? dr : { ...dr, qty: { [lines[0].commitmentId]: lines[0].quantity || 1 } }));
        }
      })
      .catch(e => { if (alive) setLoadError(e); });
    return () => { alive = false; };
  }, [txnId, loadKey]);

  const order = opts?.order;
  const lines = order?.lines || [];
  const cat = categoryOf(draft.category);
  const choices = relatedChoices(t, draft.category, order, locale);
  const relatedKey = draft.related && choices.some(c => c.key === draft.related) ? draft.related : (choices[0]?.key || '');
  const affected = lines.map(l => ({ commitmentId: l.commitmentId, quantity: draft.qty[l.commitmentId] || 0 })).filter(a => a.quantity > 0);
  const itemsRequired = ITEM_CATEGORIES.includes(draft.category);
  const canSteer = ITEM_CATEGORIES.includes(draft.category) && (steer?.canReturn || steer?.canCancel);
  // Nothing to choose (one item, ordered once, and at most one return or
  // refund to link): the items step is skipped.
  const skipItems = !!opts && lines.length <= 1 && Math.floor(lines[0]?.quantity || 1) <= 1 && choices.length <= 1;
  const steps = skipItems ? 2 : 3;
  const stepNo = { category: 1, steer: 1, items: 2, describe: steps, review: steps }[step];

  useLayoutEffect(() => {
    if (step === 'items' && skipItems) setStep('describe');
  }, [step, skipItems]);

  // A return: report its items unless some were chosen already.
  const withReturnItems = (d, ret) => {
    if (!ret || Object.values(d.qty).some(q => q > 0)) return d.qty;
    const qty = { ...d.qty };
    for (const cid of ret.commitmentIds || []) { const l = lineOf(order, cid); if (l) qty[cid] = l.quantity || 1; }
    return qty;
  };
  const chooseCategory = value => {
    const first = relatedChoices(t, value, order, locale)[0];
    setDraft(d => ({ ...d, category: value, related: first?.key || '', qty: withReturnItems(d, first?.ret) }));
    setStep(ITEM_CATEGORIES.includes(value) && (steer?.canReturn || steer?.canCancel) ? 'steer' : 'items');
  };
  const chooseRelated = c => setDraft(d => ({ ...d, related: c.key, qty: withReturnItems(d, c.ret) }));

  const send = async () => {
    setSending(true);
    setSendError(null);
    try {
      const body = { transactionId: txnId, category: draft.category, description: draft.description.trim(), affected };
      if (relatedKey) {
        const [refType, ...rest] = relatedKey.split(':');
        body.related = [{ refType, refId: rest.join(':') }];
      }
      const v = await apiJSON('/grievances', { method: 'POST', body });
      clearDraft(txnId);
      setCreated(v);
    } catch (e) {
      setSendError(e); // the draft stays as it is
    } finally {
      setSending(false);
    }
  };

  const back = () => {
    setSendError(null);
    if (step === 'steer' || step === 'items') setStep(canSteer && step === 'items' ? 'steer' : 'category');
    else if (step === 'describe') setStep(!skipItems ? 'items' : canSteer ? 'steer' : 'category');
    else if (step === 'review') setStep('describe');
  };

  const titles = {
    category: t("What's the problem?"), steer: t("What's the problem?"), items: t('Which items?'),
    describe: t('Tell us what happened'), review: t('Check and send'),
  };

  return createPortal(
    <div className="fixed inset-0 z-[80] flex items-end sm:items-center justify-center sm:p-4 bg-black/70 backdrop-blur-sm"
      onKeyDown={e => { if (e.key === 'Escape' && !sending) onClose(); }}>
      <div role="dialog" aria-modal="true" aria-labelledby="gv-raise-title"
        className="w-full sm:max-w-lg max-h-[92vh] flex flex-col rounded-t-2xl sm:rounded-2xl border border-white/10 shadow-2xl overflow-hidden"
        style={{ background: '#0d1426' }}>
        {/* Header */}
        <div className="px-4 py-3 border-b border-white/[0.07] flex items-center gap-2 shrink-0">
          {!created && step !== 'category' && (
            <button type="button" onClick={back} disabled={sending} aria-label={t('Back')}
              className="p-1.5 rounded-lg text-slate-400 hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300">
              <ChevronLeft className="w-5 h-5" aria-hidden="true" />
            </button>
          )}
          <div className="flex-1 min-w-0">
            {!created && <p className="text-[11px] text-slate-500">{t('Step {n} of {total}', { n: stepNo, total: steps })}</p>}
            <h2 id="gv-raise-title" className="text-base font-bold text-white truncate">
              {created ? t('Report sent') : titles[step]}
            </h2>
          </div>
          <button type="button" onClick={onClose} disabled={sending} aria-label={t('Close')}
            className="p-1.5 rounded-lg text-slate-400 hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300">
            <X className="w-5 h-5" aria-hidden="true" />
          </button>
        </div>
        {!created && (
          <div className="h-1 bg-white/[0.06] shrink-0" aria-hidden="true">
            <div className="h-1 bg-emerald-400 transition-all" style={{ width: `${(stepNo / steps) * 100}%` }} />
          </div>
        )}

        <div className="flex-1 overflow-y-auto px-4 py-4 space-y-4">
          {!opts && !loadError && <Spinner label={t('Loading your order…')} />}
          {loadError && <ErrorBox load error={loadError} onRetry={() => { setLoadError(null); setLoadKey(k => k + 1); }} />}

          {opts && created && <Confirmation view={created} policy={opts.policy} order={order} onView={() => onViewGrievance(created.id)} onDone={onClose} />}

          {opts && !created && step === 'category' && (
            <>
              {(opts.open || []).length > 0 && (
                <div className="rounded-xl border border-cyan-500/30 bg-cyan-500/10 px-3 py-2.5 text-xs text-cyan-100 space-y-1.5">
                  <p className="flex items-center gap-1.5 font-semibold"><Info className="w-3.5 h-3.5" aria-hidden="true" /> {t('You already have an open report for this order')}</p>
                  {opts.open.map(g => (
                    <button key={g.id} type="button" onClick={() => onViewGrievance(g.id)}
                      className="w-full text-left flex items-center gap-2 underline-offset-2 hover:underline focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 rounded">
                      {t(categoryOf(g.category).label)} · {t(STATUS[statusKey(g)].label)} <ChevronRight className="w-3.5 h-3.5 ml-auto" aria-hidden="true" />
                    </button>
                  ))}
                </div>
              )}
              <fieldset>
                <legend className="sr-only">{t("What's the problem?")}</legend>
                <div className="grid grid-cols-2 gap-2">
                  {CATEGORIES.map(c => (
                    <button key={c.value} type="button" onClick={() => chooseCategory(c.value)} aria-pressed={draft.category === c.value}
                      className={`text-left rounded-xl border px-3 py-3 min-h-[88px] flex flex-col gap-1.5 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 ${draft.category === c.value ? 'border-cyan-400/60 bg-cyan-400/10' : 'border-white/10 hover:border-white/25 hover:bg-white/[0.04]'}`}>
                      <c.Icon className="w-6 h-6 text-cyan-300" aria-hidden="true" />
                      <span className="text-sm font-semibold text-white leading-tight">{t(c.label)}</span>
                      <span className="text-[11px] text-slate-400 leading-snug">{t(c.hint)}</span>
                    </button>
                  ))}
                </div>
              </fieldset>
            </>
          )}

          {opts && !created && step === 'steer' && (
            <div className="space-y-3">
              <div className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 px-4 py-3 space-y-1.5">
                <p className="text-sm font-bold text-emerald-200 flex items-center gap-2"><cat.Icon className="w-4 h-4" aria-hidden="true" /> {t(cat.label)}</p>
                <p className="text-sm text-slate-200">
                  {steer?.canReturn
                    ? t('This order can still be returned. A normal return is faster: the seller collects the item and your refund follows the return policy, without waiting for a review.')
                    : t('This order can still be cancelled. A normal cancellation is faster: it takes effect straight away and your refund follows the cancellation policy.')}
                </p>
              </div>
              <div className="grid gap-2">
                {steer?.canReturn && (
                  <button type="button" className={btnPrimary} onClick={() => { onClose(); steer.onReturn?.(); }}>
                    <RotateCcw className="w-4 h-4" aria-hidden="true" /> {t('Return the item')}
                  </button>
                )}
                {steer?.canCancel && (
                  <button type="button" className={steer?.canReturn ? btnGhost : btnPrimary} onClick={() => { onClose(); steer.onCancel?.(); }}>
                    <Ban className="w-4 h-4" aria-hidden="true" /> {t('Cancel the order')}
                  </button>
                )}
              </div>
              <button type="button" onClick={() => setStep('items')}
                className="w-full text-center text-sm text-cyan-300 underline underline-offset-2 py-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 rounded">
                {t('Report the problem anyway')}
              </button>
            </div>
          )}

          {opts && !created && step === 'items' && (
            <div className="space-y-3">
              {choices.length > 0 && (
                <fieldset className="space-y-2">
                  <legend className="text-xs font-semibold text-slate-400 mb-1">
                    {REFUND_CATEGORIES.includes(draft.category) ? t('Which refund?') : t('Which return?')}
                  </legend>
                  {choices.map(c => (
                    <label key={c.key} className={`flex items-center gap-3 rounded-xl border px-3 py-2.5 cursor-pointer focus-within:ring-2 focus-within:ring-cyan-300 ${relatedKey === c.key ? 'border-cyan-400/60 bg-cyan-400/10' : 'border-white/10'}`}>
                      <input type="radio" name="gv-related" className="accent-cyan-400" checked={relatedKey === c.key} onChange={() => chooseRelated(c)} />
                      <span className="text-sm text-slate-200">{c.label}</span>
                    </label>
                  ))}
                </fieldset>
              )}
              {choices.length === 0 && (RETURN_CATEGORIES.includes(draft.category) || REFUND_CATEGORIES.includes(draft.category)) && (
                <p className="text-xs text-slate-400 flex items-start gap-1.5"><Info className="w-3.5 h-3.5 mt-0.5 shrink-0" aria-hidden="true" />
                  {REFUND_CATEGORIES.includes(draft.category) ? t('No refund is recorded on this order yet. Your report will be linked to the order.') : t('No return is recorded on this order yet. Your report will be linked to the order.')}
                </p>
              )}
              <fieldset className="space-y-2">
                <legend className="text-xs font-semibold text-slate-400 mb-1">
                  {itemsRequired ? t('Choose the items and how many') : t('Choose the items (optional)')}
                </legend>
                {lines.length === 0 && <p className="text-sm text-slate-500">{t('No items found on this order.')}</p>}
                {lines.map(l => {
                  const q = draft.qty[l.commitmentId] || 0;
                  const max = Math.max(1, Math.floor(l.quantity || 1));
                  const setQ = n => setDraft(d => ({ ...d, qty: { ...d.qty, [l.commitmentId]: Math.max(0, Math.min(max, n)) } }));
                  return (
                    <div key={l.commitmentId} className={`rounded-xl border px-3 py-2.5 flex items-center gap-3 ${q > 0 ? 'border-cyan-400/50 bg-cyan-400/5' : 'border-white/10'}`}>
                      <Package className="w-5 h-5 text-slate-400 shrink-0" aria-hidden="true" />
                      <div className="flex-1 min-w-0">
                        <p className="text-sm font-medium text-white truncate">{l.name}</p>
                        <p className="text-[11px] text-slate-500">{t('Ordered: {n}', { n: l.quantity })}{l.unitPrice ? ` · ${fmtIDR(l.unitPrice, order.currency)}` : ''}</p>
                      </div>
                      <div className="flex items-center rounded-lg border border-white/10 overflow-hidden" role="group" aria-label={t('Quantity for {name}', { name: l.name })}>
                        <button type="button" className="px-3 py-2 text-slate-300 hover:text-white disabled:opacity-30 focus:outline-none focus-visible:bg-white/10" disabled={q <= 0}
                          aria-label={t('Fewer')} onClick={() => setQ(q - 1)}><Minus className="w-4 h-4" aria-hidden="true" /></button>
                        <span className="w-8 text-center text-sm text-white tabular-nums" aria-live="polite">{q}</span>
                        <button type="button" className="px-3 py-2 text-slate-300 hover:text-white disabled:opacity-30 focus:outline-none focus-visible:bg-white/10" disabled={q >= max}
                          aria-label={t('More')} onClick={() => setQ(q + 1)}><Plus className="w-4 h-4" aria-hidden="true" /></button>
                      </div>
                    </div>
                  );
                })}
              </fieldset>
            </div>
          )}

          {opts && !created && step === 'describe' && (
            <div className="space-y-2">
              <label htmlFor="gv-desc" className="text-xs font-semibold text-slate-400">{t('What happened?')}</label>
              <textarea id="gv-desc" rows={6} maxLength={MAX_DESC} autoFocus value={draft.description}
                onChange={e => setDraft({ description: e.target.value })}
                placeholder={t('For example: the box was crushed and the pizza was spilled when it arrived at 19:10. Say what you would like: a refund, a replacement…')}
                className={inputCls} />
              <div className="flex items-start justify-between gap-3 text-[11px] text-slate-500">
                <p className="flex items-start gap-1.5"><MessageCircle className="w-3.5 h-3.5 mt-0.5 shrink-0" aria-hidden="true" />
                  {t('Photos and chat go through the help channel the seller offers, not this form.')}</p>
                <span className="tabular-nums shrink-0">{draft.description.length}/{MAX_DESC}</span>
              </div>
            </div>
          )}

          {opts && !created && step === 'review' && (
            <div className="space-y-3">
              <dl className={`${card} divide-y divide-white/[0.05] text-sm`}>
                <div className="px-4 py-2.5 flex gap-3"><dt className="w-24 shrink-0 text-slate-500">{t('Problem')}</dt>
                  <dd className="text-white flex items-center gap-1.5"><cat.Icon className="w-4 h-4 text-cyan-300" aria-hidden="true" />{t(cat.label)}</dd></div>
                <div className="px-4 py-2.5 flex gap-3"><dt className="w-24 shrink-0 text-slate-500">{t('Items')}</dt>
                  <dd className="text-white">{affected.length ? itemsText(t, affected, order) : t('The whole order')}</dd></div>
                {relatedKey && (
                  <div className="px-4 py-2.5 flex gap-3"><dt className="w-24 shrink-0 text-slate-500">{t('About')}</dt>
                    <dd className="text-white">{choices.find(c => c.key === relatedKey)?.label}</dd></div>
                )}
                <div className="px-4 py-2.5 flex gap-3"><dt className="w-24 shrink-0 text-slate-500">{t('Seller')}</dt>
                  <dd className="text-white">{order?.providerName || '—'}</dd></div>
                <div className="px-4 py-2.5"><dt className="text-slate-500 mb-1">{t('What happened')}</dt>
                  <dd className="text-slate-200 whitespace-pre-wrap break-words">{draft.description.trim()}</dd></div>
              </dl>
              {opts.policy?.firstResponseHours > 0 && (
                <p className="text-xs text-slate-400 flex items-start gap-1.5"><Info className="w-3.5 h-3.5 mt-0.5 shrink-0" aria-hidden="true" />
                  {t('The seller will respond {when}.', { when: responseTime(t, opts.policy.firstResponseHours) })}</p>
              )}
              <ErrorBox error={sendError} onRetry={sendError && !['INVALID', 'NOT_FOUND'].includes(sendError.code) ? send : null} />
            </div>
          )}
        </div>

        {/* Footer */}
        {opts && !created && ['items', 'describe', 'review'].includes(step) && (
          <div className="px-4 py-3 border-t border-white/[0.07] shrink-0">
            {step === 'items' && (
              <button type="button" className={`${btnPrimary} w-full`} disabled={itemsRequired && affected.length === 0} onClick={() => setStep('describe')}>
                {t('Next')} <ChevronRight className="w-4 h-4" aria-hidden="true" />
              </button>
            )}
            {step === 'describe' && (
              <button type="button" className={`${btnPrimary} w-full`} disabled={!draft.description.trim()} onClick={() => setStep('review')}>
                {t('Check report')} <ChevronRight className="w-4 h-4" aria-hidden="true" />
              </button>
            )}
            {step === 'review' && (
              <button type="button" className={`${btnPrimary} w-full`} disabled={sending || !draft.description.trim()} onClick={send}>
                {sending ? <Loader2 className="w-4 h-4 animate-spin" aria-hidden="true" /> : <Send className="w-4 h-4" aria-hidden="true" />}
                {sending ? t('Sending…') : sendError ? t('Try sending again') : t('Send report')}
              </button>
            )}
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}

function GrievanceNumber({ id }) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className="text-slate-500">{t('Report number')}</span>
      <span className="font-mono text-slate-200 break-all">{id}</span>
      <button type="button" aria-label={t('Copy report number')}
        onClick={() => { navigator.clipboard?.writeText(id).then(() => setCopied(true), () => {}); }}
        className="p-1 rounded text-slate-500 hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300">
        {copied ? <CircleCheck className="w-3.5 h-3.5 text-emerald-400" aria-hidden="true" /> : <Copy className="w-3.5 h-3.5" aria-hidden="true" />}
      </button>
    </span>
  );
}

function SentState({ view }) {
  const { t, locale } = useI18n();
  const d = view.lastSent?.delivery;
  if (!view.lastSent || view.lastSent.by !== 'ME') return null;
  if (d === 'SENDING') return <p className="text-xs text-amber-300 flex items-center gap-1.5"><Loader2 className="w-3.5 h-3.5 animate-spin" aria-hidden="true" /> {t('Sending to the seller…')}</p>;
  if (d === 'ACK') return <p className="text-xs text-emerald-300 flex items-center gap-1.5"><CircleCheck className="w-3.5 h-3.5" aria-hidden="true" /> {t('Delivered to the seller {time}', { time: fmtWIB(view.lastSent.at, locale) })}</p>;
  if (d === 'NACK' || d === 'FAILED') return <p className="text-xs text-rose-300 flex items-center gap-1.5"><CircleX className="w-3.5 h-3.5" aria-hidden="true" /> {t('Could not be sent')}</p>;
  return null;
}

function Confirmation({ view, policy, order, onView, onDone }) {
  const { t } = useI18n();
  const cat = categoryOf(view.category);
  return (
    <div className="space-y-4">
      <div className="flex flex-col items-center text-center gap-2 py-2">
        <div className="w-14 h-14 rounded-full bg-emerald-500/15 border-2 border-emerald-500/40 flex items-center justify-center">
          <CircleCheck className="w-8 h-8 text-emerald-400" aria-hidden="true" />
        </div>
        <p className="text-lg font-bold text-emerald-200">{t('Your report has been sent')}</p>
        <p className="text-xs"><GrievanceNumber id={view.id} /></p>
        <SentState view={view} />
      </div>
      <div className={`${card} px-4 py-3 text-sm text-slate-300 space-y-1`}>
        <p className="flex items-center gap-1.5 text-white font-semibold"><cat.Icon className="w-4 h-4 text-cyan-300" aria-hidden="true" /> {t(cat.label)}</p>
        {view.affected?.length > 0 && <p>{itemsText(t, view.affected, order)}</p>}
      </div>
      <div className="space-y-2">
        <p className="text-xs font-bold uppercase tracking-widest text-slate-500">{t('What happens next')}</p>
        <ol className="space-y-2 text-sm text-slate-300 list-decimal pl-5">
          <li>{policy?.firstResponseHours > 0
            ? t('The seller looks at your report and responds, {when}.', { when: responseTime(t, policy.firstResponseHours) })
            : t('The seller looks at your report and responds.')}</li>
          <li>{t('If the seller offers a solution, such as a refund, you can accept or reject it in My help. We mark it "Action needed".')}</li>
          <li>{t('Once it is solved you can close the report, or tell us if the problem is not solved.')}</li>
        </ol>
      </div>
      <div className="grid grid-cols-2 gap-2">
        <button type="button" className={btnGhost} onClick={onDone}>{t('Done')}</button>
        <button type="button" className={btnPrimary} onClick={onView}>{t('View report')}</button>
      </div>
    </div>
  );
}

/* ─────────────────────────── list ────────────────────────────────────────── */

function GrievanceRow({ v, onOpen }) {
  const { t, locale } = useI18n();
  const cat = categoryOf(v.category);
  const ns = nextStep(t, v, locale);
  return (
    <button type="button" onClick={() => onOpen(v.id)}
      className={`w-full text-left rounded-xl border px-4 py-3 flex items-start gap-3 hover:bg-white/[0.04] focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 transition-colors ${v.needsAction ? 'border-rose-500/40 bg-rose-500/[0.04]' : 'border-white/[0.07] bg-white/[0.02]'}`}>
      <div className="w-9 h-9 rounded-xl bg-white/[0.05] flex items-center justify-center shrink-0 relative">
        <cat.Icon className="w-4 h-4 text-cyan-300" aria-hidden="true" />
        {v.unread && <span className="absolute -top-0.5 -right-0.5 w-2.5 h-2.5 rounded-full bg-cyan-400" aria-hidden="true" />}
      </div>
      <div className="flex-1 min-w-0 space-y-1">
        <div className="flex items-center gap-2 flex-wrap">
          <p className="text-sm font-semibold text-white">{t(cat.label)}</p>
          {v.raisedBy === 'SELLER' && <span className="text-[10px] font-semibold px-1.5 py-0.5 rounded bg-purple-500/15 text-purple-200 border border-purple-500/30">{t('From the seller')}</span>}
          {v.unread && <span className="sr-only">{t('New update')}</span>}
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          <StatusChip view={v} />
          {v.needsAction && <NeedsActionBadge />}
        </div>
        <p className={`text-xs ${ns.mine ? 'text-rose-200 font-semibold' : 'text-slate-400'}`}>{ns.title}</p>
        {v.description && <p className="text-[11px] text-slate-500 truncate">{v.description}</p>}
        <p className="text-[10px] text-slate-600">{fmtWIB(v.lastEventAt || v.openedAt, locale)}</p>
      </div>
      <ChevronRight className="w-4 h-4 text-slate-600 shrink-0 mt-2" aria-hidden="true" />
    </button>
  );
}

// "My help": every report, open ones first (the API sorts them).
export function GrievanceList({ onOpen, active = true }) {
  const { t } = useI18n();
  const { items, loading, error, reload } = useGrievances('', active);
  if (loading) return <Spinner label={t('Loading your reports…')} />;
  return (
    <div className="space-y-2" data-tour="help-list">
      {error && <ErrorBox load error={error} onRetry={reload} />}
      {!error && items.length === 0 && (
        <div className="flex flex-col items-center gap-2 py-16 text-center text-slate-500">
          <Inbox className="w-12 h-12 opacity-30" aria-hidden="true" />
          <p className="text-sm text-slate-300">{t('No reports yet')}</p>
          <p className="text-xs max-w-xs">{t('If something is wrong with an order, open the order and choose "Problem with this order?".')}</p>
        </div>
      )}
      {items.map(v => <GrievanceRow key={v.id} v={v} onOpen={onOpen} />)}
    </div>
  );
}

// One order's reports and the button to raise a new one (order detail).
export function OrderGrievances({ txnId, steer, onOpen }) {
  const { t } = useI18n();
  const { items, needsAction, error } = useGrievances(txnId, true);
  return (
    <section className={`${card} overflow-hidden`} aria-label={t('Help with this order')}>
      <div className="px-4 py-2.5 border-b border-white/[0.06] flex items-center gap-2">
        <LifeBuoy className="w-3.5 h-3.5 text-slate-500" aria-hidden="true" />
        <p className="text-[11px] font-bold uppercase tracking-widest text-slate-500 flex-1">{t('Help with this order')}</p>
        {needsAction > 0 && <NeedsActionBadge />}
      </div>
      <div className="p-3 space-y-2">
        {error && error.code !== 'NOT_FOUND' && <p className="text-[11px] text-slate-500">{t('Could not load the reports for this order.')}</p>}
        {items.map(v => <GrievanceRow key={v.id} v={v} onOpen={onOpen} />)}
        <ReportProblem txnId={txnId} steer={steer} onViewGrievance={onOpen} />
      </div>
    </section>
  );
}

/* ─────────────────────────── detail ──────────────────────────────────────── */

const STEPPER = ['Sent', 'Being handled', 'Resolved', 'Closed'];
function Stepper({ view }) {
  const { t } = useI18n();
  const idx = { OPEN: 0, IN_PROGRESS: 1, RESOLVED: 2, CLOSED: 3 }[statusKey(view)];
  return (
    <div className="relative">
    <div className="absolute inset-x-8 top-3 h-px bg-white/10" aria-hidden="true" />
    <ol className="flex items-start justify-between relative" aria-label={t('Progress')}>
      {STEPPER.map((label, i) => (
        <li key={label} className="relative z-10 flex flex-col items-center gap-1 flex-1" aria-current={i === idx ? 'step' : undefined}>
          <span className={`w-6 h-6 rounded-full flex items-center justify-center text-[10px] font-bold ${i < idx ? 'bg-emerald-400 text-slate-900' : i === idx ? 'bg-emerald-400 text-slate-900 ring-4 ring-emerald-400/20' : 'bg-slate-700 text-slate-400'}`}>
            {i < idx ? <CircleCheck className="w-3.5 h-3.5" aria-hidden="true" /> : i + 1}
          </span>
          <span className={`text-[10px] font-medium text-center ${i <= idx ? 'text-emerald-300' : 'text-slate-500'}`}>
            {t(label)}{i < idx && <span className="sr-only"> ({t('done')})</span>}
          </span>
        </li>
      ))}
    </ol>
    </div>
  );
}

const EVENT_LABEL = {
  OPENED: 'Report opened', ACKNOWLEDGED: 'Report received', RESOLUTION_PROPOSED: 'Solution offered',
  ACTION_COMPLETED: 'Solution carried out', RESOLVED: 'Marked as resolved', OBJECTION: 'Objection raised', CLOSED: 'Report closed',
};
function eventLabel(t, e) {
  if (e.eventType === 'RESOLUTION_DECIDED') return e.result === 'REJECTED' ? t('Offer rejected') : t('Offer accepted');
  if (e.eventType === 'RESOLUTION_PROPOSED' && e.version > 1) return t('Offer updated');
  return t(EVENT_LABEL[e.eventType] || 'Update');
}

function Timeline({ view }) {
  const { t, locale } = useI18n();
  const items = [...(view.timeline || [])].reverse();
  if (!items.length) return null;
  return (
    <section className={`${card} overflow-hidden`} aria-label={t('What happened so far')}>
      <p className="px-4 py-2.5 border-b border-white/[0.06] text-[11px] font-bold uppercase tracking-widest text-slate-500">{t('What happened so far')}</p>
      <ol className="px-4 py-3 space-y-3">
        {items.map((e, i) => {
          const failed = e.delivery === 'FAILED' || e.delivery === 'NACK';
          return (
            <li key={`${e.at}-${e.eventType}-${i}`} className="flex items-start gap-3">
              <span className={`mt-0.5 w-6 h-6 rounded-full flex items-center justify-center shrink-0 ${failed ? 'bg-rose-500/15 text-rose-300' : e.by === 'ME' ? 'bg-cyan-500/15 text-cyan-300' : 'bg-purple-500/15 text-purple-300'}`}>
                {failed ? <CircleX className="w-3.5 h-3.5" aria-hidden="true" /> : e.by === 'ME' ? <Send className="w-3 h-3" aria-hidden="true" /> : <Store className="w-3 h-3" aria-hidden="true" />}
              </span>
              <div className="flex-1 min-w-0">
                <p className="text-sm text-slate-100">
                  <span className="font-semibold">{eventLabel(t, e)}</span>
                  <span className="text-slate-400"> · {e.by === 'ME' ? t('You') : t('Seller')}</span>
                </p>
                {e.note && <p className="text-xs text-slate-400 whitespace-pre-wrap break-words">{e.note}</p>}
                <p className="text-[10px] text-slate-500 flex items-center gap-2 flex-wrap">
                  {fmtWIB(e.at, locale)}
                  {failed && <span className="text-rose-300 font-semibold">{t('Could not be sent')}</span>}
                  {e.delivery === 'SENDING' && <span className="text-amber-300">{t('Sending…')}</span>}
                </p>
              </div>
            </li>
          );
        })}
      </ol>
    </section>
  );
}

function Channels({ channels }) {
  const { t } = useI18n();
  const links = channelLinks(channels);
  if (!links.length) return null;
  const meta = {
    whatsapp: [MessageCircle, t('WhatsApp')], chat: [MessageCircle, t('Chat with the seller')],
    web: [ExternalLink, t('Open the help page')], phone: [Phone, null], email: [Mail, null],
  };
  return (
    <section className={`${card} px-4 py-3 space-y-2`} aria-label={t('Contact the seller')}>
      <p className="text-[11px] font-bold uppercase tracking-widest text-slate-500">{t('Contact the seller')}</p>
      <p className="text-xs text-slate-400">{t('Send photos or chat through these channels.')}</p>
      <div className="flex flex-wrap gap-2">
        {links.map((l, i) => {
          const [Icon, label] = meta[l.kind];
          const text = label || (l.kind === 'phone' ? t('Call {phone}', { phone: l.label }) : l.label);
          return (
            <a key={i} href={l.href} target={l.kind === 'phone' || l.kind === 'email' ? undefined : '_blank'} rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 px-3 py-2 rounded-xl text-xs font-semibold text-cyan-200 border border-cyan-500/30 bg-cyan-500/10 hover:bg-cyan-500/20 focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300">
              <Icon className="w-3.5 h-3.5" aria-hidden="true" /> {text}
            </a>
          );
        })}
      </div>
    </section>
  );
}

function authorizationLabel(t, offer) {
  if (offer.authorization === 'EXPLICIT_AGREEMENT') return t('Needs your approval');
  if (offer.authorization === 'POLICY_AUTHORIZED') return t('Handled under the return policy');
  if (offer.authorization === 'UNILATERAL') return offer.fundedBy === 'ME' ? t('Compensation from the app') : offer.fundedBy === 'THEM' ? t('Compensation from the seller') : t('Compensation from the app / seller');
  return '';
}

function actionSentence(t, a, order) {
  const items = itemsText(t, a.affected, order);
  const amount = fmtIDR(a.amount, a.currency || order?.currency);
  switch (a.type) {
    case 'REFUND': return items ? t('Refund of {amount} for {items}', { amount, items }) : t('Refund of {amount}', { amount });
    case 'REPLACEMENT': return items ? t('Replacement of {items}', { items }) : t('A replacement');
    case 'REDELIVERY': return items ? t('New delivery of {items}', { items }) : t('A new delivery');
    case 'INFORMATION_PROVIDED': return t('An explanation from the seller');
    case 'NO_ACTION': return t('No further action');
    default: return a.amount ? t('Compensation of {amount}', { amount }) : t('Another solution');
  }
}

function refundArrival(t, a, order) {
  const refunds = order?.refunds || [];
  const r = refunds.find(x => x.settlementId && x.settlementId === a.settlementRef)
    || refunds.find(x => x.reference && x.reference === a.executionReference)
    || refunds[refunds.length - 1];
  return r?.timelineDays ? t('Arrives within {n} days to your original payment method', { n: r.timelineDays }) : t('Arrives to your original payment method');
}

function OfferCard({ offer, order, actionable, onAccept, onReject, historic }) {
  const { t, locale } = useI18n();
  const fromSeller = offer.proposedBy === 'THEM';
  const informational = offer.authorization !== 'EXPLICIT_AGREEMENT';
  const lead = offer.authorization === 'POLICY_AUTHORIZED'
    ? t('Under the return policy:')
    : fromSeller ? t('The seller offers:') : t('Your proposal:');
  const executed = (offer.actions || []).filter(a => a.executed);
  return (
    <div className={`rounded-xl border px-4 py-3 space-y-3 ${historic ? 'border-white/[0.06] bg-white/[0.01] opacity-80' : actionable ? 'border-amber-400/50 bg-amber-500/[0.06]' : 'border-white/[0.08] bg-white/[0.02]'}`}>
      <div className="flex items-center gap-2 flex-wrap">
        <HandCoins className="w-4 h-4 text-amber-300" aria-hidden="true" />
        <span className="text-sm font-bold text-white">{historic ? t('Version {n}', { n: offer.version }) : t('Offer')}</span>
        {!historic && offer.version > 1 && <span className="text-[10px] font-bold px-1.5 py-0.5 rounded bg-cyan-500/15 text-cyan-200 border border-cyan-500/30">{t('Offer updated')}</span>}
        <span className="text-[10px] font-semibold px-1.5 py-0.5 rounded bg-white/[0.06] text-slate-300 border border-white/10">{authorizationLabel(t, offer)}</span>
      </div>
      <div className="space-y-2">
        <p className="text-xs text-slate-400">{lead}</p>
        <ul className="space-y-2">
          {(offer.actions || []).map((a, i) => (
            <li key={a.actionId || i} className="text-sm text-slate-100">
              <p className="font-semibold">{actionSentence(t, a, order)}</p>
              {a.description && <p className="text-xs text-slate-400 whitespace-pre-wrap break-words">{a.description}</p>}
              {a.liable && <p className="text-[11px] text-slate-500">{a.liable === 'THEM' ? t('Paid by the seller') : t('Paid by the app')}</p>}
            </li>
          ))}
        </ul>
        {offer.total > 0 && (offer.actions || []).length > 1 && (
          <p className="text-sm text-slate-200 flex justify-between border-t border-white/[0.06] pt-2"><span>{t('Total')}</span><span className="font-bold">{fmtIDR(offer.total, order?.currency)}</span></p>
        )}
        {informational && !historic && (offer.actions || []).some(a => a.type === 'REFUND') && !executed.length && (
          <p className="text-xs text-slate-400 flex items-start gap-1.5"><Info className="w-3.5 h-3.5 mt-0.5 shrink-0" aria-hidden="true" />
            {offer.authorization === 'POLICY_AUTHORIZED' ? t('Your money is refunded under the return policy. You do not need to do anything.') : t('This is paid to you directly. You do not need to do anything.')}</p>
        )}
      </div>

      {offer.decision && (
        <p className={`text-xs flex items-start gap-1.5 ${offer.decision.result === 'ACCEPTED' ? 'text-emerald-300' : 'text-rose-300'}`}>
          {offer.decision.result === 'ACCEPTED' ? <CircleCheck className="w-3.5 h-3.5 mt-0.5 shrink-0" aria-hidden="true" /> : <CircleX className="w-3.5 h-3.5 mt-0.5 shrink-0" aria-hidden="true" />}
          <span>
            {offer.decision.result === 'ACCEPTED'
              ? (offer.decision.by === 'ME' ? t('You accepted this offer') : t('The seller accepted this'))
              : (offer.decision.by === 'ME' ? t('You rejected this offer') : t('The seller rejected this'))}
            {offer.decision.at ? ` · ${fmtWIB(offer.decision.at, locale)}` : ''}
            {offer.decision.reason ? ` — "${offer.decision.reason}"` : ''}
          </span>
        </p>
      )}

      {!historic && executed.length > 0 && (
        <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 space-y-2">
          {executed.map((a, i) => (
            <div key={a.actionId || i} className="text-xs text-emerald-100 space-y-0.5">
              <p className="font-semibold flex items-center gap-1.5"><CircleCheck className="w-3.5 h-3.5" aria-hidden="true" />
                {a.type === 'REFUND' ? t('Money refunded: {amount}', { amount: fmtIDR(a.amount, a.currency || order?.currency) }) : t('Done: {what}', { what: actionSentence(t, a, order) })}
              </p>
              {a.type === 'REFUND' && a.executionReference && <p>{t('Refund reference')}: <span className="font-mono">{a.executionReference}</span></p>}
              {a.type === 'REFUND' && <p>{refundArrival(t, a, order)}</p>}
              {a.completedAt && <p className="text-emerald-300/80">{fmtWIB(a.completedAt, locale)}</p>}
            </div>
          ))}
        </div>
      )}

      {actionable && (
        <div className="grid grid-cols-2 gap-2">
          <button type="button" className={btnDanger} onClick={onReject}><CircleX className="w-4 h-4" aria-hidden="true" /> {t('Reject')}</button>
          <button type="button" className={btnPrimary} onClick={onAccept}><CircleCheck className="w-4 h-4" aria-hidden="true" /> {t('Accept')}</button>
        </div>
      )}
    </div>
  );
}

// A grievance in full: progress, next step, offer, timeline and actions.
export function GrievanceDetail({ id }) {
  const { t, locale } = useI18n();
  const now = useNow();
  const [view, setView] = useState(null);
  const [loadError, setLoadError] = useState(null);
  const [loadKey, setLoadKey] = useState(0);
  // Pending user action: { kind: 'accept'|'reject'|'objection'|'close'|'response', text }
  const [form, setForm] = useState(null);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState(null);
  const [showHistory, setShowHistory] = useState(false);

  useEffect(() => {
    let alive = true;
    apiJSON(`/grievances/${encodeURIComponent(id)}`)
      .then(d => { if (alive) { setView(d); setLoadError(null); } })
      .catch(e => { if (alive) setLoadError(e); });
    return () => { alive = false; };
  }, [id, loadKey]);
  useLive(() => setLoadKey(k => k + 1), !!view && !busy);

  if (!view && !loadError) return <Spinner label={t('Loading the report…')} />;
  if (!view) return (
    <div className="space-y-3 py-8">
      <ErrorBox load error={loadError} onRetry={loadError.code === 'NOT_FOUND' ? null : () => { setLoadError(null); setLoadKey(k => k + 1); }} />
    </div>
  );

  const order = view.order;
  const cat = categoryOf(view.category);
  const ns = nextStep(t, view, locale);
  const n = view.next || {};
  const canDecide = n.action === 'DECIDE' && n.waitingOn === 'ME' && !!view.offer;
  const canRespond = view.raisedBy === 'SELLER' && n.action === 'PROPOSE' && n.waitingOn === 'ME';
  const resolvedOpen = view.caseStatus === 'RESOLVED' && !view.deemedClosed && view.openedBy === 'ME';
  const objectionLeft = view.objectionDeadline ? timeLeft(t, view.objectionDeadline, now) : null;
  const canObject = resolvedOpen && (!view.objectionDeadline || !!objectionLeft);
  const canClose = resolvedOpen;
  const relatedLabels = (view.related || []).map(r => relatedLabel(t, r, order, locale)).filter(Boolean);
  const answeredAfterSend = view.lastSent?.by === 'ME' && (view.timeline || []).some(e => e.by === 'THEM' && new Date(e.at) > new Date(view.lastSent.at));

  const retryable = !!actionError && !['INVALID', 'NOT_FOUND'].includes(actionError.code);
  const dismiss = () => { setForm(null); setActionError(null); };
  const submit = async () => {
    setBusy(true);
    setActionError(null);
    try {
      const enc = encodeURIComponent(id);
      let v;
      const text = (form.text || '').trim();
      if (form.kind === 'accept') v = await apiJSON(`/grievances/${enc}/decision`, { method: 'POST', body: { accept: true } });
      else if (form.kind === 'reject') v = await apiJSON(`/grievances/${enc}/decision`, { method: 'POST', body: { accept: false, ...(text ? { reason: text } : {}) } });
      else if (form.kind === 'objection') v = await apiJSON(`/grievances/${enc}/objection`, { method: 'POST', body: { explanation: text } });
      else if (form.kind === 'close') v = await apiJSON(`/grievances/${enc}/close`, { method: 'POST' });
      else if (form.kind === 'response') v = await apiJSON(`/grievances/${enc}/response`, { method: 'POST', body: { description: text } });
      setView(prev => ({ ...prev, ...v }));
      setForm(null);
      setConfirming(false);
    } catch (e) {
      setActionError(e); // keep what was typed; offer a retry
      setConfirming(false);
    } finally {
      setBusy(false);
    }
  };

  const confirmCopy = form && {
    accept: { title: t('Accept this offer?'), body: view.offer && <ul className="list-disc pl-5">{(view.offer.actions || []).map((a, i) => <li key={a.actionId || i}>{actionSentence(t, a, order)}</li>)}</ul>, label: t('Accept'), tone: 'primary' },
    reject: { title: t('Reject this offer?'), body: <p>{t('The seller can then send a new offer.')}</p>, label: t('Reject'), tone: 'danger' },
    objection: { title: t('Send that the problem is not solved?'), body: <p>{t('The seller will look at your report again.')}</p>, label: t('Send'), tone: 'primary' },
    close: { title: t('Close this report?'), body: <p>{t('Close it only if the problem is solved. You cannot reopen it.')}</p>, label: t('Close report'), tone: 'danger' },
    response: { title: t('Send your explanation?'), body: null, label: t('Send'), tone: 'primary' },
  }[form.kind];

  const textForm = (kind, label, placeholder, required) => form?.kind === kind && (
    <div className="space-y-2">
      <label htmlFor={`gv-${kind}`} className="text-xs font-semibold text-slate-300">{label}</label>
      <textarea id={`gv-${kind}`} rows={4} maxLength={MAX_DESC} autoFocus value={form.text || ''}
        onChange={e => setForm(f => ({ ...f, text: e.target.value }))} placeholder={placeholder} className={inputCls} />
      <p className="text-right text-[11px] text-slate-500 tabular-nums">{(form.text || '').length}/{MAX_DESC}</p>
      <ErrorBox error={actionError} onRetry={retryable ? submit : null} />
      <div className="grid grid-cols-2 gap-2">
        <button type="button" className={btnGhost} disabled={busy} onClick={() => { setForm(null); setActionError(null); }}>{t('Cancel')}</button>
        <button type="button" className={kind === 'reject' ? btnDanger : btnPrimary} disabled={busy || (required && !(form.text || '').trim())} onClick={() => setConfirming(true)}>
          {t('Continue')}
        </button>
      </div>
    </div>
  );

  return (
    <div className="space-y-4">
      {/* Summary */}
      <section className={`${card} px-4 py-3 space-y-2`}>
        <div className="flex items-start gap-3">
          <div className="w-10 h-10 rounded-xl bg-white/[0.05] flex items-center justify-center shrink-0">
            <cat.Icon className="w-5 h-5 text-cyan-300" aria-hidden="true" />
          </div>
          <div className="flex-1 min-w-0 space-y-1">
            <p className="text-base font-bold text-white">{t(cat.label)}</p>
            <div className="flex items-center gap-2 flex-wrap"><StatusChip view={view} />{view.needsAction && <NeedsActionBadge />}</div>
          </div>
        </div>
        <p className="text-[11px]"><GrievanceNumber id={view.id} /></p>
        <p className="text-[11px] text-slate-500">{view.openedBy === 'ME' ? t('Reported by you {time}', { time: fmtWIB(view.openedAt, locale) }) : t('Reported by the seller {time}', { time: fmtWIB(view.openedAt, locale) })}</p>
        {view.affected?.length > 0 && <p className="text-sm text-slate-300">{itemsText(t, view.affected, order)}</p>}
        {relatedLabels.map(l => <p key={l} className="text-xs text-slate-400">{t('About')}: {l}</p>)}
      </section>

      <Stepper view={view} />

      {/* Next step */}
      <section aria-live="polite" className={`rounded-xl border px-4 py-3 space-y-1 ${ns.mine ? 'border-rose-500/40 bg-rose-500/10' : 'border-cyan-500/25 bg-cyan-500/[0.06]'}`}>
        <p className="text-[10px] font-bold uppercase tracking-widest text-slate-400">{t('Next step')}</p>
        <p className={`text-sm font-bold flex items-center gap-1.5 ${ns.mine ? 'text-rose-100' : 'text-cyan-100'}`}>
          {ns.mine ? <TriangleAlert className="w-4 h-4" aria-hidden="true" /> : statusKey(view) === 'CLOSED' ? <Lock className="w-4 h-4" aria-hidden="true" /> : <Hourglass className="w-4 h-4" aria-hidden="true" />}
          {ns.title}
        </p>
        {ns.detail && <p className="text-xs text-slate-300">{ns.detail}</p>}
        {!answeredAfterSend && <SentState view={view} />}
        {answeredAfterSend && <p className="text-xs text-purple-200 flex items-center gap-1.5"><Store className="w-3.5 h-3.5" aria-hidden="true" /> {t('The seller has answered')}</p>}
      </section>

      {/* Seller-raised */}
      {view.raisedBy === 'SELLER' && (
        <section className="rounded-xl border border-purple-500/30 bg-purple-500/[0.06] px-4 py-3 space-y-2">
          <p className="text-sm font-bold text-purple-100 flex items-center gap-1.5"><Store className="w-4 h-4" aria-hidden="true" /> {t('The seller reported a problem')}</p>
          {view.description && <p className="text-sm text-slate-200 whitespace-pre-wrap break-words">{view.description}</p>}
          {canRespond && !form && (
            <button type="button" className={btnPrimary} onClick={() => { setActionError(null); setForm({ kind: 'response', text: '' }); }}>
              <MessageCircle className="w-4 h-4" aria-hidden="true" /> {t('Give an explanation')}
            </button>
          )}
          {textForm('response', t('Your explanation'), t('Explain what happened from your side, for example which item you returned and its condition.'), true)}
        </section>
      )}
      {view.raisedBy !== 'SELLER' && view.description && (
        <section className={`${card} px-4 py-3`}>
          <p className="text-[11px] font-bold uppercase tracking-widest text-slate-500 mb-1">{t('What you told us')}</p>
          <p className="text-sm text-slate-200 whitespace-pre-wrap break-words">{view.description}</p>
        </section>
      )}

      {/* Offer */}
      {view.offer && (
        <section className="space-y-2" aria-label={t('Offer')}>
          <OfferCard offer={view.offer} order={order} actionable={canDecide && !form}
            onAccept={() => { setActionError(null); setForm({ kind: 'accept' }); setConfirming(true); }}
            onReject={() => { setActionError(null); setForm({ kind: 'reject', text: '' }); }} />
          {form?.kind === 'accept' && <ErrorBox error={actionError} onRetry={retryable ? submit : null} onDismiss={dismiss} />}
          {textForm('reject', t('Why do you reject it? (optional)'), t('For example: the refund does not cover the delivery fee.'), false)}
          {(view.history || []).length > 0 && (
            <div>
              <button type="button" onClick={() => setShowHistory(s => !s)} aria-expanded={showHistory}
                className="text-xs text-slate-400 hover:text-white flex items-center gap-1 py-1 focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-300 rounded">
                <ChevronDown className={`w-3.5 h-3.5 transition-transform ${showHistory ? 'rotate-180' : ''}`} aria-hidden="true" />
                {t('Earlier offers ({n})', { n: view.history.length })}
              </button>
              {showHistory && <div className="space-y-2 mt-2">{view.history.map(o => <OfferCard key={`${o.proposalId}-${o.version}`} offer={o} order={order} historic />)}</div>}
            </div>
          )}
        </section>
      )}

      {/* After resolution */}
      {resolvedOpen && (
        <section className={`${card} px-4 py-3 space-y-3`}>
          {!form && (
            <div className="grid gap-2 sm:grid-cols-2">
              {canObject && (
                <button type="button" className={btnGhost} onClick={() => { setActionError(null); setForm({ kind: 'objection', text: '' }); }}>
                  <TriangleAlert className="w-4 h-4" aria-hidden="true" /> {t('Problem not solved?')}
                  {objectionLeft && <span className="text-[11px] text-slate-400 font-normal">({objectionLeft})</span>}
                </button>
              )}
              {canClose && (
                <button type="button" className={btnPrimary} onClick={() => { setActionError(null); setForm({ kind: 'close' }); setConfirming(true); }}>
                  <Lock className="w-4 h-4" aria-hidden="true" /> {t('Close report')}
                </button>
              )}
            </div>
          )}
          {!canObject && <p className="text-xs text-slate-400">{t('The time to object has ended; the report will close automatically.')}</p>}
          {form?.kind === 'close' && <ErrorBox error={actionError} onRetry={retryable ? submit : null} onDismiss={dismiss} />}
          {textForm('objection', t('What is still wrong?'), t('For example: the refund has not arrived, or the replacement is also damaged.'), true)}
        </section>
      )}
      {view.deemedClosed && (
        <p className="text-xs text-slate-400 flex items-center gap-1.5"><Lock className="w-3.5 h-3.5" aria-hidden="true" /> {t('Closed automatically')}</p>
      )}

      <Channels channels={view.channels} />
      <OrderContext order={order} affected={view.affected} />
      <Timeline view={view} />

      {confirming && confirmCopy && (
        <ConfirmDialog title={confirmCopy.title} body={confirmCopy.body} confirmLabel={confirmCopy.label} tone={confirmCopy.tone} busy={busy}
          onConfirm={submit}
          onCancel={() => { setConfirming(false); if (form && (form.kind === 'accept' || form.kind === 'close')) setForm(null); }} />
      )}
    </div>
  );
}
