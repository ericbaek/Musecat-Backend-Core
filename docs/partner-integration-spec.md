# Musecat Partner Integration Specification v1

Status: Musecat-defined contract, 2026-10-18. This document is normative for every third-party
platform that connects with Musecat. Partners implement it; they do not negotiate it. Values that
differ per partner (URLs, credentials, supported services) are declared once in the onboarding form
in section 8. Anything not listed there is fixed by this specification.

Musecat implementation status per section is in section 9. Sections 1–3 are implemented on the
feature branch; sections 4–6 define the contract Musecat is implementing next. Partners may build
against sections 4–6 now using the schemas here; Musecat will not change them without a version bump.

Keywords MUST, MUST NOT, SHOULD and MAY follow RFC 2119.

## 1. Model

Two independent OAuth 2.0 relationships exist between Musecat and a partner:

| Direction | Authorization server | Client | Purpose |
| --- | --- | --- | --- |
| A. Partner → Musecat | Musecat | Partner server | Partner uses general Musecat features for the connected user (`account:access`) |
| B. Musecat → Partner | Partner | Musecat server | Musecat reads the user's approved game records from the partner |

Each direction has its own grant, tokens, expiry and revocation. There is no shared token, shared
login session or universal user ID. Section 5 defines how both directions are approved with one
user action; it does not merge them.

Identity keys:

- Direction A identity of a Musecat user, as seen by the partner: `(musecat_issuer, client_id, sub)` from `GET /developer/v1/me`.
- Direction B identity of a partner user, as seen by Musecat: `(partner_issuer, sub)` from the partner userinfo endpoint.
- Email, nickname, display name and numeric IDs embedded in other payloads are never identity keys.

Identifiers (`partner_id`, `service_id`) MUST match `^[a-z][a-z0-9_-]{0,127}$`.

### 1.1 Service registry

A service is one game title whose records can be authorized independently. Musecat owns the
canonical service IDs; partners declare which ones they support. Current registry:

| `service_id` | Title |
| --- | --- |
| `chunithm` | CHUNITHM |
| `maimai` | maimai でらっくす |
| `ongeki` | オンゲキ |
| `sdvx` | SOUND VOLTEX |
| `iidx` | beatmania IIDX |
| `taiko` | 太鼓の達人 |
| `wacca` | WACCA |
| `arcaea` | Arcaea |
| `polaris_chord` | ポラリスコード |
| `ddr` | DanceDanceRevolution |
| `jubeat` | jubeat |
| `popn` | pop'n music |
| `nostalgia` | ノスタルジア |
| `gitadora` | GITADORA |
| `museca` | MÚSECA (legacy) |

Partners needing a title not listed request an ID from Musecat; they MUST NOT invent one. A single
partner identity MAY hold grants for several services. A brand or domain change (for example
chuni.info → rhythm.info) is a new issuer and therefore a new identity unless section 7 applies.

## 2. Direction A: partner uses Musecat (implemented)

Normative reference: `developer-api.md` and `developer-openapi.yaml`. Summary of fixed values:

| Item | Value |
| --- | --- |
| Grant | Authorization Code + PKCE S256 only |
| Client type | Confidential server; `client_secret_basic` |
| Scope for new integrations | `account:access` (single scope; management excluded) |
| Authorization request lifetime | 5 minutes |
| Authorization code lifetime | 90 seconds, single use |
| Access token lifetime | ≤ 600 seconds, opaque, usable only on `/developer/v1/*` |
| Refresh token | Rotates on every use; family absolute lifetime 30 days; replay revokes the grant |
| Revocation | RFC 7009 `POST /oauth/revoke`; revoking either token revokes the grant |
| Discovery | RFC 8414 `/.well-known/oauth-authorization-server` |
| Identity | `GET /developer/v1/me` → `{sub, username, nickname}` |
| Resource surface | 83 allowlisted operations under `/developer/v1/account/*` |

Partners MUST NOT request legacy narrow scopes for new integrations.

## 3. Direction B: Musecat reads partner records (adapter implemented; partner profile fixed here)

The partner MUST operate an OAuth 2.0 authorization server satisfying this profile. Musecat's adapter
is generic; a partner that deviates is not registered.

### 3.1 Authorization server profile

| Item | Requirement |
| --- | --- |
| Grant | Authorization Code (RFC 6749 §4.1) with PKCE S256 (RFC 7636). `plain` MUST be rejected. |
| Client authentication | `client_secret_basic` (RFC 6749 §2.3.1). `client_secret_post` MAY additionally be supported but Musecat will use Basic. |
| Endpoints | Fixed HTTPS `authorization_url`, `token_url`, `userinfo_url`, `revocation_url`. No query, fragment or userinfo in the URL. Redirect responses (3xx) from token/userinfo are treated as failure. |
| Issuer | Fixed HTTPS origin string. The authorization response MUST include `iss` (RFC 9207) equal to it. |
| Redirect URI | Exact-match registration of the Musecat callback supplied at onboarding. Separate development and production registrations. |
| State | Opaque; echoed unchanged. |
| Scopes | One identity scope plus one scope per service (section 3.3). Token response MUST include `scope`; omitted `scope` is interpreted as "all requested scopes granted". |
| Access token | Bearer, opaque or JWT; Musecat does not introspect it. Lifetime ≥ 300 seconds recommended. |
| Refresh token | REQUIRED. Rotation on use MAY be enabled. Family absolute lifetime ≥ 30 days. A grant whose access token expired without a refresh token is treated by Musecat as unusable. |
| Revocation | RFC 7009 endpoint accepting `token` with Basic client auth. Unknown tokens return 200. |
| Timeouts | Musecat uses a 10-second total deadline per HTTP call and caps userinfo responses at 64 KiB. |
| Errors | RFC 6749 §5.2 JSON error bodies. |

### 3.2 Userinfo

`GET {userinfo_url}` with `Authorization: Bearer <access_token>` and `Accept: application/json`.

Response (200, `application/json`):

```json
{
  "sub": "stable-string-subject",
  "iss": "https://partner.example",
  "name": "Display name shown on the Musecat consent screen",
  "services": ["chunithm", "maimai"]
}
```

| Field | Requirement |
| --- | --- |
| `sub` | REQUIRED. Top-level JSON string, 1–256 characters, stable for the account's lifetime, unique within the issuer. Not an email, not a nickname, not a number. |
| `iss` | OPTIONAL. If present MUST equal the registered issuer. |
| `name` | OPTIONAL. Safe display name; Musecat shows it on the joint consent screen only. |
| `services` | OPTIONAL. Service IDs the account currently has data for. Informational. |

Nested subjects (`{"user":{"id":…}}`) and numeric subjects are not accepted.

### 3.3 Scopes

| Scope | Grants |
| --- | --- |
| `profile` | Userinfo only. Required for every connection. |
| `<service_id>:read` | Read access to that service's records API (section 3.4). |

Partners whose existing scope strings differ declare the mapping in the onboarding form. The
semantics above are fixed: the identity scope MUST NOT expose records, and a service scope MUST NOT
expose other services.

### 3.4 Records API

`GET {records_url}/{service_id}` with `Authorization: Bearer <access_token>`.

Responses:

| Status | Meaning |
| --- | --- |
| 200 | Record envelope below. |
| 403 `{"error":"records_private"}` | The user has set this service private on the partner. Musecat hides any cached data. |
| 403 `{"error":"insufficient_scope"}` | Token lacks `<service_id>:read`. |
| 404 | The account has no data for this service. |
| 401 | Invalid/expired token → Musecat refreshes once, then marks the connection degraded. |
| 429 | Musecat backs off per `Retry-After` (seconds). |

Envelope (200). Service-specific statistics are expressed as typed fields so Musecat renders every
partner with one generic component; partners do not need to coordinate UI with Musecat.

```json
{
  "spec": "musecat-records/v1",
  "service": "chunithm",
  "sub": "stable-string-subject",
  "visibility": "public",
  "updated_at": "2026-10-18T03:21:00Z",
  "source": {"name": "CHUNITHM Info", "url": "https://partner.example/u/xyz"},
  "player": {"name": "PLAYER", "title": "…", "level": 85, "team": "…"},
  "fields": [
    {"key": "rating", "label": {"ko": "레이팅", "en": "Rating", "ja": "レーティング"}, "type": "decimal", "value": 16.42, "precision": 2},
    {"key": "rating_max", "label": {"ko": "최고 레이팅", "en": "Max rating", "ja": "最高レーティング"}, "type": "decimal", "value": 16.58, "precision": 2},
    {"key": "over_power", "label": {"ko": "OVER POWER", "en": "OVER POWER", "ja": "OVER POWER"}, "type": "decimal", "value": 12345.67, "precision": 2},
    {"key": "play_count", "label": {"ko": "플레이 수", "en": "Plays", "ja": "プレイ回数"}, "type": "integer", "value": 2310},
    {"key": "last_played_at", "label": {"ko": "마지막 플레이", "en": "Last played", "ja": "最終プレイ"}, "type": "datetime", "value": "2026-10-17T14:02:00Z"}
  ],
  "sections": [
    {
      "key": "best",
      "label": {"ko": "베스트", "en": "Best", "ja": "ベスト"},
      "columns": [
        {"key": "title", "label": {"ko": "곡", "en": "Song", "ja": "曲"}, "type": "string"},
        {"key": "difficulty", "label": {"ko": "난이도", "en": "Difficulty", "ja": "難易度"}, "type": "string"},
        {"key": "score", "label": {"ko": "점수", "en": "Score", "ja": "スコア"}, "type": "integer"},
        {"key": "lamp", "label": {"ko": "램프", "en": "Lamp", "ja": "ランプ"}, "type": "string"}
      ],
      "rows": [["Song A", "MASTER 14+", 1009876, "AJ"]],
      "total": 30,
      "more_url": "https://partner.example/u/xyz/best"
    }
  ]
}
```

| Field | Requirement |
| --- | --- |
| `spec` | MUST be `musecat-records/v1`. |
| `service`, `sub` | MUST match the request and the token's subject. |
| `visibility` | `public` or `private`. `private` MAY be returned instead of 403; Musecat treats both identically. |
| `updated_at` | RFC 3339 UTC. Musecat shows it as the data freshness and uses it for cache invalidation. |
| `source` | REQUIRED. Attribution shown with the data. |
| `player` | OPTIONAL. All keys optional. |
| `fields` | 0–24 summary entries. `type` ∈ `integer`, `decimal`, `string`, `percent`, `datetime`, `date`, `rank`. `label` MUST include `en`; `ko`/`ja` SHOULD be included. |
| `sections` | 0–4 tables, each ≤ 50 rows, ≤ 8 columns. Rows are positional arrays matching `columns`. `more_url` is an outbound link to the partner for the full list. |
| Size | Response ≤ 256 KiB. |

Musecat caches an envelope for at most 24 hours and re-fetches on profile view when stale. Musecat
never displays data for a service the user has not granted, and never publishes partner data on a
public Musecat profile unless both the partner `visibility` is `public` and the Musecat user has
enabled publication for that service.

## 4. Server-to-server authentication

Used by sections 5–6. Each direction uses a static bearer credential issued by the **receiving**
side at onboarding:

| Calls | Header | Credential issued by |
| --- | --- | --- |
| Musecat → partner joint endpoints | `Authorization: Bearer <partner_issued_key>` | Partner |
| Partner → Musecat joint endpoints | `Authorization: Bearer <musecat_issued_key>` | Musecat |

Keys are ≥ 32 random bytes, base64url, separate per environment, and distinct from OAuth client
secrets. Rotation: the receiver accepts two keys during a rotation window declared in the onboarding
form. All calls carry `X-Request-Id` (UUIDv4); receivers MUST treat a repeated `X-Request-Id` within
10 minutes as an idempotent retry and return the original result. Requests and responses are
`application/json`; non-2xx responses carry `{"error": "<code>", "error_description": "…"}`.

Error codes used throughout sections 5–6:

| Code | HTTP | Meaning |
| --- | --- | --- |
| `invalid_request` | 400 | Malformed body or missing field |
| `unauthorized_partner` | 401 | Bearer key unknown/expired |
| `invalid_intent` | 400 | Handle unknown or not issued for this counterparty |
| `intent_expired` | 410 | Past `expires_at` |
| `intent_consumed` | 409 | Already redeemed by a receipt |
| `manifest_unsupported` | 400 | Receiver does not support the manifest version |
| `subject_mismatch` | 409 | Verified identity differs from the intent's bound account |
| `temporarily_unavailable` | 503 | Retry later |

## 5. Joint connection protocol (one approval for both directions)

### 5.1 Consent manifest

Musecat owns the consent text. A manifest version freezes the purposes, scopes and wording shown
on the single approval screen. Partners declare which versions they support.

`musecat-joint/v1` grants, with one approval:

1. Direction A: `account:access` for the partner's registered Musecat client.
2. Direction B: `profile` plus `<service_id>:read` for each service listed in the intent.

Bullets shown to the user (Musecat renders in the user's locale):

- *{partner}* can use general Musecat features with my account; management is excluded.
- Musecat can retrieve my approved *{service list}* records from *{partner}*.
- Record publication follows *{partner}*'s privacy settings and my Musecat publication setting.

### 5.2 Roles and handles

| Term | Definition |
| --- | --- |
| `correlation_id` | UUIDv4 generated by the initiating side; identifies the attempt on both servers and in logs (never the user). |
| `intent` | Opaque one-use handle (≥ 32 random bytes, base64url) issued by the partner for its logged-in user. Only a hash is persisted on either side. Lifetime 5 minutes. |
| `generation` | Opaque string issued by Musecat when a receipt succeeds. Identifies this approval instance for revocation (section 6). |
| `pair_id` | Equals `correlation_id` of the attempt that produced the current `generation`. |

### 5.3 Partner-hosted endpoints

Base URL `{joint_url}` is declared at onboarding. All require the Musecat → partner bearer key.

**`POST {joint_url}/intents/lookup`** — Musecat retrieves and validates an intent it received from the browser.

Request:
```json
{"intent": "<handle>", "partner_id": "chuniinfo"}
```
Response 200:
```json
{
  "correlation_id": "uuid",
  "sub": "partner-subject",
  "name": "Display name",
  "services": ["chunithm"],
  "manifest": "musecat-joint/v1",
  "code_challenge": "<S256 challenge for the partner's Direction-A code>",
  "redirect_uri": "https://partner.example/oauth/musecat/callback",
  "expires_at": "RFC3339"
}
```
Lookup is repeatable until the intent is consumed or expired. `redirect_uri` MUST be one of the
partner's registered Musecat callbacks. `code_challenge` is generated by the partner at intent
creation; the partner keeps the verifier server-side.

**`POST {joint_url}/receipts`** — Musecat reports that the user approved the manifest and hands over the Direction-A code; the partner atomically consumes the intent and issues its Direction-B code.

Request:
```json
{
  "intent": "<handle>",
  "correlation_id": "uuid",
  "manifest": "musecat-joint/v1",
  "generation": "<opaque>",
  "musecat_code": "<one-use Direction-A authorization code>",
  "musecat_code_expires_at": "RFC3339",
  "code_challenge": "<S256 challenge for the Direction-B code>",
  "redirect_uri": "<Musecat's registered callback at the partner>"
}
```
Response 200:
```json
{
  "partner_code": "<one-use Direction-B authorization code>",
  "partner_code_expires_at": "RFC3339",
  "scope": "profile chunithm:read"
}
```

Partner obligations on receipt: verify the bearer key; verify the intent hash, counterparty,
expiry and unconsumed state **atomically**; verify `manifest` is supported; bind `partner_code` to
Musecat's registered client, the supplied `redirect_uri`, `code_challenge` (S256) and the intent's
account and services; set `partner_code` lifetime ≤ 90 seconds; store `generation` and
`correlation_id` against the resulting grant for section 6. Only after a 200 response does the
partner exchange `musecat_code` at Musecat `/oauth/token` with its own verifier and `redirect_uri`.
A failed exchange MUST NOT be retried with the same code; the partner shows a partial state and
offers a restart.

**`POST {joint_url}/revocations`** — see section 6.

### 5.4 Musecat-hosted endpoints

All require the partner → Musecat bearer key.

**`GET {musecat_api}/partner/v1/joint/{correlation_id}`** — partner polls the attempt state.

Response 200:
```json
{"correlation_id": "uuid", "status": "pending|partial|connected|degraded|cancelled|expired|invalidated", "generation": "<opaque or null>", "musecat_access": true, "partner_access": true}
```

**`POST {musecat_api}/partner/v1/revocations`** — see section 6.

### 5.5 Browser flow

Entry from the partner:

1. Partner (user logged in) creates an intent and redirects the browser to
   `{musecat_web}/{locale}/connect/{partner_id}?intent=<handle>`.
2. Musecat server calls `intents/lookup`, signs the Musecat user in if needed, binds the attempt to
   that exact Musecat session, and renders the manifest with both identities (`name` from the
   intent, Musecat nickname).
3. User approves once. Musecat server, in order: creates the Direction-A grant and code for the
   partner client (bound to the intent's `redirect_uri` and `code_challenge`); generates its
   Direction-B PKCE pair; calls `receipts`; exchanges `partner_code` at the partner token endpoint;
   calls userinfo and verifies `sub` equals the intent's `sub` (else `subject_mismatch` → attempt
   invalidated, Direction-A grant revoked); stores the connection; records the attempt as connected
   on its side.
4. Musecat redirects the browser to the partner's `redirect_uri` with `code=<musecat_code>`,
   `state=<correlation_id>`, `iss=<musecat_issuer>`. The partner exchanges the code (standard
   Direction-A callback handling) and shows completion only when its own exchange succeeded and
   `GET /partner/v1/joint/{correlation_id}` returns `connected`.

Entry from Musecat:

1. Musecat redirects the browser to `{connect_start_url}?correlation_id=<uuid>&return_to={musecat_web}/{locale}/connect/{partner_id}`.
2. Partner signs its user in, creates an intent with that `correlation_id`, and redirects to
   `return_to` with `?intent=<handle>`. Flow continues at step 2 above.

Rules on both sides: changing the logged-in account on either service invalidates the attempt;
`redirect_uri` and `return_to` are validated against registration, never taken from user input;
intents, codes and tokens never appear in logs or analytics; completion is never shown on one side
based on the other side's claim alone.

### 5.6 Partial states

There is no cross-server transaction. The following outcomes are expected and MUST be rendered as
"partially connected, retry or disconnect", never as success:

- Receipt 200 but Musecat's exchange of `partner_code` failed → Musecat revokes its Direction-A grant, marks the attempt `partial`.
- Receipt 200 and Musecat connected, but the partner's exchange of `musecat_code` failed → partner shows partial; Musecat status stays `connected` on its side until the partner revokes or the user restarts, which supersedes the generation.
- Receipt returned non-2xx → nothing was issued; Musecat shows retry.

A restart always creates a new `correlation_id`; a successful restart issues a new `generation`
and both sides treat the previous generation as superseded.

## 6. Bilateral revocation

Either side may initiate. The initiator revokes locally first (access stops immediately), then
notifies the other side durably (outbox with exponential backoff, ≥ 24 hours of retries). A failed
notification never restores local access; the UI shows "disconnected here, remote revocation
pending".

**`POST {joint_url}/revocations`** (Musecat → partner) and **`POST {musecat_api}/partner/v1/revocations`** (partner → Musecat):

```json
{"pair_id": "uuid", "generation": "<opaque>", "reason": "user|security|admin|superseded", "revoked_at": "RFC3339"}
```

Response 200 `{"status": "revoked|already_revoked|unknown_generation"}`.

Receiver rules: revoke only the grant(s) created for exactly that `pair_id` + `generation`; a
notification for an older generation than the current one returns `unknown_generation` and changes
nothing; duplicate deliveries return `already_revoked`; pre-existing independent grants outside the
joint attempt are not touched unless `reason` is `security` and the receiver's own policy says so.

A one-sided unlink that the user performs without the joint action (for example through the
generic Musecat connections page) remains local-only and does not send a notification.

## 7. Issuer or client migration

When a partner changes issuer (domain), OAuth client or subject format:

1. The partner declares the new values in a new onboarding form; Musecat registers them as a new
   provider/client. Old grants are not copied.
2. If the partner can prove subject continuity, it MAY publish a signed mapping endpoint; Musecat
   will specify that endpoint in a later version. Until then, users reconnect.
3. The old registration is kept read-only until its refresh families expire, then removed.

Email matching, nickname matching and "same `sub` string at a different issuer" are never used.

## 8. Onboarding form

A partner submits one document per Musecat deployment (development, production) through the
Musecat Developer Center (`/{locale}/developer/apps`; design in
[`developer-center.md`](developer-center.md)). Until the Developer Center ships, the same JSON
is sent to the Musecat operator. Fields marked Musecat are generated by Musecat on approval and
revealed once; partner-issued secrets are entered by the partner and encrypted at rest.

Activation sequence: submit → automated checks (TLS, metadata issuer match, PKCE `plain`
rejected, client auth required, joint bearer enforced, conformance report) → operator **Approve**
→ live within 30 seconds on every Musecat instance. No further coordination is required.

```json
{
  "spec": "musecat-partner-onboarding/v1",
  "environment": "development",
  "partner_id": "chuniinfo",
  "display_name": {"ko": "츄니인포", "en": "CHUNITHM Info", "ja": "CHUNITHM Info"},
  "description": {"ko": "…", "en": "…", "ja": "…"},
  "contact": {"name": "…", "email": "…"},

  "direction_a": {
    "redirect_uris": ["https://partner.example/oauth/musecat/callback"],
    "client_id": "<Musecat>",
    "client_secret": "<Musecat, private channel>"
  },

  "direction_b": {
    "issuer": "https://partner.example",
    "authorization_url": "https://partner.example/oauth/authorize",
    "token_url": "https://partner.example/oauth/token",
    "userinfo_url": "https://partner.example/oauth/userinfo",
    "revocation_url": "https://partner.example/oauth/revoke",
    "records_url": "https://partner.example/api/musecat/records",
    "client_auth_method": "client_secret_basic",
    "identity_scope": "profile",
    "services": [
      {"id": "chunithm", "scope": "chunithm:read"}
    ],
    "musecat_redirect_uri": "<Musecat>",
    "musecat_client_id": "<partner issues>",
    "musecat_client_secret": "<partner issues, private channel>"
  },

  "joint": {
    "enabled": true,
    "manifests": ["musecat-joint/v1"],
    "joint_url": "https://partner.example/api/musecat/joint",
    "connect_start_url": "https://partner.example/connect/musecat/start",
    "musecat_to_partner_key": "<partner issues>",
    "partner_to_musecat_key": "<Musecat>",
    "key_rotation_window_hours": 24
  }
}
```

A partner that only wants Direction A omits `direction_b` and `joint`. A partner that wants Direction
B without one-step consent sets `joint.enabled` to `false`; users then approve each direction on
the respective site.

An approved form is the source of truth: the runtime derives the OAuth client, external provider
and joint partner registrations from it directly (Developer Center §4). Environment variables
remain only for Musecat's first-party clients and bootstrap.

## 9. Conformance and status

Partners self-certify with the Musecat conformance kit (mock Musecat + test runner) before
registration. Required passes: section 3 profile (PKCE, `iss`, userinfo `sub`, refresh, revoke,
records envelope validation, `records_private`), section 5 (lookup/receipt happy path, expired,
consumed, wrong key, manifest unsupported, subject mismatch, partial recovery) and section 6
(duplicate, old generation, unknown pair).

Musecat implementation status:

| Section | Status |
| --- | --- |
| 2 Direction A | Implemented (`delegation`, `/developer/v1/account/*`) |
| 3.1–3.3 Direction B adapter | Implemented (`connections`); `subject_field` is configured to `sub` per this spec |
| 3.4 Records fetch, cache and profile rendering | Not started |
| 4 Server-to-server auth | Not started |
| 5 Joint protocol | Ledger and coordinator implemented (`connections.JointManager`); HTTP endpoints, consent-page wiring and code issuance not started |
| 6 Bilateral revocation | Not started |
| 8 Developer Center (self-service form, checks, one-click approval, hot activation) | Designed (`developer-center.md`); not started |
| 9 Conformance kit | Designed (`developer-center.md` §6); not started |

Partner-facing promises in this document hold once the corresponding row reads "Implemented"; until
then partners build against the schemas here and test with the conformance kit when it ships.
