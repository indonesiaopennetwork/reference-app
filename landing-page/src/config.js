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
