// Decodes a JWT's payload for display purposes only -- no signature
// verification, since this is never used for a trust/security decision
// (the token was already validated by Zitadel and by bap-application's own
// backend verification on every real API call). Used by UserProfileMenu.jsx
// to read the id_token's name/email claims.
export function decodeJwtPayload(token) {
  try {
    const payload = token.split('.')[1]
    // base64url -> bytes -> UTF-8 text. fatal: invalid UTF-8 throws (and so
    // returns null), as decodeURIComponent did before.
    const bin = atob(payload.replaceAll('-', '+').replaceAll('_', '/'))
    const bytes = Uint8Array.from(bin, c => c.charCodeAt(0))
    return JSON.parse(new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes))
  } catch {
    return null
  }
}
