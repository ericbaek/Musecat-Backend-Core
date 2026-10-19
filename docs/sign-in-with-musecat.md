# Sign in with Musecat

Musecat supports an opt-in OpenID Connect Authorization Code flow for registered confidential
server applications. This is a deliberately constrained profile, not an OIDC certification claim.
It follows [OIDC Core](https://openid.net/specs/openid-connect-core-1_0.html) and
[Discovery](https://openid.net/specs/openid-connect-discovery-1_0.html).

## Enable an application

In Developer Center, enable **Sign in with Musecat** (`direction_a.login_enabled: true`), register
exact HTTPS callback URLs and submit for operator review. After approval, generate the client
secret once and keep it on the partner server. Existing approved forms remain live until a change
is approved. Enabling login changes the client's allowed scopes/fingerprint and requires existing
users to reconnect; a form with this flag absent/false keeps its previous registration unchanged.
The deployment must configure persistent signing keys before OpenID requests can succeed.

Use `scope=openid profile arcade:read`, random session-bound `state`, random session-bound `nonce`
(16–512 non-whitespace characters), and PKCE S256. After the user's explicit consent, one callback
provides login authentication plus read permissions. `openid` alone reveals only an application-
specific subject. `profile` enables basic profile claims, and `arcade:read` enables public arcade
queries. Neither scope grants write, management or account-security access. Broader general-user
API delegation uses an explicitly requested `account:access` scope.

## Protocol

- Discovery: `GET {issuer}/.well-known/openid-configuration`.
- Authorization: discovered endpoint with `response_type=code`, `response_mode=query` (optional),
  `client_id`, exact `redirect_uri`, `scope`, `state`, `nonce`, `code_challenge`,
  `code_challenge_method=S256`. One-use request lasts five minutes; code lasts 90 seconds.
- Token: `POST {issuer}/oauth/token`, form-encoded authorization-code grant, HTTP Basic client
  authentication with separately URL-form-encoded ID/secret. The code is bound to client, callback,
  verifier, user session and the original nonce.
- OpenID code exchanges add a signed RS256 `id_token`. It contains `iss`, app-specific stable `sub`,
  `aud` (client ID), `iat`, `exp` (at most ten minutes), and the original `nonce`; header `kid`
  selects a public RSA key from `GET {issuer}/oauth/jwks`. Private keys never leave the server.
- Verify signature/RS256, exact issuer/audience, expiry, issuance time and the session's original
  nonce before creating your own session. Reject duplicated callback parameters; verify state and
  callback issuer before exchanging. The ID token cannot be used as a Musecat API bearer token.
- `GET` or empty `POST {issuer}/oauth/userinfo` with the opaque bearer access token returns `sub`.
  With `profile`, it also returns `preferred_username` and `nickname`. Verify its `sub` equals the
  ID token's `sub`. No email or global user ID is returned; do not merge accounts by display name.
- `GET {issuer}/developer/v1/arcades`, `/developer/v1/arcade`, `/developer/v1/arcades/nearby` use
  the same access token and `arcade:read`, preserving public/private visibility checks.
- `GET {issuer}/developer/v1/me` returns the basic profile under `profile` or legacy `profile:read`.
- Refresh tokens retain the existing rotating 30-day family. Refresh responses do not issue a new
  ID token or expand scopes. Access/revoke/withdrawal restrictions apply to UserInfo and APIs.
  A partner's own login session is its responsibility; no front/back-channel single logout is claimed.

Unsupported in this profile: browser-only/public clients, implicit/hybrid flow, dynamic client
registration, email/offline_access scopes, prompt/max_age/auth_time, request objects, encrypted
ID tokens and arbitrary claims requests. Unknown authorization parameters are rejected explicitly.
Clients must select the authorization-code profile and configure the scopes/nonce above.

## Running partner example

`examples/musecat-signin/server.mjs` and `client.mjs` require Node.js with native fetch. No npm
dependencies are needed. Put the example behind a public HTTPS reverse proxy, register
`https://PARTNER_HOST/callback`, then set `MUSECAT_OAUTH_ISSUER`, `MUSECAT_CLIENT_ID`,
`MUSECAT_CLIENT_SECRET` and `PARTNER_WEB_ORIGIN=https://PARTNER_HOST`. Run:

```sh
node examples/musecat-signin/server.mjs
```

It listens on 127.0.0.1:8080 (override `PORT`). Open `/` for the sign-in button. The callback
validates the ID token and matching UserInfo, creates an opaque Secure/HttpOnly/SameSite=Lax
session cookie, and offers `/arcades` with the server-held access token. Attempt state is one-use.
Tokens and credentials never enter browser storage or logs. The demo uses bounded in-memory
sessions, expires them by token expiry and does not implement silent refresh; use an encrypted
shared session store and your own account/session lifecycle in production.

Run `node --test examples/musecat-signin/client.test.mjs` for ID-token verification regressions.

## Musecat deployment keys

Configure `MUSECAT_OIDC_SIGNING_KEYS` as a JSON array of base64-encoded PKCS8 DER RSA private keys
(at least 2048 bits). The first key signs; all keys publish only their public components through
JWKS. Generate into a protected file for your deployment secret manager, for example:

```sh
umask 077
node -e 'const {generateKeyPairSync}=require("node:crypto");const {privateKey}=generateKeyPairSync("rsa",{modulusLength:2048});process.stdout.write(JSON.stringify([privateKey.export({type:"pkcs8",format:"der"}).toString("base64")]));' > oidc-signing-keys.json
```

Keep identical keys on every instance and preserve them across restarts. Rotate by prepending a
new key, keep the old key published until all ten-minute ID tokens and cached JWKS have expired,
then remove the old key. Invalid configuration fails startup without disclosing key data. Missing
keys leave existing OAuth operational while OpenID discovery/issuance returns 503. Full owns the
guarded hidden-nonce migration `1792030010_oidc_login.go`; Core bootstraps fresh databases only.
