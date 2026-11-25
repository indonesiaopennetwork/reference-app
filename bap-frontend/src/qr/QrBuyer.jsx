import { useCallback, useEffect, useState } from "react";
import {
  MapPin, Minus, Plus, Store, Truck, HandCoins, CheckCircle2, AlertCircle, RefreshCw, ChevronRight, ShieldCheck, Receipt, PackageCheck, QrCode, Star, ArrowLeft, XCircle, LifeBuoy,
} from 'lucide-react';
import { useI18n } from '../i18n.jsx';
import { basePath, captureTokenFromUrl, getToken } from '../auth.js';
import { qrApi, QrError } from './api.js';
import { formatDistance, rupiah } from './domain.js';
import { Alert, Button, Card, Field, Spinner } from './ui.jsx';
import { cx, inputClass } from './styles.js';

// The page a buyer lands on after scanning a packet QR: qr-service redirects
// {bap-frontend}/?packet_id=…&scan_id=… here. No login needed. Flow:
//   resolve (nearest merchant) → [pick merchant] → choose items and how many
//   (min basket) → quote (Beckn select)
//   → details + checkout (Beckn init) → place order (Beckn confirm).
// Payment is not collected in this flow yet.
// Progress is kept in sessionStorage so a reload resumes the same checkout.

const SESSION_KEY = id => `qr.checkout.${id}`;
const BUYER_KEY = 'qr.buyer';

function readJSON(storage, key) {
  try { return JSON.parse(storage.getItem(key) || 'null'); } catch { return null; }
}
function writeJSON(storage, key, value) {
  try { value == null ? storage.removeItem(key) : storage.setItem(key, JSON.stringify(value)); } catch { /* storage blocked */ }
}

function packetIdFromUrl() {
  const q = new URLSearchParams(window.location.search);
  if (q.get('packet_id')) return q.get('packet_id');
  const m = window.location.pathname.slice(basePath.length).match(/^qr\/([a-z0-9]+)/i);
  return m ? m[1] : '';
}

export default function QrBuyer() {
  const { t } = useI18n();
  const [packetId] = useState(packetIdFromUrl);
  const [scanId] = useState(() => new URLSearchParams(window.location.search).get("scan_id") || "");
  // Scan simulator demo scenario (SINGLE | MULTI | QUOTE_CHANGED): the BAP
  // plays the merchants itself. Only honoured when the BAP enables demos.
  const [demo] = useState(() => (new URLSearchParams(window.location.search).get("demo") || "").toUpperCase());

  const [packet, setPacket] = useState(null);
  const [stage, setStage] = useState(packetId ? 'resolving' : 'error'); // resolving | pick | choose | quoting | review | place | order | none | error
  const [candidate, setCandidate] = useState(null); // the merchant the buyer is choosing from
  const [chosen, setChosen] = useState({}); // packet_item_id → quantity
  const [resolved, setResolved] = useState(null);
  const [quote, setQuote] = useState(null);
  const [amount, setAmount] = useState(null); // order total to place (resumed order)
  const [session, setSessionState] = useState(() => readJSON(sessionStorage, SESSION_KEY(packetId)));
  const [error, setError] = useState(() => (packetId ? null : new QrError(404, 'PACKET_NOT_FOUND', 'This link has no packet id')));
  const [notice, setNotice] = useState(null);

  const setSession = useCallback(next => {
    setSessionState(next);
    writeJSON(sessionStorage, SESSION_KEY(packetId), next);
  }, [packetId]);

  // Fresh resolve: discover the merchants around the packet's node.
  const runResolve = useCallback(async () => {
    try {
      let scanKey = '';
      if (!scanId) {
        scanKey = sessionStorage.getItem(`qr.scanKey.${packetId}`) || crypto.randomUUID();
        try { sessionStorage.setItem(`qr.scanKey.${packetId}`, scanKey); } catch { /* storage blocked */ }
      }
      const r = await qrApi.resolve({
        packet_id: packetId,
        ...(scanId ? { scan_id: scanId } : { client_scan_key: scanKey }),
        ...(demo ? { demo_scenario: demo } : {}),
      });
      setPacket(r.packet);
      setResolved(r);
      if (r.mode === 'NO_MERCHANT') { setStage('none'); return; }
      setSession({ guestToken: r.guest_token, transactionId: null });
      setChosen({});
      if (r.mode === 'FAST_CHECKOUT') {
        setCandidate(r.candidates[0]);
        setStage('choose');
      } else {
        setStage('pick');
      }
    } catch (e) {
      setError(e);
      setStage('error');
    }
  }, [packetId, scanId, demo, setSession]);

  const resolve = useCallback(() => {
    setStage('resolving');
    setError(null);
    setNotice(null);
    setQuote(null);
    return runResolve();
  }, [runResolve]);

  // On load: show the packet, then resume a checkout from this tab or resolve.
  useEffect(() => {
    if (new URLSearchParams(window.location.search).get('token')) captureTokenFromUrl();
    if (!packetId) return;
    qrApi.packet(packetId).then(setPacket).catch(() => {});
    const s = readJSON(sessionStorage, SESSION_KEY(packetId));
    if (s?.transactionId && s?.guestToken) {
      qrApi.order(s.transactionId, s.guestToken).then(o => {
        if (o.state === 'AWAITING_CONFIRM') { setAmount(o.total); setStage('place'); }
        else if (['CONFIRM_SENT', 'CONFIRMED', 'FAILED'].includes(o.state)) setStage('order');
        else resolve();
      }).catch(() => resolve());
    } else {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- sets state only after its await
      runResolve();
    }
  }, [packetId, resolve, runResolve]);

  // Picking a merchant starts a fresh choice (its items and prices differ).
  const pick = c => {
    if (c.candidate_id !== candidate?.candidate_id) setChosen({});
    setCandidate(c);
    setNotice(null);
    setStage('choose');
  };

  // Quote what the buyer chose (Beckn select).
  const getQuote = async () => {
    setStage('quoting');
    setError(null);
    setNotice(null);
    try {
      const items = Object.entries(chosen).filter(([, n]) => n > 0).map(([id, n]) => ({ packet_item_id: id, quantity: n }));
      const q = await qrApi.quote(candidate.candidate_id, items, session.guestToken);
      setQuote(q);
      setSession({ ...session, transactionId: q.transaction_id });
      setStage('review');
    } catch (e) {
      setNotice(e.message);
      setStage('choose');
    }
  };

  // Beckn confirm at the agreed total; errors go back to the caller.
  const placeOrder = async total => {
    await qrApi.placeOrder(session.transactionId, total, session.guestToken);
    setStage('order');
  };

  const restart = () => { setSession(null); resolve(); };

  return (
    <div className="qr-scope min-h-screen pb-16">
      <header className="sticky top-0 z-20 glass border-b border-white/[0.06]">
        <div className="mx-auto flex max-w-xl items-center gap-3 px-4 h-14">
          <a href={basePath} className="text-lg font-bold text-white">ION</a>
          <span className="flex items-center gap-1.5 rounded-full bg-brand/10 px-2.5 py-1 text-xs font-semibold text-brand">
            <QrCode className="w-3.5 h-3.5" /> {t('QR order')}
          </span>
          {demo && (
            <span className="rounded-full bg-violet-500/20 px-2 py-0.5 text-[10px] font-bold tracking-wide text-violet-200" title={t('Demo merchants: no real order is placed')}>
              DEMO
            </span>
          )}
          {stage !== 'resolving' && stage !== 'order' && (
            <button type="button" onClick={restart} className="ml-auto text-xs text-slate-400 hover:text-white flex items-center gap-1">
              <RefreshCw className="w-3.5 h-3.5" /> {t('Start over')}
            </button>
          )}
        </div>
      </header>

      <main className="mx-auto max-w-xl space-y-4 px-4 pt-4">
        <PacketHeader packet={packet} />

        {stage === 'resolving' && <Progress title={t('Finding the nearest merchant…')} lines={[t('Searching shops near this spot on the ION network'), t('Checking who stocks every item')]} />}
        {stage === 'quoting' && <Progress title={t('Asking the merchant for a price…')} lines={[t('Sending your basket to the merchant'), t('Waiting for their quote')]} />}
        {stage === 'error' && <ErrorScreen error={error} onRetry={resolve} />}
        {stage === 'none' && <NoMerchant resolved={resolved} onRetry={resolve} />}
        {stage === 'pick' && <PickMerchant resolved={resolved} notice={notice} onPick={pick} />}
        {stage === 'choose' && candidate && (
          <ChooseItems
            candidate={candidate}
            minBasket={packet?.min_basket_value || 0}
            chosen={chosen}
            setChosen={setChosen}
            notice={notice}
            onBack={resolved?.candidates?.length > 1 ? () => setStage('pick') : null}
            onNext={getQuote}
          />
        )}
        {stage === 'review' && quote && (
          <Review
            quote={quote}
            guestToken={session?.guestToken}
            onBack={() => setStage('choose')}
            onPlace={placeOrder}
            onExpired={restart}
          />
        )}
        {stage === 'place' && session && <PlaceOrder amount={amount} onPlace={placeOrder} onExpired={restart} />}
        {stage === 'order' && session && <OrderTracking transactionId={session.transactionId} guestToken={session.guestToken} onNew={restart} />}
      </main>
    </div>
  );
}

/* ─────────────────────────── packet header ───────────────────────────────── */

function PacketHeader({ packet }) {
  const { t } = useI18n();
  if (!packet) return <div className="glass h-24 animate-pulse rounded-2xl" />;
  return (
    <section className="glass rounded-2xl p-4">
      <p className="text-[11px] font-bold tracking-[0.2em] text-brand uppercase">{t('Scan · Order · Delivered')}</p>
      <h1 className="mt-1 text-2xl text-white">{packet.title}</h1>
      <p className="mt-1 flex items-start gap-1.5 text-sm text-slate-400">
        <MapPin className="mt-0.5 w-4 h-4 shrink-0" /> {packet.location_label}
      </p>
      <div className="mt-3 flex flex-wrap gap-1.5">
        {packet.items?.map(it => (
          <span key={it.id} className="rounded-lg border border-white/10 bg-white/[0.06] px-2 py-1 text-xs text-slate-200">{it.display_name}</span>
        ))}
      </div>
      {packet.status && packet.status !== 'ACTIVE' && (
        <p className="mt-3 text-xs font-semibold text-amber-300">{t('This packet is not taking orders right now.')}</p>
      )}
    </section>
  );
}

function Progress({ title, lines }) {
  const [i, setI] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setI(n => Math.min(n + 1, lines.length - 1)), 2500);
    return () => clearInterval(id);
  }, [lines.length]);
  return (
    <Card>
      <div className="flex flex-col items-center gap-4 py-6 text-center">
        <Spinner className="w-8 h-8 text-brand" />
        <p className="text-base font-semibold text-white">{title}</p>
        <ul className="space-y-1.5 text-sm">
          {lines.map((l, n) => (
            <li key={l} className={cx('flex items-center gap-2', n < i ? 'text-emerald-300' : n === i ? 'text-slate-200' : 'text-slate-600')}>
              {n < i ? <CheckCircle2 className="w-4 h-4" /> : <span className="w-4 h-4 inline-block rounded-full border border-current" />}
              {l}
            </li>
          ))}
        </ul>
      </div>
    </Card>
  );
}

function ErrorScreen({ error, onRetry }) {
  const { t } = useI18n();
  const inactive = error?.code === 'PACKET_NOT_ACTIVE';
  const notFound = error?.code === 'PACKET_NOT_FOUND';
  return (
    <Card>
      <div className="flex flex-col items-center gap-3 py-6 text-center">
        <AlertCircle className="w-10 h-10 text-rose-400" />
        <p className="text-lg font-semibold text-white">
          {inactive ? t('This QR code is paused') : notFound ? t('Unknown QR code') : t('Something went wrong')}
        </p>
        <p className="max-w-sm text-sm text-slate-400">{error?.message}</p>
        <div className="flex gap-2">
          {!notFound && !inactive && <Button onClick={onRetry}><RefreshCw className="w-4 h-4" /> {t('Try again')}</Button>}
          <a href={basePath}><Button variant="secondary">{t('Browse the shop')}</Button></a>
        </div>
      </div>
    </Card>
  );
}

function NoMerchant({ resolved, onRetry }) {
  const { t } = useI18n();
  return (
    <Card>
      <div className="flex flex-col items-center gap-3 py-6 text-center">
        <Store className="w-10 h-10 text-amber-300" />
        <p className="text-lg font-semibold text-white">{t('No merchant can fill this basket right now')}</p>
        <p className="max-w-sm text-sm text-slate-400">{resolved?.message}</p>
        {resolved?.missing_items?.length > 0 && (
          <p className="text-sm text-slate-300">{t('Not available nearby:')} <b>{resolved.missing_items.join(', ')}</b></p>
        )}
        <div className="flex gap-2">
          <Button onClick={onRetry}><RefreshCw className="w-4 h-4" /> {t('Check again')}</Button>
          <a href={basePath}><Button variant="secondary">{t('Browse the shop')}</Button></a>
        </div>
      </div>
    </Card>
  );
}

/* ─────────────────────────── merchant picker ─────────────────────────────── */

function PickMerchant({ resolved, notice, onPick }) {
  const { t } = useI18n();
  return (
    <div className="space-y-3">
      <div>
        <h2 className="text-lg text-white">{t('Choose a merchant')}</h2>
        <p className="text-sm text-slate-400">{t('{n} shops nearby stock these items. Nearest first.', { n: resolved.candidates.length })}</p>
      </div>
      {notice && <Alert tone="warning">{notice}</Alert>}
      {resolved.candidates.map(c => (
        <button key={c.candidate_id} type="button" onClick={() => onPick(c)}
          className="glass w-full rounded-2xl p-4 text-left transition-colors hover:border-brand/40">
          <div className="flex items-start gap-3">
            <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-brand/10 text-brand"><Store className="w-5 h-5" /></div>
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <p className="font-semibold text-white">{c.provider_name}</p>
                {c.recommended && <span className="flex items-center gap-1 rounded-full bg-emerald-500/15 px-2 py-0.5 text-[11px] font-bold text-emerald-300"><Star className="w-3 h-3" /> {t('Best match')}</span>}
              </div>
              <p className="mt-0.5 text-xs text-slate-400">
                {c.distance_meters != null ? t('{d} away', { d: formatDistance(c.distance_meters) }) : t('Distance unknown')}
                {c.address && ` · ${c.address}`}
              </p>
              <p className="mt-1 text-xs text-slate-400">
                {t('{n} items', { n: c.items_total })}
              </p>
            </div>
            <div className="text-right">
              <p className="font-semibold text-white">{rupiah(c.estimated_subtotal)}</p>
              <p className="text-[11px] text-slate-500">{t('one of each')}{c.delivery_fee != null && ` · + ${rupiah(c.delivery_fee)} ${t('delivery')}`}</p>
              <ChevronRight className="ml-auto mt-1 w-4 h-4 text-slate-500" />
            </div>
          </div>
        </button>
      ))}
    </div>
  );
}

/* ─────────────────────────── choose items ────────────────────────────────── */

// ChooseItems: the buyer picks which of the merchant's items to order and how
// many. The subtotal must reach the packet's minimum basket before quoting.
function ChooseItems({ candidate, minBasket, chosen, setChosen, notice, onBack, onNext }) {
  const { t } = useI18n();
  const qtyOf = l => chosen[l.packet_item_id] || 0;
  const setQty = (l, n) => setChosen(c => ({ ...c, [l.packet_item_id]: Math.max(0, Math.min(999, n)) }));
  const subtotal = candidate.lines.reduce((sum, l) => sum + l.unit_price * qtyOf(l), 0);
  const count = candidate.lines.reduce((sum, l) => sum + qtyOf(l), 0);
  const short = Math.max(0, minBasket - subtotal);
  return (
    <div className="space-y-3">
      <Card>
        <div className="flex items-start gap-3">
          <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-brand/10 text-brand"><Store className="w-5 h-5" /></div>
          <div className="min-w-0 flex-1">
            <p className="text-xs text-slate-400">{t('Choose from')}</p>
            <p className="font-semibold text-white">{candidate.provider_name}</p>
            <p className="text-xs text-slate-400">{candidate.distance_meters != null ? t('{d} away', { d: formatDistance(candidate.distance_meters) }) : t('Distance unknown')}{candidate.address && ` · ${candidate.address}`}</p>
          </div>
          {onBack && <button type="button" onClick={onBack} className="text-xs text-brand hover:underline flex items-center gap-1"><ArrowLeft className="w-3 h-3" />{t('Change')}</button>}
        </div>
        <ul className="mt-4 divide-y divide-white/[0.06]">
          {candidate.lines.map(l => {
            const n = qtyOf(l);
            return (
              <li key={l.packet_item_id} className="flex items-center gap-3 py-2.5">
                {l.image ? <img src={l.image} alt="" className="h-12 w-12 rounded-lg object-cover" /> : <div className="h-12 w-12 rounded-lg bg-white/[0.06]" />}
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm text-slate-100">{l.name}</p>
                  <p className="text-xs text-slate-400">{rupiah(l.unit_price)}</p>
                </div>
                {n === 0 ? (
                  <Button size="sm" variant="secondary" onClick={() => setQty(l, 1)}><Plus className="w-3.5 h-3.5" /> {t('Add')}</Button>
                ) : (
                  <div className="flex items-center rounded-xl border border-brand/40 bg-brand/10" aria-label={t('Quantity')}>
                    <button type="button" className="px-2.5 py-1.5 text-brand" onClick={() => setQty(l, n - 1)} aria-label={t('Less')}><Minus className="w-3.5 h-3.5" /></button>
                    <span className="w-7 text-center text-sm font-semibold text-white">{n}</span>
                    <button type="button" className="px-2.5 py-1.5 text-brand disabled:opacity-40" disabled={n >= 999} onClick={() => setQty(l, n + 1)} aria-label={t('More')}><Plus className="w-3.5 h-3.5" /></button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      </Card>

      {notice && <Alert tone="warning">{notice}</Alert>}

      <div className="glass sticky bottom-3 rounded-2xl p-4">
        <div className="flex items-center justify-between text-sm">
          <span className="text-slate-300">{t('{n} items', { n: count })}</span>
          <span className="font-semibold text-white">{rupiah(subtotal)}</span>
        </div>
        {minBasket > 0 && (
          <p className={cx('mt-1 text-xs', short > 0 ? 'text-amber-300' : 'text-emerald-300')}>
            {short > 0
              ? t('Minimum order {min}: add {more} more', { min: rupiah(minBasket), more: rupiah(short) })
              : t('Minimum order {min} reached', { min: rupiah(minBasket) })}
          </p>
        )}
        <Button size="lg" className="mt-3 w-full" disabled={count === 0 || short > 0} onClick={onNext}>
          {t('Continue')} <ChevronRight className="w-4 h-4" />
        </Button>
      </div>
    </div>
  );
}

/* ─────────────────────────── review + checkout ───────────────────────────── */

function PriceRows({ pricing }) {
  const { t } = useI18n();
  return (
    <dl className="space-y-1.5 text-sm">
      <Row label={t('Items')} value={rupiah(pricing.items_subtotal)} />
      {pricing.tax > 0 && <Row label={t('Tax')} value={rupiah(pricing.tax)} />}
      {pricing.delivery_fee > 0 && <Row label={t('Delivery fee')} value={rupiah(pricing.delivery_fee)} />}
      <div className="flex justify-between border-t border-white/10 pt-2 text-base font-bold text-white">
        <dt>{t('Total')}</dt><dd>{rupiah(pricing.grand_total)}</dd>
      </div>
    </dl>
  );
}

const Row = ({ label, value }) => (
  <div className="flex justify-between text-slate-300"><dt>{label}</dt><dd>{value}</dd></div>
);

function Review({ quote, guestToken, onBack, onPlace, onExpired }) {
  const { t } = useI18n();
  const nodeOk = quote.node.allow_node_as_delivery;
  const saved = readJSON(localStorage, BUYER_KEY) || {};
  const [form, setForm] = useState({
    mode: nodeOk ? 'DELIVERY_TO_QR_NODE' : 'SELF_PICKUP',
    unit: saved.unit || '', name: saved.name || '', phone: saved.phone || '',
  });
  const [errors, setErrors] = useState({});
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState(null);
  const [changed, setChanged] = useState(null); // QUOTE_CHANGED data
  const set = (k, v) => setForm(f => ({ ...f, [k]: v }));

  const submit = async e => {
    e.preventDefault();
    const errs = {};
    if (!form.name.trim()) errs.name = t('Enter your name');
    if (!/^(\+62|62|0)8[\d\s-]{7,15}$/.test(form.phone.trim())) errs.phone = t('Enter an Indonesian mobile number, e.g. 0812 3456 7890');
    setErrors(errs);
    if (Object.keys(errs).length) return;
    writeJSON(localStorage, BUYER_KEY, { name: form.name.trim(), phone: form.phone.trim(), unit: form.unit.trim() });
    setBusy(true);
    setFailure(null);
    try {
      const r = await qrApi.checkout({
        transaction_id: quote.transaction_id, fulfillment_mode: form.mode,
        unit_label: form.mode === 'DELIVERY_TO_QR_NODE' ? form.unit.trim() : '',
        buyer_name: form.name.trim(), buyer_phone: form.phone.trim(),
      }, guestToken);
      await onPlace(r.pricing.grand_total);
    } catch (err) {
      if (err.code === 'QUOTE_CHANGED' && err.data) setChanged(err.data);
      else if (err.code === 'INVALID_PHONE') setErrors({ phone: err.message });
      else setFailure(err);
    } finally {
      setBusy(false);
    }
  };

  const acceptChanged = async () => {
    setBusy(true);
    setFailure(null);
    try { await onPlace(changed.pricing.grand_total); } catch (err) { setFailure(err); } finally { setBusy(false); }
  };

  if (changed) {
    return (
      <Card>
        <div className="space-y-4">
          <Alert tone="warning" title={t('The merchant updated the total')}>
            <p>{t('Was {a}, now {b}.', { a: rupiah(changed.previous_total), b: rupiah(changed.pricing.grand_total) })}</p>
          </Alert>
          <PriceRows pricing={changed.pricing} />
          {failure && <Alert tone="error" title={t('Couldn\'t place the order')}><p>{failure.message}</p></Alert>}
          <Button size="lg" className="w-full" busy={busy} onClick={acceptChanged}>
            {t('Accept and place order · {total}', { total: rupiah(changed.pricing.grand_total) })}
          </Button>
          <Button variant="ghost" className="w-full" onClick={onExpired}>{t('Cancel and start over')}</Button>
        </div>
      </Card>
    );
  }

  const m = quote.merchant;
  return (
    <form onSubmit={submit} className="space-y-4" noValidate>
      <Card>
        <div className="flex items-start gap-3">
          <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-emerald-500/10 text-emerald-300"><Store className="w-5 h-5" /></div>
          <div className="min-w-0 flex-1">
            <p className="text-xs text-slate-400">{t('Your basket from')}</p>
            <p className="font-semibold text-white">{m.provider_name}</p>
            <p className="text-xs text-slate-400">{m.distance_meters != null ? t('{d} away', { d: formatDistance(m.distance_meters) }) : t('Distance unknown')}{m.address && ` · ${m.address}`}</p>
          </div>
          {onBack && <button type="button" onClick={onBack} className="text-xs text-brand hover:underline flex items-center gap-1"><ArrowLeft className="w-3 h-3" />{t('Change')}</button>}
        </div>
        <ul className="mt-4 space-y-2">
          {quote.lines.map(l => (
            <li key={l.packet_item_id + l.name} className="flex items-center gap-3 text-sm">
              {l.image ? <img src={l.image} alt="" className="h-10 w-10 rounded-lg object-cover" /> : <div className="h-10 w-10 rounded-lg bg-white/[0.06]" />}
              <div className="min-w-0 flex-1">
                <p className="truncate text-slate-100">{l.name}</p>
                <p className="truncate text-xs text-slate-500">{l.offer_name} · {rupiah(l.unit_price)} × {l.quantity}</p>
              </div>
              <p className="text-slate-200">{rupiah(l.subtotal)}</p>
            </li>
          ))}
        </ul>
        <div className="mt-4"><PriceRows pricing={quote.pricing} /></div>
        <p className="mt-2 flex items-center gap-1.5 text-[11px] text-slate-500"><ShieldCheck className="w-3.5 h-3.5" /> {t('Price quoted by the merchant over the ION network')}</p>
      </Card>

      <Card title={t('How do you want it?')}>
        <div className="grid gap-2 sm:grid-cols-2">
          <ModeOption active={form.mode === 'DELIVERY_TO_QR_NODE'} disabled={!nodeOk} onClick={() => set('mode', 'DELIVERY_TO_QR_NODE')}
            icon={<Truck className="w-4 h-4" />} title={t('Deliver here')} detail={nodeOk ? quote.node.location_label : t('Not offered for this spot')} />
          <ModeOption active={form.mode === 'SELF_PICKUP'} onClick={() => set('mode', 'SELF_PICKUP')}
            icon={<HandCoins className="w-4 h-4" />} title={t('I\'ll pick it up')} detail={t('Collect at {m}', { m: m.provider_name })} />
        </div>
        <div className="mt-4 space-y-3">
          {form.mode === 'DELIVERY_TO_QR_NODE' && (
            <Field label={t('Room / unit (optional)')} htmlFor="unit" hint={t('So the courier finds you, e.g. Kamar 12')}>
              <input id="unit" className={inputClass(false)} value={form.unit} maxLength={100} onChange={e => set('unit', e.target.value)} placeholder="Kamar 12" />
            </Field>
          )}
          <Field label={t('Your name')} htmlFor="name" error={errors.name}>
            <input id="name" autoComplete="name" className={inputClass(errors.name)} value={form.name} maxLength={100} onChange={e => set('name', e.target.value)} />
          </Field>
          <Field label={t('WhatsApp / phone')} htmlFor="phone" error={errors.phone}>
            <input id="phone" type="tel" autoComplete="tel" inputMode="tel" className={inputClass(errors.phone)} value={form.phone} onChange={e => set('phone', e.target.value)} placeholder="0812 3456 7890" />
          </Field>
        </div>
      </Card>

      {failure && (
        <Alert tone="error" title={t('Checkout failed')}>
          <p>{failure.message}</p>
          {['QUOTE_UNAVAILABLE', 'INVALID_GUEST_TOKEN', 'ORDER_NOT_FOUND'].includes(failure.code) && (
            <Button size="sm" variant="secondary" onClick={onExpired}>{t('Start over')}</Button>
          )}
        </Alert>
      )}

      <Button type="submit" size="lg" className="w-full" busy={busy}>
        {busy ? t('Placing your order…') : t('Place order · {total}', { total: rupiah(quote.pricing.grand_total) })}
      </Button>
    </form>
  );
}

function ModeOption({ active, disabled, onClick, icon, title, detail }) {
  return (
    <button type="button" disabled={disabled} onClick={onClick}
      className={cx('rounded-xl border p-3 text-left transition-colors disabled:opacity-40',
        active ? 'border-brand/60 bg-brand/10' : 'border-white/10 bg-white/[0.03] hover:bg-white/[0.06]')}>
      <p className={cx('flex items-center gap-2 text-sm font-semibold', active ? 'text-brand' : 'text-white')}>{icon}{title}</p>
      <p className="mt-0.5 line-clamp-2 text-xs text-slate-400">{detail}</p>
    </button>
  );
}

/* ─────────────────────────── place order ─────────────────────────────────── */

// PlaceOrder: an order whose checkout is done but not yet placed (e.g. the
// page was reloaded in between). Placing sends Beckn confirm.
function PlaceOrder({ amount, onPlace, onExpired }) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const place = async () => {
    setBusy(true);
    setError(null);
    try { await onPlace(amount); } catch (e) { setError(e); } finally { setBusy(false); }
  };
  return (
    <Card>
      <div className="flex flex-col items-center gap-3 text-center">
        <p className="text-xs font-bold tracking-[0.2em] text-slate-400 uppercase">{t('Ready to order')}</p>
        <p className="text-3xl font-bold text-white">{rupiah(amount)}</p>
        <p className="text-sm text-slate-400">{t('Your details are with the merchant. Place the order to confirm it.')}</p>
        {error && <Alert tone="error" title={t('Couldn\'t place the order')}><p>{error.message}</p></Alert>}
        <Button size="lg" className="w-full" busy={busy} onClick={place}>{t('Place order')}</Button>
        <Button variant="ghost" className="w-full" onClick={onExpired}>{t('Cancel and start over')}</Button>
      </div>
    </Card>
  );
}

/* ─────────────────────────── order tracking ──────────────────────────────── */

const FULFILMENT_LABELS = {
  PENDING: 'Waiting for the merchant', PACKED: 'Packed', DISPATCHED: 'On the way', SHIPPED: 'On the way',
  OUT_FOR_DELIVERY: 'Out for delivery', IN_TRANSIT: 'On the way', DELIVERED: 'Delivered', COMPLETED: 'Delivered',
};

function OrderTracking({ transactionId, guestToken, onNew }) {
  const { t } = useI18n();
  const [order, setOrder] = useState(null);
  const [error, setError] = useState(null);

  const load = useCallback(() => qrApi.order(transactionId, guestToken).then(o => { setOrder(o); setError(null); }).catch(setError), [transactionId, guestToken]);
  useEffect(() => { load(); }, [load]);
  useEffect(() => {
    const id = setInterval(() => { if (!document.hidden) load(); }, 5000);
    return () => clearInterval(id);
  }, [load]);

  if (!order) return error ? <Alert tone="error">{error.message}</Alert> : <Card><div className="flex justify-center py-8"><Spinner /></div></Card>;

  const confirmed = order.state === 'CONFIRMED' || ['CONFIRMED', 'COMPLETE', 'STATUS_SENT', 'UPDATE_SENT', 'CANCEL_SENT', 'RATE_SENT', 'SUPPORT_SENT'].includes(order.transaction_status);
  const cancelled = order.transaction_status === 'CANCELLED';
  const delivered = ['DELIVERED', 'COMPLETED'].includes(order.fulfillment_status) || order.transaction_status === 'COMPLETE';
  const steps = [
    { label: t('Order placed'), done: true },
    { label: t('Confirmed by {m}', { m: order.provider_name }), done: confirmed },
    { label: order.fulfillment_status && !['PENDING'].includes(order.fulfillment_status) ? t(FULFILMENT_LABELS[order.fulfillment_status] || order.fulfillment_status) : t('Being prepared'), done: confirmed && !!order.fulfillment_status && order.fulfillment_status !== 'PENDING' },
    { label: t('Delivered'), done: delivered },
  ];

  return (
    <div className="space-y-4">
      <Card>
        <div className="flex flex-col items-center gap-2 text-center">
          {order.state === 'FAILED' ? <AlertCircle className="w-12 h-12 text-rose-400" /> : cancelled ? <AlertCircle className="w-12 h-12 text-amber-300" /> : <PackageCheck className="w-12 h-12 text-emerald-400" />}
          <p className="text-xl font-bold text-white">
            {order.state === 'FAILED' ? t('We couldn\'t place your order') : cancelled ? t('Order cancelled') : confirmed ? t('Order confirmed!') : t('Order placed')}
          </p>
          <p className="text-sm text-slate-400">
            {order.state === 'FAILED' ? t('The merchant didn\'t accept it. Please try again.') : confirmed ? t('{m} is preparing your basket.', { m: order.provider_name }) : t('Sending your order to {m}…', { m: order.provider_name })}
          </p>
        </div>
        {order.state !== 'FAILED' && !cancelled && (
          <ol className="mt-5 space-y-3">
            {steps.map((s, i) => (
              <li key={i} className="flex items-center gap-3 text-sm">
                <span className={cx('grid h-6 w-6 place-items-center rounded-full border', s.done ? 'border-emerald-400 bg-emerald-500/20 text-emerald-300' : 'border-white/15 text-slate-600')}>
                  {s.done ? <CheckCircle2 className="w-4 h-4" /> : i + 1}
                </span>
                <span className={s.done ? 'text-slate-100' : 'text-slate-500'}>{s.label}</span>
              </li>
            ))}
          </ol>
        )}
        {order.last_error && <p className="mt-3 rounded-lg bg-rose-500/10 px-3 py-2 text-xs text-rose-200">{order.last_error}</p>}
      </Card>

      <Card title={t('Order details')}>
        <ul className="space-y-1.5 text-sm">
          {order.lines.map(l => (
            <li key={l.packet_item_id + l.name} className="flex justify-between text-slate-300"><span>{l.name} ×{l.quantity}</span><span>{rupiah(l.subtotal)}</span></li>
          ))}
        </ul>
        <div className="mt-3 flex justify-between border-t border-white/10 pt-2 font-bold text-white"><span>{t('Total')}</span><span>{rupiah(order.total)}</span></div>
        <dl className="mt-4 space-y-1 text-xs text-slate-400">
          <div className="flex items-center gap-2">{order.fulfillment_mode === 'SELF_PICKUP' ? <HandCoins className="w-3.5 h-3.5" /> : <Truck className="w-3.5 h-3.5" />}
            {order.fulfillment_mode === 'SELF_PICKUP' ? t('Self pickup at {m}', { m: order.provider_name }) : `${order.unit_label ? order.unit_label + ', ' : ''}${order.node.location_label}`}</div>
          <div className="flex items-center gap-2"><Receipt className="w-3.5 h-3.5" /> <span className="font-mono">{order.transaction_id}</span></div>
        </dl>
      </Card>

      {['CONFIRMED', 'CONFIRM_SENT'].includes(order.state) && (
        <OrderActions order={order} transactionId={transactionId} guestToken={guestToken} onUpdate={setOrder} delivered={delivered} cancelled={cancelled} />
      )}

      <div className="flex flex-col gap-2">
        {getToken() && <a href={basePath}><Button variant="secondary" className="w-full">{t('See it in my orders')}</Button></a>}
        <Button variant="ghost" className="w-full" onClick={onNew}>{t('Order this basket again')}</Button>
      </div>
    </div>
  );
}

/* ─────────────────────────── after-order actions (Beckn) ─────────────────── */

// Reasons a buyer may give for cancelling (ION cancellation reason codes).
const CANCEL_REASONS = [
  ['CHANGE_OF_MIND', 'Change of mind'],
  ['ITEM_NO_LONGER_NEEDED', 'Item no longer needed'],
  ['DELIVERY_TOO_LONG', 'Delivery takes too long'],
  ['INCORRECT_ADDRESS', 'Incorrect address'],
  ['DUPLICATE_ORDER', 'Duplicate order'],
  ['WRONG_ITEM_ORDERED', 'Wrong item ordered'],
  ['FOUND_BETTER_PRICE', 'Found a better price'],
  ['SELLER_UNRESPONSIVE', 'Seller unresponsive'],
];

// The BAP sends each of these to the merchant as a Beckn message (status,
// cancel, rate, support) through its ONIX adapter; the merchant's on_* answer
// updates the order, which OrderTracking keeps polling.
function OrderActions({ order, transactionId, guestToken, onUpdate, delivered, cancelled }) {
  const { t } = useI18n();
  const [panel, setPanel] = useState(null); // cancel | rate | support
  const [busy, setBusy] = useState(null);
  const [note, setNote] = useState(null);
  const [reason, setReason] = useState(CANCEL_REASONS[0][0]);
  const [stars, setStars] = useState(0);
  const [feedback, setFeedback] = useState('');
  const [support, setSupport] = useState('');
  const demo = Boolean(order.demo_scenario);

  const run = async (key, fn, done) => {
    setBusy(key);
    setNote(null);
    try {
      onUpdate(await fn());
      setNote({ tone: 'success', text: done });
      setPanel(null);
    } catch (e) {
      setNote({ tone: 'error', text: e.message });
    } finally {
      setBusy(null);
    }
  };

  const sent = demo ? t('Done (demo order: nothing is sent to a merchant).') : null;
  const pending = { CANCEL_SENT: t('Cancellation requested; waiting for the merchant.'), RATE_SENT: t('Rating sent to the merchant.'), SUPPORT_SENT: t('Support request sent to the merchant.'), STATUS_SENT: t('Asked the merchant for the latest status.') }[order.transaction_status];

  if (cancelled) return null;
  return (
    <Card title={t('Your order with {m}', { m: order.provider_name })}>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="secondary" busy={busy === 'status'}
          onClick={() => run('status', () => qrApi.refreshStatus(transactionId, guestToken), sent || t('Asked the merchant for the latest status.'))}>
          <RefreshCw className="w-3.5 h-3.5" /> {t('Check status')}
        </Button>
        {!delivered && <Button size="sm" variant="secondary" onClick={() => setPanel(panel === 'cancel' ? null : 'cancel')}><XCircle className="w-3.5 h-3.5" /> {t('Cancel order')}</Button>}
        {delivered && <Button size="sm" variant="secondary" onClick={() => setPanel(panel === 'rate' ? null : 'rate')}><Star className="w-3.5 h-3.5" /> {t('Rate')}</Button>}
        <Button size="sm" variant="secondary" onClick={() => setPanel(panel === 'support' ? null : 'support')}><LifeBuoy className="w-3.5 h-3.5" /> {t('Get help')}</Button>
      </div>

      {panel === 'cancel' && (
        <div className="mt-4 space-y-3 rounded-xl border border-white/10 p-3">
          <Field label={t('Why are you cancelling?')} htmlFor="cancel-reason">
            <select id="cancel-reason" className={inputClass(false)} value={reason} onChange={e => setReason(e.target.value)}>
              {CANCEL_REASONS.map(([code, label]) => <option key={code} value={code} className="bg-slate-900">{t(label)}</option>)}
            </select>
          </Field>
          <p className="text-xs text-slate-500">{t('The merchant\'s cancellation policy decides; a refund follows if it accepts.')}</p>
          <Button size="sm" variant="danger" busy={busy === 'cancel'}
            onClick={() => run('cancel', () => qrApi.cancel(transactionId, reason, guestToken), sent || t('Cancellation sent to the merchant.'))}>
            {t('Cancel order')}
          </Button>
        </div>
      )}

      {panel === 'rate' && (
        <div className="mt-4 space-y-3 rounded-xl border border-white/10 p-3">
          <div className="flex gap-1" role="radiogroup" aria-label={t('Rating')}>
            {[1, 2, 3, 4, 5].map(n => (
              <button key={n} type="button" role="radio" aria-checked={stars === n} onClick={() => setStars(n)} className="p-1" aria-label={`${n}`}>
                <Star className={cx('w-6 h-6', n <= stars ? 'fill-amber-400 text-amber-400' : 'text-slate-600')} />
              </button>
            ))}
          </div>
          <textarea rows={2} className={inputClass(false)} value={feedback} maxLength={300} onChange={e => setFeedback(e.target.value)} placeholder={t('Anything to add? (optional)')} />
          <Button size="sm" disabled={!stars} busy={busy === 'rate'}
            onClick={() => run('rate', () => qrApi.rate(transactionId, stars, feedback, guestToken), sent || t('Thanks! Your rating was sent to the merchant.'))}>
            {t('Send rating')}
          </Button>
        </div>
      )}

      {panel === 'support' && (
        <div className="mt-4 space-y-3 rounded-xl border border-white/10 p-3">
          <textarea rows={3} className={inputClass(false)} value={support} maxLength={500} onChange={e => setSupport(e.target.value)} placeholder={t('What went wrong?')} />
          <Button size="sm" disabled={!support.trim()} busy={busy === 'support'}
            onClick={() => run('support', () => qrApi.support(transactionId, support.trim(), guestToken), sent || t('Sent. The merchant will get back to you.'))}>
            {t('Send to the merchant')}
          </Button>
        </div>
      )}

      {note && <div className="mt-3"><Alert tone={note.tone}>{note.text}</Alert></div>}
      {!note && pending && !demo && <p className="mt-3 text-xs text-slate-400">{pending}</p>}
    </Card>
  );
}
