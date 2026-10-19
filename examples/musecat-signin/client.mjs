import { createPublicKey, verify } from 'node:crypto';

export function validateIDToken(token, jwks, { issuer, clientId, nonce, now = Math.floor(Date.now() / 1000) }) {
  const parts = token.split('.');
  if (parts.length !== 3) throw new Error('Invalid ID token');
  const header = JSON.parse(Buffer.from(parts[0], 'base64url'));
  const claims = JSON.parse(Buffer.from(parts[1], 'base64url'));
  const keys = jwks.keys.filter((k) => k.kid === header.kid && k.kty === 'RSA' && k.alg === 'RS256' && k.use === 'sig');
  if (header.alg !== 'RS256' || header.crit || keys.length !== 1) throw new Error('Invalid signing key');
  const publicKey = createPublicKey({ key: keys[0], format: 'jwk' });
  if (!verify('RSA-SHA256', Buffer.from(`${parts[0]}.${parts[1]}`), publicKey, Buffer.from(parts[2], 'base64url'))) throw new Error('Invalid signature');
  if (claims.iss !== issuer || claims.aud !== clientId || claims.nonce !== nonce || !nonce || typeof claims.sub !== 'string' || !claims.sub || !Number.isInteger(claims.exp) || claims.exp <= now || !Number.isInteger(claims.iat) || claims.iat > now + 30 || claims.exp - claims.iat > 600) throw new Error('Invalid claims');
  return claims;
}
export async function jsonRequest(url, options = {}) {
  const response = await fetch(url, { ...options, redirect: 'error', signal: AbortSignal.timeout(10000) });
  if (!response.ok) throw new Error('Musecat request failed');
  const reader = response.body.getReader();
  const chunks = []; let size = 0;
  while (true) {
    const { done, value } = await reader.read(); if (done) break;
    size += value.byteLength;
    if (size > 1024 * 1024) { await reader.cancel(); throw new Error('Response too large'); }
    chunks.push(Buffer.from(value));
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}
