# Backend Architecture Contract (API v2)

This is the normative architecture contract for Musecat Backend Core API v2.
It is deliberately written for both people and AI agents.

- [`openapi.yaml`](openapi.yaml) is the wire-format contract: route, request, response, status, and schema.
- This document is the behavioural contract: authorization, ownership, state transitions, transaction boundaries, and deployment boundaries.
- If the documents disagree, do not guess. Update both in the same change and add a regression test.

API v2 is a deliberate breaking cutover. Existing frontend calls to PocketBase collection REST endpoints are unsupported from this release onward.

## Non-negotiable boundaries

1. Frontends and integrations MUST use documented custom API routes. They MUST NOT use `/api/collections/{collection}/records` for arcade-domain reads or mutations.
2. `arcade` is the aggregate root. `arcade.game_v2` is the only pointer to the current game state; its target batch and revisions are immutable. Changelog rows are immutable audit history.
3. Public access is controlled by `arcade.public`. `closed=true` means a public historical venue, not a private record.
4. Every cross-record arcade mutation MUST use a transaction. External HTTP, notification delivery, and unbounded work MUST NOT occur inside that transaction.
5. Core owns reusable schema and API semantics. Full owns deployment-only migration execution and operational notification delivery.

## Community post prototype

`community_post` is the source of truth for the first community vertical slice.
Its raw PocketBase REST rules are locked; clients use only `GET /community/posts`,
`GET /community/post`, `POST /community/post`, and `PUT /community/post`.

- Phase one labels originals as Korean (`ko-KR`) and assumes Korean authors;
  it does not reject Latin-only game or venue names. Active authenticated users
  may publish, and active posts are publicly readable immediately after persistence.
- An author may edit the original only until `editable_until`, exactly five
  minutes after creation. Editing does not extend the deadline.
- `translate_after` initially equals `editable_until`. The translation worker
  creates English (`en-US`) and Japanese (`ja-JP`) variants from the final
  Korean source after that time; it never delays or removes the Korean post.
- External translation HTTP is outside database transactions. A short claim
  transaction marks one post as processing; a second transaction stores a
  validated result or retry state. Source hashes prevent stale results from
  overwriting a concurrently changed post.
- Translation retries are bounded to five attempts. Provider failures remain
  visible only as translation status while the Korean original stays readable.
- `MUSECAT_COMMUNITY_TRANSLATIONS_PUBLIC` defaults off. Until explicitly
  enabled, `locale=en-US|ja-JP` falls back to the Korean original even when
  translations are ready. This allows translation quality to be verified
  before the non-Korean experience is launched.
- Translation providers are selected only through server environment variables.
  Full development uses DeepSeek V4 Flash in non-thinking JSON mode; the Gemini
  adapter remains available as a fallback. Prompts preserve official arcade,
  game, cabinet, version, product, username, URL, price, and mention values and
  may include an operator-maintained Musecat glossary.

## Visibility and role matrix

Definitions:

- **anonymous**: no valid user token.
- **contributor**: an active authenticated user without a staff role.
- **creator**: the `arcade.createdBy` user.
- **official arcade account**: an authenticated user with the `arcade_owner` tag and the target arcade id in `user.owns`.
- **moderator**: a user tagged `developer` or `moderator`. Supporter tags are not moderators for review decisions.
- **public/open**: `public=true`, `closed=false`.
- **public/closed**: `public=true`, `closed=true`.
- **private**: `public=false` regardless of `closed`.

| Resource / action | Anonymous | Contributor | Creator | Moderator |
| --- | --- | --- | --- | --- |
| Public/open detail, search, changelog, notices | allow | allow | allow | allow |
| Public/closed detail, search, changelog, notices | allow | allow | allow | allow |
| `/user/changelog` rows from public arcades | allow | allow | allow | allow |
| `/user/changelog` rows from a private arcade | deny | deny | rows in own arcades | allow |
| `/rankings` leaderboard | top 100; `viewer=null` | top 100 plus own eligible rank | top 100 plus own eligible rank | top 100 plus own eligible rank |
| `/arcades` operating list | public/open only | public/open only | public/open only | public/open only |
| Nearby, updates, visit, visit stats | public/open only | public/open only | public/open only | public/open only |
| `/arcade/analytics` basic metrics | public/open only | public/open only | public/open only | public/open only |
| `/arcade/analytics` protected metrics (`page_views_by_source`, `series_filter_entries`, `direction_clicks`, `visit_verifications`, `distinct_visitors`) | omitted | omitted | omitted unless official account | allow |
| Private detail via `/arcade` | 404 | 404 | 404 | 404 |
| Private detail via `/arcade/draft` | deny | deny | allow | allow |
| Public-conversion XP preview | deny | deny | creator only | deny |
| My draft list/delete | deny | own drafts only | own drafts only | use specific draft route |
| Immediate wiki edits on public arcade | deny | allow for level 10+ | allow for level 10+ | allow |
| Arcade memo create/update/rollback | deny | authenticated users with arcade write access | authenticated users with arcade write access | authenticated users with arcade write access |
| Arcade notice create | deny | deny; supporters may create unless another official account manages the arcade through `owns` | official account may create only in own `owns` arcade | allow |
| Arcade notice update/delete | deny | own authored notice only | own authored notice only | allow |
| Edit-report create | deny | allow for accessible changelog | allow | allow |
| Review queue and review decision | deny | deny | deny unless tagged | allow |
| Bulk game version update (`POST /arcade/game/bulk_version`) | deny | allow only for level-30 supporter | deny | allow |
| Latest subway map metadata and file bytes | allow | allow | allow | allow |
| Subway map create/update/delete | deny | deny | deny | allow |

Update campaign checks use `POST /campaign/check`. An active authenticated user
may confirm whether a public/open campaign target is still old or updated. A
`still_old` result awards 1 XP once per user and campaign target; an updated
result awards the campaign XP atomically with the game-state mutation. The
default path requires a same-day verified arcade visit. The
optional `bypass_location=true` path is restricted to users tagged
`supporter`, `founding_supporter`, `developer`, or `moderator`; it skips only
the visit-location check and does not relax campaign, arcade, or target
validation. An updated result through the bypass path awards 1 XP instead of
the campaign's configured reward. Clients must show a two-step warning
confirmation before sending the bypass flag. `GET /campaign?id=...` returns
the remaining old-version targets, current `to_version` targets, newest-first
report logs, and the latest `still_old` reporter/time. The updated target view
is derived from the current public game state at the campaign's `to_version`,
not only from campaign `result=updated` checks, so machines already on the
target version are included. The report view includes only currently
public/open arcades.

`GET /arcades/nearby` is the home-page campaign discovery aggregate. Its
response includes `campaigns` for active campaigns with pending targets in
the current filtered page, plus `items[].campaigns` with each arcade's target
counts. Campaign visibility therefore follows the nearby page's existing
address, country, distance, game, and pagination filters; no separate campaign
radius is applied.

The `GET /arcade` public endpoint MUST return `404`, rather than `403`, for every private id. A public/closed arcade remains readable through detail and search but MUST NOT enter operating discovery, nearby, update, or visit flows.

Profile visit data is derived only from currently public arcades (open or
closed), so a venue that later becomes private cannot leak through a visitor's
profile. `visit_visibility=private` omits the public profile's visit summary;
the owner receives it through `GET /user/me`. `summary` exposes one entry per
arcade with its last local visit day and count, while `full` additionally
exposes that arcade's complete local visit-day list. Neither form exposes
raw GPS verification data, accuracy, XP, or visit-record ids. Arcade entries
are ordered by descending visit count and then descending most-recent visit.
Each entry may include `photo_url`, the first publicly readable photo in the
current arcade photo molecule, delivered only through the custom photo-file
route. Country counts, total visit count, distinct arcade count, and total travel
distance are calculated from that same visible arcade visit sequence. Travel
distance is the straight-line sum between consecutive current arcade
locations; a visit whose location is unavailable breaks the sequence rather
than joining the surrounding visits.

Profile countries are stored only through `PUT /user/countries` as an ordered
`user_info.countries` list of ISO 3166-1 alpha-2 codes. The first code is the
representative country. Active contributors below level 15 may save one code;
contributors at level 15 or above may save up to three. This limit is based only
on level and does not depend on supporter or staff tags. A loss of level-15
access does not delete stored choices: public profile reads expose only the
first code while `GET /user/me` retains all saved codes. Compact user DTOs
expose only `primary_country`, and the client renders it with the Dashboard
country icon source in every nickname profile surface whenever a country is
present. The icon is ordered before any supporter or staff badge.

`GET /rankings` always returns the public top 100. User metrics include explorer,
visits, XP, level, and photographer. The `arcade_visits` metric returns public
arcades ranked by the total XP awarded by their completed visit-verification
records; the response displays the verification count separately. Public
closed arcades remain eligible as historical venues, while private arcades are excluded. For
explorer entries, `score` is the number of distinct public arcades and
`stats.travel_distance_km` is the rounded whole-kilometer straight-line distance
between consecutive period-visible arcade locations. A missing location breaks
the distance sequence.
A valid user token may add
only that user's own `viewer` entry, calculated with the same score, tie, and
visibility predicates as the public list; otherwise `viewer` is `null`. In
particular, `private` visit visibility excludes explorer and visit rankings,
including the authenticated user's own entry.

`GET /arcade/ranking` returns up to five users for one public arcade, ranked by
the total XP earned from that arcade's completed visit verifications plus
positive `xp:arcade-edit:<part>:<arcadeId>` changelog grants for all supported
parts (`basic`, `game`, `hour`, `sns`, `gtk`, `photo`, and `memo`). Public closed
arcades remain eligible. Private arcades return `404`; visit XP from users with
`visit_visibility=private` is omitted, while their eligible arcade edit XP is
still ranked. Withdrawn users are omitted.

## Arcade aggregate and history

| Data | Source of truth | Mutation owner | Rules |
| --- | --- | --- | --- |
| `arcade` | aggregate root and current relation pointers | dedicated custom mutation handler | No client writes it through PocketBase REST. |
| `arcade_basic`, `hour`, `sns`, `gtk`, `photo` | versioned molecule for one aggregate section | corresponding `handlers/arcade/<part>` handler | A replacement molecule is created, then the root pointer changes in the same transaction. |
| `arcade.game_v2` | `arcade_game_history_batch` pointer | `PUT /arcade/game`, game rollback | One immutable batch contains all active revisions. `PUT /arcade/game` applies the public `add`/`modify`/`remove` delta after materializing the current batch; rollback changes only this pointer. The API wire fields remain `base_state_id` and `state_id`. |
| `arcade.memo` | `arcade_memo` pointer | `PUT /arcade/memo`, memo rollback | Each save creates an immutable Tiptap JSON revision. Rollback changes only this pointer; authenticated arcade write access is required. |
| `arcade_game_id` | durable installation identity | game mutation handler | `arcade`, `series`, and creator are immutable during normal mutations. Version/location/quantity never live here. Durable features such as `arcade_flag.game_id` reference this ID. The public API uses `modify[].id` and `remove[]`; `add[]` omits IDs. An inactive entry may be reused only for the same arcade, canonical series, and verified cabinet, preserving its flags. The guarded Full catalog migration may reparent `series` only after recording the exact prior row in `game_catalog_migration_origin`; the entry ID and all dependants remain unchanged. |
| `arcade_game_history` | immutable state for one entry in one batch | game mutation handler | `(batch, entry)` is unique. A known cabinet is unique by `(batch, version, cabinet)`; a current (non-imported) unverified cabinet is unique by `(batch, version)`. Historical imported rows may retain multiple unverified entries for one version because the legacy schema had no cabinet identity. Version must belong to entry.series. A missing entry from a batch is removed from that state. |
| `game_series_version_cabinet` | supported cabinet catalog for a canonical game version | guarded catalog migration and later custom catalog handlers | `(version, cabinet)` is unique. `price_default` is the source version's cabinet-specific snapshot when one exists; it may be empty when compatibility is known but no cabinet-specific source price exists. |
| atom collections | data inside a molecule or upload staging | owning part handler | Atoms cannot be directly CRUDed through raw REST. Published photo atoms are immutable. |
| `arcade_changelog` | append-only audit evidence | `arcadeinternal.UpdateArcadeFieldsTxWithLogs` or explicitly documented admin flow | Clients MUST NOT edit/delete rows. `by` is the server-authenticated editor. |
| `arcade_memo` | immutable memo document revision | `PUT /arcade/memo` | Records are never updated or deleted. The current revision is selected by `arcade.memo`; each pointer change writes `changed="memo"` with before/after revision ids. |
| `arcade_request_admin` | support and edit-review queue | custom request/report/review handlers | `reported_editor` is derived from cited changelog; review fields are server-written. |
| `arcade_analytics_event` | append-only public interaction and mutation markers | analytics handler or the owning mutation transaction | No IP, user-agent, or user identity is stored. Multi-series events share one `event_group`; raw REST is locked. |

GTK atom types are server-validated against the closed catalog used by the
fresh bootstrap schema. `SellFood` represents food available for purchase,
and `SeatingArea` represents a place to sit within the arcade. These are normal
boolean facility atoms and accept only the shared `note` field; they do not use
`Parking` metadata.

Rollback is a normal, immediate wiki action. When `report=true`, `POST /arcade/rollback` MUST atomically create the rollback changelog entry and a `rollback_report` linked to the cited prior changelog. A standalone `POST /arcade/edit_report` creates `edit_report`. Neither path bans a user nor performs an automatic rollback beyond the contributor's explicit rollback request.

Both changelog read endpoints enrich immutable evidence with scoped memo
snapshots and currently accessible `photo_assets` references. Snapshot lookup
must verify the referenced record belongs to the changelog's arcade. Photo
references use the same current authorization as `/arcade/photo/file`; gallery
membership removal does not unpublish an immutable photo. Memo snapshot status
distinguishes absent revisions from unavailable evidence. Read enrichment never
rewrites logs. New rollback logs carry `source="rollback"` so clients do not
render revision-pointer changes as ordinary content diffs.

Game mutations require `base_state_id`; a stale value returns `409`. The public request is a delta with required `add`, `modify`, and `remove` arrays. `modify` is a complete replacement object, not a patch, and a single modify cannot delete other active entries. Same-series version changes retain the entry; a cross-series change is rejected. Removed entries remain durable for historical flags, which appear as `orphanFlags` while absent from the selected batch. Re-addition can reuse the newest inactive history match for the same canonical series and verified cabinet; empty/unverified cabinets never reuse an identity.

Every game revision round-trips its canonical `game_cabinet` ID. A non-empty
cabinet is valid only when `game_series_version_cabinet` contains the submitted
version/cabinet pair. An empty cabinet means genuinely unverified cabinet
identity. Game edits and every state-cloning flow, including confirmation,
uncertain rollback, administrative bulk version changes, and generic game
rollback, MUST preserve the cabinet unless that request explicitly changes it.
The expanded `/arcade` game response returns the cabinet catalog object
`{id, en, kr, jp}` for verified revisions and `null` for unverified revisions.
The mutation and filter contracts continue to use the canonical cabinet ID.

`GET /game/catalog?locale=en-US|ko-KR|ja-JP` is the feature-neutral game
catalog read contract. It returns localized series and versions plus only the
explicitly compatible cabinets for each version. Every cabinet row carries its
canonical ID and the `game_series_version_cabinet.price_default` snapshot;
version-level `price_default` remains available as a fallback. Unverified
cabinet identity is intentionally not a catalog row and is represented only as
`null` in an arcade game entry or game mutation input.

`/moderation/game/catalog` is the only game-catalog write boundary. An active
authenticated user with `developer` or `moderator` access, or a
`supporter`/`founding_supporter` who has reached level 30, may create or update
a manufacturer, game series, version, cabinet, or explicit version/cabinet
compatibility. Every request carries a UUID `operation_id`, a 10–500 character
`reason`, and (except creation) the current `expected_revision`; duplicate
operation IDs replay only an identical request from the same actor and stale
revisions return `409`.

Catalog records are never hard-deleted. Archive and restore are revisioned
operations. Archive fails when the record is referenced by a current arcade
installation, active child catalog relation, or active campaign. Every accepted
operation writes one immutable `game_catalog_changelog` record containing the
authenticated actor and tags, reason, before/after snapshots, and revisions.
`GET /moderation/game/catalog/changes` lists that evidence and a guarded
`POST /moderation/game/catalog/changes/revert` records a new revert operation;
it does not erase history. Full owns notifications for those log records.

The `POST /arcade/game/bulk_version` operation is an administrative version
swap for developer/moderator accounts and level-30 supporters. It applies the
same immutable batch and `changed="game"` changelog semantics per affected
arcade as a regular game mutation, does not award XP, and does not maintain any
separate review-state metadata.

Every user-initiated game-state mutation writes one immutable `arcade_changelog` row with `changed="game"`. Its `from` and `to` values are revision-batch IDs; log version 2 contains `state_from`, `state_to`, and an entry-level `before`/`after` snapshot including cabinet. The row's authenticated `by` and `created` are the canonical editor and timestamp for timeline UI. Legacy backfill does not create user-edit changelog rows. Full's legacy game-history import MUST preserve each source `arcade_game.id` as the corresponding history-batch ID so existing game changelog `from`/`to` values remain rollback targets; it must import every molecule and atom before cleanup. The one-time guarded Full game-catalog migration also has no authenticated editor and therefore MUST NOT invent a user changelog row: it preserves the selected batch and revisions, creates a complete immutable shadow batch, records source rows and pointer changes in the locked migration-origin catalog, and switches `arcade.game_v2` atomically.

The catalog cutover may assign a cabinet only from an explicit source-series mapping or an exact reviewed evidence rule. Mixed CHUNITHM rows are represented in the new selected shadow batch as separate Silver and Gold revisions whose quantities sum to the original; the original batch and revision remain untouched. Unknown or contradictory evidence keeps an empty cabinet. A Full release MUST NOT run the pointer-switch data migration until its pinned Core API can round-trip cabinet IDs and permit the same canonical version once per distinct cabinet.

An unresolved report is unique per `(arcade, changelog)` across report kinds. Report text and review notes are limited to 1,200 Unicode characters. The reviewer records `reviewed_by`, `reviewed_at`, `review_outcome`, and `review_note`; resolution only records a decision and MUST NOT silently mutate the cited content.

## API and PocketBase boundary

The PocketBase collection API is persistence infrastructure, not the application API.

`subwayMap` is a versioned public asset store outside the arcade aggregate. Public
clients read the latest regional version through `GET /subway/map` and download
only URLs returned by that response. A developer/moderator may create a new
version, update an explicitly selected version, or hard-delete one version. A
delete deliberately reveals the previous regional version as current. These
single-record operations do not write arcade changelog or XP rows.

- All arcade-domain `list`, `view`, `create`, `update`, and `delete` rules are locked (`nil`) by `1784200000_contract_v2.go`.
- `arcade_photo_atoms` retains only the narrow view rule `public = true && arcade.public = true`; its `photo` field is protected so PocketBase evaluates that rule before direct file delivery. List and mutation remain blocked. Clients MUST NOT treat this as a supported REST record API or construct `/api/files` URLs.
- Custom replacements are:

| Need | API v2 route |
| --- | --- |
| public arcade detail | `GET /arcade?id=...` |
| private creator/staff draft detail | `GET /arcade/draft?id=...` |
| public-conversion XP preview | `GET /arcade/public?arcade=...` |
| own drafts | `GET /arcade/drafts` |
| delete own draft | `DELETE /arcade/draft?id=...` |
| arcade memo | `GET /arcade/memo?arcade=...`, `PUT /arcade/memo` |
| changelog timeline | `GET /arcade/changelog?arcade=...` (optional `changed=basic|game|hour|sns|gtk|photo|memo`) |
| arcade contribution ranking | `GET /arcade/ranking?arcade=...` |
| user-authored changelog timeline | `GET /user/changelog?user=...` (optional `changed=basic|game|hour|sns|gtk|photo|memo`) |
| photo atom list | `GET /arcade/photo/atoms?arcade=...` |
| photo bytes | `GET /arcade/photo/file?id=...` (the `file_url` returned for an atom) |
| delete pending own photo atom | `DELETE /arcade/photo/atom?id=...` |
| standalone edit report | `POST /arcade/edit_report` |
| reviewer queue | `GET /moderation/arcade/edit-reports` |
| reviewer decision | `PUT /moderation/arcade/edit-report` |
| arcade analytics | `GET /arcade/analytics?arcade=...` and `POST /arcade/analytics/event` |
| localized game/version/cabinet catalog | `GET /game/catalog?locale=en-US|ko-KR|ja-JP` |
| protected game catalog management | `GET|POST|PUT|DELETE /moderation/game/catalog`, `POST /moderation/game/catalog/restore`, `GET /moderation/game/catalog/changes`, `POST /moderation/game/catalog/changes/revert` |
| campaign progress and report log | `GET /campaign?id=...` |
| latest regional subway map | `GET /subway/map?region=center|busan` |
| subway map file bytes | `GET /subway/map/file?id=...&field=lightSVG|darkSVG|image|file` (the `file_url` returned by the map response) |
| subway map version management | `POST /subway/map`, `PUT /subway/map`, `DELETE /subway/map?id=...` (developer/moderator only) |

New frontend code MUST NOT reintroduce collection names, PocketBase record rules, collection filters, raw REST pagination, or PocketBase file URL construction as a compatibility layer. Sitemap, changelog timeline, draft list/delete, and photo atom management migrate to these custom routes.

## State transitions and transactions

### Geo and publication

1. New arcade creation and an explicit location change call geo lookup before opening a database transaction.
2. Only successful, valid `country` and IANA `timezone` results enter the bounded geo cache. Failures never enter cache.
3. If geo lookup fails for a new arcade or location change, the request returns `503` and persists no location/aggregate mutation.
4. Public conversion validates the already stored country and IANA timezone. It MUST NOT call external geo HTTP or wait for a network response inside its transaction.
5. A missing or invalid stored geo result rejects public conversion. Repair the location through the normal basic update route first.

### Cache, XP, and notifications

- `/arcades`, `/search`, and `/arcades/nearby` MUST read the same public arcade candidate snapshot. The snapshot derives game membership only from the immutable batch selected by `arcade.game_v2` and retains revision-level `(series, cabinet)` pairs rather than independent sets.
- `/arcades/nearby` accepts grouped `game_filter` values for cabinet-aware filtering. Each repeated value is a JSON object with a `series` id and an optional `cabinet` id; omitting `cabinet` means all cabinets for that series, and separate series groups use AND semantics. Grouped and legacy game parameters cannot be mixed. The legacy `game_series`/`game_cabinet` pair remains supported for compatibility: series-only filters ignore cabinet identity, while repeated and comma-separated values preserve positional `(series, cabinet)` pairs and require equal lengths. Invalid grouped filters, cabinet-only legacy requests, or unequal legacy list lengths return `400`.
- Nearby remains an operating-discovery route: private and public/closed arcades, historical unselected batches, and unverified cabinet revisions for a cabinet-qualified pair MUST NOT affect results, pagination, country totals, or nearest-arcade summaries.
- Candidate invalidation follows changes to arcade/basic data, `arcade.game_v2`, current game entries/revisions, versions, cabinets, and version/cabinet compatibility records.
- `/arcades` is paginated `{page, per_page, last_page, total, items}` and includes only public/open candidates. Search intentionally includes public/closed candidates.
- `GET /arcade/public?arcade=...` is a creator-only, read-only XP estimate. It uses the same idempotent public and draft-backfill grant keys as `PUT /arcade/public`, writes no visibility or ledger state, and the successful PUT response is authoritative if the draft changes afterward. Draft backfill is independent of the seven-day arcade-edit cooldown: each changed area earns its backfill once per arcade, regardless of how recently that area received normal edit XP.
- Public conversion remains creator-only. A creator tagged `supporter`, `founding_supporter`, `developer`, or `moderator` may set `bypass_requirements=true` on `PUT /arcade/public` to skip only the game, contact-or-hours, and Korea facility-photo publication requirements. Stored country/timezone validation, private/open state validation, and all other public-conversion checks remain mandatory. Clients must show a two-step warning confirmation before sending this flag.
- XP ledger changes and aggregate mutations belong to the same transaction. No XP grant may survive a failed aggregate mutation.
- XP is available only after the authenticated user has a non-empty `username`. Before one-time username setup, XP-producing actions retain their normal mutation semantics where applicable but award `0`; they must not create `user_level` or `user_level_log` state, and public-conversion XP previews must report no eligible XP.
- Normal edit XP remains scoped by user, arcade, and part. Basic/hour/sns/gtk/photo edits continue to grant 3 XP with their existing seven-day cooldown. Game edits use a rolling seven-day window of distinct durable `arcade_game_id` values: the target is `min(10, 2*n + 1)` and each request receives only the increase over XP already granted in that window. Revisiting an entry already counted in the window grants 0; entries become eligible again after they leave the window. Administrative bulk game-version updates and public-conversion backfill do not use this scale.
- Notification delivery is after persistence and best-effort. A Telegram/Discord failure MUST NOT roll back a completed user request.
- Review processing has no automated ban and no automated rollback.
- Public `GET /arcade` detail loads record one `page_view` event best-effort. The direction-click event route is anonymous and accepts only `direction_click`.
- A successful flag creation records a `fault_report` marker in the same transaction; its flag id preserves cumulative counting after the user deletes the flag.
- Flag resolution is owned by Core. `arcade_flag.resolution_vote_state` is `idle|active`; the first current-round `fixed` reaction starts a round, while `issue_persist` before that point appends report history and starts the next report round semantics. During an active round, `fixed` and `wrong` are one mutually exclusive vote per user, with `level_snapshot` captured at creation. The score is the fixed snapshot-level sum minus the wrong snapshot-level sum. A fixed voter keeps a zero score active for 72 hours; a negative score or zero fixed voters closes the round. The delay is 15 minutes at 30+, 3 hours at 20–29, 24 hours at 10–19, 48 hours at 5–9, and 72 hours at 0–4. Every score-changing event calculates a candidate deadline from the server event time, but an active flag keeps the earlier of its existing `resolveAt` and that candidate so a vote can never postpone resolution. Successful flag reactions refresh the flag's `updated` activity timestamp. A flag with no reaction activity for 120 days is opened by the nightly stale sweep as a zero-score, 72-hour resolution window; this window is allowed to remain active without a user fixed vote until it receives a user vote or reaches its deadline. Legacy reactions remain stored with `resolution_context=legacy` and never enter new-round totals. Deadline reconciliation runs inside reaction transactions and the every-minute Core cron, while the 120-day stale sweep runs once per night; tagged-user immediate resolution is not supported.
- The flag read and reaction mutation responses include the authoritative `resolution` summary, `myVote`, and newest-first `reportHistory`. Clients may calculate only display countdown/progress from `resolveAt`; they MUST NOT infer score, thresholds, rounds, or solve state from raw reaction counts.
- Analytics responses always include only `page_views`, `fault_reports`, and `edit_count` for anonymous/contributor callers. `page_views_by_source`, `series_filter_entries`, `direction_clicks`, `visit_verifications`, and `distinct_visitors` are omitted unless the caller is an official arcade account or a `developer|moderator`.

## Core and Full migration boundary

Core's migrations define the reusable schema/API contract and are verified only
against a fresh Core bootstrap database. **Full MUST NOT import Core's bootstrap
migration package**: existing Full installations have an independent migration
history.

Core MUST NOT contain a migration that transforms existing production or Full
rows. If a release requires data backfill, catalog conversion, pointer changes,
or any other transformation of an existing Full database, Backend Full owns a
separate guarded forward migration for that operation. Core may document the
required preconditions and expose the API/schema needed by that migration, but
the data migration itself belongs in Full.

Schema-only contract changes in Core must remain safe for a fresh bootstrap and
must not be described as a Full migration. Full applies its own local schema or
data migration according to its independent deployment history; it must never
execute Core's bootstrap package or fabricate a Core migration record.

Deployment order is mandatory: first publish the Core release containing the
custom API/schema contract; then bump Full to that exact contract and apply any
required Full-owned forward migration. Full MUST NOT apply a data migration
until the pinned Core API can round-trip the new fields and enforce its
preconditions.

## Frontend response and error contract

| Endpoint class | Required behaviour |
| --- | --- |
| public detail/search | private ids are `404`; public/closed remains visible |
| operating discovery | excludes closed and private records |
| authenticated draft API | `401` for missing token, `404` for inaccessible private id, `403` only for an identified forbidden mutation |
| list pagination | clients consume `page`, `per_page`, `last_page`, `total`, and `items`; never infer a full dataset from one page |
| validation | `400` for malformed/invalid client input, `409` for state conflicts (published draft delete, duplicate report, published atom delete), `503` for required geo dependency failure |
| review | only `developer|moderator`; outcomes are `upheld`, `dismissed`, or `actioned` |

## AI change checklist

Before changing code, an AI agent MUST classify the change and complete every applicable item.

| Change type | Required work |
| --- | --- |
| route/request/response/status | update `docs/openapi.yaml`, this contract if policy changes, handler tests, and examples |
| visibility/role/read rule | update matrix, custom handler tests, raw REST regression tests, and OpenAPI `404/403` documentation |
| arcade schema/rules | update Core's fresh-bootstrap schema/API contract; specify any Full-owned forward/data migration separately; test Core on a fresh database |
| aggregate mutation | identify source of truth, transaction boundary, changelog effect, XP effect, and cache invalidation; add atomicity tests |
| geo/cache/network behaviour | prove no external request is inside the transaction; test cache hit/failure and persistence rollback |
| review workflow | validate parent arcade/changelog, derive editor server-side, enforce 1,200-char and duplicate rules, test strict reviewer authorization |
| frontend migration | link only custom endpoints and remove raw collection/rule/filter references |

Do not implement an ambiguous policy from inference. Record the decision in this document and OpenAPI before relying on it.


## Passport

`GET /user/passport?year=all|YYYY` and `GET /user/passport/stamps` are active-owner-only,
no-store APIs. Both and public `visit_stats` use `LoadPassport`, selecting current
public arcades including closed venues. Private venues disappear from every aggregate.
The public payload adds only all-time city aggregates, never first visit dates or
owner-only month/weekday/stamp details. Existing private/summary/full visibility applies.

Periods use stored local `visit_day`; active days deduplicate those date strings even
across countries. New discoveries use lifetime first chronological verification.
Distances use current locations, with both consecutive endpoints inside the selected
period; missing locations and excluded period records break a segment. Totals are
straight-line distances, not actual travel or play time. Month series include zero months.

Canonical `passport_city` records use GeoNames IDs as unique source identifiers; raw
REST is locked. `arcade_basic.city_id` is optional and versioned with basic history.
Current city metadata applies retrospectively. A missing or country-mismatched city is
unclassified; those arcades remain in country and venue totals. Cities are matched by
country, admin1 and locality aliases, never proximity. Ambiguity requires operator review.
GeoNames import and candidate review are explicit Full operations, outside requests and
transactions. Core's schema migration bootstraps only a fresh test database.

`POST /arcade/visit` additionally returns `first_visit_to_arcade`, true only for the newly
committed first visit; duplicate same-day requests return false and never award another
stamp or XP. Existing 6/3 XP remains unchanged. Passport is GPS visit evidence, not play evidence.
