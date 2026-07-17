# Backend Architecture Notes

This document is the high-level map for the backend data model and handler layout.
It is intentionally narrower than the OpenAPI spec:

- `docs/openapi.yaml` describes API wire contracts.
- `docs/architecture-contract.md` defines authorization, ownership, state-transition, and migration policy.
- This file describes why the backend is split the way it is.

The main goal is to keep future collection growth understandable.

## Core Rule

Collection count alone is not a problem.

The backend becomes hard to manage when:

- one feature needs several collections just to read a single page
- a split exists only because of convenience, not domain boundaries
- the same record lifecycle is represented by too many fragments
- no one can tell which collection is the source of truth

The current codebase is mostly split by domain, not by accident.

## Handler Layout

`main.go` currently wires the backend into a few clear areas:

- `handlers/user`
- `handlers/search`
- `handlers/arcade/basic`
- `handlers/arcade/game`
- `handlers/arcade/photo`
- `handlers/arcade/flag`
- `handlers/arcade/gtk`
- `handlers/arcade/hour`
- `handlers/arcade/sns`
- `handlers/arcade/public`
- `handlers/arcade/admin`
- `handlers/arcade/query`

The important pattern is that `handlers/arcade/query` acts as a read facade, while the
other arcade handlers own mutation flows for their respective parts.

## Collection Map

### Identity And User State

| Collection | Responsibility | Main Consumers |
| --- | --- | --- |
| `_pb_users_auth_` | Auth/account records | user signup, withdrawal, auth checks |
| `user_info` | Profile fields such as nickname, avatar, sns, etc. | profile reads and profile updates |
| `user_ban` | Moderation bans and account restrictions | admin and auth gating |
| `user_report` | User report workflow | authenticated reporting and moderation |

### Arcade Core

| Collection | Responsibility | Main Consumers |
| --- | --- | --- |
| `arcade` | Core arcade record and canonical relations | nearly all arcade reads and writes |
| `arcade_basic` | Basic editable details such as name, address, country, location | basic updates, search, arcade summaries |
| `arcade_hour` | Opening-hour data | hour updates and read expansion |
| `arcade_sns` | SNS links | SNS updates and read expansion |
| `arcade_gtk` | GTK-related arcade data, including structured parking meta on `Parking` atoms | GTK updates and read expansion |
| `arcade_game` | Arcade game molecule/group record | game updates and expansion |
| `arcade_game_atoms` | Atomic game rows linked to a molecule | game expansion, moderator rollback/confirm flows |
| `arcade_photo` | Photo molecule record that groups photo atoms | photo updates and read expansion |
| `arcade_photo_atoms` | Uploaded photo atoms/files | upload and photo update flows |
| `arcade_flag` | Flag records for arcade issues | flag create/delete/read paths |
| `arcade_flag_reaction` | Flag reaction records | reaction update flow |
| `arcade_notice` | Public notices attached to arcades | notice read/write flows |
| `arcade_request_admin` | Admin request queue for arcades | admin request creation and review |
| `arcade_changelog` | Audit trail for arcade mutations. See [`arcade-changelog.md`](arcade-changelog.md). | supporter score, audit views, changelog UI |

### Game Metadata

| Collection | Responsibility | Main Consumers |
| --- | --- | --- |
| `game_series` | Series metadata | game expansion and arcade game display |
| `game_series_version` | Version-level metadata tied to a series | game expansion, public read endpoints |
| `game_manufacturer` | Manufacturer metadata | game-series related reads |

### Support And Moderation

| Collection | Responsibility | Main Consumers |
| --- | --- | --- |
| `support_feedback` | Public support feedback queue | support endpoints and admin review |
| `supporter_request` | Supporter qualification request workflow | supporter score and request endpoints |
| `feed` | General feed items | feed queries and related moderation flows |
| `z_error_log` | Error log sink | operational logging and debugging |

### Legacy Or Deleted Collections

These are useful to keep in mind when reading migrations, but they should not be treated as current core model boundaries:

- `arcade_ticket_request`
- `game_series_group`
- `z_legacy_photos`

If a future migration reintroduces one of these ideas, document why before promoting it back into active use.

## Read And Write Paths

Some endpoints read directly from multiple collections, but they still have a clear source-of-truth split:

- `handlers/search/search.go` reads `user` + `user_info` for users, and `arcade` + `arcade_basic` via the arcade query cache for arcades.
- `handlers/arcade/query` is the canonical read layer for combined arcade views.
- Mutation handlers under `handlers/arcade/*` usually own one part of the arcade aggregate and then update related audit or molecule records.

That pattern is healthy as long as the read layer stays a facade and the write ownership stays clear.

## When To Add A New Collection

Create a new collection only when at least one of these is true:

- the data has a different lifecycle from the parent record
- a different role owns the writes
- the record is an append-only log or audit trail
- the data is a fan-out list that would make the parent record too noisy
- the data needs independent moderation or access rules

Do not split just because the form looks larger.

## When To Avoid Another Split

Avoid another collection when:

- the new data is always loaded with the parent and never independently queried
- the same handler would need to join the new collection on every request
- the new collection only exists to keep the schema visually tidy
- the write path is still a single atomic update on the parent aggregate

## Maintenance Checklist

When adding a feature or new collection:

1. Decide whether the new data is source-of-truth, derived read data, or audit history.
2. Put the collection name in `handlers/arcade/internal/collections.go` if it is arcade-related.
3. Add or update the relevant migration.
4. Update the relevant handler doc under `docs/`.
5. If the new collection affects multiple domains, add a short note here explaining the boundary.

## Practical Takeaway

If the backend grows from the current set into the 40+ range, that is still acceptable.
The real question is whether each collection still has a narrow, explainable purpose.

If the answer becomes "no", the fix is usually:

- merge a duplicated read model back into a single source
- move cross-cutting read logic into `handlers/arcade/query`
- keep logs and audit history separate from editable records
