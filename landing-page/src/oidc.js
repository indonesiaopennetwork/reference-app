// Minimal OAuth2 Authorization Code + PKCE client for Zitadel. No library --
// this is ~80 lines of standard, well-documented browser-native crypto
// (SubtleCrypto) and fetch calls, not worth a dependency for.
import { config } from './config.js'

const REDIRECT_URI = `${window.location.origin}/callback`
const STORAGE_TOKEN_KEY = 'landing.accessToken'
const STORAGE_EXPIRES_KEY = 'landing.accessTokenExpiresAt'
const STORAGE_IDTOKEN_KEY = 'landing.idToken'
const SESSION_VERIFIER_KEY = 'landing.pkce.verifier'
const SESSION_STATE_KEY = 'landing.pkce.state'

function randomString(length) {
  const bytes = new Uint8Array(length)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('').slice(0, length)
}

function base64url(buffer) {
  return btoa(String.fromCharCode(...new Uint8Array(buffer)))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')
}

async function codeChallengeFor(verifier) {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))
  return base64url(digest)
}

// The language the user chose in BAP / BPP (they send ?lang=en|id when
// redirecting here), remembered so Zitadel's login page is shown in it.
// Without one, Zitadel follows the browser's language.
const LANG_KEY = 'landing.lang'
function loginLocale() {
  const fromUrl = new URLSearchParams(window.location.search).get('lang')
  if (fromUrl === 'en' || fromUrl === 'id') {
    localStorage.setItem(LANG_KEY, fromUrl)
    return fromUrl
  }
  const stored = localStorage.getItem(LANG_KEY)
  return stored === 'en' || stored === 'id' ? stored : null
}

// Redirects the browser to Zitadel's hosted login/register page. Called
// whenever this app has no valid session -- see App.jsx.
export async function startLogin() {
  const verifier = randomString(64)
  const state = randomString(32)
  sessionStorage.setItem(SESSION_VERIFIER_KEY, verifier)
  sessionStorage.setItem(SESSION_STATE_KEY, state)

  const challenge = await codeChallengeFor(verifier)
  const params = new URLSearchParams({
    response_type: 'code',
    client_id: config.zitadelClientId,
    redirect_uri: REDIRECT_URI,
    scope: 'openid profile email',
    code_challenge: challenge,
    code_challenge_method: 'S256',
    state,
  })
  const locale = loginLocale()
  if (locale) params.set('ui_locales', locale)
  window.location.href = `${config.zitadelIssuer}/oauth/v2/authorize?${params}`
}

// Call on the /callback route. Exchanges the authorization code for tokens
// and stores the access token. Throws on any mismatch/failure -- the
// caller sends the user back through startLogin() rather than trying to
// recover a broken callback.
export async function handleCallback() {
  const url = new URL(window.location.href)
  const code = url.searchParams.get('code')
  const state = url.searchParams.get('state')
  const error = url.searchParams.get('error')
  if (error) throw new Error(`Zitadel returned an error: ${error}`)

  const expectedState = sessionStorage.getItem(SESSION_STATE_KEY)
  const verifier = sessionStorage.getItem(SESSION_VERIFIER_KEY)
  if (!code || !state || !verifier || state !== expectedState) {
    throw new Error('invalid OAuth callback (missing/mismatched state)')
  }
  sessionStorage.removeItem(SESSION_STATE_KEY)
  sessionStorage.removeItem(SESSION_VERIFIER_KEY)

  const body = new URLSearchParams({
    grant_type: 'authorization_code',
    code,
    redirect_uri: REDIRECT_URI,
    client_id: config.zitadelClientId,
    code_verifier: verifier,
  })
  const res = await fetch(`${config.zitadelIssuer}/oauth/v2/token`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body,
  })
  if (!res.ok) throw new Error(`token exchange failed: ${res.status}`)
  const data = await res.json()
  storeToken(data.access_token, data.expires_in, data.id_token)
}

function storeToken(accessToken, expiresInSeconds, idToken) {
  localStorage.setItem(STORAGE_TOKEN_KEY, accessToken)
  localStorage.setItem(STORAGE_EXPIRES_KEY, String(Date.now() + expiresInSeconds * 1000))
  if (idToken) localStorage.setItem(STORAGE_IDTOKEN_KEY, idToken)
}

export function clearToken() {
  localStorage.removeItem(STORAGE_TOKEN_KEY)
  localStorage.removeItem(STORAGE_EXPIRES_KEY)
  localStorage.removeItem(STORAGE_IDTOKEN_KEY)
}

// Ends the Zitadel SSO session via RP-initiated logout (not just this app's
// local token) so a later login actually re-prompts for credentials instead
// of silently re-authenticating off the browser's Zitadel session cookie.
export function logout() {
  const idToken = localStorage.getItem(STORAGE_IDTOKEN_KEY)
  clearToken()

  let done = false
  function endSession() {
    if (done) return
    done = true
    const params = new URLSearchParams({
      client_id: config.zitadelClientId,
      post_logout_redirect_uri: window.location.origin,
    })
    if (idToken) params.set('id_token_hint', idToken)
    window.location.href = `${config.zitadelIssuer}/oidc/v1/end_session?${params}`
  }

  // bap-frontend and bpp-frontend each keep their own copy of the access
  // token in their own origin's localStorage -- clear both before ending
  // the Zitadel session, otherwise a still-valid token there lets someone
  // straight back into the buyer/seller app without logging in again.
  let pending = 2
  function onIframeSettled() {
    pending -= 1
    if (pending <= 0) endSession()
  }
  const bapIframe = document.createElement('iframe')
  bapIframe.style.display = 'none'
  bapIframe.src = `${config.bapUrl}/logout`
  bapIframe.onload = onIframeSettled
  document.body.appendChild(bapIframe)
  const bppIframe = document.createElement('iframe')
  bppIframe.style.display = 'none'
  bppIframe.src = `${config.bppUrl}/logout`
  bppIframe.onload = onIframeSettled
  document.body.appendChild(bppIframe)
  setTimeout(endSession, 1500) // fallback if bap-frontend/bpp-frontend are unreachable
}

// Returns the stored access token, or null if missing/expired. A 60s
// safety margin avoids using a token that expires mid-request.
export function getValidToken() {
  const token = localStorage.getItem(STORAGE_TOKEN_KEY)
  const expiresAt = Number(localStorage.getItem(STORAGE_EXPIRES_KEY) || 0)
  if (!token || Date.now() > expiresAt - 60_000) return null
  return token
}
