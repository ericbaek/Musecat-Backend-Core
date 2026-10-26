// Dependency-free Node.js example. Use a shared encrypted session store in production.
import { createServer } from 'node:http';
import { randomBytes, createHash } from 'node:crypto';
import { jsonRequest, validateIDToken } from './client.mjs';

const issuer = process.env.MUSECAT_OAUTH_ISSUER;
const clientId = process.env.MUSECAT_CLIENT_ID;
const clientSecret = process.env.MUSECAT_CLIENT_SECRET;
const origin = process.env.PARTNER_WEB_ORIGIN;
for (const value of [issuer, origin]) {
  const url = new URL(value);
  if (url.protocol !== 'https:' || url.origin !== value || url.username || url.password) throw new Error('Set HTTPS issuer and partner origins');
}
if (!clientId || !clientSecret) throw new Error('Set client credentials');
const metadata = await jsonRequest(`${issuer}/.well-known/openid-configuration`);
if (metadata.issuer !== issuer) throw new Error('Issuer mismatch');
for (const key of ['authorization_endpoint', 'token_endpoint', 'userinfo_endpoint', 'jwks_uri']) {
  if (new URL(metadata[key]).origin !== issuer) throw new Error('Unexpected endpoint origin');
}
const callback = `${origin}/callback`;
const random = () => randomBytes(32).toString('base64url');
const sessions = new Map();
const cookieName = '__Host-musecat-demo';
const cookie = (id, seconds) => `${cookieName}=${id}; Path=/; Max-Age=${seconds}; Secure; HttpOnly; SameSite=Lax`;
const escape = (value) => String(value).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const formEncode = (value) => new URLSearchParams({ v: value }).toString().slice(2);
createServer(async (req, res) => {
  res.setHeader('Cache-Control', 'no-store');
  res.setHeader('Referrer-Policy', 'no-referrer');
  res.setHeader('X-Frame-Options', 'DENY');
  res.setHeader('Content-Security-Policy', "default-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'");
  const redirect = (target) => { res.writeHead(302, { Location: target }); res.end(); };
  try {
    const url = new URL(req.url, origin);
    if (req.method !== 'GET') { res.writeHead(405); res.end(); return; }
    for (const [id, row] of sessions) if (row.expires < Date.now()) sessions.delete(id);
    const cookies = (req.headers.cookie || '').split(';').map((part) => part.trim()).filter((part) => part.startsWith(`${cookieName}=`));
    const id = cookies.length === 1 ? cookies[0].slice(cookieName.length + 1) : '';
    const session = sessions.get(id);
    if (url.pathname === '/login') {
      if (sessions.size >= 1000) { res.writeHead(503); res.end('Try later'); return; }
      const id = random(), state = random(), nonce = random(), verifier = random();
      sessions.set(id, { state, nonce, verifier, expires: Date.now() + 300000 });
      res.setHeader('Set-Cookie', cookie(id, 300));
      const target = new URL(metadata.authorization_endpoint);
      target.search = new URLSearchParams({ response_type: 'code', client_id: clientId, redirect_uri: callback, scope: 'openid profile arcade:read', state, nonce, code_challenge_method: 'S256', code_challenge: createHash('sha256').update(verifier).digest('base64url') }).toString();
      redirect(target.href); return;
    }
    if (url.pathname === '/callback') {
      sessions.delete(id); // Attempt state is one-use, including failures.
      if (!session?.state || [...url.searchParams.keys()].some((key) => url.searchParams.getAll(key).length !== 1) || url.searchParams.get('state') !== session.state || url.searchParams.get('iss') !== issuer || url.searchParams.has('error') || !url.searchParams.get('code')) throw new Error('Invalid callback');
      const token = await jsonRequest(metadata.token_endpoint, { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded', Authorization: `Basic ${Buffer.from(`${formEncode(clientId)}:${formEncode(clientSecret)}`).toString('base64')}` }, body: new URLSearchParams({ grant_type: 'authorization_code', code: url.searchParams.get('code'), redirect_uri: callback, code_verifier: session.verifier }) });
      if (!Number.isInteger(token.expires_in) || token.expires_in < 1 || token.expires_in > 600) throw new Error('Invalid token lifetime');
      const claims = validateIDToken(token.id_token, await jsonRequest(metadata.jwks_uri), { issuer, clientId, nonce: session.nonce });
      const profile = await jsonRequest(metadata.userinfo_endpoint, { headers: { Authorization: `Bearer ${token.access_token}` } });
      if (profile.sub !== claims.sub || token.token_type.toLowerCase() !== 'bearer' || !token.scope.split(' ').includes('arcade:read')) throw new Error('UserInfo or scope mismatch');
      // Identify accounts by (issuer, sub). Never merge using email or display name.
      const loggedIn = random();
      sessions.set(loggedIn, { profile, accessToken: token.access_token, expires: Math.min(claims.exp * 1000, Date.now() + token.expires_in * 1000) });
      res.setHeader('Set-Cookie', cookie(loggedIn, Math.max(0, Math.floor((sessions.get(loggedIn).expires - Date.now()) / 1000))));
      redirect('/'); return;
    }
    if (url.pathname === '/arcades' && session?.profile) {
      const arcades = await jsonRequest(`${issuer}/developer/v1/arcades`, { headers: { Authorization: `Bearer ${session.accessToken}` } });
      res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(arcades)); return;
    }
    if (url.pathname !== '/') { res.writeHead(404); res.end(); return; }
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.end(session?.profile ? `<h1>Signed in as ${escape(session.profile.nickname || session.profile.preferred_username || session.profile.sub)}</h1><a href="/arcades">View public arcades</a>` : '<h1>Partner sign-in example</h1><a href="/login">뮤즈캣으로 로그인 · Sign in with Musecat</a>');
  } catch {
    // Do not log callback URLs, tokens, secrets or raw remote response bodies.
    res.writeHead(400); res.end('Sign-in failed. Start again at /.');
  }
}).listen(Number(process.env.PORT || 8080), '127.0.0.1');
