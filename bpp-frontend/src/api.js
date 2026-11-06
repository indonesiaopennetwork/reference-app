import { getToken, clearToken, redirectToLogin, basePath } from './auth.js'

export const API_BASE = `${basePath}api/v1`

// apiFetch calls the dashboard API with the seller's bearer token and returns
// the JSON body. A failed call throws an Error whose message is the response
// body; when the body is the API's JSON error ({error, code}) the error also
// carries status, code ('NOT_FOUND', 'INVALID', 'SEND_FAILED', ...) and
// detail (the error text).
export async function apiFetch(path, opts = {}) {
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
  if (!res.ok) {
    const b = await res.text()
    const err = new Error(b || res.statusText)
    err.status = res.status
    try {
      const j = JSON.parse(b)
      if (j && typeof j === 'object') { err.code = j.code; err.detail = j.error }
    } catch { /* not JSON */ }
    throw err
  }
  return res.json()
}
