// Decodes a JWT's payload for display purposes only -- no signature
// verification, since this is never used for a trust/security decision
// (the token was already validated by Zitadel and by bap-application's own
// backend verification on every real API call). Used by UserProfileMenu.jsx
// to read the id_token's name/email claims.
export function decodeJwtPayload(token) {
  try {
    const payload = token.split('.')[1]
    const json = atob(payload.replace(/-/g, '+').replace(/_/g, '/'))
    return JSON.parse(decodeURIComponent(escape(json)))
  } catch {
    return null
  }
}
