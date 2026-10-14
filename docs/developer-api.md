# Musecat Developer API

Connect a user's Musecat account to your service and access only the permissions they approve.
Musecat is the OAuth authorization server. Your server is the OAuth client. This is independent
of importing external game records into Musecat. The current release supports registered
confidential server applications using Authorization Code with PKCE S256 and HTTP Basic client
authentication. It is OAuth API delegation, not an OpenID Connect login or ID-token service.

## Register your application

Ask the Musecat operator to register your application's immutable ID, display name, description,
exact HTTPS callback URLs and permitted scopes. Receive the client ID and a random client secret
of at least 32 characters through a private channel. Keep the secret, access tokens and refresh
tokens on your server. Your browser should hold only your own application's session cookie.
There is no public dynamic registration, password grant, implicit grant or browser-only client.
No partner is hardcoded; CHUNITHM Info, NosLog and rhythm.info are independent registrations.
A change to a client's registered configuration invalidates existing grants until reapproval.
Registration epochs are persisted at startup; disabling a client and later restoring the same
configuration cannot revive its previous tokens. Deploy the same registry on all instances.
Do not copy grants from a predecessor client when services merge into rhythm.info.

## Authorization flow

1. Generate an unpredictable `state` bound to the current user session in your application.
   Generate a 43–128 character PKCE verifier and its base64url(SHA-256(verifier)) challenge.
2. Redirect the browser to the metadata's authorization endpoint with `response_type=code`,
   `client_id`, the exact registered `redirect_uri`, space-separated `scope`, `state`,
   `code_challenge`, and `code_challenge_method=S256`.
3. Musecat validates the application and redirect before creating a five-minute request.
   It redirects to the configured Musecat consent page with an opaque `request` handle.
4. The Musecat consent application signs the user in. Its server calls `GET /oauth/consent`
   with that handle and the user's Musecat session token. This returns the registered app
   identity and requested permission descriptions, and binds the attempt to that user/session.
   The UI must render those server-returned permissions, require an explicit decision,
   protect its mutation with same-origin CSRF checks, prevent framing and send no referrers.
   The consent frontend is a separate implementation; configure its real URL before enabling
   OAuth clients. This backend release does not supply that page.
5. The consent application posts `{request, approve, scopes}` to `/oauth/consent` with the same
   session token. Approved scopes must be a nonempty subset of the request. Denial leaves the
   existing grant unchanged. Approval replaces that app's consent and invalidates its old tokens.
   The response's `redirect_uri` is constructed exclusively from the registered callback.
   Navigate there. Never accept a callback address supplied by the consent page or end user.
6. Your callback verifies the exact expected `state` and `iss` before exchanging the code.
   Codes expire after 90 seconds, are bound to the client, redirect URI and PKCE challenge,
   and can be redeemed once. A denied request returns `error=access_denied` and `state`.
7. Your server exchanges the code at `/oauth/token`. Use the returned opaque access token
   exclusively on `/developer/v1/*`. It is not a Musecat/PocketBase login session token.

Example browser redirect (encode each query value):

```text
GET /oauth/authorize?response_type=code&client_id=example_partner&redirect_uri=https%3A%2F%2Fpartner.example%2Foauth%2Fmusecat%2Fcallback&scope=profile%3Aread%20arcade%3Aread&state=YOUR_RANDOM_SESSION_STATE&code_challenge=YOUR_S256_CHALLENGE&code_challenge_method=S256
```

The token response has `access_token`, `refresh_token`, `token_type: "Bearer"`, `expires_in`
and a space-separated `scope` reflecting only the permissions actually approved.

```sh
curl -u "$MUSECAT_CLIENT_ID:$MUSECAT_CLIENT_SECRET" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'grant_type=authorization_code' \
  --data-urlencode "code=$AUTHORIZATION_CODE" \
  --data-urlencode "redirect_uri=$REGISTERED_CALLBACK" \
  --data-urlencode "code_verifier=$PKCE_VERIFIER" \
  "$MUSECAT_OAUTH_ISSUER/oauth/token"
```

Use URL-form encoding for both HTTP Basic credentials before base64 encoding per OAuth 2.0.
The example assumes URL-safe client IDs/secrets. Reject duplicated callback parameters and
never log authorization codes, the verifier, client secrets or tokens.

## Permissions

| Scope | Permission and endpoint |
| --- | --- |
| `profile:read` | `GET /developer/v1/me`: app-specific stable `sub`, username, nickname; no email or staff roles |
| `arcade:read` | Public arcade list, public detail and nearby discovery |
| `arcade:report:create` | Create a fault report using the user's normal arcade write permissions |
| `arcade:report:react` | Add/remove a report reaction using the user's normal permissions |
| `campaign:read` | Read campaigns, their detail and arcade campaign status |
| `campaign:respond` | Submit a campaign response; location, eligibility, rate and XP rules still apply |

Approval cannot grant powers the user does not possess. Every resource request verifies the
live account, ban/withdrawal state, current client configuration, grant, token expiry and scope.
The API reuses the existing domain handlers; approving a scope never bypasses venue visibility,
ownership, GPS or game validation. Normal operation responses are returned for writes.
There is no arbitrary proxy to internal APIs, raw collection access or moderation scope.
Existing in-flight requests can finish after revocation; new requests fail immediately.

```sh
curl -H "Authorization: Bearer $MUSECAT_ACCESS_TOKEN" \
  "$MUSECAT_OAUTH_ISSUER/developer/v1/me"
```

`sub` is stable for this Musecat user and registered application, including after revocation and
reauthorization. Different client registrations receive different subjects. Do not use email,
nickname or a different app's subject to link accounts. Account merger requires an explicit,
reviewed migration and fresh approval if the client or requested permissions change.

## Refresh and revoke

Access tokens last at most ten minutes. Refresh tokens have a fixed absolute 30-day family
expiry and rotate on every successful use. Serialize refresh requests per linked account;
retrying a consumed refresh token revokes the current grant and requires new user consent.
A consumed authorization-code replay also revokes its current grant. Old tokens from an earlier
approval cannot revoke a later approval. A password/session-key reset invalidates all app grants.
No token is ever returned by a list or profile endpoint. The database stores only token hashes.

```sh
curl -u "$MUSECAT_CLIENT_ID:$MUSECAT_CLIENT_SECRET" \
  --data-urlencode 'grant_type=refresh_token' \
  --data-urlencode "refresh_token=$MUSECAT_REFRESH_TOKEN" \
  "$MUSECAT_OAUTH_ISSUER/oauth/token"

curl -u "$MUSECAT_CLIENT_ID:$MUSECAT_CLIENT_SECRET" \
  --data-urlencode "token=$MUSECAT_REFRESH_TOKEN" \
  "$MUSECAT_OAUTH_ISSUER/oauth/revoke"
```

Revoking an access or refresh token revokes the app grant. Unknown tokens, including another
client's token, return 200 without revealing their validity. A user may list and revoke apps via
`GET /user/authorizations` and `DELETE /user/authorizations/{id}` with a Musecat session token.
These management endpoints are for the Musecat UI/BFF, not delegated applications. They remain
available when new authorization is disabled. Revocation cancels bound pending attempts;
withdrawal deletes grants, codes, tokens and user-bound pending requests transactionally.

## Errors and limits

OAuth endpoints return `{error}` with standard codes such as `invalid_request`, `invalid_client`,
`invalid_scope`, `invalid_grant`, `unsupported_grant_type`, `access_denied`, or
`temporarily_unavailable`. Invalid clients return 401 with a Basic challenge. Resource endpoints
return 401 `invalid_token` or 403 `insufficient_scope` with a Bearer challenge; domain errors keep
their existing API contract. OAuth and personal responses send no-store/no-referrer headers.
Protocol errors return to a verified registered callback with `error`, `state` and `iss`. Invalid
clients, callbacks, malformed state and ambiguous parameters remain local errors without redirecting.
Never redirect the user to an unvalidated callback when handling an authorization error.

Default all-user IP limits: authorize 10/10 minutes, token 60/10 minutes, consent read 60/10
minutes, consent decision 20/10 minutes, revoke 60/10 minutes, developer resources 120/minute.
Existing operator limits and exemptions take precedence. Quotas may therefore differ in a
deployment; handle HTTP 429 without automatically restarting an authorization or token exchange.

## Operator configuration

The executable reads `MUSECAT_OAUTH_CLIENTS`, `MUSECAT_OAUTH_ISSUER` (HTTPS backend origin), and
`MUSECAT_OAUTH_CONSENT_URL` (HTTPS Musecat consent page without query/fragment).
Missing configuration disables authorization/token/resource endpoints. Invalid supplied
configuration fails startup without printing secret values. These are separate from
`MUSECAT_EXTERNAL_PROVIDERS`, which configures the reverse direction of account linking.

```json
[
  {
    "id": "example_partner",
    "name": "Example partner",
    "description": "Shows arcades and submits fault reports on your behalf.",
    "secret": "REPLACE_WITH_A_RANDOM_SERVER_SECRET_AT_LEAST_32_CHARACTERS",
    "redirect_uris": ["https://partner.example/oauth/musecat/callback"],
    "scopes": ["profile:read", "arcade:read", "arcade:report:create"]
  }
]
```

Discovery: `/.well-known/oauth-authorization-server`. Public reference: `/developer`.
Machine-readable contract: `/developer/openapi.yaml`. HTTPS termination and trusted ingress IP
forwarding must be configured; do not expose development HTTP or a fake consent page in production.
Core supplies the fresh schema; Full owns the guarded existing-database migration. Apply and
validate the Full migration on a disposable copy before deployment, never by running Core against
an existing Full database. The executables clean expired requests, codes and token families every
five minutes in bounded batches. No production database is modified by the test suite.

Security basis: [OAuth 2.0](https://www.rfc-editor.org/rfc/rfc6749),
[PKCE](https://www.rfc-editor.org/rfc/rfc7636),
[OAuth security best practices](https://www.rfc-editor.org/rfc/rfc9700),
[revocation](https://www.rfc-editor.org/rfc/rfc7009),
[authorization server metadata](https://www.rfc-editor.org/rfc/rfc8414).
