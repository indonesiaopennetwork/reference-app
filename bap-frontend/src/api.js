import { useCallback, useEffect, useState } from 'react';
import { getToken, clearToken, redirectToLogin, basePath } from './auth.js';
import { useLive } from './live.js';

// BPP target, network ID, and BAP identity are all server-side config (BAP .env).
// The frontend only needs the API base path.
export const API_BASE = `${basePath}api/v1`;

// Attaches the buyer's bearer token (if any) to every API call. Auth is a
// frontend-only gate here (see App()'s authReady check) -- the backend
// doesn't hard-reject a missing/invalid token, so a 401 should never
// happen in normal use, but we still recover the same way bpp-frontend
// does if it ever does.
export async function apiFetch(path, opts = {}) {
  const token = getToken();
  const res = await fetch(`${API_BASE}${path}`, {
    ...opts,
    headers: {
      ...opts.headers,
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
  });
  if (res.status === 401) {
    clearToken();
    redirectToLogin();
  }
  return res;
}

export const fmtIDR = (amount, cur) => {
  if (amount == null) return '—';
  try { return new Intl.NumberFormat('id-ID', { style: 'currency', currency: cur || 'IDR', maximumFractionDigits: 0 }).format(amount); }
  catch { return `${cur || 'IDR'} ${amount}`; }
};

// ApiError carries the backend's error code: NOT_FOUND, INVALID,
// SEND_FAILED (not delivered to the seller, nothing recorded -- retry), or
// NETWORK when the request never got an answer.
export class ApiError extends Error {
  constructor(message, code, status) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

// apiJSON calls the API with an optional JSON body and returns the parsed
// response, or throws an ApiError.
export async function apiJSON(path, { method = 'GET', body } = {}) {
  let r;
  try {
    r = await apiFetch(path, {
      method,
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch (e) {
    throw new ApiError(e.message, 'NETWORK', 0);
  }
  const d = await r.json().catch(() => ({}));
  if (!r.ok) {
    const code = d.code || (r.status === 404 ? 'NOT_FOUND' : r.status === 400 ? 'INVALID' : r.status >= 500 ? 'SEND_FAILED' : 'ERROR');
    throw new ApiError(d.error || `HTTP ${r.status}`, code, r.status);
  }
  return d;
}

// useGrievances lists the buyer's grievances (all, or one order's) and keeps
// the list live while active. needsAction counts those waiting on the buyer.
export function useGrievances(txnId = '', active = true) {
  const [state, setState] = useState({ loading: true, error: null, items: [], needsAction: 0 });
  const load = useCallback(async () => {
    try {
      const q = txnId ? `?transaction_id=${encodeURIComponent(txnId)}` : '';
      const d = await apiJSON(`/grievances${q}`);
      setState({ loading: false, error: null, items: d.items || [], needsAction: d.needsAction || 0 });
    } catch (e) {
      setState(s => ({ ...s, loading: false, error: e }));
    }
  }, [txnId]);
  useEffect(() => { if (active) void load(); }, [active, load]);
  useLive(() => { void load(); }, active);
  return { ...state, reload: load };
}

// useNow is the current time, updated every minute (for "6 days left").
export function useNow(every = 60000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(timer);
  }, [every]);
  return now;
}
