import test from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign } from 'node:crypto';
import { validateIDToken } from './client.mjs';
const { privateKey, publicKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });
const key = { ...publicKey.export({ format: 'jwk' }), kid: 'active', alg: 'RS256', use: 'sig' };
const expected = { issuer: 'https://musecat.example', clientId: 'partner', nonce: 'session-nonce', now: 1000 };
const claims = { iss: expected.issuer, aud: expected.clientId, sub: 'pairwise-subject', nonce: expected.nonce, iat: 990, exp: 1500 };
function token(claims, header = { alg: 'RS256', kid: 'active' }) {
 const body = [header, claims].map((v) => Buffer.from(JSON.stringify(v)).toString('base64url')).join('.');
 return `${body}.${sign('RSA-SHA256', Buffer.from(body), privateKey).toString('base64url')}`;
}
test('accepts signed issuer, audience, subject and session nonce', () => assert.equal(validateIDToken(token(claims), { keys: [key] }, expected).sub, 'pairwise-subject'));
test('rejects substituted, expired and session-unbound ID tokens', () => {
 for (const change of [{ iss: 'https://other.example' }, { aud: 'other' }, { nonce: 'other' }, { exp: 900 }, { sub: '' }, { iat: 2000 }]) {
  assert.throws(() => validateIDToken(token({ ...claims, ...change }), { keys: [key] }, expected));
 }
 assert.throws(() => validateIDToken(token(claims, { alg: 'none', kid: 'active' }), { keys: [key] }, expected));
 assert.throws(() => validateIDToken(`${token(claims)}changed`, { keys: [key] }, expected));
 assert.throws(() => validateIDToken(token(claims), { keys: [] }, expected));
});
