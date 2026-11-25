import { useCallback, useEffect, useRef, useState } from 'react';
import { QrCode, Plus, RefreshCw, ExternalLink, Printer, Download, Copy, ArrowLeft, Pencil, Trash2, Smartphone, RotateCcw, CheckCircle2, Search, MapPin, Wand2, ChevronDown, Package, X } from 'lucide-react';
import { basePath } from '../auth.js';
import { adminApi, AdminError, qrApi } from './api.js';
import {
  FORMAT_TYPES, LOCATION_PRESETS, PACKET_PRESETS, STATUSES, formatDistance,
  formatLabel, rupiah, serverFieldErrors, shortTime, validatePacketForm,
} from './domain.js';
import { Alert, Button, Card, ErrorNote, Field, Spinner, StatusBadge, Tag } from './ui.jsx';
import { cx, inputClass } from './styles.js';

// Packet console: create, validate, print and watch QR packets. Served at
// /qr-admin inside the shop (the "QR Packets" header button). Packets live
// in ion-qr-code's qr-service; bap-application forwards these calls to it
// (internal/qrsvc HandleAdmin) and lists the orders each packet produced.

const ROOT = `${basePath}qr-admin`;

/* ─────────────────────────── tiny router ─────────────────────────────────── */

function currentRoute() {
  const rest = window.location.pathname.slice(ROOT.length).replace(/^\/+|\/+$/g, '');
  return { parts: rest ? rest.split('/') : [], query: new URLSearchParams(window.location.search) };
}

function useRoute() {
  const [route, setRoute] = useState(currentRoute);
  useEffect(() => {
    const on = () => setRoute(currentRoute());
    window.addEventListener('popstate', on);
    return () => window.removeEventListener('popstate', on);
  }, []);
  const navigate = useCallback((to, state) => {
    window.history.pushState(state || {}, '', `${ROOT}${to}`);
    setRoute(currentRoute());
    window.scrollTo(0, 0);
  }, []);
  return [route, navigate];
}

function Link({ to, navigate, className, children }) {
  return (
    <a href={`${ROOT}${to}`} className={className} onClick={e => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); navigate(to); } }}>
      {children}
    </a>
  );
}

// useLoad runs load(signal) when deps change; {data, error, loading, reload, setData}.
function useLoad(load, deps) {
  const [state, setState] = useState({ data: null, error: null, loading: true });
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const ctrl = new AbortController();
    load(ctrl.signal)
      .then(data => !ctrl.signal.aborted && setState({ data, error: null, loading: false }))
      .catch(error => { if (error.name !== 'AbortError') setState(s => ({ ...s, error, loading: false })); });
    return () => ctrl.abort();
  }, [...deps, tick]); // eslint-disable-line react-hooks/exhaustive-deps
  const reload = useCallback(() => { setState(s => ({ ...s, loading: true, error: null })); setTick(n => n + 1); }, []);
  return { ...state, reload, setData: data => setState(s => ({ ...s, data })) };
}

/* ─────────────────────────── panel ───────────────────────────────────────── */

// QrAdminPanel is the console body. App.jsx renders it under the shop's own
// header (same login, cart and orders) when the path is /qr-admin/*.
export default function QrAdminPanel({ onExit }) {
  const [route, navigate] = useRoute();
  const [a, b] = route.parts;
  const simulating = a === 'simulate' || b === 'simulate';
  let page;
  if (!a) page = <PacketsList navigate={navigate} query={route.query} onExit={onExit} />;
  else if (a === 'simulate') page = <SimulatorPicker navigate={navigate} onExit={onExit} />;
  else if (b === 'simulate') page = <Simulator id={a} navigate={navigate} />;
  else if (a === 'new') page = <PacketForm navigate={navigate} />;
  else if (b === 'edit') page = <PacketForm id={a} navigate={navigate} />;
  else if (b === 'poster') page = <Poster id={a} navigate={navigate} />;
  else page = <PacketDetail id={a} navigate={navigate} />;
  return (
    <div className="qr-scope space-y-6">
      <nav className="flex gap-1 border-b border-white/[0.06] print:hidden" aria-label="QR console">
        {[['', 'Packets', !simulating], ['/simulate', 'Scan Simulator', simulating]].map(([to, label, active]) => (
          <Link key={label} to={to} navigate={navigate}
            className={cx('-mb-px flex items-center gap-1.5 border-b-2 px-4 py-2.5 text-sm font-semibold transition-colors',
              active ? 'border-brand text-white' : 'border-transparent text-slate-400 hover:text-white')}>
            {label === 'Packets' ? <QrCode className="w-4 h-4" /> : <Smartphone className="w-4 h-4" />} {label}
          </Link>
        ))}
      </nav>
      {page}
    </div>
  );
}

// SimulatorPicker is the Scan Simulator tab: search the ACTIVE packets (or
// paste any packet id) and open one in the phone simulator.
const PACKET_ID_RE = /^[a-z0-9]{12,16}$/;

function SimulatorPicker({ navigate, onExit }) {
  const { data, error, loading, reload } = useLoad(signal => adminApi.list({ status: 'ACTIVE', limit: 100 }, signal), []);
  const [query, setQuery] = useState('');
  const open = id => navigate(`/${id}/simulate`, { from: 'picker' });

  const packets = data?.packets || [];
  const q = query.trim().toLowerCase();
  const shown = q
    ? packets.filter(p => `${p.title} ${p.id} ${p.location_label}`.toLowerCase().includes(q))
    : packets;
  // A pasted id that isn't in the active list can still be opened (any status).
  const pastedId = PACKET_ID_RE.test(q) && !packets.some(p => p.id === q) ? q : null;

  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          {onExit && (
            <button type="button" onClick={onExit} className="flex items-center gap-1 text-xs text-slate-400 hover:text-white">
              <ArrowLeft className="w-3 h-3" /> Back to shop
            </button>
          )}
          <h1 className="mt-1 text-2xl text-white">Scan Simulator</h1>
          <p className="text-sm text-slate-400">Pick a packet and go through the buyer flow on a simulated phone, with real or demo merchants.</p>
        </div>
        <Button variant="secondary" onClick={reload} busy={loading}><RefreshCw className="w-4 h-4" /> Refresh</Button>
      </header>

      <form className="flex flex-wrap items-center gap-3" onSubmit={e => { e.preventDefault(); if (pastedId) open(pastedId); else if (shown.length === 1) open(shown[0].id); }}>
        <div className="relative min-w-[260px] flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 w-4 h-4 -translate-y-1/2 text-slate-500" />
          <input className={cx(inputClass(false), 'pl-9')} value={query} onChange={e => setQuery(e.target.value)}
            placeholder="Search by title, address or id, or paste any packet id" aria-label="Search packets" />
        </div>
        {data && <span className="text-xs text-slate-500">{q ? `${shown.length} of ${packets.length}` : packets.length} active packet{packets.length === 1 ? '' : 's'}</span>}
      </form>

      <ErrorNote error={error} title="Could not load packets" />

      {pastedId && (
        <button type="button" onClick={() => open(pastedId)}
          className="glass flex w-full items-center gap-3 rounded-2xl p-4 text-left transition-colors hover:border-brand/40">
          <Smartphone className="w-5 h-5 shrink-0 text-brand" />
          <span className="min-w-0 flex-1 text-sm text-slate-200">Open packet <span className="font-mono text-white">{pastedId}</span> in the simulator <span className="text-slate-500">(any status)</span></span>
          <span className="text-xs font-semibold text-brand">Open →</span>
        </button>
      )}

      {loading && !data ? (
        <div className="flex justify-center py-16 text-slate-400"><Spinner className="w-6 h-6" /></div>
      ) : packets.length === 0 && !error ? (
        <div className="glass flex flex-col items-center gap-3 rounded-2xl px-6 py-12 text-center">
          <Smartphone className="w-8 h-8 text-slate-500" />
          <p className="font-semibold text-white">No active packets</p>
          <p className="max-w-md text-sm text-slate-400">Validate a packet on the Packets tab first, or paste any packet id above.</p>
          <Button variant="secondary" onClick={() => navigate('')}>Go to Packets</Button>
        </div>
      ) : shown.length === 0 && !pastedId ? (
        <p className="py-10 text-center text-sm text-slate-400">No active packet matches &quot;{query}&quot;.</p>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {shown.map(p => (
            <article key={p.id} className="glass group flex flex-col rounded-2xl p-4 transition-colors hover:border-brand/40">
              <div className="flex items-center justify-between gap-2">
                <span className="rounded-full border border-white/10 bg-white/[0.04] px-2 py-0.5 text-[11px] font-semibold text-slate-300">{formatLabel(p.format_type)}</span>
                <Tag>{p.id}</Tag>
              </div>
              <h2 className="mt-3 line-clamp-2 text-base text-white" style={{ fontFamily: 'inherit', letterSpacing: 'normal' }}>{p.title}</h2>
              <p className="mt-1 flex items-start gap-1.5 text-xs text-slate-400">
                <MapPin className="mt-0.5 w-3.5 h-3.5 shrink-0" /><span className="line-clamp-2">{p.location_label}</span>
              </p>
              <dl className="mt-3 grid grid-cols-3 gap-2 text-[11px]">
                <div className="rounded-lg bg-white/[0.03] px-2 py-1.5"><dt className="text-slate-500">Radius</dt><dd className="text-slate-200">{formatDistance(p.radius_meters)}</dd></div>
                <div className="rounded-lg bg-white/[0.03] px-2 py-1.5"><dt className="text-slate-500">Min basket</dt><dd className="text-slate-200">{rupiah(p.min_basket_value)}</dd></div>
                <div className="rounded-lg bg-white/[0.03] px-2 py-1.5"><dt className="text-slate-500">Version</dt><dd className="text-slate-200">v{p.version}</dd></div>
              </dl>
              <div className="mt-4 flex items-center justify-between gap-2 border-t border-white/[0.06] pt-3">
                <span className="text-[11px] text-slate-500">Updated {shortTime(p.updated_at)}</span>
                <Button size="sm" onClick={() => open(p.id)}><Smartphone className="w-3.5 h-3.5" /> Simulate</Button>
              </div>
            </article>
          ))}
        </div>
      )}
    </div>
  );
}

/* ─────────────────────────── status actions ──────────────────────────────── */

const ACTIONS = {
  validate: { label: 'Validate & activate', verb: 'Activated', variant: 'success' },
  pause: { label: 'Pause', verb: 'Paused', variant: 'warning' },
  retire: { label: 'Retire', verb: 'Retired', variant: 'danger' },
};

function allowedActions(status) {
  if (status === 'DRAFT' || status === 'PAUSED') return ['validate', 'retire'];
  if (status === 'ACTIVE') return ['pause', 'retire'];
  return [];
}

async function runAction(packet, action) {
  try {
    const data = await adminApi.transition(packet.id, action, packet.version);
    const warnings = data.warnings || [];
    return { tone: warnings.length ? 'warning' : 'success', title: `${ACTIONS[action].verb}: ${data.packet.title} is now ${data.packet.status}`, lines: warnings, coverage: data.coverage, packet: data.packet };
  } catch (err) {
    if (!(err instanceof AdminError)) throw err;
    if (err.has('COVERAGE_NOT_CONFIGURED') || err.has('COVERAGE_UNAVAILABLE')) {
      return {
        tone: 'warning',
        title: err.has('COVERAGE_NOT_CONFIGURED') ? 'qr-service has no coverage check: packet stays as it was' : 'Coverage check unreachable: packet left unchanged',
        lines: [...(err.messages[0]?.vals || []), 'Set COVERAGE_BAP_URL on qr-service, or for local testing REQUIRE_COVERAGE_FOR_ACTIVATION=false to activate with a warning.'],
      };
    }
    if (err.has('COVERAGE_INCOMPLETE')) {
      return { tone: 'error', title: 'No merchant in range stocks every item', lines: err.data?.missing_items?.length ? [`Missing: ${err.data.missing_items.join(', ')}`] : [], coverage: err.data };
    }
    return { tone: 'error', title: err.message, lines: err.messages.slice(1).map(m => m.errcode) };
  }
}

// deletePacket asks, then deletes; a live packet is paused first (qr-service
// refuses to delete an ACTIVE one). Returns an ActionResult, or null if cancelled.
async function deletePacket(packet) {
  const live = packet.status === 'ACTIVE';
  const ok = window.confirm(live
    ? `"${packet.title}" is live. Pause it and delete it permanently? Its printed QR code will stop working.`
    : `Delete "${packet.title}" permanently? Its QR code will stop working. Orders already placed stay in the order history.`);
  if (!ok) return null;
  try {
    let version = packet.version;
    if (live) version = (await adminApi.transition(packet.id, 'pause', version)).packet.version;
    await adminApi.remove(packet.id, version);
    return { tone: 'success', title: `Deleted "${packet.title}"`, lines: [] };
  } catch (err) {
    return { tone: 'error', title: err.message, lines: [] };
  }
}

function ActionResult({ result, onClose }) {
  if (!result) return null;
  const cov = result.coverage;
  return (
    <Alert tone={result.tone} title={result.title} onClose={onClose}>
      {result.lines.map((l, i) => <p key={i}>{l}</p>)}
      {cov && (
        <div className="mt-2 space-y-1">
          <p className="font-semibold">Merchant coverage</p>
          {cov.eligible_providers?.length ? (
            <ul className="list-disc pl-4">
              {cov.eligible_providers.map(p => <li key={`${p.bpp_id}|${p.provider_id}`}>{p.provider_name || p.provider_id} · {p.distance_meters >= 0 ? formatDistance(p.distance_meters) : 'distance unknown'}</li>)}
            </ul>
          ) : <p>No eligible merchants.</p>}
        </div>
      )}
    </Alert>
  );
}

/* ─────────────────────────── list ────────────────────────────────────────── */

const PAGE = 25;

function PacketsList({ navigate, query, onExit }) {
  const status = query.get('status') || '';
  const page = Number(query.get('page') || 1);
  const [result, setResult] = useState(() => window.history.state?.deleted || null);
  const [busy, setBusy] = useState(null);
  const { data, error, loading, reload } = useLoad(signal => adminApi.list({ status, page, limit: PAGE }, signal), [status, page]);

  const go = (s, p = 1) => navigate(`${s || p > 1 ? '?' : ''}${new URLSearchParams({ ...(s && { status: s }), ...(p > 1 && { page: String(p) }) })}`);
  const toggle = async p => {
    setBusy(p.id);
    setResult(null);
    try { setResult(await runAction(p, p.status === 'ACTIVE' ? 'pause' : 'validate')); reload(); } finally { setBusy(null); }
  };
  const remove = async p => {
    setBusy(`del:${p.id}`);
    try { const r = await deletePacket(p); if (r) { setResult(r); reload(); } } finally { setBusy(null); }
  };
  const packets = data?.packets || [];
  const pages = data ? Math.max(1, Math.ceil(data.total / PAGE)) : 1;

  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          {onExit && (
            <button type="button" onClick={onExit} className="flex items-center gap-1 text-xs text-slate-400 hover:text-white">
              <ArrowLeft className="w-3 h-3" /> Back to shop
            </button>
          )}
          <h1 className="mt-1 text-2xl text-white">QR Packets</h1>
          <p className="text-sm text-slate-400">Curated baskets tied to a physical spot. Buyers scan the QR and order from the nearest merchant.</p>
        </div>
        <div className="flex gap-2">
          <Button variant="secondary" onClick={reload} busy={loading}><RefreshCw className="w-4 h-4" /> Refresh</Button>
          <Button onClick={() => navigate('/new')}><Plus className="w-4 h-4" /> New packet</Button>
        </div>
      </header>

      <div className="flex flex-wrap gap-1.5">
        {['', ...STATUSES].map(s => (
          <button key={s || 'ALL'} type="button" onClick={() => go(s)}
            className={cx('rounded-full border px-3 py-1 text-xs font-bold transition-colors', status === s ? 'border-brand/60 bg-brand/15 text-brand' : 'border-white/10 text-slate-300 hover:bg-white/[0.06]')}>
            {s || 'ALL'}
          </button>
        ))}
        {data && <span className="ml-auto self-center text-xs text-slate-500">{data.total} packet(s)</span>}
      </div>

      <ActionResult result={result} onClose={() => setResult(null)} />
      <ErrorNote error={error} title="Could not load packets" />

      {loading && !data ? (
        <div className="flex justify-center py-16 text-slate-400"><Spinner className="w-6 h-6" /></div>
      ) : packets.length === 0 && !error ? (
        <div className="glass flex flex-col items-center gap-3 rounded-2xl px-6 py-12 text-center">
          <p className="font-semibold text-white">{status ? `No ${status} packets` : 'No packets yet'}</p>
          <p className="max-w-md text-sm text-slate-400">Create a packet, validate it against discovery, then print its QR poster.</p>
          <Button onClick={() => navigate('/new')}><Plus className="w-4 h-4" /> Create a packet</Button>
        </div>
      ) : (
        <Card bodyClassName="p-0">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[720px] text-left text-sm">
              <thead className="text-xs text-slate-400 uppercase">
                <tr className="border-b border-white/[0.06]">
                  <th className="px-4 py-3">Packet</th><th className="px-4 py-3">Format</th><th className="px-4 py-3">Status</th><th className="px-4 py-3">Updated</th><th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/[0.05]">
                {packets.map(p => (
                  <tr key={p.id} className="hover:bg-white/[0.02]">
                    <td className="px-4 py-3">
                      <Link to={`/${p.id}`} navigate={navigate} className="font-semibold text-white hover:underline">{p.title}</Link>
                      <div className="mt-0.5 flex items-center gap-2 text-xs text-slate-500"><Tag>{p.id}</Tag><span className="truncate max-w-[280px]">{p.location_label}</span></div>
                    </td>
                    <td className="px-4 py-3 text-slate-300">{formatLabel(p.format_type)}</td>
                    <td className="px-4 py-3"><StatusBadge status={p.status} /><div className="mt-0.5 text-xs text-slate-500">v{p.version}</div></td>
                    <td className="px-4 py-3 text-xs text-slate-400">{shortTime(p.updated_at)}</td>
                    <td className="px-4 py-3">
                      <div className="flex justify-end gap-1.5">
                        <Button size="sm" variant="secondary" onClick={() => navigate(`/${p.id}`)}>View / QR</Button>
                        {/* Same width for Pause and Activate (and a blank slot when
                            retired) so the buttons line up row to row. */}
                        {p.status !== 'RETIRED' ? (
                          <Button size="sm" className="w-[84px]" variant={p.status === 'ACTIVE' ? 'warning' : 'success'} busy={busy === p.id} onClick={() => toggle(p)}>
                            {p.status === 'ACTIVE' ? 'Pause' : 'Activate'}
                          </Button>
                        ) : <span className="w-[84px]" aria-hidden="true" />}
                        <Button size="sm" variant="ghost" className="text-rose-300 hover:text-rose-200" busy={busy === `del:${p.id}`} onClick={() => remove(p)} title="Delete packet" aria-label={`Delete ${p.title}`}>
                          <Trash2 className="w-3.5 h-3.5" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {pages > 1 && (
            <div className="flex items-center justify-between border-t border-white/[0.06] px-4 py-2 text-xs">
              <Button size="sm" variant="ghost" disabled={page <= 1} onClick={() => go(status, page - 1)}>← Prev</Button>
              <span className="text-slate-400">Page {page} of {pages}</span>
              <Button size="sm" variant="ghost" disabled={page >= pages} onClick={() => go(status, page + 1)}>Next →</Button>
            </div>
          )}
        </Card>
      )}
    </div>
  );
}

/* ─────────────────────────── create / edit ───────────────────────────────── */

/* ─────────────────────────── basket: product search + rows ───────────────── */

// matchSummary says in plain words how a packet item finds products at a merchant.
function matchSummary(it) {
  const v = it.match_value.trim();
  if (!v) return 'No matching rule yet';
  if (it.match_type === 'GTIN') return `Barcode ${v}`;
  if (it.match_type === 'ION_SKU') return `Exact product id ${v}`;
  return `Name contains "${v}"`;
}

// ProductSearch searches the network (a Beckn discover through the BAP) for
// products to add to the basket (the whole network: the radius only matters at scan time).
// SEARCH_DEBOUNCE_MS: the pause after typing before a search runs.
const SEARCH_DEBOUNCE_MS = 500;

function ProductSearch({ items, onAdd }) {
  const [q, setQ] = useState('');
  const [state, setState] = useState({ loading: false, error: null, data: null, query: '' });
  const ctrlRef = useRef(null);
  const inBasket = pr => items.some(it => it.match_type === pr.suggested_match.match_type && it.match_value.trim().toLowerCase() === pr.suggested_match.match_value.toLowerCase());

  // Runs one search, cancelling the one before; a stale answer is dropped.
  const runSearch = useCallback(async raw => {
    const query = raw.trim();
    ctrlRef.current?.abort();
    if (query.length < 2) {
      setState({ loading: false, error: null, data: null, query: '' });
      return;
    }
    const ctrl = new AbortController();
    ctrlRef.current = ctrl;
    setState(st => ({ ...st, loading: true, error: null, query }));
    try {
      const data = await adminApi.products({ q: query }, ctrl.signal);
      if (!ctrl.signal.aborted) setState({ loading: false, error: null, data, query });
    } catch (err) {
      if (err.name !== 'AbortError' && !ctrl.signal.aborted) setState({ loading: false, error: err, data: null, query });
    }
  }, []);

  // Search as you type, once typing pauses.
  useEffect(() => {
    const t = setTimeout(() => runSearch(q), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [q, runSearch]);
  useEffect(() => () => ctrlRef.current?.abort(), []);

  // Not a <form>: this sits inside the packet form, and a nested form's submit
  // would also submit (and validate) the packet.
  return (
    <div>
      <div className="relative">
        <Search className="pointer-events-none absolute left-3.5 top-1/2 w-4 h-4 -translate-y-1/2 text-slate-500" />
        <input className={cx(inputClass(false), 'py-3 pl-10 pr-10')} value={q} onChange={e => setQ(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter') { e.preventDefault(); runSearch(q); }
            if (e.key === 'Escape') setQ('');
          }}
          placeholder="Search products to add, e.g. keripik, indomie, aqua" aria-label="Search products" />
        <span className="absolute right-3 top-1/2 -translate-y-1/2">
          {state.loading ? <Spinner className="w-4 h-4 text-brand" />
            : q && <button type="button" onClick={() => setQ('')} className="grid h-6 w-6 place-items-center rounded-full text-slate-400 hover:bg-white/10 hover:text-white" aria-label="Clear search"><X className="w-3.5 h-3.5" /></button>}
        </span>
      </div>

      {state.loading && !state.data && <p className="mt-3 flex items-center gap-2 text-sm text-slate-400"><Spinner className="w-4 h-4" /> Searching the network…</p>}
      {state.error && <div className="mt-3"><ErrorNote error={state.error} title="Search failed" /></div>}
      {state.data && (
        state.data.products.length === 0 ? (
          <p className="mt-3 text-sm text-slate-400">No products match &quot;{state.query}&quot;.</p>
        ) : (
          <div className="mt-2 overflow-hidden rounded-xl border border-white/10 bg-slate-950/40">
            <p className="border-b border-white/[0.06] px-3 py-1.5 text-[11px] font-semibold tracking-wide text-slate-500 uppercase">
              {state.data.products.length} result{state.data.products.length === 1 ? '' : 's'}
            </p>
            <ul className="max-h-72 divide-y divide-white/[0.05] overflow-y-auto">
              {state.data.products.map(pr => {
                const added = inBasket(pr);
                return (
                  <li key={`${pr.bpp_id}|${pr.resource_id}`} className="flex items-center gap-3 px-3 py-2 hover:bg-white/[0.03]">
                    <Thumb src={pr.image} />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-semibold text-white">{pr.name}</p>
                      <p className="truncate text-xs text-slate-400">{pr.provider_name} · {rupiah(pr.price)}</p>
                    </div>
                    <Button size="sm" variant={added ? 'ghost' : 'secondary'} disabled={added || items.length >= 50} onClick={() => onAdd(pr)}>
                      {added ? <><CheckCircle2 className="w-3.5 h-3.5 text-emerald-400" /> Added</> : <><Plus className="w-3.5 h-3.5" /> Add</>}
                    </Button>
                  </li>
                );
              })}
            </ul>
          </div>
        )
      )}
    </div>
  );
}

// Thumb is a product image, or a placeholder.
function Thumb({ src, size = 'h-10 w-10' }) {
  return src
    ? <img src={src} alt="" className={cx(size, 'shrink-0 rounded-lg object-cover')} />
    : <div className={cx(size, 'grid shrink-0 place-items-center rounded-lg bg-white/[0.06]')}><Package className="w-4 h-4 text-slate-500" /></div>;
}

// BasketRow is one packet item, added from search. The buyer chooses which
// items to order and how many after scanning, so there is no quantity here;
// how it is matched comes from the picked product.
function BasketItem({ it, i, err, remove }) {
  const itemErr = err(`items.${i}.match_value`) || err(`items.${i}.display_name`);
  return (
    <li className={cx('group flex items-center gap-3 rounded-xl border p-2.5', itemErr ? 'border-rose-400/40 bg-rose-500/5' : 'border-white/10 bg-white/[0.03]')}>
      <Thumb src={it._image} />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-semibold text-white" title={it.display_name}>{it.display_name}</p>
        <p className="truncate text-xs text-slate-500" title={matchSummary(it)}>{it._seller ? `Found at ${it._seller}` : matchSummary(it)}</p>
        {itemErr && <p className="mt-0.5 text-xs font-semibold text-rose-300">{itemErr}: remove it and add it again from search.</p>}
      </div>
      <button type="button" onClick={() => remove(i)} aria-label={`Remove ${it.display_name}`} title="Remove"
        className="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-slate-500 transition-colors hover:bg-rose-500/15 hover:text-rose-300">
        <X className="w-4 h-4" />
      </button>
    </li>
  );
}

// PrefillMenu is a card-header menu that fills that card from a preset.
// options are {name}; picking one calls onPick(name) and resets the menu.
function PrefillMenu({ label, options, onPick }) {
  return (
    <label className="relative inline-flex items-center">
      <Wand2 className="pointer-events-none absolute left-2.5 w-3.5 h-3.5 text-brand" />
      <select value="" onChange={e => onPick(e.target.value)} aria-label={label} title={label}
        className="w-[7.5rem] cursor-pointer appearance-none truncate rounded-xl border border-brand/30 bg-brand/10 py-1.5 pl-8 pr-7 text-xs font-semibold text-brand outline-none transition-colors hover:bg-brand/15 focus:border-brand/60">
        <option value="" className="bg-slate-900 text-slate-300">Prefill</option>
        {options.map(o => <option key={o.name} value={o.name} className="bg-slate-900 text-white">{o.name}</option>)}
      </select>
      <ChevronDown className="pointer-events-none absolute right-2 w-3.5 h-3.5 text-brand" />
    </label>
  );
}

const blankForm = () => ({
  title: '', format_type: 'GENERAL', allow_node_as_delivery: true,
  location_label: '', lat: '', lon: '', radius_meters: 3000, min_basket_value: 0, items: [],
});

const fromPacket = p => ({
  title: p.title, format_type: p.format_type, allow_node_as_delivery: p.allow_node_as_delivery,
  location_label: p.location_label, lat: String(p.lat), lon: String(p.lon), radius_meters: p.radius_meters,
  min_basket_value: p.min_basket_value,
  items: (p.items || []).map(({ match_type, match_value, display_name }) => ({ match_type, match_value, display_name })),
});

const toInput = f => ({
  title: f.title.trim(), format_type: f.format_type, allow_node_as_delivery: f.allow_node_as_delivery,
  location_label: f.location_label.trim(), lat: Number(f.lat), lon: Number(f.lon), radius_meters: Number(f.radius_meters),
  min_basket_value: Number(f.min_basket_value),
  items: f.items.map((it, i) => ({ match_type: it.match_type, match_value: it.match_value.trim(), display_name: it.display_name.trim(), sort_order: i })),
});

function PacketForm({ id, navigate }) {
  const editing = Boolean(id);
  const [form, setForm] = useState(blankForm);
  const [packet, setPacket] = useState(null);
  const [loadError, setLoadError] = useState(null);
  const [errors, setErrors] = useState({});
  const [submitError, setSubmitError] = useState(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!editing) return;
    adminApi.get(id).then(p => { setPacket(p); setForm(fromPacket(p)); }).catch(setLoadError);
  }, [editing, id]);

  const set = (k, v) => setForm(f => ({ ...f, [k]: v }));
  // Prefill menus, one per card. Each fills only its own card's fields.
  const prefillPacket = name => {
    const p = PACKET_PRESETS.find(x => x.name === name);
    if (p) setForm(f => ({ ...f, ...p.values }));
  };
  const prefillLocation = name => {
    const p = LOCATION_PRESETS.find(x => x.name === name);
    if (p) setForm(f => ({ ...f, location_label: p.label, lat: String(p.lat), lon: String(p.lon) }));
  };
  // A picked product becomes a packet item that finds it at any merchant
  // (barcode, or the main words of its name). An untouched blank row is replaced.
  const addProduct = pr => setForm(f => {
    const item = {
      match_type: pr.suggested_match.match_type, match_value: pr.suggested_match.match_value,
      display_name: pr.display_name,
      _image: pr.image, _seller: pr.provider_name,
    };
    const blank = it => !it.match_value.trim() && !it.display_name.trim();
    const items = f.items.length === 1 && blank(f.items[0]) ? [item] : [...f.items, item];
    return { ...f, items };
  });

  const submit = async e => {
    e.preventDefault();
    setSubmitError(null);
    const errs = validatePacketForm(form);
    setErrors(errs);
    if (Object.keys(errs).length) return;
    setSaving(true);
    try {
      const data = editing ? await adminApi.update(id, packet.version, toInput(form)) : await adminApi.create(toInput(form));
      navigate(`/${data.packet.id}`, { saved: editing ? 'updated' : 'created', warnings: data.warnings, coverage: data.coverage });
    } catch (err) {
      if (err instanceof AdminError) setErrors(serverFieldErrors(err));
      setSubmitError(err);
    } finally {
      setSaving(false);
    }
  };

  if (editing && loadError) return <ErrorNote error={loadError} title="Could not load packet" />;
  if (editing && !packet) return <div className="flex justify-center py-16 text-slate-400"><Spinner className="w-6 h-6" /></div>;
  const err = k => errors[k];

  return (
    <form onSubmit={submit} noValidate className="space-y-5">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <Link to={editing ? `/${id}` : ''} navigate={navigate} className="flex items-center gap-1 text-xs text-slate-400 hover:text-white"><ArrowLeft className="w-3 h-3" /> {editing ? 'Back to packet' : 'All packets'}</Link>
          <h1 className="mt-1 text-2xl text-white">{editing ? 'Edit packet' : 'New packet'}</h1>
          {editing && <p className="mt-1 flex items-center gap-2 text-sm text-slate-400"><StatusBadge status={packet.status} /> v{packet.version} · <span className="font-mono">{id}</span></p>}
        </div>
        <div className="flex gap-2">
          <Button variant="secondary" onClick={() => window.history.back()}>Cancel</Button>
          <Button type="submit" busy={saving}>{editing ? 'Save changes' : 'Create as DRAFT'}</Button>
        </div>
      </header>

      {editing && packet.status === 'ACTIVE' && (
        <Alert tone="info" title="This packet is live">Changing location, radius or items moves it back to DRAFT until validated again.</Alert>
      )}
      <ErrorNote error={submitError} title="Save failed" onClose={() => setSubmitError(null)} />
      {Object.keys(errors).length > 0 && !submitError && <Alert tone="error" title="Fix the highlighted fields." />}

      <div className="grid items-start gap-5 lg:grid-cols-3">
        <div className="space-y-5 lg:col-span-2">
          <Card title="Details" actions={<PrefillMenu label="Packet presets" options={PACKET_PRESETS} onPick={prefillPacket} />}>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Title" htmlFor="title" error={err('title')} className="sm:col-span-2">
                <input id="title" className={inputClass(err('title'))} value={form.title} maxLength={120} onChange={e => set('title', e.target.value)} placeholder="Kos Melati - Paket Malam" />
              </Field>
              <Field label="Minimum order" htmlFor="minb" error={err('min_basket_value')} hint="Items subtotal the buyer must reach (0 = none)">
                <div className="relative">
                  <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-sm text-slate-500">Rp</span>
                  <input id="minb" type="number" min="0" step="500" className={cx(inputClass(err('min_basket_value')), 'pl-10')} value={form.min_basket_value} onChange={e => set('min_basket_value', e.target.value)} />
                </div>
              </Field>
              <Field label="Type of spot" htmlFor="format" error={err('format_type')} hint="Optional label on the poster">
                <select id="format" className={inputClass(err('format_type'))} value={form.format_type} onChange={e => set('format_type', e.target.value)}>
                  {FORMAT_TYPES.map(f => <option key={f.value} value={f.value} className="bg-slate-900">{f.label}</option>)}
                </select>
              </Field>
              <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-white/10 bg-white/[0.02] p-3 sm:col-span-2">
                <input type="checkbox" className="mt-0.5 w-4 h-4 accent-cyan-400" checked={form.allow_node_as_delivery} onChange={e => set('allow_node_as_delivery', e.target.checked)} />
                <span>
                  <span className="block text-sm font-semibold text-slate-100">Deliver to this spot</span>
                  <span className="block text-xs text-slate-500">Buyers can get the order delivered here. Self pickup is always offered.</span>
                </span>
              </label>
            </div>
          </Card>

          <Card title={`Items${form.items.length ? ` · ${form.items.length}` : ''}`}>
            <ProductSearch items={form.items} onAdd={addProduct} />
            {err('items') && <p className="mt-3 text-sm font-semibold text-rose-300">{err('items')}</p>}
            {form.items.length === 0 ? (
              <div className="mt-4 flex flex-col items-center gap-1 rounded-xl border border-dashed border-white/15 px-4 py-8 text-center">
                <Package className="w-6 h-6 text-slate-600" />
                <p className="text-sm text-slate-300">No items yet</p>
                <p className="text-xs text-slate-500">Search above to add products from the network.</p>
              </div>
            ) : (
              <ul className="mt-4 grid gap-2 sm:grid-cols-2">
                {form.items.map((it, i) => (
                  <BasketItem key={`${it.match_type}|${it.match_value}`} it={it} i={i} err={err}
                    remove={j => set('items', form.items.filter((_, k) => k !== j))} />
                ))}
              </ul>
            )}
          </Card>
        </div>

        <div className="space-y-5">
          <Card title="Location" actions={<PrefillMenu label="Sample locations" options={LOCATION_PRESETS} onPick={prefillLocation} />}>
            <div className="space-y-4">
              <Field label="Address" htmlFor="loc" error={err('location_label')} hint="Buyers add their own room or unit">
                <input id="loc" className={inputClass(err('location_label'))} value={form.location_label} onChange={e => set('location_label', e.target.value)} placeholder="Jl. Margonda Raya No. 12, Depok" />
              </Field>
              <div className="grid grid-cols-2 gap-3">
                <Field label="Latitude" htmlFor="lat" error={err('lat')}><input id="lat" inputMode="decimal" className={cx(inputClass(err('lat')), 'font-mono')} value={form.lat} onChange={e => set('lat', e.target.value)} placeholder="-6.3625" /></Field>
                <Field label="Longitude" htmlFor="lon" error={err('lon')}><input id="lon" inputMode="decimal" className={cx(inputClass(err('lon')), 'font-mono')} value={form.lon} onChange={e => set('lon', e.target.value)} placeholder="106.8295" /></Field>
              </div>
              <div>
                <div className="mb-1 flex items-center justify-between">
                  <label htmlFor="radius" className="text-xs font-semibold text-slate-400">Merchant radius</label>
                  <span className="rounded-md bg-brand/10 px-2 py-0.5 text-xs font-semibold text-brand">{formatDistance(Number(form.radius_meters))}</span>
                </div>
                <input id="radius" type="range" min="100" max="25000" step="100" className="w-full accent-cyan-400" value={form.radius_meters} onChange={e => set('radius_meters', Number(e.target.value))} />
                <div className="flex justify-between text-[11px] text-slate-600"><span>100 m</span><span>25 km</span></div>
                {err('radius_meters') && <p className="mt-1 text-xs font-semibold text-rose-300">{err('radius_meters')}</p>}
              </div>
            </div>
          </Card>

          <section className="rounded-2xl border border-white/[0.06] bg-white/[0.02] p-4">
            <h3 className="text-xs font-bold tracking-wide text-slate-400 uppercase" style={{ fontFamily: 'inherit' }}>When a buyer scans</h3>
            <ol className="mt-2 space-y-1.5 text-xs text-slate-400">
              <li><span className="font-semibold text-slate-200">1.</span> The nearest merchant within the radius that stocks every item is found.</li>
              <li><span className="font-semibold text-slate-200">2.</span> The buyer picks which items and how many.</li>
              <li><span className="font-semibold text-slate-200">3.</span> They pay once the minimum order is reached.</li>
            </ol>
          </section>
        </div>
      </div>
    </form>
  );
}

/* ─────────────────────────── detail ──────────────────────────────────────── */

function useBlobUrl(load, deps) {
  const [s, setS] = useState({ url: null, blob: null, error: null });
  useEffect(() => {
    const ctrl = new AbortController();
    let url;
    load(ctrl.signal).then(blob => { url = URL.createObjectURL(blob); setS({ url, blob, error: null }); })
      .catch(error => error.name !== 'AbortError' && setS({ url: null, blob: null, error }));
    return () => { ctrl.abort(); if (url) URL.revokeObjectURL(url); };
  }, deps); // eslint-disable-line react-hooks/exhaustive-deps
  return s;
}

function download(blob, name) {
  const url = URL.createObjectURL(blob);
  const a = Object.assign(document.createElement('a'), { href: url, download: name });
  document.body.appendChild(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

// buyerUrl opens the buyer page straight from this BAP (recorded as a
// DIRECT_RESOLVE scan); the printed QR goes through qr-service's /p/:id.
const buyerUrl = id => `${window.location.origin}${basePath}?packet_id=${encodeURIComponent(id)}`;

function QrStudio({ packet, navigate }) {
  const [size, setSize] = useState(512);
  const [copied, setCopied] = useState(false);
  const png = useBlobUrl(signal => adminApi.qrPng(packet.id, size, signal), [packet.id, size]);
  const copy = async () => { await navigator.clipboard.writeText(packet.deep_link_url); setCopied(true); setTimeout(() => setCopied(false), 1500); };
  return (
    <Card title="QR code">
      <div className="flex flex-col items-center gap-4">
        <div className="grid aspect-square w-full max-w-[260px] place-items-center rounded-2xl bg-white">
          {png.url ? <img src={png.url} alt={`QR code for ${packet.title}`} className="h-full w-full rounded-2xl object-contain p-2" />
            : png.error ? <p className="p-4 text-center text-sm text-rose-600">{png.error.message}</p>
              : <Spinner className="w-6 h-6 text-slate-400" />}
        </div>
        <p className="text-center text-xs text-slate-400">Encodes <span className="break-all font-mono text-slate-200">{packet.deep_link_url}</span></p>
        <div className="flex flex-wrap items-center justify-center gap-2">
          <select value={size} onChange={e => setSize(Number(e.target.value))} className="rounded-lg border border-white/10 bg-white/[0.06] px-2 py-1 text-xs text-white">
            {[256, 512, 1024, 2048].map(s => <option key={s} value={s} className="bg-slate-900">{s}px</option>)}
          </select>
          <Button size="sm" variant="secondary" disabled={!png.blob} onClick={() => download(png.blob, `packet-${packet.id}-${size}.png`)}><Download className="w-3.5 h-3.5" /> PNG</Button>
          <Button size="sm" variant="secondary" onClick={async () => download(await adminApi.qrSvg(packet.id), `packet-${packet.id}.svg`)}><Download className="w-3.5 h-3.5" /> SVG</Button>
          <Button size="sm" variant="secondary" onClick={copy}><Copy className="w-3.5 h-3.5" /> {copied ? 'Copied' : 'Link'}</Button>
        </div>
        <Button className="w-full" onClick={() => navigate(`/${packet.id}/poster`)}><Printer className="w-4 h-4" /> Print poster</Button>
        <a href={buyerUrl(packet.id)} target="_blank" rel="noreferrer" className="w-full">
          <Button variant="secondary" className="w-full"><ExternalLink className="w-4 h-4" /> Open buyer page</Button>
        </a>
      </div>
    </Card>
  );
}

const ORDER_TONE = {
  CONFIRMED: 'text-emerald-300', CONFIRM_SENT: 'text-emerald-200',
  AWAITING_CONFIRM: 'text-amber-300', INIT_SENT: 'text-amber-200', QUOTED: 'text-slate-300', FAILED: 'text-rose-300',
};

function PacketOrders({ packetId }) {
  const { data, error, loading, reload } = useLoad(signal => adminApi.orders(packetId, signal), [packetId]);
  return (
    <Card title={`Orders from this QR${data ? ` (${data.length})` : ''}`} actions={<Button size="sm" variant="ghost" onClick={reload} busy={loading}><RefreshCw className="w-3.5 h-3.5" /></Button>} bodyClassName="p-0">
      {error ? <div className="p-4"><ErrorNote error={error} title="Could not load orders" /></div>
        : !data ? <div className="flex justify-center py-6"><Spinner /></div>
          : data.length === 0 ? <p className="p-4 text-sm text-slate-400">No checkouts yet. Open the buyer page or scan the QR to start one.</p> : (
            <div className="overflow-x-auto">
              <table className="w-full min-w-[520px] text-left text-sm">
                <thead className="text-xs text-slate-400 uppercase"><tr className="border-b border-white/[0.06]"><th className="px-4 py-2">When</th><th className="px-4 py-2">Merchant</th><th className="px-4 py-2">State</th><th className="px-4 py-2 text-right">Total</th></tr></thead>
                <tbody className="divide-y divide-white/[0.05]">
                  {data.map(o => (
                    <tr key={o.transaction_id} title={o.transaction_id}>
                      <td className="px-4 py-2 text-xs text-slate-400">{shortTime(o.created_at)}<div className="font-mono text-[10px] text-slate-600">{o.transaction_id.slice(0, 8)} · v{o.packet_version}</div></td>
                      <td className="px-4 py-2 text-slate-200">{o.provider_name}<div className="text-[11px] text-slate-500">{o.fulfillment_mode === 'SELF_PICKUP' ? 'Self pickup' : o.fulfillment_mode ? 'Deliver to spot' : ''}</div></td>
                      <td className={cx('px-4 py-2 text-xs font-semibold', ORDER_TONE[o.state])}>{o.state}{o.demo_scenario && <span className="ml-1.5 rounded bg-violet-500/20 px-1 py-0.5 text-[10px] font-bold text-violet-200">DEMO</span>}<div className="font-normal text-slate-500">{o.transaction_status}</div>{o.last_error && <div className="max-w-[220px] truncate font-normal text-rose-300/80" title={o.last_error}>{o.last_error}</div>}</td>
                      <td className="px-4 py-2 text-right text-slate-200">{rupiah(o.total)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
    </Card>
  );
}

function PacketDetail({ id, navigate }) {
  const saved = window.history.state?.saved;
  const [result, setResult] = useState(() => (saved ? { tone: window.history.state.warnings?.length ? 'warning' : 'success', title: `Packet ${saved}`, lines: window.history.state.warnings || [], coverage: window.history.state.coverage } : null));
  const [busy, setBusy] = useState(null);
  const { data: packet, error, loading, reload, setData } = useLoad(signal => adminApi.get(id, signal), [id]);

  const remove = async () => {
    setBusy('delete');
    try {
      const r = await deletePacket(packet);
      if (!r) return;
      if (r.tone === 'success') navigate('', { deleted: r }); else setResult(r);
    } finally { setBusy(null); }
  };

  const act = async action => {
    if (action === 'retire' && !window.confirm('Retire this packet permanently? Its QR code will stop working.')) return;
    setBusy(action);
    try {
      const r = await runAction(packet, action);
      setResult(r);
      if (r.packet) setData({ ...packet, ...r.packet }); else reload();
    } finally { setBusy(null); }
  };

  if (error) return <ErrorNote error={error} title="Could not load packet" />;
  if (loading && !packet) return <div className="flex justify-center py-16 text-slate-400"><Spinner className="w-6 h-6" /></div>;
  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <Link to="" navigate={navigate} className="flex items-center gap-1 text-xs text-slate-400 hover:text-white"><ArrowLeft className="w-3 h-3" /> All packets</Link>
          <h1 className="mt-1 text-2xl text-white">{packet.title}</h1>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-sm text-slate-400">
            <StatusBadge status={packet.status} /><Tag>{packet.id}</Tag>{packet.format_type !== 'GENERAL' && <span>{formatLabel(packet.format_type)}</span>}<span>· v{packet.version}</span><span>· updated {shortTime(packet.updated_at)}</span>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="secondary" onClick={() => navigate(`/${id}/simulate`)}><Smartphone className="w-4 h-4" /> Simulate scan</Button>
          {packet.status !== 'RETIRED' && <Button variant="secondary" onClick={() => navigate(`/${id}/edit`)}><Pencil className="w-4 h-4" /> Edit</Button>}
          {allowedActions(packet.status).map(a => (
            <Button key={a} variant={ACTIONS[a].variant} busy={busy === a} onClick={() => act(a)}>{a === 'validate' ? 'Validate against discovery' : ACTIONS[a].label}</Button>
          ))}
          <Button variant="danger" busy={busy === 'delete'} onClick={remove}><Trash2 className="w-4 h-4" /> Delete</Button>
        </div>
      </header>

      <ActionResult result={result} onClose={() => setResult(null)} />
      {packet.status === 'DRAFT' && !result && (
        <Alert tone="info" title="Draft: scans don't reach checkout yet">Validating checks that a merchant within {formatDistance(packet.radius_meters)} stocks every item, then activates the packet.</Alert>
      )}

      <div className="grid gap-5 lg:grid-cols-3">
        <div className="space-y-5 lg:col-span-2">
          <Card title="Spot & rules">
            <dl className="grid gap-x-6 gap-y-3 text-sm sm:grid-cols-2">
              <div className="sm:col-span-2">
                <dt className="text-xs text-slate-500">Location</dt>
                <dd className="text-slate-100">{packet.location_label}</dd>
                <dd className="font-mono text-xs text-slate-400">{packet.lat}, {packet.lon} · <a className="text-brand underline" href={`https://www.openstreetmap.org/?mlat=${packet.lat}&mlon=${packet.lon}#map=17/${packet.lat}/${packet.lon}`} target="_blank" rel="noreferrer">map ↗</a></dd>
              </div>
              <div><dt className="text-xs text-slate-500">Delivery</dt><dd className="text-slate-200">{packet.allow_node_as_delivery ? 'To the spot, or self pickup' : 'Self pickup only'}</dd></div>
              <div><dt className="text-xs text-slate-500">Radius</dt><dd className="text-slate-200">{formatDistance(packet.radius_meters)}</dd></div>
              <div><dt className="text-xs text-slate-500">Min basket</dt><dd className="text-slate-200">{rupiah(packet.min_basket_value)}</dd></div>
              <div><dt className="text-xs text-slate-500">Created by</dt><dd className="font-mono text-xs text-slate-300">{packet.created_by}</dd></div>
            </dl>
          </Card>

          <Card title={`Basket · ${packet.items.length} items (buyers choose what and how many)`} bodyClassName="p-0">
            <table className="w-full text-left text-sm">
              <tbody className="divide-y divide-white/[0.05]">
                {packet.items.map(it => (
                  <tr key={it.id}>
                    <td className="px-4 py-2 text-slate-100">{it.display_name}</td>
                    <td className="px-4 py-2"><Tag>{it.match_type}</Tag> <span className="font-mono text-xs text-slate-400">{it.match_value}</span></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>

          <PacketOrders packetId={packet.id} />
        </div>
        <QrStudio packet={packet} navigate={navigate} />
      </div>
    </div>
  );
}

/* ─────────────────────────── scan simulator ──────────────────────────────── */

// The simulator runs the real buyer page (src/qr/QrBuyer.jsx) in a phone
// frame: every step is live (discover, select, init, confirm).
// "New scan" clears the checkout the page keeps in sessionStorage (shared
// with this same-origin frame) so the next load starts from the scan again.
const SIM_POLL_MS = 3000;


// BecknTrail lists one order's Beckn messages as the BAP logged them
// (OUTBOUND = this BAP sent it through onix-bap, INBOUND = the seller's on_*
// callback), refreshed while open.
function BecknTrail({ transactionId, demo }) {
  const [msgs, setMsgs] = useState(null);
  const [error, setError] = useState(null);
  useEffect(() => {
    if (demo) return undefined;
    let stop = false;
    const load = () => adminApi.becknMessages(transactionId).then(m => !stop && setMsgs(m)).catch(e => !stop && setError(e));
    load();
    const t = setInterval(() => { if (!document.hidden) load(); }, SIM_POLL_MS);
    return () => { stop = true; clearInterval(t); };
  }, [transactionId, demo]);

  if (demo) return <p className="px-4 pb-3 text-xs text-violet-200/80">Demo order: the BAP played the merchant, so no Beckn messages were sent.</p>;
  if (error) return <p className="px-4 pb-3 text-xs text-rose-300">{error.message}</p>;
  if (!msgs) return <div className="px-4 pb-3"><Spinner className="w-4 h-4 text-slate-500" /></div>;
  if (msgs.length === 0) return <p className="px-4 pb-3 text-xs text-slate-500">No Beckn messages logged yet.</p>;
  return (
    <ol className="space-y-1 px-4 pb-3">
      {msgs.map((m, i) => {
        const out = m.direction === 'OUTBOUND';
        return (
          <li key={i} className="flex items-center gap-2 font-mono text-[11px]">
            <span className="w-16 shrink-0 text-slate-500">{new Date(m.at).toLocaleTimeString()}</span>
            <span className={cx('w-4 shrink-0 text-center', out ? 'text-brand' : 'text-emerald-300')} title={out ? 'BAP → network (via onix-bap)' : 'network → BAP (callback)'}>{out ? '→' : '←'}</span>
            <span className="w-24 shrink-0 text-slate-200">{m.action}</span>
            {m.ack_status && (
              <span className={cx('rounded px-1.5 py-0.5 text-[10px] font-bold', m.ack_status === 'ACK' ? 'bg-emerald-500/15 text-emerald-300' : 'bg-rose-500/15 text-rose-300')}>{m.ack_status}</span>
            )}
            {m.duration_ms != null && <span className="text-slate-500">{m.duration_ms} ms</span>}
            {m.error && <span className="truncate text-rose-300/80" title={m.error}>{m.error}</span>}
          </li>
        );
      })}
    </ol>
  );
}

function Simulator({ id, navigate }) {
  const [fromPicker] = useState(() => window.history.state?.from === 'picker');
  const { data: packet, error } = useLoad(signal => adminApi.get(id, signal), [id]);
  const qr = useBlobUrl(signal => adminApi.qrPng(id, 512, signal), [id]);
  const [mode, setMode] = useState('direct'); // direct | redirect
  const [frameKey, setFrameKey] = useState(0);
  // '' = the real network; SINGLE | MULTI | QUOTE_CHANGED = demo merchants
  // played by the BAP (only when it has QR_DEMO_ENABLED). null until known.
  const [scenario, setScenario] = useState(null);
  const { data: settings } = useLoad(signal => qrApi.settings(signal), []);
  const demoEnabled = Boolean(settings?.demo_enabled);
  const activeScenario = scenario ?? (demoEnabled ? 'SINGLE' : '');
  const [since, setSince] = useState(() => new Date().toISOString());
  const [orders, setOrders] = useState(null);
  const [trail, setTrail] = useState(null); // transaction id whose Beckn messages are open

  // Live activity: this packet's orders, refreshed while the page is open.
  useEffect(() => {
    let stop = false;
    const load = () => adminApi.orders(id).then(o => !stop && setOrders(o)).catch(() => {});
    load();
    const t = setInterval(() => { if (!document.hidden) load(); }, SIM_POLL_MS);
    return () => { stop = true; clearInterval(t); };
  }, [id]);

  const newScan = () => {
    try {
      sessionStorage.removeItem(`qr.checkout.${id}`);
      sessionStorage.removeItem(`qr.scanKey.${id}`);
    } catch { /* storage blocked */ }
    setSince(new Date().toISOString());
    setFrameKey(k => k + 1);
  };

  if (error) return <ErrorNote error={error} title="Could not load packet" />;
  if (!packet) return <div className="flex justify-center py-16 text-slate-400"><Spinner className="w-6 h-6" /></div>;

  // A demo scenario rides on the page address, which the QR redirect can't
  // carry, so demos always open by direct link. sim= forces a fresh load.
  const useRedirect = mode === 'redirect' && !activeScenario;
  const src = useRedirect
    ? packet.deep_link_url
    : `${basePath}qr/${encodeURIComponent(id)}?sim=${frameKey}${activeScenario ? `&demo=${activeScenario}` : ''}`;
  const pickScenario = next => {
    try {
      sessionStorage.removeItem(`qr.checkout.${id}`);
      sessionStorage.removeItem(`qr.scanKey.${id}`);
    } catch { /* storage blocked */ }
    setScenario(next);
    if (next) setMode('direct');
    setSince(new Date().toISOString());
    setFrameKey(k => k + 1);
  };
  const scenarios = [
    ['', 'Real network', 'Discover, select, init and confirm go out over Beckn to real merchants.'],
    ['SINGLE', 'Demo: one merchant', 'Fast checkout: a single clear match is quoted straight away.'],
    ['MULTI', 'Demo: several merchants', 'Three shops nearby; the buyer picks one (nearest is recommended).'],
    ['QUOTE_CHANGED', 'Demo: price changes', 'At checkout the merchant raises a price; the buyer accepts the new total.'],
  ];
  const recent = (orders || []).slice(0, 8);
  const modes = [
    ['direct', 'Direct link', `${basePath}qr/${id}: recorded as a DIRECT_RESOLVE scan.`],
    ['redirect', 'Through the QR code', `${packet.deep_link_url}: exactly what the printed QR encodes, recorded as a REDIRECT scan.`],
  ];

  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div className="min-w-0">
          {fromPicker
            ? <Link to="/simulate" navigate={navigate} className="flex items-center gap-1 text-xs text-slate-400 hover:text-white"><ArrowLeft className="w-3 h-3" /> All packets to simulate</Link>
            : <Link to={`/${id}`} navigate={navigate} className="flex items-center gap-1 text-xs text-slate-400 hover:text-white"><ArrowLeft className="w-3 h-3" /> Back to packet</Link>}
          <h1 className="mt-1 text-2xl text-white">Scan simulator</h1>
          <p className="mt-1 flex flex-wrap items-center gap-2 text-sm text-slate-400"><StatusBadge status={packet.status} /> {packet.title} <Tag>{packet.id}</Tag></p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button onClick={newScan}><RotateCcw className="w-4 h-4" /> New scan</Button>
          <a href={src} target="_blank" rel="noreferrer"><Button variant="secondary"><ExternalLink className="w-4 h-4" /> Open in new tab</Button></a>
        </div>
      </header>

      {packet.status !== 'ACTIVE' && (
        <Alert tone="warning" title={`This packet is ${packet.status}`}>
          Scans of a packet that isn&apos;t active stop at the &quot;not taking orders&quot; screen. Validate it on the packet page first.
        </Alert>
      )}

      <div className="grid gap-6 lg:grid-cols-[auto_1fr]">
        {/* Phone */}
        <div className="mx-auto w-full max-w-[380px] lg:w-[380px]">
          {/* Slim bezel, no notch; shorter than a real phone so the
              whole flow fits beside the controls. */}
          <div className="rounded-[2.25rem] border-[5px] border-slate-800 bg-slate-950 p-0.5 shadow-2xl shadow-black/50">
            <iframe key={`${frameKey}-${useRedirect ? 'r' : 'd'}`} src={src} title="Buyer phone" className="block h-[600px] w-full rounded-[1.85rem] bg-[#080d1e]" />
          </div>
          <p className="mt-2 text-center text-xs text-slate-500">The real buyer page, as a phone sees it after scanning.</p>
        </div>

        {/* Controls + activity */}
        <div className="space-y-5">
          <Card title="Merchants">
            <div className="grid gap-2 sm:grid-cols-2">
              {scenarios.map(([key, title, detail]) => {
                const disabled = key !== '' && !demoEnabled;
                return (
                  <button key={key || 'real'} type="button" disabled={disabled} onClick={() => pickScenario(key)}
                    className={cx('rounded-xl border p-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-40',
                      activeScenario === key ? 'border-brand/60 bg-brand/10' : 'border-white/10 bg-white/[0.03] hover:bg-white/[0.06]')}>
                    <p className={cx('text-sm font-semibold', activeScenario === key ? 'text-brand' : 'text-white')}>{title}</p>
                    <p className="mt-0.5 text-xs text-slate-400">{detail}</p>
                  </button>
                );
              })}
            </div>
            <p className="mt-3 text-xs text-slate-500">
              {demoEnabled
                ? 'Demo merchants are played by this BAP: no Beckn calls, and nothing reaches a seller. Scan and order states are real.'
                : 'Demo merchants are off on this BAP (set QR_DEMO_ENABLED=true to turn them on).'}
            </p>
          </Card>

          <Card title="How the phone opens it">
            <div className="grid gap-2 sm:grid-cols-2">
              {modes.map(([key, title, detail]) => (
                <button key={key} type="button" disabled={key === 'redirect' && Boolean(activeScenario)}
                  onClick={() => { setMode(key); setSince(new Date().toISOString()); }}
                  className={cx('rounded-xl border p-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-40', (useRedirect ? 'redirect' : 'direct') === key ? 'border-brand/60 bg-brand/10' : 'border-white/10 bg-white/[0.03] hover:bg-white/[0.06]')}>
                  <p className={cx('text-sm font-semibold', (useRedirect ? 'redirect' : 'direct') === key ? 'text-brand' : 'text-white')}>{title}</p>
                  <p className="mt-0.5 break-all text-xs text-slate-400">{detail}</p>
                </button>
              ))}
            </div>
            {activeScenario && <p className="mt-3 text-xs text-slate-500">Demo scenarios open by direct link: the QR redirect can't carry the scenario.</p>}
            {useRedirect && (
              <p className="mt-3 text-xs text-amber-200/80">
                qr-service sends the phone to its TENANT_WEB_BASE_URL. It must point at this app ({window.location.origin}); otherwise the frame lands elsewhere.
              </p>
            )}
          </Card>

          <Card title="Live activity" actions={<span className="flex items-center gap-1.5 text-[11px] text-slate-500"><span className="pulse-dot h-1.5 w-1.5 rounded-full bg-emerald-400" /> live</span>} bodyClassName="p-0">
            {!orders ? <div className="flex justify-center py-6"><Spinner /></div>
              : recent.length === 0 ? <p className="p-4 text-sm text-slate-400">No checkouts yet. Go through the phone flow and each step shows up here.</p>
                : (
                  <ul className="divide-y divide-white/[0.05]">
                    {recent.map(o => {
                      const fresh = o.created_at >= since;
                      return (
                        <li key={o.transaction_id} className={cx(fresh && 'bg-brand/[0.04]')}>
                        <div className="flex items-center gap-3 px-4 py-3 text-sm">
                          {o.state === 'CONFIRMED'
                            ? <CheckCircle2 className="w-4 h-4 shrink-0 text-emerald-400" />
                            : <span className={cx('h-2 w-2 shrink-0 rounded-full', o.state === 'FAILED' ? 'bg-rose-400' : 'bg-amber-300')} />}
                          <div className="min-w-0 flex-1">
                            <p className="text-slate-100">
                              <span className={cx('font-semibold', ORDER_TONE[o.state])}>{o.state}</span>
                              <span className="text-slate-500"> · {o.provider_name}</span>
                              {o.demo_scenario && <span className="ml-2 rounded bg-violet-500/20 px-1.5 py-0.5 text-[10px] font-bold text-violet-200">DEMO</span>}
                              {fresh && <span className="ml-2 rounded bg-brand/15 px-1.5 py-0.5 text-[10px] font-bold text-brand">THIS SCAN</span>}
                            </p>
                            <p className="truncate text-xs text-slate-500">
                              {shortTime(o.created_at)} · Beckn {o.transaction_status || '—'}
                              {o.last_error && <span className="text-rose-300/80"> · {o.last_error}</span>}
                            </p>
                          </div>
                          <span className="text-slate-200">{rupiah(o.total)}</span>
                          <button type="button" onClick={() => setTrail(trail === o.transaction_id ? null : o.transaction_id)}
                            className={cx('shrink-0 rounded-lg border px-2 py-1 text-[11px] font-semibold transition-colors', trail === o.transaction_id ? 'border-brand/50 text-brand' : 'border-white/10 text-slate-400 hover:text-white')}>
                            Beckn messages
                          </button>
                        </div>
                        {trail === o.transaction_id && <BecknTrail transactionId={o.transaction_id} demo={Boolean(o.demo_scenario)} />}
                        </li>
                      );
                    })}
                  </ul>
                )}
          </Card>

          <Card title="Scan with a real phone">
            <div className="flex flex-wrap items-center gap-4">
              <div className="grid h-36 w-36 shrink-0 place-items-center rounded-xl bg-white">
                {qr.url ? <img src={qr.url} alt="Packet QR code" className="h-full w-full rounded-xl object-contain p-1.5" /> : <Spinner className="w-5 h-5 text-slate-400" />}
              </div>
              <p className="min-w-0 flex-1 text-xs text-slate-400">
                Encodes <span className="break-all font-mono text-slate-200">{packet.deep_link_url}</span>. A phone can open it only when that address is reachable from the phone: set PUBLIC_BASE_URL and TENANT_WEB_BASE_URL on qr-service to this computer&apos;s LAN address.
              </p>
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}

/* ─────────────────────────── printable poster ────────────────────────────── */

function Poster({ id, navigate }) {
  const { data: packet, error } = useLoad(signal => adminApi.get(id, signal), [id]);
  const qr = useBlobUrl(signal => adminApi.qrPng(id, 1024, signal), [id]);
  if (error) return <ErrorNote error={error} title="Could not load packet" />;
  if (!packet) return <div className="flex justify-center py-16"><Spinner className="w-6 h-6" /></div>;
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2 print:hidden">
        <Link to={`/${id}`} navigate={navigate} className="flex items-center gap-1 text-sm text-slate-400 hover:text-white"><ArrowLeft className="w-4 h-4" /> Back to packet</Link>
        <div className="flex items-center gap-3">
          {packet.status !== 'ACTIVE' && <span className="text-xs font-semibold text-amber-300">Packet is {packet.status}: scans won't reach checkout until it's active.</span>}
          <Button onClick={() => window.print()} disabled={!qr.url}><Printer className="w-4 h-4" /> Print</Button>
        </div>
      </div>
      <article className="mx-auto flex aspect-[210/297] w-full max-w-[640px] flex-col overflow-hidden rounded-2xl bg-white text-slate-900 shadow-xl print:max-w-none print:rounded-none print:shadow-none">
        <header className="bg-slate-950 px-8 py-6 text-white">
          <p className="text-xs font-bold tracking-[0.25em] text-cyan-300 uppercase">Scan · Pesan · Antar</p>
          <h1 className="mt-1 text-3xl leading-tight">{packet.title}</h1>
          {packet.format_type !== 'GENERAL' && <p className="mt-1 text-sm text-slate-300">{formatLabel(packet.format_type)}</p>}
        </header>
        <div className="flex flex-1 flex-col items-center justify-center gap-4 px-8 py-6">
          <div className="w-[62%] rounded-2xl bg-white p-3 ring-4 ring-slate-950">
            {qr.url ? <img src={qr.url} alt="QR code" className="w-full" /> : <div className="grid aspect-square place-items-center"><Spinner className="w-8 h-8 text-slate-400" /></div>}
          </div>
          <p className="text-center text-base font-extrabold">Pindai dengan kamera ponsel untuk memesan paket ini</p>
          <p className="-mt-3 text-center text-sm text-slate-600">Scan with your phone camera to order this bundle</p>
        </div>
        <section className="border-t-2 border-dashed border-slate-300 px-8 py-5">
          <h2 className="text-xs font-bold tracking-widest text-slate-500 uppercase" style={{ fontFamily: 'inherit' }}>Isi paket · In this bundle</h2>
          <ul className="mt-2 grid grid-cols-2 gap-x-6 gap-y-1 text-sm">
            {packet.items.map(it => (
              <li key={it.id} className="font-semibold">{it.display_name}</li>
            ))}
          </ul>
        </section>
        <footer className="flex items-center justify-between gap-4 bg-slate-100 px-8 py-3 text-xs text-slate-700">
          <span className="font-semibold">📍 {packet.location_label}</span><span className="font-mono text-slate-500">{packet.id}</span>
        </footer>
      </article>
    </div>
  );
}
