# External account connections

External account linking is independent of Musecat sign-in and of partner permissions to mutate
arcades. These endpoints verify an external account through an explicitly configured OAuth provider.
They do not issue partner delegation tokens, expose play records, or publish game data.

## Model and ownership

`external_account_connection` stores the Musecat user, provider ID, issuer, verified subject,
actually granted scopes, and encrypted provider credentials. `(issuer, subject)` is unique globally;
`(user, issuer)` is unique per Musecat account. A conflicting identity returns 409 and is never
transferred or silently replaced. Email and nickname are not identity keys.

`external_service_connection` attaches independently named game services to an account connection.
A rhythm.info identity can therefore authorize CHUNITHM and SOUND VOLTEX separately. An independent
NosLog issuer remains a separate identity even if its subject happens to be identical. All new and
reauthorized service grants start private with no selected public fields. This phase has no API to
publish those fields. Reauthorization replaces the game consent with exactly the requested services
whose required scopes were granted, rather than keeping permissions that were not reapproved.

`external_connection_request` stores a state hash, session-token hash, encrypted PKCE verifier,
provider configuration fingerprint, requested scopes and services, five-minute expiry, and a
pending/exchanging state. There is at most one attempt per user and issuer. A new attempt cancels
older work, including an exchange already in progress. The executable purges expired attempts every
five minutes; expiry is also enforced synchronously on claim and completion.

All three collections lock raw REST list, view, create, update and delete rules. Secret fields are
also hidden. Custom API responses omit external subjects, credentials, verifier and session hashes.
Account and game rows cascade on user/account deletion. Withdrawal deletes account connections,
game grants and all pending attempts in the same transaction as anonymization.

Core owns reusable schema and behavior. Core's migration is fresh-bootstrap only. Full applies its
independent guarded forward migration using the shared schema function, without importing Core's
migration registry. Existing incompatible fields, rules or indexes abort the whole schema change.

## HTTP API

All authenticated calls use `Authorization: Bearer <Musecat user token>`. The BFF must keep that
same token for the start and completion of a flow. A refreshed/replaced session requires a new start.
Provider callbacks terminate at their registered web/BFF URL; the BFF sends the callback code and
state to Core with the initiating Musecat user session. Core has no anonymous callback that accepts
a user ID or external UID from the browser.

| Method and path | Behavior |
| --- | --- |
| `GET /integrations/providers` | Public list of configured provider IDs, names, issuers and game services. No credentials or token endpoints. An empty registry returns `items: []`. |
| `GET /user/connections` | Active user's account connections, granted scopes and private game grants. Works when new linking is disabled. |
| `POST /user/connections/start` | Accepts `{provider, services: [service_id, ...]}`. Returns `authorization_url` and `expires_at`. Unknown service, duplicate selection and invalid payload return 400. Unconfigured provider returns 503. |
| `POST /user/connections/complete` | Accepts `{provider, state, code, iss}` or `{provider, state, error, iss}` for a denied flow. Returns the safe account projection, or 400/409/502/503. Never accepts an external account ID. |
| `DELETE /user/connections/{id}` | Deletes an owned account, its game grants, encrypted credentials and attempts for that issuer. Returns 204. Missing and other-user IDs both return 404. |

Responses use no-store. Anonymous requests require authentication; withdrawn or banned accounts
are denied. Start and completion are limited at runtime to 10 and 20 requests per client IP per
600 seconds respectively, sharing the authenticated/anonymous audience. Existing operator rate
limits retain their settings and precedence.

## Verification and race handling

Start uses OAuth Authorization Code with PKCE S256. Endpoints, client authentication and redirect
URIs come only from trusted executable configuration. Every issuer must have its own callback URL.
If a provider returns `iss`, it must exactly match the configured issuer; `require_issuer` additionally
requires that parameter. Provider ID, session, configuration fingerprint, expiry and state are
checked before code exchange.

Claiming a request changes it to exchanging and clears its stored verifier before external HTTP.
Only one completion can claim it. Failures are terminal: an expired, rejected or failed exchange
requires a new start, and the same request cannot replay a code. External HTTP happens outside
transactions, uses a ten-second total deadline, rejects redirects, and bounds the identity response
to 64 KiB. Client auth style is explicit, avoiding automatic client-auth retries with a one-use code.

The OAuth adapter retrieves a stable string subject from a configured top-level field in the
provider's authenticated user-info response. This is an OAuth adapter, not an ID-token/OIDC login
implementation. It does not trust an unverified ID token. A supplied user-info `iss` must also
match. Providers with nested or numeric identity contracts need a reviewed adapter change after
their exact specification is available; do not guess their identity field from a public profile API.

Before persistence, a transaction revalidates the current token and account restrictions, the
claimed request, expiry and identity uniqueness. A simultaneous disconnect, withdrawal, cleanup or
new start removes the claim, preventing an old callback from recreating the connection. Credential
changes that invalidate the Musecat session during external I/O also prevent completion.

Granted scopes are intersected with requested scopes. An omitted token response scope means the
requested scopes, per OAuth. Required identity scopes must all be granted. Only requested services
with all their required scopes become game grants; other services are omitted from the result.

## Local disconnect boundary

Disconnect is an atomic local unlink and works even if the provider is disabled or unreachable.
This phase does not call the provider's token-revocation endpoint. Removing a Musecat connection
therefore does not claim to revoke authorization in the provider's own account settings. No
provider tokens are returned to clients, and local credentials and pending attempts are removed.
There is no external play-data cache or partner delegation grant in this phase.

## Runtime configuration

Both executables read `MUSECAT_EXTERNAL_PROVIDERS` and `MUSECAT_CONNECTION_ENCRYPTION_KEY` at startup
and inject a constructed manager into `coreapp.Config.Connections`. Core configuration does not
read ambient environment variables or register jobs. An empty provider configuration disables new
linking. A supplied invalid configuration fails startup without printing secret values.

The provider variable is a JSON array. This illustrative provider is not a live partner contract:

```json
[
  {
    "id": "rhythm",
    "name": "Rhythm account",
    "issuer": "https://accounts.rhythm.example",
    "authorization_url": "https://accounts.rhythm.example/authorize",
    "token_url": "https://accounts.rhythm.example/token",
    "userinfo_url": "https://accounts.rhythm.example/userinfo",
    "subject_field": "sub",
    "client_id": "musecat",
    "client_secret": "replace-at-deployment",
    "client_auth_method": "client_secret_basic",
    "redirect_uri": "https://musecat.example/api/connections/callback/rhythm",
    "require_issuer": true,
    "scopes": ["profile"],
    "services": [
      {"id": "chunithm", "name": "CHUNITHM", "scopes": ["chuni:read"]},
      {"id": "sdvx", "name": "SOUND VOLTEX", "scopes": ["sdvx:read"]}
    ]
  }
]
```

Use a separate registered provider and callback for NosLog. Provider and service IDs are lowercase
ASCII identifiers. Endpoints must be fixed HTTPS URLs without user-info, query or fragment. The
client-auth method is `client_secret_basic` or `client_secret_post`; credentials are server-only.
Production endpoints and scopes must be confirmed with each partner before enabling it.

The encryption key is standard base64 for exactly 32 random bytes, stored in deployment secrets
and backed up separately from the database. AES-256-GCM binds request and account ciphertext to
their record ID and purpose. Changing the key requires reconnecting accounts; local disconnect
remains possible. This phase does not implement a key-ring rotation protocol.

A chuni.info to rhythm.info domain/issuer change does not automatically migrate identities. Keep
existing connections distinguishable and require a verified migration procedure or user reconnection.
One account can support several services once the partner uses a shared issuer and stable subject.

## Validation

Focused coverage includes multiple providers and games, private defaults, account conflicts,
current-session binding, PKCE, one-use claims, scope narrowing, malformed identity responses,
disconnect/withdrawal/new-start races, post-I/O restrictions, schema guards and raw collection locks.
Run `GOWORK=off go test ./...` for Core. Full additionally tests its guarded migration against a
fresh disposable synthetic existing installation. No production database is needed for these tests.

The flow follows [OAuth security guidance](https://datatracker.ietf.org/doc/html/rfc9700) and
[authorization-server issuer identification](https://www.rfc-editor.org/info/rfc9207/).
