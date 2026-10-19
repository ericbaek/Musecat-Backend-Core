# Musecat Developer API

Connect a user's Musecat account once with `account:access` to use general user features on their behalf.
Management functions and account-security operations are excluded, including for staff accounts.
The development environment uses [Musecat Preview](https://preview.musecat.app) and OAuth issuer
`https://db-dev.musecat.app`. Register development applications in the
[Developer Center](https://preview.musecat.app/kr/developer); application credentials are issued
there immediately after validated registration. Other example domains in this guide are placeholders.
Musecat is the OAuth authorization server for Direction A. Your server is the OAuth client.
Direction B lets Musecat connect to your authorization server and read your users' records.
Each direction has its own grant and tokens. The implemented reciprocal one-step flow coordinates
both under explicit bilateral consent; independent single-direction OAuth remains available.
See [`joint-connections.md`](joint-connections.md) and the
[partner integration specification](partner-integration-spec.md) for consent and server endpoints.
The implementation supports registered
confidential server applications using Authorization Code with PKCE S256 and HTTP Basic client
authentication. Opt-in OpenID Connect login is documented in [Sign in with Musecat](sign-in-with-musecat.md).
The readonly login scope bundle is `openid profile arcade:read`; the general API delegation flow below uses `account:access`.

## Register your application

Create an application in Developer Center with an immutable partner ID, display name, contact
and the direction you need. Direction A requires exact HTTPS callback URLs. Registration is immediately usable after structural validation; generate its Musecat client secret without operator review. Opt into Sign in with Musecat if
you need the readonly `openid profile arcade:read` scope bundle.

A Direction B-only application needs no Direction A callback or Musecat-issued client secret.
Declare your OAuth/records endpoints and service scopes and store your
partner-issued credentials for Musecat in the application's Direction B settings. Joint consent
requires both directions and its separate server keys. See
[Developer Center](developer-center.md) for the registration contract.

Keep client secrets, access tokens and refresh tokens on your server. Your browser should hold
only your own application's session cookie. Developer Center registration requires an active Musecat account, but no operator approval; there is no unauthenticated OAuth dynamic registration, password
grant, implicit grant or browser-only client. No partner is hardcoded.
A change to a client's registered configuration invalidates existing grants until users consent again.
Registration epochs are persisted at startup; disabling a client and later restoring the same
configuration cannot revive its previous tokens. Deploy the same registry on all instances.
Do not copy grants from a predecessor client when services merge into rhythm.info.

## Authorization flow

1. Generate an unpredictable `state` bound to the current user session in your application.
   Generate a 43–128 character PKCE verifier and its base64url(SHA-256(verifier)) challenge.
2. Redirect the browser to the metadata's authorization endpoint with `response_type=code`,
   `client_id`, the exact registered `redirect_uri`, `scope=account:access`, `state`,
   `code_challenge`, and `code_challenge_method=S256`.
3. Musecat validates the application and redirect before creating a five-minute request.
   It redirects to the configured Musecat consent page with an opaque `request` handle.
4. The Musecat consent application signs the user in. Its server calls `GET /oauth/consent`
   with that handle and the user's Musecat session token. This returns the registered app
   identity and requested permission descriptions, and binds the attempt to that user/session.
   The Musecat UI renders the server-returned permission descriptions as a bullet list and
   approves the requested connection with one button; there are no per-feature checkboxes.
   The UI must require an explicit decision,
   protect its mutation with same-origin CSRF checks, prevent framing and send no referrers.
   The WebCore consent frontend lives at `/{locale}/oauth/authorize` (for example,
   `https://musecat.example/kr/oauth/authorize`) and reuses Musecat's Google sign-in popup.
   Configure that deployed HTTPS URL before enabling OAuth clients. Core serves the API,
   while WebCore supplies the login/consent page and same-origin BFF.
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
GET /oauth/authorize?response_type=code&client_id=example_partner&redirect_uri=https%3A%2F%2Fpartner.example%2Foauth%2Fmusecat%2Fcallback&scope=account%3Aaccess&state=YOUR_RANDOM_SESSION_STATE&code_challenge=YOUR_S256_CHALLENGE&code_challenge_method=S256
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

## One connection, general user features

`account:access` is the primary scope for new integrations. It allows the explicit general-user
operation list under `/developer/v1/account`, with the same request payloads, multipart fields,
query parameters, responses, ownership, visibility, location, eligibility and validation rules as
the corresponding Musecat custom API. The public OpenAPI document contains the complete list
(currently 82 method/path pairs). This is an allowlist, not an unrestricted proxy: unlisted and
future operations are denied until deliberately added to the contract.

| Feature | Examples (all require `account:access`) |
| --- | --- |
| Profile and preferences | `GET /developer/v1/account/user/me`, `PUT /developer/v1/account/user/profile`, `PUT /developer/v1/account/user/game-preferences` |
| Arcades and information edits | `GET /developer/v1/account/arcade`, `PUT /developer/v1/account/arcade/basic`, `PUT /developer/v1/account/arcade/game` |
| Photos | `POST /developer/v1/account/arcade/photo/upload` (multipart), `PUT /developer/v1/account/arcade/photo` (apply photo selection) |
| Fault reports and reactions | `POST /developer/v1/account/arcade/flag`, `POST /developer/v1/account/arcade/flag/reaction` |
| Visits and campaigns | `POST /developer/v1/account/arcade/visit`, `POST /developer/v1/account/campaign/check` |
| Community | `GET`, `POST`, `PUT /developer/v1/account/community/post` |

Management/moderation operations, campaign administration, subway-map editing, bulk game-version
administration, support queues, signup, withdrawal, credential changes, authorization of other apps,
reverse account linking, and raw PocketBase collection APIs are excluded. Staff status does not
make those operations available through a connected app. A user submitting their own support request
is a general-user operation; processing the administrative queue is not.

Each resource call checks the live grant, client configuration, expiry, account key and active-user
policy before using the existing domain handler. It cannot grant powers the user does not possess.
An arcade update still needs the usual location evidence or applicable account permission; OAuth
approval is not proof of physical presence. Use each operation's OpenAPI schema rather than adding
an arbitrary user ID, role or GPS-bypass flag. In-flight accepted requests may finish after revocation;
new requests fail. Mutating request attempts are attributed in server logs to the Musecat user and
app-specific delegation subject; this log entry alone is not evidence of a committed edit.

```sh
curl -H "Authorization: Bearer $MUSECAT_ACCESS_TOKEN" \
  "$MUSECAT_OAUTH_ISSUER/developer/v1/account/user/me"

curl -H "Authorization: Bearer $MUSECAT_ACCESS_TOKEN" \
  "$MUSECAT_OAUTH_ISSUER/developer/v1/me"
```

`GET /developer/v1/me` returns `{sub, username, nickname}` plus optional absolute public avatar `picture`. Use `(issuer, sub)` as the
verified external identity for this registered application. `sub` is stable across revocation
and reauthorization for the same Musecat user and client; different clients receive different
subjects. `/developer/v1/account/user/me` returns the normal profile projection, including its
Musecat resource ID; do not confuse that `id` with the app-specific OAuth `sub`. These resource endpoints do not return an ID token; OpenID authorization-code exchange does. Do not infer identity from email, nickname or a different app's subject.

An omitted/empty authorization `scope` defaults to `account:access` only when the registered
client permits it. Send it explicitly in new integrations. Older narrow grants never acquire
account access automatically. Changing the registered scopes invalidates existing grants and
requires fresh consent. A client/issuer merger also requires explicit migration or reconnection.

### Legacy narrow scopes

These remain compatible for existing applications. They do not grant the account namespace.
An account-access token can also use these legacy resource aliases.

| Scope | Permission and endpoint |
| --- | --- |
| `profile:read` | `GET /developer/v1/me`: app-specific stable `sub`, username, nickname |
| `arcade:read` | Public arcade list, public detail and nearby discovery |
| `arcade:report:create` | Create a fault report using the user's normal arcade write permissions |
| `arcade:report:react` | Add/remove a report reaction using the user's normal permissions |
| `campaign:read` | Read campaigns, their detail and arcade campaign status |
| `campaign:respond` | Submit a campaign response with existing eligibility and location checks |

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
their existing API contract. Excluded registered account operations return 403 `access_denied`;
unregistered paths may return 404/405. OAuth and personal responses send no-store/no-referrer headers.
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
    "description": "Uses general Musecat features on your behalf; management excluded.",
    "secret": "REPLACE_WITH_A_RANDOM_SERVER_SECRET_AT_LEAST_32_CHARACTERS",
    "redirect_uris": ["https://partner.example/oauth/musecat/callback"],
    "scopes": ["account:access"]
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

## Partner handoff

CHUNITHM Info implementation brief: [`partners/chuniinfo/agent.md`](partners/chuniinfo/agent.md)
in the source repository. Share it with this guide, `developer-openapi.yaml` and `joint-connections.md`. The partner brief
is a repository handoff file, not an additional HTTP API endpoint.

### External profile visibility and arcade discovery

Connected-app profile reads use the ordinary third-party projection, even when the actor
is the profile owner or staff. Private favorites and games are omitted, country display
follows the public profile policy, and private passports/visit statistics return 404.
Summary passports omit visit dates; full passports include them. Profile update responses
use the same public projection. These read rules do not change authorized write operations.

Bulk arcade enumeration is not part of the external API: `/developer/v1/arcades` is absent
and `/developer/v1/account/arcades` is denied by the allowlist (including HEAD). Existing
grants do not bypass this restriction. Individual arcade detail and paginated nearby
search remain available; first-party `/arcades` retains its existing behavior.
