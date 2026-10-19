// Decodes a JWT's payload for display purposes only -- no signature
// verification, since this is never used for a trust/security decision
// (the token was already validated by Zitadel and by bpp-application's own
// backend verification on every real API call). Used by UserProfileMenu.jsx
// to read the id_token's name/email claims.
export function decodeJwtPayload(token) {
  try {
    const payload = token.split('.')[1]
    const binary = atob(payload.replaceAll('-', '+').replaceAll('_', '/'))
    const bytes = Uint8Array.from(binary, (c) => c.codePointAt(0))
    // fatal: malformed UTF-8 throws (and so returns null), as before.
    const json = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
    return JSON.parse(json)
  } catch {
    return null
  }
}
