// Runtime config, injected via /env-config.js at container start (see
// env.sh) so the same built bundle works locally and in the cloud without
// rebuilding. window.__env__ is undefined under `npm run dev` (no
// entrypoint script runs) -- the defaults below are this project's actual
// local docker-compose ports/values, matching every other frontend's
// dev-fallback convention in this repo.
const env = typeof window !== 'undefined' ? window.__env__ || {} : {}

export const config = {
  bapUrl: env.BAP_URL || 'http://localhost:3000',
  bppUrl: env.BPP_URL || 'http://localhost:3001',
  bppApiUrl: env.BPP_API_URL || 'http://localhost:8080',
  zitadelIssuer: env.ZITADEL_ISSUER || 'http://localhost:8090',
  zitadelClientId: env.ZITADEL_CLIENT_ID || '',
}

// Path this app is mounted at, with a trailing slash: '/' when served at a
// domain root, '/tsp/remiges/' behind ion-landing-page's proxy. Derived from
// where the bundle itself was loaded from (<base>assets/index-*.js in a
// build, /src/config.js under `npm run dev`), so no per-mount config needed.
// Resolved through a variable on purpose: Vite rewrites the literal
// `new URL('<path>', import.meta.url)` form as a static asset reference,
// and under `npm run dev` that turned '..' into '/@fs/<abs path to this
// app>' (no trailing slash), so every API call went to a broken
// '/@fs/.../<app>api/v1/...' path. Via a variable it stays a plain runtime
// resolution -- the same result a production build gets.
const moduleUrl = import.meta.url
export const basePath = new URL('..', moduleUrl).pathname
