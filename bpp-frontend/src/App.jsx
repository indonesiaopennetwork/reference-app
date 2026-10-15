import { useState, useEffect, useCallback, useMemo, Fragment } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import {
  LayoutDashboard, ShoppingBag, Package, Radio, MessageSquare,
  ChevronRight, TrendingUp, Clock, CheckCircle2, AlertCircle,
  RefreshCw, X, ChevronLeft, ChevronRight as ChevronRightIcon,
  Upload, Plus, Trash2, Eye, ArrowUpRight, Filter, Search,
  Activity, Zap, Store, Calendar, Tag, BarChart2, Inbox,
  ArrowRight, Circle, Star, Ticket, Ban, Wand2,
  BookOpen, FolderOpen, Send, ArrowLeft, ExternalLink,
  ChevronDown, Copy, Check, Scale, Pencil, Compass, Lock,
  PauseCircle, PlayCircle,
} from 'lucide-react'
import { captureTokenFromUrl, getToken, getIdToken, clearToken, redirectToLogin, basePath } from './auth.js'
import Tour from './Tour.jsx'
import { useReconSelection, inDateRange } from './reconSelection.js'
import UserProfileMenu from './UserProfileMenu.jsx'
import { useI18n, rich, LanguageToggle, tNow, localeNow } from './i18n.jsx'

const API_BASE = `${basePath}api/v1`
const BRAND = 'linear-gradient(135deg,#00b8e6 0%,#1e2fa0 100%)'
const BRAND2 = 'linear-gradient(135deg,#e6006e 0%,#7c3aed 100%)'

// ─── helpers ────────────────────────────────────────────────────────────────

async function apiFetch(path, opts = {}) {
  const token = getToken()
  const res = await fetch(`${API_BASE}${path}`, {
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    ...opts,
  })
  if (res.status === 401) {
    // Token missing/expired/invalid -- nothing to recover client-side,
    // send the seller back through landing-page's login redirect.
    clearToken()
    redirectToLogin()
    throw new Error('unauthorized')
  }
  if (!res.ok) { const b = await res.text(); throw new Error(b || res.statusText) }
  return res.json()
}

// LIVE_EVERY is how often a live page (useApi with { live: true }) refreshes
// its data in the background, so changes made elsewhere -- the buyer app's
// answers, automatic delivery and completion, reconciliation replies -- show
// without reloading the page.
const LIVE_EVERY = 5000

function useApi(path, deps = [], { live = false } = {}) {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const load = useCallback(async () => {
    if (!path) return
    setLoading(true); setError(null)
    try { setData(await apiFetch(path)) }
    catch (e) { setError(e.message) }
    finally { setLoading(false) }
  }, [path, ...deps]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { load() }, [load])

  // Live pages refresh quietly (no spinner; a failed refresh keeps what is
  // shown) every LIVE_EVERY while the tab is visible, and when it comes back.
  useEffect(() => {
    if (!live || !path) return
    const refresh = () => {
      if (document.hidden) return
      apiFetch(path).then(d => { setData(d); setError(null) }).catch(() => { })
    }
    const timer = setInterval(refresh, LIVE_EVERY)
    document.addEventListener('visibilitychange', refresh)
    window.addEventListener('focus', refresh)
    return () => {
      clearInterval(timer)
      document.removeEventListener('visibilitychange', refresh)
      window.removeEventListener('focus', refresh)
    }
  }, [live, path, ...deps]) // eslint-disable-line react-hooks/exhaustive-deps

  return { data, loading, error, reload: load }
}

// The logged-in seller's own provider ({id, name}) -- /providers is scoped to
// it server-side. Used to fill provider fields instead of hard-coded seeds.
function useOwnProvider() {
  const { data } = useApi('/providers')
  return data?.items?.[0] || null
}

const fmtDate = ts => {
  if (!ts) return '—'
  const d = new Date(ts)
  return d.toLocaleDateString(localeNow(), { day: '2-digit', month: 'short', year: 'numeric' })
    + ' ' + d.toLocaleTimeString(localeNow(), { hour: '2-digit', minute: '2-digit' })
}

const fmtDateShort = ts => {
  if (!ts) return '—'
  return new Date(ts).toLocaleDateString(localeNow(), { day: '2-digit', month: 'short', year: 'numeric' })
}

function fmtName(obj) {
  if (!obj) return '—'
  if (typeof obj === 'string') { try { obj = JSON.parse(obj) } catch { return obj } }
  return obj?.name || obj?.code || obj?.descriptor?.name || '—'
}

// ─── base components ─────────────────────────────────────────────────────────

function StatusBadge({ status }) {
  const { t } = useI18n()
  const map = {
    ACTIVE: { cls: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/25', dot: 'bg-emerald-400' },
    DRAFT: { cls: 'bg-amber-500/15  text-amber-400  border-amber-500/25', dot: 'bg-amber-400' },
    CANCELLED: { cls: 'bg-red-500/15    text-red-400    border-red-500/25', dot: 'bg-red-400' },
    COMPLETE: { cls: 'bg-sky-500/15    text-sky-400    border-sky-500/25', dot: 'bg-sky-400' },
    ACK: { cls: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/25', dot: 'bg-emerald-400' },
    NACK: { cls: 'bg-red-500/15    text-red-400    border-red-500/25', dot: 'bg-red-400' },
    // reconciliation batches
    SENT: { cls: 'bg-sky-500/15    text-sky-400    border-sky-500/25', dot: 'bg-sky-400' },
    SENDING: { cls: 'bg-sky-500/15    text-sky-400    border-sky-500/25', dot: 'bg-sky-400' },
    PENDING: { cls: 'bg-amber-500/15  text-amber-400  border-amber-500/25', dot: 'bg-amber-400' },
    AGREED: { cls: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/25', dot: 'bg-emerald-400' },
    ADJUSTED: { cls: 'bg-violet-500/15 text-violet-400 border-violet-500/25', dot: 'bg-violet-400' },
    DISPUTED: { cls: 'bg-red-500/15    text-red-400    border-red-500/25', dot: 'bg-red-400' },
    FAILED: { cls: 'bg-red-500/15    text-red-400    border-red-500/25', dot: 'bg-red-400' },
  }
  const s = map[status] || { cls: 'bg-slate-500/15 text-slate-400 border-slate-500/25', dot: 'bg-slate-400' }
  return (
    <span className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-semibold border ${s.cls}`}>
      <span className={`w-1.5 h-1.5 rounded-full ${s.dot}`} />
      {status ? t(status) : '—'}
    </span>
  )
}

function Card({ children, className = '', style, ...rest }) {
  return (
    <div className={`bg-[#111827] border border-white/[0.07] rounded-2xl ${className}`} style={style} {...rest}>
      {children}
    </div>
  )
}

function LoadingSpinner({ label }) {
  return (
    <div className="flex flex-col items-center justify-center py-16 gap-3">
      <div className="w-8 h-8 rounded-full border-2 border-slate-700 border-t-cyan-400 animate-spin" />
      {label && <p className="text-xs text-slate-500">{label}</p>}
    </div>
  )
}

function ErrorBox({ message, onRetry }) {
  const { t } = useI18n()
  return (
    <div className="flex items-center gap-3 bg-red-500/10 border border-red-500/20 rounded-xl p-4 text-sm text-red-400">
      <AlertCircle size={16} className="shrink-0" />
      <span className="flex-1 text-xs">{message}</span>
      {onRetry && <button onClick={onRetry} className="text-xs font-semibold underline underline-offset-2 shrink-0">{t('Retry')}</button>}
    </div>
  )
}

function SectionTitle({ children }) {
  return (
    <p className="text-[10px] font-bold uppercase tracking-widest text-slate-500 mb-3">{children}</p>
  )
}

function Pagination({ page, totalPages, onPage, total, limit }) {
  const { t } = useI18n()
  if (totalPages <= 1) return null
  return (
    <div className="flex items-center justify-between px-5 py-3 border-t border-white/[0.06]">
      <span className="text-xs text-slate-500">{t('{total} total · page {page}/{totalPages}', { total, page, totalPages })}</span>
      <div className="flex gap-1.5">
        <button disabled={page <= 1} onClick={() => onPage(p => p - 1)}
          className="p-1.5 rounded-lg border border-white/[0.08] text-slate-400 hover:text-white disabled:opacity-30 transition-colors">
          <ChevronLeft size={14} />
        </button>
        <button disabled={page >= totalPages} onClick={() => onPage(p => p + 1)}
          className="p-1.5 rounded-lg border border-white/[0.08] text-slate-400 hover:text-white disabled:opacity-30 transition-colors">
          <ChevronRightIcon size={14} />
        </button>
      </div>
    </div>
  )
}

// ─── Sidebar ─────────────────────────────────────────────────────────────────

const NAV_ITEMS = [
  { id: 'overview', label: 'Overview', icon: LayoutDashboard, desc: 'Stats & insights' },
  { id: 'catalogs', label: 'Catalogs', icon: BookOpen, desc: 'Manage & publish' },
  { id: 'orders', label: 'Orders', icon: ShoppingBag, desc: 'Manage contracts' },
  { id: 'inventory', label: 'Inventory', icon: Package, desc: 'Resources & offers' },
  { id: 'reconciliation', label: 'Reconciliation', icon: Scale, desc: 'Settle with buyer apps' },
  { id: 'support', label: 'Support Tickets', icon: Ticket, desc: 'Customer support' },
  { id: 'ratings', label: 'Ratings', icon: Star, desc: 'Order ratings' },
  { id: 'messages', label: 'Message Log', icon: MessageSquare, desc: 'Protocol audit' },
]

// First-visit walkthrough of each dashboard section (see Tour.jsx). A step's
// `page` opens that section; `target` is the data-tour element to spotlight.
// The catalog part is hands-on: the user clicks the real buttons (Auto-fill
// does the typing) and ends up with a sample catalog published to the network.
const onPage = p => ({ page }) => page === p
const tourSteps = t => [
  {
    eyebrow: t('Welcome'), title: t('Your seller dashboard'),
    body: <p>{t("This is where you sell on the ION network: publish what you sell, fulfil the orders buyer apps send you, and get paid. This tour shows each section, and you'll publish a sample catalog along the way. It takes about three minutes.")}</p>
  },
  {
    eyebrow: t('Overview'), page: 'overview', target: 'nav-overview', title: t('Overview'),
    body: <p>{t("Your business at a glance: active and pending orders, today's volume and recent activity.")}</p>
  },

  {
    eyebrow: t('Catalogs'), page: 'catalogs', target: 'nav-catalogs', title: t("Let's publish a catalog"),
    skip: { label: t('Skip this part'), to: 'orders' },
    body: <><p>{rich(t("Buyers find you through your <b>catalog</b>. You'll now publish a sample one in four steps: create the catalog, add a product, give it an offer, then publish."))}</p><p className="text-slate-400">{rich(t('You click; <b>Auto-fill</b> does the typing.'))}</p></>
  },
  {
    eyebrow: t('Publish · {n} of 4', { n: 1 }), page: 'catalogs', target: 'create-catalog', until: onPage('create_catalog'), waitLabel: t('Click Create Catalog'),
    title: t('Create a catalog'),
    body: <p>{rich(t('Click <b>Create Catalog</b>. You can have several catalogs, for example one per store or product line.'))}</p>
  },
  {
    eyebrow: t('Publish · {n} of 4', { n: 1 }), target: 'catalog-autofill', action: 'click', waitLabel: t('Click Auto-fill Sample'),
    title: t('Fill in the details'),
    body: <p>{rich(t('A catalog needs an ID, a name and a validity period. Your provider details come from your account. Click <b>Auto-fill Sample</b> to fill in an example.'))}</p>
  },
  {
    eyebrow: t('Publish · {n} of 4', { n: 1 }), target: 'catalog-submit', until: onPage('catalog_detail'), waitLabel: t('Click Create'),
    title: t('Save the catalog'),
    body: <p>{rich(t("The form is filled in. Click <b>Create Catalog & Add Products</b>. The catalog is saved as a <b>Draft</b>; buyers can't see it until you publish."))}</p>
  },
  {
    eyebrow: t('Publish · {n} of 4', { n: 2 }), target: 'add-product', until: onPage('add_product'), waitLabel: t('Click Add Product'),
    title: t('Add a product'),
    body: <p>{rich(t("Your catalog is created, but it's empty. Click <b>Add Product</b>."))}</p>
  },
  {
    eyebrow: t('Publish · {n} of 4', { n: 2 }), target: 'product-autofill', action: 'click', waitLabel: t('Click Auto-fill'),
    title: t('Describe the product'),
    body: <p>{rich(t('A product has a name, descriptions, an image, stock, and details like brand, weight and packaging. Click <b>Auto-fill</b> for a sample product.'))}</p>
  },
  {
    eyebrow: t('Publish · {n} of 4', { n: 2 }), target: 'product-submit', until: onPage('add_offer'), waitLabel: t('Click Save'),
    title: t('Save the product'),
    body: <p>{rich(t('Click <b>Save Resource → Setup Offer</b>. The product is saved and you go straight to its offer.'))}</p>
  },
  {
    eyebrow: t('Publish · {n} of 4', { n: 3 }), target: 'offer-autofill', action: 'click', waitLabel: t('Click Auto-fill'),
    title: t('Create the offer'),
    body: <p>{rich(t('The <b>offer</b> is how you sell the product: its price, tax and policies. The product is already linked. Click <b>Auto-fill</b>.'))}</p>
  },
  {
    eyebrow: t('Publish · {n} of 4', { n: 3 }), target: 'offer-price', title: t('The price'),
    body: <p>{t("The price is before tax; the tax (PPN 12% by default) is set below it. The buyer's total, including tax, is worked out for you.")}</p>
  },
  {
    eyebrow: t('Publish · {n} of 4', { n: 3 }), target: 'offer-policies', title: t('Your policies'),
    body: <><p>{rich(t('Under <b>Policies & Serviceability</b>, decide whether returns and cancellations are allowed, their windows, the cancellation cutoff and any restocking fee. Also set COD and your delivery area and hours.'))}</p><p className="text-slate-400">{t('These rules decide cancellations and returns automatically later. No approval is needed.')}</p></>
  },
  {
    eyebrow: t('Publish · {n} of 4', { n: 3 }), target: 'offer-submit', until: onPage('catalog_detail'), waitLabel: t('Click Add Offer'),
    title: t('Save the offer'),
    body: <p>{rich(t('Click <b>Add Offer to Catalog</b>.'))}</p>
  },
  {
    eyebrow: t('Publish · {n} of 4', { n: 4 }), target: 'publish', until: () => !!document.querySelector('[data-tour="published-badge"]'), waitLabel: t('Click Publish'),
    title: t('Publish to Network'),
    body: <><p>{rich(t('Your catalog now has a product with an offer. Click <b>Publish to Network</b> and confirm.'))}</p><p className="text-slate-400">{t('Buyer apps on the network can then find it and place orders. You can keep editing and publish updates any time.')}</p></>
  },
  {
    eyebrow: t('Published'), target: 'published-badge', title: t("You're live"),
    body: <><p>{rich(t('Your catalog is <b>Published</b>: buyers can find it now.'))}</p><p className="text-slate-400">{t('You can still add products, edit existing ones, and click Update on Network any time to push your changes live.')}</p></>
  },

  {
    id: 'orders', eyebrow: t('Orders'), page: 'orders', target: 'nav-orders', title: t('Orders'),
    body: <><p>{rich(t('Orders arrive here once a buyer confirms. Open one and move it <b>Packed → Shipped</b>; it becomes <b>Delivered</b> automatically.'))}</p><p className="text-slate-400">{t("Cancellations and returns follow your offer's policies. You'll see the outcome here.")}</p></>
  },
  {
    eyebrow: t('Inventory'), page: 'inventory', target: 'nav-inventory', title: t('Inventory'),
    body: <p>{t('Everything you sell across catalogs: products, offers and live stock. Stock goes down when an order is confirmed and back up when one is cancelled or returned.')}</p>
  },
  {
    eyebrow: t('Reconciliation'), page: 'reconciliation', target: 'nav-reconciliation', title: t('Get paid'),
    body: <p>{rich(t("After delivery plus your settlement window, an order's payment falls <b>due</b>. Send <b>reconcile</b> to the buyer app (it can also send you one); when both sides agree, SEAM has the PJP pay you from the held funds."))}</p>
  },
  {
    eyebrow: t('Support'), page: 'support', target: 'nav-support', title: t('Support tickets'),
    body: <p>{t('Help requests buyers raise about their orders.')}</p>
  },
  {
    eyebrow: t('Ratings'), page: 'ratings', target: 'nav-ratings', title: t('Ratings'),
    body: <p>{t('How buyers rated their orders.')}</p>
  },
  {
    eyebrow: t('Message Log'), page: 'messages', target: 'nav-messages', title: t('Message Log'),
    body: <p>{t("Every Beckn message you sent or received, with its full payload. Useful when something doesn't look right.")}</p>
  },
  {
    eyebrow: t('Done'), target: 'tour-button', title: t("You're all set"),
    body: <p>{t('Start by creating your first catalog. You can run this tour again any time from here.')}</p>
  },
]
const TOUR_DONE_KEY = 'bpp.tourDone'

function Sidebar({ active, onNav, open, onClose, onTour }) {
  const { t } = useI18n()
  const navContent = (
    <>
      {/* Logo */}
      <div className="px-5 pt-5 pb-4 border-b border-white/[0.06]">
        <div className="flex items-center justify-between gap-2">
          <span className="text-lg font-bold text-white tracking-tight truncate">
            ION <span className="font-normal text-slate-400">{t('Reference App')}</span>
          </span>
          {onClose && (
            <button onClick={onClose} className="lg:hidden p-1.5 rounded-lg text-slate-500 hover:text-white">
              <X size={18} />
            </button>
          )}
        </div>
        <div className="mt-3 flex items-center gap-2">
          <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse shrink-0" />
          <p className="text-[10px] text-slate-500 font-medium truncate">{t('BPP Admin · ION Network')}</p>
          <LanguageToggle className="ml-auto" />
        </div>
      </div>

      {/* Nav */}
      <nav className="flex-1 px-3 py-4 space-y-0.5 overflow-y-auto">
        {NAV_ITEMS.map(({ id, label, icon: Icon, desc }) => {
          const isActive = active === id || (id === 'catalogs' && ['catalog_detail', 'create_catalog', 'add_product', 'add_offer', 'edit_product', 'edit_offer', 'publish'].includes(active))
          return (
            <button key={id} data-tour={`nav-${id}`} onClick={() => { onNav(id); onClose?.() }}
              className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-xl text-sm font-medium transition-all group ${isActive ? 'text-white' : 'text-slate-400 hover:text-slate-200 hover:bg-slate-900'
                }`}
              style={isActive ? { background: 'rgba(0,184,230,0.12)', border: '1px solid rgba(0,184,230,0.25)' } : {}}
            >
              <div className={`w-8 h-8 rounded-lg flex items-center justify-center shrink-0 transition-all ${isActive ? '' : 'bg-slate-900 group-hover:bg-white/[0.07]'
                }`}
                style={isActive ? { background: 'rgba(0,184,230,0.2)' } : {}}>
                <Icon size={15} style={isActive ? { color: '#00b8e6' } : {}} />
              </div>
              <div className="flex-1 min-w-0 text-left">
                <p className="text-sm leading-none">{t(label)}</p>
                <p className={`text-[10px] mt-0.5 truncate ${isActive ? 'text-cyan-400/60' : 'text-slate-600'}`}>{t(desc)}</p>
              </div>
              {isActive && <ChevronRight size={13} style={{ color: '#00b8e6', flexShrink: 0 }} />}
            </button>
          )
        })}
      </nav>

      {/* Footer */}
      <div className="px-4 py-3 border-t border-white/[0.06] space-y-3">
        <button data-tour="tour-button" onClick={() => { onClose?.(); onTour?.() }}
          className="w-full flex items-center justify-center gap-2 px-3 py-2 rounded-xl text-xs font-semibold text-cyan-300 transition-all bg-cyan-500/10 hover:bg-cyan-500/20 border border-cyan-500/20 hover:border-cyan-500/40">
          <Compass size={13} className="shrink-0" /> <span className="truncate">{t('Take the tour')}</span>
        </button>
        <a
          href={`${window.__env__?.BAP_URL || import.meta.env.VITE_BAP_URL || "#"}?token=${encodeURIComponent(getToken() || '')}&id_token=${encodeURIComponent(getIdToken() || '')}`}
          target="_blank"
          rel="noopener noreferrer"
          className="w-full flex items-center justify-center gap-2 px-3 py-2 rounded-xl text-xs font-semibold text-emerald-400 transition-all bg-emerald-500/10 hover:bg-emerald-500/20 border border-emerald-500/20 hover:border-emerald-500/40"
        >
          <ExternalLink size={13} /> {t('Go to BAP APP')}
        </a>
        <UserProfileMenu />
        <p className="text-[10px] text-slate-600 text-center">Beckn Protocol v2.0 · ION Retail</p>
      </div>
    </>
  )

  return (
    <>
      {/* Desktop sidebar */}
      <aside className="hidden lg:flex fixed inset-y-0 left-0 z-40 flex-col w-64 border-r border-white/[0.06]"
        style={{ background: '#0a0e1a' }}>
        {navContent}
      </aside>

      {/* Mobile drawer */}
      {open && (
        <>
          <div className="fixed inset-0 z-40 bg-black/60 lg:hidden" onClick={onClose} />
          <aside className="fixed inset-y-0 left-0 z-50 flex flex-col w-72 border-r border-white/[0.06] lg:hidden"
            style={{ background: '#0a0e1a' }}>
            {navContent}
          </aside>
        </>
      )}
    </>
  )
}

// ─── PageHeader ───────────────────────────────────────────────────────────────

function PageHeader({ title, subtitle, action, breadcrumb }) {
  return (
    <div className="flex items-start justify-between gap-3 mb-7">
      <div className="min-w-0">
        {breadcrumb && <p className="text-[10px] font-semibold uppercase tracking-widest mb-1.5" style={{ color: '#00b8e6' }}>{breadcrumb}</p>}
        <h1 className="text-2xl font-bold text-white leading-tight">{title}</h1>
        {subtitle && <p className="text-sm text-slate-400 mt-1">{subtitle}</p>}
      </div>
      {action}
    </div>
  )
}

// ─── Overview ────────────────────────────────────────────────────────────────

function StatCard({ label, value, icon: Icon, gradient, sub, onClick, loading }) {
  const { t } = useI18n()
  const Wrap = onClick ? 'button' : 'div'
  return (
    <Wrap onClick={onClick}
      className={`relative w-full text-left rounded-2xl p-5 border border-white/[0.07] overflow-hidden transition-all
        ${onClick ? 'cursor-pointer hover:border-white/[0.14] hover:shadow-lg active:scale-[0.98]' : ''}`}
      style={{ background: '#111827' }}
    >
      {/* Glow blob */}
      <div className="absolute -right-4 -top-4 w-24 h-24 rounded-full opacity-10 blur-xl"
        style={{ background: gradient }} />

      <div className="relative flex items-start justify-between gap-3">
        <div>
          <p className="text-[11px] font-semibold uppercase tracking-widest text-slate-500">{label}</p>
          <p className="text-4xl font-bold text-white mt-1.5 leading-none">
            {loading ? <span className="inline-block w-12 h-8 rounded bg-slate-700/60 animate-pulse" /> : (value ?? '—')}
          </p>
          {sub && <p className="text-xs text-slate-500 mt-2">{sub}</p>}
        </div>
        <div className="w-10 h-10 rounded-xl flex items-center justify-center shrink-0"
          style={{ background: gradient + '33' }}>
          <Icon size={18} style={{ color: 'white', opacity: 0.9 }} />
        </div>
      </div>
      {onClick && (
        <div className="relative mt-4 flex items-center gap-1 text-[11px] font-semibold"
          style={{ color: '#00b8e6' }}>
          {t('View details')} <ArrowRight size={11} />
        </div>
      )}
    </Wrap>
  )
}

function FunnelCard({ funnel, loading }) {
  const { t } = useI18n()
  const steps = [
    { key: 'select', label: 'Select', color: '#00b8e6', emoji: '🔍' },
    { key: 'init', label: 'Init', color: '#7c3aed', emoji: '📋' },
    { key: 'confirm', label: 'Confirm', color: '#10b981', emoji: '✅' },
  ]
  const max = Math.max(1, ...steps.map(s => funnel?.[s.key] ?? 0))
  const selectCount = funnel?.select ?? 0
  const confirmCount = funnel?.confirm ?? 0
  const convRate = selectCount > 0 ? Math.round((confirmCount / selectCount) * 100) : 0

  return (
    <Card className="p-5">
      <div className="flex items-center justify-between mb-4">
        <SectionTitle>{t('Conversion Funnel')}</SectionTitle>
        {!loading && (
          <span className="text-xs font-bold px-2.5 py-1 rounded-full"
            style={{ background: 'rgba(16,185,129,0.15)', color: '#10b981' }}>
            {t('{rate}% conversion', { rate: convRate })}
          </span>
        )}
      </div>
      {loading ? <LoadingSpinner /> : (
        <div className="space-y-4">
          {steps.map(({ key, label, color, emoji }, idx) => {
            const val = funnel?.[key] ?? 0
            const pct = (val / max) * 100
            const drop = idx > 0 ? steps[idx - 1].key : null
            const dropVal = drop ? (funnel?.[drop] ?? 0) : null
            const dropPct = dropVal > 0 ? Math.round(((dropVal - val) / dropVal) * 100) : null
            return (
              <div key={key}>
                <div className="flex justify-between items-center text-xs mb-1.5">
                  <span className="text-slate-300 font-medium flex items-center gap-1.5">
                    {emoji} {label}
                  </span>
                  <div className="flex items-center gap-2">
                    {dropPct !== null && dropPct > 0 && (
                      <span className="text-red-400 text-[10px]">−{dropPct}%</span>
                    )}
                    <span className="font-bold text-white">{val}</span>
                  </div>
                </div>
                <div className="h-2.5 bg-slate-800 rounded-full overflow-hidden">
                  <motion.div
                    className="h-full rounded-full"
                    style={{ background: color }}
                    initial={{ width: 0 }}
                    animate={{ width: `${pct}%` }}
                    transition={{ duration: 0.8, ease: 'easeOut', delay: idx * 0.1 }}
                  />
                </div>
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}

function ActivityRow({ o, onView }) {
  return (
    <div className="flex items-center gap-3 py-3 border-b border-white/[0.05] last:border-0 group">
      <div className="w-8 h-8 rounded-lg flex items-center justify-center shrink-0 text-sm"
        style={{ background: 'rgba(0,184,230,0.1)' }}>
        {o.status === 'ACTIVE' ? '✅' : o.status === 'DRAFT' ? '⏳' : o.status === 'CANCELLED' ? '❌' : '📦'}
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-slate-200 truncate">{o.bap_id}</p>
        <p className="text-[11px] text-slate-500 font-mono">{o.transaction_id?.slice(0, 12)}…</p>
      </div>
      <div className="flex flex-col items-end gap-1 shrink-0">
        <StatusBadge status={o.status} />
        <p className="text-[10px] text-slate-600">{fmtDateShort(o.created_at?.Time || o.created_at)}</p>
      </div>
      <button onClick={() => onView(o)} className="opacity-0 group-hover:opacity-100 transition-opacity ml-1 text-cyan-400">
        <Eye size={14} />
      </button>
    </div>
  )
}

function OverviewPage({ onNav }) {
  const { t } = useI18n()
  const { data: stats, loading: sLoad, error: sErr, reload } = useApi('/dashboard/stats', [], { live: true })
  const { data: orders } = useApi('/orders?limit=6', [], { live: true })
  const { data: supportData } = useApi('/support-tickets?page=1&limit=1')
  const { data: ratingsData } = useApi('/ratings?page=1&limit=1')
  const totalTickets = supportData?.total ?? null
  const totalRatings = ratingsData?.total ?? null

  return (
    <div>
      <PageHeader
        breadcrumb={t('BPP Admin · Dashboard')}
        title={t('Overview')}
        subtitle={t('Live snapshot of your seller activity on the ION network')}
        action={
          <button onClick={reload}
            className="shrink-0 whitespace-nowrap flex items-center gap-1.5 text-xs text-slate-400 hover:text-white transition-colors px-3 py-1.5 rounded-lg border border-white/[0.07] hover:border-white/20">
            <RefreshCw size={12} className={sLoad ? 'animate-spin' : ''} />
            {t('Refresh')}
          </button>
        }
      />

      {sErr && <div className="mb-4"><ErrorBox message={sErr} onRetry={reload} /></div>}

      {/* Stock alerts */}
      {!sLoad && (stats?.out_of_stock > 0 || stats?.low_stock > 0) && (
        <div className="mb-5 flex flex-wrap gap-3">
          {stats?.out_of_stock > 0 && (
            <button onClick={() => onNav('inventory', { initialTab: 'stock' })}
              className="flex items-center gap-2 text-sm font-semibold px-4 py-2 rounded-xl transition-all hover:opacity-90"
              style={{ background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.25)', color: '#ef4444' }}>
              <AlertCircle size={14} />
              {t(stats.out_of_stock === 1 ? '{count} resource out of stock — view stock' : '{count} resources out of stock — view stock', { count: stats.out_of_stock })}
            </button>
          )}
          {stats?.low_stock > 0 && (
            <button onClick={() => onNav('inventory', { initialTab: 'stock' })}
              className="flex items-center gap-2 text-sm font-semibold px-4 py-2 rounded-xl transition-all hover:opacity-90"
              style={{ background: 'rgba(245,158,11,0.1)', border: '1px solid rgba(245,158,11,0.25)', color: '#f59e0b' }}>
              <AlertCircle size={14} />
              {t(stats.low_stock === 1 ? '{count} resource running low — view stock' : '{count} resources running low — view stock', { count: stats.low_stock })}
            </button>
          )}
        </div>
      )}

      {/* Stat cards */}
      <div className="grid grid-cols-2 sm:grid-cols-2 xl:grid-cols-4 gap-4 mb-4">
        <StatCard label={t('Active Orders')} value={stats?.active_orders} icon={CheckCircle2} gradient="linear-gradient(135deg,#10b981,#059669)" loading={sLoad} onClick={() => onNav('orders')} />
        <StatCard label={t('Pending Orders')} value={stats?.pending_orders} icon={Clock} gradient="linear-gradient(135deg,#f59e0b,#d97706)" loading={sLoad} onClick={() => onNav('orders')} />
        <StatCard label={t('Orders Today')} value={stats?.today_orders} icon={TrendingUp} gradient="linear-gradient(135deg,#00b8e6,#1e2fa0)" loading={sLoad} onClick={() => onNav('orders')} />
        <StatCard label={t('Resources')} value={stats?.resource_count} icon={Package} gradient="linear-gradient(135deg,#06b6d4,#0891b2)" loading={sLoad} onClick={() => onNav('inventory', { initialTab: 'resources' })} />
      </div>
      <div className="grid grid-cols-2 sm:grid-cols-2 xl:grid-cols-4 gap-4 mb-6">
        <StatCard label={t('Active Offers')} value={stats?.offer_count} icon={Tag} gradient="linear-gradient(135deg,#e6006e,#7c3aed)" loading={sLoad} onClick={() => onNav('inventory', { initialTab: 'offers' })} />
        <StatCard label={t('Out of Stock')} value={stats?.out_of_stock ?? 0} icon={Ban} gradient="linear-gradient(135deg,#ef4444,#b91c1c)" loading={sLoad} onClick={() => onNav('inventory', { initialTab: 'stock' })} />
        <StatCard label={t('Support Tickets')} value={totalTickets} icon={Ticket} gradient="linear-gradient(135deg,#8b5cf6,#6d28d9)" loading={totalTickets === null} onClick={() => onNav('support')} />
        <StatCard label={t('Ratings')} value={totalRatings} icon={Star} gradient="linear-gradient(135deg,#f59e0b,#b45309)" loading={totalRatings === null} onClick={() => onNav('ratings')} />
      </div>

      {/* Bottom section */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-5">
        {/* Recent orders */}
        <div className="lg:col-span-2">
          <Card>
            <div className="flex items-center justify-between px-5 py-4 border-b border-white/[0.06]">
              <div>
                <p className="text-sm font-bold text-white">{t('Recent Orders')}</p>
                <p className="text-[11px] text-slate-500 mt-0.5">{t('Latest incoming contracts')}</p>
              </div>
              <button onClick={() => onNav('orders')}
                className="flex items-center gap-1 text-xs font-semibold transition-colors"
                style={{ color: '#00b8e6' }}>
                {t('View all')} <ChevronRight size={12} />
              </button>
            </div>
            <div className="px-5 py-1">
              {!orders?.items?.length
                ? <p className="text-sm text-slate-500 py-8 text-center">{t('No orders yet')}</p>
                : orders.items.map(o => (
                  <ActivityRow key={o.id} o={o} onView={() => onNav('orders')} />
                ))
              }
            </div>
          </Card>
        </div>

        {/* Funnel */}
        <FunnelCard funnel={stats?.funnel} loading={sLoad} />
      </div>
    </div>
  )
}

// ─── Orders ───────────────────────────────────────────────────────────────────

function OrderDetailDrawer({ order, onClose }) {
  const { t } = useI18n()
  const { data, loading, reload } = useApi(order?.id ? `/orders/${order.id}` : null, [order?.id], { live: true })
  const [updating, setUpdating] = useState(false)

  let contractAttrs = data?.contract?.contract_attributes;
  if (typeof contractAttrs === 'string') {
    try { contractAttrs = JSON.parse(contractAttrs); } catch { }
  }
  const performances = contractAttrs?.performance || [];
  // Returns are decided by the offer's return policy when the buyer requests
  // them -- the seller only sees the outcome.
  const returnPerfs = performances.filter(p => p.performanceAttributes?.type === 'RETURN');
  const deliveryPerf = performances.find(p => p.performanceAttributes?.type !== 'RETURN');
  const deliveryStatus = deliveryPerf?.status?.code;

  const updateStatus = async (status) => {
    setUpdating(true)
    try {
      await apiFetch(`/orders/${order.id}/fulfillment-status`, {
        method: 'PUT',
        body: JSON.stringify({ status })
      })
      reload()
    } catch (e) {
      alert(t('Failed to update status: {error}', { error: e.message }))
    } finally {
      setUpdating(false)
    }
  }

  return (
    <AnimatePresence>
      {order && (
        <>
          <motion.div className="fixed inset-0 bg-black/60 backdrop-blur-sm z-40"
            initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
            onClick={onClose} />
          <motion.div
            className="fixed right-0 top-0 bottom-0 z-50 w-[500px] flex flex-col border-l border-white/[0.07]"
            style={{ background: '#0a0e1a' }}
            initial={{ x: '100%' }} animate={{ x: 0 }} exit={{ x: '100%' }}
            transition={{ type: 'spring', stiffness: 280, damping: 28 }}
          >
            {/* Header */}
            <div className="flex items-center justify-between px-6 py-4 border-b border-white/[0.07] shrink-0">
              <div>
                <p className="font-bold text-white">{t('Order Detail')}</p>
                <p className="text-xs text-slate-500 font-mono mt-0.5">{order.transaction_id}</p>
              </div>
              <button onClick={onClose}
                className="w-8 h-8 rounded-lg flex items-center justify-center text-slate-400 hover:text-white hover:bg-white/[0.07] transition-all">
                <X size={16} />
              </button>
            </div>

            <div className="flex-1 overflow-y-auto px-6 py-5 space-y-6">
              {loading ? <LoadingSpinner label={t('Loading contract…')} /> : (
                <>
                  {/* Contract info */}
                  <section>
                    <SectionTitle>{t('Contract')}</SectionTitle>
                    <Card className="divide-y divide-white/[0.05]">
                      {[
                        [t('Status'), <StatusBadge key="s" status={data?.contract?.status} />],
                        [t('Buyer'), data?.contract?.bap_id],
                        [t('Domain'), data?.contract?.domain || '—'],
                        [t('Created'), fmtDate(data?.contract?.created_at?.Time || data?.contract?.created_at)],
                        [t('Updated'), fmtDate(data?.contract?.updated_at?.Time || data?.contract?.updated_at)],
                      ].map(([k, v]) => (
                        <div key={k} className="flex items-center justify-between px-4 py-2.5 text-sm">
                          <span className="text-slate-500 shrink-0 w-24">{k}</span>
                          <span className="text-slate-200 text-right">{v}</span>
                        </div>
                      ))}
                    </Card>
                  </section>

                  {/* Performance Actions */}
                  {data?.contract?.status === 'ACTIVE' && (
                    <section>
                      <SectionTitle>{t('Performance Actions')}</SectionTitle>

                      {returnPerfs.length > 0 ? (
                        <Card className="p-4 space-y-2">
                          <p className="text-sm font-semibold text-purple-400 flex items-center gap-2">
                            <RefreshCw size={16} /> {t('Returns')}
                          </p>
                          {returnPerfs.map(rp => {
                            const code = rp.status?.code || ''
                            const color = code === 'Return_Approved' ? 'text-emerald-400' : code === 'Return_Rejected' ? 'text-rose-400' : 'text-amber-400'
                            return (
                              <div key={rp.id} className="text-xs flex items-start justify-between gap-3">
                                <span className="text-slate-400 font-mono">{rp.id} · {(rp.commitmentIds || []).join(', ')}</span>
                                <span className={`${color} text-right`}>
                                  {t(code.replace(/_/g, ' '))}{rp.status?.shortDesc ? ` — ${rp.status.shortDesc}` : ''}
                                </span>
                              </div>
                            )
                          })}
                          <p className="text-[10px] text-slate-500">{t("Accepted or rejected automatically by the offer's return policy (window, restocking fee).")}</p>
                        </Card>
                      ) : (
                        <Card className="p-4 flex items-center gap-3">
                          {(!deliveryStatus || (deliveryStatus.toLowerCase() !== 'packed' && deliveryStatus.toLowerCase() !== 'shipped' && deliveryStatus.toLowerCase() !== 'delivered')) && (
                            <button
                              disabled={updating}
                              onClick={() => updateStatus('Packed')}
                              className="flex-1 py-2 px-3 bg-amber-500/10 text-amber-400 border border-amber-500/20 hover:bg-amber-500/20 rounded-lg text-sm font-semibold transition-colors disabled:opacity-50"
                            >
                              {updating ? t('Updating…') : t('Mark Packed')}
                            </button>
                          )}
                          {deliveryStatus?.toLowerCase() === 'packed' && (
                            <button
                              disabled={updating}
                              onClick={() => updateStatus('Shipped')}
                              className="flex-1 py-2 px-3 bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 hover:bg-emerald-500/20 rounded-lg text-sm font-semibold transition-colors disabled:opacity-50"
                            >
                              {updating ? t('Updating…') : t('Mark Shipped')}
                            </button>
                          )}
                          {deliveryStatus?.toLowerCase() === 'shipped' && (
                            <p className="text-sm font-semibold text-cyan-400 w-full text-center">
                              {t('Order is Shipped (Auto-Delivering)')}
                            </p>
                          )}
                          {deliveryStatus?.toLowerCase() === 'delivered' && (
                            <p className="text-sm font-semibold text-slate-400 w-full text-center">
                              {t('Order is Delivered')}
                            </p>
                          )}
                        </Card>
                      )}
                    </section>
                  )}

                  {/* Commitments */}
                  {data?.commitments?.length > 0 && (
                    <section>
                      <SectionTitle>{t('Commitments ({count})', { count: data.commitments.length })}</SectionTitle>
                      <div className="space-y-2">
                        {data.commitments.map(cm => (
                          <Card key={cm.id} className="p-4 space-y-2">
                            <div className="flex items-center justify-between">
                              <p className="text-xs font-mono text-slate-400 truncate">{cm.id}</p>
                              <StatusBadge status={cm.status} />
                            </div>
                            {cm.offer_id && (
                              <div className="flex items-center gap-2 text-xs">
                                <Tag size={11} className="text-slate-500" />
                                <span className="text-slate-300">{cm.offer_id}</span>
                              </div>
                            )}
                          </Card>
                        ))}
                      </div>
                    </section>
                  )}

                  {/* Considerations */}
                  {data?.considerations?.length > 0 && (
                    <section>
                      <SectionTitle>{t('Consideration')}</SectionTitle>
                      <div className="space-y-2">
                        {data.considerations.map((c, i) => {
                          let attrs = c.consideration_attributes
                          try { if (typeof attrs === 'string') attrs = JSON.parse(attrs) } catch { }
                          const price = attrs?.price || attrs?.totalAmount
                          const currency = attrs?.currency || 'IDR'
                          return (
                            <Card key={i} className="p-4">
                              <div className="flex items-center justify-between mb-2">
                                <span className="text-xs font-semibold text-slate-300">{c.status_code}</span>
                                {price && (
                                  <span className="text-lg font-bold" style={{ color: '#00b8e6' }}>
                                    {currency === 'IDR' ? 'Rp' : currency === 'INR' ? '₹' : ''}{Number(price).toLocaleString(localeNow())}
                                  </span>
                                )}
                              </div>
                              {attrs && (
                                <pre className="text-[10px] text-slate-500 bg-black/30 rounded-lg p-2 overflow-x-auto mt-1">
                                  {JSON.stringify(attrs, null, 2)}
                                </pre>
                              )}
                            </Card>
                          )
                        })}
                      </div>
                    </section>
                  )}

                  {/* Ratings */}
                  {data?.contract?.id && <OrderRatings contractId={data.contract.id} />}
                </>
              )}
            </div>
          </motion.div>
        </>
      )}
    </AnimatePresence>
  )
}

// Delivery progress of an order (PENDING → Packed → Shipped → Delivered), with
// the latest return's outcome underneath when there is one.
const FULFILLMENT_STYLE = {
  PENDING: 'bg-slate-500/15 text-slate-300 border-slate-500/25',
  PACKED: 'bg-amber-500/15 text-amber-300 border-amber-500/25',
  SHIPPED: 'bg-sky-500/15 text-sky-300 border-sky-500/25',
  DELIVERED: 'bg-emerald-500/15 text-emerald-300 border-emerald-500/25',
}
const RETURN_LABEL = {
  Return_Approved: ['Return approved', 'text-emerald-400'],
  Return_Rejected: ['Return rejected', 'text-rose-400'],
  Return_Initiated: ['Return requested', 'text-amber-400'],
}

function FulfillmentStatus({ status, returnStatus, contractStatus }) {
  const { t } = useI18n()
  const code = String(status || '').toUpperCase()
  // An order cancelled before it was packed never entered fulfillment.
  if (contractStatus === 'CANCELLED' && (!code || code === 'PENDING')) {
    return <span className="text-xs text-slate-500">{t('Not fulfilled')}</span>
  }
  if (!code) return <span className="text-xs text-slate-600">—</span>
  const [retText, retCls] = RETURN_LABEL[returnStatus] || []
  return (
    <div className="flex flex-col items-start gap-1">
      <span className={`inline-flex px-2.5 py-0.5 rounded-full text-xs font-semibold border ${FULFILLMENT_STYLE[code] || FULFILLMENT_STYLE.PENDING}`}>
        {t(status)}
      </span>
      {retText && <span className={`text-[10px] font-semibold ${retCls}`}>{t(retText)}</span>}
    </div>
  )
}

function OrdersPage() {
  const { t } = useI18n()
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState(null)
  const [search, setSearch] = useState('')
  const limit = 20
  const { data, loading, error, reload } = useApi(`/orders?page=${page}&limit=${limit}`, [page], { live: true })
  const total = data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / limit))
  const items = (data?.items ?? []).filter(o =>
    !search || o.bap_id?.includes(search) || o.transaction_id?.includes(search) || o.status?.includes(search.toUpperCase())
  )

  return (
    <div>
      <PageHeader breadcrumb={t('Orders')} title={t('Order Management')}
        subtitle={total ? t('{total} total contracts on the network', { total }) : t('No orders yet')}
        action={
          <button onClick={reload}
            className="shrink-0 whitespace-nowrap flex items-center gap-1.5 text-xs text-slate-400 hover:text-white transition-colors px-3 py-1.5 rounded-lg border border-white/[0.07]">
            <RefreshCw size={12} className={loading ? 'animate-spin' : ''} /> {t('Refresh')}
          </button>
        }
      />

      {error && <div className="mb-4"><ErrorBox message={error} onRetry={reload} /></div>}

      {/* Search */}
      <div className="relative mb-4 max-w-sm">
        <Search size={13} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
        <input value={search} onChange={e => setSearch(e.target.value)}
          placeholder={t('Search buyer, txn ID or status…')}
          className="w-full bg-slate-900 border border-white/[0.08] rounded-xl pl-9 pr-3 py-2 text-sm text-slate-200 placeholder-slate-500 focus:outline-none focus:border-cyan-500/40 transition-colors" />
      </div>

      <Card>
        {loading ? <LoadingSpinner label={t('Loading orders…')} /> : (
          <div className="overflow-x-auto"><table className="w-full text-sm min-w-[500px]">
            <thead>
              <tr className="text-[10px] font-bold text-slate-500 uppercase tracking-widest border-b border-white/[0.06]">
                <th className="text-left px-5 py-3">{t('Txn ID')}</th>
                <th className="text-left px-5 py-3">{t('Buyer')}</th>
                <th className="text-left px-5 py-3">{t('Status')}</th>
                <th className="text-left px-5 py-3">{t('Fulfillment')}</th>
                <th className="text-left px-5 py-3">{t('Offer')}</th>
                <th className="text-left px-5 py-3">{t('Created')}</th>
                <th className="px-5 py-3 w-16" />
              </tr>
            </thead>
            <tbody className="divide-y divide-white/[0.04]">
              {items.map(o => (
                <tr key={o.id} className="hover:bg-white/[0.025] transition-colors group">
                  <td className="px-5 py-3 font-mono text-xs text-slate-500">{o.transaction_id?.slice(0, 8)}…</td>
                  <td className="px-5 py-3 text-slate-300 font-medium max-w-[180px] truncate">{o.bap_id}</td>
                  <td className="px-5 py-3"><StatusBadge status={o.status} /></td>
                  <td className="px-5 py-3"><FulfillmentStatus status={o.fulfillment_status} returnStatus={o.return_status} contractStatus={o.status} /></td>
                  <td className="px-5 py-3 text-slate-400 text-xs max-w-[140px] truncate">{fmtName(o.offer_descriptor)}</td>
                  <td className="px-5 py-3 text-slate-500 text-xs">{fmtDateShort(o.created_at?.Time || o.created_at)}</td>
                  <td className="px-5 py-3">
                    <button onClick={() => setSelected(o)}
                      className="opacity-0 group-hover:opacity-100 flex items-center gap-1 text-xs font-semibold transition-all"
                      style={{ color: '#00b8e6' }}>
                      <Eye size={13} /> {t('View')}
                    </button>
                  </td>
                </tr>
              ))}
              {!items.length && (
                <tr><td colSpan={7} className="px-5 py-12 text-center text-slate-500">{t('No orders found')}</td></tr>
              )}
            </tbody>
          </table>
          </div>
        )}
        <Pagination page={page} totalPages={totalPages} onPage={setPage} total={total} limit={limit} />
      </Card>

      <OrderDetailDrawer order={selected} onClose={() => setSelected(null)} />
    </div>
  )
}

// ─── Inventory ────────────────────────────────────────────────────────────────

function ResourcesTab() {
  const { t } = useI18n()
  const [page, setPage] = useState(1)
  const limit = 20
  const { data, loading, error, reload } = useApi(`/inventory/resources?page=${page}&limit=${limit}`, [page])
  const { data: stockData } = useApi('/inventory/stock')
  const total = data?.total ?? 0

  // Build a quick lookup: resourceId -> stock item
  const stockMap = {}
  for (const s of (stockData?.items ?? [])) stockMap[s.resourceId] = s

  return (
    <div>
      {error && <div className="mb-3"><ErrorBox message={error} onRetry={reload} /></div>}
      <Card className="mt-4">
        {loading ? <LoadingSpinner label={t('Loading resources…')} /> : (
          <div className="overflow-x-auto"><table className="w-full text-sm min-w-[500px]">
            <thead>
              <tr className="text-[10px] font-bold text-slate-500 uppercase tracking-widest border-b border-white/[0.06]">
                <th className="text-left px-5 py-3">{t('Resource ID')}</th>
                <th className="text-left px-5 py-3">{t('Name')}</th>
                <th className="text-left px-5 py-3">{t('Description')}</th>
                <th className="text-left px-5 py-3">{t('Catalog')}</th>
                <th className="text-center px-5 py-3">{t('In Stock')}</th>
                <th className="text-center px-5 py-3">{t('Sold')}</th>
                <th className="text-left px-5 py-3">{t('Added')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/[0.04]">
              {(data?.items ?? []).map(r => {
                const s = stockMap[r.id]
                return (
                  <tr key={r.id} className="hover:bg-white/[0.025] transition-colors">
                    <td className="px-5 py-3 font-mono text-[11px] text-slate-500 max-w-[140px] truncate" title={r.id}>{r.id}</td>
                    <td className="px-5 py-3 text-slate-200 font-semibold">{r.descriptor_name || '—'}</td>
                    <td className="px-5 py-3 text-slate-400 text-xs max-w-[220px] truncate">{r.descriptor_short_desc || '—'}</td>
                    <td className="px-5 py-3">
                      <span className="text-[11px] bg-white/[0.06] border border-white/[0.08] px-2 py-0.5 rounded-full text-slate-400">{r.catalog_id}</span>
                    </td>
                    <td className="px-5 py-3 text-center">
                      {s
                        ? <StockBadge qty={s.quantity} />
                        : <span className="text-xs text-slate-600">—</span>}
                    </td>
                    <td className="px-5 py-3 text-center text-xs text-slate-400 font-semibold">
                      {s ? s.sold : '—'}
                    </td>
                    <td className="px-5 py-3 text-slate-500 text-xs">{fmtDateShort(r.created_at?.Time || r.created_at)}</td>
                  </tr>
                )
              })}
              {!data?.items?.length && (
                <tr><td colSpan={7} className="px-5 py-12 text-center text-slate-500">{t('No resources published yet')}</td></tr>
              )}
            </tbody>
          </table>
          </div>
        )}
        <Pagination page={page} totalPages={Math.max(1, Math.ceil(total / limit))} onPage={setPage} total={total} limit={limit} />
      </Card>
    </div>
  )
}

// Stock badge: green if > 5, yellow if 1-5, red if 0
function StockBadge({ qty }) {
  const { t } = useI18n()
  if (qty === 0) return (
    <span className="text-xs font-bold px-2 py-0.5 rounded-full" style={{ color: '#ef4444', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.2)' }}>
      {t('Out of Stock')}
    </span>
  )
  if (qty <= 5) return (
    <span className="text-xs font-bold px-2 py-0.5 rounded-full" style={{ color: '#f59e0b', background: 'rgba(245,158,11,0.1)', border: '1px solid rgba(245,158,11,0.2)' }}>
      {t('Low: {qty}', { qty })}
    </span>
  )
  return (
    <span className="text-xs font-bold px-2 py-0.5 rounded-full" style={{ color: '#10b981', background: 'rgba(16,185,129,0.1)', border: '1px solid rgba(16,185,129,0.2)' }}>
      {qty}
    </span>
  )
}

function StockTab() {
  const { t } = useI18n()
  const { data, loading, error, reload } = useApi('/inventory/stock')
  const items = data?.items ?? []

  const totalStock = items.reduce((s, r) => s + r.quantity, 0)
  const totalSold = items.reduce((s, r) => s + r.sold, 0)
  const outOfStock = items.filter(r => r.quantity === 0).length

  return (
    <div>
      {error && <div className="mb-3"><ErrorBox message={error} onRetry={reload} /></div>}

      {/* Quick stat bar */}
      <div className="mt-4 grid grid-cols-3 gap-3 mb-4">
        {[
          { label: t('Total Items'), value: items.length, color: '#00b8e6' },
          { label: t('Units In Stock'), value: totalStock, color: '#10b981' },
          { label: t('Units Sold'), value: totalSold, color: '#e6006e' },
        ].map(({ label, value, color }) => (
          <div key={label} className="rounded-xl p-4 border border-white/[0.07]" style={{ background: '#111827' }}>
            <p className="text-[11px] uppercase tracking-widest font-semibold text-slate-500 mb-1">{label}</p>
            <p className="text-2xl font-bold" style={{ color }}>{loading ? '…' : value}</p>
          </div>
        ))}
      </div>

      {outOfStock > 0 && (
        <div className="mb-4 px-4 py-3 rounded-xl text-sm font-semibold flex items-center gap-2"
          style={{ background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.2)', color: '#ef4444' }}>
          <AlertCircle size={14} />
          {t(outOfStock === 1 ? '{count} resource is out of stock' : '{count} resources are out of stock', { count: outOfStock })}
        </div>
      )}

      <Card>
        {loading ? <LoadingSpinner label={t('Loading stock…')} /> : (
          <div className="overflow-x-auto"><table className="w-full text-sm min-w-[500px]">
            <thead>
              <tr className="text-[10px] font-bold text-slate-500 uppercase tracking-widest border-b border-white/[0.06]">
                <th className="text-left px-5 py-3">{t('Resource')}</th>
                <th className="text-left px-5 py-3">{t('Catalog')}</th>
                <th className="text-center px-5 py-3">{t('In Stock')}</th>
                <th className="text-center px-5 py-3">{t('Sold')}</th>
                <th className="text-left px-5 py-3">{t('Last Updated')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/[0.04]">
              {items.map(r => (
                <tr key={r.resourceId} className="hover:bg-white/[0.025] transition-colors">
                  <td className="px-5 py-3">
                    <p className="text-slate-200 font-semibold text-sm">{r.name}</p>
                    <p className="text-[11px] font-mono text-slate-600 mt-0.5" title={r.resourceId}>{r.resourceId}</p>
                  </td>
                  <td className="px-5 py-3">
                    <span className="text-[11px] bg-white/[0.06] border border-white/[0.08] px-2 py-0.5 rounded-full text-slate-400">{r.catalogId || '—'}</span>
                  </td>
                  <td className="px-5 py-3 text-center"><StockBadge qty={r.quantity} /></td>
                  <td className="px-5 py-3 text-center text-sm font-bold text-slate-400">{r.sold}</td>
                  <td className="px-5 py-3 text-xs text-slate-500">{fmtDate(r.updatedAt)}</td>
                </tr>
              ))}
              {!items.length && (
                <tr><td colSpan={5} className="px-5 py-12 text-center text-slate-500">
                  {t('No stock data yet — publish a catalog with stock quantities to start tracking')}
                </td></tr>
              )}
            </tbody>
          </table>
          </div>
        )}
      </Card>
    </div>
  )
}

function OffersTab() {
  const { t } = useI18n()
  const [page, setPage] = useState(1)
  const limit = 20
  const { data, loading, error, reload } = useApi(`/inventory/offers?page=${page}&limit=${limit}`, [page])
  const total = data?.total ?? 0

  return (
    <div>
      {error && <div className="mb-3"><ErrorBox message={error} onRetry={reload} /></div>}
      <Card className="mt-4">
        {loading ? <LoadingSpinner label={t('Loading offers…')} /> : (
          <div className="overflow-x-auto"><table className="w-full text-sm min-w-[500px]">
            <thead>
              <tr className="text-[10px] font-bold text-slate-500 uppercase tracking-widest border-b border-white/[0.06]">
                <th className="text-left px-5 py-3">{t('Offer ID')}</th>
                <th className="text-left px-5 py-3">{t('Name')}</th>
                <th className="text-left px-5 py-3">{t('Resources')}</th>
                <th className="text-left px-5 py-3">{t('Valid Until')}</th>
                <th className="text-left px-5 py-3">{t('Added')}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-white/[0.04]">
              {(data?.items ?? []).map(o => (
                <tr key={o.id} className="hover:bg-white/[0.025] transition-colors">
                  <td className="px-5 py-3 font-mono text-[11px] text-slate-500 max-w-[140px] truncate" title={o.id}>{o.id}</td>
                  <td className="px-5 py-3 text-slate-200 font-semibold">{o.descriptor_name || '—'}</td>
                  <td className="px-5 py-3">
                    {o.resource_ids?.length
                      ? <span className="text-xs px-2 py-0.5 rounded-full border border-cyan-500/25 font-semibold" style={{ color: '#00b8e6', background: 'rgba(0,184,230,0.1)' }}>
                        {t(o.resource_ids.length === 1 ? '{count} resource' : '{count} resources', { count: o.resource_ids.length })}
                      </span>
                      : <span className="text-slate-600">—</span>
                    }
                  </td>
                  <td className="px-5 py-3 text-slate-500 text-xs">
                    {o.validity_end?.Time ? fmtDateShort(o.validity_end.Time) : '—'}
                  </td>
                  <td className="px-5 py-3 text-slate-500 text-xs">{fmtDateShort(o.created_at?.Time || o.created_at)}</td>
                </tr>
              ))}
              {!data?.items?.length && (
                <tr><td colSpan={5} className="px-5 py-12 text-center text-slate-500">{t('No offers published yet')}</td></tr>
              )}
            </tbody>
          </table>
          </div>
        )}
        <Pagination page={page} totalPages={Math.max(1, Math.ceil(total / limit))} onPage={setPage} total={total} limit={limit} />
      </Card>
    </div>
  )
}

function InventoryProductsTab() {
  const { t } = useI18n()
  const [search, setSearch] = useState('')
  const [catalogFilter, setCatalogFilter] = useState('')
  const [page, setPage] = useState(1)
  const limit = 20

  // provider_id is no longer a query param -- the API always scopes to the
  // logged-in seller's own provider (see internal/dashboardsvc), so there's
  // nothing left to filter by there.
  const url = `/inventory/items?search=${encodeURIComponent(search)}&catalog_id=${encodeURIComponent(catalogFilter)}&page=${page}&limit=${limit}`
  const { data, loading, error, reload } = useApi(url, [search, catalogFilter, page])
  const { data: catData } = useApi('/catalogs?limit=100', [])

  const items = data?.items || []
  const total = data?.total || 0
  const totalPages = Math.max(1, Math.ceil(total / limit))
  const catalogs = catData?.items || []

  // Group items by provider then catalog
  const grouped = {}
  items.forEach(item => {
    const pKey = item.providerName || item.providerId
    const cKey = item.catalogName || item.catalogId
    if (!grouped[pKey]) grouped[pKey] = {}
    if (!grouped[pKey][cKey]) grouped[pKey][cKey] = []
    grouped[pKey][cKey].push(item)
  })

  return (
    <div className="space-y-4 mt-6">
      {/* Filters */}
      <Card className="p-4">
        <div className="flex flex-wrap gap-3 items-center">
          <div className="relative flex-1 min-w-44">
            <Search size={13} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
            <input value={search} onChange={e => { setSearch(e.target.value); setPage(1) }}
              placeholder={t('Search by name or ID…')}
              className="w-full pl-8 pr-3 py-2 rounded-lg text-sm text-white placeholder-slate-600 border border-white/[0.08] bg-slate-900 focus:outline-none focus:border-blue-500/50 transition-colors" />
          </div>
          <select value={catalogFilter} onChange={e => { setCatalogFilter(e.target.value); setPage(1) }}
            className="px-3 py-2 rounded-lg text-sm text-white border border-white/[0.08] bg-slate-900 focus:outline-none">
            <option value="">{t('All Catalogs')}</option>
            {catalogs.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
          <button onClick={reload} className="p-2 rounded-lg border border-white/[0.08] text-slate-400 hover:text-white transition-colors">
            <RefreshCw size={13} />
          </button>
        </div>
      </Card>

      {loading ? <LoadingSpinner /> : error ? <ErrorBox message={error} /> : (
        Object.keys(grouped).length === 0 ? (
          <Card className="p-12 text-center text-slate-500">{t('No products found.')}</Card>
        ) : (
          Object.entries(grouped).map(([providerName, catalogs]) => (
            <div key={providerName} className="space-y-3">
              {/* Provider header */}
              <div className="flex items-center gap-2 px-1">
                <Store size={14} className="text-slate-500" />
                <p className="text-sm font-bold text-slate-300">{providerName}</p>
              </div>
              {Object.entries(catalogs).map(([catalogName, resources]) => (
                <Card key={catalogName}>
                  {/* Catalog sub-header */}
                  <div className="flex items-center gap-2 px-4 py-3 border-b border-white/[0.06]">
                    <BookOpen size={12} className="text-slate-500" />
                    <p className="text-xs font-semibold text-slate-400">{catalogName}</p>
                    <span className="ml-auto text-[10px] text-slate-600">{t('{count} products', { count: resources.length })}</span>
                  </div>
                  <div className="overflow-x-auto"><table className="w-full text-sm min-w-[500px]">
                    <thead>
                      <tr>
                        {['Resource', 'Description', 'Stock', 'Sold'].map(h => (
                          <th key={h} className="px-4 py-2 text-left text-[10px] uppercase tracking-widest text-slate-600 font-semibold">{t(h)}</th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {resources.map(r => (
                        <tr key={r.id} className="border-t border-white/[0.04] hover:bg-white/[0.02] transition-colors">
                          <td className="px-4 py-2.5">
                            <p className="font-medium text-white text-sm">{r.name}</p>
                            <p className="text-[11px] font-mono text-slate-600">{r.id}</p>
                          </td>
                          <td className="px-4 py-2.5 text-xs text-slate-500 max-w-48 truncate">{r.shortDesc || '—'}</td>
                          <td className="px-4 py-2.5">
                            <span className={`text-sm font-bold ${r.stock === 0 ? 'text-red-400' : r.stock <= 5 ? 'text-amber-400' : 'text-emerald-400'}`}>
                              {r.stock}
                            </span>
                          </td>
                          <td className="px-4 py-2.5 text-slate-500 text-sm">{r.sold}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  </div>
                </Card>
              ))}
            </div>
          ))
        )
      )}
      <Pagination page={page} totalPages={totalPages} onPage={setPage} total={total} limit={limit} />
    </div>
  )
}

function InventoryPage({ initialTab = 'products' }) {
  const { t } = useI18n()
  const [tab, setTab] = useState(initialTab)
  return (
    <div>
      <PageHeader breadcrumb={t('Inventory')} title={t('Your Inventory')}
        subtitle={t('Products by provider & catalog, with stock levels')} />
      <div className="flex gap-1 p-1 rounded-xl w-fit max-w-full overflow-x-auto" style={{ background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.07)' }}>
        {['products', 'resources', 'offers', 'stock'].map(k => (
          <button key={k} onClick={() => setTab(k)}
            className={`shrink-0 whitespace-nowrap px-3 sm:px-5 py-2 rounded-lg text-sm font-semibold transition-all ${tab !== k ? 'text-slate-400 hover:text-slate-200' : 'text-white shadow-lg'
              }`}
            style={tab === k ? { background: BRAND } : {}}>
            {k === 'products' ? `🏪 ${t('By Catalog')}` : k === 'resources' ? `📦 ${t('Resources')}` : k === 'offers' ? `🏷️ ${t('Offers')}` : `📊 ${t('Stock')}`}
          </button>
        ))}
      </div>
      {tab === 'products' ? <InventoryProductsTab />
        : tab === 'resources' ? <ResourcesTab />
          : tab === 'offers' ? <OffersTab />
            : <StockTab />}
    </div>
  )
}

// ─── Publish ──────────────────────────────────────────────────────────────────

const EMPTY_RESOURCE = () => ({
  id: '', name: '', short_desc: '', long_desc: '', stock_quantity: '',
  product_highlights: '', specifications: '',
  media_url: '',
  // identity
  brand: '', origin_country: 'IN',
  // physical
  weight_value: '', weight_unit: 'G',
  volume_value: '', volume_unit: 'ML',
  dim_unit: 'CM', dim_length: '', dim_breadth: '', dim_height: '',
  color: '', material: '', finish: '',
  // packaged goods
  mfr_type: 'MANUFACTURER', mfr_name: '', mfr_address: '',
  common_name: '',
  net_qty_value: '', net_qty_unit: 'ML',
})

// IONTaxDetail enums (ion common/Tax/v1) offered in the offer form.
const TAX_REGIMES = ['PPN', 'PPNBM', 'EXEMPT']
const TAX_CATEGORIES = ['BKP', 'JKP', 'NON_BKP']

// Per-unit price breakup for an offer consideration: the entered (pre-tax)
// price as BASE_PRICE plus one TAX line carrying IONTaxDetail. PPN on regular
// goods follows PMK 131/2024 -- 12% on a DPP (taxableBase) of 11/12 of the
// price, i.e. 11% effective; PPNBM applies its rate to the full price; EXEMPT
// declares a zero TAX line. totalAmount = base + tax (tax-exclusive pricing).
function buildPriceBreakup(o) {
  const base = Number(o.price)
  const regime = o.tax_regime || 'PPN'
  const rate = regime === 'EXEMPT' ? 0 : Number(o.tax_rate || 0) / 100
  const taxableBase = Math.round((regime === 'PPN' ? base * 11 / 12 : base) * 100) / 100
  const taxAmount = Math.round(taxableBase * rate)
  return {
    breakup: [
      { title: o.name || o.id, amount: base, type: 'BASE_PRICE' },
      {
        title: regime === 'EXEMPT' ? 'Tax exempt' : `${regime} ${Number(o.tax_rate || 0)}%`,
        amount: taxAmount, type: 'TAX',
        taxDetail: { rate, included: false, taxableBase, taxRegime: regime, taxCategory: o.tax_category || 'BKP' },
      },
    ],
    totalAmount: base + taxAmount,
  }
}

// Tax regime / category / rate inputs for an offer, with a live total preview.
function OfferTaxFields({ offer, onChange, labelCls, inputCls }) {
  const { t } = useI18n()
  const exempt = offer.tax_regime === 'EXEMPT'
  const preview = offer.price ? buildPriceBreakup(offer) : null
  const tax = preview?.breakup.find(b => b.type === 'TAX')
  return (
    <>
      <div><label className={labelCls}>{t('Tax Regime')}</label>
        <select className={inputCls} value={offer.tax_regime || 'PPN'} onChange={e => onChange('tax_regime', e.target.value)}>
          {TAX_REGIMES.map(r => <option key={r} className="bg-slate-900">{r}</option>)}
        </select>
      </div>
      <div><label className={labelCls}>{t('Tax Category')}</label>
        <select className={inputCls} value={offer.tax_category || 'BKP'} onChange={e => onChange('tax_category', e.target.value)}>
          {TAX_CATEGORIES.map(c => <option key={c} className="bg-slate-900">{c}</option>)}
        </select>
      </div>
      <div><label className={labelCls}>{t('Tax Rate (%)')}</label>
        <input type="number" min="0" step="any" className={inputCls} disabled={exempt}
          value={exempt ? '0' : (offer.tax_rate ?? '')} onChange={e => onChange('tax_rate', e.target.value)} placeholder="12" />
      </div>
      {preview && (
        <p className="col-span-full text-[11px] text-slate-500">
          {offer.tax_regime === 'PPN' && <>{t('PPN on DPP 11/12 × price (Rp {amount})', { amount: tax.taxDetail.taxableBase.toLocaleString(localeNow()) })} · </>}
          {t('Tax Rp {amount}', { amount: tax.amount.toLocaleString(localeNow()) })} · {t('Buyer pays')} <span className="text-slate-300 font-semibold">Rp {preview.totalAmount.toLocaleString(localeNow())}</span>
        </p>
      )}
    </>
  )
}

// RetailOffer 2.1 policies.returns.method / policies.replacement.method enum.
const RETURN_METHODS = ['SELLER_PICKUP', 'BUYER_SHIP', 'DROP_OFF']
// RetailOffer 2.1 policies.returns.restockingFee.type enum.
const FEE_TYPES = ['PERCENTAGE', 'FIXED']

const EMPTY_OFFER = () => ({
  id: '', name: '', short_desc: '', resource_ids: '', price: '', currency: 'IDR',
  // tax (price is pre-tax; see buildPriceBreakup)
  tax_regime: 'PPN', tax_category: 'BKP', tax_rate: '12',
  validity_start: '', validity_end: '',
  // offer attributes
  offer_context: 'https://schema.ion.id/release1/schema/extension/trade/TradeOffer/v1/context.jsonld',
  // policies
  returns_allowed: true, returns_window: 'P7D', returns_method: 'SELLER_PICKUP',
  returns_fee_type: '', returns_fee_value: '',
  cancel_allowed: true, cancel_window: 'PT2H', cancel_event: 'BEFORE_PACKING',
  replace_allowed: true, replace_window: 'P7D', replace_method: 'SELLER_PICKUP', replace_subject_avail: true,
  // payment & serviceability
  cod_available: true,
  max_distance: '15', distance_unit: 'KM',
  timing_days: ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN'],
  timing_start: '09:00', timing_end: '21:00',
})

const CATALOG_TYPES = ['', 'regular']
const ALL_DAYS = ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN']

// ---------------------------------------------------------------------------
// Auto-fill seed data — pool of real Indonesian snack products.
// Each entry maps 1:1 to a complete form state (catalog + 1 resource + 1 offer).
// ---------------------------------------------------------------------------
const AUTO_FILL_POOL = [
  {
    catalog_id: 'cat-khong-guan-001', catalog_name: 'Toko Camilan Nusantara', catalog_type: '',
    provider_id: 'provider-toko-nusantara-001', provider_name: 'Toko Camilan Nusantara',
    validity_start: '2026-04-11', validity_end: '2027-04-11',
    resources: [{
      ...EMPTY_RESOURCE(),
      id: 'res-biscuit-khong-guan-001',
      name: 'Biskuit Kaleng Khong Guan (Khong Guan Assorted Biscuits Tin)',
      short_desc: 'Aneka biskuit renyah dalam kaleng ikonik (Assorted crispy biscuits in iconic tin)',
      long_desc: 'Koleksi biskuit klasik Khong Guan dengan berbagai rasa: vanila, cokelat, dan keju. Hadir dalam kaleng dekoratif yang bisa digunakan kembali. (Classic Khong Guan biscuit collection with vanilla, chocolate, and cheese varieties in a reusable decorative tin.)',
      media_url: 'https://images.unsplash.com/photo-1558961363-fa8fdf82db35?w=800&q=80',
      stock_quantity: '200', brand: 'Khong Guan', origin_country: 'ID',
      weight_value: '1600', weight_unit: 'G', color: 'Red', material: 'Tin', finish: 'Glossy',
      mfr_type: 'MANUFACTURER', mfr_name: 'Khong Guan Biscuit Factory Pte Ltd', mfr_address: 'Jakarta, DKI Jakarta, ID',
      common_name: 'Biskuit Assorted (Assorted Biscuits)', net_qty_value: '1600', net_qty_unit: 'G',
    }],
    offers: [{
      ...EMPTY_OFFER(),
      id: 'offer-biscuit-khong-guan-001', name: 'Biskuit Kaleng Khong Guan (Khong Guan Assorted Biscuits Tin)',
      short_desc: 'Aneka biskuit renyah dalam kaleng ikonik', resource_ids: 'res-biscuit-khong-guan-001',
      price: '185000', currency: 'IDR', validity_start: '2026-04-11', validity_end: '2027-04-11',
    }],
  },
  {
    catalog_id: 'cat-chitato-001', catalog_name: 'Toko Camilan Nusantara', catalog_type: '',
    provider_id: 'provider-toko-nusantara-001', provider_name: 'Toko Camilan Nusantara',
    validity_start: '2026-04-11', validity_end: '2027-04-11',
    resources: [{
      ...EMPTY_RESOURCE(),
      id: 'res-chips-chitato-sapi-003',
      name: 'Keripik Chitato Rasa Sapi Panggang (Chitato Beef BBQ Potato Chips)',
      short_desc: 'Keripik kentang gurih rasa sapi panggang (Crunchy potato chips with BBQ beef flavor)',
      long_desc: 'Chitato hadir dengan rasa Sapi Panggang yang kaya dan bumbu khas Indonesia. Dibuat dari kentang pilihan yang diproses dengan teknologi modern untuk tekstur renyah sempurna. (Chitato with rich BBQ Beef flavor and distinctive Indonesian seasoning, made from selected potatoes for perfect crunch.)',
      media_url: 'https://images.unsplash.com/photo-1621939514649-280e2ee25f60?w=800&q=80',
      stock_quantity: '1000', brand: 'Chitato', origin_country: 'ID',
      weight_value: '68', weight_unit: 'G', color: 'Red', material: 'Plastic', finish: 'Glossy',
      mfr_type: 'MANUFACTURER', mfr_name: 'PT Indofood CBP Sukses Makmur Tbk', mfr_address: 'Jakarta, DKI Jakarta, ID',
      common_name: 'Keripik Kentang (Potato Chips)', net_qty_value: '68', net_qty_unit: 'G',
    }],
    offers: [{
      ...EMPTY_OFFER(),
      id: 'offer-chips-chitato-sapi-003', name: 'Keripik Chitato Rasa Sapi Panggang (Chitato Beef BBQ Potato Chips)',
      short_desc: 'Keripik kentang gurih rasa sapi panggang', resource_ids: 'res-chips-chitato-sapi-003',
      price: '12000', currency: 'IDR', validity_start: '2026-04-11', validity_end: '2027-04-11',
      returns_allowed: false, replace_window: 'P3D',
    }],
  },
  {
    catalog_id: 'cat-lapis-legit-001', catalog_name: 'Toko Camilan Nusantara', catalog_type: '',
    provider_id: 'provider-toko-nusantara-001', provider_name: 'Toko Camilan Nusantara',
    validity_start: '2026-04-11', validity_end: '2027-04-11',
    resources: [{
      ...EMPTY_RESOURCE(),
      id: 'res-cake-lapis-legit-005',
      name: 'Kue Lapis Legit Premium (Premium Spekkoek Layered Cake)',
      short_desc: 'Kue lapis klasik Belanda-Indonesia dengan lapisan sempurna (Classic Dutch-Indonesian layered cake)',
      long_desc: 'Lapis Legit adalah kue tradisional warisan Belanda-Indonesia yang dibuat dengan lebih dari 18 lapisan tipis. Setiap lapisan dipanggang satu per satu menggunakan rempah pilihan seperti kayu manis, cengkeh, dan kapulaga. (Lapis Legit is a traditional Dutch-Indonesian heritage cake with over 18 thin layers, each baked individually using selected spices like cinnamon, cloves, and cardamom.)',
      media_url: 'https://images.unsplash.com/photo-1578985545062-69928b1d9587?w=800&q=80',
      stock_quantity: '50', brand: 'Dapur Lapis', origin_country: 'ID',
      weight_value: '700', weight_unit: 'G', color: 'Brown', material: 'Paper Box', finish: 'Matte',
      mfr_type: 'MANUFACTURER', mfr_name: 'CV Dapur Lapis Nusantara', mfr_address: 'Bandung, Jawa Barat, ID',
      common_name: 'Kue Lapis Legit (Spekkoek Cake)', net_qty_value: '700', net_qty_unit: 'G',
    }],
    offers: [{
      ...EMPTY_OFFER(),
      id: 'offer-cake-lapis-legit-005', name: 'Kue Lapis Legit Premium (Premium Spekkoek Layered Cake)',
      short_desc: 'Kue lapis klasik Belanda-Indonesia', resource_ids: 'res-cake-lapis-legit-005',
      price: '185000', currency: 'IDR', validity_start: '2026-04-11', validity_end: '2027-04-11',
      returns_allowed: false, replace_allowed: false, max_distance: '10',
    }],
  },
  {
    catalog_id: 'cat-monde-butter-001', catalog_name: 'Toko Camilan Nusantara', catalog_type: '',
    provider_id: 'provider-toko-nusantara-001', provider_name: 'Toko Camilan Nusantara',
    validity_start: '2026-04-11', validity_end: '2027-04-11',
    resources: [{
      ...EMPTY_RESOURCE(),
      id: 'res-biscuit-monde-butter-007',
      name: 'Biskuit Monde Butter Cookies (Monde Butter Cookies)',
      short_desc: 'Kue kering butter premium dalam kaleng cantik (Premium butter cookies in a beautiful tin)',
      long_desc: 'Monde Butter Cookies hadir dalam kaleng premium dengan pilihan cookies berbentuk bunga, pretzel, dan cinnamon. Dibuat dari butter pilihan berkualitas tinggi dengan tekstur yang lumer di mulut. (Monde Butter Cookies in premium tin with flower-shaped, pretzel, and cinnamon cookie varieties. Made from high-quality selected butter with a melt-in-your-mouth texture.)',
      media_url: 'https://images.unsplash.com/photo-1548365328-8c6db3220e4c?w=800&q=80',
      stock_quantity: '150', brand: 'Monde', origin_country: 'ID',
      weight_value: '454', weight_unit: 'G', color: 'Blue', material: 'Tin', finish: 'Glossy',
      mfr_type: 'MANUFACTURER', mfr_name: 'PT Monde Mahkota Biskuit', mfr_address: 'Jakarta, DKI Jakarta, ID',
      common_name: 'Kue Kering Butter (Butter Cookies)', net_qty_value: '454', net_qty_unit: 'G',
    }],
    offers: [{
      ...EMPTY_OFFER(),
      id: 'offer-biscuit-monde-butter-007', name: 'Biskuit Monde Butter Cookies (Monde Butter Cookies)',
      short_desc: 'Kue kering butter premium dalam kaleng cantik', resource_ids: 'res-biscuit-monde-butter-007',
      price: '120000', currency: 'IDR', validity_start: '2026-04-11', validity_end: '2027-04-11',
    }],
  },
  {
    catalog_id: 'cat-brownies-amanda-001', catalog_name: 'Toko Camilan Nusantara', catalog_type: '',
    provider_id: 'provider-toko-nusantara-001', provider_name: 'Toko Camilan Nusantara',
    validity_start: '2026-04-11', validity_end: '2027-04-11',
    resources: [{
      ...EMPTY_RESOURCE(),
      id: 'res-cake-brownies-amanda-009',
      name: 'Brownies Kukus Amanda Cokelat (Amanda Steamed Chocolate Brownies)',
      short_desc: 'Brownies kukus lembut khas Bandung dengan cokelat premium (Soft steamed Bandung-style brownies)',
      long_desc: 'Brownies Kukus Amanda adalah ikon kuliner Bandung yang terkenal. Dibuat dengan cokelat premium, teksturnya sangat lembut dan basah. Tersedia dalam varian original, keju, dan tiramisu. (Amanda Steamed Brownies are a famous Bandung culinary icon. Made with premium chocolate, extremely soft and moist. Available in original, cheese, and tiramisu variants.)',
      media_url: 'https://images.unsplash.com/photo-1606313564200-e75d5e30476c?w=800&q=80',
      stock_quantity: '60', brand: 'Amanda', origin_country: 'ID',
      weight_value: '800', weight_unit: 'G', color: 'Brown', material: 'Cardboard', finish: 'Matte',
      mfr_type: 'MANUFACTURER', mfr_name: 'CV Amanda Brownies', mfr_address: 'Bandung, Jawa Barat, ID',
      common_name: 'Brownies Kukus (Steamed Brownies)', net_qty_value: '800', net_qty_unit: 'G',
    }],
    offers: [{
      ...EMPTY_OFFER(),
      id: 'offer-cake-brownies-amanda-009', name: 'Brownies Kukus Amanda Cokelat (Amanda Steamed Chocolate Brownies)',
      short_desc: 'Brownies kukus lembut khas Bandung dengan cokelat premium', resource_ids: 'res-cake-brownies-amanda-009',
      price: '95000', currency: 'IDR', validity_start: '2026-04-11', validity_end: '2027-04-11',
      returns_allowed: false, replace_allowed: false, max_distance: '10',
    }],
  },
]

// Build resourceAttributes JSON-LD object from flat form fields.
function buildResourceAttrs(r) {
  const hasIdentity = r.brand || r.origin_country
  const hasPhysical = r.weight_value || r.volume_value || r.dim_length || r.color || r.material || r.finish
  const hasPkg = r.mfr_name || r.common_name || r.net_qty_value
  if (!hasIdentity && !hasPhysical && !hasPkg && !r.product_highlights && !r.specifications) return undefined

  const attrs = {
    '@context': 'https://schema.ion.id/release1/schema/extension/trade/TradeResource/v1/context.jsonld',
    '@type': 'ion:IONTradeResource',
  }
  if (r.product_highlights) attrs.productHighlights = r.product_highlights
  if (r.specifications) attrs.specifications = r.specifications
  if (hasIdentity) {
    attrs.identity = {}
    if (r.brand) attrs.identity.brand = r.brand
    if (r.origin_country) attrs.identity.originCountry = r.origin_country
  }
  if (hasPhysical) {
    attrs.physical = {}
    if (r.weight_value) attrs.physical.weight = { unitCode: r.weight_unit, unitQuantity: Number(r.weight_value) }
    if (r.volume_value) attrs.physical.volume = { unitCode: r.volume_unit, unitQuantity: Number(r.volume_value) }
    if (r.dim_length || r.dim_breadth || r.dim_height)
      attrs.physical.dimensions = { unit: r.dim_unit, length: Number(r.dim_length) || 0, breadth: Number(r.dim_breadth) || 0, height: Number(r.dim_height) || 0 }
    if (r.color || r.material || r.finish) {
      attrs.physical.appearance = {}
      if (r.color) attrs.physical.appearance.color = r.color
      if (r.material) attrs.physical.appearance.material = r.material
      if (r.finish) attrs.physical.appearance.finish = r.finish
    }
  }
  if (hasPkg) {
    attrs.packagedGoodsDeclaration = {}
    if (r.mfr_name) attrs.packagedGoodsDeclaration.manufacturerOrPacker = { type: r.mfr_type, name: r.mfr_name, ...(r.mfr_address ? { address: r.mfr_address } : {}) }
    if (r.common_name) attrs.packagedGoodsDeclaration.commonOrGenericName = r.common_name
    if (r.net_qty_value) attrs.packagedGoodsDeclaration.netQuantity = { unitCode: r.net_qty_unit, unitQuantity: Number(r.net_qty_value) }
  }
  return attrs
}

// Build offerAttributes JSON-LD from flat form fields.
// Returns undefined when no meaningful attributes are set so the field is
// omitted from the payload entirely (avoids sending a broken @context to CDS).
// Reverse of buildResourceAttrs + the publish descriptor: a stored resource
// (GET /catalogs/:id/resources/:rid) back into the Add Product form's fields.
function resourceToForm(r) {
  const a = r.resourceAttributes || {}
  const id = a.identity || {}, ph = a.physical || {}, ap = ph.appearance || {}, dim = ph.dimensions || {}
  const pkg = a.packagedGoodsDeclaration || {}, mfr = pkg.manufacturerOrPacker || {}
  const s = v => (v == null ? '' : String(v))
  return {
    ...EMPTY_RESOURCE(),
    id: r.id, name: r.descriptor?.name || '', short_desc: r.descriptor?.shortDesc || '', long_desc: r.descriptor?.longDesc || '',
    media_url: r.descriptor?.mediaFile?.[0]?.uri || '',
    stock_quantity: r.stockQuantity ? String(r.stockQuantity) : '',
    product_highlights: a.productHighlights || '', specifications: a.specifications || '',
    brand: id.brand || '', origin_country: id.originCountry || '',
    weight_value: s(ph.weight?.unitQuantity), weight_unit: ph.weight?.unitCode || 'G',
    volume_value: s(ph.volume?.unitQuantity), volume_unit: ph.volume?.unitCode || 'ML',
    dim_unit: dim.unit || 'CM', dim_length: s(dim.length || ''), dim_breadth: s(dim.breadth || ''), dim_height: s(dim.height || ''),
    color: ap.color || '', material: ap.material || '', finish: ap.finish || '',
    mfr_type: mfr.type || 'MANUFACTURER', mfr_name: mfr.name || '', mfr_address: mfr.address || '',
    common_name: pkg.commonOrGenericName || '',
    net_qty_value: s(pkg.netQuantity?.unitQuantity), net_qty_unit: pkg.netQuantity?.unitCode || 'ML',
  }
}

// Reverse of buildOfferAttrs + buildPriceBreakup: a stored offer
// (GET /catalogs/:id/offers/:oid) back into the offer form's fields.
function offerToForm(o) {
  const a = o.offerAttributes || {}
  const pol = a.policies || {}, ret = pol.returns || {}, can = pol.cancellation || {}, rep = pol.replacement || {}
  const svc = a.serviceability || {}, timing = svc.timing?.[0] || {}
  const ca = o.considerations?.[0]?.considerationAttributes || {}
  const base = (ca.breakup || []).find(b => b.type === 'BASE_PRICE')
  const tax = (ca.breakup || []).find(b => b.type === 'TAX')
  const local = iso => { if (!iso) return ''; const d = new Date(iso); return isNaN(d) ? '' : new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16) }
  const blank = EMPTY_OFFER()
  return {
    ...blank,
    id: o.id, name: o.descriptor?.name || '', short_desc: o.descriptor?.shortDesc || '',
    resource_ids: (o.resourceIds || []).join(', '),
    price: base ? String(base.amount) : ca.totalAmount != null ? String(ca.totalAmount) : '',
    currency: ca.currency || 'IDR',
    tax_regime: tax?.taxDetail?.taxRegime || (tax ? 'PPN' : 'EXEMPT'),
    tax_category: tax?.taxDetail?.taxCategory || 'BKP',
    tax_rate: tax?.taxDetail?.rate != null ? String(Math.round(tax.taxDetail.rate * 10000) / 100) : blank.tax_rate,
    validity_start: local(o.validity?.startDate), validity_end: local(o.validity?.endDate),
    offer_context: a['@context'] || blank.offer_context,
    returns_allowed: !!ret.allowed, returns_window: ret.window || blank.returns_window, returns_method: ret.method || blank.returns_method,
    returns_fee_type: ret.restockingFee?.type || '', returns_fee_value: ret.restockingFee?.value != null ? String(ret.restockingFee.value) : '',
    cancel_allowed: !!can.allowed, cancel_window: can.window || blank.cancel_window, cancel_event: can.cutoffEvent || blank.cancel_event,
    replace_allowed: !!rep.allowed, replace_window: rep.window || blank.replace_window, replace_method: rep.method || blank.replace_method,
    replace_subject_avail: rep.subjectToAvailability ?? blank.replace_subject_avail,
    cod_available: !!a.paymentConstraints?.codAvailable,
    max_distance: svc.distanceConstraint?.maxDistance != null ? String(svc.distanceConstraint.maxDistance) : '',
    distance_unit: svc.distanceConstraint?.unit || 'KM',
    timing_days: timing.daysOfWeek || [], timing_start: timing.timeRange?.start || '09:00', timing_end: timing.timeRange?.end || '21:00',
  }
}

function buildOfferAttrs(o) {
  const hasPolicies = o.returns_allowed || o.cancel_allowed || o.replace_allowed
  const hasPayment = o.cod_available
  const hasServiceability = o.max_distance || (o.timing_days && o.timing_days.length > 0)
  const hasContext = o.offer_context

  if (!hasPolicies && !hasPayment && !hasServiceability && !hasContext) return undefined

  const attrs = { '@type': 'ion:IONTradeOffer' }
  if (hasContext) attrs['@context'] = o.offer_context

  if (hasPolicies) {
    attrs.policies = {
      returns: {
        allowed: !!o.returns_allowed,
        ...(o.returns_allowed ? { window: o.returns_window, method: o.returns_method } : {}),
        // RetailOffer 2.1 policies.returns.restockingFee: { type: PERCENTAGE|FIXED, value }
        ...(o.returns_allowed && o.returns_fee_type && o.returns_fee_value !== '' && o.returns_fee_value != null
          ? { restockingFee: { type: o.returns_fee_type, value: Number(o.returns_fee_value) } } : {}),
      },
      cancellation: { allowed: !!o.cancel_allowed, ...(o.cancel_allowed ? { window: o.cancel_window, cutoffEvent: o.cancel_event } : {}) },
      replacement: { allowed: !!o.replace_allowed, ...(o.replace_allowed ? { window: o.replace_window, method: o.replace_method, subjectToAvailability: !!o.replace_subject_avail } : {}) },
    }
  }
  if (hasPayment) attrs.paymentConstraints = { codAvailable: true }

  if (hasServiceability) {
    attrs.serviceability = {}
    if (o.max_distance) {
      attrs.serviceability.distanceConstraint = { maxDistance: Number(o.max_distance), unit: o.distance_unit || 'KM' }
    }
    if (o.timing_days && o.timing_days.length > 0) {
      attrs.serviceability.timing = [{ daysOfWeek: o.timing_days, timeRange: { start: o.timing_start || '09:00', end: o.timing_end || '21:00' } }]
    }
  }
  return attrs
}

function PublishPage({ onNav }) {
  const { t } = useI18n()
  const [step, setStep] = useState(0) // 0=catalog, 1=resources, 2=offers, 3=review
  const [form, setForm] = useState({
    catalog_id: '', catalog_name: '', provider_id: '', provider_name: '', catalog_type: '',
    validity_start: '', validity_end: '',
    resources: [EMPTY_RESOURCE()], offers: [EMPTY_OFFER()],
  })
  const [error, setError] = useState(null)
  const [loading, setLoading] = useState(false)
  const [autoFillIdx, setAutoFillIdx] = useState(0)
  const ownProvider = useOwnProvider()
  // The provider is the seller's own account, never typed in.
  useEffect(() => {
    if (ownProvider) setForm(f => ({ ...f, provider_id: ownProvider.id, provider_name: ownProvider.name }))
  }, [ownProvider])
  // track which resource/offer "advanced" sections are expanded
  const [resOpen, setResOpen] = useState({})
  const [offOpen, setOffOpen] = useState({})

  // Cycle through AUTO_FILL_POOL on each click so consecutive clicks give different products.
  // A short hex suffix is appended to every ID to guarantee uniqueness across publishes.
  const handleAutoFill = () => {
    const seed = AUTO_FILL_POOL[autoFillIdx % AUTO_FILL_POOL.length]
    const suffix = Date.now().toString(36) // e.g. "lf3k2a"
    const uid = id => `${id}-${suffix}`

    setForm({
      ...seed,
      ...(ownProvider ? { provider_id: ownProvider.id, provider_name: ownProvider.name } : {}),
      catalog_id: uid(seed.catalog_id),
      resources: seed.resources.map(r => ({ ...r, id: uid(r.id) })),
      offers: seed.offers.map(o => ({
        ...o,
        id: uid(o.id),
        resource_ids: o.resource_ids.split(',').map(rid => uid(rid.trim())).join(', '),
      })),
    })
    setAutoFillIdx(i => i + 1)
    setResOpen({})
    setOffOpen({})
    setStep(0)
  }

  const STEPS = ['Catalog Info', 'Resources', 'Offers', 'Review & Publish']

  const setField = (k, v) => setForm(f => ({ ...f, [k]: v }))
  const setResourceField = (i, k, v) => setForm(f => { const r = [...f.resources]; r[i] = { ...r[i], [k]: v }; return { ...f, resources: r } })
  const setOfferField = (i, k, v) => setForm(f => { const o = [...f.offers]; o[i] = { ...o[i], [k]: v }; return { ...f, offers: o } })
  const toggleDay = (i, day) => setForm(f => {
    const o = [...f.offers]; const days = o[i].timing_days
    o[i] = { ...o[i], timing_days: days.includes(day) ? days.filter(d => d !== day) : [...days, day] }
    return { ...f, offers: o }
  })
  const toISO = dt => dt ? new Date(dt).toISOString() : undefined

  const handleSubmit = async () => {
    setLoading(true); setError(null)
    try {
      const catStart = toISO(form.validity_start), catEnd = toISO(form.validity_end)
      const catalog = {
        id: form.catalog_id,
        descriptor: { name: form.catalog_name },
        provider: { id: form.provider_id, descriptor: { name: form.provider_name } },
        ...(catStart ? { validity: { startDate: catStart, endDate: catEnd } } : {}),
        resources: form.resources.map(r => {
          const mediaFile = r.media_url ? [{ uri: r.media_url, mimeType: 'image/jpeg' }] : undefined
          const attrs = buildResourceAttrs(r)
          return {
            id: r.id,
            descriptor: {
              name: r.name, shortDesc: r.short_desc, longDesc: r.long_desc,
              ...(mediaFile ? { mediaFile } : {}),
            },
            ...(attrs ? { resourceAttributes: attrs } : {}),
            ...(r.stock_quantity !== '' && !isNaN(Number(r.stock_quantity)) ? { stockQuantity: Number(r.stock_quantity) } : {}),
          }
        }),
        offers: form.offers.map(o => {
          const start = toISO(o.validity_start), end = toISO(o.validity_end)
          const offerAttrs = buildOfferAttrs(o)
          return {
            id: o.id,
            descriptor: { name: o.name, shortDesc: o.short_desc },
            resourceIds: o.resource_ids.split(',').map(s => s.trim()).filter(Boolean),
            ...(o.price ? {
              considerations: [{
                id: `${o.id}-price`, status: { code: 'ACTIVE', name: 'ACTIVE' },
                considerationAttributes: {
                  '@context': 'https://schema.ion.id/release1/schema/extension/trade/TradeConsideration/v1/context.jsonld',
                  '@type': 'ion:IONTradeConsideration',
                  currency: o.currency,
                  ...buildPriceBreakup(o),
                  paymentMethods: o.cod_available ? ['PREPAID', 'COD', 'DIGITAL_WALLET'] : ['PREPAID', 'DIGITAL_WALLET'],
                }
              }]
            } : {}),
            ...(start ? { validity: { startDate: start, endDate: end } } : {}),
            ...(offerAttrs ? { offerAttributes: offerAttrs } : {}),
          }
        }),
        ...(form.catalog_type ? { publishDirectives: { catalogType: form.catalog_type } } : {}),
      }
      await apiFetch('/catalog/publish', { method: 'POST', body: JSON.stringify({ catalogs: [catalog] }) })
      onNav('inventory')
    } catch (err) { setError(err.message) }
    finally { setLoading(false) }
  }

  const inputCls = 'w-full bg-slate-900 border border-white/[0.08] rounded-xl px-3.5 py-2.5 text-sm text-slate-200 placeholder-slate-600 focus:outline-none focus:border-cyan-500/40 focus:bg-slate-800 transition-all'
  const labelCls = 'block text-xs font-semibold text-slate-400 mb-1.5'

  return (
    <div className="max-w-3xl">
      <div className="flex items-start justify-between gap-4 mb-6">
        <PageHeader breadcrumb={t('Publish Catalog')} title={t('Publish to Network')}
          subtitle={t('Push your resources and offers to the ION network')} />
        <button onClick={handleAutoFill}
          className="shrink-0 flex items-center gap-2 px-4 py-2.5 rounded-xl text-sm font-semibold border border-dashed border-cyan-500/40 text-cyan-400 hover:bg-cyan-500/10 hover:border-cyan-500/70 transition-all active:scale-95 mt-1"
          title={t('Auto-fill form with a sample Indonesian product')}>
          <Wand2 size={15} /> {t('Auto-fill Sample')}
        </button>
      </div>

      {/* Stepper */}
      <div className="flex items-center mb-8">
        {STEPS.map((s, i) => (
          <div key={i} className="flex items-center flex-1 last:flex-none">
            <button onClick={() => i < step && setStep(i)}
              className={`flex items-center gap-2 text-xs font-semibold transition-all ${i < step ? 'cursor-pointer' : 'cursor-default'
                }`}>
              <div className={`w-7 h-7 rounded-full flex items-center justify-center text-[11px] font-bold transition-all ${i < step ? 'bg-emerald-500 text-white' :
                i === step ? 'text-white' : 'bg-slate-800 text-slate-500'
                }`}
                style={i === step ? { background: BRAND } : {}}>
                {i < step ? <CheckCircle2 size={14} /> : i + 1}
              </div>
              <span className={i === step ? 'text-white' : i < step ? 'text-emerald-400' : 'text-slate-500'}>{t(s)}</span>
            </button>
            {i < STEPS.length - 1 && (
              <div className="flex-1 mx-3 h-px" style={{ background: i < step ? '#10b981' : 'rgba(255,255,255,0.08)' }} />
            )}
          </div>
        ))}
      </div>

      {error && <div className="mb-4"><ErrorBox message={error} /></div>}

      <AnimatePresence mode="wait">
        <motion.div key={step}
          initial={{ opacity: 0, x: 20 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: -20 }}
          transition={{ duration: 0.18 }}>

          {/* ── Step 0: Catalog Info ── */}
          {step === 0 && (
            <Card className="p-6 space-y-5">
              <p className="text-sm font-bold text-white">{t('Catalog Details')}</p>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <div><label className={labelCls}>{t('Catalog ID *')}</label>
                  <input className={inputCls} value={form.catalog_id} onChange={e => setField('catalog_id', e.target.value)} placeholder="cat-venky-bazaar-2026" required />
                </div>
                <div><label className={labelCls}>{t('Catalog Name *')}</label>
                  <input className={inputCls} value={form.catalog_name} onChange={e => setField('catalog_name', e.target.value)} placeholder={t('Venky Bazaar Catalog')} required />
                </div>
                <div><label className={labelCls}>{t('Provider ID *')}</label>
                  <input className={inputCls + ' opacity-60 cursor-not-allowed'} value={form.provider_id} readOnly placeholder={t('Loading…')}
                    title={t('Your provider comes from your account')} />
                </div>
                <div><label className={labelCls}>{t('Provider Name *')}</label>
                  <input className={inputCls + ' opacity-60 cursor-not-allowed'} value={form.provider_name} readOnly placeholder={t('Loading…')}
                    title={t('Your provider comes from your account')} />
                </div>
                <div className="col-span-2">
                  <label className={labelCls}>{t('Catalog Type')} <span className="text-slate-600 font-normal">({t('optional')})</span></label>
                  <select className={inputCls} value={form.catalog_type} onChange={e => setField('catalog_type', e.target.value)}>
                    {CATALOG_TYPES.map(c => <option key={c} value={c}>{c || t('— select type —')}</option>)}
                  </select>
                </div>
              </div>
              {/* Catalog validity */}
              <div className="pt-3 border-t border-white/[0.06]">
                <p className="text-xs font-bold text-slate-400 uppercase tracking-widest mb-3">{t('Catalog Validity')} <span className="text-slate-600 font-normal normal-case">({t('optional')})</span></p>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                  <div><label className={labelCls}>{t('Valid From')}</label>
                    <input type="datetime-local" className={inputCls} value={form.validity_start} onChange={e => setField('validity_start', e.target.value)} />
                  </div>
                  <div><label className={labelCls}>{t('Valid Until')}</label>
                    <input type="datetime-local" className={inputCls} value={form.validity_end} onChange={e => setField('validity_end', e.target.value)} />
                  </div>
                </div>
              </div>
            </Card>
          )}

          {/* ── Step 1: Resources ── */}
          {step === 1 && (
            <div className="space-y-3 max-w-2xl">
              {form.resources.map((r, i) => (
                <Card key={i} className="p-5">
                  {/* Card header */}
                  <div className="flex items-center justify-between mb-4">
                    <div className="flex items-center gap-2">
                      <div className="w-6 h-6 rounded-md flex items-center justify-center text-[11px] font-bold text-white" style={{ background: BRAND }}>{i + 1}</div>
                      <p className="text-sm font-bold text-white">{t('Resource {n}', { n: i + 1 })}</p>
                    </div>
                    {form.resources.length > 1 && (
                      <button onClick={() => setForm(f => ({ ...f, resources: f.resources.filter((_, j) => j !== i) }))}
                        className="text-xs text-red-400 hover:text-red-300 flex items-center gap-1 transition-colors">
                        <Trash2 size={12} /> {t('Remove')}
                      </button>
                    )}
                  </div>

                  {/* Basic info */}
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    <div><label className={labelCls}>{t('Resource ID *')}</label>
                      <input className={inputCls} value={r.id} onChange={e => setResourceField(i, 'id', e.target.value)} placeholder="item-flask-mh500-yellow" required />
                    </div>
                    <div><label className={labelCls}>{t('Name *')}</label>
                      <input className={inputCls} value={r.name} onChange={e => setResourceField(i, 'name', e.target.value)} placeholder={t('Stainless Steel Hiking Flask')} required />
                    </div>
                    <div><label className={labelCls}>{t('Short Description')}</label>
                      <input className={inputCls} value={r.short_desc} onChange={e => setResourceField(i, 'short_desc', e.target.value)} placeholder={t('500ml insulated flask')} />
                    </div>
                    <div><label className={labelCls}>{t('Long Description')}</label>
                      <textarea className={inputCls} rows="2" value={r.long_desc} onChange={e => setResourceField(i, 'long_desc', e.target.value)} placeholder={t('Full product description…')} />
                    </div>
                  </div>

                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-3">
                    <div><label className={labelCls}>{t('Product Highlights')}</label>
                      <textarea className={inputCls} rows="3" value={r.product_highlights} onChange={e => setResourceField(i, 'product_highlights', e.target.value)} placeholder={t('List highlights separated by newlines…')} />
                    </div>
                    <div><label className={labelCls}>{t('Specifications')}</label>
                      <textarea className={inputCls} rows="3" value={r.specifications} onChange={e => setResourceField(i, 'specifications', e.target.value)} placeholder={t('List specifications separated by newlines…')} />
                    </div>
                  </div>

                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-3">
                    <div><label className={labelCls}>{t('Stock Quantity')} <span className="text-slate-600 font-normal">({t('blank = unlimited')})</span></label>
                      <input className={inputCls} type="number" min="0" value={r.stock_quantity} onChange={e => setResourceField(i, 'stock_quantity', e.target.value)} placeholder={t('e.g. 50')} />
                    </div>
                    <div><label className={labelCls}>{t('Image URL')} <span className="text-slate-600 font-normal">({t('optional')})</span></label>
                      <input className={inputCls} value={r.media_url} onChange={e => setResourceField(i, 'media_url', e.target.value)} placeholder="https://example.com/product.jpg" />
                    </div>
                  </div>

                  {/* Identity & Physical — collapsible */}
                  <button onClick={() => setResOpen(s => ({ ...s, [`${i}_phys`]: !s[`${i}_phys`] }))}
                    className="mt-4 w-full flex items-center justify-between text-xs font-semibold text-slate-400 hover:text-slate-200 transition-colors pt-3 border-t border-white/[0.06]">
                    <span>🏷️ {t('Identity & Physical Attributes')}</span>
                    <ChevronRightIcon size={13} className={`transition-transform ${resOpen[`${i}_phys`] ? 'rotate-90' : ''}`} />
                  </button>
                  {resOpen[`${i}_phys`] && (
                    <div className="mt-3 space-y-4">
                      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                        <div><label className={labelCls}>{t('Brand')}</label>
                          <input className={inputCls} value={r.brand} onChange={e => setResourceField(i, 'brand', e.target.value)} placeholder="InstaCuppa" />
                        </div>
                        <div><label className={labelCls}>{t('Origin Country')}</label>
                          <input className={inputCls} value={r.origin_country} onChange={e => setResourceField(i, 'origin_country', e.target.value)} placeholder="IN" />
                        </div>
                      </div>
                      <p className="text-[10px] uppercase tracking-widest text-slate-600 font-bold">{t('Weight & Volume')}</p>
                      <div className="grid grid-cols-4 gap-2">
                        <div className="col-span-2"><label className={labelCls}>{t('Weight')}</label>
                          <input className={inputCls} type="number" value={r.weight_value} onChange={e => setResourceField(i, 'weight_value', e.target.value)} placeholder="350" />
                        </div>
                        <div className="col-span-2"><label className={labelCls}>{t('Unit')}</label>
                          <select className={inputCls} value={r.weight_unit} onChange={e => setResourceField(i, 'weight_unit', e.target.value)}>
                            {['G', 'KG', 'MG', 'OZ', 'LB'].map(u => <option key={u}>{u}</option>)}
                          </select>
                        </div>
                        <div className="col-span-2"><label className={labelCls}>{t('Volume')}</label>
                          <input className={inputCls} type="number" value={r.volume_value} onChange={e => setResourceField(i, 'volume_value', e.target.value)} placeholder="500" />
                        </div>
                        <div className="col-span-2"><label className={labelCls}>{t('Unit')}</label>
                          <select className={inputCls} value={r.volume_unit} onChange={e => setResourceField(i, 'volume_unit', e.target.value)}>
                            {['ML', 'L', 'FL_OZ'].map(u => <option key={u}>{u}</option>)}
                          </select>
                        </div>
                      </div>
                      <p className="text-[10px] uppercase tracking-widest text-slate-600 font-bold">{t('Dimensions')}</p>
                      <div className="grid grid-cols-4 gap-2">
                        <div><label className={labelCls}>{t('L (cm)')}</label>
                          <input className={inputCls} type="number" value={r.dim_length} onChange={e => setResourceField(i, 'dim_length', e.target.value)} placeholder="25" />
                        </div>
                        <div><label className={labelCls}>{t('B (cm)')}</label>
                          <input className={inputCls} type="number" value={r.dim_breadth} onChange={e => setResourceField(i, 'dim_breadth', e.target.value)} placeholder="7" />
                        </div>
                        <div><label className={labelCls}>{t('H (cm)')}</label>
                          <input className={inputCls} type="number" value={r.dim_height} onChange={e => setResourceField(i, 'dim_height', e.target.value)} placeholder="7" />
                        </div>
                        <div><label className={labelCls}>{t('Unit')}</label>
                          <select className={inputCls} value={r.dim_unit} onChange={e => setResourceField(i, 'dim_unit', e.target.value)}>
                            {['CM', 'MM', 'IN'].map(u => <option key={u}>{u}</option>)}
                          </select>
                        </div>
                      </div>
                      <p className="text-[10px] uppercase tracking-widest text-slate-600 font-bold">{t('Appearance')}</p>
                      <div className="grid grid-cols-3 gap-3">
                        <div><label className={labelCls}>{t('Color')}</label>
                          <input className={inputCls} value={r.color} onChange={e => setResourceField(i, 'color', e.target.value)} placeholder={t('Yellow')} />
                        </div>
                        <div><label className={labelCls}>{t('Material')}</label>
                          <input className={inputCls} value={r.material} onChange={e => setResourceField(i, 'material', e.target.value)} placeholder={t('Stainless Steel')} />
                        </div>
                        <div><label className={labelCls}>{t('Surface Finish')}</label>
                          <input className={inputCls} value={r.finish} onChange={e => setResourceField(i, 'finish', e.target.value)} placeholder={t('Matte')} />
                        </div>
                      </div>
                    </div>
                  )}

                  {/* Packaged goods — collapsible */}
                  <button onClick={() => setResOpen(s => ({ ...s, [`${i}_pkg`]: !s[`${i}_pkg`] }))}
                    className="mt-4 w-full flex items-center justify-between text-xs font-semibold text-slate-400 hover:text-slate-200 transition-colors pt-3 border-t border-white/[0.06]">
                    <span>📦 {t('Packaged Goods Declaration')}</span>
                    <ChevronRightIcon size={13} className={`transition-transform ${resOpen[`${i}_pkg`] ? 'rotate-90' : ''}`} />
                  </button>
                  {resOpen[`${i}_pkg`] && (
                    <div className="mt-3 space-y-4">
                      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                        <div><label className={labelCls}>{t('Manufacturer Type')}</label>
                          <select className={inputCls} value={r.mfr_type} onChange={e => setResourceField(i, 'mfr_type', e.target.value)}>
                            {['MANUFACTURER', 'PACKER', 'IMPORTER'].map(m => <option key={m} value={m}>{t(m)}</option>)}
                          </select>
                        </div>
                        <div><label className={labelCls}>{t('Manufacturer Name')}</label>
                          <input className={inputCls} value={r.mfr_name} onChange={e => setResourceField(i, 'mfr_name', e.target.value)} placeholder="InstaCuppa India Pvt Ltd" />
                        </div>
                        <div className="col-span-2"><label className={labelCls}>{t('Manufacturer Address')}</label>
                          <input className={inputCls} value={r.mfr_address} onChange={e => setResourceField(i, 'mfr_address', e.target.value)} placeholder="Bangalore, Karnataka, IN" />
                        </div>
                        <div><label className={labelCls}>{t('Common / Generic Name')}</label>
                          <input className={inputCls} value={r.common_name} onChange={e => setResourceField(i, 'common_name', e.target.value)} placeholder={t('Stainless Steel Vacuum Flask')} />
                        </div>
                        <div><label className={labelCls}>{t('Net Qty')}</label>
                          <div className="flex gap-2">
                            <input className={inputCls} type="number" value={r.net_qty_value} onChange={e => setResourceField(i, 'net_qty_value', e.target.value)} placeholder="500" />
                            <select className={inputCls.replace('w-full', 'w-24') + ' shrink-0'} value={r.net_qty_unit} onChange={e => setResourceField(i, 'net_qty_unit', e.target.value)}>
                              {['ML', 'L', 'G', 'KG', 'PCS'].map(u => <option key={u}>{u}</option>)}
                            </select>
                          </div>
                        </div>
                      </div>
                    </div>
                  )}
                </Card>
              ))}
              <button onClick={() => setForm(f => ({ ...f, resources: [...f.resources, EMPTY_RESOURCE()] }))}
                className="w-full py-3 rounded-xl border border-dashed border-white/[0.12] text-sm text-slate-400 hover:text-slate-200 hover:border-white/20 flex items-center justify-center gap-2 transition-all">
                <Plus size={14} /> {t('Add Another Resource')}
              </button>
            </div>
          )}

          {/* ── Step 2: Offers ── */}
          {step === 2 && (
            <div className="space-y-3 max-w-2xl">
              {form.offers.map((o, i) => (
                <Card key={i} className="p-5">
                  {/* Card header */}
                  <div className="flex items-center justify-between mb-4">
                    <div className="flex items-center gap-2">
                      <div className="w-6 h-6 rounded-md flex items-center justify-center text-[11px] font-bold text-white" style={{ background: BRAND2 }}>{i + 1}</div>
                      <p className="text-sm font-bold text-white">{t('Offer {n}', { n: i + 1 })}</p>
                    </div>
                    {form.offers.length > 1 && (
                      <button onClick={() => setForm(f => ({ ...f, offers: f.offers.filter((_, j) => j !== i) }))}
                        className="text-xs text-red-400 hover:text-red-300 flex items-center gap-1 transition-colors">
                        <Trash2 size={12} /> {t('Remove')}
                      </button>
                    )}
                  </div>

                  {/* Basic info */}
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    <div><label className={labelCls}>{t('Offer ID *')}</label>
                      <input className={inputCls} value={o.id} onChange={e => setOfferField(i, 'id', e.target.value)} placeholder="offer-flask-mh500-yellow" required />
                    </div>
                    <div><label className={labelCls}>{t('Offer Name *')}</label>
                      <input className={inputCls} value={o.name} onChange={e => setOfferField(i, 'name', e.target.value)} placeholder={t('Hiking Flask MH500 Yellow')} required />
                    </div>
                    <div className="col-span-2"><label className={labelCls}>{t('Short Description')}</label>
                      <input className={inputCls} value={o.short_desc} onChange={e => setOfferField(i, 'short_desc', e.target.value)} placeholder={t('Brief offer description')} />
                    </div>
                    <div className="col-span-2"><label className={labelCls}>{t('Resource IDs')} <span className="text-slate-600 font-normal">({t('comma-separated')})</span></label>
                      <input className={inputCls} value={o.resource_ids} onChange={e => setOfferField(i, 'resource_ids', e.target.value)} placeholder="item-flask-mh500-yellow" />
                    </div>
                    <div><label className={labelCls}>{t('Price')}</label>
                      <div className="relative">
                        <span className="absolute left-3.5 top-1/2 -translate-y-1/2 text-slate-500 text-sm">Rp</span>
                        <input className={inputCls + ' pl-9'} value={o.price} onChange={e => setOfferField(i, 'price', e.target.value)} placeholder="799" />
                      </div>
                    </div>
                    <div><label className={labelCls}>{t('Currency')}</label>
                      <input className={inputCls} value={o.currency} onChange={e => setOfferField(i, 'currency', e.target.value)} placeholder="IDR" />
                    </div>
                    <OfferTaxFields offer={o} onChange={(k, v) => setOfferField(i, k, v)} labelCls={labelCls} inputCls={inputCls} />
                    <div><label className={labelCls}>{t('Validity Start')}</label>
                      <input type="datetime-local" className={inputCls} value={o.validity_start} onChange={e => setOfferField(i, 'validity_start', e.target.value)} />
                    </div>
                    <div><label className={labelCls}>{t('Validity End')}</label>
                      <input type="datetime-local" className={inputCls} value={o.validity_end} onChange={e => setOfferField(i, 'validity_end', e.target.value)} />
                    </div>
                  </div>

                  {/* Policies — collapsible */}
                  <button onClick={() => setOffOpen(s => ({ ...s, [`${i}_pol`]: !s[`${i}_pol`] }))}
                    className="mt-4 w-full flex items-center justify-between text-xs font-semibold text-slate-400 hover:text-slate-200 transition-colors pt-3 border-t border-white/[0.06]">
                    <span>📋 {t('Policies (Returns · Cancellation · Replacement)')}</span>
                    <ChevronRightIcon size={13} className={`transition-transform ${offOpen[`${i}_pol`] ? 'rotate-90' : ''}`} />
                  </button>
                  {offOpen[`${i}_pol`] && (
                    <div className="mt-3 space-y-4">
                      {/* Returns */}
                      <div className="p-3 rounded-lg border border-white/[0.06]" style={{ background: 'rgba(0,184,230,0.04)' }}>
                        <div className="flex items-center justify-between mb-2">
                          <p className="text-xs font-bold text-cyan-400">{t('Returns')}</p>
                          <label className="flex items-center gap-2 text-xs text-slate-400 cursor-pointer">
                            <input type="checkbox" className="rounded" checked={o.returns_allowed} onChange={e => setOfferField(i, 'returns_allowed', e.target.checked)} />
                            {t('Allowed')}
                          </label>
                        </div>
                        {o.returns_allowed && (
                          <div className="grid grid-cols-2 gap-2">
                            <div><label className={labelCls}>{t('Window (ISO 8601)')}</label>
                              <input className={inputCls} value={o.returns_window} onChange={e => setOfferField(i, 'returns_window', e.target.value)} placeholder="P7D" />
                            </div>
                            <div><label className={labelCls}>{t('Method')}</label>
                              <select className={inputCls} value={o.returns_method} onChange={e => setOfferField(i, 'returns_method', e.target.value)}>
                                {RETURN_METHODS.map(m => <option key={m} value={m}>{t(m)}</option>)}
                              </select>
                            </div>
                            <div><label className={labelCls}>{t('Restocking Fee')}</label>
                              <select className={inputCls} value={o.returns_fee_type || ''} onChange={e => setOfferField(i, 'returns_fee_type', e.target.value)}>
                                <option value="">{t('None')}</option>
                                {FEE_TYPES.map(f => <option key={f} value={f}>{t(f)}</option>)}
                              </select>
                            </div>
                            <div><label className={labelCls}>{o.returns_fee_type === 'PERCENTAGE' ? t('Fee (%)') : t('Fee Amount')}</label>
                              <input type="number" min="0" step="any" className={inputCls} disabled={!o.returns_fee_type}
                                value={o.returns_fee_value ?? ''} onChange={e => setOfferField(i, 'returns_fee_value', e.target.value)}
                                placeholder={o.returns_fee_type === 'PERCENTAGE' ? '10' : '5000'} />
                            </div>
                          </div>
                        )}
                      </div>
                      {/* Cancellation */}
                      <div className="p-3 rounded-lg border border-white/[0.06]" style={{ background: 'rgba(245,158,11,0.04)' }}>
                        <div className="flex items-center justify-between mb-2">
                          <p className="text-xs font-bold text-amber-400">{t('Cancellation')}</p>
                          <label className="flex items-center gap-2 text-xs text-slate-400 cursor-pointer">
                            <input type="checkbox" className="rounded" checked={o.cancel_allowed} onChange={e => setOfferField(i, 'cancel_allowed', e.target.checked)} />
                            {t('Allowed')}
                          </label>
                        </div>
                        {o.cancel_allowed && (
                          <div className="grid grid-cols-2 gap-2">
                            <div><label className={labelCls}>{t('Window (ISO 8601)')}</label>
                              <input className={inputCls} value={o.cancel_window} onChange={e => setOfferField(i, 'cancel_window', e.target.value)} placeholder="PT2H" />
                            </div>
                            <div><label className={labelCls}>{t('Cutoff Event')}</label>
                              <select className={inputCls} value={o.cancel_event} onChange={e => setOfferField(i, 'cancel_event', e.target.value)}>
                                {['BEFORE_PACKING', 'BEFORE_DISPATCH', 'BEFORE_DELIVERY'].map(e => <option key={e} value={e}>{t(e)}</option>)}
                              </select>
                            </div>
                          </div>
                        )}
                      </div>
                      {/* Replacement */}
                      <div className="p-3 rounded-lg border border-white/[0.06]" style={{ background: 'rgba(16,185,129,0.04)' }}>
                        <div className="flex items-center justify-between mb-2">
                          <p className="text-xs font-bold text-emerald-400">{t('Replacement')}</p>
                          <label className="flex items-center gap-2 text-xs text-slate-400 cursor-pointer">
                            <input type="checkbox" className="rounded" checked={o.replace_allowed} onChange={e => setOfferField(i, 'replace_allowed', e.target.checked)} />
                            {t('Allowed')}
                          </label>
                        </div>
                        {o.replace_allowed && (
                          <div className="grid grid-cols-2 gap-2">
                            <div><label className={labelCls}>{t('Window (ISO 8601)')}</label>
                              <input className={inputCls} value={o.replace_window} onChange={e => setOfferField(i, 'replace_window', e.target.value)} placeholder="P7D" />
                            </div>
                            <div><label className={labelCls}>{t('Method')}</label>
                              <select className={inputCls} value={o.replace_method} onChange={e => setOfferField(i, 'replace_method', e.target.value)}>
                                {RETURN_METHODS.map(m => <option key={m} value={m}>{t(m)}</option>)}
                              </select>
                            </div>
                            <div className="col-span-2">
                              <label className="flex items-center gap-2 text-xs text-slate-400 cursor-pointer">
                                <input type="checkbox" className="rounded" checked={o.replace_subject_avail} onChange={e => setOfferField(i, 'replace_subject_avail', e.target.checked)} />
                                {t('Subject to availability')}
                              </label>
                            </div>
                          </div>
                        )}
                      </div>
                    </div>
                  )}

                  {/* Payment & Serviceability — collapsible */}
                  <button onClick={() => setOffOpen(s => ({ ...s, [`${i}_svc`]: !s[`${i}_svc`] }))}
                    className="mt-4 w-full flex items-center justify-between text-xs font-semibold text-slate-400 hover:text-slate-200 transition-colors pt-3 border-t border-white/[0.06]">
                    <span>🚚 {t('Payment & Serviceability')}</span>
                    <ChevronRightIcon size={13} className={`transition-transform ${offOpen[`${i}_svc`] ? 'rotate-90' : ''}`} />
                  </button>
                  {offOpen[`${i}_svc`] && (
                    <div className="mt-3 space-y-4">
                      <label className="flex items-center gap-3 text-sm text-slate-300 cursor-pointer">
                        <input type="checkbox" className="rounded" checked={o.cod_available} onChange={e => setOfferField(i, 'cod_available', e.target.checked)} />
                        <span>{t('Cash on Delivery (COD) available')}</span>
                      </label>
                      <div className="grid grid-cols-3 gap-3">
                        <div className="col-span-2"><label className={labelCls}>{t('Max Delivery Distance')}</label>
                          <input className={inputCls} type="number" value={o.max_distance} onChange={e => setOfferField(i, 'max_distance', e.target.value)} placeholder="15" />
                        </div>
                        <div><label className={labelCls}>{t('Unit')}</label>
                          <select className={inputCls} value={o.distance_unit} onChange={e => setOfferField(i, 'distance_unit', e.target.value)}>
                            {['KM', 'MI'].map(u => <option key={u}>{u}</option>)}
                          </select>
                        </div>
                      </div>
                      <div>
                        <label className={labelCls}>{t('Delivery Days')}</label>
                        <div className="flex flex-wrap gap-2 mt-1">
                          {ALL_DAYS.map(day => (
                            <button key={day} type="button"
                              onClick={() => toggleDay(i, day)}
                              className={`px-2.5 py-1 rounded-lg text-xs font-bold border transition-all ${o.timing_days.includes(day)
                                ? 'text-white border-cyan-500/40'
                                : 'text-slate-500 border-white/[0.08] hover:border-white/20'
                                }`}
                              style={o.timing_days.includes(day) ? { background: 'rgba(0,184,230,0.15)' } : {}}>
                              {t(day)}
                            </button>
                          ))}
                        </div>
                      </div>
                      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                        <div><label className={labelCls}>{t('Delivery Window Start')}</label>
                          <input type="time" className={inputCls} value={o.timing_start} onChange={e => setOfferField(i, 'timing_start', e.target.value)} />
                        </div>
                        <div><label className={labelCls}>{t('Delivery Window End')}</label>
                          <input type="time" className={inputCls} value={o.timing_end} onChange={e => setOfferField(i, 'timing_end', e.target.value)} />
                        </div>
                      </div>
                    </div>
                  )}
                </Card>
              ))}
              <button onClick={() => setForm(f => ({ ...f, offers: [...f.offers, EMPTY_OFFER()] }))}
                className="w-full py-3 rounded-xl border border-dashed border-white/[0.12] text-sm text-slate-400 hover:text-slate-200 hover:border-white/20 flex items-center justify-center gap-2 transition-all">
                <Plus size={14} /> {t('Add Another Offer')}
              </button>
            </div>
          )}

          {/* ── Step 3: Review ── */}
          {step === 3 && (
            <div className="space-y-4">
              <Card className="p-5">
                <SectionTitle>{t('Catalog')}</SectionTitle>
                <div className="space-y-2 text-sm">
                  {[
                    [t('ID'), form.catalog_id], [t('Name'), form.catalog_name],
                    [t('Provider'), `${form.provider_name} (${form.provider_id})`],
                    [t('Type'), form.catalog_type || t('Not set')],
                    [t('Valid From'), form.validity_start ? fmtDate(new Date(form.validity_start).toISOString()) : '—'],
                    [t('Valid Until'), form.validity_end ? fmtDate(new Date(form.validity_end).toISOString()) : '—'],
                  ].map(([k, v]) => (
                    <div key={k} className="flex justify-between">
                      <span className="text-slate-500">{k}</span>
                      <span className="text-slate-200 font-medium">{v || '—'}</span>
                    </div>
                  ))}
                </div>
              </Card>
              <Card className="p-5">
                <SectionTitle>{t('Resources ({count})', { count: form.resources.length })}</SectionTitle>
                <div className="space-y-3">
                  {form.resources.map((r, i) => (
                    <div key={i} className="flex items-start gap-3 text-sm">
                      {r.media_url
                        ? <img src={r.media_url} alt={r.name} className="w-10 h-10 rounded-lg object-cover shrink-0 border border-white/[0.08]" onError={e => { e.target.style.display = 'none' }} />
                        : <div className="w-10 h-10 rounded-lg shrink-0 flex items-center justify-center" style={{ background: 'rgba(0,184,230,0.08)', border: '1px solid rgba(0,184,230,0.15)' }}><Package size={16} style={{ color: '#00b8e6' }} /></div>
                      }
                      <div className="flex-1 min-w-0">
                        <p className="text-slate-200 font-semibold truncate">{r.name || '—'}</p>
                        <p className="text-[11px] font-mono text-slate-600">{r.id}</p>
                        <div className="flex flex-wrap gap-1 mt-1">
                          {r.brand && <span className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.06] text-slate-400">{r.brand}</span>}
                          {r.color && <span className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.06] text-slate-400">{r.color}</span>}
                          {r.material && <span className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.06] text-slate-400">{r.material}</span>}
                        </div>
                      </div>
                      <span className="shrink-0 text-xs px-2 py-0.5 rounded-full border"
                        style={r.stock_quantity !== '' && !isNaN(Number(r.stock_quantity))
                          ? { color: '#00b8e6', borderColor: 'rgba(0,184,230,0.25)', background: 'rgba(0,184,230,0.08)' }
                          : { color: '#64748b', borderColor: 'rgba(255,255,255,0.08)' }}>
                        {r.stock_quantity !== '' && !isNaN(Number(r.stock_quantity)) ? t('Qty: {qty}', { qty: r.stock_quantity }) : `∞ ${t('Unlimited')}`}
                      </span>
                    </div>
                  ))}
                </div>
              </Card>
              <Card className="p-5">
                <SectionTitle>{t('Offers ({count})', { count: form.offers.length })}</SectionTitle>
                <div className="space-y-3">
                  {form.offers.map((o, i) => (
                    <div key={i} className="flex items-start justify-between gap-3 text-sm">
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2">
                          <Tag size={13} className="text-slate-500 shrink-0" />
                          <span className="text-slate-200 font-semibold truncate">{o.name || '—'}</span>
                        </div>
                        <p className="text-[11px] font-mono text-slate-600 mt-0.5 ml-5">{o.id}</p>
                        <div className="flex flex-wrap gap-1 mt-1 ml-5">
                          {o.cod_available && <span className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.06] text-slate-400">COD ✓</span>}
                          {o.returns_allowed && <span className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.06] text-slate-400">{t('Returns {window}', { window: o.returns_window })}</span>}
                          {o.max_distance && <span className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.06] text-slate-400">📍 {o.max_distance} {o.distance_unit}</span>}
                          {o.timing_days.length < 7 && <span className="text-[10px] px-1.5 py-0.5 rounded bg-white/[0.06] text-slate-400">{o.timing_days.map(d => t(d)).join(', ')}</span>}
                        </div>
                      </div>
                      {o.price && <span className="shrink-0 text-xs font-bold" style={{ color: '#00b8e6' }}>{o.price} {o.currency}</span>}
                    </div>
                  ))}
                </div>
              </Card>
            </div>
          )}
        </motion.div>
      </AnimatePresence>

      {/* Navigation buttons */}
      <div className="flex items-center justify-between mt-6 pt-5 border-t border-white/[0.06]">
        <button onClick={() => step > 0 && setStep(s => s - 1)}
          className={`flex items-center gap-2 px-4 py-2 rounded-xl text-sm font-semibold transition-all
            ${step === 0 ? 'opacity-0 pointer-events-none' : 'text-slate-400 hover:text-white border border-white/[0.08] hover:border-white/20'}`}>
          <ChevronLeft size={15} /> {t('Back')}
        </button>
        {step < 3 ? (
          <button onClick={() => setStep(s => s + 1)}
            className="flex items-center gap-2 px-5 py-2.5 rounded-xl text-sm font-bold text-white transition-all active:scale-95 shadow-lg"
            style={{ background: BRAND }}>
            {t('Continue')} <ArrowRight size={15} />
          </button>
        ) : (
          <button onClick={handleSubmit} disabled={loading}
            className="flex items-center gap-2 px-6 py-2.5 rounded-xl text-sm font-bold text-white disabled:opacity-60 transition-all active:scale-95 shadow-lg"
            style={{ background: BRAND }}>
            {loading ? <><RefreshCw size={14} className="animate-spin" /> {t('Publishing…')}</> : <><Upload size={14} /> {t('Publish Catalog')}</>}
          </button>
        )}
      </div>
    </div>
  )
}

// ─── Messages ─────────────────────────────────────────────────────────────────

const ACTION_META = {
  select: { color: '#00b8e6', bg: 'rgba(0,184,230,0.12)', label: 'SELECT' },
  on_select: { color: '#67e8f9', bg: 'rgba(103,232,249,0.1)', label: 'ON_SELECT' },
  init: { color: '#a78bfa', bg: 'rgba(167,139,250,0.12)', label: 'INIT' },
  on_init: { color: '#c4b5fd', bg: 'rgba(196,181,253,0.1)', label: 'ON_INIT' },
  confirm: { color: '#34d399', bg: 'rgba(52,211,153,0.12)', label: 'CONFIRM' },
  on_confirm: { color: '#6ee7b7', bg: 'rgba(110,231,183,0.1)', label: 'ON_CONFIRM' },
  cancel: { color: '#f87171', bg: 'rgba(248,113,113,0.12)', label: 'CANCEL' },
  on_cancel: { color: '#fca5a5', bg: 'rgba(252,165,165,0.1)', label: 'ON_CANCEL' },
}

function MessagesPage() {
  const { t } = useI18n()
  const [limit, setLimit] = useState(50)
  const [filter, setFilter] = useState('')
  const [dirFilter, setDirFilter] = useState('ALL')
  const { data, loading, error, reload } = useApi(`/messages?limit=${limit}`, [limit], { live: true })
  const [expanded, setExpanded] = useState(() => new Set())
  const toggle = key => setExpanded(prev => {
    const next = new Set(prev)
    next.has(key) ? next.delete(key) : next.add(key)
    return next
  })

  const items = (data?.items ?? []).filter(m => {
    const matchText = !filter || String(m.action).includes(filter.toLowerCase()) || String(m.transaction_id).includes(filter)
    const matchDir = dirFilter === 'ALL' || m.direction === dirFilter
    return matchText && matchDir
  })

  const ackCount = items.filter(m => m.ack_status?.ack_status === 'ACK' || m.ack_status === 'ACK').length
  const nackCount = items.filter(m => m.ack_status?.ack_status === 'NACK' || m.ack_status === 'NACK').length

  return (
    <div>
      <PageHeader breadcrumb={t('Protocol Audit')} title={t('Message Log')}
        subtitle={t('{count} recent protocol messages', { count: items.length })}
        action={
          <button onClick={reload}
            className="shrink-0 whitespace-nowrap flex items-center gap-1.5 text-xs text-slate-400 hover:text-white transition-colors px-3 py-1.5 rounded-lg border border-white/[0.07]">
            <RefreshCw size={12} className={loading ? 'animate-spin' : ''} /> {t('Refresh')}
          </button>
        }
      />

      {/* Quick stats */}
      {!loading && items.length > 0 && (
        <div className="grid grid-cols-3 gap-2 sm:gap-3 mb-5">
          <Card className="p-3 sm:p-4 flex items-center gap-3">
            <div className="hidden sm:flex w-8 h-8 rounded-lg items-center justify-center text-sm" style={{ background: 'rgba(0,184,230,0.15)' }}>📨</div>
            <div><p className="text-[11px] text-slate-500">{t('Total')}</p><p className="text-lg font-bold text-white">{items.length}</p></div>
          </Card>
          <Card className="p-3 sm:p-4 flex items-center gap-3">
            <div className="hidden sm:flex w-8 h-8 rounded-lg items-center justify-center text-sm" style={{ background: 'rgba(16,185,129,0.15)' }}>✅</div>
            <div><p className="text-[11px] text-slate-500">ACK</p><p className="text-lg font-bold text-emerald-400">{ackCount}</p></div>
          </Card>
          <Card className="p-3 sm:p-4 flex items-center gap-3">
            <div className="hidden sm:flex w-8 h-8 rounded-lg items-center justify-center text-sm" style={{ background: 'rgba(239,68,68,0.15)' }}>❌</div>
            <div><p className="text-[11px] text-slate-500">NACK</p><p className="text-lg font-bold text-red-400">{nackCount}</p></div>
          </Card>
        </div>
      )}

      {error && <div className="mb-4"><ErrorBox message={error} onRetry={reload} /></div>}

      {/* Filters */}
      <div className="flex gap-3 mb-4 flex-wrap">
        <div className="relative flex-1 min-w-[200px] max-w-sm">
          <Search size={13} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
          <input value={filter} onChange={e => setFilter(e.target.value)}
            placeholder={t('Filter by action or txn ID…')}
            className="w-full bg-slate-900 border border-white/[0.08] rounded-xl pl-9 pr-3 py-2 text-sm text-slate-200 placeholder-slate-500 focus:outline-none focus:border-cyan-500/40 transition-colors" />
        </div>
        <div className="flex gap-1 p-0.5 rounded-xl" style={{ background: 'rgba(255,255,255,0.04)', border: '1px solid rgba(255,255,255,0.07)' }}>
          {['ALL', 'INBOUND', 'OUTBOUND'].map(d => (
            <button key={d} onClick={() => setDirFilter(d)}
              className={`px-3 py-1.5 rounded-lg text-xs font-semibold transition-all ${dirFilter === d ? 'text-white' : 'text-slate-400 hover:text-slate-200'}`}
              style={dirFilter === d ? { background: BRAND } : {}}>
              {t(d)}
            </button>
          ))}
        </div>
        <select value={limit} onChange={e => setLimit(Number(e.target.value))}
          className="bg-slate-900 border border-white/[0.08] rounded-xl px-3 py-2 text-sm text-slate-300 focus:outline-none focus:border-cyan-500/40 cursor-pointer">
          {[25, 50, 100, 200].map(n => <option key={n} value={n}>{t('{count} rows', { count: n })}</option>)}
        </select>
        {items.length > 0 && (
          <button onClick={() => setExpanded(expanded.size ? new Set() : new Set(items.map((m, i) => m.id || i)))}
            className="px-3 py-2 rounded-xl text-xs font-semibold text-slate-400 hover:text-white border border-white/[0.08] transition-colors">
            {expanded.size ? t('Collapse all') : t('Expand all')}
          </button>
        )}
      </div>

      <Card>
        {loading ? <LoadingSpinner label={t('Loading messages…')} /> : !items.length ? (
          <div className="px-5 py-12 text-center text-sm text-slate-500">{t('No messages found')}</div>
        ) : (
          <>
            {/* Desktop / tablet: table with expandable payload rows */}
            <div className="hidden md:block overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-[10px] font-bold text-slate-500 uppercase tracking-widest border-b border-white/[0.06]">
                    <th className="w-10 px-3 py-3" />
                    <th className="text-left px-4 py-3">{t('Action')}</th>
                    <th className="text-left px-4 py-3">{t('Direction')}</th>
                    <th className="text-left px-4 py-3">{t('Status')}</th>
                    <th className="text-left px-4 py-3">{t('Txn ID')}</th>
                    <th className="text-left px-4 py-3 hidden lg:table-cell">URL</th>
                    <th className="text-left px-4 py-3">{t('Time')}</th>
                    <th className="text-left px-4 py-3">{t('Payload')}</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((m, idx) => {
                    const key = m.id || idx
                    const open = expanded.has(key)
                    return (
                      <Fragment key={key}>
                        <tr onClick={() => toggle(key)}
                          className={`cursor-pointer border-t border-white/[0.04] transition-colors ${open ? 'bg-white/[0.035]' : 'hover:bg-white/[0.025]'}`}>
                          <td className="px-3 py-2.5 text-slate-500">
                            <ChevronDown size={14} className={`transition-transform ${open ? 'rotate-180 text-cyan-400' : ''}`} />
                          </td>
                          <td className="px-4 py-2.5"><ActionBadge action={m.action} /></td>
                          <td className="px-4 py-2.5"><DirectionLabel direction={m.direction} /></td>
                          <td className="px-4 py-2.5"><StatusBadge status={ackOf(m)} /></td>
                          <td className="px-4 py-2.5 font-mono text-[11px] text-slate-500">{shortTxn(m.transaction_id)}</td>
                          <td className="px-4 py-2.5 text-xs text-slate-500 max-w-[180px] truncate hidden lg:table-cell" title={m.url}>{m.url || '—'}</td>
                          <td className="px-4 py-2.5 text-xs text-slate-500 whitespace-nowrap">{fmtDate(m.created_at?.Time || m.created_at)}</td>
                          <td className="px-4 py-2.5 max-w-[260px]">
                            <span className="block truncate font-mono text-[11px] text-slate-500" title={t('Click to expand')}>
                              {payloadPreview(m.request_payload)}
                            </span>
                          </td>
                        </tr>
                        {open && (
                          <tr className="bg-white/[0.035]">
                            <td colSpan={8} className="px-4 pb-4 pt-1"><PayloadPanel message={m} /></td>
                          </tr>
                        )}
                      </Fragment>
                    )
                  })}
                </tbody>
              </table>
            </div>

            {/* Mobile: stacked cards */}
            <div className="md:hidden divide-y divide-white/[0.05]">
              {items.map((m, idx) => {
                const key = m.id || idx
                const open = expanded.has(key)
                return (
                  <div key={key} className={open ? 'bg-white/[0.035]' : ''}>
                    <button onClick={() => toggle(key)} className="w-full text-left px-4 py-3 flex items-start gap-3">
                      <div className="flex-1 min-w-0 space-y-1.5">
                        <div className="flex items-center gap-2 flex-wrap">
                          <ActionBadge action={m.action} />
                          <DirectionLabel direction={m.direction} />
                          <StatusBadge status={ackOf(m)} />
                        </div>
                        <div className="flex items-center justify-between gap-2 text-[11px] text-slate-500">
                          <span className="font-mono">{shortTxn(m.transaction_id)}</span>
                          <span className="whitespace-nowrap">{fmtDate(m.created_at?.Time || m.created_at)}</span>
                        </div>
                      </div>
                      <ChevronDown size={16} className={`mt-1 shrink-0 text-slate-500 transition-transform ${open ? 'rotate-180 text-cyan-400' : ''}`} />
                    </button>
                    {open && <div className="px-4 pb-4"><PayloadPanel message={m} /></div>}
                  </div>
                )
              })}
            </div>
          </>
        )}
      </Card>
    </div>
  )
}

const ackOf = m => m.ack_status?.ack_status || m.ack_status
const shortTxn = id => (id ? String(id).slice(0, 8) + '…' : '—')

// One-line preview of a payload for the collapsed row.
const payloadPreview = p => {
  if (p == null) return '—'
  const s = typeof p === 'string' ? p : JSON.stringify(p)
  return s.length > 120 ? s.slice(0, 120) + '…' : s
}

function ActionBadge({ action }) {
  const meta = ACTION_META[action] || { color: '#94a3b8', bg: 'rgba(148,163,184,0.08)', label: action?.toUpperCase() }
  return (
    <span className="inline-flex items-center px-2.5 py-1 rounded-lg text-[11px] font-bold font-mono"
      style={{ background: meta.bg, color: meta.color }}>
      {meta.label}
    </span>
  )
}

function DirectionLabel({ direction }) {
  const { t } = useI18n()
  return (
    <span className={`text-xs font-medium whitespace-nowrap ${direction === 'INBOUND' ? 'text-cyan-400' : 'text-violet-400'}`}>
      {direction === 'INBOUND' ? `↓ ${t('IN')}` : `↑ ${t('OUT')}`}
    </span>
  )
}

// Collapsible JSON tree: click the ▸/▾ toggle (or the collapsed summary) on
// any object or array to fold it. Everything starts expanded.
function JsonNode({ name, value, depth, last }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(true)
  const isArr = Array.isArray(value)
  const isObj = value !== null && typeof value === 'object'
  const comma = last ? '' : ','
  const label = name !== undefined && (
    <><span className="text-sky-300">"{name}"</span><span className="text-slate-500">: </span></>
  )
  const indent = { paddingLeft: depth ? 14 : 0 }

  if (!isObj) {
    const color = typeof value === 'string' ? 'text-emerald-300'
      : typeof value === 'number' ? 'text-amber-300'
        : typeof value === 'boolean' ? 'text-violet-300' : 'text-slate-500'
    return <div style={indent}>{label}<span className={color}>{JSON.stringify(value)}</span><span className="text-slate-500">{comma}</span></div>
  }

  const entries = isArr ? value.map((v, i) => [i, v]) : Object.entries(value)
  const [openCh, closeCh] = isArr ? ['[', ']'] : ['{', '}']
  const toggle = e => { e.stopPropagation(); setOpen(o => !o) }
  const toggler = entries.length > 0 && (
    <button onClick={toggle} className="inline-block w-3 -ml-3 text-slate-500 hover:text-cyan-400" aria-label={open ? t('Collapse') : t('Expand')}>
      {open ? '▾' : '▸'}
    </button>
  )

  if (!entries.length) {
    return <div style={indent}>{label}<span className="text-slate-400">{openCh}{closeCh}</span><span className="text-slate-500">{comma}</span></div>
  }

  if (!open) {
    return (
      <div style={indent}>
        {toggler}{label}
        <button onClick={toggle} className="text-slate-400 hover:text-cyan-400">
          {openCh}<span className="px-1 text-slate-500 italic">{t(isArr ? '{count} items' : '{count} keys', { count: entries.length })}</span>{closeCh}
        </button>
        <span className="text-slate-500">{comma}</span>
      </div>
    )
  }

  return (
    <div style={indent}>
      <div>{toggler}{label}<span className="text-slate-400">{openCh}</span></div>
      {entries.map(([k, v], i) => (
        <JsonNode key={k} name={isArr ? undefined : k} value={v} depth={depth + 1} last={i === entries.length - 1} />
      ))}
      <div><span className="text-slate-400">{closeCh}</span><span className="text-slate-500">{comma}</span></div>
    </div>
  )
}

// Expanded view of a message: request / response payload tabs, error details,
// pretty-printed JSON with copy-to-clipboard.
function PayloadPanel({ message: m }) {
  const { t } = useI18n()
  const tabs = [
    { id: 'request', label: t('Request'), data: m.request_payload },
    ...(m.response_payload != null ? [{ id: 'response', label: t('Response'), data: m.response_payload }] : []),
  ]
  const [tab, setTab] = useState('request')
  const [copied, setCopied] = useState(false)
  const current = tabs.find(x => x.id === tab) || tabs[0]
  const text = current.data == null ? '' : JSON.stringify(current.data, null, 2)

  const copy = async e => {
    e.stopPropagation()
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch { }
  }

  return (
    <div className="rounded-xl border border-white/[0.07] bg-slate-950/60 overflow-hidden" onClick={e => e.stopPropagation()}>
      <div className="flex items-center justify-between gap-2 px-3 py-2 border-b border-white/[0.06] flex-wrap">
        <div className="flex gap-1">
          {tabs.map(x => (
            <button key={x.id} onClick={() => setTab(x.id)}
              className={`px-2.5 py-1 rounded-md text-[11px] font-semibold transition-colors ${current.id === x.id ? 'text-white bg-white/[0.08]' : 'text-slate-400 hover:text-slate-200'}`}>
              {x.label}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-3 text-[11px] text-slate-500 min-w-0">
          {m.message_id && <span className="font-mono truncate hidden sm:inline" title={t('Message ID')}>msg {String(m.message_id).slice(0, 8)}…</span>}
          {text && (
            <button onClick={copy} className="flex items-center gap-1 text-slate-400 hover:text-white transition-colors">
              {copied ? <Check size={12} className="text-emerald-400" /> : <Copy size={12} />} {copied ? t('Copied') : t('Copy')}
            </button>
          )}
        </div>
      </div>
      {m.error_message && (
        <div className="px-3 py-2 text-xs text-red-300 bg-red-500/10 border-b border-red-500/20 break-words">{m.error_message}</div>
      )}
      {text ? (
        <div className="max-h-[28rem] overflow-auto p-3 text-[11px] leading-relaxed font-mono text-slate-300 whitespace-pre">
          <JsonNode value={current.data} depth={0} last />
        </div>
      ) : (
        <div className="p-3 text-xs text-slate-500">{t('No payload recorded')}</div>
      )}
    </div>
  )
}

// ─── Reconciliation ───────────────────────────────────────────────────────────
// Settlement reconciliation with buyer apps (ION Release 1
// core/api/v2.0.0/ion-additional.yaml in ion-specs). The buyer's payment is held at the PJP. Per BAP:
// contracts due now (payout basis event + settlementWindow has passed) with
// the summary /reconcile would carry, and a manual send; contracts still to
// fall due; and past batches, sent or received from the buyer app.

const rupiah = n => n == null ? '—' : `Rp ${Number(n).toLocaleString('id-ID')}`

function untilText(dueAt, now) {
  if (!dueAt) return null
  const ms = new Date(dueAt).getTime() - now
  if (ms <= 0) return tNow('due now')
  const s = Math.round(ms / 1000), d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60)
  return tNow('due in {time}', { time: d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : m ? `${m}m ${s % 60}s` : `${s}s` })
}

// How many contracts the next /reconcile to a BAP carries: a disputed batch
// again in full, or what is due now (in_next_batch, decided by the server).
// The batch summary for just the ticked contracts.
function tickedSummary(rows) {
  const sum = k => Math.round(rows.reduce((t, r) => t + (r[k] || 0), 0) * 100) / 100
  return { contractCount: rows.length, totalOrderAmount: sum('order_amount'), totalRefund: sum('refund'), totalCommission: sum('commission'), totalNetPayable: sum('net_payable') }
}

// In a disputed batch only the contracts with accord false are in dispute;
// last_result says which, and the figures that differ.
const isDisputed = r => r.reconciliation_status === 'DISPUTED' && !r.last_result?.accord
// Sent before its settlement window: the buyer app answered accord false and
// the contract waits until it is due (no reconciliation status).
const sentEarly = r => !r.reconciliation_status && r.last_result?.code === 'SETTLEMENT_WINDOW_NOT_REACHED'
const diffOf = (r, field) => r.reconciliation_status === 'DISPUTED' && r.last_result?.diffs?.find(d => d.field === field)

function DiffNote({ d }) {
  const { t } = useI18n()
  if (!d) return null
  return (
    <div className="text-[10px] font-semibold text-rose-300" title={t('Your figure: {ours}', { ours: rupiah(d.ours) })}>
      {t('Buyer app: {amount}', { amount: rupiah(d.theirs) })} ({d.diff > 0 ? '+' : '−'}{rupiah(Math.abs(d.diff))})
    </div>
  )
}

// select (optional, from useReconSelection) adds a checkbox column for
// choosing the contracts the next /reconcile carries.
function ReconContractsTable({ rows, now, select }) {
  const { t } = useI18n()
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs">
        <thead>
          <tr className="text-[10px] font-bold text-slate-500 uppercase tracking-widest border-b border-white/[0.06]">
            {select && (
              <th className="pl-4 pr-1 py-2 w-6">
                <input type="checkbox" className="accent-cyan-500 cursor-pointer disabled:cursor-not-allowed" disabled={select.header.disabled}
                  checked={select.header.checked} ref={el => { if (el) el.indeterminate = select.header.indeterminate }}
                  onChange={select.toggleAll} title={t('Select all due and disputed contracts')} />
              </th>
            )}
            <th className="text-left px-4 py-2">{t('Contract')}</th>
            <th className="text-left px-4 py-2">{t('Created')}</th>
            <th className="text-left px-4 py-2">{t('Status')}</th>
            <th className="text-left px-4 py-2">{t('Payout clock')}</th>
            <th className="text-right px-4 py-2">{t('Paid')}</th>
            <th className="text-right px-4 py-2">{t('Refund')}</th>
            <th className="text-right px-4 py-2">{t('Finder fee')}</th>
            <th className="text-right px-4 py-2">{t('Net payable')}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-white/[0.04]">
          {rows.map(r => (
            <tr key={r.contract_id}>
              {select && (
                <td className="pl-4 pr-1 py-2">
                  {r.selectable && (
                    <input type="checkbox" className="accent-cyan-500 cursor-pointer"
                      checked={select.isTicked(r)} onChange={() => select.toggle(r)}
                      title={t('Include in the next /reconcile')} />
                  )}
                </td>
              )}
              <td className="px-4 py-2 font-mono text-slate-400 whitespace-nowrap" title={r.contract_id}>{String(r.contract_id).slice(0, 8)}…</td>
              <td className="px-4 py-2 text-slate-400 whitespace-nowrap">{r.created_at ? fmtDate(r.created_at) : '—'}</td>
              <td className="px-4 py-2 whitespace-nowrap">
                <StatusBadge status={r.status} />
                {isDisputed(r) && <span className="ml-1.5 text-[11px] font-semibold text-rose-300">{t('Disputed')}</span>}
                {r.reconciliation_status === 'DISPUTED' && !isDisputed(r) && <span className="ml-1.5 text-[11px] text-slate-500">{t('Matched — batch disputed')}</span>}
                {sentEarly(r) && <span className="ml-1.5 text-[11px] font-semibold text-amber-300" title={r.last_result.reason}>{t('Sent before settlement window')}</span>}
                {sentEarly(r) && <div className="mt-0.5 text-[10px] text-amber-300/80 whitespace-normal max-w-[18rem]">{r.last_result.reason}</div>}
                {isDisputed(r) && !r.last_result?.diffs?.length && r.last_result?.reason && <div className="mt-0.5 text-[10px] text-rose-300/80 whitespace-normal max-w-[18rem]">{r.last_result.reason}</div>}
                {r.refunded_after_payout > 0 && <span className="ml-1.5 text-[11px] text-slate-500" title={t('Refunded by you after payout — outside reconciliation')}>{t('+{amount} refunded after payout', { amount: rupiah(r.refunded_after_payout) })}</span>}
              </td>
              <td className="px-4 py-2 text-slate-400 min-w-[12rem]">
                <span className="text-slate-300">{t(r.basis_event)}</span>
                {r.basis_at && <> {fmtDate(r.basis_at)}</>}
                <span className="text-slate-600"> + {r.settlement_window}</span>
                {r.due_at && <span className={`ml-2 font-semibold ${r.due ? 'text-emerald-400' : 'text-amber-400'}`}>{untilText(r.due_at, now)}</span>}
              </td>
              <td className="px-4 py-2 text-right tabular-nums text-slate-300 whitespace-nowrap">{rupiah(r.order_amount)}<DiffNote d={diffOf(r, 'orderAmount')} /></td>
              <td className="px-4 py-2 text-right tabular-nums text-rose-300 whitespace-nowrap">{r.refund ? `− ${rupiah(r.refund)}` : '—'}<DiffNote d={diffOf(r, 'refundAmount')} /></td>
              <td className="px-4 py-2 text-right tabular-nums text-amber-300 whitespace-nowrap">{r.commission ? `− ${rupiah(r.commission)}` : '—'}<DiffNote d={diffOf(r, 'commission')} /></td>
              <td className="px-4 py-2 text-right tabular-nums font-semibold text-white whitespace-nowrap">{rupiah(r.net_payable)}<DiffNote d={diffOf(r, 'netPayable')} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// One buyer app's contracts to reconcile, and its Send. The ticked contracts
// (see useReconSelection) are what the /reconcile carries.
function BapReconCard({ g, now, visible, sending, onSend }) {
  const { t } = useI18n()
  const sel = useReconSelection(g.due, visible)
  const count = sel.ticked.length
  const due = g.due.filter(visible), upcoming = g.upcoming.filter(visible)
  return (
    <Card className="mb-5 overflow-hidden">
      <div className="px-5 py-3 border-b border-white/[0.06] flex flex-wrap items-center gap-3">
        <div className="min-w-0">
          <p className="text-[10px] font-bold uppercase tracking-widest text-slate-500">{t('Buyer app')}</p>
          <p className="text-sm font-mono text-white truncate">{g.bap_id}</p>
        </div>
        <button disabled={!count || sending} onClick={() => onSend(g.bap_id, sel.ticked)}
          className="ml-auto flex items-center gap-1.5 px-4 py-2 rounded-xl text-xs font-bold text-white disabled:opacity-40 transition-opacity"
          style={{ background: BRAND }}>
          <Send size={13} /> {sending ? t('Sending…')
            : count && count < sel.total ? t('Send reconcile ({count} of {total})', { count, total: sel.total })
            : count ? t('Send reconcile ({count})', { count })
            : g.due.some(r => r.selectable) ? t('No contract selected') : t('Nothing due yet')}
        </button>
      </div>

      {count > 0 && (() => {
        const sm = tickedSummary(sel.ticked)
        return (
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-px bg-white/[0.04]">
          {[
            [t('Contracts'), sm.contractCount, 'text-white'],
            [t('Paid (held at PJP)'), rupiah(sm.totalOrderAmount), 'text-slate-200'],
            [t('Refunds'), sm.totalRefund ? `− ${rupiah(sm.totalRefund)}` : '—', 'text-rose-300'],
            [t('Finder fee to buyer app'), sm.totalCommission ? `− ${rupiah(sm.totalCommission)}` : '—', 'text-amber-300'],
            [t('Net payable'), rupiah(sm.totalNetPayable), 'text-emerald-300'],
          ].map(([label, value, cls]) => (
            <div key={label} className="bg-slate-950/40 px-4 py-3">
              <p className="text-[10px] text-slate-500">{label}</p>
              <p className={`text-sm font-bold tabular-nums ${cls}`}>{value}</p>
            </div>
          ))}
        </div>
        )
      })()}

      {due.length > 0 && (
        <div className="pt-2">
          <p className="px-5 pt-2 text-[11px] font-bold uppercase tracking-widest text-emerald-400">{t('Due now')}</p>
          <ReconContractsTable rows={due} now={now} select={sel} />
        </div>
      )}
      {upcoming.length > 0 && (
        <div className="pt-2 pb-1">
          <p className="px-5 pt-2 text-[11px] font-bold uppercase tracking-widest text-amber-400">{t('Upcoming')}</p>
          <ReconContractsTable rows={upcoming} now={now} />
        </div>
      )}
    </Card>
  )
}

function ReconciliationPage() {
  const { t } = useI18n()
  const { data, loading, error, reload } = useApi('/reconciliation', [], { live: true })
  const [now, setNow] = useState(Date.now())
  const [sending, setSending] = useState(null)
  const [notice, setNotice] = useState(null)
  const [openBatch, setOpenBatch] = useState(null)
  // Filters: one buyer app (each gets its own /reconcile) and the
  // contracts' creation date.
  const [np, setNp] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const visible = useCallback(r => inDateRange(r, from, to), [from, to])

  // Tick every second for the countdowns; the data itself is live (useApi),
  // so contracts move from upcoming to due and batch outcomes arrive.
  useEffect(() => {
    const tick = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(tick)
  }, [])

  const send = async (bapId, rows) => {
    const ids = rows.map(r => r.contract_id)
    const question = t('Send /reconcile to {bap} for the {count} selected contracts?', { bap: bapId, count: ids.length })
    if (!window.confirm(question)) return
    setSending(bapId); setNotice(null)
    try {
      const r = await apiFetch('/reconciliation/send', { method: 'POST', body: JSON.stringify({ bap_id: bapId, contract_ids: ids }) })
      setNotice({ ok: true, text: t('Sent {id} — net payable {amount}', { id: r.batch.id, amount: rupiah(r.batch.summary?.totalNetPayable) }) })
    } catch (e) {
      let msg = e.message
      try { msg = JSON.parse(e.message).error || msg } catch { }
      setNotice({ ok: false, text: msg })
    } finally {
      setSending(null); reload()
    }
  }

  const allBaps = data?.baps || []
  const baps = np ? allBaps.filter(g => g.bap_id === np) : allBaps
  const batches = data?.batches || []
  const terms = data?.terms

  return (
    <div>
      <PageHeader breadcrumb={t('Settlement')} title={t('Reconciliation')}
        subtitle={terms ? t('Payout terms: {basis} + {window}', { basis: terms.settlement_basis, window: terms.settlement_window }) : t('Settle with buyer apps')}
        action={
          <button onClick={reload}
            className="shrink-0 whitespace-nowrap flex items-center gap-1.5 text-xs text-slate-400 hover:text-white transition-colors px-3 py-1.5 rounded-lg border border-white/[0.07]">
            <RefreshCw size={12} className={loading ? 'animate-spin' : ''} /> {t('Refresh')}
          </button>
        }
      />

      {allBaps.length > 0 && (
        <Card className="mb-4 px-4 py-3 flex flex-wrap items-end gap-3">
          <label className="text-[11px] text-slate-400 flex flex-col gap-1">
            {t('Buyer app')}
            <select value={np} onChange={e => setNp(e.target.value)} className="bg-slate-900 border border-white/10 rounded-lg px-2 py-1.5 text-xs text-white">
              <option value="">{t('All buyer apps ({count})', { count: allBaps.length })}</option>
              {allBaps.map(g => <option key={g.bap_id} value={g.bap_id}>{g.bap_id}</option>)}
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
            <button onClick={() => { setNp(''); setFrom(''); setTo('') }} className="text-xs text-slate-400 hover:text-white px-3 py-1.5 rounded-lg border border-white/10">{t('Clear filters')}</button>
          )}
          <p className="text-[11px] text-slate-500 basis-full">{t('Each buyer app gets its own /reconcile. Tick any due or disputed contracts that match the filters; they go out together as a new batch.')}</p>
        </Card>
      )}

      {error && <div className="mb-4"><ErrorBox message={error} onRetry={reload} /></div>}
      {notice && (
        <div className={`mb-4 px-4 py-3 rounded-xl text-sm border ${notice.ok ? 'bg-emerald-500/10 border-emerald-500/20 text-emerald-300' : 'bg-red-500/10 border-red-500/20 text-red-300'}`}>
          {notice.text}
        </div>
      )}

      {loading && !data ? <LoadingSpinner label={t('Loading settlements…')} /> : baps.length === 0 ? (
        <Card className="p-8 text-center text-sm text-slate-500">{t('Nothing to reconcile — every settlement is in a batch.')}</Card>
      ) : baps.map(g => (
        <BapReconCard key={g.bap_id} g={g} now={now} visible={visible} sending={sending === g.bap_id} onSend={send} />
      ))}

      <SectionTitleBar>{t('Batches')}</SectionTitleBar>
      <Card>
        {!batches.length ? <p className="px-5 py-8 text-center text-sm text-slate-500">{t('No reconciliation exchanged yet.')}</p> : (
          <div className="divide-y divide-white/[0.05]">
            {batches.map(b => {
              let sum = b.summary
              try { if (typeof sum === 'string') sum = JSON.parse(sum) } catch { }
              const open = openBatch === b.id
              const sent = b.direction !== 'INBOUND'
              return (
                <div key={b.id}>
                  <button onClick={() => setOpenBatch(open ? null : b.id)}
                    className="w-full text-left px-5 py-3 flex flex-wrap items-center gap-3 hover:bg-white/[0.025]">
                    <ChevronDown size={14} className={`text-slate-500 transition-transform ${open ? 'rotate-180 text-cyan-400' : ''}`} />
                    <span className={`text-[10px] font-bold uppercase tracking-widest ${sent ? 'text-cyan-300' : 'text-slate-400'}`}>{sent ? t('Sent') : t('Received')}</span>
                    <span className="font-mono text-xs text-slate-300 truncate max-w-[22rem]" title={b.id}>{b.id}</span>
                    <StatusBadge status={b.status} />
                    <span className="text-xs text-slate-500">{t('{count} contracts', { count: b.contract_ids?.length || 0 })}</span>
                    <span className="ml-auto text-xs font-semibold text-emerald-300 tabular-nums">{rupiah(sum?.totalNetPayable)}</span>
                    <span className="text-[11px] text-slate-500 whitespace-nowrap">{fmtDate(b.created_at)}</span>
                  </button>
                  {b.error_message && <p className="px-5 pb-2 -mt-1 text-xs text-red-300 break-words">{b.error_message}</p>}
                  {open && (
                    <div className="px-5 pb-4 grid grid-cols-1 lg:grid-cols-2 gap-3">
                      {[[sent ? t('/reconcile sent') : t('/reconcile received'), b.request_payload],
                        [sent ? t('/on_reconcile received') : t('/on_reconcile sent'), b.response_payload]].map(([label, payload]) => (
                        <div key={label} className="rounded-xl border border-white/[0.07] bg-slate-950/60 overflow-hidden">
                          <p className="px-3 py-2 text-[11px] font-semibold text-slate-400 border-b border-white/[0.06]">{label}</p>
                          {payload ? (
                            <div className="max-h-[28rem] overflow-auto p-3 text-[11px] leading-relaxed font-mono text-slate-300 whitespace-pre">
                              <JsonNode value={typeof payload === 'string' ? JSON.parse(payload) : payload} depth={0} last />
                            </div>
                          ) : <p className="p-3 text-xs text-slate-500">{t('Nothing yet')}</p>}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </Card>
    </div>
  )
}

function SectionTitleBar({ children }) {
  return <p className="mt-2 mb-3 text-[11px] font-bold uppercase tracking-widest text-slate-500">{children}</p>
}

// ─── OrderRatings (inline inside OrderDetailDrawer) ──────────────────────────

function OrderRatings({ contractId }) {
  const { t } = useI18n()
  const { data, loading } = useApi(contractId ? `/ratings?limit=20` : null, [contractId])
  const ratings = (data?.items || []).filter(r => r.contractId === contractId)
  if (loading) return null
  if (!ratings.length) return null
  return (
    <section>
      <SectionTitle>{t('Customer Ratings ({count})', { count: ratings.length })}</SectionTitle>
      <div className="space-y-2">
        {ratings.map(r => {
          let range = r.range
          try { if (typeof range === 'string') range = JSON.parse(range) } catch { }
          const score = range?.value ?? range?.score ?? '?'
          const max = range?.max ?? 5
          return (
            <Card key={r.id} className="p-4">
              <div className="flex items-center justify-between mb-1">
                <div className="flex items-center gap-1">
                  {Array.from({ length: max }, (_, i) => (
                    <Star key={i} size={12} className={i < score ? 'text-amber-400 fill-amber-400' : 'text-slate-600'} />
                  ))}
                  <span className="text-xs text-slate-400 ml-1">{score}/{max}</span>
                </div>
                <span className="text-[10px] text-slate-500">{fmtDate(r.createdAt)}</span>
              </div>
              <p className="text-[10px] text-slate-500">{r.bapId}</p>
            </Card>
          )
        })}
      </div>
    </section>
  )
}

// ─── SupportPage ─────────────────────────────────────────────────────────────

function SupportPage() {
  const { t } = useI18n()
  const [page, setPage] = useState(1)
  const limit = 20
  const { data, loading, error, reload } = useApi(`/support-tickets?page=${page}&limit=${limit}`, [page], { live: true })
  const items = data?.items || []
  const total = data?.total ?? 0
  const totalPages = Math.ceil(total / limit) || 1

  const statusCls = {
    OPEN: 'bg-amber-500/15 text-amber-400 border border-amber-500/25',
    RESOLVED: 'bg-emerald-500/15 text-emerald-400 border border-emerald-500/25',
  }

  return (
    <div>
      <PageHeader title={t('Support Tickets')} subtitle={t('Customer support requests received via Beckn protocol')}
        action={<button onClick={reload} className="shrink-0 whitespace-nowrap flex items-center gap-2 text-xs font-semibold px-4 py-2 rounded-xl text-slate-300 border border-white/10 hover:border-white/20 transition-all"><RefreshCw size={13} /> {t('Refresh')}</button>} />

      {error && <ErrorBox message={error} onRetry={reload} />}

      <Card>
        {loading ? <LoadingSpinner label={t('Loading tickets…')} /> : (
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-white/[0.06]">
                {['Ticket ID', 'Order (Txn)', 'BAP', 'Description', 'Status', 'Created'].map(h => (
                  <th key={h} className="px-5 py-3 text-left text-[10px] font-bold uppercase tracking-widest text-slate-500">{t(h)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {items.map(tk => (
                <tr key={tk.id} className="border-b border-white/[0.04] hover:bg-white/[0.02] transition-colors">
                  <td className="px-5 py-3 font-mono text-slate-400">{tk.id.slice(0, 8)}…</td>
                  <td className="px-5 py-3 font-mono text-slate-400">{tk.transactionId ? tk.transactionId.slice(0, 8) + '…' : '—'}</td>
                  <td className="px-5 py-3 text-slate-300 max-w-[140px] truncate">{tk.bapId || '—'}</td>
                  <td className="px-5 py-3 text-slate-300 max-w-[200px] truncate">{tk.name || tk.shortDesc || '—'}</td>
                  <td className="px-5 py-3">
                    <span className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-semibold ${statusCls[tk.status] || 'bg-slate-500/15 text-slate-400'}`}>
                      {tk.status ? t(tk.status) : '—'}
                    </span>
                  </td>
                  <td className="px-5 py-3 text-slate-500">{fmtDateShort(tk.createdAt)}</td>
                </tr>
              ))}
              {!items.length && (
                <tr><td colSpan={6} className="px-5 py-12 text-center text-slate-500">{t('No support tickets yet')}</td></tr>
              )}
            </tbody>
          </table>
        )}
        <Pagination page={page} totalPages={totalPages} onPage={setPage} total={total} limit={limit} />
      </Card>
    </div>
  )
}

// ─── RatingsPage ─────────────────────────────────────────────────────────────

function RatingsPage() {
  const { t } = useI18n()
  const [page, setPage] = useState(1)
  const limit = 20
  const { data, loading, error, reload } = useApi(`/ratings?page=${page}&limit=${limit}`, [page], { live: true })
  const items = data?.items || []
  const total = data?.total ?? 0
  const totalPages = Math.ceil(total / limit) || 1

  return (
    <div>
      <PageHeader title={t('Ratings')} subtitle={t('Order ratings submitted by buyers')}
        action={<button onClick={reload} className="shrink-0 whitespace-nowrap flex items-center gap-2 text-xs font-semibold px-4 py-2 rounded-xl text-slate-300 border border-white/10 hover:border-white/20 transition-all"><RefreshCw size={13} /> {t('Refresh')}</button>} />

      {error && <ErrorBox message={error} onRetry={reload} />}

      <Card>
        {loading ? <LoadingSpinner label={t('Loading ratings…')} /> : (
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-white/[0.06]">
                {['Order (Txn)', 'BAP', 'Target', 'Score', 'Date'].map(h => (
                  <th key={h} className="px-5 py-3 text-left text-[10px] font-bold uppercase tracking-widest text-slate-500">{t(h)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {items.map(r => {
                let range = r.range
                try { if (typeof range === 'string') range = JSON.parse(range) } catch { }
                const score = range?.value ?? range?.score ?? '?'
                const max = range?.max ?? 5
                return (
                  <tr key={r.id} className="border-b border-white/[0.04] hover:bg-white/[0.02] transition-colors">
                    <td className="px-5 py-3 font-mono text-slate-400">{r.transactionId ? r.transactionId.slice(0, 8) + '…' : '—'}</td>
                    <td className="px-5 py-3 text-slate-300 max-w-[140px] truncate">{r.bapId || '—'}</td>
                    <td className="px-5 py-3 text-slate-400 max-w-[120px] truncate">{r.targetId || '—'}</td>
                    <td className="px-5 py-3">
                      <div className="flex items-center gap-1">
                        {Array.from({ length: max }, (_, i) => (
                          <Star key={i} size={11} className={i < score ? 'text-amber-400 fill-amber-400' : 'text-slate-700'} />
                        ))}
                        <span className="ml-1 text-slate-300 font-semibold">{score}/{max}</span>
                      </div>
                    </td>
                    <td className="px-5 py-3 text-slate-500">{fmtDateShort(r.createdAt)}</td>
                  </tr>
                )
              })}
              {!items.length && (
                <tr><td colSpan={5} className="px-5 py-12 text-center text-slate-500">{t('No ratings yet')}</td></tr>
              )}
            </tbody>
          </table>
        )}
        <Pagination page={page} totalPages={totalPages} onPage={setPage} total={total} limit={limit} />
      </Card>
    </div>
  )
}

// ─── Catalog Management ───────────────────────────────────────────────────────

function CatalogsPage({ onNav }) {
  const { t } = useI18n()
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [publishing, setPublishing] = useState(null)
  const limit = 20

  // provider_id is no longer a query param -- the API always scopes to the
  // logged-in seller's own provider (see internal/dashboardsvc), so there's
  // nothing left to filter by there.
  const url = `/catalogs?search=${encodeURIComponent(search)}&page=${page}&limit=${limit}`
  const { data, loading, error, reload } = useApi(url, [search, page])

  const items = data?.items || []
  const total = data?.total || 0
  const totalPages = Math.max(1, Math.ceil(total / limit))

  const handlePublish = async (id) => {
    if (!window.confirm(t('Publish catalog "{id}" to the network?', { id }))) return
    setPublishing(id)
    try {
      await apiFetch(`/catalogs/${id}/publish`, { method: 'POST' })
      alert(t('Catalog published successfully!'))
    } catch (e) {
      alert(t('Publish failed: {error}', { error: e.message }))
    } finally {
      setPublishing(null)
    }
  }

  const fmtDate = ts => ts ? new Date(ts).toLocaleDateString(localeNow()) : '—'

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-2xl font-bold text-white tracking-tight">{t('Catalogs')}</h1>
          <p className="text-sm text-slate-500 mt-0.5">{t('Create catalogs, add products, and publish to the network')}</p>
        </div>
        <button data-tour="create-catalog" onClick={() => onNav('create_catalog')}
          className="shrink-0 whitespace-nowrap flex items-center gap-2 px-4 py-2 rounded-xl text-sm font-semibold text-white shadow-lg transition-all hover:scale-105 active:scale-95"
          style={{ background: BRAND }}>
          <Plus size={15} /> {t('Create Catalog')}
        </button>
      </div>

      {/* Filters */}
      <Card>
        <div className="flex flex-wrap gap-3 items-center">
          <div className="relative flex-1 min-w-52">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" />
            <input value={search} onChange={e => { setSearch(e.target.value); setPage(1) }}
              placeholder={t('Search by name, ID or provider…')}
              className="w-full pl-9 pr-3 py-2 rounded-lg text-sm text-white placeholder-slate-600 border border-white/[0.08] bg-slate-900 focus:outline-none focus:border-blue-500/50 transition-colors" />
          </div>
          <button onClick={reload} className="p-2 rounded-lg border border-white/[0.08] text-slate-400 hover:text-white transition-colors">
            <RefreshCw size={14} />
          </button>
        </div>
      </Card>

      {/* Table */}
      <Card>
        {loading ? <LoadingSpinner /> : error ? <ErrorBox message={error} /> : (
          <div className="overflow-x-auto"><table className="w-full text-sm min-w-[500px]">
            <thead>
              <tr className="border-b border-white/[0.06]">
                {['Catalog', 'Provider', 'Products', 'Status', 'Validity', 'Actions'].map(h => (
                  <th key={h} className="px-4 py-3 text-left text-[10px] uppercase tracking-widest text-slate-500 font-semibold">{t(h)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {items.map(cat => (
                <tr key={cat.id} className="border-b border-white/[0.04] hover:bg-white/[0.02] transition-colors">
                  <td className="px-4 py-3">
                    <p className="font-semibold text-white">{cat.name}</p>
                    <p className="text-[11px] text-slate-500 font-mono mt-0.5">{cat.id}</p>
                  </td>
                  <td className="px-4 py-3 text-slate-400 text-xs">{cat.providerId}</td>
                  <td className="px-4 py-3">
                    <span className="text-slate-300 text-xs">{t('{res} products · {offers} offers', { res: cat.resourceCount, offers: cat.offerCount })}</span>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex flex-col gap-0.5">
                      {cat.isPublished ? (
                        <>
                          <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-bold bg-emerald-500/15 text-emerald-400 w-fit">
                            <CheckCircle2 size={9} /> {t('Published')}
                          </span>
                          <span className="text-[10px] text-slate-600">{fmtDate(cat.networkPublishedAt)}</span>
                        </>
                      ) : (
                        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-bold bg-amber-500/15 text-amber-400 w-fit">
                          <Circle size={9} className="fill-amber-400" /> {t('Draft')}
                        </span>
                      )}
                      {!cat.isActive && (
                        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-bold bg-amber-500/15 text-amber-400 w-fit">
                          <PauseCircle size={9} /> {t('Paused')}
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-3 text-xs text-slate-500">
                    {cat.validityStart ? `${fmtDate(cat.validityStart)} – ${fmtDate(cat.validityEnd)}` : '—'}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-2">
                      <button onClick={() => onNav('catalog_detail', { catalogId: cat.id })}
                        className="px-2.5 py-1 rounded-md text-xs font-medium border border-white/[0.10] text-slate-300 hover:text-white hover:border-white/30 transition-colors flex items-center gap-1">
                        <Eye size={11} /> {t('View')}
                      </button>
                      <button onClick={() => onNav('add_product', { catalogId: cat.id })}
                        className="px-2.5 py-1 rounded-md text-xs font-medium border border-blue-500/30 text-blue-400 hover:bg-blue-500/10 transition-colors flex items-center gap-1">
                        <Plus size={11} /> {t('Product')}
                      </button>
                      <button onClick={() => handlePublish(cat.id)} disabled={publishing === cat.id || !cat.resourceCount && !cat.offerCount}
                        title={(!cat.resourceCount && !cat.offerCount) ? t('Add a product or offer before publishing') : undefined}
                        className="px-2.5 py-1 rounded-md text-xs font-medium border border-emerald-500/30 text-emerald-400 hover:bg-emerald-500/10 transition-colors flex items-center gap-1 disabled:opacity-40">
                        {publishing === cat.id ? <RefreshCw size={11} className="animate-spin" /> : <Send size={11} />} {cat.isPublished ? t('Update') : t('Publish')}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {!items.length && (
                <tr><td colSpan={6} className="px-4 py-12 text-center text-slate-500">
                  {t('No catalogs yet.')} <button onClick={() => onNav('create_catalog')} className="text-blue-400 hover:underline ml-1">{t('Create one →')}</button>
                </td></tr>
              )}
            </tbody>
          </table>
          </div>
        )}
        <Pagination page={page} totalPages={totalPages} onPage={setPage} total={total} limit={limit} />
      </Card>
    </div>
  )
}

// ─── Create Catalog Page ──────────────────────────────────────────────────────

const CATALOG_AUTOFILL_POOL = [
  {
    id: 'cat-outdoor-gear', name: 'Outdoor Gear Store', short_desc: 'Camping, hiking & adventure equipment',
    provider_id: 'provider-venky-bazaar', provider_name: 'Venky Bazaar'
  },
  {
    id: 'cat-indonesian-snacks', name: 'Toko Camilan Nusantara', short_desc: 'Authentic Indonesian biscuits, chips & sweets',
    provider_id: 'provider-toko-nusantara', provider_name: 'Toko Nusantara'
  },
  {
    id: 'cat-electronics', name: 'Tech Essentials Hub', short_desc: 'Gadgets, accessories and consumer electronics',
    provider_id: 'provider-techmart', provider_name: 'TechMart'
  },
  {
    id: 'cat-apparel', name: 'FashionForward Apparel', short_desc: 'Trendy clothing for men and women',
    provider_id: 'provider-fashionco', provider_name: 'FashionCo'
  },
]
let _catalogFillIdx = 0

function CreateCatalogPage({ onNav }) {
  const { t } = useI18n()
  const [form, setForm] = useState({
    id: '', name: '', short_desc: '',
    provider_id: '', provider_name: '',
    validity_start: '', validity_end: '',
  })
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const ownProvider = useOwnProvider()

  const labelCls = 'block text-[11px] uppercase tracking-widest text-slate-500 font-semibold mb-1'
  const inputCls = 'w-full px-3 py-2 rounded-lg text-sm text-white placeholder-slate-600 border border-white/[0.08] bg-slate-900 focus:outline-none focus:border-blue-500/50 transition-colors'

  const set = (k, v) => setForm(f => ({ ...f, [k]: v }))
  // The provider is the seller's own account, never typed in.
  useEffect(() => {
    if (ownProvider) setForm(f => ({ ...f, provider_id: ownProvider.id, provider_name: ownProvider.name }))
  }, [ownProvider])

  const handleAutoFill = () => {
    const seed = CATALOG_AUTOFILL_POOL[_catalogFillIdx % CATALOG_AUTOFILL_POOL.length]
    _catalogFillIdx++
    const suffix = Date.now().toString(36)
    const now = new Date()
    const start = now.toISOString().slice(0, 16)
    const end = new Date(now.getTime() + 365 * 24 * 60 * 60 * 1000).toISOString().slice(0, 16)
    setForm({
      ...seed,
      ...(ownProvider ? { provider_id: ownProvider.id, provider_name: ownProvider.name } : {}),
      id: `${seed.id}-${suffix}`, validity_start: start, validity_end: end,
    })
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (!form.id || !form.name || !form.provider_id || !form.provider_name) {
      setError(t('Catalog ID, Name, Provider ID and Provider Name are required.'))
      return
    }
    setLoading(true)
    setError(null)
    try {
      await apiFetch('/catalogs', {
        method: 'POST',
        body: JSON.stringify({
          id: form.id,
          descriptor: { name: form.name, shortDesc: form.short_desc },
          provider: { id: form.provider_id, descriptor: { name: form.provider_name } },
          ...(form.validity_start && form.validity_end ? {
            validity: { startDate: new Date(form.validity_start).toISOString(), endDate: new Date(form.validity_end).toISOString() }
          } : {}),
        }),
      })
      onNav('catalog_detail', { catalogId: form.id })
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="max-w-2xl space-y-6">
      <div className="flex items-center gap-3">
        <button onClick={() => onNav('catalogs')} className="p-2 rounded-lg border border-white/[0.08] text-slate-400 hover:text-white transition-colors">
          <ArrowLeft size={16} />
        </button>
        <div className="flex-1">
          <h1 className="text-2xl font-bold text-white tracking-tight">{t('Create Catalog')}</h1>
          <p className="text-sm text-slate-500 mt-0.5">{t('Fill in the catalog details — add products after creation')}</p>
        </div>
        <button type="button" data-tour="catalog-autofill" onClick={handleAutoFill}
          className="flex items-center gap-2 px-3 py-2 rounded-xl text-xs font-semibold border border-purple-500/30 text-purple-400 hover:bg-purple-500/10 transition-colors">
          <Wand2 size={13} /> {t('Auto-fill Sample')}
        </button>
      </div>

      {error && <ErrorBox message={error} />}

      <Card data-tour="catalog-form">
        <form onSubmit={handleSubmit} className="space-y-5">
          <p className="text-xs uppercase tracking-widest text-slate-500 font-bold pb-2 border-b border-white/[0.06]">{t('Catalog Info')}</p>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div><label className={labelCls}>{t('Catalog ID *')}</label>
              <input className={inputCls} value={form.id} onChange={e => set('id', e.target.value)} placeholder="cat-outdoor-2026" required />
            </div>
            <div><label className={labelCls}>{t('Catalog Name *')}</label>
              <input className={inputCls} value={form.name} onChange={e => set('name', e.target.value)} placeholder={t('Outdoor Gear 2026')} required />
            </div>
            <div className="col-span-2"><label className={labelCls}>{t('Short Description')}</label>
              <input className={inputCls} value={form.short_desc} onChange={e => set('short_desc', e.target.value)} placeholder={t('Camping and hiking gear for adventurers')} />
            </div>
          </div>

          <p className="text-xs uppercase tracking-widest text-slate-500 font-bold pb-2 border-b border-white/[0.06] mt-4 flex items-center gap-2">
            {t('Provider')}
            <span className="normal-case tracking-normal font-medium text-slate-600 flex items-center gap-1"><Lock size={10} /> {t('From your account')}</span>
          </p>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div><label className={labelCls}>{t('Provider ID *')}</label>
              <input className={inputCls + ' opacity-60 cursor-not-allowed'} value={form.provider_id} readOnly placeholder={t('Loading…')}
                title={t('Your provider comes from your account')} />
            </div>
            <div><label className={labelCls}>{t('Provider Name *')}</label>
              <input className={inputCls + ' opacity-60 cursor-not-allowed'} value={form.provider_name} readOnly placeholder={t('Loading…')}
                title={t('Your provider comes from your account')} />
            </div>
          </div>

          <p className="text-xs uppercase tracking-widest text-slate-500 font-bold pb-2 border-b border-white/[0.06] mt-4">{t('Validity (optional)')}</p>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div><label className={labelCls}>{t('Start Date')}</label>
              <input type="datetime-local" className={inputCls} value={form.validity_start} onChange={e => set('validity_start', e.target.value)} />
            </div>
            <div><label className={labelCls}>{t('End Date')}</label>
              <input type="datetime-local" className={inputCls} value={form.validity_end} onChange={e => set('validity_end', e.target.value)} />
            </div>
          </div>

          <div className="flex gap-3 pt-2">
            <button type="button" onClick={() => onNav('catalogs')}
              className="px-5 py-2.5 rounded-xl text-sm font-semibold border border-white/[0.10] text-slate-400 hover:text-white transition-colors">
              {t('Cancel')}
            </button>
            <button type="submit" data-tour="catalog-submit" disabled={loading}
              className="flex-1 px-5 py-2.5 rounded-xl text-sm font-semibold text-white shadow-lg transition-all hover:scale-[1.01] active:scale-[0.99] disabled:opacity-50"
              style={{ background: BRAND }}>
              {loading ? t('Creating…') : t('Create Catalog & Add Products →')}
            </button>
          </div>
        </form>
      </Card>
    </div>
  )
}

// ─── Catalog Detail Page ──────────────────────────────────────────────────────

function CatalogDetailPage({ onNav, catalogId }) {
  const { t } = useI18n()
  const { data, loading, error, reload } = useApi(`/catalogs/${catalogId}`, [catalogId])
  const [publishing, setPublishing] = useState(false)
  const [togglingActive, setTogglingActive] = useState(false)
  const [deleting, setDeleting] = useState(false)

  const cat = data?.catalog || {}
  const resources = data?.resources || []
  const offers = data?.offers || []
  const catalogEmpty = !resources.length && !offers.length

  // Build a map from resource_id → ALL offers referencing it (an offer's
  // resourceIds is an array, so more than one offer can share a resource).
  const offersByResourceId = {}
  offers.forEach(o => {
    (o.resourceIds || []).forEach(rid => {
      (offersByResourceId[rid] ||= []).push(o)
    })
  })

  // Flatten to one row per resource-offer pair (a resource with N offers
  // gets N rows; a resource with none gets a single offer-less row) so
  // every offer is individually visible, editable and removable -- a
  // resource with 2+ live offers otherwise hid every offer but the last
  // from this page, including from "Remove offer".
  const productRows = resources.flatMap(r => {
    const rOffers = offersByResourceId[r.id] || []
    if (rOffers.length === 0) return [{ resource: r, offer: null, isFirst: true }]
    return rOffers.map((offer, i) => ({ resource: r, offer, isFirst: i === 0 }))
  })

  const handlePublish = async () => {
    if (!window.confirm(t('Publish this catalog to the Beckn network?'))) return
    setPublishing(true)
    try {
      await apiFetch(`/catalogs/${catalogId}/publish`, { method: 'POST' })
      reload()
      alert(t('Published successfully!'))
    } catch (e) {
      alert(t('Publish failed: {error}', { error: e.message }))
    } finally {
      setPublishing(false)
    }
  }

  const handleToggleActive = async () => {
    const next = !cat.isActive
    const msg = next
      ? t('Resume this catalog? It will be marked active on the next publish.')
      : t('Pause this catalog? It stays fully intact, but will be excluded from search once you publish this change.')
    if (!window.confirm(msg)) return
    setTogglingActive(true)
    try {
      await apiFetch(`/catalogs/${catalogId}/active`, {
        method: 'PATCH',
        body: JSON.stringify({ isActive: next }),
      })
      reload()
    } catch (e) {
      alert(t('Update failed: {error}', { error: e.message }))
    } finally {
      setTogglingActive(false)
    }
  }

  const handleRemoveOffer = async (oid) => {
    if (!window.confirm(t('Remove this offer? This cannot be undone.'))) return
    try {
      await apiFetch(`/catalogs/${catalogId}/offers/${oid}`, { method: 'DELETE' })
      reload()
    } catch (e) {
      alert(t('Remove failed: {error}', { error: e.message }))
    }
  }

  const handleDeleteCatalog = async () => {
    const name = cat.name || catalogId
    const msg = cat.isPublished
      ? t('This permanently retires "{name}" from the Beckn network. This cannot be undone, and this catalog ID can never be reused. Continue?', { name })
      : t('Delete "{name}"? It has never been published, so this just removes it from your catalog list.', { name })
    if (!window.confirm(msg)) return
    setDeleting(true)
    try {
      await apiFetch(`/catalogs/${catalogId}`, { method: 'DELETE' })
      onNav('catalogs')
    } catch (e) {
      alert(t('Delete failed: {error}', { error: e.message }))
      setDeleting(false)
    }
  }

  const fmtDate = ts => ts ? new Date(ts).toLocaleDateString(localeNow()) : '—'

  const getPrice = (offer) => {
    if (!offer) return '—'
    try {
      const ca = typeof offer.considerationAttributes === 'string'
        ? JSON.parse(offer.considerationAttributes)
        : offer.considerationAttributes
      if (ca?.totalAmount !== undefined) return `${ca.currency || ''} ${ca.totalAmount}`.trim()
      if (ca?.breakup?.[0]?.amount !== undefined) return `${ca.breakup[0].amount}`
    } catch { /* ignore */ }
    return '—'
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center gap-4">
        <button onClick={() => onNav('catalogs')} className="p-2 rounded-lg border border-white/[0.08] text-slate-400 hover:text-white transition-colors">
          <ArrowLeft size={16} />
        </button>
        <div className="flex-1">
          {loading ? <div className="h-7 w-48 rounded bg-white/[0.06] animate-pulse" /> : (
            <>
              <div className="flex items-center gap-3">
                <h1 className="text-2xl font-bold text-white tracking-tight">{cat.name || catalogId}</h1>
                {cat.isPublished ? (
                  <span data-tour="published-badge" className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-[10px] font-bold bg-emerald-500/15 text-emerald-400">
                    <CheckCircle2 size={10} /> {t('Published')}
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-[10px] font-bold bg-amber-500/15 text-amber-400">
                    <Circle size={10} className="fill-amber-400" /> {t('Draft')}
                  </span>
                )}
                {!cat.isActive && (
                  <span className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-[10px] font-bold bg-amber-500/15 text-amber-400">
                    <PauseCircle size={10} /> {t('Paused')}
                  </span>
                )}
              </div>
              <p className="text-xs text-slate-500 font-mono mt-0.5">{cat.id} · {cat.providerId}
                {cat.networkPublishedAt && <span className="ml-2 text-emerald-600">· {t('Last published {date}', { date: fmtDate(cat.networkPublishedAt) })}</span>}
              </p>
            </>
          )}
        </div>
        <div className="flex gap-2">
          <button onClick={handleDeleteCatalog} disabled={deleting}
            title={t('Permanently delete this catalog')}
            className="flex items-center gap-2 px-3 py-2 rounded-xl text-sm font-semibold border border-red-500/30 text-red-400 hover:bg-red-500/10 transition-colors disabled:opacity-50">
            <Trash2 size={14} />
            {deleting ? t('Deleting…') : t('Delete Catalog')}
          </button>
          <button onClick={handleToggleActive} disabled={togglingActive}
            className="flex items-center gap-2 px-3 py-2 rounded-xl text-sm font-semibold border border-slate-500/30 text-slate-400 hover:bg-slate-500/10 transition-colors disabled:opacity-50">
            {cat.isActive ? <PauseCircle size={14} /> : <PlayCircle size={14} />}
            {cat.isActive ? t('Pause') : t('Resume')}
          </button>
          <button onClick={() => onNav('add_offer', { catalogId })}
            className="flex items-center gap-2 px-3 py-2 rounded-xl text-sm font-semibold border border-slate-500/30 text-slate-400 hover:bg-slate-500/10 transition-colors">
            <Tag size={14} /> {t('Add Offer')}
          </button>
          <button data-tour="add-product" onClick={() => onNav('add_product', { catalogId })}
            className="flex items-center gap-2 px-4 py-2 rounded-xl text-sm font-semibold border border-blue-500/30 text-blue-400 hover:bg-blue-500/10 transition-colors">
            <Plus size={14} /> {t('Add Product')}
          </button>
          <div className="flex flex-col items-end gap-1">
            <button data-tour="publish" onClick={handlePublish} disabled={publishing || catalogEmpty}
              title={catalogEmpty ? t('Add a product or offer before publishing') : undefined}
              className="flex items-center gap-2 px-4 py-2 rounded-xl text-sm font-semibold text-white shadow-lg transition-all hover:scale-105 active:scale-95 disabled:opacity-50 disabled:hover:scale-100">
              {publishing ? <RefreshCw size={14} className="animate-spin" /> : <Send size={14} />}
              {publishing ? t('Publishing…') : cat.isPublished ? t('Update on Network') : t('Publish to Network')}
            </button>
            {catalogEmpty && (
              <p className="text-[11px] text-amber-500">{t('Add a product or offer before publishing')}</p>
            )}
          </div>
        </div>
      </div>

      {error && <ErrorBox message={error} />}

      {/* Catalog meta */}
      {!loading && cat.id && (
        <div className="grid grid-cols-4 gap-4">
          <Card className="p-4">
            <p className="text-[10px] uppercase tracking-widest text-slate-600 font-bold mb-1">{t('Provider')}</p>
            <p className="text-sm font-semibold text-slate-200">{cat.providerName || cat.providerId}</p>
          </Card>
          <Card className="p-4">
            <p className="text-[10px] uppercase tracking-widest text-slate-600 font-bold mb-1">{t('Products')}</p>
            <p className="text-sm font-semibold text-slate-200">{t('{res} products · {offers} offers', { res: resources.length, offers: offers.length })}</p>
          </Card>
          <Card className="p-4">
            <p className="text-[10px] uppercase tracking-widest text-slate-600 font-bold mb-1">{t('Validity')}</p>
            <p className="text-sm font-semibold text-slate-200">{cat.validityStart ? `${fmtDate(cat.validityStart)} – ${fmtDate(cat.validityEnd)}` : t('Not set')}</p>
          </Card>
          <Card className="p-4">
            <p className="text-[10px] uppercase tracking-widest text-slate-600 font-bold mb-1">{t('Network Status')}</p>
            {cat.isPublished ? (
              <div>
                <span className="inline-flex items-center gap-1 text-xs font-bold text-emerald-400"><CheckCircle2 size={11} /> {t('Published')}</span>
                <p className="text-[10px] text-slate-600 mt-0.5">{fmtDate(cat.networkPublishedAt)}</p>
              </div>
            ) : (
              <span className="inline-flex items-center gap-1 text-xs font-bold text-amber-400"><Circle size={11} className="fill-amber-400" /> {t('Draft (never published)')}</span>
            )}
            {!cat.isActive && (
              <p className="text-[10px] text-amber-500 mt-1 flex items-center gap-1"><PauseCircle size={10} /> {t('Paused — excluded from search once published')}</p>
            )}
          </Card>
        </div>
      )}

      {/* Products table */}
      <Card>
        <div className="flex items-center justify-between mb-4">
          <div>
            <p className="text-sm font-bold text-slate-200">{t('Products')}</p>
          </div>
          <div className="flex gap-2">
            <button onClick={reload} className="p-1.5 rounded-lg border border-white/[0.08] text-slate-500 hover:text-white transition-colors">
              <RefreshCw size={13} />
            </button>
            <button onClick={() => onNav('add_product', { catalogId })}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold text-white transition-all hover:scale-105"
              style={{ background: BRAND }}>
              <Plus size={12} /> {t('Add Product')}
            </button>
          </div>
        </div>
        {loading ? <LoadingSpinner /> : (
          <div className="overflow-x-auto"><table className="w-full text-sm min-w-[500px]">
            <thead>
              <tr className="border-b border-white/[0.06]">
                {['Resource', 'Description', 'Price', 'Offer ID', 'Validity', ''].map(h => (
                  <th key={h} className="px-4 py-2 text-left text-[10px] uppercase tracking-widest text-slate-500 font-semibold">{h && t(h)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {productRows.map(({ resource: r, offer, isFirst }, idx) => (
                <tr key={`${r.id}-${offer?.id || 'none'}-${idx}`} className="border-b border-white/[0.04] hover:bg-white/[0.02] transition-colors">
                  <td className="px-4 py-3">
                    {isFirst ? (
                      <div className="flex items-center gap-3">
                        {r.mediaFiles && r.mediaFiles !== '[]' && r.mediaFiles !== 'null' && (() => {
                          try {
                            const mf = typeof r.mediaFiles === 'string' ? JSON.parse(r.mediaFiles) : r.mediaFiles
                            const url = Array.isArray(mf) ? mf[0]?.url : mf?.url
                            if (url) return <img src={url} alt="" className="w-10 h-10 rounded-lg object-cover border border-white/10 shrink-0" />
                          } catch { return null }
                          return null
                        })()}
                        <div>
                          <p className="font-semibold text-white">{r.name}</p>
                          <p className="text-[11px] font-mono text-slate-500">{r.id}</p>
                        </div>
                      </div>
                    ) : (
                      <p className="text-[11px] text-slate-600 italic pl-[52px]">{t('(same product)')}</p>
                    )}
                  </td>
                  <td className="px-4 py-3 text-slate-400 text-xs max-w-48 truncate">{isFirst ? (r.shortDesc || '—') : ''}</td>
                  <td className="px-4 py-3 font-semibold text-emerald-400">{getPrice(offer)}</td>
                  <td className="px-4 py-3 text-[11px] font-mono text-slate-500">{offer?.id || '—'}</td>
                  <td className="px-4 py-3 text-xs text-slate-500">
                    {offer?.validityStart ? `${fmtDate(offer.validityStart)} – ${fmtDate(offer.validityEnd)}` : '—'}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex justify-end gap-1.5">
                      {isFirst && (
                        <button onClick={() => onNav('edit_product', { catalogId, editResourceId: r.id })}
                          className="flex items-center gap-1 px-2 py-1 rounded-lg text-[11px] font-semibold border border-white/[0.08] text-slate-300 hover:text-white hover:border-white/20 transition-colors whitespace-nowrap">
                          <Pencil size={11} /> {t('Edit product')}
                        </button>
                      )}
                      {offer ? (
                        <>
                          <button onClick={() => onNav('edit_offer', { catalogId, editOfferId: offer.id })}
                            className="flex items-center gap-1 px-2 py-1 rounded-lg text-[11px] font-semibold border border-white/[0.08] text-slate-300 hover:text-white hover:border-white/20 transition-colors whitespace-nowrap">
                            <Tag size={11} /> {t('Edit offer')}
                          </button>
                          <button onClick={() => handleRemoveOffer(offer.id)}
                            className="flex items-center gap-1 px-2 py-1 rounded-lg text-[11px] font-semibold border border-red-500/30 text-red-400 hover:bg-red-500/10 transition-colors whitespace-nowrap">
                            <Trash2 size={11} /> {t('Remove offer')}
                          </button>
                        </>
                      ) : (
                        <button onClick={() => onNav('add_offer', { catalogId, preselectedResourceId: r.id })}
                          className="flex items-center gap-1 px-2 py-1 rounded-lg text-[11px] font-semibold border border-blue-500/30 text-blue-400 hover:bg-blue-500/10 transition-colors whitespace-nowrap">
                          <Plus size={11} /> {t('Add offer')}
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
              {!resources.length && (
                <tr><td colSpan={6} className="px-4 py-12 text-center text-slate-500">
                  {t('No products yet.')}{' '}
                  <button onClick={() => onNav('add_product', { catalogId })} className="text-blue-400 hover:underline">{t('Add the first product →')}</button>
                </td></tr>
              )}
            </tbody>
          </table>
          </div>
        )}
      </Card>
    </div>
  )
}

// ─── Add Product Page ─────────────────────────────────────────────────────────

const DAYS_OF_WEEK = ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN']

const PRODUCT_AUTOFILL_POOL = [
  {
    res: { id: 'item-keripik-singkong', name: 'Keripik Singkong Balado (Spicy Cassava Chips)', short_desc: 'Crispy cassava chips with balado seasoning', long_desc: 'Keripik singkong renyah dengan bumbu balado khas Minang yang pedas manis. (Crispy cassava chips with authentic Minang sweet and spicy balado seasoning.)', product_highlights: '- Terbuat dari singkong segar pilihan (Made from fresh selected cassava)\n- Bumbu balado asli minang (Authentic Minang balado seasoning)\n- Tanpa pengawet buatan (No artificial preservatives)', specifications: '- Berat Bersih (Net Weight): 150g\n- Kemasan (Packaging): Plastik Ziplock (Ziplock Plastic)\n- Bahan Utama (Main Ingredient): Singkong, Cabai (Cassava, Chili)', stock_quantity: '50', media_url: 'https://images.unsplash.com/photo-1621939514649-280e2ee25f60?w=400', brand: 'Chitato', origin_country: 'ID', weight_value: '150', weight_unit: 'G', color: 'Golden', material: 'Cassava' },
    off: { id: 'offer-keripik-singkong', name: 'Keripik Singkong Balado', price: '25000', currency: 'IDR', cod_available: true, returns_allowed: true, returns_window: 'P7D', returns_method: 'SELLER_PICKUP', returns_fee_type: 'PERCENTAGE', returns_fee_value: '10', cancel_allowed: true, cancel_window: 'PT2H', cancel_event: 'BEFORE_PACKING', replace_allowed: false, max_distance: '20', timing_days: ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN'], timing_start: '09:00', timing_end: '21:00', offer_context: 'https://schema.ion.id/release1/schema/extension/trade/TradeOffer/v1/context.jsonld' }
  },
  {
    res: { id: 'item-bolu-pisang', name: 'Bolu Pisang Cokelat (Chocolate Banana Cake)', short_desc: 'Soft banana cake with chocolate chips', long_desc: 'Bolu pisang panggang yang lembut dipadukan dengan taburan cokelat lezat di atasnya. (Soft baked banana cake topped with delicious chocolate chips.)', product_highlights: '- Dibuat dengan pisang ambon segar (Made with fresh Ambon bananas)\n- Tekstur lembut dan lumer di mulut (Soft texture that melts in the mouth)\n- Taburan cokelat premium (Premium chocolate chips)', specifications: '- Berat Bersih (Net Weight): 400g\n- Kemasan (Packaging): Kardus Makanan (Food Box)\n- Bahan Utama (Main Ingredient): Tepung, Pisang, Cokelat (Flour, Banana, Chocolate)', stock_quantity: '30', media_url: 'https://images.unsplash.com/photo-1578985545062-69928b1d9587?w=400', brand: 'HomeMade', origin_country: 'ID', weight_value: '400', weight_unit: 'G', color: 'Brown', material: 'Flour' },
    off: { id: 'offer-bolu-pisang', name: 'Bolu Pisang Cokelat', price: '45000', currency: 'IDR', cod_available: true, returns_allowed: false, cancel_allowed: true, cancel_window: 'PT1H', cancel_event: 'BEFORE_PACKING', replace_allowed: false, max_distance: '10', timing_days: ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT'], timing_start: '08:00', timing_end: '18:00', offer_context: 'https://schema.ion.id/release1/schema/extension/trade/TradeOffer/v1/context.jsonld' }
  },
  {
    res: { id: 'item-teh-botol', name: 'Teh Botol Sosro (Bottled Jasmine Tea)', short_desc: 'Ready-to-drink jasmine tea', long_desc: 'Minuman teh melati siap saji legendaris Indonesia yang menyegarkan dahaga. (Legendary Indonesian ready-to-drink jasmine tea that is very refreshing.)', product_highlights: '- Terbuat dari daun teh melati asli (Made from real jasmine tea leaves)\n- Proses penyeduhan higienis (Hygienic brewing process)\n- Menyegarkan dahaga (Refreshing thirst quencher)', specifications: '- Isi Bersih (Net Volume): 350ml\n- Kemasan (Packaging): Botol Plastik PET (PET Plastic Bottle)\n- Bahan Utama (Main Ingredient): Air, Ekstrak Teh (Water, Tea Extract)', stock_quantity: '200', media_url: 'https://images.unsplash.com/photo-1556679343-c7306c1976bc?w=400', brand: 'Sosro', origin_country: 'ID', weight_value: '350', weight_unit: 'ML', color: 'Amber', material: 'PET Bottle' },
    off: { id: 'offer-teh-botol', name: 'Teh Botol Sosro', price: '5000', currency: 'IDR', cod_available: true, returns_allowed: false, cancel_allowed: true, cancel_window: 'PT30M', cancel_event: 'BEFORE_PACKING', replace_allowed: false, max_distance: '15', timing_days: ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN'], timing_start: '07:00', timing_end: '22:00', offer_context: 'https://schema.ion.id/release1/schema/extension/trade/TradeOffer/v1/context.jsonld' }
  },
]
let _productFillIdx = 0

// ── Shared offer form component ────────────────────────────────────────────

function OfferForm({ off, oSet, offOpen, setOffOpen, catalogId, lockId, showResourcePicker }) {
  const { t } = useI18n()
  const labelCls = 'block text-[11px] uppercase tracking-widest text-slate-500 font-semibold mb-1'
  const inputCls = 'w-full px-3 py-2 rounded-lg text-sm text-white placeholder-slate-600 border border-white/[0.08] bg-slate-900 focus:outline-none focus:border-blue-500/50 transition-colors'

  const { data: catResData } = useApi(showResourcePicker && !lockId ? `/catalogs/${catalogId}/resources` : null, [catalogId, showResourcePicker, lockId])
  const existingResources = catResData?.items || []

  return (
    <>
      {/* Resource picker for offer-only mode -- not shown while editing an
          existing offer (lockId): its resource association stays fixed. */}
      {showResourcePicker && !lockId && existingResources.length > 0 && (
        <div className="mb-4">
          <label className={labelCls}>{t('Link to Resource(s) *')}</label>
          <div className="space-y-1 max-h-48 overflow-y-auto">
            {existingResources.map(r => (
              <label key={r.id} className="flex items-center gap-2 px-3 py-2 rounded-lg border border-white/[0.06] hover:bg-slate-800 cursor-pointer">
                <input type="checkbox" className="accent-blue-500"
                  checked={off.resource_ids.split(',').map(s => s.trim()).includes(r.id)}
                  onChange={e => {
                    const cur = off.resource_ids.split(',').map(s => s.trim()).filter(Boolean)
                    const next = e.target.checked ? [...cur, r.id] : cur.filter(x => x !== r.id)
                    oSet('resource_ids', next.join(', '))
                  }} />
                <span className="text-xs text-slate-300 flex-1">{r.name}</span>
                <span className="text-[10px] font-mono text-slate-600">{r.id}</span>
              </label>
            ))}
          </div>
        </div>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <div><label className={labelCls}>{t('Offer ID *')}</label>
          <input className={inputCls + (lockId ? ' opacity-60 cursor-not-allowed' : '')} value={off.id} readOnly={lockId}
            title={lockId ? t('The offer ID cannot change') : undefined}
            onChange={e => oSet('id', e.target.value)} placeholder="offer-flask-mh500" required />
        </div>
        <div><label className={labelCls}>{t('Offer Name *')}</label>
          <input className={inputCls} value={off.name} onChange={e => oSet('name', e.target.value)} placeholder={t('Flask 500ml Yellow')} required />
        </div>
        {!showResourcePicker && !lockId && (
          <div className="col-span-full"><label className={labelCls}>{t('Resource IDs')} <span className="text-slate-600 font-normal">({t('comma-separated; leave blank to auto-link')})</span></label>
            <input className={inputCls} value={off.resource_ids} onChange={e => oSet('resource_ids', e.target.value)} placeholder="item-flask-mh500" />
          </div>
        )}
        <div data-tour="offer-price"><label className={labelCls}>{t('Price')}</label>
          <input className={inputCls} type="number" min="0" step="0.01" value={off.price} onChange={e => oSet('price', e.target.value)} placeholder="499" />
        </div>
        <div><label className={labelCls}>{t('Currency')}</label>
          <select className={inputCls} value={off.currency} onChange={e => oSet('currency', e.target.value)}>
            {['IDR', 'INR', 'USD', 'EUR', 'SGD', 'MYR'].map(c => <option key={c} className="bg-slate-900">{c}</option>)}
          </select>
        </div>
        <OfferTaxFields offer={off} onChange={oSet} labelCls={labelCls} inputCls={inputCls} />
        <div><label className={labelCls}>{t('Validity Start')}</label>
          <input type="datetime-local" className={inputCls} value={off.validity_start} onChange={e => oSet('validity_start', e.target.value)} />
        </div>
        <div><label className={labelCls}>{t('Validity End')}</label>
          <input type="datetime-local" className={inputCls} value={off.validity_end} onChange={e => oSet('validity_end', e.target.value)} />
        </div>
        <div><label className={labelCls}>{t('Short Description')}</label>
          <input className={inputCls} value={off.short_desc} onChange={e => oSet('short_desc', e.target.value)} placeholder={t('Best seller flask')} />
        </div>
      </div>

      {/* Policies collapsible */}
      <button type="button" data-tour="offer-policies" onClick={() => setOffOpen(s => ({ ...s, pol: !s.pol }))}
        className="mt-4 w-full flex items-center justify-between text-xs font-semibold text-slate-400 hover:text-slate-200 transition-colors pt-3 border-t border-white/[0.06]">
        <span>📋 {t('Policies & Serviceability')}</span>
        <ChevronRightIcon size={13} className={`transition-transform ${offOpen.pol ? 'rotate-90' : ''}`} />
      </button>
      {offOpen.pol && (
        <div className="mt-3 space-y-3">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            {/* Returns */}
            <div className="p-3 rounded-lg bg-slate-800/50 border border-white/[0.06] space-y-2">
              <div className="flex items-center justify-between">
                <p className="text-xs font-semibold text-slate-300">{t('Returns')}</p>
                <label className="flex items-center gap-2 cursor-pointer">
                  <span className="text-xs text-slate-500">{t('Allowed')}</span>
                  <input type="checkbox" checked={off.returns_allowed} onChange={e => oSet('returns_allowed', e.target.checked)} className="accent-blue-500" />
                </label>
              </div>
              {off.returns_allowed && (
                <div className="grid grid-cols-2 gap-2">
                  <div><label className={labelCls}>{t('Window')}</label>
                    <input className={inputCls} value={off.returns_window} onChange={e => oSet('returns_window', e.target.value)} placeholder="P7D" />
                  </div>
                  <div><label className={labelCls}>{t('Method')}</label>
                    <select className={inputCls} value={off.returns_method} onChange={e => oSet('returns_method', e.target.value)}>
                      {RETURN_METHODS.map(m => <option key={m} value={m} className="bg-slate-900">{t(m)}</option>)}
                    </select>
                  </div>
                  <div><label className={labelCls}>{t('Restocking Fee')}</label>
                    <select className={inputCls} value={off.returns_fee_type || ''} onChange={e => oSet('returns_fee_type', e.target.value)}>
                      <option value="" className="bg-slate-900">{t('None')}</option>
                      {FEE_TYPES.map(f => <option key={f} value={f} className="bg-slate-900">{t(f)}</option>)}
                    </select>
                  </div>
                  <div><label className={labelCls}>{off.returns_fee_type === 'PERCENTAGE' ? t('Fee (%)') : t('Fee Amount')}</label>
                    <input type="number" min="0" step="any" className={inputCls} disabled={!off.returns_fee_type}
                      value={off.returns_fee_value ?? ''} onChange={e => oSet('returns_fee_value', e.target.value)}
                      placeholder={off.returns_fee_type === 'PERCENTAGE' ? '10' : '5000'} />
                  </div>
                </div>
              )}
            </div>
            {/* Cancellation */}
            <div className="p-3 rounded-lg bg-slate-800/50 border border-white/[0.06] space-y-2">
              <div className="flex items-center justify-between">
                <p className="text-xs font-semibold text-slate-300">{t('Cancellation')}</p>
                <label className="flex items-center gap-2 cursor-pointer">
                  <span className="text-xs text-slate-500">{t('Allowed')}</span>
                  <input type="checkbox" checked={off.cancel_allowed} onChange={e => oSet('cancel_allowed', e.target.checked)} className="accent-blue-500" />
                </label>
              </div>
              {off.cancel_allowed && (
                <div className="grid grid-cols-2 gap-2">
                  <div><label className={labelCls}>{t('Window')}</label>
                    <input className={inputCls} value={off.cancel_window} onChange={e => oSet('cancel_window', e.target.value)} placeholder="PT2H" />
                  </div>
                  <div><label className={labelCls}>{t('Cutoff')}</label>
                    <select className={inputCls} value={off.cancel_event} onChange={e => oSet('cancel_event', e.target.value)}>
                      {['BEFORE_PACKING', 'BEFORE_SHIPPING', 'BEFORE_DELIVERY'].map(ev => <option key={ev} value={ev} className="bg-slate-900">{t(ev)}</option>)}
                    </select>
                  </div>
                </div>
              )}
            </div>
          </div>
          {/* COD + distance */}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div className="p-3 rounded-lg bg-slate-800/50 border border-white/[0.06]">
              <label className="flex items-center gap-2 cursor-pointer">
                <input type="checkbox" checked={off.cod_available} onChange={e => oSet('cod_available', e.target.checked)} className="accent-blue-500" />
                <span className="text-xs font-semibold text-slate-300">{t('Cash on Delivery (COD)')}</span>
              </label>
            </div>
            <div className="p-3 rounded-lg bg-slate-800/50 border border-white/[0.06] space-y-2">
              <p className="text-xs font-semibold text-slate-300">{t('Delivery Radius')}</p>
              <div className="flex gap-2">
                <input className={inputCls} type="number" value={off.max_distance} onChange={e => oSet('max_distance', e.target.value)} placeholder="15" />
                <select className={`${inputCls.replace('w-full', 'w-20')} shrink-0`} value={off.distance_unit} onChange={e => oSet('distance_unit', e.target.value)}>
                  {['KM', 'MI'].map(u => <option key={u} className="bg-slate-900">{u}</option>)}
                </select>
              </div>
            </div>
          </div>
          {/* Timing */}
          <div className="p-3 rounded-lg bg-slate-800/50 border border-white/[0.06] space-y-3">
            <p className="text-xs font-semibold text-slate-300">{t('Operating Hours')}</p>
            <div className="flex flex-wrap gap-1.5">
              {DAYS_OF_WEEK.map(d => (
                <button key={d} type="button"
                  onClick={() => oSet('timing_days', off.timing_days.includes(d) ? off.timing_days.filter(x => x !== d) : [...off.timing_days, d])}
                  className={`px-2.5 py-1.5 rounded-md text-[11px] font-bold transition-colors ${off.timing_days.includes(d) ? 'text-white' : 'border border-white/[0.10] text-slate-500 hover:text-white'}`}
                  style={off.timing_days.includes(d) ? { background: BRAND } : {}}>
                  {t(d)}
                </button>
              ))}
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div><label className={labelCls}>{t('Opens')}</label>
                <input type="time" className={inputCls} value={off.timing_start} onChange={e => oSet('timing_start', e.target.value)} />
              </div>
              <div><label className={labelCls}>{t('Closes')}</label>
                <input type="time" className={inputCls} value={off.timing_end} onChange={e => oSet('timing_end', e.target.value)} />
              </div>
            </div>
          </div>
        </div>
      )}
    </>
  )
}

// ── Add Product Page (resource only) ──────────────────────────────────────

function AddProductPage({ onNav, catalogId, editResourceId }) {
  const { t } = useI18n()
  const labelCls = 'block text-[11px] uppercase tracking-widest text-slate-500 font-semibold mb-1'
  const inputCls = 'w-full px-3 py-2 rounded-lg text-sm text-white placeholder-slate-600 border border-white/[0.08] bg-slate-900 focus:outline-none focus:border-blue-500/50 transition-colors'

  const [res, setRes] = useState(EMPTY_RESOURCE())
  const [resOpen, setResOpen] = useState({})
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [success, setSuccess] = useState(false)

  const rSet = (k, v) => setRes(r => ({ ...r, [k]: v }))
  const editing = !!editResourceId
  const [loadingItem, setLoadingItem] = useState(editing)

  // Edit mode: load the stored resource into the form.
  useEffect(() => {
    if (!editing) return
    apiFetch(`/catalogs/${catalogId}/resources/${encodeURIComponent(editResourceId)}`)
      .then(d => setRes(resourceToForm(d.resource)))
      .catch(e => setError(e.message))
      .finally(() => setLoadingItem(false))
  }, [editing, catalogId, editResourceId])

  const handleAutoFill = () => {
    const seed = PRODUCT_AUTOFILL_POOL[_productFillIdx % PRODUCT_AUTOFILL_POOL.length]
    _productFillIdx++
    const uid = id => `${id}-${Date.now().toString(36)}`
    setRes({ ...EMPTY_RESOURCE(), ...seed.res, id: uid(seed.res.id) })
    setResOpen({}); setError(null)
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (!res.id || !res.name) { setError(t('Resource ID and Name are required.')); return }
    setLoading(true); setError(null)

    const resourceAttrs = buildResourceAttrs(res)
    const mediaFile = res.media_url ? [{ uri: res.media_url, mimeType: 'image/jpeg' }] : []

    const resource = {
      id: res.id,
      descriptor: { name: res.name, shortDesc: res.short_desc, longDesc: res.long_desc, mediaFile },
      stockQuantity: res.stock_quantity ? Number(res.stock_quantity) : 0,
      ...(resourceAttrs ? { resourceAttributes: resourceAttrs } : {}),
    }
    try {
      if (editing) {
        await apiFetch(`/catalogs/${catalogId}/resources/${encodeURIComponent(editResourceId)}`, { method: 'PUT', body: JSON.stringify(resource) })
        setSuccess(true)
        setTimeout(() => onNav('catalog_detail', { catalogId }), 1000)
        return
      }
      // Resource only -- the real offer is created on the next (add_offer) page.
      await apiFetch(`/catalogs/${catalogId}/products`, { method: 'POST', body: JSON.stringify({ resource }) })
      setSuccess(true)
      setTimeout(() => onNav('add_offer', { catalogId, preselectedResourceId: res.id }), 1000)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  if (success) {
    return (
      <div className="flex flex-col items-center justify-center py-24 gap-4">
        <CheckCircle2 size={48} className="text-emerald-400" />
        <p className="text-white text-lg font-semibold">{editing ? t('Product updated!') : t('Resource added!')}</p>
        <p className="text-slate-500 text-sm">{editing ? t('Back to the catalog — publish to send the change') : t('Opening offer setup…')}</p>
      </div>
    )
  }

  return (
    <div className="max-w-2xl space-y-6">
      <div className="flex items-center gap-3">
        <button onClick={() => onNav('catalog_detail', { catalogId })} className="p-2 rounded-lg border border-white/[0.08] text-slate-400 hover:text-white transition-colors">
          <ArrowLeft size={16} />
        </button>
        <div className="flex-1">
          <h1 className="text-2xl font-bold text-white tracking-tight">{editing ? t('Edit Product') : t('Add Product')}</h1>
          <p className="text-xs text-slate-500 font-mono mt-0.5">{t('catalog: {id}', { id: catalogId })}</p>
        </div>
        {!editing && (
          <button type="button" data-tour="product-autofill" onClick={handleAutoFill}
            className="flex items-center gap-2 px-3 py-2 rounded-xl text-xs font-semibold border border-purple-500/30 text-purple-400 hover:bg-purple-500/10 transition-colors">
            <Wand2 size={13} /> {t('Auto-fill')}
          </button>
        )}
      </div>

      {error && <ErrorBox message={error} />}
      {loadingItem && <LoadingSpinner label={t('Loading product…')} />}

      <form onSubmit={handleSubmit} className={`space-y-4 ${loadingItem ? 'hidden' : ''}`}>
        <Card className="p-4 sm:p-6" data-tour="product-form">
          <p className="text-xs uppercase tracking-widest text-slate-500 font-bold pb-3 border-b border-white/[0.06] mb-4">📦 {t('Resource Details')}</p>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div><label className={labelCls}>{t('Resource ID *')}</label>
              <input className={inputCls + (editing ? ' opacity-60 cursor-not-allowed' : '')} value={res.id} readOnly={editing}
                title={editing ? t('The resource ID cannot change') : undefined}
                onChange={e => rSet('id', e.target.value)} placeholder="item-flask-mh500" required />
            </div>
            <div><label className={labelCls}>{t('Name *')}</label>
              <input className={inputCls} value={res.name} onChange={e => rSet('name', e.target.value)} placeholder={t('Stainless Steel Hiking Flask')} required />
            </div>
            <div><label className={labelCls}>{t('Short Description')}</label>
              <input className={inputCls} value={res.short_desc} onChange={e => rSet('short_desc', e.target.value)} placeholder={t('500ml insulated flask')} />
            </div>
            <div><label className={labelCls}>{t('Long Description')}</label>
              <textarea className={inputCls} rows="2" value={res.long_desc} onChange={e => rSet('long_desc', e.target.value)} placeholder={t('Full description…')} />
            </div>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-3">
            <div><label className={labelCls}>{t('Product Highlights')}</label>
              <textarea className={inputCls} rows="3" value={res.product_highlights} onChange={e => rSet('product_highlights', e.target.value)} placeholder={t('List highlights separated by newlines…')} />
            </div>
            <div><label className={labelCls}>{t('Specifications')}</label>
              <textarea className={inputCls} rows="3" value={res.specifications} onChange={e => rSet('specifications', e.target.value)} placeholder={t('List specifications separated by newlines…')} />
            </div>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-3">
            <div><label className={labelCls}>{t('Stock Quantity')} <span className="text-slate-600 font-normal">({t('blank = unlimited')})</span></label>
              <input className={inputCls} type="number" min="0" value={res.stock_quantity} onChange={e => rSet('stock_quantity', e.target.value)} placeholder="50" />
            </div>
            <div><label className={labelCls}>{t('Image URL')}</label>
              <input className={inputCls} value={res.media_url} onChange={e => rSet('media_url', e.target.value)} placeholder="https://example.com/product.jpg" />
            </div>
          </div>

          {/* Identity & Physical */}
          <button type="button" onClick={() => setResOpen(s => ({ ...s, phys: !s.phys }))}
            className="mt-4 w-full flex items-center justify-between text-xs font-semibold text-slate-400 hover:text-slate-200 transition-colors pt-3 border-t border-white/[0.06]">
            <span>🏷️ {t('Identity & Physical Attributes')}</span>
            <ChevronRightIcon size={13} className={`transition-transform ${resOpen.phys ? 'rotate-90' : ''}`} />
          </button>
          {resOpen.phys && (
            <div className="mt-3 space-y-4">
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <div><label className={labelCls}>{t('Brand')}</label>
                  <input className={inputCls} value={res.brand} onChange={e => rSet('brand', e.target.value)} placeholder="InstaCuppa" />
                </div>
                <div><label className={labelCls}>{t('Origin Country')}</label>
                  <input className={inputCls} value={res.origin_country} onChange={e => rSet('origin_country', e.target.value)} placeholder="IN" />
                </div>
              </div>
              <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
                <div className="col-span-1 sm:col-span-2"><label className={labelCls}>{t('Weight')}</label>
                  <input className={inputCls} type="number" value={res.weight_value} onChange={e => rSet('weight_value', e.target.value)} placeholder="350" />
                </div>
                <div className="col-span-1 sm:col-span-2"><label className={labelCls}>{t('Unit')}</label>
                  <select className={inputCls} value={res.weight_unit} onChange={e => rSet('weight_unit', e.target.value)}>
                    {['G', 'KG', 'MG', 'OZ', 'LB'].map(u => <option key={u} className="bg-slate-900">{u}</option>)}
                  </select>
                </div>
                <div className="col-span-1 sm:col-span-2"><label className={labelCls}>{t('Volume')}</label>
                  <input className={inputCls} type="number" value={res.volume_value} onChange={e => rSet('volume_value', e.target.value)} placeholder="500" />
                </div>
                <div className="col-span-1 sm:col-span-2"><label className={labelCls}>{t('Unit')}</label>
                  <select className={inputCls} value={res.volume_unit} onChange={e => rSet('volume_unit', e.target.value)}>
                    {['ML', 'L', 'FL_OZ'].map(u => <option key={u} className="bg-slate-900">{u}</option>)}
                  </select>
                </div>
              </div>
              <div className="grid grid-cols-3 gap-3">
                <div><label className={labelCls}>{t('Color')}</label><input className={inputCls} value={res.color} onChange={e => rSet('color', e.target.value)} placeholder={t('Yellow')} /></div>
                <div><label className={labelCls}>{t('Material')}</label><input className={inputCls} value={res.material} onChange={e => rSet('material', e.target.value)} placeholder={t('Steel')} /></div>
                <div><label className={labelCls}>{t('Surface Finish')}</label><input className={inputCls} value={res.finish} onChange={e => rSet('finish', e.target.value)} placeholder={t('Matte')} /></div>
              </div>
            </div>
          )}

          {/* Packaged Goods */}
          <button type="button" onClick={() => setResOpen(s => ({ ...s, pkg: !s.pkg }))}
            className="mt-4 w-full flex items-center justify-between text-xs font-semibold text-slate-400 hover:text-slate-200 transition-colors pt-3 border-t border-white/[0.06]">
            <span>📦 {t('Packaged Goods Declaration')}</span>
            <ChevronRightIcon size={13} className={`transition-transform ${resOpen.pkg ? 'rotate-90' : ''}`} />
          </button>
          {resOpen.pkg && (
            <div className="mt-3 grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div><label className={labelCls}>{t('Manufacturer Type')}</label>
                <select className={inputCls} value={res.mfr_type} onChange={e => rSet('mfr_type', e.target.value)}>
                  {['MANUFACTURER', 'PACKER', 'IMPORTER'].map(m => <option key={m} value={m} className="bg-slate-900">{t(m)}</option>)}
                </select>
              </div>
              <div><label className={labelCls}>{t('Manufacturer Name')}</label>
                <input className={inputCls} value={res.mfr_name} onChange={e => rSet('mfr_name', e.target.value)} placeholder={t('Company Ltd')} />
              </div>
              <div className="col-span-full"><label className={labelCls}>{t('Manufacturer Address')}</label>
                <input className={inputCls} value={res.mfr_address} onChange={e => rSet('mfr_address', e.target.value)} placeholder={t('City, State, Country')} />
              </div>
              <div><label className={labelCls}>{t('Common / Generic Name')}</label>
                <input className={inputCls} value={res.common_name} onChange={e => rSet('common_name', e.target.value)} placeholder={t('Vacuum Flask')} />
              </div>
              <div><label className={labelCls}>{t('Net Quantity')}</label>
                <div className="flex gap-2">
                  <input className={inputCls} type="number" value={res.net_qty_value} onChange={e => rSet('net_qty_value', e.target.value)} placeholder="500" />
                  <select className={`${inputCls.replace('w-full', 'w-24')} shrink-0`} value={res.net_qty_unit} onChange={e => rSet('net_qty_unit', e.target.value)}>
                    {['ML', 'L', 'G', 'KG', 'PCS'].map(u => <option key={u} className="bg-slate-900">{u}</option>)}
                  </select>
                </div>
              </div>
            </div>
          )}
        </Card>

        <div className="flex flex-col sm:flex-row gap-3">
          <button type="button" onClick={() => onNav('catalog_detail', { catalogId })}
            className="px-5 py-3 rounded-xl text-sm font-semibold border border-white/[0.10] text-slate-400 hover:text-white transition-colors text-center">
            {t('Cancel')}
          </button>
          <button type="submit" data-tour="product-submit" disabled={loading}
            className="flex-1 px-5 py-3 rounded-xl text-sm font-semibold text-white shadow-lg transition-all hover:scale-[1.01] active:scale-[0.99] disabled:opacity-50 flex items-center justify-center gap-2"
            style={{ background: BRAND }}>
            {loading ? <><RefreshCw size={14} className="animate-spin" /> {t('Saving…')}</> : editing ? <><Check size={14} /> {t('Save Changes')}</> : <><Plus size={14} /> {t('Save Resource → Setup Offer')}</>}
          </button>
        </div>
      </form>
    </div>
  )
}

// ── Add Offer Page (standalone) ───────────────────────────────────────────

function AddOfferPage({ onNav, catalogId, preselectedResourceId, editOfferId }) {
  const { t } = useI18n()
  const [off, setOff] = useState(() => ({
    ...EMPTY_OFFER(),
    resource_ids: preselectedResourceId || '',
  }))
  const [offOpen, setOffOpen] = useState({})
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)
  const [success, setSuccess] = useState(false)

  const oSet = (k, v) => setOff(o => ({ ...o, [k]: v }))
  const editing = !!editOfferId
  const [loadingItem, setLoadingItem] = useState(editing)

  // Edit mode: load the stored offer (with its price) into the form.
  useEffect(() => {
    if (!editing) return
    apiFetch(`/catalogs/${catalogId}/offers/${encodeURIComponent(editOfferId)}`)
      .then(d => setOff(offerToForm(d.offer)))
      .catch(e => setError(e.message))
      .finally(() => setLoadingItem(false))
  }, [editing, catalogId, editOfferId])

  const handleAutoFill = () => {
    const seed = PRODUCT_AUTOFILL_POOL[_productFillIdx % PRODUCT_AUTOFILL_POOL.length]
    _productFillIdx++
    const uid = id => `${id}-${Date.now().toString(36)}`
    setOff({
      ...EMPTY_OFFER(), ...seed.off, id: uid(seed.off.id),
      resource_ids: preselectedResourceId || ''
    })
    setOffOpen({}); setError(null)
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (!off.id || !off.name) { setError(t('Offer ID and Name are required.')); return }
    setLoading(true); setError(null)

    const offerAttrs = buildOfferAttrs(off)
    const resourceIds = off.resource_ids.split(',').map(s => s.trim()).filter(Boolean)

    const payload = {
      id: off.id,
      descriptor: { name: off.name, shortDesc: off.short_desc },
      resourceIds,
      ...(off.validity_start && off.validity_end ? {
        validity: { startDate: new Date(off.validity_start).toISOString(), endDate: new Date(off.validity_end).toISOString() }
      } : {}),
      ...(offerAttrs ? { offerAttributes: offerAttrs } : {}),
      ...(off.price ? {
        considerations: [{
          id: `${off.id}-price`,
          status: { code: 'ACTIVE', name: 'ACTIVE' },
          considerationAttributes: {
            '@context': 'https://schema.ion.id/release1/schema/extension/trade/TradeConsideration/v1/context.jsonld',
            '@type': 'ion:IONTradeConsideration',
            currency: off.currency,
            ...buildPriceBreakup(off),
            paymentMethods: off.cod_available ? ['PREPAID', 'COD', 'DIGITAL_WALLET'] : ['PREPAID', 'DIGITAL_WALLET'],
          },
        }],
      } : {}),
    }

    try {
      if (editing) {
        await apiFetch(`/catalogs/${catalogId}/offers/${encodeURIComponent(editOfferId)}`, { method: 'PUT', body: JSON.stringify(payload) })
      } else {
        await apiFetch(`/catalogs/${catalogId}/offers`, { method: 'POST', body: JSON.stringify(payload) })
      }
      setSuccess(true)
      setTimeout(() => onNav('catalog_detail', { catalogId }), 1200)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  if (success) {
    return (
      <div className="flex flex-col items-center justify-center py-24 gap-4">
        <CheckCircle2 size={48} className="text-emerald-400" />
        <p className="text-white text-lg font-semibold">{editing ? t('Offer updated!') : t('Offer added!')}</p>
        <p className="text-slate-500 text-sm">{t('Redirecting to catalog…')}</p>
      </div>
    )
  }

  return (
    <div className="max-w-2xl space-y-6">
      <div className="flex items-center gap-3">
        <button onClick={() => onNav('catalog_detail', { catalogId })} className="p-2 rounded-lg border border-white/[0.08] text-slate-400 hover:text-white transition-colors">
          <ArrowLeft size={16} />
        </button>
        <div className="flex-1">
          <h1 className="text-2xl font-bold text-white tracking-tight">{editing ? t('Edit Offer') : t('Add Offer')}</h1>
          <p className="text-xs text-slate-500 font-mono mt-0.5">{t('catalog: {id}', { id: catalogId })}</p>
        </div>
        {!editing && (
          <button type="button" data-tour="offer-autofill" onClick={handleAutoFill}
            className="flex items-center gap-2 px-3 py-2 rounded-xl text-xs font-semibold border border-purple-500/30 text-purple-400 hover:bg-purple-500/10 transition-colors">
            <Wand2 size={13} /> {t('Auto-fill')}
          </button>
        )}
      </div>

      {error && <ErrorBox message={error} />}
      {loadingItem && <LoadingSpinner label={t('Loading offer…')} />}

      <form onSubmit={handleSubmit} className={`space-y-4 ${loadingItem ? 'hidden' : ''}`}>
        <Card className="p-4 sm:p-6">
          <p className="text-xs uppercase tracking-widest text-slate-500 font-bold pb-3 border-b border-white/[0.06] mb-4">🏷️ {t('Offer Details')}</p>
          <OfferForm off={off} oSet={oSet} offOpen={offOpen} setOffOpen={setOffOpen}
            catalogId={catalogId} showResourcePicker={!preselectedResourceId} lockId={editing} />
        </Card>

        <div className="flex flex-col sm:flex-row gap-3">
          <button type="button" onClick={() => onNav('catalog_detail', { catalogId })}
            className="px-5 py-3 rounded-xl text-sm font-semibold border border-white/[0.10] text-slate-400 hover:text-white transition-colors text-center">
            {t('Cancel')}
          </button>
          <button type="submit" data-tour="offer-submit" disabled={loading}
            className="flex-1 px-5 py-3 rounded-xl text-sm font-semibold text-white shadow-lg transition-all hover:scale-[1.01] active:scale-[0.99] disabled:opacity-50 flex items-center justify-center gap-2"
            style={{ background: BRAND }}>
            {loading ? <><RefreshCw size={14} className="animate-spin" /> {editing ? t('Saving…') : t('Adding…')}</> : editing ? <><Check size={14} /> {t('Save Changes')}</> : <><Tag size={14} /> {t('Add Offer to Catalog')}</>}
          </button>
        </div>
      </form>
    </div>
  )
}

// ─── App shell ────────────────────────────────────────────────────────────────

const PAGES = {
  overview: OverviewPage,
  orders: OrdersPage,
  inventory: InventoryPage,
  catalogs: CatalogsPage,
  create_catalog: CreateCatalogPage,
  catalog_detail: CatalogDetailPage,
  add_product: AddProductPage,
  add_offer: AddOfferPage,
  edit_product: AddProductPage,
  edit_offer: AddOfferPage,
  publish: PublishPage,
  messages: MessagesPage,
  reconciliation: ReconciliationPage,
  support: SupportPage,
  ratings: RatingsPage,
}

export default function App() {
  const { t, chosen: langChosen } = useI18n()
  const [page, setPage] = useState('overview')
  const [navExtra, setNavExtra] = useState({})
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [authReady, setAuthReady] = useState(false)
  const Page = PAGES[page] || OverviewPage

  const onNav = (target, extra = {}) => { setPage(target); setNavExtra(extra); setSidebarOpen(false) }

  // Guided tour: opens by itself on a seller's first visit, and from the
  // sidebar's "Take the tour" any time after.
  const [tourOpen, setTourOpen] = useState(false)
  const tourStepList = useMemo(() => tourSteps(t), [t])
  // First visit: the tour starts once a language has been picked.
  useEffect(() => {
    if (!authReady || !langChosen) return
    let done = false
    try { done = localStorage.getItem(TOUR_DONE_KEY) === '1' } catch { }
    if (!done) setTourOpen(true)
  }, [authReady, langChosen])
  const closeTour = useCallback(() => {
    setTourOpen(false)
    setSidebarOpen(false)
    try { localStorage.setItem(TOUR_DONE_KEY, '1') } catch { }
  }, [])
  // Each step opens its section; on small screens the sidebar (drawer) is
  // opened too so the highlighted menu item is visible.
  const onTourStep = useCallback(step => {
    if (step.page) { setPage(step.page); setNavExtra(step.extra || {}) }
    setSidebarOpen(window.innerWidth < 1024 && !!step.target && (step.target.startsWith('nav-') || step.target === 'tour-button'))
  }, [])

  // Gate on a token before rendering anything real. There's no login UI
  // here at all -- landing-page is the single entry point (see
  // src/auth.js); a visit with no token, ever, redirects there.
  useEffect(() => {
    if (window.location.pathname === `${basePath}logout`) {
      clearToken() // loaded in a hidden iframe from landing-page's logout button
      return
    }
    captureTokenFromUrl()
    if (!getToken()) {
      redirectToLogin()
      return
    }
    setAuthReady(true)
  }, [])

  if (!authReady) {
    return (
      <div className="flex min-h-screen items-center justify-center" style={{ background: '#080d1e' }}>
        <p className="text-slate-400 text-sm">{t('Redirecting to login…')}</p>
      </div>
    )
  }

  return (
    <div className="flex min-h-screen" style={{ background: '#080d1e' }}>
      <Sidebar active={page} onNav={onNav} open={sidebarOpen} onClose={() => setSidebarOpen(false)} onTour={() => setTourOpen(true)} />
      <Tour steps={tourStepList} open={tourOpen} onClose={closeTour} onStep={onTourStep} state={{ page }} />

      <main className="flex-1 lg:ml-64 min-h-screen overflow-y-auto">
        <div className="bg-orange-500 text-white text-xs sm:text-sm text-center py-1.5 px-4">
          {t('[FOR DEMO PURPOSE ONLY] - This is a sample application for reference and testing')}
        </div>

        {/* Mobile top bar */}
        <div className="lg:hidden sticky top-0 z-30 flex items-center gap-3 px-4 py-3 border-b border-white/[0.06]"
          style={{ background: '#0a0e1a' }}>
          <button onClick={() => setSidebarOpen(true)} className="p-2 rounded-lg text-slate-400 hover:text-white transition-colors">
            <svg width="20" height="20" fill="none" stroke="currentColor" strokeWidth="2" viewBox="0 0 24 24">
              <path strokeLinecap="round" d="M4 6h16M4 12h16M4 18h16" />
            </svg>
          </button>
          <span className="text-sm font-semibold text-white">{t('BPP Admin')}</span>
          <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
          <LanguageToggle className="ml-auto" />
        </div>

        <div className="p-4 sm:p-6 lg:p-9">
          <AnimatePresence mode="wait">
            <motion.div key={page}
              initial={{ opacity: 0, y: 12 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -8 }}
              transition={{ duration: 0.18 }}>
              <Page onNav={onNav} {...navExtra} />
            </motion.div>
          </AnimatePresence>
        </div>
      </main>
    </div>
  )
}
