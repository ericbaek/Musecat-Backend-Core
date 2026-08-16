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
| My draft list/delete | deny | own drafts only | own drafts only | use specific draft route |
| Immediate wiki edits on public arcade | deny | allow | allow | allow |
| Arcade notice create | deny | deny; supporters may create unless another official account manages the arcade through `owns` | official account may create only in own `owns` arcade | allow |
| Arcade notice update/delete | deny | own authored notice only | own authored notice only | allow |
| Edit-report create | deny | allow for accessible changelog | allow | allow |
| Review queue and review decision | deny | deny | deny unless tagged | allow |
| Bulk game version update (`POST /arcade/game/bulk_version`) | deny | deny | deny | allow |

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

`GET /rankings` always returns the public top 100. A valid user token may add
only that user's own `viewer` entry, calculated with the same score, tie, and
visibility predicates as the public list; otherwise `viewer` is `null`. In
particular, `private` visit visibility excludes explorer and visit rankings,
including the authenticated user's own entry.

## Arcade aggregate and history

| Data | Source of truth | Mutation owner | Rules |
| --- | --- | --- | --- |
| `arcade` | aggregate root and current relation pointers | dedicated custom mutation handler | No client writes it through PocketBase REST. |
| `arcade_basic`, `hour`, `sns`, `gtk`, `photo` | versioned molecule for one aggregate section | corresponding `handlers/arcade/<part>` handler | A replacement molecule is created, then the root pointer changes in the same transaction. |
| `arcade.game_v2` | `arcade_game_history_batch` pointer | `PUT /arcade/game`, game rollback | One immutable batch contains all active revisions. Rollback changes only this pointer. The API wire fields remain `base_state_id` and `state_id`. |
| `arcade_game_id` | durable installation identity | game mutation handler | `arcade`, `series`, and creator are immutable during normal mutations. Version/location/quantity never live here. Durable features such as `arcade_flag.game_id` reference this ID. The API wire fields remain `games[].id` and `game_id`. The guarded Full catalog migration may reparent `series` only after recording the exact prior row in `game_catalog_migration_origin`; the entry ID and all dependants remain unchanged. |
| `arcade_game_history` | immutable state for one entry in one batch | game mutation handler | `(batch, entry)` is unique. A known cabinet is unique by `(batch, version, cabinet)`; a current (non-imported) unverified cabinet is unique by `(batch, version)`. Historical imported rows may retain multiple unverified entries for one version because the legacy schema had no cabinet identity. Version must belong to entry.series. A missing entry from a batch is removed from that state. |
| `game_series_version_cabinet` | supported cabinet catalog for a canonical game version | guarded catalog migration and later custom catalog handlers | `(version, cabinet)` is unique. `price_default` is the source version's cabinet-specific snapshot when one exists; it may be empty when compatibility is known but no cabinet-specific source price exists. |
| atom collections | data inside a molecule or upload staging | owning part handler | Atoms cannot be directly CRUDed through raw REST. Published photo atoms are immutable. |
| `arcade_changelog` | append-only audit evidence | `arcadeinternal.UpdateArcadeFieldsTxWithLogs` or explicitly documented admin flow | Clients MUST NOT edit/delete rows. `by` is the server-authenticated editor. |
| `arcade_request_admin` | support and edit-review queue | custom request/report/review handlers | `reported_editor` is derived from cited changelog; review fields are server-written. |
| `arcade_analytics_event` | append-only public interaction and mutation markers | analytics handler or the owning mutation transaction | No IP, user-agent, or user identity is stored. Multi-series events share one `event_group`; raw REST is locked. |

GTK atom types are server-validated against the closed catalog used by the
fresh bootstrap schema. `ATM` is a normal boolean facility atom and accepts
only the shared `note` field; it does not use `Parking` metadata.

Rollback is a normal, immediate wiki action. When `report=true`, `POST /arcade/rollback` MUST atomically create the rollback changelog entry and a `rollback_report` linked to the cited prior changelog. A standalone `POST /arcade/edit_report` creates `edit_report`. Neither path bans a user nor performs an automatic rollback beyond the contributor's explicit rollback request.

Game mutations require `base_state_id`; a stale value returns `409`. Existing `games[].id` values are stable entry IDs, while rows without one create a new entry. Same-series version changes retain the entry; a cross-series change is rejected. Removed entries remain durable for historical flags, which appear as `orphanFlags` while absent from the selected batch.

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

The developer/moderator-only `POST /arcade/game/bulk_version` operation is an administrative version swap. It applies the same immutable batch and `changed="game"` changelog semantics per affected arcade as a regular game mutation, does not award XP, and does not maintain any separate review-state metadata.

Every user-initiated game-state mutation writes one immutable `arcade_changelog` row with `changed="game"`. Its `from` and `to` values are revision-batch IDs; log version 2 contains `state_from`, `state_to`, and an entry-level `before`/`after` snapshot including cabinet. The row's authenticated `by` and `created` are the canonical editor and timestamp for timeline UI. Legacy backfill does not create user-edit changelog rows. Full's legacy game-history import MUST preserve each source `arcade_game.id` as the corresponding history-batch ID so existing game changelog `from`/`to` values remain rollback targets; it must import every molecule and atom before cleanup. The one-time guarded Full game-catalog migration also has no authenticated editor and therefore MUST NOT invent a user changelog row: it preserves the selected batch and revisions, creates a complete immutable shadow batch, records source rows and pointer changes in the locked migration-origin catalog, and switches `arcade.game_v2` atomically.

The catalog cutover may assign a cabinet only from an explicit source-series mapping or an exact reviewed evidence rule. Mixed CHUNITHM rows are represented in the new selected shadow batch as separate Silver and Gold revisions whose quantities sum to the original; the original batch and revision remain untouched. Unknown or contradictory evidence keeps an empty cabinet. A Full release MUST NOT run the pointer-switch data migration until its pinned Core API can round-trip cabinet IDs and permit the same canonical version once per distinct cabinet.

An unresolved report is unique per `(arcade, changelog)` across report kinds. Report text and review notes are limited to 1,200 Unicode characters. The reviewer records `reviewed_by`, `reviewed_at`, `review_outcome`, and `review_note`; resolution only records a decision and MUST NOT silently mutate the cited content.

## API and PocketBase boundary

The PocketBase collection API is persistence infrastructure, not the application API.

- All arcade-domain `list`, `view`, `create`, `update`, and `delete` rules are locked (`nil`) by `1784200000_contract_v2.go`.
- `arcade_photo_atoms` retains only the narrow view rule `public = true && arcade.public = true`; its `photo` field is protected so PocketBase evaluates that rule before direct file delivery. List and mutation remain blocked. Clients MUST NOT treat this as a supported REST record API or construct `/api/files` URLs.
- Custom replacements are:

| Need | API v2 route |
| --- | --- |
| public arcade detail | `GET /arcade?id=...` |
| private creator/staff draft detail | `GET /arcade/draft?id=...` |
| own drafts | `GET /arcade/drafts` |
| delete own draft | `DELETE /arcade/draft?id=...` |
| changelog timeline | `GET /arcade/changelog?arcade=...` (optional `changed=basic|game|hour|sns|gtk|photo`) |
| user-authored changelog timeline | `GET /user/changelog?user=...` (optional `changed=basic|game|hour|sns|gtk|photo`) |
| photo atom list | `GET /arcade/photo/atoms?arcade=...` |
| photo bytes | `GET /arcade/photo/file?id=...` (the `file_url` returned for an atom) |
| delete pending own photo atom | `DELETE /arcade/photo/atom?id=...` |
| standalone edit report | `POST /arcade/edit_report` |
| reviewer queue | `GET /moderation/arcade/edit-reports` |
| reviewer decision | `PUT /moderation/arcade/edit-report` |
| arcade analytics | `GET /arcade/analytics?arcade=...` and `POST /arcade/analytics/event` |
| localized game/version/cabinet catalog | `GET /game/catalog?locale=en-US|ko-KR|ja-JP` |

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
- `/arcades/nearby` accepts series-only filtering without regard to cabinet identity. If `game_cabinet` is present, `game_series` is required, repeated and comma-separated values preserve their input order, the expanded lists must have equal lengths, and each positional `(series, cabinet)` pair must match the same current revision. Every requested pair uses AND semantics. Cabinet-only or unequal-length requests return `400`.
- Nearby remains an operating-discovery route: private and public/closed arcades, historical unselected batches, and unverified cabinet revisions for a cabinet-qualified pair MUST NOT affect results, pagination, country totals, or nearest-arcade summaries.
- Candidate invalidation follows changes to arcade/basic data, `arcade.game_v2`, current game entries/revisions, versions, cabinets, and version/cabinet compatibility records.
- `/arcades` is paginated `{page, per_page, last_page, total, items}` and includes only public/open candidates. Search intentionally includes public/closed candidates.
- XP ledger changes and aggregate mutations belong to the same transaction. No XP grant may survive a failed aggregate mutation.
- Notification delivery is after persistence and best-effort. A Telegram/Discord failure MUST NOT roll back a completed user request.
- Review processing has no automated ban and no automated rollback.
- Public `GET /arcade` detail loads record one `page_view` event best-effort. The direction-click event route is anonymous and accepts only `direction_click`.
- A successful flag creation records a `fault_report` marker in the same transaction; its flag id preserves cumulative counting after the user deletes the flag.
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
