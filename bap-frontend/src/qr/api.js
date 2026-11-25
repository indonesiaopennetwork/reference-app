// API clients for the QR pages.
//
//   qrApi    — the buyer checkout (/api/v1/qr/*). No login needed: every call
//              after resolve carries the guest token resolve returned. A
//              logged-in buyer's token is sent too, so the order also shows
//              in their shop order history.
//   adminApi — the packet console. bap-application forwards
//              /api/v1/qr/admin/packets/* to qr-service; it needs a login.

import { basePath, clearToken, getToken } from '../auth.js';

const BASE = `${basePath}api/v1/qr`;

export class QrError extends Error {
  constructor(status, code, message, data) {
    super(message || code || `HTTP ${status}`);
    this.status = status;
    this.code = code;
    this.data = data ?? null;
  }
}

async function readJSON(res) {
  const text = await res.text();
  if (!text) return null;
  try { return JSON.parse(text); } catch { return { error: text.slice(0, 200) }; }
}

// call is one buyer API request. A stale login token makes the backend answer
// 401 even though no login is needed here, so it is dropped and the call
// retried as a guest.
async function call(path, { method = 'GET', body, guestToken, signal } = {}, retried = false) {
  const headers = { Accept: 'application/json' };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (guestToken) headers['X-QR-Guest-Token'] = guestToken;
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let res;
  try {
    res = await fetch(`${BASE}${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal });
  } catch (e) {
    if (e.name === 'AbortError') throw e;
    throw new QrError(0, 'NETWORK', 'No connection. Check your internet and try again.');
  }
  if (res.status === 401 && token && !retried) {
    clearToken();
    return call(path, { method, body, guestToken, signal }, true);
  }
  const data = await readJSON(res);
  if (!res.ok) throw new QrError(res.status, data?.code, data?.error, data?.data);
  return data;
}

export const qrApi = {
  settings: signal => call('/settings', { signal }),
  packet: (id, signal) => call(`/packets/${encodeURIComponent(id)}`, { signal }),
  resolve: (body, signal) => call('/resolve', { method: 'POST', body, signal }),
  // items: [{ packet_item_id, quantity }], what the buyer chose.
  quote: (candidateId, items, guestToken) => call('/quote', { method: 'POST', body: { candidate_id: candidateId, items }, guestToken }),
  checkout: (body, guestToken) => call('/checkout', { method: 'POST', body, guestToken }),
  // Beckn confirm at the total the buyer agreed to.
  placeOrder: (transactionId, expectedAmount, guestToken) =>
    call(`/orders/${encodeURIComponent(transactionId)}/place`, { method: 'POST', body: { expected_amount: expectedAmount }, guestToken }),
  order: (transactionId, guestToken) => call(`/orders/${encodeURIComponent(transactionId)}`, { guestToken }),
  // After confirm: Beckn status / cancel / rate / support, sent by the BAP.
  refreshStatus: (transactionId, guestToken) => call(`/orders/${encodeURIComponent(transactionId)}/status`, { method: 'POST', guestToken }),
  cancel: (transactionId, reasonCode, guestToken) => call(`/orders/${encodeURIComponent(transactionId)}/cancel`, { method: 'POST', body: { reason_code: reasonCode }, guestToken }),
  rate: (transactionId, rating, feedback, guestToken) => call(`/orders/${encodeURIComponent(transactionId)}/rate`, { method: 'POST', body: { rating, feedback }, guestToken }),
  support: (transactionId, description, guestToken) => call(`/orders/${encodeURIComponent(transactionId)}/support`, { method: 'POST', body: { description }, guestToken }),
};

// ---------------------------------------------------------------------------
// Packet console (qr-service admin API through bap-application)
// ---------------------------------------------------------------------------

// qr-service answers in the alya envelope:
//   {"status": "success"|"error", "data": ..., "messages": [{errcode, field, vals}]}
// bap-application's own errors (login, proxy) are {error, code}.
export class AdminError extends Error {
  constructor(status, messages = [], data = null) {
    const first = messages[0];
    super(first ? describeMessage(first) : `HTTP ${status}`);
    this.status = status;
    this.messages = messages;
    this.data = data;
  }
  has(errcode) { return this.messages.some(m => m.errcode === errcode); }
}

const ERRCODE_TEXT = {
  PACKET_NOT_FOUND: 'Packet not found',
  VERSION_CONFLICT: 'Someone else changed this packet. Reload and try again.',
  PACKET_RETIRED: 'This packet is retired and can no longer change.',
  PACKET_ACTIVE: 'This packet is live. Pause it before deleting.',
  INVALID_STATUS_TRANSITION: 'That status change is not allowed from the current status.',
  COVERAGE_NOT_CONFIGURED: 'qr-service has no buyer app configured for coverage checks.',
  COVERAGE_UNAVAILABLE: 'The coverage check is unreachable or timed out.',
  COVERAGE_INCOMPLETE: 'No merchant in range stocks every item.',
  RATE_LIMITED: 'Rate limited. Wait a minute and retry.',
  UNAUTHORIZED: 'qr-service rejected the admin credentials (QR_ADMIN_TOKEN).',
  FORBIDDEN: 'Your login lacks qr-service\'s ion_admin role.',
  LOGIN_REQUIRED: 'Log in to manage QR packets.',
  QR_NOT_CONFIGURED: 'QR packets are not configured on this BAP (QR_SERVICE_URL).',
  QR_SERVICE_UNAVAILABLE: 'qr-service is unreachable. Is it running?',
  invalid_json: 'Request body is not valid JSON.',
  database_error: 'Server error. Check the qr-service logs.',
};

export function describeMessage(m) {
  const base = ERRCODE_TEXT[m.errcode] || m.errcode;
  const field = m.field ? ` [${m.field}]` : '';
  const vals = m.vals?.length ? `: ${m.vals.join(' ')}` : '';
  return `${base}${field}${vals}`;
}

function adminHeaders(extra = {}) {
  const token = getToken();
  return { Accept: 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...extra };
}

async function adminCall(path, { method = 'GET', body, signal } = {}) {
  const headers = adminHeaders(body !== undefined ? { 'Content-Type': 'application/json' } : {});
  let res;
  try {
    res = await fetch(`${BASE}/admin/${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify({ data: body }), signal });
  } catch (e) {
    if (e.name === 'AbortError') throw e;
    throw new AdminError(0, [{ errcode: 'NETWORK', vals: ['bap-application unreachable'] }]);
  }
  const data = await readJSON(res);
  if (data && data.code && !data.status) throw new AdminError(res.status, [{ errcode: data.code, vals: [] }]);
  if (!res.ok || data?.status === 'error') throw new AdminError(res.status, data?.messages || [], data?.data ?? null);
  return data?.data;
}

async function adminBlob(path, signal) {
  const res = await fetch(`${BASE}/admin/${path}`, { headers: adminHeaders(), signal });
  if (!res.ok) {
    const data = await readJSON(res);
    throw new AdminError(res.status, data?.messages || (data?.code ? [{ errcode: data.code }] : []));
  }
  return res.blob();
}

export const adminApi = {
  list: ({ status, page = 1, limit = 25 } = {}, signal) => {
    const q = new URLSearchParams({ page: String(page), limit: String(limit) });
    if (status) q.set('status', status);
    return adminCall(`packets?${q}`, { signal });
  },
  get: (id, signal) => adminCall(`packets/${encodeURIComponent(id)}`, { signal }),
  create: input => adminCall('packets', { method: 'POST', body: input }),
  update: (id, version, input) => adminCall(`packets/${encodeURIComponent(id)}`, { method: 'PUT', body: { ...input, version } }),
  remove: (id, version) => adminCall(`packets/${encodeURIComponent(id)}?version=${version}`, { method: 'DELETE' }),
  transition: (id, action, version) => adminCall(`packets/${encodeURIComponent(id)}/${action}`, { method: 'POST', body: { version } }),
  qrPng: (id, size, signal) => adminBlob(`packets/${encodeURIComponent(id)}/qr.png?size=${size}`, signal),
  qrSvg: (id, signal) => adminBlob(`packets/${encodeURIComponent(id)}/qr.svg`, signal),
  // Product search for a packet's basket (a Beckn discover run by the BAP).
  products: async ({ q, lat, lon, radius_meters }, signal) => {
    const qs = new URLSearchParams({ q });
    if (lat != null && lon != null) { qs.set('lat', String(lat)); qs.set('lon', String(lon)); qs.set('radius_meters', String(radius_meters || 3000)); }
    const res = await fetch(`${BASE}/products?${qs}`, { headers: adminHeaders(), signal });
    const data = await readJSON(res);
    if (!res.ok) throw new AdminError(res.status, [{ errcode: data?.code || `HTTP ${res.status}`, vals: data?.error ? [data.error] : [] }]);
    return data;
  },
  // The Beckn messages the BAP logged for one order (console only).
  becknMessages: async (transactionId, signal) => {
    const res = await fetch(`${BASE}/orders/${encodeURIComponent(transactionId)}/beckn`, { headers: adminHeaders(), signal });
    const data = await readJSON(res);
    if (!res.ok) throw new AdminError(res.status, [{ errcode: data?.code || `HTTP ${res.status}`, vals: [] }]);
    return data;
  },
  orders: async (id, signal) => {
    const res = await fetch(`${BASE}/packet-orders/${encodeURIComponent(id)}`, { headers: adminHeaders(), signal });
    const data = await readJSON(res);
    if (!res.ok) throw new AdminError(res.status, [{ errcode: data?.code || `HTTP ${res.status}`, vals: [] }]);
    return data;
  },
};
