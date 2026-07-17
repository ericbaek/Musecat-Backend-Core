# Backend Architecture Contract (API v2)

This is the normative architecture contract for Musecat Backend Core API v2.
It is deliberately written for both people and AI agents.

- [`openapi.yaml`](openapi.yaml) is the wire-format contract: route, request, response, status, and schema.
- This document is the behavioural contract: authorization, ownership, state transitions, transaction boundaries, and deployment boundaries.
- If the documents disagree, do not guess. Update both in the same change and add a regression test.

API v2 is a deliberate breaking cutover. Existing frontend calls to PocketBase collection REST endpoints are unsupported from this release onward.

## Non-negotiable boundaries

1. Frontends and integrations MUST use documented custom API routes. They MUST NOT use `/api/collections/{collection}/records` for arcade-domain reads or mutations.
2. `arcade` is the aggregate root. `arcade.game_state` is the only pointer to the current game state; its target batch and revisions are immutable. Changelog rows are immutable audit history.
3. Public access is controlled by `arcade.public`. `closed=true` means a public historical venue, not a private record.
4. Every cross-record arcade mutation MUST use a transaction. External HTTP, notification delivery, and unbounded work MUST NOT occur inside that transaction.
5. Core owns reusable schema and API semantics. Full owns deployment-only migration execution and operational notification delivery.

## Visibility and role matrix

Definitions:

- **anonymous**: no valid user token.
- **contributor**: an active authenticated user without a staff role.
- **creator**: the `arcade.createdBy` user.
- **moderator**: a user tagged `developer` or `moderator`. Supporter tags are not moderators for review decisions.
- **public/open**: `public=true`, `closed=false`.
- **public/closed**: `public=true`, `closed=true`.
- **private**: `public=false` regardless of `closed`.

| Resource / action | Anonymous | Contributor | Creator | Moderator |
| --- | --- | --- | --- | --- |
| Public/open detail, search, changelog, notices | allow | allow | allow | allow |
| Public/closed detail, search, changelog, notices | allow | allow | allow | allow |
| `/arcades` operating list | public/open only | public/open only | public/open only | public/open only |
| Nearby, updates, visit, visit stats | public/open only | public/open only | public/open only | public/open only |
| Private detail via `/arcade` | 404 | 404 | 404 | 404 |
| Private detail via `/arcade/draft` | deny | deny | allow | allow |
| My draft list/delete | deny | own drafts only | own drafts only | use specific draft route |
| Immediate wiki edits on public arcade | deny | allow | allow | allow |
| Edit-report create | deny | allow for accessible changelog | allow | allow |
| Review queue and review decision | deny | deny | deny unless tagged | allow |

The `GET /arcade` public endpoint MUST return `404`, rather than `403`, for every private id. A public/closed arcade remains readable through detail and search but MUST NOT enter operating discovery, nearby, update, or visit flows.

## Arcade aggregate and history

| Data | Source of truth | Mutation owner | Rules |
| --- | --- | --- | --- |
| `arcade` | aggregate root and current relation pointers | dedicated custom mutation handler | No client writes it through PocketBase REST. |
| `arcade_basic`, `hour`, `sns`, `gtk`, `photo` | versioned molecule for one aggregate section | corresponding `handlers/arcade/<part>` handler | A replacement molecule is created, then the root pointer changes in the same transaction. |
| `arcade.game_state` | `arcade_game_revision_batch` pointer | `PUT /arcade/game`, game rollback | One immutable batch contains all active revisions. Rollback changes only this pointer. |
| `arcade_game_entry` | durable installation identity | game mutation handler | `arcade`, `series`, and creator are immutable. Version/location/quantity never live here. Durable features such as flags reference this ID. |
| `arcade_game_revision` | immutable state for one entry in one batch | game mutation handler | `(batch, entry)` and `(batch, version)` are unique. Version must belong to entry.series. A missing entry from a batch is removed from that state. |
| atom collections | data inside a molecule or upload staging | owning part handler | Atoms cannot be directly CRUDed through raw REST. Published photo atoms are immutable. |
| `arcade_changelog` | append-only audit evidence | `arcadeinternal.UpdateArcadeFieldsTxWithLogs` or explicitly documented admin flow | Clients MUST NOT edit/delete rows. `by` is the server-authenticated editor. |
| `arcade_request_admin` | support and edit-review queue | custom request/report/review handlers | `reported_editor` is derived from cited changelog; review fields are server-written. |

Rollback is a normal, immediate wiki action. When `report=true`, `POST /arcade/rollback` MUST atomically create the rollback changelog entry and a `rollback_report` linked to the cited prior changelog. A standalone `POST /arcade/edit_report` creates `edit_report`. Neither path bans a user nor performs an automatic rollback beyond the contributor's explicit rollback request.

Game mutations require `base_state_id`; a stale value returns `409`. Existing `games[].id` values are stable entry IDs, while rows without one create a new entry. Same-series version changes retain the entry; a cross-series change is rejected. Removed entries remain durable for historical flags, which appear as `orphanFlags` while absent from the selected batch.

Every game-state mutation writes one immutable `arcade_changelog` row with `changed="game"`. Its `from` and `to` values are revision-batch IDs; log version 2 contains `state_from`, `state_to`, and an entry-level `before`/`after` snapshot. The row's authenticated `by` and `created` are the canonical editor and timestamp for timeline UI. Legacy backfill does not create user-edit changelog rows.

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
| changelog timeline | `GET /arcade/changelog?arcade=...` |
| photo atom list | `GET /arcade/photo/atoms?arcade=...` |
| photo bytes | `GET /arcade/photo/file?id=...` (the `file_url` returned for an atom) |
| delete pending own photo atom | `DELETE /arcade/photo/atom?id=...` |
| standalone edit report | `POST /arcade/edit_report` |
| reviewer queue | `GET /moderation/arcade/edit-reports` |
| reviewer decision | `PUT /moderation/arcade/edit-report` |

New frontend code MUST NOT reintroduce collection names, PocketBase record rules, collection filters, raw REST pagination, or PocketBase file URL construction as a compatibility layer. Sitemap, changelog timeline, draft list/delete, and photo atom management migrate to these custom routes.

## State transitions and transactions

### Geo and publication

1. New arcade creation and an explicit location change call geo lookup before opening a database transaction.
2. Only successful, valid `country` and IANA `timezone` results enter the bounded geo cache. Failures never enter cache.
3. If geo lookup fails for a new arcade or location change, the request returns `503` and persists no location/aggregate mutation.
4. Public conversion validates the already stored country and IANA timezone. It MUST NOT call external geo HTTP or wait for a network response inside its transaction.
5. A missing or invalid stored geo result rejects public conversion. Repair the location through the normal basic update route first.

### Cache, XP, and notifications

- `/arcades`, `/search`, and `/arcades/nearby` MUST read the same public arcade candidate snapshot. Candidate invalidation follows arcade/basic/game/atom/version changes.
- `/arcades` is paginated `{page, per_page, last_page, total, items}` and includes only public/open candidates. Search intentionally includes public/closed candidates.
- XP ledger changes and aggregate mutations belong to the same transaction. No XP grant may survive a failed aggregate mutation.
- Notification delivery is after persistence and best-effort. A Telegram/Discord failure MUST NOT roll back a completed user request.
- Review processing has no automated ban and no automated rollback.

## Core and Full migration boundary

Core's `0000000000_initial_schema.go` is a bootstrap snapshot for a fresh Core database. **Full MUST NOT import that package**: existing Full installations have independent migration history.

Every shared forward schema change must be represented by matching guarded migrations:

1. Core: `migrations/1784200000_contract_v2.go`.
2. Full: an equivalent local migration with the same guarded operations.

The migration must check field/index existence before adding it and must be safe on both a fresh Core database and an existing Full history. Do not fake a bootstrap migration record, reset migration history, or import Core's bootstrap into Full.

Deployment order is mandatory: first publish the Core release containing the
custom API v2 handlers and this migration; then bump Full to that exact Core
release; finally apply Full's local forward migration. Full MUST NOT apply the
raw-REST lock while it is linked to an earlier Core release, because that would
remove the deprecated fallback before the custom replacement endpoints exist.

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
| arcade schema/rules | add guarded Core and Full forward migrations; do not alter deployed migration history; test fresh Core and Full-history forward application |
| aggregate mutation | identify source of truth, transaction boundary, changelog effect, XP effect, and cache invalidation; add atomicity tests |
| geo/cache/network behaviour | prove no external request is inside the transaction; test cache hit/failure and persistence rollback |
| review workflow | validate parent arcade/changelog, derive editor server-side, enforce 1,200-char and duplicate rules, test strict reviewer authorization |
| frontend migration | link only custom endpoints and remove raw collection/rule/filter references |

Do not implement an ambiguous policy from inference. Record the decision in this document and OpenAPI before relying on it.
