# Account-owned API

A bot key replaces interactive login for arcade automation. Every bot belongs to
the Musecat account that issued its key. `createdBy` and changelog `by` remain that
account's ID; `contribution_source` identifies which bot performed the work.
There is no synthetic bot user, inherited moderator API access, or JWT exchange.

## Issue and revoke a key

Open your own profile, choose the **API** tab, then **Manage keys**. Enter a key name
and issue a key. Copy the secret immediately: it is shown once. The server stores
only its SHA-256 hash. Keep the key in your bot's secret storage.

Only completed, active accounts with `founding_supporter`, `supporter`, `designer`,
`moderator`, `developer` or `arcade_owner` may issue and use keys. Ordinary accounts
are not eligible. Entitlement is rechecked on every call;
removing it blocks existing keys. Owners can still list/revoke their own keys.
An eligible account may hold five unrevoked keys. Keys have no automatic expiry,
including keys issued under the former expiry policy. Issue a replacement and
revoke the old key to rotate credentials. The key list returns safe metadata,
revocation and last-used time, never the
secret or digest. Bot names are immutable snapshots in committed history.

The account-session endpoints are `GET|POST /user/bot-keys` and
`DELETE /user/bot-keys/{id}`. A bot key cannot call these endpoints. Revocation is
idempotent and affects future requests; a previously accepted request may finish.
Account withdrawal/ban and account token-key rotation also block subsequent use.

## Call the API

Send `Authorization: Bearer <bot-key>` to `https://backend.musecat.app/bot/v1`.
Keys are not accepted in query parameters, in normal web APIs, or in raw PocketBase
collection APIs. Do not exchange them for a web login token.

```sh
curl -H "Authorization: Bearer $MUSECAT_BOT_KEY" \
  "$MUSECAT_API_URL/bot/v1/arcade/drafts"

curl -H "Authorization: Bearer $MUSECAT_BOT_KEY" \
  -H 'Content-Type: application/json' \
  --data '{"name":"Example Arcade","address":"Seoul","location":{"lat":37.5665,"lon":126.978}}' \
  "$MUSECAT_API_URL/bot/v1/arcade/new"
```

The corresponding canonical API's query, body, response, resource authorization,
photo limits and game `base_state_id` conflict rules apply. See `openapi.yaml`.

| Operation | API route suffix |
| --- | --- |
| Search and address lookup | `GET /search`, `/geocode`, `/reverse_geocode`, `/arcades/nearby` |
| Game catalog | `GET /game/catalog` |
| Detail, own drafts and history | `GET /arcade`, `/arcade/draft`, `/arcade/drafts`, `/arcade/changelog` |
| Create a private draft | `POST /arcade/new` |
| Edit information | `PUT /arcade/basic`, `/arcade/hour`, `/arcade/gtk`, `/arcade/sns`, `/arcade/game` |
| Upload and apply photos | `POST /arcade/photo/upload`, `PUT /arcade/photo` |
| Read photo assets | `GET /arcade/photo/atoms`, `/arcade/photo/file` |
| Preview requirements and publish | `GET|PUT /arcade/public` |

Creation returns a private draft. Fill in games and the applicable hours/contact
and photo requirements before publishing. Publication remains creator-only and
uses the existing account-level rules. A supplied map coordinate is not GPS proof.

Upload photo files with the existing multipart `photos` field; then apply the
returned atom IDs with `PUT /arcade/photo`. Retry failed files rather than
re-uploading successful files. Existing mutation retry behavior applies: creation
does not promise exactly-once delivery, so after an ambiguous network failure,
check your drafts before creating again.

Every key is limited to 60 accepted requests per minute across clients and server
instances sharing the database. A limit response is `429` with `Retry-After: 60`.
Existing IP-based limits also apply. Account settings, attendance, visit verification, moderation and unlisted routes
are excluded. Official operations below reuse live domain permissions.

## History and XP

Arcade and account history return `contribution_source: {kind, id, name}` for bot
work. `id` identifies the credential record, not the secret. Normal historical
rows retain a null source. Bot names are private to the issuing account. History responses omit `name` for
all other viewers, including staff; the website shows the issuing account and a
**BOT** badge without the name. Raw public photo metadata never exposes the stored
attribution snapshot.
Revoking a key does not rewrite or erase its earlier contributions.

Bot edits grant no XP. Bot-created arcades receive no creation/publication reward,
including when published later from the website. Bot changelog rows are excluded
from deferred draft rewards, and bot-uploaded photo atoms stay ineligible when
applied from the website. A real subsequent web edit remains eligible under the
normal cooldowns; an unchanged re-save is not a new contribution. Previously
earned XP and legacy history are unchanged. Publication preview and committed
rewards use the same rules. A bot publishing a web-created draft preserves
previously eligible web contributions.

## Relationship to external integrations

The external integration/preview branches already contain Developer Center and
OAuth account delegation. Personal bot keys are a separate credential type with
no client secret, consent redirect, or external app registration requirement.
They can ship on `main` without enabling all integration routes.

Both credential types should eventually resolve to the same verified account,
request-scoped contribution source and existing domain handlers. The
`service/contribution` boundary carries attribution through nested transactions;
it accepts no source claims from HTTP input. A later developer integration adapter
can attach its app identity and use the same external-contribution XP policy.
Developer Center must never expose or manage another user's personal key, and
existing OAuth tokens must not gain new permissions implicitly.

## Release

Core owns fresh-bootstrap schema and API semantics. Full owns
`1794900000_bot_api.go`, its independent additive production migration, and
`1795100000_bot_keys_without_expiry.go`, which makes the legacy date optional
without rewriting existing keys. Release
Core first, then pin that exact revision in Full before publishing its binary.
Run Full's migration preflight/dry run on a disposable copied database as required
by its migration contract. Do not run Core migrations against Full or production.
Deploy the backend contract before the frontend key-management UI.

The API tab filters account-owned change history by all bots or a specific key, including revoked keys. `GET /user/changelog?user=<owner>&bot=all|<key-id>` requires the authenticated owner; filtering precedes counting and pagination. Home dashboard summary and recent update feeds exclude bot changelog events before grouping and pagination. Human edits to bot-created arcades remain eligible for these feeds.

Developers may read another account's bot console through `GET /user/bot-keys?user=<id>` and `/user/changelog?user=<id>&bot=all|<key-id>`. Bot names remain owner-only; foreign key metadata omits the name and never includes secrets. Other roles cannot inspect foreign consoles. Issuance and revocation remain scoped to the authenticated owner. Public user-history summaries retain redacted bot attribution for activity badges without exposing edit details.

## Official arcade operations

The issuing account may use these explicit suffixes under `/bot/v1`, subject to
existing notice roles, current arcade ownership and conversation ACLs. Ownership
loss immediately blocks future management requests. Secrets, account credentials,
moderation endpoints, GPS checks and automatic web login remain unavailable.

| Method | Suffix |
| --- | --- |
| GET | `/arcade/analytics` |
| GET/PUT | `/arcade/memo` |
| GET/POST/PUT/DELETE | `/arcade/notice` |
| GET/PUT | `/arcade/report-settings` |
| PUT | `/arcade/inquiry-settings` |
| GET | `/user/arcade-inquiry-onboarding` |
| POST | `/arcade/flag/reaction` |
| GET/PUT | `/user/inquiry-bots/arcade/{id}` |
| GET | `/user/conversations`, `/user/conversations/{id}`, `/user/conversations/{id}/messages` |
| POST | `/user/conversations/{id}/messages` |
| PUT | `/user/conversations/{id}/read` |
| PATCH | `/user/conversations/{id}/inquiry` |
| GET | `/user/conversations/{id}/attachments/{messageId}/{file}` |

For example, POST this JSON to `/arcade/notice`:

```json
{"arcade":"ARCADE_ID","title":"Opening hours","type":"alert","display_mode":"normal","document":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Open from 10:00."}]}]}}
```

Notices accept the same multipart uploads (three images, 5 MiB each, 16 MiB body)
and active display limits (one required, three pinned) as the website. GET reads
active notices; PUT/DELETE identify the returned notice `id`. Notices keep their
existing standalone persistence and do not enter arcade edit history.

Onboarding lists owned arcades with saved report/inquiry settings. PUT settings
with `{"arcade":"ARCADE_ID","mode":"internal"}`; report modes are internal/external,
inquiry modes additionally allow disabled. External mode requires contactType
and contactValue. Automatic-reply GET/PUT uses the existing BotConfig schema.

GET the conversations list, then POST `{"text":"Reply","clientMessageId":"unique-id"}`
to its messages suffix. Preserve the same ID/body on retries. PATCH inquiry with
`{"status":"solved"}` (waiting/recognised/solved); PUT read with throughSequence.
Message uploads retain the three-image, 15,000,000-byte-per-image limit and
46,048,576-byte body limit. Attachment reads recheck live conversation access.
POST `{"flag":"FLAG_ID","reaction":"fixed","action":"add"}` to flag/reaction
to resolve a report under the existing official-account resolution policy.
These operations grant no XP through the existing external-contribution policy.
