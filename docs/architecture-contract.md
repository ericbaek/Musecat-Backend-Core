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

The fresh Core bootstrap enables password authentication for the `user` auth
collection with email and username as its identities. This is a collection-wide
authentication setting; the test-account endpoint does not change it. Full keeps existing
installations aligned through its guarded forward migration.

## Product and deployment composition

| Concern | Core | Full (deployment application) |
| --- | --- | --- |
| API, authorization, visibility, XP, history | Own implementation and contract | Consume Core without parallel handlers |
| Schema | Reusable schema and fresh bootstrap only | Guarded forward migrations for existing data |
| Background work | Domain transitions and reusable worker/provider adapters | Choose providers, credentials, and register schedules |
| Runtime | Reference runner for a fresh standalone instance | Compose production resources and operational hooks |
| Documentation | Canonical OpenAPI embedded in the module | Serve the exact contract from the compiled Core dependency |
| Release | Test and publish Core independently | Pin, test, migrate, and deploy the selected version |

`coreapp.Configure` accepts a `Config` with an offline geo resolver and explicit
`DocumentationConfig`. It installs domain hooks and routes, but does not read
environment variables, register a migration runner, or register cron jobs.
The executables own those decisions in `main.go` and `runtime.go`. A translation
job is registered only with an injected `community.Translator`; credentials are
read once at startup. Missing translation credentials disable that job. Invalid
configured resources fail startup. Core's reference runner supports an explicit
OpenAPI file override for development; Full always serves the embedded contract.
Documentation credentials are optional, but supplying only one credential fails
closed for all documentation routes.

A local workspace is an explicit development opt-in. Release builds and CI use
`GOWORK=off` and Full's `go.mod`; an adjacent Core checkout never selects the
contract implicitly. Full must pin a Core revision containing the APIs it uses
before a release. Core CI must not require access to the private Full repository.

## Public endpoint request limits

Active `user_ban` records block OAuth sign-in by the provider email hash or
the matched user's record and email hash. Password sign-in and auth refresh
also check the matched user. The OAuth and refresh auth responses include
`ACCOUNT_BANNED`, reason, expiry, and permanence so clients can explain the
restriction. `POST /user/signup` returns the same ban details with 403 when
an authenticated account becomes banned before signup is completed, including
an active withdrawal cooldown on an already withdrawn account.

Fresh Core databases enable per-client-IP limits for anonymous and authenticated
users together: `GET /geocode` and `GET /reverse_geocode` allow 30 requests per
60 seconds each, `GET /geo` allows 60 per 60 seconds, and
`POST /support_feedback` allows 10 per 600 seconds. Exceeding a limit returns
`429` with the PocketBase error envelope. Switching authentication state does
not reset the default IP bucket. PocketBase superuser and excluded-IP exemptions
still apply.

At server startup, Core enables rate limiting and adds missing all-user rules
without replacing existing rules or their configured thresholds. Existing
operator-defined audience-specific rules retain their precedence. This runtime
configuration also covers Full installations without importing Core migrations.

`POST /support_feedback` accepts at most three images, each at most 15,000,000
bytes. Its encoded body ceiling is 46,048,576 bytes (three files plus 1 MiB for
form fields and multipart overhead). File and body overflow return `413`,
including streamed requests without Content-Length. The web BFF enforces the
same byte ceiling before parsing the body. Creation includes `created` and
`updated` timestamps, matching the feedback mapper. Raw collection capacity is
not the public upload contract. The strict reviewer-only GET queue is unchanged.

## Supporter application review

`GET /moderation/supporter/requests`, `GET /moderation/supporter/request?id=...`,
and `PUT /moderation/supporter/request` require an active developer or moderator.
Supporter roles alone cannot read applicant ledgers or make decisions. Detail
reads include the applicant's joined date, current XP ledger, actual authored
changelog count, and edits grouped by arcade, including private venues visible
to strict reviewers. Edit counts are not XP grant counts and retain rate-limited
contributions. The saved `score_total` remains the application-time snapshot.
Ledger entries resolve verified visit records to their arcade ID and name, so
reviewers can include visit grants in per-arcade earned XP. Missing source records
keep their XP in the ledger without inventing an arcade association.

A decision changes only a pending application to approved or rejected and stores
`decision_reason`. Approval appends `founding_supporter` to `user.tags`, preserving
existing tags and avoiding duplicates. Both saves run in one transaction; a tag
capacity or persistence failure leaves the application pending. Rejection does
not grant or remove roles. Repeated decisions return 409; missing or withdrawn
applicants cannot be approved. These are user-role/application mutations, so no
arcade changelog row is written. Core adds no production migration: the fields
and role value already exist in the shared schema.

## Geocoding cache identity

Geocoding caches encode query, region, and mode as separate components. Forward
responses preserve the exact trimmed query, including its case. Reverse lookup
keys retain full coordinate precision; nearby positions must not reuse another
position's address or response coordinates. Non-finite coordinates are rejected.

## Community post prototype

`community_post` is the source of truth for the first community vertical slice.
Its raw PocketBase REST rules are locked; clients use only `GET /community/posts`,
`GET /community/post`, `POST /community/post`, `PUT /community/post`, and `DELETE /community/post`.

- List reads accept repeated `game_series` IDs and apply their union before
  counting and pagination. Omitted filters include all active posts.
- Phase one labels originals as Korean (`ko-KR`) and assumes Korean authors;
  it does not reject Latin-only game or venue names. Active authenticated users
  may publish, and active posts are publicly readable immediately after persistence.
- An author may edit the original only until `editable_until`, exactly five
  minutes after creation. Editing does not extend the deadline.
- An active author may delete a post at any time. Deletion retains a private
  tombstone, clears public photos and cached translations, and excludes the post
  from public detail, feeds, galleries, counts and translation jobs. An author
  retry returns 204. Other users cannot edit or delete the post.
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
- Translation providers are selected by the executable and injected into the
  worker. The reference runner reads server environment variables; reusable
  OpenAI is the only translation adapter; `OPENAI_API_KEY` selects availability.
  The default model is `gpt-6-luna` with reasoning effort `none` (`low` is optional). Prompts preserve official arcade,
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
| Public/open detail, search, notices | allow | allow | allow | allow |
| Public/closed detail, search, notices | allow | allow | allow | allow |
| `/arcade/changelog` detailed public history | deny | allow | allow | allow |
| `/user/changelog` public arcade rows | summaries | details | details | details |
| `/user/changelog` private arcade rows | deny | deny | details in own arcades | details |
| `/rankings` leaderboard | top 100; `viewer=null` | top 100 plus own eligible rank | top 100 plus own eligible rank | top 100 plus own eligible rank |
| `/arcades` operating list | public/open only | public/open only | public/open only | public/open only |
| Nearby, updates, visit, visit stats | public/open only | public/open only | public/open only | public/open only |
| `/arcade/analytics` basic metrics | public/open only | public/open only | public/open only | public/open only |
| `/arcade/analytics` breakdowns (`page_views_by_source`, `series_filter_entries`) | omitted | level 15+ and page views > 1000 | level 15+ and page views > 1000, or official account | allow |
| `/arcade/analytics` protected metrics (`direction_clicks`, `visit_verifications`, `distinct_visitors`) | omitted | omitted | omitted unless official account | allow |
| Private detail via `/arcade` | 404 | 404 | 404 | 404 |
| Private detail via `/arcade/draft` | deny | deny | allow | allow |
| Public-conversion XP preview | deny | deny | creator only | deny |
| My draft list/delete | deny | own drafts only | own drafts only | use specific draft route |
| Immediate wiki edits on public arcade | deny | allow | allow | allow |
| Arcade memo create/update/rollback | deny | authenticated users with arcade write access | authenticated users with arcade write access | authenticated users with arcade write access |
| Arcade notice create | deny | deny; level-30+ supporters may create unless another official account manages the arcade through `owns` | official account may create only in own `owns` arcade | allow |
| Arcade notice update/delete | deny | own authored notice only for level-30+ supporters | own authored notice only | allow |
| Edit-report create | deny | allow for accessible changelog | allow | allow |
| Review queue and review decision | deny | deny | deny unless tagged | allow |
| Own feedback (`GET /user/feedback`) | deny | own records only | own records only | own records only |
| Support feedback queue (`GET /support_feedback`) | deny | deny | deny | active developer or moderator only |
| Bulk game version update (`POST /arcade/game/bulk_version`) | deny | deny | deny | allow |
| Latest subway map metadata and file bytes | allow | allow | allow | allow |
| Subway map create/update/delete | deny | deny | deny | allow |

Public arcade wiki edits have no level threshold for active authenticated users.
Private draft edits, photo uploads, flag/notice mutations, and rollbacks require
the draft creator or a `developer|moderator`. Aggregate mutations recheck this
against the current arcade record inside their transaction.

Unauthenticated (anonymous) callers of `GET /arcade` receive redacted sensitive payloads:
- When `expand=game`, each item has `tag: null`, `location: ""`, `updated: ""`, and `updated_by: ""`. `price` retains only representative price items (`represent=true`, falling back to the first item for legacy rows), sets `accept: null`, and marks `hasHiddenPrices=true` when additional items are hidden.
- When `expand=hour`, the response retains only today's schedule for the arcade's resolved timezone and `Note`, omitting all other weekday keys.

`PUT /arcade/basic` location changes use the authenticated editor's current
level, regardless of supporter or staff tags. Levels below 10 may move the
stored location by at most 1 km; levels 10-14 by at most 5 km; and levels
15-29 by at most 10 km. Level 30 and above may move it any distance only when
the destination resolves to the arcade's current country. The server enforces
these limits using the saved coordinates and its own country lookup; denied
moves return `403` with `location_move_distance_exceeded`,
`location_move_country_changed`, or `location_move_origin_missing`.

Update campaign checks use `POST /campaign/check`. An active authenticated user
may confirm whether a public/open campaign target is still old or updated. A
`still_old` result awards 1 XP once per user and campaign target; an updated
result awards the campaign XP atomically with the game-state mutation. The
default path requires a same-day verified arcade visit. The
optional `bypass_location=true` path is restricted to users at level 10 or
above; it skips only the visit-location check and does not relax campaign,
arcade, or target validation. An updated result through the bypass path awards
1 XP instead of the campaign's configured reward. Clients must show a two-step
warning confirmation before sending the bypass flag. Campaign checks are
append-only reports with a per-report location-verification flag. A
`still_old` report can revert the latest update for seven days after the
update, subject to the same campaign and visit/bypass validation. `GET
/campaign?id=...` returns the remaining old-version targets, current
`to_version` targets, newest-first report logs, per-target report history, and
the latest `still_old` reporter/time. The updated target view is derived from
the current public game state at the campaign's `to_version`, not only from
campaign `result=updated` checks, so machines already on the target version
are included. The report view includes only currently public/open arcades.

Arcade detail prompts (`GET /arcade/campaigns`) and anonymous presence
(`GET /arcade/campaigns/presence`) use a narrower target set: current
`from_version` machines plus current `to_version` machines with an `updated`
report for that campaign and arcade/game created less than seven days ago.
Machines already on `to_version` without a campaign update report never show
a prompt. Once the update report's seven-day review window expires, the
machine is accepted as updated and its prompt/presence disappears on the next
read. Expiry does not mutate the game, delete reports, or remove updated
machines from campaign overview statistics/history.

`GET /campaign/photo` is the automatic photo-update campaign discovery route.
It returns only public/open arcades whose current `arcade.photo` molecule has
no public atom (`photo_status=missing`) or whose newest public atom is at least
one year old (`photo_status=stale`). The newest timestamp is read from an
atom's `updated` value when available and otherwise its immutable `created`
value. Photo atoms removed from the current gallery do not keep an arcade
fresh. The response uses the standard `page`, `per_page`, `last_page`, `total`,
and `items` envelope; missing targets sort before stale targets, and stale
targets sort oldest first.

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
arcade with its count but no visit dates, while `full` additionally
exposes its last local visit day and complete local visit-day list. Neither form exposes
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
`user_info.countries` list of ISO 3166-1 alpha-2 codes together with
`country_mode=auto|manual|off`. `auto` is the default, and resolves one
representative country from public verified visits by total visit count, then
distinct arcade count, then most recent visit; a stable country-code tie break
keeps equal histories deterministic. The resolved Auto country is cached on
`user_info` and refreshed when a verified visit is recorded, so profile and
ranking reads do not re-scan a user's visit history. `manual` exposes the saved ordered list;
`off` exposes neither a country nor a primary country. Active contributors below level 15 may save one code;
contributors at level 15 or above may save up to three. This limit is based only
on level and does not depend on supporter or staff tags. A loss of level-15
access does not delete stored choices: public profile reads expose only the
first code while `GET /user/me` retains all saved codes. Compact user DTOs
expose only `primary_country`, and the client renders it with the Dashboard
country icon source in every nickname profile surface whenever a country is
present. The icon is ordered before any supporter or staff badge.

Active users edit their own `user_info` profile only through `PUT /user/profile`
and game series preferences only through `PUT /user/game-preferences`. The
`user_info` raw PocketBase update rule is locked in Core bootstrap. Profile
updates validate nickname, bio, SNS links, image files, and background position
on the server. Avatar deletion is explicit; omitted files remain unchanged.
Background upload, deletion, and position changes require level 15 based on
`user_level.exp`. Inactive or withdrawn accounts cannot use either mutation.
The preference route replaces the ordered series list, removes duplicates,
validates series IDs, and preserves `warp` when omitted.
Existing Full installations require their own guarded forward migration to
lock the existing `user_info` update rule before this API cutover.

`GET /rankings` always returns the public top 100. User metrics include explorer,
visits, XP, level, and photographer. The `arcade_visits` metric returns public
arcades ranked by the total XP awarded by their completed visit-verification
records; the response displays the verification count separately and includes
the first publicly readable photo from the current photo molecule when one is
available. Public
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

`GET /arcade/ranking?arcade=...&metric=total|edit|passport` returns up to five
users for one public arcade. `total` (also the default) ranks by visit XP plus
edit XP, `edit` by edit XP only, and `passport` by completed visit-verification
XP only. Edit XP comes from positive `xp:arcade-edit:<part>:<arcadeId>` grants for all supported
parts (`basic`, `game`, `hour`, `sns`, `gtk`, `photo`, and `memo`). Public closed
arcades remain eligible. Private arcades return `404`; visit XP from users with
`visit_visibility=private` is omitted, while their eligible arcade edit XP is
still ranked. Only positive edit grants count; corrections do not reduce a
contributor's earned XP. Withdrawn users are omitted. Each metric independently
selects its top five and computes tie ranks from its own score.

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

Profile edit activity (`/user/changelog`) returns only `id`, `arcade`,
`arcade_name`, and `changed` to anonymous callers. An active authenticated user
also receives detailed evidence for rows allowed by the same arcade visibility
filter: public arcades, their own private arcades, or private arcades for strict
reviewers. An invalid bearer token cannot request details. Responses are private
and non-cacheable. The ID alone is pagination metadata.

Authenticated `/arcade/changelog` and `/user/changelog` responses enrich immutable evidence
with scoped memo snapshots and currently accessible `photo_assets` references. Snapshot lookup
must verify the referenced record belongs to the changelog's arcade. Photo
references use the same current authorization as `/arcade/photo/file`; gallery
membership removal does not unpublish an immutable photo. Memo snapshot status
distinguishes absent revisions from unavailable evidence. Read enrichment never
rewrites logs. New rollback logs carry `source="rollback"` so clients do not
render revision-pointer changes as ordinary content diffs.

Game mutations require `base_state_id`; a stale value returns `409`. The public request is a delta with required `add`, `modify`, and `remove` arrays. `modify` is a complete replacement object, not a patch, and a single modify cannot delete other active entries. Same-series version changes retain the entry; a cross-series change is rejected. Removed entries remain durable for historical flags, which appear as `orphanFlags` while absent from the selected batch. Re-addition can reuse the newest inactive history match for the same canonical series and verified cabinet; empty/unverified cabinets never reuse an identity.

For `PUT /arcade/game`, a `modify` object may omit `price` to preserve the
stored price exactly, including null or legacy values. All other replacement
fields retain their existing requirements. New `add` objects require price;
explicit null price is invalid. A location-only edit does not introduce a
price change in immutable history or `game_diff`.

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
Each series also includes its distinct compatible cabinets. Both that list and
each version's cabinet list are ordered by the earliest release date among all
active versions compatible with the cabinet, newest date first. Cabinets with
no dated compatible version follow dated cabinets, then localized name and ID
break ties. The protected catalog management response orders its cabinets by
the same active compatibility rule.

Both the public catalog and protected management catalog omit version records
with a non-empty `alias_of`. These source IDs remain stored for immutable
historical game revisions but are not selectable versions for editing, bulk
version changes, or campaign setup. Filtering uses alias identity, not matching
display names; distinct non-alias versions with equal names remain separate.

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
swap for developer/moderator accounts. It applies the
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
| arcade contribution ranking | `GET /arcade/ranking?arcade=...` (optional `metric=total|edit|passport`, default `total`) |
| user-authored changelog timeline | `GET /user/changelog?user=...` (optional `changed=basic|game|hour|sns|gtk|photo|memo`) |
| arcade favorites | `PUT /arcade/favorite`; `PUT /arcade/favorite/order` persists an active owner's ordered arcade IDs, rejects duplicate/non-owned IDs, and appends omitted favorites in their existing order. New favorites append to the end. Owner profile reads include `favorite_arcades`, while public profile reads include it only when `favorite_visibility=public`; owners control this with `PUT /user/favorite-visibility` |
| photo atom list | `GET /arcade/photo/atoms?arcade=...` |
| photo bytes | `GET /arcade/photo/file?id=...` (the `file_url` returned for an atom; optional `thumb=96x96|384x384|680x0`) |
| delete pending own photo atom | `DELETE /arcade/photo/atom?id=...` |
| standalone edit report | `POST /arcade/edit_report` |
| reviewer queue | `GET /moderation/arcade/edit-reports` |
| reviewer decision | `PUT /moderation/arcade/edit-report` |
| arcade analytics | `GET /arcade/analytics?arcade=...` and `POST /arcade/analytics/event` |
| localized game/version/cabinet catalog | `GET /game/catalog?locale=en-US|ko-KR|ja-JP` |
| protected game catalog management | `GET|POST|PUT|DELETE /moderation/game/catalog`, `POST /moderation/game/catalog/restore`, `POST /moderation/game/catalog/compatibilities`, `GET /moderation/game/catalog/changes`, `POST /moderation/game/catalog/changes/revert`. Compatibility replacement validates the reviewed active revisions and atomically creates, restores, or archives its links. |
| campaign progress and report log | `GET /campaign?id=...` |
| latest regional subway map | `GET /subway/map?region=center|busan` |
| subway map file bytes | `GET /subway/map/file?id=...&field=lightSVG|darkSVG|image|file` (the `file_url` returned by the map response) |
| subway map version management | `POST /subway/map`, `PUT /subway/map`, `DELETE /subway/map?id=...` (developer/moderator only) |

New frontend code MUST NOT reintroduce collection names, PocketBase record rules, collection filters, raw REST pagination, or PocketBase file URL construction as a compatibility layer. Sitemap, changelog timeline, draft list/delete, and photo atom management migrate to these custom routes.

## Rich-text validation and denied notice mutations

Notice authorization denials terminate the handler before any save; the existing
403 JSON error body is preserved. A denied create, update, or delete never changes
persisted notice state.

Memo and notice writes validate nested document nodes, marks, text, and attributes
before persistence. Documents allow extension node names but have a maximum of
5000 nodes, 64 nested content levels, and 128 total JSON nesting levels; heading levels are integers 1–6 and each node allows at most 32 marks.
Malformed documents return 400 and do not create revisions or changelog entries.
Existing immutable history is not rewritten; consumers must validate stored
snapshots before rendering or editing them.

## State transitions and transactions

### Geo and publication

1. New arcade creation and an explicit location change call geo lookup before opening a database transaction.
2. Only successful, valid `country` and IANA `timezone` results enter the bounded geo cache. Failures never enter cache.
3. If geo lookup fails for a new arcade or location change, the request returns `503` and persists no location/aggregate mutation.
4. Public conversion validates the already stored country and IANA timezone. It MUST NOT call external geo HTTP or wait for a network response inside its transaction.
5. A missing or invalid stored geo result rejects public conversion. Repair the location through the normal basic update route first.

The lookup is served by the versioned GeoJSON boundaries embedded in the
application binary, as described in [`docs/offline-geo.md`](offline-geo.md).
`MUSECAT_GEO_DATA_DIR` is an optional deployment override for an unpacked
bundle. Valid country and IANA timezone boundaries resolve in-process. A missing
or invalid country code (including the bundled France/Norway `-99` sentinel)
uses BigDataCloud, then Nominatim, within an eight-second total budget, keeping
the offline timezone. Each provider response must have a two-letter uppercase
country code; invalid responses try the next provider. A missing or invalid bundle returns the same
`503` dependency failure. City labels come from the local `passport_city`
GeoNames catalog: after the country lookup, the nearest catalog city is chosen
deterministically. This deliberately favours a populated Passport over an
unclassified venue; the country boundary still prevents cross-country labels.
Valid offline results do not enter the external HTTP cache; they are read from the
immutable in-memory bundle on every request. Successful country fallbacks use the
bounded 24-hour geo cache and coordinate-keyed singleflight; failures are not cached.
An exact country match takes precedence. If no country contains the coordinate,
one country exterior boundary within 1,000 metres may resolve a coastline
approximation gap. Country misses beyond that tolerance may use the external
country fallback. Multiple nearby country features, overlapping matches, and
missing or ambiguous timezones remain failures. Timezone and city containment
continue to use the original coordinate; the request never moves the arcade
location. All external fallback calls happen before the mutation transaction.

### Cache, XP, and notifications

- Candidate snapshot builds are scoped to an invalidation generation. Each caller rechecks that generation before returning and retries if visibility changed during the build; post-invalidation requests cannot join an obsolete build.
- `/arcades`, `/search`, and `/arcades/nearby` MUST read the same public arcade candidate snapshot. The snapshot derives game membership only from the immutable batch selected by `arcade.game_v2` and retains revision-level `(series, cabinet)` pairs rather than independent sets.
- `/arcades/nearby` accepts grouped `game_filter` values for cabinet-aware filtering. Each repeated value is a JSON object with a `series` id and an optional `cabinet` id; omitting `cabinet` means all cabinets for that series, and separate series groups use AND semantics. Grouped and legacy game parameters cannot be mixed. The legacy `game_series`/`game_cabinet` pair remains supported for compatibility: series-only filters ignore cabinet identity, while repeated and comma-separated values preserve positional `(series, cabinet)` pairs and require equal lengths. Invalid grouped filters, cabinet-only legacy requests, or unequal legacy list lengths return `400`.
- Nearby remains an operating-discovery route: private and public/closed arcades, historical unselected batches, and unverified cabinet revisions for a cabinet-qualified pair MUST NOT affect results, pagination, country totals, or nearest-arcade summaries.
- Candidate invalidation follows changes to arcade/basic data, `arcade.game_v2`, current game entries/revisions, versions, cabinets, and version/cabinet compatibility records.
- `/arcades` is paginated `{page, per_page, last_page, total, items}` and includes only public/open candidates. Search intentionally includes public/closed candidates.
- `GET /arcade/public?arcade=...` is a creator-only, read-only XP estimate. It uses the same idempotent public and draft-backfill grant keys as `PUT /arcade/public`, writes no visibility or ledger state, and the successful PUT response is authoritative if the draft changes afterward. Public conversion grants 5 XP. Draft backfill is independent of the seven-day arcade-edit cooldown: each changed eligible area earns 2 XP once per arcade, regardless of how recently that area received normal edit XP.
- Public conversion remains creator-only and uses the creator's level snapshot at request time: levels 0-4 require game information, contact-or-hours, and facility-photo-or-location-verification; levels 5-9 require game information and facility-photo-or-location-verification; levels 10+ require game information only. Supporter and staff tags never bypass these requirements. The one-time publication location verification keeps its stricter 100-meter distance / 100-meter GPS-accuracy rule, is tied to the current `arcade.basic` revision, and is invalidated automatically when basic information changes. It does not create a Passport visit or award XP.
- XP ledger changes and aggregate mutations belong to the same transaction. No XP grant may survive a failed aggregate mutation.
- XP is available only after the authenticated user has a non-empty `username`. Before one-time username setup, XP-producing actions retain their normal mutation semantics where applicable but award `0`; they must not create `user_level` or `user_level_log` state, and public-conversion XP previews must report no eligible XP.
- Normal edit XP remains scoped by user, arcade, and part. Basic/hour/sns/gtk edits grant 2 XP with their existing seven-day cooldown. Photo atoms grant 2 XP each only when first published, with a rolling seven-day cap of 20 XP for a photo-campaign target and 10 XP for another public arcade; upload-only, reorder, removal, and reuse of a published atom grant 0. Game edits use a rolling seven-day window of distinct durable `arcade_game_id` values: the target is `min(10, 2*n)` and each request receives only the increase over XP already granted in that window. Revisiting an entry already counted in the window grants 0; entries become eligible again after they leave the window. Administrative bulk game-version updates and public-conversion backfill do not use this scale.
- Passport visits grant 5 XP for a lifetime first visit to an arcade and 2 XP for a revisit. Attendance check-in grants 1 XP. Flag creation grants 5 XP; `issue_persist` reactions grant 2 XP, `fixed` reactions grant 3 XP, and `wrong` votes grant 0 XP. Deleting a rewarded reaction reverses the exact original ledger amount.
- Notification delivery is after persistence and best-effort. A Telegram/Discord failure MUST NOT roll back a completed user request.
- Review processing has no automated ban and no automated rollback.
- Public `GET /arcade` detail loads record one `page_view` event best-effort. The direction-click event route is anonymous and accepts only `direction_click`. Its JSON body is capped at 2 KiB and at most ten distinct series IDs. After validating the arcade, it applies the rate limit before game-series database validation, so rejected series IDs consume quota rather than creating a lookup-amplification path. It retains no client identity: a process-local limiter keyed by the Core app instance and PocketBase `RealIP()` accepts at most 20 events per client in a rolling ten minutes and one event per client/arcade every 30 seconds. Expired per-arcade cooldown entries are removed on the next client request, and the limiter evicts the least recently active client when its 4,096-client memory cap is reached. `RealIP()` uses proxy headers only when PocketBase trusted-proxy settings permit them; otherwise it uses the direct remote IP. Limit rejection returns `429` with `Retry-After`. This is intentionally best-effort across process restarts and separate instances, preserving the no-IP-storage analytics contract; edge-level rate limiting remains required when deployment-wide enforcement is needed.
- A successful flag creation records a `fault_report` marker in the same transaction; its flag id preserves cumulative counting after the user deletes the flag.
- Flag resolution is owned by Core. `arcade_flag.resolution_vote_state` is `idle|active`; the first current-round `fixed` reaction starts a round, while `issue_persist` before that point appends report history and starts the next report round semantics. During an active round, `fixed` and `wrong` are one mutually exclusive vote per user, with `level_snapshot` captured at creation. The score is the fixed snapshot-level sum minus the wrong snapshot-level sum. A fixed voter keeps a zero score active for 72 hours; a negative score or zero fixed voters closes the round. The delay is 15 minutes at 30+, 3 hours at 20–29, 24 hours at 10–19, 48 hours at 5–9, and 72 hours at 0–4. Every score-changing event calculates a candidate deadline from the server event time, but an active flag keeps the earlier of its existing `resolveAt` and that candidate so a vote can never postpone resolution. Successful flag reactions refresh the flag's `updated` activity timestamp. A flag with no reaction activity for 120 days is opened by the nightly stale sweep as a zero-score, 72-hour resolution window; this window is allowed to remain active without a user fixed vote until it receives a user vote or reaches its deadline. Legacy reactions remain stored with `resolution_context=legacy` and never enter new-round totals. Deadline reconciliation runs inside reaction transactions and the every-minute Core cron, while the 120-day stale sweep runs once per night; tagged-user immediate resolution is not supported.
- The flag read and reaction mutation responses include the authoritative `resolution` summary, `myVote`, and newest-first `reportHistory`. Clients may calculate only display countdown/progress from `resolveAt`; they MUST NOT infer score, thresholds, rounds, or solve state from raw reaction counts.
- Analytics responses include `page_views`, `fault_reports`, and `edit_count` for every caller. `page_views_by_source` and `series_filter_entries` are additionally available to authenticated users whose server-computed level is at least 15 when page views exceed 1,000, and remain available to official arcade accounts and `developer|moderator` callers. `direction_clicks`, `visit_verifications`, and `distinct_visitors` remain omitted unless the caller is an official arcade account or a `developer|moderator`. The web summary card remains staff-only; lower breakdowns display collecting for non-staff viewers until page views exceed 1,000, while staff see them immediately regardless of view count. Both states retain a September 30, 2026 collection-start caption.

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

`GET /user/passport?year=all|YYYY` and `GET /user/passport/stamps` accept an optional
`user` ID. Omit it for the authenticated active owner; an explicit ID also permits
anonymous reading according to that user's visibility. Both APIs are no-store and
use `LoadPassport`, as does legacy public `visit_stats`. Only current public arcades,
including closed venues, are included. Private venues disappear from every aggregate.

`private` returns 404 to other viewers except active developers and moderators,
who receive the `full` projection. Banned and withdrawn staff have no override.
Active developers and moderators may also read the owner-level profile fields
(private series, favorites, visit statistics and settings) through `GET /user`
by ID or username. These responses are private and no-store. `summary` exposes totals, country/city maps,
period statistics, filters and venue stamps, but omits first/latest visit dates and
all per-venue date arrays from both APIs (including top venues) and legacy profile
stats. `full` additionally exposes first/latest dates and every selected-period local
visit date in `visit_dates`, newest first. Select `year=all` for all dates. The owner
has the same complete fields regardless of public visibility. Raw GPS, accuracy,
precise timestamps and visit-record IDs remain private in every scope. Existing
summary/full settings take the new scope immediately; there is no versioned opt-in.
`visibility` in the response tells the web which projection was applied.
`city_catalog_available=false` distinguishes missing reference data from zero cities.

Periods use stored local `visit_day`; active days deduplicate those date strings even
across countries. New discoveries use lifetime first chronological verification.
Distances use current locations, with both consecutive endpoints inside the selected
period; missing locations and excluded period records break a segment. Totals are
straight-line distances, not actual travel or play time. Month series include zero months.

Canonical `passport_city` records use GeoNames IDs as unique source identifiers; raw
REST is locked. `arcade_basic.city_id` is optional and versioned with basic history.
Current city metadata applies retrospectively. A missing or country-mismatched city is
unclassified; those arcades remain in country and venue totals. Cities are matched by
country, admin1 and locality aliases, never proximity. Explicit GeoNames hierarchy
links attach PPLX neighbourhood aliases to a unique parent city; standalone cities
such as Petaling Jaya remain distinct. Absent or ambiguous parentage is not inferred.
Rows missing admin1 are skipped during import. Address-based candidates require a
named alias and use coordinates only to reject distant homonyms (50 km sanity limit,
not a boundary). Every address candidate remains unapproved until operator review.
The catalog can be populated from the global allCountries extract; cities500 is a
smaller operational starting point and does not cover every settlement. GeoNames import and candidate review are explicit Full operations, outside requests and
transactions. Core's schema migration bootstraps only a fresh test database.

`POST /arcade/visit` additionally returns `first_visit_to_arcade`, true only for the newly
committed first visit; duplicate same-day requests return false and never award another
stamp or XP. Clients must acquire a fresh GPS position when the user explicitly starts a
Passport check rather than submitting persisted coordinates. The measured point must be
within 100 meters of the arcade, and reported GPS accuracy must be at most 150 meters.
The first visit awards 5 XP and a revisit awards 2 XP. Passport is GPS visit evidence, not play evidence.


### Feedback and small image upload limits

`POST /support_feedback` accepts a non-empty message up to 13,000 Unicode code points,
including any frontend classification or reply-email metadata. The frontend retains
its 1,200-character general and 12,000-character error-report input limits. Core's
fresh schema declares the stored limit explicitly; Full upgrades existing schemas
with its own guarded forward migration and preserves any larger operator limit.

`GET /user/feedback` requires an active user, always scopes createdBy to that token,
and returns that user's records (all statuses by default) newest first, with an
optional status filter. Foreign createdBy query parameters cannot change the scope.
Anonymous submissions cannot be claimed. Withdrawn and banned accounts are denied.
The existing developer/moderator queue remains separately protected. The own-history
DTO contains id, message, status, createdBy, photos (filenames), created and updated;
it does not change file authorization.

Flag uploads accept 3 images of 15,000,000 bytes and a 46,048,576-byte encoded body.
Notice create/update accepts 3 images of 5,242,880 bytes and a 16,777,216-byte body.
Both verify actual file contents and return 413 for file/body overflow; excessive
counts or non-images return 400. Files are multipart-only, never JSON file objects.
The frontend shares each feature's limits across UI, service and bounded BFF reader.
Existing stored attachments are preserved when updates omit photos. These checks do
not change arcade visibility, author/reviewer permissions, XP or history semantics.

### Profile background framing

`background_position` stores x/y cover anchors (0–100), optional zoom (1–4, default 1), and clockwise rotation (0/90/180/270, default 0). Anchors apply after rotation. Preserve the full image; these are rendering transforms. Legacy x/y-only JSON remains valid within the existing 128-byte field, so no schema migration or data backfill is required. Publish this Core contract before updating Full’s pinned Core dependency and deploying frontend transform writes.

## Public arcade summary timestamps

`ArcadeSummary.updated` exposes the persisted `arcade.updated` aggregate timestamp, including in `GET /arcades`. Content mutations and rollbacks save the aggregate root, so sitemap consumers can use this value as the page modification time. Candidate snapshot rebuilds and read requests never substitute the current clock time. The existing public/closed/private visibility rules are unchanged.

### Deployment visit policy extension

Core does not own deployment-specific abuse detection, sanctions, schemas, or API routes.
Full can register a visit policy evaluated inside the visit transaction, after the
same-day duplicate check and before XP or a visit is created. A returned restriction
code is committed and returned as a generic 403. Policy failures roll back the request.
Full can decorate new visit records and supply a trusted, server-owned aggregate SQL
condition for visit-derived rankings and counts; Core defaults to unrestricted visits.

Community post media accepts up to eight ordered JPEG/PNG/WebP/GIF files, each at most 10 MiB, through bounded multipart POST/PUT. Active profile achievement galleries apply author/flair/has_images filters before count and pagination; media has public originals and thumbnails, while translations continue to cache text only.

`price.list[].title` accepts a string or number, including numeric credit counts.
Game mutations preserve numeric titles in existing revisions and new requests.
Historical revisions remain immutable; an untouched numeric title does not count
as a changed entry for changelog or XP.

Delta mutations validate fields and catalog references only for submitted add/modify
items. Untouched active revisions are copied without revalidating their price,
tags, or cabinet compatibility. The resulting list still enforces durable-entry
and version/cabinet uniqueness, and base-state concurrency checks remain required.

## Arcade moderation tools

`GET /moderation/arcade/requests` lists all `arcade_request_admin` records,
including general inquiries, edit reports and rollback reports. Active users with
`developer` or `moderator` tags may resolve a request through
`PUT /moderation/arcade/request` with `id`, `outcome=upheld|dismissed|actioned`
and an optional note (up to 1200 characters). Resolution atomically records
`status=done`, reviewer, review time, outcome and note; repeated resolution is 409.
The existing own-request endpoint and edit-report-only queue retain their scopes.

`GET|PUT /moderation/arcade/status` reads or updates `public`, `closed`, `country` and `timezone` for
any arcade, including private or closed records. The same strict active reviewer
guard applies. PUT requires both boolean flags, an ISO 3166-1 alpha-2 country
code and a valid IANA timezone name (excluding `Local`), and persists all four
settings in a transaction. Country is trimmed and uppercased; timezone is trimmed.
Existing aggregate hooks invalidate discovery caches after commit. This operator
status correction does not award publication/edit XP or create a content revision
or arcade changelog row; public-conversion requirements apply to the creator's
publication workflow, not this administrative correction.

## Home dashboard aggregates and history

`GET /stats` retains legacy `flag_count` (reports plus reactions) and adds
open/resolved report counts, verified visits respecting
deployment aggregate policy, and cumulative page views. All totals exclude private
arcades and include public closed venues. Page views deduplicate `event_group`
across game-series rows and provide a daily UTC cumulative series from September
30, 2026 through today, carrying the previous total through inactive days.

`GET /arcades/updates` is the public Summary API: no query parameters are
accepted, and at most six latest distinct-arcade blocks are returned. If the six
newest are all KR and a non-KR arcade has history, the newest non-KR block replaces
the sixth entry. Results remain in descending activity order. Fewer are returned
when fewer eligible arcades exist; no foreign entry is invented if none exists.

`GET /arcades/updates/history` requires user authentication and returns `items`,
`page`, `per_page`, `last_page`, `total` across chronological blocks, including
older blocks for the same arcade. Passport verifications are excluded before
block grouping and pagination. Both endpoints exclude private and closed arcades.
Blocks retain `id` as the arcade ID and expose `update_id` for activity identity.
The public endpoint cannot expose additional history through page/limit overrides.

`flag_reaction_count` separately counts reactions on public arcade reports while
`flag_count` continues to include both reports and reactions.

## Arcade analytics acquisition sources

Page views and direction clicks accept `direct`, `search` (site search),
`nearby`, `other`, `poster`, `google`, `naver`, and `bing`. Values are trimmed
and lowercased; missing or unrecognized values fall back to `direct`.
The web arcade-detail route prioritizes an explicit `source` query parameter
(e.g. `source=poster` for poster QR codes). Without it, the incoming Referer
hostname identifies Google (including country domains), Naver, or Bing and
passes the resolved source to both page-view and direction-click recording.
Missing, malformed, or unrecognized referrers remain `direct`. Raw referrers
and search terms are not stored, and historical `direct` events are not reclassified.

## User and support moderation

Active developers and moderators can list all user reports with
`GET /moderation/user/reports`, update their `waiting|processing|done` status
with `PUT /moderation/user/report`, and list/create/update/delete bans with
`GET /moderation/user/bans` and `POST|DELETE /moderation/user/ban`.
Bans use the user's ID and normalized email hash, preserving auth restrictions.
An omitted expiry is permanent; a supplied expiry must be a future RFC3339 timestamp.
`PUT /support_feedback` updates feedback status to `waiting|recognised|solved`.
These administrative mutations do not grant XP or write arcade content history.
`GET /subway/map/history` uses the existing active administrator guard and lists
all versions, optionally restricted to `center|busan`, newest release first.

## Test account creation

`POST /moderation/user/test-account` requires an active developer or moderator.
It accepts email, username (4–15 ASCII letters/digits), password (8–71
characters), and nickname (1–25 characters, no flag emoji). The user and matching
user_info are saved atomically with ordinary roles and standard profile visibility.
Email and case-insensitive username duplicates return 409. No password or new
session token is returned; the caller keeps their session. The endpoint does not
change collection-wide password authentication or verify the supplied email.

## Official arcade account creation

`POST /moderation/user/arcade-account` requires an active developer, moderator, or designer.
The designer role grants this provisioning capability only; strict reviewer routes remain
restricted to developers and moderators. The input follows the test-account fields and
adds a required `arcadeId` identifying an existing public arcade (open or closed).
Creation atomically saves the user, profile, `tags=["arcade_owner"]`, and `owns=[arcadeId]`.
Missing/private arcades return 404; duplicate email or username returns 409.
Client-supplied tags, ownership arrays, verification flags, and extra privileges are ignored.
The response contains id, email, username, nickname, and arcadeId, never a password or
session token; the caller keeps their session. This user provisioning does not edit arcade
content, award XP, or append arcade changelog rows.

Password authentication accepts email or username. Web official-account sign-in uses the
username identity and validates the resulting official role and arcade ownership before
issuing its session cookie. Normal web user sign-in continues to use Google OAuth.


## Official arcade account credential management

`PUT /user/arcade-account/credentials` is available only to active users tagged
`arcade_owner`, including accounts whose assigned arcade was removed. It accepts
`currentPassword` and an optional `email` and/or `newPassword`; at least one must
actually change. Current and new passwords preserve whitespace. Email follows the
same normalization and uniqueness rules as account creation. The fresh user role,
restriction, current password, duplicate-email lookup and credential writes are
checked together in one transaction. Incorrect passwords return 401, duplicate
email 409, and invalid/no-op requests 400. Email changes clear verification and
email visibility. Both changes rotate the token key, invalidating all prior
sessions; no token, email or password is returned. The web client clears its session
cookie only after success and returns to official-account sign-in.

`POST /user/withdraw` requires the exact current password for official accounts,
even if they also have an OAuth link. The existing anonymization and 30-day
re-registration policy remains in effect. Official withdrawal clears `owns` and
removes `arcade_owner` atomically, releasing official management without deleting
arcades or contributions. Ordinary OAuth withdrawal retains its password-free flow.
These account operations do not award XP or write arcade content history.

## Official arcade profiles

Arcade detail and authorized draft detail include `admin.officialAccountIds`, an
ordered list of non-withdrawn `arcade_owner` users whose `owns` relation includes
the arcade. Creation authorship alone does not establish official ownership.
Profile details continue to use the public `/user` projection.

## External account connections

External game accounts use a separate connection ledger, not PocketBase sign-in external auths.
`external_account_connection` has unique `(issuer, subject)` and `(user, issuer)` identities;
`external_service_connection` attaches multiple separately authorized game services to one account.
Names, emails and game-specific service IDs never replace the verified issuer/subject identity.
New and reauthorized game grants are private with no selected public fields. Records and the
explicit publication switch are separate APIs; acting on Musecat requires a separate delegated grant.

`GET /integrations/providers` exposes only safe configured provider metadata. Active-user-only
`GET /user/connections`, `POST /user/connections/start`, `POST /user/connections/complete`, and
`DELETE /user/connections/{id}` manage owned connections. Raw REST for all connection collections
is locked, and subject/credential/state/session/verifier fields are hidden. Responses never expose
tokens or external subjects. Local disconnect works independently of provider availability and
removes credentials, game grants and pending attempts atomically. Independent unlink is local-only;
joint disconnect additionally queues generation-bound bilateral revocation. Withdrawal performs
cleanup and queues joint notifications within its account transaction.

Requests expire after five minutes, are bound to the exact initiating Musecat token, and use
hashed state plus encrypted PKCE verifier. A single pending-to-exchanging claim clears the verifier
before network I/O. Completion rechecks the current token, account restrictions, live claim,
expiry, configured issuer and identity uniqueness in a second transaction. Disconnect, withdrawal,
cleanup and newer attempts invalidate in-flight results. Subject conflicts return 409 without
transferring or replacing an identity. Provider failures cannot be replayed with the same request.

Only trusted executable-injected HTTPS providers are used; no public endpoint accepts URLs,
credentials or external account IDs. Providers require distinct callback URIs, explicit client
authentication and issuer checks. The adapter obtains a stable string subject through authenticated
OAuth user-info; it does not implement ID-token-based OIDC verification. A changed issuer requires
reconnection or a separately reviewed verified migration, including rhythm.info account unification.

Core configuration performs no environment reads or scheduling. Executables select providers,
require a base64 32-byte AES-GCM key, and schedule expired-request cleanup every five minutes.
Runtime rate limits add 10 start and 20 complete requests per client IP per 600 seconds across
both audiences without replacing existing operator rules. Full owns its independent guarded
forward schema migration; Core migrations remain fresh-bootstrap only. These operations do not
award XP or append arcade changelog history. See [external connections](external-connections.md)
for configuration, reauthorization semantics, race handling and integration limitations.

## Public developer API and OAuth delegation

Musecat's OAuth authorization server is separate from `connections` (where Musecat is an OAuth
client). `delegation` owns registered client epochs, five-minute authorization requests, grants,
one-use PKCE codes and opaque access/refresh tokens. `handlers/delegation` exposes the public
OAuth protocol and `/developer/v1/*` routes. `docs/developer-openapi.yaml` and
`docs/developer-api.md` are the external developer contract, served independently of protected
internal documentation at `/developer`.

The executable injects trusted confidential-client configuration, issuer, consent application URL,
and the existing `user.CheckActiveUser` policy. Core reads no environment variables. Registration
is operator-controlled; no partner or external account merger is hardcoded. At startup, registered
client configuration is synchronized transactionally into a locked registration table. Each change,
removal or re-enablement changes its persisted epoch, so restoring an earlier configuration cannot
resurrect pre-change tokens or pending approvals. Enabled registrations are capped at 128; startup
work is bounded. All instances must deploy the same client registry. Missing configuration disables
new OAuth operations, while user-owned grant listing and revocation remain available.

Authorization validates the exact HTTPS callback, scopes, state and S256 challenge before redirecting
to the fixed Musecat consent application. The first authenticated consent read binds the attempt to
the current user and exact session token. The decision repeats active-account/session validation
inside the transaction and accepts only a nonempty subset of requested permissions. Denial leaves
an existing approval unchanged; approval replaces scopes and invalidates the previous grant generation.
WebCore owns the consent UI/BFF at `/{locale}/oauth/authorize`, with a bullet permission summary and one explicit connection approval,
same-origin CSRF protection, no-referrer, no-store and frame/analytics protection. Deploy and configure
that HTTPS page before enabling clients. The API parses a bounded JSON snapshot because PocketBase's
rewindable HTTP body can return data and EOF together; strict trailing-data checks must not reread it.

Code redemption and refresh are atomic claims. Codes bind client, callback, S256 verifier and grant
generation, and expire after 90 seconds. Access tokens last at most ten minutes; refresh rotation
preserves a fixed 30-day family expiry. Reuse of a consumed code or refresh token revokes the current
grant in a committed transaction. An older approval's replay cannot revoke a newer approval. Secrets,
raw tokens and PKCE verifiers are never stored. Personal, OAuth and resource responses use no-store
and no-referrer, including early middleware errors. OAuth/resource queries and referrers are redacted
before PocketBase activity logging; ingress logs require corresponding operator redaction.

Delegated bearer tokens work only on the explicitly mapped `/developer/v1/*` surface. The resource
boundary resolves the current grant, registration epoch, expiry, account token-key fingerprint,
active account and required scope before assigning the verified actor to an existing domain handler.
The primary `account:access` scope maps the explicit general-user allowlist (currently 82 method/path
pairs) into `/developer/v1/account`, reusing the original route registrations and domain guards.
Management/moderation routes are rejected even for a staff actor; signup, withdrawal, credential
changes, other-app authorization, reverse account linking and raw PocketBase routes are excluded.
Unlisted/new operations fail closed. Legacy narrow scopes do not permit the account namespace;
existing grants never broaden automatically. Omitted scopes default to account access only for a
registered client that already permits that scope. Client changes invalidate grants for reapproval.
Mutating account request attempts log the user, app-specific subject, method and path without
bodies or credentials; committed domain history remains the authority for successful mutations.
No delegated token is converted to a Musecat JWT. Public/closed/private visibility, ownership, staff
checks, GPS eligibility, immutable history and XP rules remain in those handlers. In-flight accepted
requests can finish after revocation; subsequent requests fail. The legacy `/developer/v1/me` identity endpoint exposes only the
client-specific stable subject, username and nickname, without email, credentials or roles. The account namespace retains each documented domain
projection, including normal profile resource IDs; these are distinct from the app-specific subject.

Users revoke only their own grants, including apps absent from current configuration. Revocation
also cancels their bound pending requests. Withdrawal deletes grants, codes, tokens and bound requests
in its existing transaction; a password/account-key reset invalidates every old delegated token.
Core's migration creates only fresh schema. Full must apply its independent guarded forward migration
to existing installations and pin this Core revision. Both executables register bounded expiry cleanup.

## Developer Center: self-service partner registration

Developer Center and external-connection JSON requests accept exactly one document with
optional trailing whitespace. Decode a bounded body snapshot because PocketBase rewinds
its body reader on EOF; a final read returning both bytes and EOF must not replay the body.
Unknown fields, second documents, and route body-limit overflows remain rejected.

`partner_application` and `partner_application_event` are locked base collections owned by
`partnerapp`. An active user registers up to eight applications of the `musecat-partner-onboarding/v1`
form (`partner_id`, localized name/description, contact and at least one direction). Direction A
requires 1–8 redirect URIs; Direction B-only apps may omit `direction_a` or use empty redirects.
`login_enabled` and joint declarations require nonempty Direction-A redirects; joint also requires
Direction B. All declarations are validated structurally. Create and update immediately install the validated registration without moderator approval. Operators retain monitoring, suspend and reinstate through `/moderation/developer/apps/*`; mutations append immutable events. Legacy submit/approve endpoints are idempotent for registered apps and activate legacy drafts/queued forms. The form never carries credentials.

Registration/update copies `submitted_form` to `approved_form` (historical storage names retained; status `approved` now means registered), validates the merged OAuth client registry
(static executable clients plus live applications; duplicates and executable-owned IDs fail with
409) and then calls `delegation.Manager.Reload`, which swaps the client map under a lock and runs
`SynchronizeClients`. The existing registration epoch/config-hash mechanism therefore applies
unchanged: a changed approved form or rotated secret is a new fingerprint and invalidates earlier
grants; suspension disables the registration row so tokens fail with 503. The application `live`
flag is true when either approved direction is active;
`direction_a_declared`/`direction_a_live` distinguish OAuth capability. Direction A is live
only with approved nonempty redirects, a generated secret and a non-suspended status. B-only
apps never enter the OAuth registry, even if a previous approved version left a stored secret.
Secret generation/rotation returns `invalid_state` for B-only apps. Owners generate the
Direction-A client secret after registration; only its SHA-256 is stored and the plaintext is
returned once with
`no-store`/`no-referrer`.

Direction B activates independently of Direction A and its secret from the same record: the owner
stores the partner-issued Musecat client credentials (`PUT /developer/apps/{id}/credentials`), which are AES-GCM sealed with the connection
key and bound to the application ID. When the approved form declares `direction_b`, the service
maps it to a `connections.Provider` (`subject_field` fixed to `sub`, `client_secret_basic`,
`require_issuer`, callback `<web origin>/api/user/connections/callback/<partner_id>`) and calls
`connections.Manager.Reload`, so the partner appears in `GET /integrations/providers` at once.
Issuer/callback collisions with static or other live providers fail approval or credential entry
with 409. Suspension, credential removal or a missing connection key deactivates the provider;
existing user connections remain listed and locally disconnectable. Joint declarations activate when approved with Direction B credentials, a sealed outbound server key and a hashed inbound server key.

`coreapp.Configure` loads live applications on serve; executables register
`partnerapp.Service.RegisterSyncCron` so other instances converge within a minute. These routes
are outside the delegated `account:access` allowlist. Core bootstraps fresh schema; Full owns the
guarded forward migration. See [`developer-center.md`](developer-center.md) for the full design.

The owner-only `/developer/apps/{id}/console` exposes approved settings, paginated pairwise
account identifiers and 14 UTC days of aggregate activity. Names require profile consent;
account/token/generation restrictions still apply. `oauth_client_usage` is locked and stores
only client, day, kind (`api`, `code`, `refresh`), requests and errors. Attributable failures
count as errors; unknown credentials are not attributed. API includes UserInfo and delegated
resources. Counters are best-effort and never roll back authentication or domain operations;
no tokens, IPs, URLs, query strings or request bodies enter these aggregates. Cleanup retains
30 days. Full owns the forward migration; Core creates only fresh bootstrap schema.
Optional HTTPS website/privacy/terms/support URLs belong to the submitted and approved form;
omitting them preserves old form hashes. Credential issuance history contains timestamps and
rotation events, not recoverable secrets or a fabricated grace period.

## Reciprocal connection orchestration

Approved joint registrations drive `/user/joint/*` and `/partner/v1/*` through
`connections.JointManager`. The consent summary binds a verified partner subject, requested
services, registered callback/PKCE challenge and the exact Musecat session to `musecat-joint/v1`.
The approve action atomically claims the attempt, issues and binds the Direction A grant/code,
then calls the partner receipt and redeems its Direction B code outside transactions. The
userinfo subject is checked before persistence. A consumed/ambiguous attempt cannot be approved
again. Both redeemed live grants are required for `connected`; partial state is never success.

Partner routes accept only distinct server bearer keys, including the previous hashed key until
the declared rotation deadline. `X-Request-Id` results are stored for ten minutes, scoped to
partner, method, path and body. Keys and raw partner subjects are never projected to settings.
The intent is hashed for binding and sealed until the approval claim clears its encrypted copy.

Joint disconnect revokes only the matching grant and connection authorization generations and
queues a remote notification in the same transaction. A later independent authorization survives
an older revocation. Outbox errors are bounded codes; notifications retry with backoff indefinitely
and never restore local access. Settings distinguish pending remote revocation. One-sided unlink
remains local-only. OAuth refresh does not replace the connection's authorization generation.
Core supplies fresh bootstrap schema; Full owns independent guarded forward migrations and job
registration. No deployment or real-partner interoperability is implied by mock conformance tests.

## Developer Center conformance execution

`POST /developer/apps/{id}/checks` queues owner-authorized work; the matching GET returns
safe history. Active strict reviewers use `/moderation/developer/apps/{id}/checks` with
the existing reviewer policy. Raw `partner_conformance_run` access is locked. Each run freezes
its submitted form and a revision of the form and credentials. Reports become stale after
configuration changes or 30 days. Credentials, test subjects, remote response bodies and tokens
are never projected or logged. Optional staging fixtures are sealed and cleared on completion,
cancellation and interrupted-run recovery. One active run per app, a one-minute cooldown and
three active runs per actor bound the queue; executables process bounded batches every minute.

The runner performs outbound I/O outside transactions, with 10-second request and 90-second
run deadlines, no redirects, no environment HTTP proxy and bounded response bodies. The default
dialer rejects private, loopback, link-local, multicast, special and IPv4-embedded translation
ranges, resolves and validates every address, then pins the selected public IP while preserving
TLS hostname verification. Registered form endpoints are the only targets.

Pass, fail, action_required and not_applicable remain distinct. A run is passed only when all
applicable cases pass; missing staging fixtures produce incomplete, not success. Disposable refresh
families are refreshed/revoked only with explicit consent. The runner never creates OAuth consent:
Direction A requires an owner grant whose code was actually redeemed; its round-trip check is
`not_applicable` for a B-only form. A successful joint round
trip must be evidenced by the owner's normal joint connection and both
live grant generations under the approved configuration. Operator approval remains a separate
review action; checks do not silently change existing approval policy or active registrations.


## Integration release preparation

Owner withdrawal suspends all owned partner applications, disables the persistent OAuth
registrations and wipes stored partner credentials and queued/running conformance fixtures in the
same transaction. After commit, the current instance reloads its registries; other instances
converge via the version poll. Registry building excludes restricted or missing owners; a withdrawn
owner cannot be approved or reinstated. Withdrawal first preserves joint revocations in the
non-personal outbox, then deletes personal connection/attempt data. Revocation retries keep the
pair UUID as their `X-Request-Id` and the original body/time for idempotency.

Public game records require a currently configured provider/service and a cache younger than
24 hours. The locked record cache adds `retry_at`; owners and background workers honor upstream
429 `Retry-After` seconds. Post-I/O persistence rechecks the current account authorization generation,
credentials and service existence, preventing stale fetches from recreating disconnected data.
The conformance runner and OAuth/record/joint adapters share the same default public-address-pinned,
proxy-free HTTPS client. Full owns the independent guarded retry-column migration.
See [release readiness](partner-release-readiness.md) for deployment and partner-dependent evidence.


## Sign in with Musecat

Approved applications may opt into `direction_a.login_enabled`. Only these dynamic clients gain
`openid`, `profile` and `arcade:read` alongside their existing general API scope. OpenID requests
require configured persistent RSA signing keys, session-bound nonce, PKCE S256 and explicit consent.
Hidden nonce fields in the request/code ledgers bind the ID token to its initiating login. Full
owns migration 1792030010; Core defines fresh bootstrap. Authorization-code exchange signs a
ten-minute-or-shorter RS256 ID token inside the token-issuance transaction; failed signing cannot
consume the code or leave tokens issued. Refresh does not reissue an ID token. Discovery and JWKS
expose no private material. UserInfo requires an exact OpenID grant, reports only app-specific sub
unless profile was approved, and shares the live revocation/account policy. General account access
does not imply OpenID scope. ID tokens are rejected on resource APIs. Readonly login tokens cannot
invoke the general write allowlist. See sign-in-with-musecat.md for the supported protocol profile.

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

### External record selection and privacy invalidation

The v1 record envelope accepts optional game semantic profiles, typed recent plays, and explicit unavailable selection metadata. The parser rejects data that contradicts a denied/no-data/unsupported item. A refresh replaces the entire payload. `POST /partner/v1/records/invalidate` authenticates a separately Musecat-issued server key scoped to an active B provider, matches only that provider's verified subject/service, atomically erases the payload, and returns identical 204 results for unknown subjects. No network call runs in that transaction. A concurrent upstream fetch started before the purge cannot restore old data; owner/background reads may refetch immediately. Public records use no-store. Existing client displays require refresh; undelivered partner notifications retain the normal freshness limit and cannot promise immediate privacy.

### Incoming records invalidation key and profile pictures

`POST /developer/apps/{id}/records-key` issues the incoming server key for any registered
Direction-B app, including B-only. It shares the stored current/previous inbound key and
configured rotation window with Joint when enabled, without granting Joint capability to
B-only providers. Authentication checks the active owner and live B provider, and honors
only the bounded previous-key grace. UserInfo exposes optional absolute `picture` only
with explicit `profile` scope. `/developer/v1/me` uses the same public avatar URL with its
existing profile access requirement. URLs use the configured issuer, never untrusted Host;
missing avatars are omitted, and emails remain excluded.

### Development audience and production review

New applications use publication development and immediately support only their owner plus
up to ten explicitly designated active test accounts. Owner tester management resolves exact
usernames to internal IDs. Dynamic OAuth clients and B providers carry Development plus
AllowedUserIDs; persistent fingerprints/epochs bind audience changes. Consent read/decision,
code/refresh exchange, resource/UserInfo access, B start/complete/fetch and Joint actor paths
must enforce the audience. Anonymous provider discovery and public record reads hide development
providers. Static executable registrations remain unrestricted by default. The session-bound consent summary exposes publication and the exact registered redirect_uri from the validated pending authorization request so the user can see development status and the return destination before deciding. It does not expose client credentials or expand the requested scopes.

Publication production requires an explicit owner request and operator approval of the current
submitted form. Production edits do not overwrite the reviewed active snapshot; any edit cancels
pending review. Rejection preserves active configuration. publication and review_status are
separate from the legacy status enum. Full adds these fields and testers through a guarded
migration and preserves existing active registrations as production. Core defines fresh schema.
Exact HTTP localhost, 127.0.0.1 and [::1] redirects are accepted for development callbacks only;
production requests reject them. Remote B/Joint fetch destinations retain public HTTPS and
SSRF rules. Audience restriction never replaces user consent or public-profile visibility.

### Development partner audiences

Developer Center applications begin in development publication and are available only to the owner and up to ten explicitly assigned testers. Production publication requires operator approval. Dynamic Direction-B provider configurations include the current audience in their configuration fingerprint. Discovery filters development metadata by the authenticated user; starts, callback completion (including after network exchange), cached owner records, and fetch persistence enforce the current audience. Removing a tester blocks cached reads and prevents in-flight exchanges/fetches from restoring access. Development records are never returned by anonymous public profile reads, even if the tester set the local publication switch. Joint entry/consent shares the audience rule; owners can still revoke or disconnect their own old connection. Static operator-supplied provider configuration retains its existing production audience unless explicitly marked development.

### CHUNITHM Info native records adapter

The fixed CHUNITHM Info origin, authorize, token and `@me` endpoints select a native adapter. Arbitrary partner endpoints retain the generic `sub`/`iss` and `musecat-records/v1` contract. The native identity is `id`; OAuth callbacks may omit `iss`, while state/session binding, dedicated callback and S256 remain enforced. It requests profile/rating/stats/scores and projects only groups present in the response's granted `scopes`. Native records use the same subject validation, bounded typed envelope, cache and owner publication/development audience gates. The source documents no independent public flag; OAuth data grants plus Musecat's explicit publish control determine sharing. Concurrent native fetches coalesce and reload encrypted credentials to avoid rotated refresh-token replay.

`@me` is fetched directly (without a game path suffix). Supported fields map to CHUNITHM rating/statistics and best30; absent metrics are not fabricated. Recent plays are explicitly unsupported until an upstream detail API is documented. Webhook notifications and Joint are not enabled by native OAuth registration; no webhook receiver URL should be advertised. Local unlink continues to commit without promising remote OAuth revocation.

Native CHUNITHM Info OAuth consent publishes the granted `chunithm` service immediately. Other providers retain private-by-default publication. Disconnect removes the profile records; upstream privacy and development audience restrictions remain enforced.

## Custom game prices

`custom_v1` uses the existing named numeric `PriceItem[]` contract, including
currency, payment methods and representative flags. `custom_v2` uses literal
`string[]`, one price or condition per line in order; `[]` means no information.
Strings have no numeric interpretation or exchange-rate conversion. The UI shows
both as Custom and chooses the input mode inside the price editor.
Legacy `custom` accepts both shapes for unchanged round trips and historical
rollback. No migration rewrites stored prices, revisions or changelog rows.
Untouched revisions and rollback retain their original raw price JSON. New
edits may have a legacy price in `before` and a versioned price in `after`.
Guest custom_v2 uses the first-item fallback with `hasHiddenPrices`; custom_v1
retains the existing representative-item policy.
