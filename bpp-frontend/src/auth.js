// Consumes the access token landing-page hands off via a URL param (see
// landing-page/src/App.jsx's "Enter as Seller" link) and stores it for
// this app's own use. There's no login UI here at all -- landing-page is
// the single entry point; a visit with no token, ever, redirects there.
const TOKEN_KEY = 'bpp.accessToken'
const ID_TOKEN_KEY = 'bpp.idToken'

// Path this app is mounted at, with a trailing slash: '/' when served at a
// domain root, '/tsp/remiges/pn/' behind ion-landing-page's proxy. Derived
// from where the bundle itself was loaded from (<base>assets/index-*.js in a
// build, /src/auth.js under `npm run dev`), so no per-mount config needed.
export const basePath = new URL('..', import.meta.url).pathname

// Reads ?token= from the URL (if present), stores it, and strips it from
// the URL bar via history.replaceState so it doesn't linger in the
// address bar or browser history.
export function captureTokenFromUrl() {
  const params = new URLSearchParams(window.location.search)
  const token = params.get('token')
  const idToken = params.get('id_token')
  if (!token && !idToken) return
  if (token) {
    localStorage.setItem(TOKEN_KEY, token)
    params.delete('token')
  }
  if (idToken) {
    localStorage.setItem(ID_TOKEN_KEY, idToken)
    params.delete('id_token')
  }
  const query = params.toString()
  const newUrl = window.location.pathname + (query ? `?${query}` : '') + window.location.hash
  window.history.replaceState({}, '', newUrl)
}

export function getToken() {
  return localStorage.getItem(TOKEN_KEY)
}

// Used only to decode profile claims (name/email) for UserProfileMenu --
// never sent anywhere, never used for authorization (that's getToken()'s job).
export function getIdToken() {
  return localStorage.getItem(ID_TOKEN_KEY)
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(ID_TOKEN_KEY)
}

export function redirectToLogin() {
  const url = window.__env__?.LANDING_PAGE_URL || import.meta.env.VITE_LANDING_PAGE_URL || 'http://localhost:3002'
  // Pass on the chosen language (see i18n.jsx) so the login page matches it.
  const lang = localStorage.getItem('lang')
  window.location.href = lang ? `${url}?lang=${encodeURIComponent(lang)}` : url
}

// This app never talks to Zitadel directly (it only decodes the id_token
// locally for display, in UserProfileMenu.jsx -- see getIdToken() above), so
// ending the SSO session has to happen on landing-page -- it owns the OIDC
// client and is the one that actually calls Zitadel's end_session endpoint.
// See landing-page/src/App.jsx's "/logout" route.
export function logout() {
  clearToken()
  const base = window.__env__?.LANDING_PAGE_URL || import.meta.env.VITE_LANDING_PAGE_URL || 'http://localhost:3002'
  window.location.href = `${base}/logout`
}
