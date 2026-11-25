// Packet enums and form rules, mirrored from ion-qr-code's qr-service
// (internal/packetsvc/types.go, db/migrations/001_init_qr_schema.sql). Keep
// in step with it.

// The kind of spot: an optional label (poster, lists) that changes no
// behaviour. GENERAL is qr-service's default.
export const FORMAT_TYPES = [
  { value: 'GENERAL', label: 'General' },
  { value: 'KOS_KOSAN', label: 'Kos / Kosan' },
  { value: 'CAFE_TO_HOME', label: 'Cafe to Home' },
  { value: 'APARTMENT_LOBBY', label: 'Apartment Lobby' },
  { value: 'WARUNG_B2B', label: 'Warung B2B' },
  { value: 'CATALOG_WALL', label: 'Catalog Wall' },
];

export const STATUSES = ['DRAFT', 'ACTIVE', 'PAUSED', 'RETIRED'];

export const formatLabel = v => FORMAT_TYPES.find(f => f.value === v)?.label || v;

// Quick-fill locations (Depok / South Jakarta).
export const LOCATION_PRESETS = [
  { name: 'Kos Melati (Depok)', label: 'Kos Melati, Jl. Margonda Raya Gg. Melati No. 12, Depok', lat: -6.3625, lon: 106.8295 },
  { name: 'Kos Anggrek (Kukusan, Depok)', label: 'Kos Anggrek, Jl. Kukusan Raya No. 8, Beji, Depok', lat: -6.3689, lon: 106.8197 },
  { name: 'Apartemen Margonda Residence', label: 'Margonda Residence 3 Lobby, Jl. Margonda Raya No. 520, Depok', lat: -6.3698, lon: 106.8327 },
  { name: 'Kantin UI (Depok)', label: 'Kantin Pusgiwa, Universitas Indonesia, Depok', lat: -6.3606, lon: 106.8272 },
  { name: 'Warung Kebagusan (Jakarta)', label: 'Warung Bu Sri, Jl. Kebagusan Raya No. 30, Jakarta Selatan', lat: -6.3083, lon: 106.8264 },
];

/** GS1 check-digit validation, as qr-service's packetsvc.validGTIN. */
export function validGTIN(s) {
  if (![8, 12, 13, 14].includes(s.length) || !/^\d+$/.test(s)) return false;
  let sum = 0;
  for (let i = s.length - 2; i >= 0; i--) {
    let d = Number(s[i]);
    if ((s.length - 2 - i) % 2 === 0) d *= 3;
    sum += d;
  }
  return (10 - (sum % 10)) % 10 === Number(s[s.length - 1]);
}

/** Client-side mirror of qr-service's rules: {formKey: message}. */
export function validatePacketForm(f) {
  const e = {};
  if (!f.title.trim()) e.title = 'Title is required';
  else if (f.title.length > 120) e.title = 'Max 120 characters';
  if (!f.location_label.trim()) e.location_label = 'Location label is required';
  else if (f.location_label.length > 300) e.location_label = 'Max 300 characters';
  const lat = Number(f.lat);
  if (f.lat === '' || Number.isNaN(lat) || lat < -90 || lat > 90) e.lat = 'Latitude between -90 and 90';
  const lon = Number(f.lon);
  if (f.lon === '' || Number.isNaN(lon) || lon < -180 || lon > 180) e.lon = 'Longitude between -180 and 180';
  const r = Number(f.radius_meters);
  if (!Number.isInteger(r) || r < 100 || r > 25000) e.radius_meters = 'Whole meters, 100 to 25000';
  const mb = Number(f.min_basket_value);
  if (Number.isNaN(mb) || mb < 0) e.min_basket_value = 'Must be 0 or more';
  else if (Math.abs(mb * 100 - Math.round(mb * 100)) > 1e-6) e.min_basket_value = 'At most 2 decimals';
  if (f.items.length === 0) e.items = 'Add at least one item';
  if (f.items.length > 50) e.items = 'At most 50 items';
  const seen = new Set();
  f.items.forEach((it, i) => {
    const v = it.match_value.trim();
    if (!v) e[`items.${i}.match_value`] = 'Required';
    else if (it.match_type === 'GTIN' && !validGTIN(v)) e[`items.${i}.match_value`] = 'Invalid GTIN check digit / length';
    const key = `${it.match_type}|${v}`;
    if (v && seen.has(key)) e[`items.${i}.match_value`] = 'Duplicate item';
    seen.add(key);
    if (!it.display_name.trim()) e[`items.${i}.display_name`] = 'Required';
  });
  return e;
}

// Go struct field names in qr-service validation errors → form keys.
const GO_FIELD_TO_FORM = {
  Title: 'title', FormatType: 'format_type', LocationLabel: 'location_label',
  Lat: 'lat', Lon: 'lon', RadiusMeters: 'radius_meters', MinBasketValue: 'min_basket_value',
  Items: 'items', MatchType: 'items', MatchValue: 'items', DisplayName: 'items', SortOrder: 'items',
};

/** Maps qr-service error messages onto form keys. */
export function serverFieldErrors(err) {
  const out = {};
  for (const m of err?.messages || []) {
    if (!m.field) continue;
    let key = GO_FIELD_TO_FORM[m.field] || m.field;
    const idx = /^items\[(\d+)\]\.(\w+)$/.exec(m.field);
    if (idx) key = `items.${idx[1]}.${idx[2]}`;
    out[key] = `${m.errcode}${m.vals?.length ? ` (${m.vals.join(' ')})` : ''}`;
  }
  return out;
}

// rupiah formats an amount in IDR, the only currency QR orders are in.
export const rupiah = n =>
  n == null ? '—' : new Intl.NumberFormat('id-ID', { style: 'currency', currency: 'IDR', maximumFractionDigits: 0 }).format(n);

export const formatDistance = m => (m == null ? null : m < 1000 ? `${Math.round(m)} m` : `${(m / 1000).toFixed(1)} km`);

export const shortTime = iso => (iso ? new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }) : '—');

// Prefill presets for the packet form's Details card; locations
// use LOCATION_PRESETS above.
export const PACKET_PRESETS = [
  { name: 'Kos night pack', values: { title: 'Kos Melati - Paket Malam', format_type: 'KOS_KOSAN', min_basket_value: 15000, allow_node_as_delivery: true } },
  { name: 'Apartment lobby', values: { title: 'Margonda Residence - Paket Lobby', format_type: 'APARTMENT_LOBBY', min_basket_value: 25000, allow_node_as_delivery: true } },
  { name: 'Cafe to home', values: { title: 'Kopi Senja - Camilan ke Rumah', format_type: 'CAFE_TO_HOME', min_basket_value: 20000, allow_node_as_delivery: true } },
  { name: 'Warung weekly restock (B2B)', values: { title: 'Warung Bu Sri - Restock Mingguan', format_type: 'WARUNG_B2B', min_basket_value: 100000, allow_node_as_delivery: false } },
  { name: 'Campus catalog wall', values: { title: 'Kantin UI - Dinding Katalog', format_type: 'CATALOG_WALL', min_basket_value: 0, allow_node_as_delivery: false } },
];
