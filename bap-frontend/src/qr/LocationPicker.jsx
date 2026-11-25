import { useCallback, useEffect, useRef, useState } from 'react';
import L from 'leaflet';
import 'leaflet/dist/leaflet.css';
import { Crosshair, Info, MapPin, Search, X } from 'lucide-react';
import { Spinner } from './ui.jsx';
import { cx, inputClass } from './styles.js';

// LocationPicker sets a packet's spot without typing coordinates: search an
// address (Photon, OpenStreetMap data), click the map, drag the pin or use the
// device location. Every pin move refills the address from the map; it stays
// editable, so a landmark can be added after placing the pin.

const PHOTON = 'https://photon.komoot.io';
// Searches stay inside Indonesia (minLon,minLat,maxLon,maxLat).
const INDONESIA_BBOX = '94.9,-11.1,141.1,6.1';
const DEFAULT_VIEW = { lat: -6.3625, lon: 106.8295, zoom: 12 }; // Depok
const PICK_ZOOM = 17;
const SEARCH_DEBOUNCE_MS = 350;

// OpenStreetMap's own tiles: free, no API key (fair-use policy:
// https://operations.osmfoundation.org/policies/tiles/). For heavy production
// traffic, point this at a paid or self-hosted tile server.
const TILES = 'https://tile.openstreetmap.org/{z}/{x}/{y}.png';
// The map data licence (ODbL) requires a visible credit; it is kept as a
// small "i" badge in the map corner that expands on hover or tap.
const CREDIT_URL = 'https://www.openstreetmap.org/copyright';

const PIN = L.divIcon({
  className: '',
  iconSize: [30, 40],
  iconAnchor: [15, 39],
  html: `<svg width="30" height="40" viewBox="0 0 30 40" xmlns="http://www.w3.org/2000/svg" style="filter:drop-shadow(0 3px 4px rgba(0,0,0,.45))">
    <path d="M15 1C7.3 1 1 7.2 1 14.9 1 25.3 15 39 15 39s14-13.7 14-24.1C29 7.2 22.7 1 15 1z" fill="#06b6d4" stroke="#fff" stroke-width="2"/>
    <circle cx="15" cy="15" r="5.2" fill="#fff"/></svg>`,
});

// describe turns a Photon feature into a title, a detail line and an address.
function describe(p) {
  const street = [p.street, p.housenumber].filter(Boolean).join(' ');
  const parts = [p.name, street, p.district || p.locality, p.city || p.county, p.state, p.postcode]
    .filter(Boolean)
    .filter((v, i, a) => a.indexOf(v) === i);
  const [title = 'Unnamed place', ...rest] = parts;
  // The address drops the province and postcode: the city is enough for a rider.
  const address = parts.filter(v => v !== p.state && v !== p.postcode).join(', ');
  return { title, detail: rest.join(', '), address };
}

async function photon(path, params, signal) {
  const res = await fetch(`${PHOTON}${path}?${new URLSearchParams(params)}`, { signal });
  if (!res.ok) throw new Error(`Location search failed (${res.status})`);
  return (await res.json()).features || [];
}

const validPoint = (lat, lon) => lat !== '' && lon !== '' && Number.isFinite(Number(lat)) && Number.isFinite(Number(lon))
  && Math.abs(Number(lat)) <= 90 && Math.abs(Number(lon)) <= 180;
const round = n => Number(n.toFixed(6));

// aside renders next to the address (on wide screens), e.g. the radius control.
export default function LocationPicker({ label, lat, lon, radius, onChange, labelError, coordError, aside }) {
  const mapEl = useRef(null);
  const mapRef = useRef(null);
  const markerRef = useRef(null);
  const circleRef = useRef(null);
  const reverseCtrl = useRef(null);
  const latest = useRef({ label, radius, onChange });
  latest.current = { label, radius, onChange };

  const [q, setQ] = useState('');
  const [search, setSearch] = useState({ loading: false, error: null, results: null });
  const [active, setActive] = useState(-1);
  const [open, setOpen] = useState(false);
  const [locating, setLocating] = useState(false);
  const [notice, setNotice] = useState(null);
  const [resolving, setResolving] = useState(false);
  const has = validPoint(lat, lon);

  // fillAddress replaces the address with the one at the new pin position.
  const fillAddress = useCallback(async (la, lo) => {
    const before = latest.current.label;
    reverseCtrl.current?.abort();
    const ctrl = new AbortController();
    reverseCtrl.current = ctrl;
    setResolving(true);
    try {
      const [f] = await photon('/reverse', { lat: la, lon: lo, limit: 1 }, ctrl.signal);
      if (ctrl.signal.aborted || !f) return;
      // Typing while the lookup ran wins over the looked-up address.
      if (latest.current.label !== before) return;
      latest.current.onChange({ location_label: describe(f.properties).address });
    } catch { /* the address stays as it is; the user can type one */ } finally {
      if (!ctrl.signal.aborted) setResolving(false);
    }
  }, []);

  // placePin moves the pin to a point chosen on the map (click, drag, GPS).
  const placePin = useCallback((la, lo) => {
    latest.current.onChange({ lat: String(round(la)), lon: String(round(lo)) });
    fillAddress(la, lo);
  }, [fillAddress]);

  // Map setup, once.
  useEffect(() => {
    const start = validPoint(lat, lon) ? { lat: Number(lat), lon: Number(lon), zoom: 15 } : DEFAULT_VIEW;
    // The map credit is the small "i" badge below, not Leaflet's text bar.
    const map = L.map(mapEl.current, { zoomControl: true, attributionControl: false }).setView([start.lat, start.lon], start.zoom);
    L.tileLayer(TILES, { maxZoom: 19 }).addTo(map);
    map.on('click', e => placePin(e.latlng.lat, e.latlng.lng));
    mapRef.current = map;
    // The map sits in a grid that may still be sizing itself.
    const ro = new ResizeObserver(() => map.invalidateSize());
    ro.observe(mapEl.current);
    return () => { ro.disconnect(); map.remove(); mapRef.current = null; markerRef.current = null; circleRef.current = null; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Pin and radius circle follow the form's values.
  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;
    if (!has) {
      markerRef.current?.remove(); markerRef.current = null;
      circleRef.current?.remove(); circleRef.current = null;
      return;
    }
    const pos = L.latLng(Number(lat), Number(lon));
    if (!markerRef.current) {
      markerRef.current = L.marker(pos, { icon: PIN, draggable: true, autoPan: true, keyboard: false }).addTo(map);
      markerRef.current.on('dragend', e => { const p = e.target.getLatLng(); placePin(p.lat, p.lng); });
      circleRef.current = L.circle(pos, { radius: latest.current.radius, color: '#22d3ee', weight: 1.5, fillColor: '#22d3ee', fillOpacity: 0.08, interactive: false }).addTo(map);
    } else if (!markerRef.current.getLatLng().equals(pos)) {
      markerRef.current.setLatLng(pos);
      circleRef.current.setLatLng(pos);
    }
    if (!map.getBounds().pad(-0.1).contains(pos)) map.panTo(pos);
  }, [has, lat, lon, placePin]);

  // Changing the radius shows the whole circle.
  useEffect(() => {
    const circle = circleRef.current;
    if (!circle) return;
    circle.setRadius(radius);
    const t = setTimeout(() => mapRef.current?.fitBounds(circle.getBounds(), { padding: [16, 16], maxZoom: PICK_ZOOM }), 250);
    return () => clearTimeout(t);
  }, [radius]);

  // Address search, as you type.
  useEffect(() => {
    const query = q.trim();
    if (query.length < 3) { setSearch({ loading: false, error: null, results: null }); return; }
    const ctrl = new AbortController();
    const t = setTimeout(async () => {
      setSearch(s => ({ ...s, loading: true, error: null }));
      try {
        const c = mapRef.current?.getCenter();
        const params = { q: query, limit: 6, bbox: INDONESIA_BBOX };
        if (c) Object.assign(params, { lat: c.lat.toFixed(4), lon: c.lng.toFixed(4) });
        const features = await photon('/api/', params, ctrl.signal);
        setSearch({ loading: false, error: null, results: features });
        setActive(features.length ? 0 : -1);
      } catch (err) {
        if (err.name !== 'AbortError') setSearch({ loading: false, error: err, results: null });
      }
    }, SEARCH_DEBOUNCE_MS);
    return () => { clearTimeout(t); ctrl.abort(); };
  }, [q]);

  const pickResult = f => {
    const [lo, la] = f.geometry.coordinates;
    const { address } = describe(f.properties);
    reverseCtrl.current?.abort();
    setResolving(false);
    onChange({ lat: String(round(la)), lon: String(round(lo)), location_label: address });
    mapRef.current?.setView([la, lo], PICK_ZOOM);
    setQ(''); setOpen(false); setNotice(null);
  };

  const useMyLocation = () => {
    if (!navigator.geolocation) { setNotice('This browser cannot share its location.'); return; }
    setLocating(true); setNotice(null);
    navigator.geolocation.getCurrentPosition(
      ({ coords }) => {
        setLocating(false);
        mapRef.current?.setView([coords.latitude, coords.longitude], PICK_ZOOM);
        placePin(coords.latitude, coords.longitude);
      },
      err => {
        setLocating(false);
        setNotice(err.code === err.PERMISSION_DENIED ? 'Location access was blocked. Search or click the map instead.' : 'Could not get your location.');
      },
      { enableHighAccuracy: true, timeout: 10000 },
    );
  };

  const onKey = e => {
    const n = search.results?.length || 0;
    if (e.key === 'ArrowDown' && n) { e.preventDefault(); setOpen(true); setActive(a => (a + 1) % n); }
    else if (e.key === 'ArrowUp' && n) { e.preventDefault(); setActive(a => (a - 1 + n) % n); }
    else if (e.key === 'Enter') { e.preventDefault(); if (open && n && active >= 0) pickResult(search.results[active]); }
    else if (e.key === 'Escape') { if (open) setOpen(false); else setQ(''); }
  };

  const showList = open && q.trim().length >= 3 && (search.results || search.error);

  return (
    <div className="space-y-3">
      {/* search */}
      <div className="relative z-20">
        <Search className="pointer-events-none absolute left-3.5 top-1/2 w-4 h-4 -translate-y-1/2 text-slate-500" />
        <input className={cx(inputClass(false), 'pl-10 pr-10')} value={q} onKeyDown={onKey}
          onChange={e => { setQ(e.target.value); setOpen(true); }}
          onFocus={() => setOpen(true)} onBlur={() => setTimeout(() => setOpen(false), 150)}
          placeholder="Search a place or street" aria-label="Search location"
          role="combobox" aria-expanded={Boolean(showList)} aria-controls="loc-results" autoComplete="off" />
        <span className="absolute right-3 top-1/2 -translate-y-1/2">
          {search.loading ? <Spinner className="w-4 h-4 text-brand" />
            : q && <button type="button" onClick={() => setQ('')} className="grid h-6 w-6 place-items-center rounded-full text-slate-400 hover:bg-white/10 hover:text-white" aria-label="Clear search"><X className="w-3.5 h-3.5" /></button>}
        </span>
        {showList && (
          <ul id="loc-results" role="listbox" className="absolute inset-x-0 top-full mt-1 max-h-72 overflow-y-auto rounded-xl border border-white/10 bg-slate-900 py-1 shadow-2xl">
            {search.error && <li className="px-3 py-2 text-sm text-rose-300">Search is unavailable right now. Click the map to drop the pin.</li>}
            {search.results?.length === 0 && <li className="px-3 py-2 text-sm text-slate-400">No places match &quot;{q.trim()}&quot;.</li>}
            {search.results?.map((f, i) => {
              const d = describe(f.properties);
              return (
                <li key={`${f.properties.osm_type}${f.properties.osm_id}-${i}`} role="option" aria-selected={i === active}
                  onMouseDown={e => { e.preventDefault(); pickResult(f); }} onMouseEnter={() => setActive(i)}
                  className={cx('flex cursor-pointer items-start gap-2.5 px-3 py-2', i === active && 'bg-white/[0.06]')}>
                  <MapPin className="mt-0.5 w-4 h-4 shrink-0 text-brand" />
                  <span className="min-w-0">
                    <span className="block truncate text-sm font-semibold text-white">{d.title}</span>
                    {d.detail && <span className="block truncate text-xs text-slate-400">{d.detail}</span>}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </div>

      {/* map; isolate keeps Leaflet's panes under the page header */}
      <div className={cx('relative isolate overflow-hidden rounded-xl border', coordError ? 'border-rose-400/50' : 'border-white/10')}>
        <div ref={mapEl} className="h-56 w-full bg-slate-800 sm:h-64" aria-label="Map: click to place the pin" />
        <button type="button" onClick={useMyLocation} disabled={locating} title="Use my location" aria-label="Use my location"
          className="absolute right-2.5 top-2.5 z-[1000] grid h-8 w-8 place-items-center rounded-md border border-black/20 bg-white text-slate-700 shadow hover:bg-slate-100 disabled:opacity-60">
          {locating ? <Spinner className="w-4 h-4" /> : <Crosshair className="w-4 h-4" />}
        </button>
        <a href={CREDIT_URL} target="_blank" rel="noreferrer" title="Map data © OpenStreetMap"
          className="group absolute bottom-1.5 right-1.5 z-[1000] flex h-5 items-center gap-1 rounded-full bg-white/80 px-1.5 text-[10px] text-slate-600 shadow-sm hover:bg-white focus:bg-white">
          <Info className="w-3 h-3" />
          <span className="hidden whitespace-nowrap group-hover:inline group-focus:inline">© OpenStreetMap</span>
        </a>
        {!has && (
          <p className="pointer-events-none absolute inset-x-0 bottom-6 z-[1000] mx-auto w-fit rounded-full bg-slate-900/85 px-3 py-1.5 text-xs font-semibold text-white shadow">
            Search above or click the map to drop the pin
          </p>
        )}
      </div>
      {notice && <p className="text-xs font-semibold text-amber-300">{notice}</p>}
      {coordError && <p className="text-xs font-semibold text-rose-300">Pick the spot: search above or click the map.</p>}

      <div className={cx('grid gap-4 pt-1', aside && 'sm:grid-cols-2')}>
        {/* address */}
        <div>
          <div className="mb-1 flex items-center justify-between">
            <label htmlFor="loc" className="text-xs font-semibold text-slate-400">Address</label>
            {resolving && <span className="flex items-center gap-1 text-[11px] text-slate-500"><Spinner className="w-3 h-3" /> Finding address…</span>}
          </div>
          <textarea id="loc" rows={2} className={cx(inputClass(labelError), 'resize-none')} value={label}
            onChange={e => onChange({ location_label: e.target.value })} placeholder="Filled in from the map" />
          {labelError && <p className="mt-1 text-xs font-semibold text-rose-300">{labelError}</p>}
        </div>
        {aside}
      </div>

    </div>
  );
}
