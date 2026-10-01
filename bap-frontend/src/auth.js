// Consumes the access token landing-page hands off via a URL param (see
// landing-page/src/App.jsx's "Enter as Buyer" link) and stores it for
// this app's own use. There's no login UI here at all -- landing-page is
// the single entry point; a visit with no token, ever, redirects there.
const TOKEN_KEY = 'bap.accessToken'
const PROVIDER_ID_KEY = 'bap.providerId'

// Path this app is mounted at, with a trailing slash: '/' when served at a
// domain root, '/tsp/remiges/cn/' behind ion-landing-page's proxy. Derived
// from where the bundle itself was loaded from (<base>assets/index-*.js in a
// build, /src/auth.js under `npm run dev`), so no per-mount config needed.
export const basePath = new URL('..', import.meta.url).pathname

// Reads ?token= from the URL (if present), stores it, and strips it from
// the URL bar via history.replaceState so it doesn't linger in the
// address bar or browser history.
export function captureTokenFromUrl() {
  const params = new URLSearchParams(window.location.search)
  const token = params.get('token')
  const providerId = params.get('providerId')
  if (token) {
    localStorage.setItem(TOKEN_KEY, token)
    params.delete('token')
  }
  if (providerId) {
    localStorage.setItem(PROVIDER_ID_KEY, providerId)
    params.delete('providerId')
  }
  const query = params.toString()
  const newUrl = window.location.pathname + (query ? `?${query}` : '') + window.location.hash
  window.history.replaceState({}, '', newUrl)
}

export function getToken() {
  return localStorage.getItem(TOKEN_KEY)
}

export function getProviderId() {
  return localStorage.getItem(PROVIDER_ID_KEY)
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(PROVIDER_ID_KEY)
}

export function redirectToLogin() {
  const url = window.__env__?.LANDING_PAGE_URL || import.meta.env.VITE_LANDING_PAGE_URL || 'http://localhost:3002'
  // Pass on the chosen language (see i18n.jsx) so the login page matches it.
  const lang = localStorage.getItem('lang')
  window.location.href = lang ? `${url}?lang=${encodeURIComponent(lang)}` : url
}

// This app never talks to Zitadel directly (no id_token), so ending the SSO
// session has to happen on landing-page -- it owns the OIDC client and its
// own stored id_token. See landing-page/src/App.jsx's "/logout" route.
export function logout() {
  clearToken()
  const base = window.__env__?.LANDING_PAGE_URL || import.meta.env.VITE_LANDING_PAGE_URL || 'http://localhost:3002'
  window.location.href = `${base}/logout`
}
