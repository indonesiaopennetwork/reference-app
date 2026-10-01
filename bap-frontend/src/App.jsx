import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import {
  ShoppingCart, Search, X, Package, CheckCircle2, Loader2,
  ArrowRight, ChevronRight, Minus, Plus, Sparkles, Tag,
  RotateCcw, CreditCard, ShieldCheck, Clock,
  Star, Info, Receipt, Store, MapPin, Calendar, Zap,
  User, Phone, Home, ChevronLeft, Banknote,
  Ban, MessageSquare, RefreshCw, ThumbsUp,
  History, Wand2, ExternalLink, ChevronDown, ChevronUp, LogOut,
  Compass, AlertCircle,
} from 'lucide-react';
import Tour from './Tour.jsx';
import { useI18n, rich, LanguageToggle, tNow, localeNow } from './i18n.jsx';
import { captureTokenFromUrl, getToken, getProviderId, clearToken, redirectToLogin, logout, basePath } from './auth.js';

// BPP target, network ID, and BAP identity are all server-side config (BAP .env).
// The frontend only needs the API base path.
const API_BASE = `${basePath}api/v1`;

// Attaches the buyer's bearer token (if any) to every API call. Auth is a
// frontend-only gate here (see App()'s authReady check) -- the backend
// doesn't hard-reject a missing/invalid token, so a 401 should never
// happen in normal use, but we still recover the same way bpp-frontend
// does if it ever does.
async function apiFetch(path, opts = {}) {
  const token = getToken();
  const res = await fetch(`${API_BASE}${path}`, {
    ...opts,
    headers: {
      ...(opts.headers || {}),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
  });
  if (res.status === 401) {
    clearToken();
    redirectToLogin();
  }
  return res;
}

/* ─────────────────────────── helpers ────────────────────────────────────── */

function parseCatalogs(data) {
  // Use a Map keyed by "bppId::resourceId" so that duplicate resources from
  // overlapping discover results (re-indexing, multiple catalogs from the same
  // BPP) are deduplicated. Last-write wins, which is fine — they're identical.
  const seen = new Map();

  (data?.message?.catalogs || []).forEach(cat => {
    (cat.resources || []).forEach(res => {
      if (!res?.id) return; // skip malformed entries

      // Find the offer that covers this resource. A resource can have more than
      // one matching offer — e.g. bpp-frontend's "Add Product" step creates an
      // empty placeholder offer (no price) alongside the resource, then a real
      // priced offer gets added afterward via "Add Offer". Prefer whichever
      // matching offer actually carries price data over the first one found.
      const offerHasPrice = o =>
        (o.considerations?.[0]?.considerationAttributes?.totalAmount != null) ||
        (o.offerAttributes?.price?.value != null);
      const matchingOffers = (cat.offers || []).filter(o => o.resourceIds?.includes(res.id));
      const offer = matchingOffers.find(offerHasPrice) || matchingOffers[0] || null;

      // Resources with no offer have no price — skip them to avoid blank cards.
      if (!offer) return;

      const oa = offer.offerAttributes || {};
      const pa = oa.price || {};
      const cons = offer.considerations?.[0]?.considerationAttributes || {};

      // Price: prefer considerations.totalAmount, fallback to offerAttributes.price.value
      let price = cons.totalAmount ?? pa.value ?? 0;
      let currency = cons.currency || pa.currency || 'IDR';

      // Price breakup: prefer considerations.breakup, build from price.components if needed
      let breakup = cons.breakup || [];
      if (!breakup.length && pa.value) {
        breakup = [{ title: res.descriptor?.name || tNow('Item price'), amount: pa.value, type: 'BASE_PRICE' }];
        if (pa.components?.length) {
          pa.components.forEach(c => {
            if (c.type === 'DISCOUNT') {
              breakup.push({ title: tNow('Discount ({pct}%)', { pct: c.value }), amount: -(pa.value * c.value / 100), type: 'DISCOUNT' });
            }
          });
          const discountAmt = pa.components.filter(c => c.type === 'DISCOUNT').reduce((s, c) => s + (pa.value * c.value / 100), 0);
          price = pa.value - discountAmt;
        }
      }

      // Seller / provider
      const provider = offer.provider || cat.provider || res.provider || {};
      const sellerName = provider.descriptor?.name || '';
      const sellerAddress = provider.availableAt?.[0]?.address;

      // Policies
      const policies = oa.policies || {};
      const serviceability = oa.serviceability || {};
      const timing = serviceability.timing?.[0] || {};
      const distance = serviceability.distanceConstraint || {};

      // Composite dedup key: bpp + resource so cross-BPP resources with
      // the same resource ID (unlikely but possible) are treated as distinct.
      const bppId = cat.bppId || '';
      const bppUri = cat.bppUri || '';
      const dedupKey = `${bppId}::${res.id}`;

      seen.set(dedupKey, {
        id: res.id,
        name: res.descriptor?.name || tNow('Unknown Product'),
        shortDesc: res.descriptor?.shortDesc || '',
        longDesc: res.descriptor?.longDesc || '',
        productHighlights: res.resourceAttributes?.productHighlights || '',
        specifications: res.resourceAttributes?.specifications || '',
        category: cat.descriptor?.name || tNow('Retail'),
        price,
        currency,
        image: res.descriptor?.mediaFile?.[0]?.uri ||
          'https://images.unsplash.com/photo-1542838132-92c53300491e?w=600&auto=format&fit=crop&q=70',
        offerId: offer.id || '',
        offerName: offer.descriptor?.name || offer.descriptor?.shortDesc || '',
        providerId: provider.id || '',
        sellerName,
        bppId,
        bppUri,
        offerAttr: oa,
        offerConsiderations: offer.considerations || [],
        // The resource as discover returned it, sent on in select.
        resourceDetail: { descriptor: res.descriptor, resourceAttributes: res.resourceAttributes },
        breakup,
        sellerAddress,
        returnPolicy: policies.returns,
        cancelPolicy: policies.cancellation,
        codAvailable: oa.paymentConstraints?.codAvailable,
        deliveryRange: distance.maxDistance ? `${distance.maxDistance} ${distance.unit || 'KM'}` : '',
        deliveryDays: timing.daysOfWeek || [],
        deliveryHours: timing.timeRange ? `${timing.timeRange.start}–${timing.timeRange.end}` : '',
        // ISO date; shown with fmtDay() in the current language.
        offerValidity: offer.validity?.endDate || '',
      });
    });
  });

  return Array.from(seen.values());
}

// Parse the on_select contract from status polling to get the BPP-confirmed quote
// The seller prices each item with its own tax line ("PPN 12%" per product).
// Show tax once: lines with the same title are added up and listed after the
// items. Different rates (PPN 11% / 12%) stay separate lines.
function combineTax(breakup) {
  const items = [], taxes = new Map();
  for (const b of breakup || []) {
    if (b.type !== 'TAX') { items.push(b); continue; }
    const t = taxes.get(b.title);
    if (t) t.amount = Math.round((t.amount + Number(b.amount || 0)) * 100) / 100;
    else taxes.set(b.title, { ...b, amount: Number(b.amount || 0) });
  }
  return [...items, ...taxes.values()];
}

function parseOnSelectContract(contract) {
  if (!contract) return null;
  const consideration = contract.consideration?.[0] ?? contract.considerations?.[0];
  const commitment = contract.commitments?.[0];
  const ca = consideration?.considerationAttributes || {};
  const pol = commitment?.offer?.offerAttributes?.policies || {};
  return {
    total: ca.totalAmount,
    currency: ca.currency || 'IDR',
    breakup: combineTax(ca.breakup),
    paymentMethods: ca.paymentMethods || [],
    codAvailable: commitment?.offer?.offerAttributes?.paymentConstraints?.codAvailable,
    seller: commitment?.offer?.provider?.descriptor?.name,
    offerName: commitment?.offer?.descriptor?.name,
    returnPolicy: pol.returns,
    cancelPolicy: pol.cancellation,
    // The seller marks an item it no longer has as CLOSED and leaves it out
    // of the price; with nothing priced there is no consideration at all.
    unavailable: (contract.commitments || [])
      .filter(c => (c.status?.descriptor?.code || c.status?.code) === 'CLOSED')
      .map(c => ({
        ids: (c.resources || []).map(r => r.id),
        name: c.resources?.[0]?.descriptor?.name || c.offer?.descriptor?.name || c.resources?.[0]?.id || c.offer?.id,
      })),
    unpriced: !consideration,
  };
}

// An order can't go ahead when any item is unavailable or nothing is priced.
const quoteBlocked = q => !!q && (q.unavailable.length > 0 || q.unpriced);

/* ─────────────────────────── UnavailablePanel ───────────────────────────── */
// Shown instead of the quote when the seller can't supply the order. The
// seller doesn't name an item it no longer has, so nameOf (from the cart or
// the product shown) supplies the name the buyer knows it by.
function UnavailablePanel({ quote, onRemove, nameOf = () => null }) {
  const { t } = useI18n();
  const names = quote.unavailable.map(u => u.ids.map(nameOf).find(Boolean) || u.name).filter(Boolean);
  return (
    <motion.div initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }}
      className="rounded-xl border border-rose-500/25 bg-rose-500/5 px-4 py-3 space-y-2" data-tour="unavailable">
      <p className="flex items-center gap-2 text-sm font-semibold text-rose-300">
        <AlertCircle className="w-4 h-4 shrink-0" /> {t('No longer available')}
      </p>
      <p className="text-xs text-slate-300">
        {names.length > 0
          ? t('The seller no longer has: {items}.', { items: names.join(', ') })
          : t('The seller could not price this order.')}
      </p>
      <p className="text-xs text-slate-500">{t('Remove it from your cart and try again, or choose another product.')}</p>
      {onRemove && (
        <button onClick={() => onRemove(quote.unavailable.flatMap(u => u.ids))}
          className="w-full mt-1 py-2.5 rounded-xl text-xs font-semibold bg-rose-500/15 text-rose-200 border border-rose-500/30 hover:bg-rose-500/25 transition-all">
          {t('Remove unavailable items')}
        </button>
      )}
    </motion.div>
  );
}

const fmt = (amount, currency = 'IDR') => {
  if (!amount && amount !== 0) return '—';
  const sym = currency === 'IDR' ? 'Rp' : '₹';
  return `${sym}${Number(amount).toLocaleString(localeNow())}`;
};

// An offer's end date ("12 Oct 2026" / "12 Okt 2026").
const fmtDay = iso => (iso ? new Date(iso).toLocaleDateString(localeNow(), { day: 'numeric', month: 'short', year: 'numeric' }) : '');

// "1 item" / "3 items".
const itemCount = (t, n) => (n === 1 ? t('1 item') : t('{count} items', { count: n }));

// "P7D" → "7" for day-based ISO-8601 windows; anything else as given.
const isoDays = w => (w || '').replace(/^P(\d+)D$/, '$1');

const FALLBACK_IMG = 'https://images.unsplash.com/photo-1542838132-92c53300491e?w=600&auto=format&fit=crop&q=70';

/* ─────────────────────────── SkeletonCard ───────────────────────────────── */
function SkeletonCard() {
  return (
    <div className="glass rounded-2xl overflow-hidden">
      <div className="skeleton h-44 w-full" />
      <div className="p-4 space-y-2.5">
        <div className="skeleton h-3 w-1/3" />
        <div className="skeleton h-4 w-4/5" />
        <div className="skeleton h-3 w-2/3" />
        <div className="flex justify-between items-center pt-2">
          <div className="skeleton h-6 w-16" />
          <div className="skeleton h-9 w-20 rounded-xl" />
        </div>
      </div>
    </div>
  );
}

/* ─────────────────────────── OfferInfoPanel ─────────────────────────────── */
// Shows catalog offer details: price breakdown, seller, policies, serviceability
function OfferInfoPanel({ product }) {
  const { breakup, currency, price, sellerName, sellerAddress,
    returnPolicy, cancelPolicy, codAvailable,
    deliveryRange, deliveryHours, deliveryDays, offerName, offerValidity } = product;
  const { t } = useI18n();

  const shortAddr = sellerAddress
    ? [sellerAddress.streetAddress, sellerAddress.addressLocality, sellerAddress.addressRegion].filter(Boolean).join(', ')
    : '';

  return (
    <div className="space-y-3">
      {/* Price section */}
      <div className="rounded-xl border border-white/10 bg-white/[0.03] overflow-hidden">
        <div className="px-4 py-2.5 bg-white/[0.04] border-b border-white/[0.07] flex items-center gap-2">
          <Receipt className="w-3.5 h-3.5 text-emerald-400" />
          <span className="text-xs font-semibold text-slate-300 uppercase tracking-wider">
            {offerName || t('Pricing')}
          </span>
          {offerValidity && (
            <span className="ml-auto text-[10px] text-slate-500 flex items-center gap-1">
              <Calendar className="w-3 h-3" /> {t('Valid till {date}', { date: fmtDay(offerValidity) })}
            </span>
          )}
        </div>
        <div className="px-4 py-3 space-y-1.5">
          {breakup.length > 0 ? (
            <>
              {breakup.map((b, i) => (
                <div key={i} className="flex justify-between text-sm">
                  <span className="text-slate-400">{b.title}</span>
                  <span className={
                    b.type === 'DISCOUNT' ? 'text-emerald-400 font-medium' :
                      b.type === 'TAX' ? 'text-amber-400' : 'text-slate-200'
                  }>
                    {b.type === 'DISCOUNT' && b.amount < 0 ? '−' : ''}{fmt(Math.abs(b.amount), currency)}
                  </span>
                </div>
              ))}
              <div className="border-t border-white/10 pt-2 mt-1 flex justify-between items-center">
                <span className="text-sm font-semibold text-slate-300">{t('Total')}</span>
                <span className="text-xl font-bold text-emerald-300">{fmt(price, currency)}</span>
              </div>
            </>
          ) : (
            <div className="flex justify-between items-center">
              <span className="text-sm text-slate-400">{t('Price')}</span>
              <span className="text-xl font-bold text-emerald-300">{fmt(price, currency)}</span>
            </div>
          )}
        </div>
      </div>

      {/* Seller */}
      {sellerName && (
        <div className="flex items-start gap-3 px-4 py-3 rounded-xl border border-white/10 bg-white/[0.03]">
          <Store className="w-4 h-4 text-slate-400 mt-0.5 shrink-0" />
          <div>
            <p className="text-sm font-medium text-slate-200">{sellerName}</p>
            {shortAddr && <p className="text-xs text-slate-500 mt-0.5">{shortAddr}</p>}
          </div>
        </div>
      )}

      {/* Delivery & badges */}
      <div className="flex flex-wrap gap-2">
        {deliveryRange && (
          <span className="flex items-center gap-1 text-[10px] font-semibold bg-blue-500/10 text-blue-400 border border-blue-500/20 px-2.5 py-1 rounded-full">
            <MapPin className="w-3 h-3" /> {t('Delivers within {range}', { range: deliveryRange })}
          </span>
        )}
        {deliveryHours && (
          <span className="flex items-center gap-1 text-[10px] font-semibold bg-white/5 text-slate-400 border border-white/10 px-2.5 py-1 rounded-full">
            <Clock className="w-3 h-3" /> {deliveryHours}
          </span>
        )}
        {codAvailable && (
          <span className="flex items-center gap-1 text-[10px] font-semibold bg-amber-500/10 text-amber-400 border border-amber-500/20 px-2.5 py-1 rounded-full">
            <CreditCard className="w-3 h-3" /> {t('Cash on Delivery')}
          </span>
        )}
        {returnPolicy?.allowed && (
          <span className="flex items-center gap-1 text-[10px] font-semibold bg-purple-500/10 text-purple-400 border border-purple-500/20 px-2.5 py-1 rounded-full">
            <RotateCcw className="w-3 h-3" /> {t('{days}-day returns', { days: isoDays(returnPolicy.window) })}
          </span>
        )}
        {cancelPolicy?.allowed && (
          <span className="flex items-center gap-1 text-[10px] font-semibold bg-rose-500/10 text-rose-400 border border-rose-500/20 px-2.5 py-1 rounded-full">
            <X className="w-3 h-3" /> {t('Free cancellation')}
          </span>
        )}
      </div>
    </div>
  );
}

/* ─────────────────────────── ConfirmedQuotePanel ────────────────────────── */
// Shows the BPP-confirmed quote from on_select (may differ from catalog price)
function ConfirmedQuotePanel({ quote, onInit, isLoading, nameOf }) {
  const { t } = useI18n();
  if (!quote) return null;
  if (quoteBlocked(quote)) return <UnavailablePanel quote={quote} nameOf={nameOf} />;
  const { total, currency, breakup, paymentMethods, codAvailable,
    seller, returnPolicy, cancelPolicy } = quote;
  return (
    <motion.div
      initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }}
      className="rounded-xl border border-emerald-500/25 bg-emerald-500/5 overflow-hidden"
    >
      <div className="flex items-center gap-2 px-4 py-2.5 bg-emerald-500/10 border-b border-emerald-500/15">
        <CheckCircle2 className="w-3.5 h-3.5 text-emerald-400" />
        <span className="text-xs font-semibold text-emerald-300 uppercase tracking-wider">{t('Confirmed Quote')}</span>
        {seller && <span className="ml-auto text-[10px] text-slate-400 flex items-center gap-1"><Store className="w-3 h-3" />{seller}</span>}
      </div>
      <div className="px-4 py-3 space-y-2">
        {breakup.length > 0 && (
          <div className="space-y-1.5">
            {breakup.map((b, i) => (
              <div key={i} className="flex justify-between text-sm">
                <span className="text-slate-400">{b.title}</span>
                <span className={b.type === 'TAX' ? 'text-amber-400' : b.type === 'DISCOUNT' ? 'text-emerald-400' : 'text-slate-200'}>
                  {fmt(b.amount, currency)}
                </span>
              </div>
            ))}
            <div className="border-t border-white/10 pt-2 flex justify-between font-bold">
              <span className="text-slate-200">{t('Total')}</span>
              <span className="text-emerald-300 text-lg">{fmt(total, currency)}</span>
            </div>
          </div>
        )}
        <div className="flex flex-wrap gap-1.5 pt-1">
          {codAvailable && <span className="text-[10px] bg-amber-500/10 text-amber-400 border border-amber-500/20 px-2 py-0.5 rounded-full">{t('COD')}</span>}
          {paymentMethods?.map(m => <span key={m} className="text-[10px] bg-white/5 text-slate-400 border border-white/10 px-2 py-0.5 rounded-full">{m}</span>)}
          {returnPolicy?.allowed && <span className="text-[10px] bg-purple-500/10 text-purple-400 border border-purple-500/20 px-2 py-0.5 rounded-full">↩ {t('{days}-day returns', { days: isoDays(returnPolicy.window) })}</span>}
        </div>
        <button
          onClick={onInit} disabled={isLoading}
          className="w-full mt-1 bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 text-white py-3 rounded-xl font-bold text-sm flex items-center justify-center gap-2 transition-all active:scale-[0.98] shadow-lg shadow-emerald-600/30"
        >
          {isLoading ? <Loader2 className="w-4 h-4 animate-spin" /> : <><span>{t('Initialize Order')}</span><ArrowRight className="w-4 h-4" /></>}
        </button>
      </div>
    </motion.div>
  );
}

/* ─────────────────────────── ProductModal ───────────────────────────────── */
function ProductModal({ product, transaction, onClose, onSelect, onInit, onConfirm, isLoading }) {
  const { t } = useI18n();
  const isThisProduct = transaction?.product?.id === product.id;
  const status = isThisProduct ? transaction.status : null;

  // Parse confirmed on_select contract once QUOTE_RECEIVED
  const confirmedQuote = (status === 'QUOTE_RECEIVED' && transaction?.contract)
    ? parseOnSelectContract(
      typeof transaction.contract === 'string'
        ? JSON.parse(transaction.contract)
        : transaction.contract
    )
    : null;

  const pendingStatuses = ['SELECT_SENT', 'INIT_SENT', 'CONFIRM_SENT'];
  const isPending = pendingStatuses.includes(status);

  const pendingLabel = {
    SELECT_SENT: t('Requesting a quote from the seller…'),
    INIT_SENT: t('Initializing order…'),
    CONFIRM_SENT: t('Confirming your order…'),
  }[status];

  return (
    <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center p-3 sm:p-4">
      <motion.div
        initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
        onClick={onClose}
      />
      <motion.div
        initial={{ opacity: 0, y: 60, scale: 0.97 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: 40, scale: 0.97 }}
        transition={{ type: 'spring', damping: 28, stiffness: 300 }}
        className="relative w-full max-w-lg glass rounded-3xl overflow-hidden max-h-[94vh] flex flex-col"
      >
        {/* ── Hero image ─────────────────────────────────────────────── */}
        <div className="relative h-52 shrink-0 bg-slate-800/80 overflow-hidden">
          <img src={product.image} alt={product.name}
            className="w-full h-full object-cover opacity-90"
            onError={e => { e.target.src = FALLBACK_IMG; }} />
          <div className="absolute inset-0 bg-gradient-to-t from-black/80 via-black/10" />
          <button onClick={onClose}
            className="absolute top-3 right-3 w-8 h-8 flex items-center justify-center rounded-full bg-black/50 backdrop-blur text-white hover:bg-black/70 transition-colors z-10">
            <X className="w-4 h-4" />
          </button>
          <div className="absolute bottom-4 left-4 right-4 flex items-end justify-between gap-2">
            <span className="text-[10px] font-bold uppercase tracking-widest bg-black/50 text-emerald-400 border border-emerald-500/20 px-2.5 py-1 rounded-full backdrop-blur">
              <Tag className="inline w-2.5 h-2.5 mr-1 -mt-0.5" />{product.category}
            </span>
          </div>
        </div>

        {/* ── Scrollable body ─────────────────────────────────────────── */}
        <div className="overflow-y-auto flex-grow px-5 py-4 space-y-4">

          {/* Product name & description */}
          <div>
            <h2 className="text-xl leading-snug">{product.name}</h2>
            {product.shortDesc && (
              <p className="text-sm text-slate-400 mt-1.5 leading-relaxed">{product.shortDesc}</p>
            )}
            {product.longDesc && product.longDesc !== product.shortDesc && (
              <p className="text-xs text-slate-500 mt-1 leading-relaxed">{product.longDesc}</p>
            )}
            {product.productHighlights && (
              <div className="mt-4">
                <h3 className="text-sm font-bold text-slate-200 mb-1.5">{t('Product Highlights')}</h3>
                <div className="text-xs text-slate-400 leading-relaxed whitespace-pre-wrap">{product.productHighlights}</div>
              </div>
            )}
            {product.specifications && (
              <div className="mt-4">
                <h3 className="text-sm font-bold text-slate-200 mb-1.5">{t('Specifications')}</h3>
                <div className="text-xs text-slate-400 leading-relaxed whitespace-pre-wrap">{product.specifications}</div>
              </div>
            )}
          </div>

          {/* ── Offer info from catalog (always shown) ───────────────── */}
          <OfferInfoPanel product={product} />

          {/* ── Action area ─────────────────────────────────────────── */}

          {/* No transaction yet — show Select button */}
          {!status && (
            <button onClick={() => onSelect(product)} disabled={isLoading}
              className="w-full flex items-center justify-center gap-2 bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 text-white py-3.5 rounded-2xl font-bold text-sm transition-all active:scale-[0.98] shadow-lg shadow-emerald-600/30">
              {isLoading
                ? <><Loader2 className="w-4 h-4 animate-spin" /> {t('Getting a quote…')}</>
                : <><Zap className="w-4 h-4" /> {t('Get a Quote')}</>}
            </button>
          )}

          {/* Waiting for async response */}
          {isPending && (
            <div className="flex items-center gap-3 px-4 py-3 rounded-xl bg-white/[0.04] border border-white/10">
              <Loader2 className="w-5 h-5 text-emerald-400 animate-spin shrink-0" />
              <div>
                <p className="text-sm text-slate-300">{pendingLabel}</p>
                <p className="text-xs text-slate-500 mt-0.5">{t('Txn {id}…', { id: transaction.id.slice(0, 8) })}</p>
              </div>
            </div>
          )}

          {/* on_select confirmed quote */}
          {status === 'QUOTE_RECEIVED' && (
            <ConfirmedQuotePanel quote={confirmedQuote} onInit={onInit} isLoading={isLoading}
              nameOf={id => (id === product.id ? product.name : null)} />
          )}

          {/* Init received — confirm */}
          {status === 'INIT_RECEIVED' && (
            <div className="space-y-3">
              <div className="flex items-center gap-2 text-sm text-slate-300 bg-emerald-500/5 border border-emerald-500/20 rounded-xl px-4 py-3">
                <Info className="w-4 h-4 text-emerald-400 shrink-0" />
                {t('Order initialized. Click below to place and pay.')}
              </div>
              <button onClick={onConfirm} disabled={isLoading}
                className="w-full flex items-center justify-center gap-2 bg-amber-500 hover:bg-amber-400 disabled:opacity-50 text-white py-3.5 rounded-2xl font-bold text-sm transition-all active:scale-[0.98] shadow-lg shadow-amber-500/30">
                {isLoading ? <Loader2 className="w-4 h-4 animate-spin" /> : <><ShieldCheck className="w-4 h-4" />{t('Pay & Confirm Order')}</>}
              </button>
            </div>
          )}

          {/* Confirmed */}
          {status === 'CONFIRMED' && (
            <motion.div initial={{ scale: 0.9, opacity: 0 }} animate={{ scale: 1, opacity: 1 }}
              className="flex flex-col items-center gap-2 py-4">
              <div className="w-16 h-16 rounded-full bg-emerald-500/15 border border-emerald-500/30 flex items-center justify-center">
                <CheckCircle2 className="w-8 h-8 text-emerald-400" />
              </div>
              <p className="font-bold text-emerald-300 text-lg">{t('Order Confirmed!')}</p>
              <p className="text-sm text-slate-400 text-center">{t('Your order has been placed on the ION network.')}</p>
              <div className="w-full mt-2 text-left"><LiveOrderItems txnId={transaction.id} /></div>
              <button onClick={onClose} className="mt-1 text-sm text-slate-400 hover:text-white transition-colors underline underline-offset-4">
                {t('Continue Shopping')}
              </button>
            </motion.div>
          )}
        </div>
      </motion.div>
    </div>
  );
}

/* ─────────────────────────── CheckoutFlow ──────────────────────────────── */
// Multi-step checkout inside the cart drawer:
//   SELECT_SENT → polling → QUOTE_RECEIVED → billing form → INIT_SENT → polling → INIT_RECEIVED → CONFIRM_SENT → CONFIRMED
function StarRating({ value, onChange }) {
  const [hover, setHover] = useState(0);
  return (
    <div className="flex gap-1">
      {[1, 2, 3, 4, 5].map(n => (
        <button key={n}
          onClick={() => onChange(n)}
          onMouseEnter={() => setHover(n)}
          onMouseLeave={() => setHover(0)}
          className="transition-transform hover:scale-110">
          <Star className={`w-6 h-6 ${n <= (hover || value) ? 'text-amber-400 fill-amber-400' : 'text-slate-600'}`} />
        </button>
      ))}
    </div>
  );
}

function CheckoutFlow({ cart, cartTotal, transaction, billing, setBilling, isLoading, onInit, onConfirm, onDone, onStatusChange, onRate, onSupport, onRequestStatus, onRemoveItems }) {
  const { t } = useI18n();
  const [ratingValue, setRatingValue] = useState(0);
  const [ratingFeedback, setRatingFeedback] = useState('');
  const [showRatingForm, setShowRatingForm] = useState(false);
  const [showSupportForm, setShowSupportForm] = useState(false);
  const [supportDesc, setSupportDesc] = useState('');
  const status = transaction?.status;

  // Parse BPP-confirmed quote from on_select
  const confirmedQuote = (status === 'QUOTE_RECEIVED' && transaction?.contract)
    ? parseOnSelectContract(
      typeof transaction.contract === 'string'
        ? JSON.parse(transaction.contract)
        : transaction.contract
    )
    : null;

  const currency = confirmedQuote?.currency || 'IDR';
  const blocked = quoteBlocked(confirmedQuote);

  /* ── Stepper ───────────────────────────────────────────────────────── */
  const STEPS = [t('Quote'), t('Billing'), t('Confirm'), t('Done')];
  const stepIndex = {
    SELECT_SENT: 0,
    QUOTE_RECEIVED: 1,
    INIT_SENT: 1,
    INIT_RECEIVED: 2,
    CONFIRM_SENT: 2,
    CONFIRMED: 3,
    STATUS_SENT: 3,
    CANCEL_SENT: 3,
    CANCELLED: 3,
    RATE_SENT: 3,
    SUPPORT_SENT: 3,
  }[status] ?? 0;

  return (
    <div className="flex flex-col flex-grow overflow-hidden">

      {/* Stepper */}
      <div className="px-5 pt-4 pb-3 shrink-0" data-tour="checkout-stepper">
        <div className="relative flex items-center justify-between">
          <div className="absolute inset-x-0 top-3 h-px bg-white/10" />
          <div className="absolute left-0 top-3 h-px bg-emerald-400 transition-all duration-700"
            style={{ width: `${(stepIndex / (STEPS.length - 1)) * 100}%` }} />
          {STEPS.map((label, i) => (
            <div key={i} className="relative flex flex-col items-center gap-1.5 z-10">
              <div className={`w-6 h-6 rounded-full flex items-center justify-center text-[10px] font-bold transition-all duration-500 ${i < stepIndex ? 'bg-emerald-400 text-slate-900' :
                i === stepIndex ? 'bg-emerald-400 text-slate-900 ring-4 ring-emerald-400/20' :
                  'bg-slate-700 text-slate-400'}`}>
                {i < stepIndex ? <CheckCircle2 className="w-3.5 h-3.5" /> : i + 1}
              </div>
              <span className={`text-[10px] font-medium whitespace-nowrap ${i <= stepIndex ? 'text-emerald-400' : 'text-slate-500'}`}>{label}</span>
            </div>
          ))}
        </div>
      </div>

      {/* Scrollable content */}
      <div className="flex-grow overflow-y-auto px-5 py-3 space-y-4">

        {/* Cart items summary (always shown, compact) */}
        <div className="rounded-xl border border-white/10 bg-white/[0.03] overflow-hidden">
          <div className="px-4 py-2.5 bg-white/[0.04] border-b border-white/[0.07] flex items-center justify-between">
            <span className="text-xs font-semibold text-slate-400 uppercase tracking-wider">{t('Order Summary')}</span>
            <span className="text-xs text-slate-400">{itemCount(t, cart.reduce((s, i) => s + i.qty, 0))}</span>
          </div>
          <div className="px-4 py-2 space-y-1.5">
            {cart.map(item => (
              <div key={item.id} className="flex items-center gap-2 py-1">
                <div className="w-8 h-8 rounded-md overflow-hidden shrink-0 bg-slate-800">
                  <img src={item.image} alt={item.name} className="w-full h-full object-cover"
                    onError={e => { e.target.src = FALLBACK_IMG; }} />
                </div>
                <span className="flex-grow text-xs text-slate-300 truncate">{item.name}</span>
                <span className="text-xs font-semibold text-slate-200 shrink-0">×{item.qty}</span>
                <span className="text-xs font-bold shrink-0">{fmt(item.price * item.qty, item.currency)}</span>
              </div>
            ))}
            <div className="border-t border-white/10 pt-2 flex justify-between items-center">
              <span className="text-xs text-slate-400">{t('Estimated Total')}</span>
              <span className="text-base font-bold text-emerald-300">{fmt(cartTotal, 'IDR')}</span>
            </div>
          </div>
        </div>

        {/* ── Step: Waiting for on_select ─────────────────────────────── */}
        {status === 'SELECT_SENT' && (
          <div className="flex flex-col items-center gap-3 py-8">
            <div className="relative w-12 h-12">
              <div className="absolute inset-0 rounded-full border-2 border-emerald-500/20 border-t-emerald-400 animate-spin" />
              <Sparkles className="absolute inset-0 m-auto w-5 h-5 text-emerald-400" />
            </div>
            <p className="text-sm text-slate-300">{t('Getting a quote from the network…')}</p>
            <p className="text-xs text-slate-500">{t('Txn {id}…', { id: transaction.id.slice(0, 8) })}</p>
          </div>
        )}

        {/* ── Step: Quote received, but the seller can't supply it ────── */}
        {status === 'QUOTE_RECEIVED' && blocked && (
          <UnavailablePanel quote={confirmedQuote} onRemove={onRemoveItems}
            nameOf={id => cart.find(i => i.id === id)?.name} />
        )}

        {/* ── Step: Quote received → show BPP quote + billing form ────── */}
        {status === 'QUOTE_RECEIVED' && !blocked && (
          <div className="space-y-4">
            {/* BPP confirmed quote */}
            {confirmedQuote && (
              <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }}
                className="rounded-xl border border-emerald-500/20 bg-emerald-500/5 overflow-hidden">
                <div className="px-4 py-2.5 bg-emerald-500/10 border-b border-emerald-500/15 flex items-center gap-2">
                  <CheckCircle2 className="w-3.5 h-3.5 text-emerald-400" />
                  <span className="text-xs font-semibold text-emerald-300 uppercase tracking-wider">{t('Quote Confirmed by Seller')}</span>
                  {confirmedQuote.seller && (
                    <span className="ml-auto text-[10px] text-slate-400 flex items-center gap-1">
                      <Store className="w-3 h-3" />{confirmedQuote.seller}
                    </span>
                  )}
                </div>
                <div className="px-4 py-3 space-y-1.5">
                  {confirmedQuote.breakup.map((b, i) => (
                    <div key={i} className="flex justify-between text-sm">
                      <span className="text-slate-400">{b.title}</span>
                      <span className={b.type === 'DISCOUNT' ? 'text-emerald-400' : b.type === 'TAX' ? 'text-amber-400' : 'text-slate-200'}>
                        {fmt(b.amount, currency)}
                      </span>
                    </div>
                  ))}
                  {confirmedQuote.total && (
                    <div className="border-t border-white/10 pt-2 flex justify-between font-bold">
                      <span className="text-slate-200">{t('Total')}</span>
                      <span className="text-emerald-300 text-lg">{fmt(confirmedQuote.total, currency)}</span>
                    </div>
                  )}
                  <div className="flex flex-wrap gap-1.5 pt-1">
                    {confirmedQuote.codAvailable && (
                      <span className="text-[10px] bg-amber-500/10 text-amber-400 border border-amber-500/20 px-2 py-0.5 rounded-full flex items-center gap-1">
                        <Banknote className="w-2.5 h-2.5" /> {t('COD available')}
                      </span>
                    )}
                    {confirmedQuote.returnPolicy?.allowed && (
                      <span className="text-[10px] bg-purple-500/10 text-purple-400 border border-purple-500/20 px-2 py-0.5 rounded-full">
                        ↩ {t('{days}-day returns', { days: isoDays(confirmedQuote.returnPolicy.window) })}
                      </span>
                    )}
                  </div>
                </div>
              </motion.div>
            )}

            {/* Billing form */}
            <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: 0.1 }}
              className="rounded-xl border border-white/10 bg-white/[0.03] overflow-hidden">
              <div className="px-4 py-2.5 bg-white/[0.04] border-b border-white/[0.07] flex items-center gap-2">
                <User className="w-3.5 h-3.5 text-slate-400" />
                <span className="text-xs font-semibold text-slate-400 uppercase tracking-wider flex-1">{t('Billing Details')}</span>
                <button
                  type="button" data-tour="billing-autofill"
                  onClick={() => {
                    const profile = BILLING_PROFILES[_billingIdx % BILLING_PROFILES.length];
                    _billingIdx++;
                    setBilling(profile);
                  }}
                  className="flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-[11px] font-semibold border border-purple-500/30 text-purple-400 hover:bg-purple-500/10 transition-colors"
                >
                  <Wand2 className="w-3 h-3" /> {t('Auto-fill')}
                </button>
              </div>
              <div className="px-4 py-3 space-y-3">
                <div className="relative">
                  <User className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-slate-500" />
                  <input
                    type="text" placeholder={t('Full name')}
                    value={billing.name}
                    onChange={e => setBilling(p => ({ ...p, name: e.target.value }))}
                    className="w-full bg-slate-900 border border-white/10 rounded-lg pl-9 pr-3 py-2.5 text-sm text-white placeholder-slate-500 outline-none focus:border-emerald-500/40 transition-colors"
                  />
                </div>
                <div className="relative">
                  <Phone className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-slate-500" />
                  <input
                    type="tel" placeholder={t('Phone number')}
                    value={billing.phone}
                    onChange={e => setBilling(p => ({ ...p, phone: e.target.value }))}
                    className="w-full bg-slate-900 border border-white/10 rounded-lg pl-9 pr-3 py-2.5 text-sm text-white placeholder-slate-500 outline-none focus:border-emerald-500/40 transition-colors"
                  />
                </div>
                <div className="relative">
                  <User className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-slate-500" />
                  <input
                    type="email" placeholder={t('Email address')}
                    value={billing.email}
                    onChange={e => setBilling(p => ({ ...p, email: e.target.value }))}
                    className="w-full bg-slate-900 border border-white/10 rounded-lg pl-9 pr-3 py-2.5 text-sm text-white placeholder-slate-500 outline-none focus:border-emerald-500/40 transition-colors"
                  />
                </div>
                <div className="relative">
                  <Home className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-slate-500" />
                  <input
                    type="text" placeholder={t('Street address')}
                    value={billing.streetAddress}
                    onChange={e => setBilling(p => ({ ...p, streetAddress: e.target.value }))}
                    className="w-full bg-slate-900 border border-white/10 rounded-lg pl-9 pr-3 py-2.5 text-sm text-white placeholder-slate-500 outline-none focus:border-emerald-500/40 transition-colors"
                  />
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                  <input
                    type="text" placeholder={t('City')}
                    value={billing.addressLocality}
                    onChange={e => setBilling(p => ({ ...p, addressLocality: e.target.value }))}
                    className="w-full bg-slate-900 border border-white/10 rounded-lg px-3 py-2.5 text-sm text-white placeholder-slate-500 outline-none focus:border-emerald-500/40 transition-colors"
                  />
                  <input
                    type="text" placeholder={t('State')}
                    value={billing.addressRegion}
                    onChange={e => setBilling(p => ({ ...p, addressRegion: e.target.value }))}
                    className="w-full bg-slate-900 border border-white/10 rounded-lg px-3 py-2.5 text-sm text-white placeholder-slate-500 outline-none focus:border-emerald-500/40 transition-colors"
                  />
                </div>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                  <input
                    type="text" placeholder={t('Pincode')}
                    value={billing.postalCode}
                    onChange={e => setBilling(p => ({ ...p, postalCode: e.target.value }))}
                    className="w-full bg-slate-900 border border-white/10 rounded-lg px-3 py-2.5 text-sm text-white placeholder-slate-500 outline-none focus:border-emerald-500/40 transition-colors"
                  />
                  <input
                    type="text" placeholder={t('Country (e.g. IN)')}
                    value={billing.addressCountry}
                    onChange={e => setBilling(p => ({ ...p, addressCountry: e.target.value }))}
                    className="w-full bg-slate-900 border border-white/10 rounded-lg px-3 py-2.5 text-sm text-white placeholder-slate-500 outline-none focus:border-emerald-500/40 transition-colors"
                  />
                </div>
              </div>
            </motion.div>
          </div>
        )}

        {/* ── Step: Waiting for on_init ──────────────────────────────── */}
        {status === 'INIT_SENT' && (
          <div className="flex flex-col items-center gap-3 py-8">
            <div className="relative w-12 h-12">
              <div className="absolute inset-0 rounded-full border-2 border-emerald-500/20 border-t-emerald-400 animate-spin" />
              <ShieldCheck className="absolute inset-0 m-auto w-5 h-5 text-emerald-400" />
            </div>
            <p className="text-sm text-slate-300">{t('Initializing your order…')}</p>
          </div>
        )}

        {/* ── Step: Init received → ready to confirm ─────────────────── */}
        {status === 'INIT_RECEIVED' && (
          <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }}
            className="rounded-xl border border-blue-500/20 bg-blue-500/5 px-4 py-3 space-y-2">
            <div className="flex items-center gap-2">
              <ShieldCheck className="w-4 h-4 text-blue-400 shrink-0" />
              <p className="text-sm font-medium text-blue-300">{t('Order Initialized')}</p>
            </div>
            <p className="text-xs text-slate-400 leading-relaxed">
              {t('Your order is ready. Review the details above and confirm to place your order on the ION network.')}
            </p>
            <div className="pt-1 text-xs text-slate-500 space-y-1">
              <p><span className="text-slate-400">{t('Name:')}</span> {billing.name || t('Customer')}</p>
              <p><span className="text-slate-400">{t('Phone:')}</span> {billing.phone || '—'}</p>
              {billing.email && <p><span className="text-slate-400">{t('Email:')}</span> {billing.email}</p>}
              <p><span className="text-slate-400">{t('Address:')}</span> {[billing.streetAddress, billing.addressLocality, billing.addressRegion, billing.postalCode].filter(Boolean).join(', ') || '—'}</p>
            </div>
          </motion.div>
        )}

        {/* ── Step: Waiting for on_confirm ──────────────────────────── */}
        {status === 'CONFIRM_SENT' && (
          <div className="flex flex-col items-center gap-3 py-8">
            <div className="relative w-12 h-12">
              <div className="absolute inset-0 rounded-full border-2 border-emerald-500/20 border-t-emerald-400 animate-spin" />
              <Banknote className="absolute inset-0 m-auto w-5 h-5 text-emerald-400" />
            </div>
            <p className="text-sm text-slate-300">{t('Placing your order…')}</p>
          </div>
        )}

        {/* ── Step: Status requested ─────────────────────────────────── */}
        {status === 'STATUS_SENT' && (
          <div className="flex flex-col items-center gap-3 py-4">
            <div className="relative w-12 h-12">
              <div className="absolute inset-0 rounded-full border-2 border-blue-500/20 border-t-blue-400 animate-spin" />
              <RefreshCw className="absolute inset-0 m-auto w-5 h-5 text-blue-400" />
            </div>
            <p className="text-sm text-slate-300">{t('Fetching the latest order status…')}</p>
          </div>
        )}

        {/* ── Step: Confirmed — Order Detail card ─────────────────── */}
        {status === 'CONFIRMED' && (
          <motion.div initial={{ scale: 0.95, opacity: 0 }} animate={{ scale: 1, opacity: 1 }}
            className="space-y-4">
            {/* Success banner */}
            <div className="flex flex-col items-center gap-2 py-4 text-center" data-tour="order-placed">
              <div className="w-16 h-16 rounded-full bg-emerald-500/15 border-2 border-emerald-500/40 flex items-center justify-center">
                <CheckCircle2 className="w-9 h-9 text-emerald-400" />
              </div>
              <p className="font-bold text-emerald-300 text-xl">{t('Order Placed!')}</p>
              <p className="text-xs text-slate-400">{t('Confirmed on the ION network')}</p>
            </div>

            {/* Order info */}
            <div className="text-xs text-slate-500 bg-white/[0.03] border border-white/[0.06] rounded-lg px-4 py-2.5 space-y-1">
              <p><span className="text-slate-400">{t('Order ID:')}</span> <span className="font-mono text-slate-300 break-all">{transaction.id}</span></p>
              {billing.name && <p><span className="text-slate-400">{t('Ordered by:')}</span> {billing.name}</p>}
              {billing.phone && <p><span className="text-slate-400">{t('Phone:')}</span> {billing.phone}</p>}
            </div>

            {/* Items — cancel one item, some quantity, or the whole order */}
            <LiveOrderItems txnId={transaction.id} onStatus={onStatusChange} />

            {/* Action buttons */}
            <div className="space-y-4" data-tour="order-actions">
              <button onClick={onRequestStatus}
                className="w-full flex items-center justify-center gap-1.5 py-2.5 rounded-xl text-xs font-semibold bg-blue-500/10 text-blue-300 border border-blue-500/20 hover:bg-blue-500/20 transition-all">
                <RefreshCw className="w-3.5 h-3.5" /> {t('Refresh Status')}
              </button>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                <button onClick={() => { setShowRatingForm(v => !v); setShowSupportForm(false); }}
                  className="flex items-center justify-center gap-1.5 py-2.5 rounded-xl text-xs font-semibold bg-amber-500/10 text-amber-300 border border-amber-500/20 hover:bg-amber-500/20 transition-all">
                  <Star className="w-3.5 h-3.5" /> {t('Rate Order')}
                </button>
                <button onClick={() => { setShowSupportForm(v => !v); setShowRatingForm(false); }}
                  className="flex items-center justify-center gap-1.5 py-2.5 rounded-xl text-xs font-semibold bg-purple-500/10 text-purple-300 border border-purple-500/20 hover:bg-purple-500/20 transition-all">
                  <MessageSquare className="w-3.5 h-3.5" /> {t('Get Support')}
                </button>
              </div>
            </div>

            {/* Inline rating form */}
            <AnimatePresence>
              {showRatingForm && !transaction.rated && (
                <motion.div initial={{ opacity: 0, y: -8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -8 }}
                  className="rounded-xl border border-amber-500/20 bg-amber-500/5 px-4 py-3 space-y-3">
                  <p className="text-xs font-semibold text-amber-300">{t('How was your experience?')}</p>
                  <StarRating value={ratingValue} onChange={setRatingValue} />
                  <textarea
                    value={ratingFeedback} onChange={e => setRatingFeedback(e.target.value)}
                    placeholder={t('Optional feedback…')}
                    rows={2}
                    className="w-full bg-slate-900 border border-white/10 rounded-lg px-3 py-2 text-xs text-white placeholder-slate-500 outline-none resize-none"
                  />
                  <button
                    disabled={!ratingValue || isLoading}
                    onClick={() => { onRate(ratingValue, ratingFeedback); setShowRatingForm(false); }}
                    className="w-full py-2 rounded-lg text-xs font-bold bg-amber-500 hover:bg-amber-400 text-white disabled:opacity-50 flex items-center justify-center gap-2 transition-all">
                    {isLoading ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <><ThumbsUp className="w-3.5 h-3.5" /> {t('Submit Rating')}</>}
                  </button>
                </motion.div>
              )}
              {transaction.rated && (
                <div className="rounded-xl border border-emerald-500/20 bg-emerald-500/5 px-4 py-2.5 text-xs text-emerald-300 flex items-center gap-2">
                  <CheckCircle2 className="w-3.5 h-3.5" /> {t('Rated {n}/5 — thank you!', { n: transaction.rated })}
                </div>
              )}
            </AnimatePresence>

            {/* Inline support form */}
            <AnimatePresence>
              {showSupportForm && (
                <motion.div initial={{ opacity: 0, y: -8 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -8 }}
                  className="rounded-xl border border-purple-500/20 bg-purple-500/5 px-4 py-3 space-y-3">
                  <p className="text-xs font-semibold text-purple-300">{t('Describe your issue')}</p>
                  <textarea
                    value={supportDesc} onChange={e => setSupportDesc(e.target.value)}
                    placeholder={t('What do you need help with?')}
                    rows={3}
                    className="w-full bg-slate-900 border border-white/10 rounded-lg px-3 py-2 text-xs text-white placeholder-slate-500 outline-none resize-none"
                  />
                  <button
                    disabled={!supportDesc.trim() || isLoading}
                    onClick={() => { onSupport(supportDesc); setSupportDesc(''); setShowSupportForm(false); }}
                    className="w-full py-2 rounded-lg text-xs font-bold bg-purple-600 hover:bg-purple-500 text-white disabled:opacity-50 flex items-center justify-center gap-2 transition-all">
                    {isLoading ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <><MessageSquare className="w-3.5 h-3.5" /> {t('Submit Request')}</>}
                  </button>
                </motion.div>
              )}
            </AnimatePresence>

            <button onClick={onDone} data-tour="continue"
              className="w-full bg-emerald-600 hover:bg-emerald-500 text-white py-3 rounded-xl font-bold text-sm flex items-center justify-center gap-2 transition-all shadow-lg shadow-emerald-600/30">
              <Sparkles className="w-4 h-4" /> {t('Continue Shopping')}
            </button>
          </motion.div>
        )}

        {/* ── Step: Cancel sent / Cancelled ──────────────────────────── */}
        {(status === 'CANCEL_SENT' || status === 'CANCELLED') && (
          <motion.div initial={{ scale: 0.95, opacity: 0 }} animate={{ scale: 1, opacity: 1 }}
            className="flex flex-col items-center gap-3 py-6 text-center">
            <div className="w-16 h-16 rounded-full bg-rose-500/15 border-2 border-rose-500/40 flex items-center justify-center">
              {status === 'CANCEL_SENT'
                ? <Loader2 className="w-9 h-9 text-rose-400 animate-spin" />
                : <Ban className="w-9 h-9 text-rose-400" />}
            </div>
            <p className="font-bold text-rose-300 text-lg">
              {status === 'CANCEL_SENT' ? t('Cancelling…') : t('Order Cancelled')}
            </p>
            <p className="text-xs text-slate-400">
              {status === 'CANCEL_SENT' ? t('Waiting for the seller to confirm the cancellation.') : t('Your order has been cancelled.')}
            </p>
            {status === 'CANCELLED' && (
              <button onClick={onDone}
                className="w-full mt-2 bg-slate-700 hover:bg-slate-600 text-white py-3 rounded-xl font-bold text-sm flex items-center justify-center gap-2 transition-all">
                <Sparkles className="w-4 h-4" /> {t('Back to Shopping')}
              </button>
            )}
          </motion.div>
        )}
      </div>

      {/* ── Sticky action footer ────────────────────────────────────── */}
      {((status === 'QUOTE_RECEIVED' && !blocked) || status === 'INIT_RECEIVED') && (
        <div className="px-5 py-4 border-t border-white/[0.07] shrink-0">
          {status === 'QUOTE_RECEIVED' && (
            <button onClick={onInit} disabled={isLoading} data-tour="init"
              className="w-full bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 text-white py-3.5 rounded-xl font-bold text-sm flex items-center justify-center gap-2 transition-all active:scale-[0.98] shadow-lg shadow-emerald-600/30">
              {isLoading
                ? <><Loader2 className="w-4 h-4 animate-spin" /> {t('Initializing…')}</>
                : <><ArrowRight className="w-4 h-4" /> {t('Initialize Order')}</>}
            </button>
          )}
          {status === 'INIT_RECEIVED' && (
            <button onClick={onConfirm} disabled={isLoading} data-tour="confirm"
              className="w-full bg-amber-500 hover:bg-amber-400 disabled:opacity-50 text-white py-3.5 rounded-xl font-bold text-sm flex items-center justify-center gap-2 transition-all active:scale-[0.98] shadow-lg shadow-amber-500/30">
              {isLoading
                ? <><Loader2 className="w-4 h-4 animate-spin" /> {t('Confirming…')}</>
                : <><ShieldCheck className="w-4 h-4" /> {t('Pay & Confirm Order')}</>}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

/* ─────────────────── Sample billing profiles ───────────────────────────── */

const BILLING_PROFILES = [
  {
    name: 'Arjun Sharma',
    email: 'arjun.sharma@example.in',
    phone: '+919876543210',
    streetAddress: '42 MG Road, Koramangala',
    addressLocality: 'Bengaluru',
    addressRegion: 'Karnataka',
    postalCode: '560034',
    addressCountry: 'IN',
  },
  {
    name: 'Priya Nair',
    email: 'priya.nair@example.in',
    phone: '+917012345678',
    streetAddress: '15 Marine Drive, Fort',
    addressLocality: 'Mumbai',
    addressRegion: 'Maharashtra',
    postalCode: '400001',
    addressCountry: 'IN',
  },
  {
    name: 'Reza Pratama',
    email: 'reza.pratama@example.id',
    phone: '+6281234567890',
    streetAddress: 'Jl. Sudirman No. 52, Senayan',
    addressLocality: 'Jakarta',
    addressRegion: 'DKI Jakarta',
    postalCode: '10270',
    addressCountry: 'ID',
  },
];
let _billingIdx = 0;

/* ─────────────────── Order History Drawer ──────────────────────────────── */

const STATUS_COLORS = {
  CONFIRMED: { bg: 'bg-emerald-500/10', text: 'text-emerald-400', border: 'border-emerald-500/20' },
  CANCELLED: { bg: 'bg-rose-500/10', text: 'text-rose-400', border: 'border-rose-500/20' },
  INIT_SENT: { bg: 'bg-blue-500/10', text: 'text-blue-400', border: 'border-blue-500/20' },
  CONFIRM_SENT: { bg: 'bg-amber-500/10', text: 'text-amber-400', border: 'border-amber-500/20' },
  INIT_RECEIVED: { bg: 'bg-emerald-500/10', text: 'text-emerald-300', border: 'border-emerald-500/20' },
  CANCEL_SENT: { bg: 'bg-rose-500/10', text: 'text-rose-400', border: 'border-rose-500/20' },
  UPDATE_SENT: { bg: 'bg-purple-500/10', text: 'text-purple-400', border: 'border-purple-500/20' },
  Return_Initiated: { bg: 'bg-yellow-500/10', text: 'text-yellow-400', border: 'border-yellow-500/20' },
  Return_Approved: { bg: 'bg-green-500/10', text: 'text-green-400', border: 'border-green-500/20' },
  Return_Rejected: { bg: 'bg-red-500/10', text: 'text-red-400', border: 'border-red-500/20' },
};

// A transaction's status as shown to the buyer (codes from the BAP's
// transactions table); unknown codes are shown as they are.
const TXN_STATUS_LABELS = {
  SELECT_SENT: 'Getting quote', QUOTE_RECEIVED: 'Quote received', INIT_SENT: 'Processing', INIT_RECEIVED: 'Awaiting payment',
  CONFIRM_SENT: 'Confirming', CONFIRMED: 'Confirmed', STATUS_SENT: 'Checking status', CANCEL_SENT: 'Cancelling',
  CANCELLED: 'Cancelled', UPDATE_SENT: 'Updating', COMPLETE: 'Completed', RATE_SENT: 'Rating sent', SUPPORT_SENT: 'Support requested',
  Return_Initiated: 'Return requested', Return_Approved: 'Return approved', Return_Rejected: 'Return rejected',
};
const txnStatusLabel = (t, s) => (TXN_STATUS_LABELS[s] ? t(TXN_STATUS_LABELS[s]) : String(s || '').replace(/_/g, ' '));

function fmtOrderDate(iso) {
  if (!iso) return '—';
  try {
    return new Date(iso).toLocaleString(localeNow(), {
      day: 'numeric', month: 'short', year: 'numeric',
      hour: '2-digit', minute: '2-digit',
    });
  } catch { return iso; }
}

/* ── helpers to parse contract data ─────────────────────────────────────── */

const fmtIDR = (amount, cur) => {
  if (amount == null) return '—';
  try { return new Intl.NumberFormat('id-ID', { style: 'currency', currency: cur || 'IDR', maximumFractionDigits: 0 }).format(amount); }
  catch { return `${cur || 'IDR'} ${amount}`; }
};

// ION cancellation reasons a buyer can give (TradeOffer
// policies.cancellation.allowedReasons; BUYER_FRAUD_SUSPECTED is seller-side).
const CANCEL_REASONS = [
  ['CHANGE_OF_MIND', 'Change of mind'],
  ['WRONG_ITEM_ORDERED', 'Wrong item ordered'],
  ['ORDERED_WRONG_VARIANT', 'Ordered wrong variant'],
  ['FOUND_BETTER_PRICE', 'Found a better price'],
  ['DELIVERY_TOO_LONG', 'Delivery takes too long'],
  ['INCORRECT_ADDRESS', 'Incorrect address'],
  ['DUPLICATE_ORDER', 'Duplicate order'],
  ['PAYMENT_METHOD_CHANGE', 'Payment method change'],
  ['ITEM_NO_LONGER_NEEDED', 'Item no longer needed'],
  ['SELLER_UNRESPONSIVE', 'Seller unresponsive'],
  ['QUALITY_ISSUE_AT_PREVIEW', 'Quality issue at preview'],
];

// Fulfillment stage of a performance status code (PENDING → Packed → Shipped
// → Delivered; returns only follow delivery).
const stageRank = code => {
  const c = String(code || '').toUpperCase();
  if (c === 'PACKED') return 1;
  if (['DISPATCHED', 'SHIPPED', 'IN_TRANSIT', 'OUT_FOR_DELIVERY'].includes(c)) return 2;
  if (c === 'DELIVERED' || c.startsWith('RETURN')) return 3;
  return 0;
};

// Why an item can't be cancelled any more under its offer's cancellation
// policy (allowed=false, or the cutoffEvent has passed), or null if it can.
// The BAP and BPP enforce the same rule.
// Fulfillment status codes as shown to the buyer.
const STAGE_LABELS = {
  PENDING: 'Pending', CONFIRMED: 'Confirmed', PACKED: 'Packed', DISPATCHED: 'Dispatched', SHIPPED: 'Shipped',
  IN_TRANSIT: 'In transit', OUT_FOR_DELIVERY: 'Out for delivery', DELIVERED: 'Delivered',
  Return_Initiated: 'Return requested', Return_Approved: 'Return approved', Return_Rejected: 'Return rejected',
};
// Codes arrive as PACKED or Packed, depending on the seller.
const stageLabel = code => {
  const label = STAGE_LABELS[code] || STAGE_LABELS[String(code || '').toUpperCase()];
  return label ? tNow(label) : code;
};

// Cancellation cutoffs (policies.cancellation.cutoffEvent), as a phrase.
const CUTOFF_LABELS = {
  BEFORE_CONFIRMATION: 'before confirmation', BEFORE_PACKING: 'before packing',
  BEFORE_DISPATCH: 'before dispatch', BEFORE_SHIPMENT: 'before shipment',
};

function cancelBlockedReason(policy, fulfillmentStatus) {
  if (policy?.allowed === false) return tNow('This item cannot be cancelled');
  const stage = stageRank(fulfillmentStatus);
  const passed = {
    BEFORE_CONFIRMATION: true,
    BEFORE_PACKING: stage >= 1,
    BEFORE_DISPATCH: stage >= 2,
    BEFORE_SHIPMENT: stage >= 2,
  }[policy?.cutoffEvent];
  if (!passed) return null;
  const when = tNow(CUTOFF_LABELS[policy.cutoffEvent]);
  return tNow('Cancellation closed — only allowed {when}; the order is {status}', {
    when, status: fulfillmentStatus ? stageLabel(fulfillmentStatus) : tNow('Confirmed'),
  });
}

// ISO 8601 duration (P7D, PT2H, P1W2DT3H) in milliseconds; null if invalid.
function isoDurationMs(s) {
  const m = /^P(?:(\d+(?:\.\d+)?)Y)?(?:(\d+(?:\.\d+)?)M)?(?:(\d+(?:\.\d+)?)W)?(?:(\d+(?:\.\d+)?)D)?(?:T(?:(\d+(?:\.\d+)?)H)?(?:(\d+(?:\.\d+)?)M)?(?:(\d+(?:\.\d+)?)S)?)?$/.exec(s || '');
  if (!m || s === 'P' || s === 'PT') return null;
  const [y, mo, w, d, h, mi, se] = m.slice(1).map(v => Number(v || 0));
  return ((((y * 365 + mo * 30 + w * 7 + d) * 24 + h) * 60 + mi) * 60 + se) * 1000;
}

// Return eligibility under an item's offer policies.returns -- the BAP and
// BPP enforce the same rule. { blocked } explains a refusal; otherwise
// { until, fee } describe the open window and the restocking fee.
function returnEligibility(policy, fulfillmentStatus, deliveredAt) {
  if (!policy?.allowed) return { blocked: tNow('This item is not returnable') };
  if (stageRank(fulfillmentStatus) < 3) return { blocked: tNow('Returns open once the order is delivered') };
  const f = policy.restockingFee;
  const fee = f?.type === 'PERCENTAGE' ? tNow('{pct}% restocking fee', { pct: f.value })
    : f?.type === 'FIXED' ? tNow('{amount} restocking fee', { amount: `Rp ${Number(f.value).toLocaleString(localeNow())}` })
      : tNow('No restocking fee');
  const ms = isoDurationMs(policy.window);
  if (!policy.window || ms == null || !deliveredAt) return { fee };
  const until = new Date(new Date(deliveredAt).getTime() + ms);
  if (Date.now() > until.getTime()) {
    return { blocked: tNow('The return window ({window}) closed on {date}', { window: policy.window, date: fmtOrderDate(until) }) };
  }
  return { until, fee };
}

// A refund settlement's status (settlementAttributes.refund.status) for display.
const REFUND_STATUS = { PENDING: 'pending', INITIATED: 'initiated', PROCESSING: 'processing', COMPLETED: 'completed', COMPLETE: 'completed', FAILED: 'failed' };
const refundStatus = (t, s) => {
  const k = String(s || '').toUpperCase();
  return REFUND_STATUS[k] ? t(REFUND_STATUS[k]) : String(s || '').toLowerCase();
};

// Reason picker limited to what every offer involved allows (all reasons when
// no offer declares allowedReasons).
function CancelReasonSelect({ value, onChange, allowedLists = [] }) {
  const lists = allowedLists.filter(l => Array.isArray(l) && l.length);
  const options = CANCEL_REASONS.filter(([code]) => lists.every(l => l.includes(code)));
  const { t } = useI18n();
  return (
    <select value={value} onChange={e => onChange(e.target.value)}
      className="bg-slate-900 border border-white/10 rounded-lg px-2 py-1.5 text-xs text-slate-200 focus:outline-none focus:border-rose-500/40">
      <option value="">{t('Reason for cancelling…')}</option>
      {options.map(([code, label]) => <option key={code} value={code}>{t(label)}</option>)}
    </select>
  );
}

function parseContractDetail(contract) {
  if (!contract || typeof contract !== 'object') return null;

  // Commitments → items
  const commitments = contract.commitments || [];
  const items = commitments.map(c => ({
    id: c.id || '',
    // The product (resource) name; the offer name only for orders placed before
    // commitments carried resource descriptors.
    name: c.resources?.[0]?.descriptor?.name || c.offer?.descriptor?.name || c.id || tNow('Item'),
    qty: c.resources?.[0]?.quantity?.unitQuantity ?? c.commitmentAttributes?.quantity?.unitQuantity ?? 1,
    offerId: c.offer?.id || '',
    resourceId: c.resources?.[0]?.id || c.offer?.resourceIds?.[0] || '',
    // A cancelled commitment is CLOSED (its ACTIVE replacement carries what remains).
    cancelled: c.status?.descriptor?.code === 'CLOSED',
    closedAs: c.status?.descriptor?.name,
    allowedReasons: c.offer?.offerAttributes?.policies?.cancellation?.allowedReasons,
    cancelPolicy: c.offer?.offerAttributes?.policies?.cancellation,
    returnPolicy: c.offer?.offerAttributes?.policies?.returns,
    // Per-unit price incl. tax (the offer consideration's totalAmount), falling
    // back to the commitment's pre-tax unit price for older orders.
    unitPrice: c.offer?.considerations?.[0]?.considerationAttributes?.totalAmount
      ?? c.commitmentAttributes?.price?.value ?? null,
  }));

  // Consideration → price. After a cancellation the old price is CANCELLED and
  // the ACTIVE one (if any) is what the buyer still pays; cancellation fees are
  // separate "cancellation-…" considerations.
  const considerations = contract.consideration || [];
  const isFee = c => /^(cancellation|restocking)-/.test(c.id || '');
  const consideration = considerations.find(c => !isFee(c) && c.status?.code !== 'CANCELLED')
    || considerations.find(c => !isFee(c)) || considerations[0];
  const fees = considerations.filter(isFee).map(c => ({
    id: c.id,
    amount: c.considerationAttributes?.totalAmount ?? 0,
    title: c.considerationAttributes?.breakup?.[0]?.title || tNow('Cancellation fee'),
  }));
  const refunds = (contract.settlements || [])
    .filter(st => st.settlementAttributes?.refund)
    .map(st => ({ id: st.id, ...st.settlementAttributes.refund }));
  const fullyCancelled = contract.status?.code === 'CANCELLED';
  const consAttr = consideration?.considerationAttributes || {};
  const price = consAttr.totalAmount ?? null;
  const currency = consAttr.currency || 'IDR';
  const breakup = combineTax(consAttr.breakup);

  // Participants
  const participants = contract.participants || [];
  const buyer = participants.find(p => p.role?.code === 'BUYER' || p.role === 'BUYER');
  const seller = participants.find(p => p.role?.code === 'SELLER' || p.role === 'SELLER');

  // Status
  const statusCode = contract.status?.code || '';

  // Performance (replaces fulfillments in Beckn v2)
  const performances = contract.performance || [];
  let fulfillmentStatus = performances[0]?.status?.code || '';
  const returnPerformance = performances.find(p => p.performanceAttributes?.type === 'RETURN');
  // Every return request and its policy outcome (Return_Approved / Return_Rejected + reason).
  const returns = performances.filter(p => p.performanceAttributes?.type === 'RETURN').map(p => ({
    id: p.id, code: p.status?.code || '', reason: p.status?.shortDesc || '',
    items: (p.commitmentIds || []).map(id => commitments.find(c => c.id === id)?.resources?.[0]?.descriptor?.name
      || commitments.find(c => c.id === id)?.offer?.descriptor?.name || id),
  }));
  if (returnPerformance && returnPerformance.status?.code) {
    fulfillmentStatus = returnPerformance.status.code;
  }

  return {
    items, price: fullyCancelled && consideration?.status?.code === 'CANCELLED' ? 0 : price,
    currency, breakup: fullyCancelled ? [] : breakup, fees, refunds, returns,
    buyer, seller, statusCode, fulfillmentStatus, id: contract.id,
  };
}

const ACTION_LABELS = {
  select: { icon: '🔍', label: 'Item Selected', color: 'text-cyan-400' },
  on_select: { icon: '💬', label: 'Quote Received', color: 'text-blue-400' },
  init: { icon: '📋', label: 'Order Initialised', color: 'text-indigo-400' },
  on_init: { icon: '✅', label: 'Order Confirmed', color: 'text-emerald-400' },
  confirm: { icon: '🛒', label: 'Confirm Sent', color: 'text-amber-400' },
  on_confirm: { icon: '🎉', label: 'Order Placed', color: 'text-emerald-400' },
  cancel: { icon: '🚫', label: 'Cancel Requested', color: 'text-rose-400' },
  on_cancel: { icon: '❌', label: 'Order Cancelled', color: 'text-rose-400' },
  rate: { icon: '⭐', label: 'Rated', color: 'text-amber-400' },
  on_rate: { icon: '⭐', label: 'Rating Confirmed', color: 'text-amber-400' },
  on_status: { icon: '📊', label: 'Status Update', color: 'text-slate-400' },
  update: { icon: '✏️', label: 'Update Requested', color: 'text-sky-400' },
  on_update: { icon: '🔄', label: 'Order Updated', color: 'text-sky-400' },
  reconcile: { icon: '🏦', label: 'Seller Settlement', color: 'text-emerald-400' },
};

// Items of a confirmed order with per-item cancellation: pick a quantity and
// cancel it, or cancel the whole order. Cancelling everything still active
// goes out as Beckn cancel, anything less as a partial-cancellation update --
// the BAP decides (/cancel-items). onChanged is called once a request is sent.
function OrderItemsCard({ txnId, order, txnStatus, deliveredAt, onChanged }) {
  const { t } = useI18n();
  const [cancelQty, setCancelQty] = useState({});   // commitmentId → units to cancel / return
  const [cancelling, setCancelling] = useState(null); // commitmentId | 'ALL' | null
  const [cancelReason, setCancelReason] = useState('');
  // Delivered orders switch from cancellation to returns (offer return policy).
  const delivered = stageRank(order.fulfillmentStatus) >= 3;

  const activeItems = order.items.filter(it => !it.cancelled);
  const blockedReason = it => cancelBlockedReason(it.cancelPolicy, order.fulfillmentStatus)
    || (stageRank(order.fulfillmentStatus) >= 3 ? t('Already delivered — use Return instead') : null);
  const cancellable = activeItems.filter(it => !blockedReason(it));
  // Cancellation is offered on a confirmed order, per item, until its offer's cutoff.
  const canCancel = !delivered && txnStatus === 'CONFIRMED' && cancellable.length > 0;
  const returnable = activeItems.filter(it => !returnEligibility(it.returnPolicy, order.fulfillmentStatus, deliveredAt).blocked);
  const canReturn = delivered && txnStatus === 'CONFIRMED' && returnable.length > 0;

  const returnItems = async (items, key) => {
    if (!window.confirm(key === 'ALL' ? t('Return all items?') : t('Return the selected quantity of this item?'))) return;
    setCancelling(key);
    try {
      const r = await apiFetch('/return-items', { method: 'POST', body: JSON.stringify({ transaction_id: txnId, items }) });
      if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || `HTTP ${r.status}`);
      setCancelQty({});
      onChanged?.();
    } catch (e) {
      alert(t('Return failed: {error}', { error: e.message }));
    } finally {
      setCancelling(null);
    }
  };

  const cancelItems = async (items, key) => {
    if (!cancelReason) { alert(t('Please choose a reason for cancelling.')); return; }
    const whole = key === 'ALL';
    if (!window.confirm(whole ? t('Cancel the entire order?') : t('Cancel the selected quantity of this item?'))) return;
    setCancelling(key);
    try {
      const r = await apiFetch('/cancel-items', {
        method: 'POST',
        body: JSON.stringify({ transaction_id: txnId, items, reason_code: cancelReason }),
      });
      if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || `HTTP ${r.status}`);
      setCancelQty({});
      onChanged?.();
    } catch (e) {
      alert(t('Cancel failed: {error}', { error: e.message }));
    } finally {
      setCancelling(null);
    }
  };

  return (
    <div className="rounded-xl border border-white/[0.07] overflow-hidden" style={{ background: 'rgba(255,255,255,0.02)' }}>
      <div className="px-4 py-2.5 border-b border-white/[0.06] flex items-center gap-2">
        <ShoppingCart className="w-3.5 h-3.5 text-slate-500" />
        <p className="text-[11px] font-bold uppercase tracking-widest text-slate-500">{t('Items Ordered')}</p>
        {['CANCEL_SENT', 'UPDATE_SENT'].includes(txnStatus) && (
          <span className="ml-auto flex items-center gap-1 text-[10px] text-amber-400"><Loader2 className="w-3 h-3 animate-spin" /> {t('Updating…')}</span>
        )}
      </div>
      <div className="divide-y divide-white/[0.04]">
        {order.items.map((item, i) => {
          const q = Math.min(cancelQty[item.id] ?? 1, item.qty);
          return (
            <div key={i} className={`px-4 py-2.5 ${item.cancelled ? 'opacity-60' : ''}`}>
              <div className="flex items-center gap-3">
                <div className="w-8 h-8 rounded-lg bg-slate-800 flex items-center justify-center shrink-0">
                  {item.cancelled ? <Ban className="w-3.5 h-3.5 text-rose-400" /> : <Package className="w-3.5 h-3.5 text-slate-500" />}
                </div>
                <div className="flex-1 min-w-0">
                  <p className={`text-sm font-medium truncate ${item.cancelled ? 'text-slate-400 line-through' : 'text-white'}`}>{item.name}</p>
                  {item.resourceId && <p className="text-[10px] font-mono text-slate-600 truncate">{item.resourceId}</p>}
                </div>
                <div className="text-right shrink-0 flex items-center gap-2">
                  {item.cancelled && <span className="text-[10px] font-bold text-rose-400 bg-rose-500/10 border border-rose-500/20 px-1.5 py-0.5 rounded">{item.closedAs === 'Returned' ? t('Returned') : t('Cancelled')}</span>}
                  <div>
                    {item.unitPrice != null && (
                      <p className={`text-sm font-semibold tabular-nums ${item.cancelled ? 'text-slate-500 line-through' : 'text-slate-200'}`}>
                        {fmtIDR(item.unitPrice * item.qty, order.currency)}
                      </p>
                    )}
                    <p className="text-[11px] text-slate-500 tabular-nums">
                      ×{item.qty}{item.unitPrice != null && item.qty > 1 ? ` · ${t('{price} each', { price: fmtIDR(item.unitPrice, order.currency) })}` : ''}
                    </p>
                  </div>
                </div>
              </div>
              {!delivered && txnStatus === 'CONFIRMED' && !item.cancelled && blockedReason(item) && (
                <p className="mt-1.5 text-right text-[10px] text-slate-500">{blockedReason(item)}</p>
              )}
              {delivered && txnStatus === 'CONFIRMED' && !item.cancelled && (() => {
                const el = returnEligibility(item.returnPolicy, order.fulfillmentStatus, deliveredAt);
                if (el.blocked) return <p className="mt-1.5 text-right text-[10px] text-slate-500">{el.blocked}</p>;
                return (
                  <div className="mt-2 flex flex-wrap items-center justify-end gap-2">
                    <span className="text-[10px] text-slate-500 mr-auto">
                      {el.until ? `${t('Returnable until {date}', { date: fmtOrderDate(el.until) })} · ` : ''}{el.fee}
                    </span>
                    {item.qty > 1 && (
                      <div className="flex items-center rounded-lg border border-white/10 overflow-hidden text-xs">
                        <button className="px-2 py-1 text-slate-400 hover:text-white disabled:opacity-30" disabled={q <= 1}
                          onClick={() => setCancelQty(m => ({ ...m, [item.id]: q - 1 }))}><Minus className="w-3 h-3" /></button>
                        <span className="px-2 text-slate-200 tabular-nums">{q}</span>
                        <button className="px-2 py-1 text-slate-400 hover:text-white disabled:opacity-30" disabled={q >= item.qty}
                          onClick={() => setCancelQty(m => ({ ...m, [item.id]: q + 1 }))}><Plus className="w-3 h-3" /></button>
                      </div>
                    )}
                    <button disabled={!!cancelling}
                      onClick={() => returnItems([{ commitment_id: item.id, quantity: q }], item.id)}
                      className="px-2.5 py-1 rounded-lg text-[11px] font-semibold text-purple-300 border border-purple-500/30 hover:bg-purple-500/10 disabled:opacity-50">
                      {cancelling === item.id ? t('Requesting…') : item.qty > 1 ? t('Return {n}', { n: q }) : t('Return item')}
                    </button>
                  </div>
                );
              })()}
              {canCancel && !item.cancelled && !blockedReason(item) && (
                <div className="mt-2 flex items-center justify-end gap-2">
                  {item.qty > 1 && (
                    <div className="flex items-center rounded-lg border border-white/10 overflow-hidden text-xs">
                      <button className="px-2 py-1 text-slate-400 hover:text-white disabled:opacity-30" disabled={q <= 1}
                        onClick={() => setCancelQty(m => ({ ...m, [item.id]: q - 1 }))}><Minus className="w-3 h-3" /></button>
                      <span className="px-2 text-slate-200 tabular-nums">{q}</span>
                      <button className="px-2 py-1 text-slate-400 hover:text-white disabled:opacity-30" disabled={q >= item.qty}
                        onClick={() => setCancelQty(m => ({ ...m, [item.id]: q + 1 }))}><Plus className="w-3 h-3" /></button>
                    </div>
                  )}
                  <button disabled={!!cancelling}
                    onClick={() => cancelItems([{ commitment_id: item.id, quantity: q }], item.id)}
                    className="px-2.5 py-1 rounded-lg text-[11px] font-semibold text-rose-300 border border-rose-500/30 hover:bg-rose-500/10 disabled:opacity-50">
                    {cancelling === item.id ? t('Cancelling…') : item.qty > 1 ? t('Cancel {n}', { n: q }) : t('Cancel item')}
                  </button>
                </div>
              )}
            </div>
          );
        })}
      </div>
      {canCancel && (
        <div className="px-4 py-2.5 border-t border-white/[0.06] flex flex-wrap items-center justify-end gap-2">
          <CancelReasonSelect value={cancelReason} onChange={setCancelReason} allowedLists={cancellable.map(it => it.allowedReasons)} />
          {cancellable.length === activeItems.length && <button disabled={!!cancelling}
            onClick={() => cancelItems(activeItems.map(it => ({ commitment_id: it.id, quantity: it.qty })), 'ALL')}
            className="px-3 py-1.5 rounded-lg text-xs font-bold text-white bg-rose-600 hover:bg-rose-500 disabled:opacity-50">
            {cancelling === 'ALL' ? t('Cancelling…') : t('Cancel entire order')}
          </button>}
        </div>
      )}
      {canReturn && returnable.length === activeItems.length && activeItems.length > 1 && (
        <div className="px-4 py-2.5 border-t border-white/[0.06] flex justify-end">
          <button disabled={!!cancelling}
            onClick={() => returnItems(activeItems.map(it => ({ commitment_id: it.id, quantity: it.qty })), 'ALL')}
            className="px-3 py-1.5 rounded-lg text-xs font-bold text-white bg-purple-600 hover:bg-purple-500 disabled:opacity-50">
            {cancelling === 'ALL' ? t('Requesting…') : t('Return all items')}
          </button>
        </div>
      )}
      {order.returns?.length > 0 && (
        <div className="px-4 py-2.5 border-t border-white/[0.06] space-y-1">
          {order.returns.map(r => (
            <div key={r.id} className="text-xs flex items-start justify-between gap-3">
              <span className="text-slate-400">{t('Return')} · {r.items.join(', ') || r.id}</span>
              <span className={r.code === 'Return_Approved' ? 'text-emerald-400' : r.code === 'Return_Rejected' ? 'text-rose-400 text-right' : 'text-amber-400'}>
                {r.code === 'Return_Approved' ? t('Approved') : r.code === 'Return_Rejected' ? `${t('Rejected')}${r.reason ? ` — ${r.reason}` : ''}` : t('Requested…')}
              </span>
            </div>
          ))}
        </div>
      )}
      {(order.fees.length > 0 || order.refunds.length > 0) && (
        <div className="px-4 py-2.5 border-t border-white/[0.06] space-y-1">
          {order.fees.map(f => (
            <div key={f.id} className="flex justify-between text-xs">
              <span className="text-slate-400">{f.title}</span>
              <span className="text-amber-400">{fmtIDR(f.amount, order.currency)}</span>
            </div>
          ))}
          {order.refunds.map(r => (
            <div key={r.id} className="flex justify-between text-xs">
              <span className="text-slate-400">
                {t('Refund')} <span className="text-slate-600">· {refundStatus(t, r.status)}{r.timelineDays ? ` · ${t('within {n} days', { n: r.timelineDays })}` : ''}</span>
              </span>
              <span className="text-emerald-400">{fmtIDR(r.amount, order.currency)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// OrderItemsCard for a just-confirmed order: loads the latest contract itself
// and re-fetches until a cancel/update callback lands. onStatus is told only
// when the order ends up fully CANCELLED -- in-flight states stay in the card.
function LiveOrderItems({ txnId, onStatus }) {
  const [data, setData] = useState(null);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    apiFetch(`/status/${txnId}`).then(r => r.json()).then(d => {
      setData(d);
      if (d.status === 'CANCELLED') onStatus?.(d.status);
    }).catch(() => { });
  }, [txnId, tick]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!data || !['CANCEL_SENT', 'UPDATE_SENT'].includes(data.status)) return;
    const timer = setTimeout(() => setTick(k => k + 1), 2000);
    return () => clearTimeout(timer);
  }, [data]);
  const contract = typeof data?.contract === 'string' ? JSON.parse(data.contract) : data?.contract;
  const order = parseContractDetail(contract?.message?.contract || contract);
  if (!order?.items?.length) return null;
  return <OrderItemsCard txnId={txnId} order={order} txnStatus={data.status} onChanged={() => setTick(k => k + 1)} />;
}

function OrderDetailPanel({ txnId, onBack }) {
  const { t, locale } = useI18n();
  const [detail, setDetail] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [confirming, setConfirming] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    if (reloadKey === 0) { setLoading(true); setError(null); }
    apiFetch(`/orders/${txnId}`)
      .then(r => r.ok ? r.json() : r.json().then(e => Promise.reject(e.error || t('Could not load the order'))))
      .then(d => { setDetail(d); setLoading(false); })
      .catch(e => { setError(String(e)); setLoading(false); });
  }, [txnId, reloadKey]);

  // While a cancel/update is in flight, re-fetch until the BPP's callback lands.
  useEffect(() => {
    if (!detail || !['CONFIRM_SENT', 'CANCEL_SENT', 'UPDATE_SENT'].includes(detail.status)) return;
    const timer = setTimeout(() => setReloadKey(k => k + 1), 2000);
    return () => clearTimeout(timer);
  }, [detail]);

  if (loading) return (
    <div className="flex-1 flex items-center justify-center">
      <Loader2 className="w-8 h-8 text-cyan-400 animate-spin" />
    </div>
  );

  if (error) return (
    <div className="flex-1 flex flex-col items-center justify-center gap-3 text-slate-500 px-6">
      <Package className="w-10 h-10 opacity-20" />
      <p className="text-sm text-center">{error}</p>
    </div>
  );

  const sc = STATUS_COLORS[detail.status] || { bg: 'bg-slate-700/30', text: 'text-slate-400', border: 'border-slate-600/30' };

  // Best contract: latest snapshot with most data
  let bestContract = null;
  const snaps = (detail.snapshots || []);
  for (const s of [...snaps].reverse()) {
    const parsed = parseContractDetail(s.contract);
    if (parsed && (parsed.items.length > 0 || parsed.price !== null)) {
      bestContract = parsed;
      break;
    }
  }
  // Fallback to top-level contract
  if (!bestContract) bestContract = parseContractDetail(detail.contract);

  const fmt = (amount, cur) => {
    if (amount == null) return '—';
    try { return new Intl.NumberFormat('id-ID', { style: 'currency', currency: cur || 'IDR', maximumFractionDigits: 0 }).format(amount); }
    catch { return `${cur || 'IDR'} ${amount}`; }
  };

  // Pay & Confirm an order left at INIT_RECEIVED (the seller's on_init terms
  // were received but checkout was never completed) -- same /confirm call as
  // the checkout; the detail re-fetches until on_confirm lands.
  const handleConfirm = async () => {
    setConfirming(true);
    try {
      const r = await apiFetch('/confirm', { method: 'POST', body: JSON.stringify({ transaction_id: txnId }) });
      if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || `HTTP ${r.status}`);
      setReloadKey(k => k + 1);
    } catch (e) {
      alert(t('Confirm failed: {error}', { error: e.message }));
    } finally {
      setConfirming(false);
    }
  };

  // When the order was first seen Delivered -- the start of the return window.
  const deliveredAt = (detail.snapshots || [])
    .filter(sn => (parseContractDetail(sn.contract?.message?.contract || sn.contract)?.fulfillmentStatus === 'Delivered')
      || (sn.contract?.performance || []).some(p => p.status?.code === 'Delivered'))
    .map(sn => sn.created_at).sort()[0] || null;

  return (
    <div className="flex-1 overflow-y-auto px-4 py-4 space-y-4">

      {/* Status banner */}
      <div className={`rounded-xl px-4 py-3 flex items-center gap-3 border ${sc.bg} ${sc.border}`}>
        <div className={`w-9 h-9 rounded-full flex items-center justify-center ${sc.bg} border ${sc.border}`}>
          {detail.status === 'CONFIRMED' || detail.status === 'on_confirm'
            ? <CheckCircle2 className={`w-5 h-5 ${sc.text}`} />
            : detail.status.includes('CANCEL')
              ? <Ban className={`w-5 h-5 ${sc.text}`} />
              : <Package className={`w-5 h-5 ${sc.text}`} />
          }
        </div>
        <div>
          <p className={`text-sm font-bold ${sc.text}`}>{txnStatusLabel(t, detail.status)}</p>
          <p className="text-[10px] text-slate-500">{fmtOrderDate(detail.updated_at)}</p>
        </div>
      </div>

      {detail.status === 'INIT_RECEIVED' && (
        <div className="rounded-xl border border-emerald-500/20 px-4 py-3 bg-emerald-500/5 flex items-center justify-between gap-3">
          <div>
            <p className="text-xs font-bold text-emerald-300">{t('Awaiting payment')}</p>
            <p className="text-[10px] text-emerald-400/70 mt-0.5">{t('The seller has accepted your order. Pay to confirm it.')}</p>
          </div>
          <button disabled={confirming} onClick={handleConfirm}
            className="shrink-0 flex items-center gap-1.5 px-3 py-1.5 bg-emerald-600 hover:bg-emerald-500 text-white rounded-lg text-xs font-bold transition-colors disabled:opacity-50">
            {confirming ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <ShieldCheck className="w-3.5 h-3.5" />}
            {confirming ? t('Confirming…') : t('Pay & Confirm')}
          </button>
        </div>
      )}
      {detail.status === 'CONFIRM_SENT' && (
        <div className="rounded-xl border border-amber-500/20 px-4 py-3 bg-amber-500/5 flex items-center gap-2 text-xs text-amber-300">
          <Loader2 className="w-3.5 h-3.5 animate-spin" /> {t('Confirming your order with the seller…')}
        </div>
      )}



      {/* IDs */}
      <div className="rounded-xl border border-white/[0.07] px-4 py-3 space-y-2" style={{ background: 'rgba(255,255,255,0.02)' }}>
        <p className="text-[10px] font-bold uppercase tracking-widest text-slate-500 mb-2">{t('Transaction')}</p>
        <div className="flex items-start justify-between gap-2">
          <span className="text-[11px] text-slate-500 shrink-0">{t('Txn ID')}</span>
          <span className="text-[11px] font-mono text-slate-300 break-all text-right">{detail.transaction_id}</span>
        </div>
        {bestContract?.id && (
          <div className="flex items-start justify-between gap-2">
            <span className="text-[11px] text-slate-500 shrink-0">{t('Contract ID')}</span>
            <span className="text-[11px] font-mono text-slate-300 break-all text-right">{bestContract.id}</span>
          </div>
        )}
      </div>

      {/* Items + cancellation */}
      {bestContract?.items?.length > 0 && (
        <OrderItemsCard txnId={txnId} order={bestContract} txnStatus={detail.status} deliveredAt={deliveredAt}
          onChanged={() => setReloadKey(k => k + 1)} />
      )}

      {/* Price / Breakup */}
      {bestContract?.price !== null && bestContract?.price !== undefined && (
        <div className="rounded-xl border border-white/[0.07] overflow-hidden" style={{ background: 'rgba(255,255,255,0.02)' }}>
          <div className="px-4 py-2.5 border-b border-white/[0.06] flex items-center gap-2">
            <CreditCard className="w-3.5 h-3.5 text-slate-500" />
            <p className="text-[11px] font-bold uppercase tracking-widest text-slate-500">{t('Payment')}</p>
          </div>
          <div className="px-4 py-3 space-y-1.5">
            {bestContract.breakup.map((b, i) => (
              <div key={i} className="flex justify-between text-xs">
                <span className="text-slate-400">{b.title}</span>
                <span className={b.amount < 0 ? 'text-emerald-400' : 'text-slate-300'}>{fmt(b.amount, bestContract.currency)}</span>
              </div>
            ))}
            <div className="flex justify-between pt-2 border-t border-white/[0.06]">
              <span className="text-sm font-bold text-white">{t('Total')}</span>
              <span className="text-sm font-bold text-emerald-300">{fmt(bestContract.price, bestContract.currency)}</span>
            </div>
          </div>
        </div>
      )}

      {/* Buyer / Seller */}
      {(bestContract?.buyer || bestContract?.seller) && (
        <div className="rounded-xl border border-white/[0.07] overflow-hidden" style={{ background: 'rgba(255,255,255,0.02)' }}>
          <div className="px-4 py-2.5 border-b border-white/[0.06] flex items-center gap-2">
            <User className="w-3.5 h-3.5 text-slate-500" />
            <p className="text-[11px] font-bold uppercase tracking-widest text-slate-500">{t('Participants')}</p>
          </div>
          <div className="divide-y divide-white/[0.04]">
            {[bestContract.buyer && { role: t('Buyer'), p: bestContract.buyer }, bestContract.seller && { role: t('Seller'), p: bestContract.seller }].filter(Boolean).map(({ role, p }) => (
              <div key={role} className="px-4 py-2.5">
                <p className="text-[10px] text-slate-500 mb-1 uppercase tracking-wider">{role}</p>
                <p className="text-sm text-white font-medium">{p.descriptor?.name || p.contact?.name || '—'}</p>
                {p.contact?.phone && <p className="text-xs text-slate-400">{p.contact.phone}</p>}
                {p.contact?.email && <p className="text-xs text-slate-400">{p.contact.email}</p>}
                {p.location?.address && <p className="text-xs text-slate-500 mt-0.5">{p.location.address}</p>}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Timeline */}
      {snaps.length > 0 && (
        <div className="rounded-xl border border-white/[0.07] overflow-hidden" style={{ background: 'rgba(255,255,255,0.02)' }}>
          <div className="px-4 py-2.5 border-b border-white/[0.06] flex items-center gap-2">
            <Clock className="w-3.5 h-3.5 text-slate-500" />
            <p className="text-[11px] font-bold uppercase tracking-widest text-slate-500">{t('Order Timeline')}</p>
          </div>
          <div className="px-4 py-3 space-y-3">
            {snaps.map((s, i) => {
              const al = ACTION_LABELS[s.action] || { icon: '•', label: s.action, color: 'text-slate-500' };
              let displayLabel = ACTION_LABELS[s.action] ? t(al.label) : al.label;
              let displayIcon = al.icon;
              let displayColor = al.color;
              
              if (s.action === 'on_status' || s.action === 'on_update' || s.action === 'update') {
                const parsedSn = parseContractDetail(s.contract);
                const fs = parsedSn.fulfillmentStatus?.toLowerCase() || '';
                
                if (fs === 'packed') {
                  displayLabel = t('Order Packed');
                  displayIcon = '📦';
                  displayColor = 'text-orange-400';
                } else if (fs === 'shipped') {
                  displayLabel = t('Order Shipped');
                  displayIcon = '🚚';
                  displayColor = 'text-blue-400';
                } else if (fs === 'delivered') {
                  displayLabel = t('Order Delivered');
                  displayIcon = '📬';
                  displayColor = 'text-emerald-400';
                } else if (fs === 'return_initiated') {
                  displayLabel = t('Return Requested');
                  displayIcon = '🔄';
                  displayColor = 'text-purple-400';
                } else if (fs === 'return_approved') {
                  displayLabel = t('Return Approved');
                  displayIcon = '✅';
                  displayColor = 'text-emerald-400';
                } else if (fs === 'return_rejected') {
                  displayLabel = t('Return Rejected');
                  displayIcon = '❌';
                  displayColor = 'text-rose-400';
                }
              }
              
              return (
                <div key={i} className="flex items-start gap-3">
                  <div className="flex flex-col items-center shrink-0">
                    <div className={`w-7 h-7 rounded-full flex items-center justify-center text-base bg-slate-800 border border-white/[0.07] ${displayColor}`}>
                      {displayIcon}
                    </div>
                    {i < snaps.length - 1 && <div className="w-px h-4 bg-white/[0.06] mt-1" />}
                  </div>
                  <div className="flex-1 pb-1">
                    <p className={`text-xs font-semibold ${displayColor}`}>{displayLabel}</p>
                    <p className="text-[10px] text-slate-600">{fmtOrderDate(s.created_at)}</p>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

function OrderHistoryDrawer({ open, onClose }) {
  const { t, locale } = useI18n();
  const [orders, setOrders] = useState([]);
  const [loading, setLoading] = useState(false);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [selectedId, setSelectedId] = useState(null);
  const limit = 10;

  const load = useCallback(async (p = 1) => {
    setLoading(true);
    try {
      const r = await apiFetch(`/orders?page=${p}&limit=${limit}`);
      if (!r.ok) throw new Error(await r.text());
      const d = await r.json();
      setOrders(d.items || []);
      setTotal(d.total || 0);
      setPage(p);
    } catch (e) {
      console.error('orders fetch', e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (open) { load(1); setSelectedId(null); }
  }, [open, load]);

  const totalPages = Math.ceil(total / limit);
  const statusStyle = (s) => STATUS_COLORS[s] || { bg: 'bg-slate-700/40', text: 'text-slate-400', border: 'border-slate-600/30' };

  return (
    <AnimatePresence>
      {open && (
        <>
          <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
            className="fixed inset-0 bg-black/60 backdrop-blur-sm z-40" onClick={onClose} />
          <motion.aside
            initial={{ x: '100%' }} animate={{ x: 0 }} exit={{ x: '100%' }}
            transition={{ type: 'spring', damping: 30, stiffness: 300 }}
            className="fixed right-0 top-0 h-full w-full max-w-md glass z-50 flex flex-col shadow-2xl overflow-hidden"
          >
            {/* Header */}
            <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.07] shrink-0">
              <div className="flex items-center gap-2">
                {selectedId ? (
                  <button onClick={() => setSelectedId(null)} className="text-slate-400 hover:text-white transition-colors mr-1">
                    <ChevronLeft className="w-5 h-5" />
                  </button>
                ) : null}
                <History className="w-5 h-5 text-cyan-400" />
                <h2 className="text-lg font-bold text-white">
                  {selectedId ? t('Order Detail') : t('Order History')}
                </h2>
                {!selectedId && total > 0 && (
                  <span className="bg-cyan-500/20 text-cyan-400 text-xs font-bold px-2 py-0.5 rounded-full">{total}</span>
                )}
              </div>
              <button onClick={onClose} className="text-slate-400 hover:text-white transition-colors">
                <X className="w-5 h-5" />
              </button>
            </div>

            {/* Detail panel */}
            <AnimatePresence mode="wait">
              {selectedId ? (
                <motion.div key="detail" initial={{ opacity: 0, x: 20 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: 20 }}
                  className="flex-1 flex flex-col overflow-hidden">
                  <OrderDetailPanel txnId={selectedId} onBack={() => setSelectedId(null)} />
                </motion.div>
              ) : (
                <motion.div key="list" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
                  className="flex-1 flex flex-col overflow-hidden" data-tour="orders-list">

                  {/* Order list */}
                  <div className="flex-1 overflow-y-auto px-4 py-4 space-y-2">
                    {loading && (
                      <div className="flex items-center justify-center py-16">
                        <Loader2 className="w-8 h-8 text-cyan-400 animate-spin" />
                      </div>
                    )}

                    {!loading && orders.length === 0 && (
                      <div className="flex flex-col items-center gap-3 py-20 text-slate-500">
                        <Package className="w-12 h-12 opacity-20" />
                        <p className="text-sm">{t('No orders yet')}</p>
                        <p className="text-xs opacity-60">{t('Your orders will appear here')}</p>
                      </div>
                    )}

                    {!loading && orders.map(order => {
                      const sc = statusStyle(order.status);
                      return (
                        <button key={order.transaction_id}
                          onClick={() => setSelectedId(order.transaction_id)}
                          className="w-full text-left rounded-xl border border-white/[0.07] px-4 py-3 flex items-center gap-3 hover:bg-white/[0.04] active:scale-[0.99] transition-all"
                          style={{ background: 'rgba(255,255,255,0.025)' }}>
                          <div className={`w-9 h-9 rounded-xl flex items-center justify-center shrink-0 ${sc.bg}`}>
                            <Package className={`w-4 h-4 ${sc.text}`} />
                          </div>
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2 flex-wrap">
                              <p className="text-sm font-semibold text-white truncate">
                                {order.item_name || t('Order #{id}', { id: order.transaction_id.slice(0, 8) })}
                              </p>
                              <span className={`shrink-0 text-[10px] font-bold px-2 py-0.5 rounded-full border ${sc.bg} ${sc.text} ${sc.border}`}>
                                {txnStatusLabel(t, order.status)}
                              </span>
                            </div>
                            <div className="flex items-center gap-3 mt-0.5 flex-wrap">
                              {order.item_price > 0 && (
                                <span className="text-xs font-semibold text-emerald-300">
                                  {order.currency} {order.item_price.toLocaleString(locale)}
                                </span>
                              )}
                              <span className="text-[10px] text-slate-600">
                                <Clock className="inline w-2.5 h-2.5 mr-0.5" />{fmtOrderDate(order.created_at)}
                              </span>
                            </div>
                          </div>
                          <ChevronRight className="w-4 h-4 text-slate-600 shrink-0" />
                        </button>
                      );
                    })}
                  </div>

                  {/* Pagination */}
                  {totalPages > 1 && (
                    <div className="shrink-0 flex items-center justify-between px-5 py-3 border-t border-white/[0.06]">
                      <button onClick={() => load(page - 1)} disabled={page <= 1}
                        className="px-3 py-1.5 rounded-lg text-xs font-semibold text-slate-400 border border-white/[0.08] hover:text-white disabled:opacity-30 disabled:cursor-not-allowed transition-colors">
                        ← {t('Prev')}
                      </button>
                      <span className="text-xs text-slate-500">{t('Page {page} of {total}', { page, total: totalPages })}</span>
                      <button onClick={() => load(page + 1)} disabled={page >= totalPages}
                        className="px-3 py-1.5 rounded-lg text-xs font-semibold text-slate-400 border border-white/[0.08] hover:text-white disabled:opacity-30 disabled:cursor-not-allowed transition-colors">
                        {t('Next')} →
                      </button>
                    </div>
                  )}
                </motion.div>
              )}
            </AnimatePresence>
          </motion.aside>
        </>
      )}
    </AnimatePresence>
  );
}

/* ─────────────────────────── App ────────────────────────────────────────── */
/* ── Guided tour ────────────────────────────────────────────────────────── */
// The ordering part is hands-on: the buyer clicks the real buttons (Auto-fill
// does the typing) and ends up with a sample order placed with a seller.
const TOUR_DONE_KEY = 'bap.tourDone';
const statusIn = (...list) => ({ status }) => list.includes(status);
const PLACED = ['CONFIRMED', 'STATUS_SENT', 'CANCEL_SENT', 'CANCELLED', 'RATE_SENT', 'SUPPORT_SENT'];
const tourSteps = t => [
  { eyebrow: t('Welcome'), title: t('Shop the ION network'),
    body: <p>{t("This buyer app shows products from every seller on the ION network in one place. This tour shows how it works, and you'll place a sample order along the way. It takes about two minutes.")}</p> },
  { eyebrow: t('Search'), target: 'search', title: t('Search'),
    body: <p>{t('Search by product name. The search runs across every seller on the network.')}</p> },
  { eyebrow: t('Browse'), target: 'filters', title: t('Categories and sorting'),
    body: <p>{t('Narrow the list by category, or sort by price or name. Tap any product card to see its details, offer, and return and cancellation policies.')}</p> },

  { eyebrow: t('Order · {n} of 4', { n: 1 }), target: 'add-first', action: 'click', waitLabel: t('Click ADD'),
    skip: { label: t('Skip this part'), to: 'orders' }, title: t("Let's place an order"),
    body: <><p>{rich(t('Click <b>ADD</b> on the first product to put it in your cart.'))}</p><p className="text-slate-400">{t('No products? A seller has to publish a catalog first, from the BPP app.')}</p></> },
  { eyebrow: t('Order · {n} of 4', { n: 1 }), target: 'view-cart', until: s => s.isCartOpen, waitLabel: t('Click View Cart'),
    title: t('Open your cart'),
    body: <p>{rich(t('Your cart can hold products from different sellers. Click <b>View Cart</b>.'))}</p> },
  { eyebrow: t('Order · {n} of 4', { n: 2 }), target: 'checkout', until: s => s.cartCheckout, waitLabel: t('Click Checkout'),
    title: t('Check out'),
    body: <p>{rich(t('Click <b>Proceed to Checkout</b>. The app asks the seller for a <b>quote</b>: live price, stock, tax and delivery.'))}</p> },
  { eyebrow: t('Order · {n} of 4', { n: 2 }), target: 'checkout-stepper', until: statusIn('QUOTE_RECEIVED', 'INIT_SENT', 'INIT_RECEIVED', 'CONFIRM_SENT', ...PLACED),
    waitLabel: t('Waiting for the seller…'), title: t('Getting a quote'),
    body: <p>{t('The seller is working out your quote. This usually takes a second or two.')}</p> },
  { eyebrow: t('Order · {n} of 4', { n: 3 }), target: 'billing-autofill', action: 'click', waitLabel: t('Click Auto-fill'),
    title: t('Your details'),
    body: <p>{rich(t("The quote shows the seller's price, tax and delivery. Below it, enter who the order is for and where to deliver it. Click <b>Auto-fill</b> for a sample address."))}</p> },
  { eyebrow: t('Order · {n} of 4', { n: 3 }), target: 'init', until: statusIn('INIT_RECEIVED', 'CONFIRM_SENT', ...PLACED), waitLabel: t('Click Initialize'),
    title: t('Initialize the order'),
    body: <p>{rich(t("Click <b>Initialize Order</b>. The seller confirms the final terms and how you'll pay."))}</p> },
  { eyebrow: t('Order · {n} of 4', { n: 4 }), target: 'confirm', until: statusIn(...PLACED), waitLabel: t('Click Pay & Confirm'),
    title: t('Pay and confirm'),
    body: <><p>{rich(t('Click <b>Pay & Confirm Order</b> to place it.'))}</p><p className="text-slate-400">{t('The payment gateway holds your payment until the order is settled with the seller.')}</p></> },
  { eyebrow: t('Order placed'), target: 'order-placed', title: t('Order placed!'),
    body: <p>{t("The seller has your order. Its items are listed below. You can cancel the whole order, or just some items, if the seller's policy allows it.")}</p> },
  { eyebrow: t('Order placed'), target: 'order-actions', title: t('After you order'),
    body: <p>{rich(t('<b>Refresh Status</b> checks for updates, <b>Rate Order</b> tells the seller how it went, and <b>Get Support</b> raises a ticket with them.'))}</p> },
  { eyebrow: t('Order placed'), target: 'continue', action: 'click', waitLabel: t('Click Continue Shopping'),
    title: t('Back to the shop'),
    body: <p>{rich(t('Click <b>Continue Shopping</b>. Your order is saved in your order history.'))}</p> },

  { id: 'orders', eyebrow: t('Orders'), target: 'orders-button', closeDrawers: true, until: s => s.isOrdersOpen, waitLabel: t('Click Orders'),
    title: t('Your orders'),
    body: <p>{rich(t('Every order you place is kept here. Click <b>Orders</b>.'))}</p> },
  { eyebrow: t('Orders'), target: 'orders-list', title: t('Order history'),
    body: <p>{t("Tap an order to see its status, delivery progress and items. From there you can cancel, or return items once they're delivered, within the seller's return window.")}</p> },
  { eyebrow: t('Done'), target: 'tour-button', closeDrawers: true, title: t("You're all set"),
    body: <p>{t('Happy shopping! You can run this tour again any time from here, and switch between English and Bahasa Indonesia with EN | ID next to it.')}</p> },
];

export default function App() {
  const { t, locale, chosen: langChosen } = useI18n();
  const steps = useMemo(() => tourSteps(t), [t]);
  const [catalog, setCatalog] = useState([]);
  const [isSearching, setIsSearching] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [providerQuery, setProviderQuery] = useState('');
  const [cart, setCart] = useState([]);
  const [transaction, setTransaction] = useState(null);
  const [isLoading, setIsLoading] = useState(false);
  const [isOrdersOpen, setIsOrdersOpen] = useState(false);
  const [isCartOpen, setIsCartOpen] = useState(false);
  const [cartCheckout, setCartCheckout] = useState(false);
  const [selectedProduct, setSelectedProduct] = useState(null);
  const [error, setError] = useState(null);
  const [activeCategory, setActiveCategory] = useState('All');
  const [sortBy, setSortBy] = useState('default');
  const [billing, setBilling] = useState({
    name: '', email: '', phone: '',
    streetAddress: '', addressLocality: '', addressRegion: '', postalCode: '', addressCountry: 'IN',
  });
  const debounceRef = useRef(null);
  const [authReady, setAuthReady] = useState(false);
  const [tourOpen, setTourOpen] = useState(false);

  // Gate on a token before rendering anything real. There's no login UI
  // here at all -- landing-page is the single entry point (see
  // src/auth.js); a visit with no token, ever, redirects there.
  useEffect(() => {
    if (window.location.pathname === `${basePath}logout`) {
      clearToken(); // loaded in a hidden iframe from landing-page's logout button
      return;
    }
    captureTokenFromUrl();
    if (!getToken()) {
      redirectToLogin();
      return;
    }
    setAuthReady(true);
  }, []);

  /* ── Discover ─────────────────────────────────────────────────────────── */
  // A newer search must never let an older, slower one overwrite its
  // results once it lands -- the old request is aborted outright, and
  // if it was already in flight past that point, its AbortError is
  // ignored rather than clearing what the newer search just showed.
  const discoverAbortRef = useRef(null);
  const discover = useCallback((q = '', provider = '') => {
    discoverAbortRef.current?.abort();
    const controller = new AbortController();
    discoverAbortRef.current = controller;

    setIsSearching(true);
    setError(null);
    let path = `/discover`;
    const params = new URLSearchParams();
    if (q.trim()) params.append('q', q.trim());
    if (provider.trim()) params.append('provider_name', provider.trim());
    if (params.toString()) path += `?${params.toString()}`;

    apiFetch(path, { signal: controller.signal })
      .then(r => { if (!r.ok) throw new Error(`Status ${r.status}`); return r.json(); })
      .then(data => {
        // /api/v1/discover now returns the catalog result directly, whether
        // DISCOVER_METHOD routed it via DiscoverSync (a direct call to
        // discovery-service) or Discover (async via onix-bap, blocked on
        // internal/discoverwait until on_discover arrives) -- both are
        // synchronous from the browser's point of view, so no more polling
        // /discover/results/:message_id here.
        setCatalog(parseCatalogs(data));
        setActiveCategory('All');
        setIsSearching(false);
      })
      .catch(e => {
        if (e.name === 'AbortError') return; // superseded by a newer search
        setError(e.message);
        setCatalog([]);
        setIsSearching(false);
      });
  }, []);

  useEffect(() => { if (authReady) discover(); }, [authReady, discover]);

  /* ── Guided tour ──────────────────────────────────────────────────────── */
  // Opens by itself on a buyer's first visit; "Tour" in the header reruns it.
  // First visit: the tour starts once a language has been picked.
  useEffect(() => {
    if (!authReady || !langChosen) return;
    let done = false;
    try { done = localStorage.getItem(TOUR_DONE_KEY) === '1'; } catch { /* storage blocked */ }
    if (!done) setTourOpen(true);
  }, [authReady, langChosen]);
  const closeTour = useCallback(() => {
    setTourOpen(false);
    try { localStorage.setItem(TOUR_DONE_KEY, '1'); } catch { /* storage blocked */ }
  }, []);
  const onTourStep = useCallback(step => {
    if (step.closeDrawers) { setIsOrdersOpen(false); setIsCartOpen(false); setSelectedProduct(null); }
  }, []);

  const handleSearchChange = e => {
    const v = e.target.value;
    setSearchQuery(v);
    clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => discover(v, providerQuery), 500);
  };

  const handleProviderChange = e => {
    const v = e.target.value;
    setProviderQuery(v);
    clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => discover(searchQuery, v), 500);
  };

  /* ── Derived: categories + filtered + sorted catalog ─────────────────── */
  const categories = ['All', ...Array.from(new Set(catalog.map(p => p.category))).filter(Boolean)];

  const visibleCatalog = catalog
    .filter(p => activeCategory === 'All' || p.category === activeCategory)
    .filter(p => !providerQuery.trim() || p.sellerName.toLowerCase().includes(providerQuery.toLowerCase().trim()))
    .sort((a, b) => {
      if (sortBy === 'price_asc') return a.price - b.price;
      if (sortBy === 'price_desc') return b.price - a.price;
      if (sortBy === 'name') return a.name.localeCompare(b.name);
      return 0;
    });

  const myProviderId = getProviderId();
  const myResources = visibleCatalog.filter(p => p.providerId === myProviderId);
  const otherResources = visibleCatalog.filter(p => p.providerId !== myProviderId);

  const catalogSections = [
    { title: 'My Resources', icon: <User className="w-5 h-5 text-emerald-400" />, items: myResources },
    { title: 'All other resources', icon: <Package className="w-5 h-5 text-sky-400" />, items: otherResources }
  ];

  /* ── Cart ─────────────────────────────────────────────────────────────── */
  const updateQty = (id, delta) =>
    setCart(p => p.map(i => i.id === id ? { ...i, qty: i.qty + delta } : i).filter(i => i.qty > 0));

  const addToCart = p => setCart(prev => {
    const ex = prev.find(i => i.id === p.id);
    return ex ? prev.map(i => i.id === p.id ? { ...i, qty: i.qty + 1 } : i) : [...prev, { ...p, qty: 1 }];
  });

  const cartCount = cart.reduce((s, i) => s + i.qty, 0);
  const cartTotal = cart.reduce((s, i) => s + i.price * i.qty, 0);

  /* ── Transaction polling ─────────────────────────────────────────────── */
  useEffect(() => {
    const terminalStatuses = ['CONFIRMED', 'CANCELLED'];
    if (!transaction || terminalStatuses.includes(transaction.status)) return;
    const id = setInterval(() => {
      apiFetch(`/status/${transaction.id}`)
        .then(r => r.json())
        .then(d => {
          setTransaction(p => {
            // Don't revert CANCEL_SENT back to CONFIRMED — the cancel is still
            // in-flight. Only advance to CANCELLED or later states.
            if (p.status === 'CANCEL_SENT' && d.status === 'CONFIRMED') return p;
            return { ...p, ...d };
          });
        })
        .catch(() => { });
    }, 2000);
    return () => clearInterval(id);
  }, [transaction]);

  /* ── Beckn: Select (single product from modal) ───────────────────────── */
  const handleSelectProduct = async (product) => {
    setIsLoading(true);
    try {
      const r = await apiFetch(`/select`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          items: [{
            resource_id: product.id,
            offer_id: product.offerId,
            offer_name: product.offerName,
            quantity: 1,
            provider_id: product.providerId,
            provider_name: product.sellerName,
            offer_attributes: product.offerAttr || {},
            offer_considerations: product.offerConsiderations || [],
            resource: product.resourceDetail,
            bpp_id: product.bppId || '',
            bpp_uri: product.bppUri || '',
          }],
        }),
      });
      if (!r.ok) throw new Error(await r.text());
      const d = await r.json();
      setTransaction({ id: d.transaction_id, status: 'SELECT_SENT', product });
    } catch (e) { alert(t('Select failed: {error}', { error: e.message })); }
    finally { setIsLoading(false); }
  };

  /* ── Beckn: Select (from cart) ───────────────────────────────────────── */
  const handleSelectCart = async () => {
    setIsLoading(true);
    try {
      const r = await apiFetch(`/select`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          items: cart.map(i => ({
            resource_id: i.id,
            offer_id: i.offerId || '',
            offer_name: i.offerName || i.name,
            quantity: i.qty,
            provider_id: i.providerId || '',
            provider_name: i.sellerName || '',
            offer_attributes: i.offerAttr || {},
            offer_considerations: i.offerConsiderations || [],
            resource: i.resourceDetail,
            bpp_id: i.bppId || '',
            bpp_uri: i.bppUri || '',
          })),
        }),
      });
      if (!r.ok) throw new Error(await r.text());
      const d = await r.json();
      setTransaction({ id: d.transaction_id, status: 'SELECT_SENT' });
      setCartCheckout(true);
      setIsCartOpen(true);
    } catch (e) { alert(t('Select failed: {error}', { error: e.message })); }
    finally { setIsLoading(false); }
  };

  /* ── Beckn: Init ─────────────────────────────────────────────────────── */
  const handleInit = async () => {
    if (!transaction) return;
    setIsLoading(true);
    try {
      const r = await apiFetch(`/init`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          transaction_id: transaction.id,
          billing: {
            name: billing.name || 'Customer',
            email: billing.email || '',
            phone: billing.phone || '9999999999',
            streetAddress: billing.streetAddress || 'TBD',
            addressLocality: billing.addressLocality || 'TBD',
            addressRegion: billing.addressRegion || 'TBD',
            postalCode: billing.postalCode || '000000',
            addressCountry: billing.addressCountry || 'IN',
          },
        }),
      });
      if (!r.ok) {
        const err = await r.json().catch(() => ({}));
        // 409: the seller can no longer supply this order.
        throw new Error(r.status === 409 ? t('Some items are no longer available from the seller.') : (err.error || `HTTP ${r.status}`));
      }
      setTransaction(p => ({ ...p, status: 'INIT_SENT' }));
    } catch (e) { alert(t('Init failed: {error}', { error: e.message })); }
    finally { setIsLoading(false); }
  };

  /* ── Beckn: Confirm ──────────────────────────────────────────────────── */
  const handleConfirm = async () => {
    if (!transaction) return;
    setIsLoading(true);
    try {
      await apiFetch(`/confirm`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ transaction_id: transaction.id }),
      });
      setTransaction(p => ({ ...p, status: 'CONFIRM_SENT' }));
    } catch (e) { alert(t('Confirm failed: {error}', { error: e.message })); }
    finally { setIsLoading(false); }
  };

  /* ── Beckn: Request Status ───────────────────────────────────────────── */
  const handleRequestStatus = async () => {
    if (!transaction) return;
    try {
      await apiFetch(`/request-status`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ transaction_id: transaction.id }),
      });
      setTransaction(p => ({ ...p, status: 'STATUS_SENT' }));
    } catch (e) { alert(t('Status request failed: {error}', { error: e.message })); }
  };

  /* ── Beckn: Cancel ───────────────────────────────────────────────────── */
  // Cancellations run from the order's items card (LiveOrderItems); keep the
  // top-level transaction status in step with what it reports.
  const handleStatusChange = useCallback(status => {
    setTransaction(p => (p && p.status !== status ? { ...p, status } : p));
  }, []);

  /* ── Beckn: Rate ─────────────────────────────────────────────────────── */
  const handleRate = async (rating, feedback) => {
    if (!transaction) return;
    setIsLoading(true);
    try {
      const ratingInput = {
        id: transaction.id,
        ratingCategory: 'order',
        range: { min: 1, max: 5, value: rating },
      };
      if (feedback && feedback.trim()) {
        ratingInput.feedbackFormSubmission = { data: { comment: feedback.trim() } };
      }
      const r = await apiFetch(`/rate`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          transaction_id: transaction.id,
          ratingInputs: [ratingInput],
        }),
      });
      if (!r.ok) {
        const err = await r.json().catch(() => ({ error: r.statusText }));
        throw new Error(err.error || t('Rating submission failed'));
      }
      setTransaction(p => ({ ...p, rated: rating }));
    } catch (e) { alert(t('Rating failed: {error}', { error: e.message })); }
    finally { setIsLoading(false); }
  };

  /* ── Beckn: Support ──────────────────────────────────────────────────── */
  const handleSupport = async (description) => {
    if (!transaction) return;
    setIsLoading(true);
    try {
      const r = await apiFetch(`/support`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          transaction_id: transaction.id,
          orderId: transaction.id,
          description,
        }),
      });
      const d = await r.json();
      setTransaction(p => ({ ...p, supportTicketStatus: d.status || 'SUPPORT_SENT' }));
      alert(t('Support ticket created! The seller will get back to you.'));
    } catch (e) { alert(t('Support request failed: {error}', { error: e.message })); }
    finally { setIsLoading(false); }
  };

  /* ── Drop items the seller no longer has, back to the cart ─────────── */
  const removeUnavailable = ids => {
    setCart(prev => prev.filter(i => !ids.includes(i.id)));
    setTransaction(null);
    setCartCheckout(false);
  };

  /* ── Reset cart checkout back to browsing ─────────────────────────────── */
  const resetCart = () => {
    setCartCheckout(false);
    setTransaction(null);
    setCart([]);
    setBilling({ name: '', email: '', phone: '', streetAddress: '', addressLocality: '', addressRegion: '', postalCode: '', addressCountry: 'IN' });
    setIsCartOpen(false);
  };

  /* ── Render ───────────────────────────────────────────────────────────── */
  if (!authReady) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <p className="text-slate-400 text-sm">{t('Redirecting to login…')}</p>
      </div>
    );
  }

  return (
    <div className="min-h-screen">

      {/* ── Nav ──────────────────────────────────────────────────────────── */}
      <header className="sticky top-0 z-30 glass border-b border-white/[0.06]">
        {/* Phones: the search box gets its own row under the logo and buttons.
            Button labels show from xl up; below that the buttons are icons. */}
        <div className="max-w-7xl mx-auto px-4 sm:px-6 py-2.5 sm:py-0 sm:h-16 flex flex-wrap sm:flex-nowrap items-center gap-2 sm:gap-4">
          <a href="https://ion.id" target="_blank" rel="noopener noreferrer" className="shrink-0">
            <span className="text-xl font-bold text-white tracking-tight">
              ION <span className="hidden xl:inline font-normal text-slate-400">{t('Reference App')}</span>
            </span>
          </a>

          {/* Phones: the language switch sits beside the search box. */}
          <div className="order-last sm:order-none basis-full sm:basis-auto flex-1 max-w-2xl mx-auto flex items-center gap-2">
          <form data-tour="search" onSubmit={e => { e.preventDefault(); clearTimeout(debounceRef.current); discover(searchQuery, providerQuery); }} className="flex-1 min-w-0 flex gap-2">
            <div className="search-glow relative flex-1 flex items-center bg-white/[0.06] border border-white/10 rounded-xl transition-all duration-300">
              {isSearching
                ? <Loader2 className="absolute left-3 w-4 h-4 text-emerald-400 animate-spin" />
                : <Search className="absolute left-3 w-4 h-4 text-slate-400" />
              }
              <input
                type="text" value={searchQuery} onChange={handleSearchChange}
                placeholder={t('Search products on the ION network…')}
                className="w-full bg-transparent pl-9 pr-4 py-2.5 text-sm text-white placeholder-slate-500 outline-none"
              />
              {searchQuery && (
                <button type="button" onClick={() => { setSearchQuery(''); discover('', providerQuery); }}
                  className="absolute right-3 text-slate-400 hover:text-white transition-colors">
                  <X className="w-4 h-4" />
                </button>
              )}
            </div>
            <div className="search-glow relative w-48 flex items-center bg-white/[0.06] border border-white/10 rounded-xl transition-all duration-300 hidden sm:flex">
              <Store className="absolute left-3 w-4 h-4 text-slate-400" />
              <input
                type="text" value={providerQuery} onChange={handleProviderChange}
                placeholder="Provider name…"
                className="w-full bg-transparent pl-9 pr-4 py-2.5 text-sm text-white placeholder-slate-500 outline-none"
              />
              {providerQuery && (
                <button type="button" onClick={() => { setProviderQuery(''); discover(searchQuery, ''); }}
                  className="absolute right-3 text-slate-400 hover:text-white transition-colors">
                  <X className="w-4 h-4" />
                </button>
              )}
            </div>
          </form>
          <span className="sm:hidden"><LanguageToggle /></span>
          </div>

          <a
            href={`${window.__env__?.BPP_ADMIN_URL || import.meta.env.VITE_BPP_ADMIN_URL || "#"}?token=${encodeURIComponent(getToken() || '')}`}
            target="_blank"
            rel="noopener noreferrer" title={t('BPP APP')}
            className="shrink-0 ml-auto sm:ml-0 flex items-center gap-2 px-3 py-2 rounded-xl transition-all text-sm font-medium text-emerald-400 hover:text-emerald-300 border border-emerald-500/20 hover:border-emerald-500/40 bg-emerald-500/10"
          >
            <ExternalLink className="w-4 h-4" />
            <span className="hidden xl:block">{t('BPP APP')}</span>
          </a>

          <button
            onClick={() => setTourOpen(true)} data-tour="tour-button" title={t('Take the tour')}
            className="shrink-0 flex items-center gap-2 px-3 py-2 rounded-xl transition-all text-sm font-medium text-cyan-300 hover:text-cyan-200 border border-cyan-500/20 hover:border-cyan-500/40 bg-cyan-500/10"
          >
            <Compass className="w-4 h-4" />
            <span className="hidden xl:block">{t('Tour')}</span>
          </button>

          <span className="hidden sm:inline-flex"><LanguageToggle /></span>

          <button
            onClick={logout} title={t('Log out')}
            className="shrink-0 flex items-center gap-2 px-3 py-2 rounded-xl transition-all text-sm font-medium text-rose-400 hover:text-rose-300 border border-rose-500/20 hover:border-rose-500/40 bg-rose-500/10"
          >
            <LogOut className="w-4 h-4" />
            <span className="hidden xl:block">{t('Log out')}</span>
          </button>

          <button
            onClick={() => setIsOrdersOpen(true)} data-tour="orders-button" title={t('Orders')}
            className="shrink-0 flex items-center gap-2 px-3 py-2 rounded-xl transition-all text-sm font-medium text-slate-400 hover:text-white border border-white/[0.08] hover:border-white/[0.15]"
          >
            <History className="w-4 h-4" />
            <span className="hidden xl:block">{t('Orders')}</span>
          </button>

          <button
            onClick={() => setIsCartOpen(true)} title={t('Cart')}
            className="relative shrink-0 flex items-center gap-2 px-4 py-2 rounded-xl transition-all text-sm font-medium text-white"
            style={{ background: 'rgba(0,184,230,0.15)', border: '1px solid rgba(0,184,230,0.3)' }}
          >
            <ShoppingCart className="w-4 h-4" />
            <span className="hidden xl:block">{t('Cart')}</span>
            {cartCount > 0 && (
              <span className="absolute -top-1.5 -right-1.5 w-5 h-5 bg-rose-500 text-white text-[10px] font-bold rounded-full flex items-center justify-center">
                {cartCount}
              </span>
            )}
          </button>
        </div>
      </header>

      <div className="bg-orange-500 text-white text-xs sm:text-sm text-center py-1.5 px-4">
        {t('[FOR DEMO PURPOSE ONLY] - This is a sample application for reference and testing')}
      </div>

      {/* ── Category chips + sort ─────────────────────────────────────────── */}
      {catalog.length > 0 && (
        <div className="sticky top-[6.75rem] sm:top-16 z-20 bg-[#080d1e]/90 backdrop-blur border-b border-white/[0.05]" data-tour="filters">
          <div className="max-w-7xl mx-auto px-4 sm:px-6 py-3 flex items-center gap-3">
            {/* Scrollable category chips */}
            <div className="flex gap-2 overflow-x-auto flex-1 scrollbar-none pb-0.5">
              {categories.map(cat => (
                <button
                  key={cat}
                  onClick={() => setActiveCategory(cat)}
                  className={`shrink-0 px-3.5 py-1.5 rounded-full text-xs font-semibold transition-all whitespace-nowrap
                    ${activeCategory === cat
                      ? 'text-white shadow-sm'
                      : 'bg-white/[0.06] text-slate-400 hover:text-slate-200 border border-white/[0.08]'
                    }`}
                  style={activeCategory === cat ? { background: 'linear-gradient(135deg,#00b8e6,#1e2fa0)', border: 'none' } : {}}
                >
                  {cat === 'All' ? t('All') : cat}
                </button>
              ))}
            </div>
            {/* Sort dropdown */}
            <select
              value={sortBy}
              onChange={e => setSortBy(e.target.value)}
              className="shrink-0 bg-slate-900 border border-white/[0.08] text-slate-300 text-xs rounded-xl px-3 py-1.5 outline-none focus:border-white/20 cursor-pointer"
            >
              <option value="default">{t('Relevance')}</option>
              <option value="price_asc">{t('Price: Low → High')}</option>
              <option value="price_desc">{t('Price: High → Low')}</option>
              <option value="name">{t('Name A–Z')}</option>
            </select>
          </div>
        </div>
      )}

      {/* ── Page header ──────────────────────────────────────────────────── */}
      <div className="max-w-7xl mx-auto px-4 sm:px-6 pt-8 pb-5">
        <div className="flex items-end justify-between gap-4 flex-wrap">
          <div>
            <p className="text-xs uppercase tracking-widest font-semibold mb-1" style={{ color: '#00b8e6' }}>
              {t('Remiges Retail · ION Network')}
            </p>
            <h1 className="text-3xl sm:text-4xl gradient-text">
              {searchQuery ? t('Results for "{q}"', { q: searchQuery }) : activeCategory !== 'All' ? activeCategory : t('Discover Products')}
            </h1>
          </div>
          {visibleCatalog.length > 0 && (
            <span className="text-sm text-slate-500">{visibleCatalog.length === 1 ? t('1 product') : t('{count} products', { count: visibleCatalog.length })}</span>
          )}
        </div>
      </div>

      {/* ── Catalog ──────────────────────────────────────────────────────── */}
      <main className="max-w-7xl mx-auto px-4 sm:px-6 pb-36">

        {isSearching && catalog.length === 0 && (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-5">
            {Array.from({ length: 8 }).map((_, i) => <SkeletonCard key={i} />)}
          </div>
        )}

        {error && !isSearching && (
          <div className="flex flex-col items-center justify-center py-28 gap-4 text-slate-400">
            <Package className="w-14 h-14 opacity-20" />
            <p className="font-medium">{t('Could not reach the network.')}</p>
            <p className="text-sm opacity-60">{t('Check your connection and try again.')}</p>
            <button onClick={() => discover(searchQuery, providerQuery)}
              className="mt-1 px-5 py-2 rounded-xl text-sm font-semibold text-white transition-all"
              style={{ background: 'linear-gradient(135deg,#00b8e6,#1e2fa0)' }}>
              {t('Retry')}
            </button>
          </div>
        )}

        {!isSearching && !error && visibleCatalog.length === 0 && (
          <div className="flex flex-col items-center justify-center py-28 gap-3 text-slate-400">
            <div className="w-20 h-20 rounded-full bg-white/[0.04] flex items-center justify-center mb-2">
              <Search className="w-9 h-9 opacity-30" />
            </div>
            <p className="font-semibold text-slate-300">{t('No products found')}</p>
            <p className="text-sm opacity-60">{searchQuery ? t('No results for "{q}"', { q: searchQuery }) : t('Try selecting a different category.')}</p>
            {activeCategory !== 'All' && (
              <button onClick={() => setActiveCategory('All')}
                className="mt-2 text-sm font-semibold px-4 py-1.5 rounded-full border border-white/10 text-slate-300 hover:border-white/20 transition-all">
                {t('Clear filter')}
              </button>
            )}
          </div>
        )}

        {visibleCatalog.length > 0 && catalogSections.map((section, sIdx) => (
          <div key={sIdx} className={`rounded-3xl p-6 sm:p-8 border border-white/[0.05] bg-white/[0.02] ${sIdx > 0 ? "mt-8" : ""}`}>
            {section.title && (
              <h2 className="text-xl font-bold text-white mb-6 flex items-center gap-3">
                <div className="p-2 rounded-xl bg-white/[0.04]">
                  {section.icon}
                </div>
                {section.title}
              </h2>
            )}
            {section.items.length === 0 ? (
              <div className="flex flex-col items-center justify-center py-10 bg-white/[0.01] rounded-2xl border border-dashed border-white/10">
                <p className="text-slate-400 text-sm">{t('No resources found in this section.')}</p>
              </div>
            ) : (
              <motion.div layout className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-5">
              <AnimatePresence>
                {section.items.map((product, i) => {
                  const inCart = cart.find(c => c.id === product.id);
                  const cardKey = `${product.bppId}::${product.id}`;
                  return (
                    <motion.div
                      key={cardKey} layout
                      initial={{ opacity: 0, y: 20 }} animate={{ opacity: 1, y: 0 }}
                      exit={{ opacity: 0, scale: 0.95 }}
                      transition={{ duration: 0.22, delay: i * 0.025 }}
                      className="product-card glass rounded-2xl overflow-hidden flex flex-col cursor-pointer group"
                      onClick={() => setSelectedProduct(product)}
                    >
                      {/* Image */}
                      <div className="relative h-44 overflow-hidden bg-slate-800/60 shrink-0">
                        <img
                          src={product.image} alt={product.name}
                          className="w-full h-full object-cover opacity-85 group-hover:opacity-100 group-hover:scale-105 transition-all duration-500"
                          onError={e => { e.target.src = FALLBACK_IMG; }}
                        />
                        <div className="absolute inset-0 bg-gradient-to-t from-black/60 via-transparent" />
                        {/* Category badge */}
                        <span className="absolute top-2.5 left-2.5 bg-black/55 backdrop-blur-md text-[10px] font-bold uppercase tracking-widest px-2.5 py-1 rounded-full border border-white/10 text-slate-300">
                          {product.category.length > 18 ? product.category.slice(0, 18) + '…' : product.category}
                        </span>
                        {/* COD badge */}
                        {product.codAvailable && (
                          <span className="absolute top-2.5 right-2.5 bg-amber-500/90 text-[9px] font-bold uppercase tracking-widest px-2 py-0.5 rounded-full text-white">
                            {t('COD')}
                          </span>
                        )}
                        {/* Offer validity */}
                        {product.offerValidity && (
                          <span className="absolute bottom-2.5 left-2.5 text-[10px] text-slate-300 bg-black/50 backdrop-blur px-2 py-0.5 rounded-full flex items-center gap-1">
                            <Calendar className="w-2.5 h-2.5" /> {t('Till {date}', { date: fmtDay(product.offerValidity) })}
                          </span>
                        )}
                      </div>

                      {/* Body */}
                      <div className="p-3.5 flex flex-col flex-grow gap-1.5">
                        <h3 className="text-sm font-semibold leading-snug line-clamp-2">{product.name}</h3>
                        {product.shortDesc && (
                          <p className="text-xs text-slate-400 line-clamp-2 leading-relaxed">{product.shortDesc}</p>
                        )}

                        {/* Seller + delivery info */}
                        <div className="flex flex-col gap-1 mt-0.5">
                          {product.sellerName && (
                            <p className="text-[11px] text-slate-500 flex items-center gap-1 truncate">
                              <Store className="w-3 h-3 shrink-0" /> {product.sellerName}
                            </p>
                          )}
                          <div className="flex gap-1.5 flex-wrap">
                            {product.deliveryRange && (
                              <span className="text-[10px] text-blue-400 flex items-center gap-0.5">
                                <MapPin className="w-2.5 h-2.5" />{product.deliveryRange}
                              </span>
                            )}
                            {product.deliveryHours && (
                              <span className="text-[10px] text-slate-500 flex items-center gap-0.5">
                                <Clock className="w-2.5 h-2.5" />{product.deliveryHours}
                              </span>
                            )}
                          </div>
                        </div>

                        {/* Price + cart controls */}
                        <div className="mt-auto pt-2.5 flex items-center justify-between gap-2 border-t border-white/[0.05]">
                          <span className="text-base font-bold">{fmt(product.price, product.currency)}</span>

                          {inCart ? (
                            <div
                              onClick={e => e.stopPropagation()}
                              className="flex items-center gap-0 rounded-xl overflow-hidden border"
                              style={{ borderColor: 'rgba(0,184,230,0.3)', background: 'rgba(0,184,230,0.1)' }}
                            >
                              <button onClick={() => updateQty(product.id, -1)}
                                className="w-8 h-8 flex items-center justify-center hover:bg-white/10 transition-colors"
                                style={{ color: '#00b8e6' }}>
                                <Minus className="w-3 h-3" />
                              </button>
                              <span className="w-7 text-center text-sm font-bold" style={{ color: '#00b8e6' }}>{inCart.qty}</span>
                              <button onClick={() => updateQty(product.id, 1)}
                                className="w-8 h-8 flex items-center justify-center hover:bg-white/10 transition-colors"
                                style={{ color: '#00b8e6' }}>
                                <Plus className="w-3 h-3" />
                              </button>
                            </div>
                          ) : (
                            <button data-tour={sIdx === 0 && i === 0 ? 'add-first' : undefined}
                              onClick={e => { e.stopPropagation(); addToCart(product); }}
                              className="flex items-center gap-1 px-3.5 py-1.5 rounded-xl text-xs font-bold text-white transition-all active:scale-95 shadow"
                              style={{ background: 'linear-gradient(135deg,#00b8e6,#1e2fa0)' }}
                            >
                              <Plus className="w-3.5 h-3.5" /> {t('ADD')}
                            </button>
                          )}
                        </div>
                      </div>
                    </motion.div>
                  );
                })}
              </AnimatePresence>
              </motion.div>
            )}
          </div>
        ))}
      </main>

      {/* ── Sticky cart footer (Swiggy-style) ────────────────────────────── */}
      <AnimatePresence>
        {cart.length > 0 && !isCartOpen && (
          <motion.div
            initial={{ y: 100, opacity: 0 }}
            animate={{ y: 0, opacity: 1 }}
            exit={{ y: 100, opacity: 0 }}
            transition={{ type: 'spring', damping: 28, stiffness: 300 }}
            className="fixed bottom-0 inset-x-0 z-30 px-4 pb-4 sm:px-6"
          >
            <button data-tour="view-cart"
              onClick={() => { setIsCartOpen(true); setCartCheckout(false); }}
              className="w-full max-w-lg mx-auto flex items-center justify-between px-5 py-4 rounded-2xl text-white shadow-2xl transition-all active:scale-[0.99]"
              style={{ background: 'linear-gradient(135deg,#00b8e6 0%,#1e2fa0 100%)' }}
            >
              <div className="flex items-center gap-3">
                <div className="w-7 h-7 rounded-lg bg-white/20 flex items-center justify-center text-xs font-bold">
                  {cartCount}
                </div>
                <div className="text-left">
                  <p className="text-xs font-medium opacity-80">{cartCount === 1 ? t('1 item added') : t('{count} items added', { count: cartCount })}</p>
                </div>
              </div>
              <div className="flex items-center gap-2 font-bold">
                <span>{fmt(cartTotal, 'IDR')}</span>
                <div className="flex items-center gap-1 bg-white/15 px-3 py-1.5 rounded-xl text-sm">
                  {t('View Cart')} <ChevronRight className="w-4 h-4" />
                </div>
              </div>
            </button>
          </motion.div>
        )}
      </AnimatePresence>

      {/* ── Product Detail / Quote Modal ─────────────────────────────────── */}
      <AnimatePresence>
        {selectedProduct && (
          <ProductModal
            product={selectedProduct}
            transaction={transaction}
            onClose={() => setSelectedProduct(null)}
            onSelect={handleSelectProduct}
            onInit={handleInit}
            onConfirm={handleConfirm}
            isLoading={isLoading}
          />
        )}
      </AnimatePresence>

      {/* ── Order History Drawer ─────────────────────────────────────────── */}
      <OrderHistoryDrawer open={isOrdersOpen} onClose={() => setIsOrdersOpen(false)} />

      {/* ── Cart Drawer ───────────────────────────────────────────────────── */}
      <AnimatePresence>
        {isCartOpen && (
          <>
            <motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
              className="fixed inset-0 bg-black/60 backdrop-blur-sm z-40"
              onClick={() => { if (!cartCheckout) setIsCartOpen(false); }} />
            <motion.aside
              initial={{ x: '100%' }} animate={{ x: 0 }} exit={{ x: '100%' }}
              transition={{ type: 'spring', damping: 30, stiffness: 300 }}
              className="fixed right-0 top-0 h-full w-full max-w-sm glass z-50 flex flex-col shadow-2xl"
            >
              {/* ── Drawer header ─────────────────────────────────── */}
              <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.07] shrink-0">
                <div className="flex items-center gap-2">
                  {cartCheckout && transaction?.status !== 'CONFIRMED' && (
                    <button onClick={() => { setCartCheckout(false); setTransaction(null); }}
                      className="text-slate-400 hover:text-white transition-colors mr-1">
                      <ChevronLeft className="w-5 h-5" />
                    </button>
                  )}
                  {cartCheckout
                    ? <><Receipt className="w-5 h-5 text-emerald-400" /><h2 className="text-lg">{t('Checkout')}</h2></>
                    : <><ShoppingCart className="w-5 h-5 text-emerald-400" /><h2 className="text-lg">{t('Your Cart')}</h2>
                      {cartCount > 0 && <span className="bg-emerald-500/20 text-emerald-400 text-xs font-bold px-2 py-0.5 rounded-full">{cartCount}</span>}</>
                  }
                </div>
                {!cartCheckout && (
                  <button onClick={() => setIsCartOpen(false)} className="text-slate-400 hover:text-white transition-colors">
                    <X className="w-5 h-5" />
                  </button>
                )}
              </div>

              {/* ── CART MODE: item list ──────────────────────────── */}
              {!cartCheckout && (
                <>
                  <div className="flex-grow overflow-y-auto px-5 py-4 space-y-3">
                    {cart.length === 0 ? (
                      <div className="h-full flex flex-col items-center justify-center text-slate-500 gap-3 py-16">
                        <ShoppingCart className="w-12 h-12 opacity-20" />
                        <p className="text-sm">{t('Your cart is empty')}</p>
                        <p className="text-xs opacity-60">{t('Add products from the shop to see them here')}</p>
                      </div>
                    ) : cart.map(item => {
                      const shortAddr = item.sellerAddress
                        ? [item.sellerAddress.streetAddress, item.sellerAddress.addressLocality, item.sellerAddress.addressRegion].filter(Boolean).join(', ')
                        : '';
                      return (
                        <div key={item.id} className="bg-white/[0.03] border border-white/[0.06] rounded-xl overflow-hidden">
                          {/* Hero image */}
                          <div className="relative h-36 bg-slate-800/80 overflow-hidden">
                            <img src={item.image} alt={item.name}
                              className="w-full h-full object-cover opacity-90"
                              onError={e => { e.target.src = FALLBACK_IMG; }} />
                            <div className="absolute inset-0 bg-gradient-to-t from-black/80 via-black/10" />
                            <div className="absolute bottom-0 left-0 right-0 px-3 pb-3">
                              <p className="font-bold text-white text-sm leading-snug">{item.name}</p>
                              {item.shortDesc && <p className="text-[11px] text-slate-300 mt-0.5 line-clamp-1">{item.shortDesc}</p>}
                            </div>
                          </div>

                          <div className="p-3 space-y-2.5">
                            {/* Offer name + validity */}
                            {(item.offerName || item.offerValidity) && (
                              <div className="flex items-center gap-2 text-[11px]">
                                {item.offerName && (
                                  <span className="bg-indigo-500/15 text-indigo-300 border border-indigo-500/25 px-2 py-0.5 rounded-full font-semibold">
                                    {item.offerName}
                                  </span>
                                )}
                                {item.offerValidity && (
                                  <span className="text-slate-500 flex items-center gap-1 ml-auto">
                                    <Calendar className="w-3 h-3" /> {t('Valid till {date}', { date: fmtDay(item.offerValidity) })}
                                  </span>
                                )}
                              </div>
                            )}

                            {/* Price row */}
                            <div className="flex items-center justify-between">
                              <span className="text-lg font-bold text-emerald-300">{fmt(item.price, item.currency)}</span>
                              <div className="flex items-center gap-1">
                                <button onClick={() => updateQty(item.id, -1)} className="w-7 h-7 flex items-center justify-center rounded-lg bg-white/[0.06] hover:bg-white/10 transition-colors"><Minus className="w-3 h-3" /></button>
                                <span className="w-7 text-center text-sm font-bold">{item.qty}</span>
                                <button onClick={() => updateQty(item.id, 1)} className="w-7 h-7 flex items-center justify-center rounded-lg bg-white/[0.06] hover:bg-white/10 transition-colors"><Plus className="w-3 h-3" /></button>
                              </div>
                            </div>

                            {/* Seller */}
                            {item.sellerName && (
                              <div className="flex items-start gap-2 text-[11px] text-slate-400">
                                <Store className="w-3.5 h-3.5 mt-0.5 shrink-0 text-slate-500" />
                                <span>
                                  <span className="font-medium text-slate-300">{item.sellerName}</span>
                                  {shortAddr && <span className="text-slate-500 ml-1">· {shortAddr}</span>}
                                </span>
                              </div>
                            )}

                            {/* Delivery badges */}
                            {(item.deliveryRange || item.deliveryHours || item.codAvailable || item.returnPolicy?.allowed || item.cancelPolicy?.allowed) && (
                              <div className="flex flex-wrap gap-1.5">
                                {item.deliveryRange && (
                                  <span className="flex items-center gap-1 text-[10px] font-semibold bg-blue-500/10 text-blue-400 border border-blue-500/20 px-2 py-0.5 rounded-full">
                                    <MapPin className="w-2.5 h-2.5" /> {t('Delivers within {range}', { range: item.deliveryRange })}
                                  </span>
                                )}
                                {item.deliveryHours && (
                                  <span className="flex items-center gap-1 text-[10px] font-semibold bg-white/5 text-slate-400 border border-white/10 px-2 py-0.5 rounded-full">
                                    <Clock className="w-2.5 h-2.5" /> {item.deliveryHours}
                                  </span>
                                )}
                                {item.codAvailable && (
                                  <span className="flex items-center gap-1 text-[10px] font-semibold bg-amber-500/10 text-amber-400 border border-amber-500/20 px-2 py-0.5 rounded-full">
                                    <CreditCard className="w-2.5 h-2.5" /> {t('Cash on Delivery')}
                                  </span>
                                )}
                                {item.returnPolicy?.allowed && (
                                  <span className="flex items-center gap-1 text-[10px] font-semibold bg-purple-500/10 text-purple-400 border border-purple-500/20 px-2 py-0.5 rounded-full">
                                    <RotateCcw className="w-2.5 h-2.5" /> {t('{days}-day returns', { days: isoDays(item.returnPolicy.window) })}
                                  </span>
                                )}
                                {item.cancelPolicy?.allowed && (
                                  <span className="flex items-center gap-1 text-[10px] font-semibold bg-rose-500/10 text-rose-400 border border-rose-500/20 px-2 py-0.5 rounded-full">
                                    <X className="w-2.5 h-2.5" /> {t('Free cancellation')}
                                  </span>
                                )}
                              </div>
                            )}
                          </div>
                        </div>
                      );
                    })}
                  </div>

                  {cart.length > 0 && (
                    <div className="px-5 py-4 border-t border-white/[0.07] space-y-3 shrink-0">
                      <div className="flex items-center justify-between">
                        <span className="text-slate-400 text-sm">{itemCount(t, cartCount)}</span>
                        <span className="text-xl font-bold">{fmt(cartTotal, 'IDR')}</span>
                      </div>
                      <button onClick={handleSelectCart} disabled={isLoading} data-tour="checkout"
                        className="w-full bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 text-white py-3.5 rounded-xl font-bold text-sm flex items-center justify-center gap-2 transition-all active:scale-[0.98] shadow-lg shadow-emerald-600/30">
                        {isLoading ? <Loader2 className="w-4 h-4 animate-spin" /> : <><span>{t('Proceed to Checkout')}</span><ChevronRight className="w-4 h-4" /></>}
                      </button>
                    </div>
                  )}
                </>
              )}

              {/* ── CHECKOUT MODE ─────────────────────────────────── */}
              {cartCheckout && (
                <CheckoutFlow
                  cart={cart} cartTotal={cartTotal}
                  transaction={transaction}
                  billing={billing} setBilling={setBilling}
                  isLoading={isLoading}
                  onInit={handleInit}
                  onConfirm={handleConfirm}
                  onDone={resetCart}
                  onStatusChange={handleStatusChange}
                  onRate={handleRate}
                  onSupport={handleSupport}
                  onRequestStatus={handleRequestStatus}
                  onRemoveItems={removeUnavailable}
                />
              )}
            </motion.aside>
          </>
        )}
      </AnimatePresence>

      <Tour steps={steps} open={tourOpen} onClose={closeTour} onStep={onTourStep}
        state={{ cartCount, isCartOpen, cartCheckout, isOrdersOpen, status: transaction?.status }} />
    </div>
  );
}
