# Arcade Changelog Rules

## Overview
`arcade_changelog` is the audit trail for arcade mutations.

The important rule is simple:
- only the mutation endpoints listed below create `arcade_changelog` rows
- if an endpoint is not listed, it does not currently write an arcade changelog row

Rows are immutable. API v2 serves arcade timelines through `GET /arcade/changelog`
and user-authored timelines through `GET /user/changelog`; clients must not use
PocketBase collection REST to read or mutate them. A report cites an existing
changelog id and derives the reported editor from the row's server-written `by`
value.

Deployment migrations are not user mutations. The guarded Full game-catalog
cutover does not create a changelog row because it has no authenticated editor.
It retains every old game batch and revision, records exact pre-change rows in
the locked migration-origin collection, creates a complete shadow current
batch, and moves `arcade.game_v2` atomically. It must never attribute that
system normalization to an arbitrary user.

Before that cutover, Full's legacy history import preserves each source
`arcade_game.id` as the matching history-batch ID and imports every source
molecule/atom. This is required because existing game changelog `from`/`to`
values are those legacy molecule IDs and must remain valid rollback targets.

Each row uses these common columns:
- `arcade`: the target arcade id
- `changed`: the arcade part that was modified
- `from`: previous value
- `to`: new value
- `by`: the authenticated user who made the change
- `log`: structured diff payload for the UI and supporter scoring

## API Matrix

| Endpoint | `changed` value | Row count | Log shape | Notes |
| --- | --- | --- | --- | --- |
| `PUT /arcade/basic` | `basic` | one row per request | `basic_diff` | Creates a new `arcade_basic` version and points `arcade.basic` to it. |
| `PUT /arcade/hour` | `hour` | one row per request | `hour_diff` | Replaces the current `arcade_hour` relation. |
| `PUT /arcade/sns` | `sns` | one row per request | `sns_diff` | Replaces the current `arcade_sns` relation. |
| `PUT /arcade/gtk` | `gtk` | one row per request | `gtk_diff` | Replaces the current `arcade_gtk` relation. |
| `PUT /arcade/game` | `game` | one row per request | `game_diff` | Accepts required `add`/`modify`/`remove` arrays, materializes the current immutable state, validates `base_state_id`, creates a new immutable history batch, then moves `arcade.game_v2` to it. Item IDs are persistent `arcade_game_id` IDs. |
| `POST /arcade/game/bulk_version` | `game` | one row per affected arcade | `game_diff` | Administrative version swap for developer/moderator or level-30 supporter accounts. It uses the normal immutable game-state batch flow. |
| `POST /campaign/check` | `game` when an old target is updated or reverted | one row | `game_diff` | Campaign checks append a report event with location-verification status. `result=updated` uses the normal immutable game-state batch flow; level-10+ location bypass awards 1 XP. A `still_old` report awards 1 XP and can revert the latest campaign update for seven days. |
| `PUT /arcade/photo` | `photo` | one row per request | `photo_diff` | Replaces the current `arcade_photo` relation. |
| `PUT /arcade/memo` | `memo` | one row per changed request | `memo_diff` | Creates an immutable Tiptap JSON revision and moves `arcade.memo` for an authenticated user with arcade write access. |
| `POST /arcade/rollback` | the requested part | one row per request | `<part>_diff` | Generic rollback for `basic`, `hour`, `sns`, `gtk`, `game`, `photo`, or `memo`; memo rollback requires authenticated arcade write access. |

The read-only arcade timeline accepts the optional
`changed=basic|game|hour|sns|gtk|photo|memo` filter. The user timeline is available
through `GET /user/changelog?user=...` and accepts the same optional filter. It
uses the same immutable rows, but scopes them by `arcade_changelog.by` and
adds `arcade_name` for profile timelines. Anonymous callers see only rows whose
arcade is public; the authenticated owner may also see their private arcade
rows, and `developer`/`moderator` reviewers may audit all rows. The optional
`changed` filter accepts exactly `basic`, `game`, `hour`, `sns`, `gtk`, or
`photo`, or `memo`. These are the complete current changelog categories in the Core
bootstrap schema and mutation handlers; activity-only values such as `flag`,
`flag_reaction`, `visit`, and `legacy` are not `arcade_changelog.changed`
values and are intentionally excluded. Rows whose arcade aggregate was deleted
are omitted, and withdrawn users return an empty page to avoid exposing
history after profile withdrawal.

When rollback includes `report=true`, its cited prior changelog and the `rollback_report` record are created/validated in the same transaction as the rollback. `POST /arcade/edit_report` is the report-only path and does not change the arcade.

## Log Shape Reference

All changelog rows use the same outer wrapper:

```json
{
  "type": "basic_diff",
  "version": 1,
  "items": []
}
```

The meaning of `type` changes per endpoint:

- `basic_diff`
- `hour_diff`
- `sns_diff`
- `gtk_diff`
- `game_diff`
- `photo_diff`
- `memo_diff`
- `<part>_diff` for rollback rows

### `memo_diff`

Memo rows identify the previous and next immutable `arcade_memo` revision in
`from` and `to`. The API additionally exposes `memo.before` and `memo.after`
document snapshots so clients can render a Tiptap before/after comparison.
An empty document is still a real revision and is never represented as a
delete. A rollback points `arcade.memo` directly at the selected prior
revision and writes another `memo_diff` row, preserving every earlier row.

Both timeline endpoints return `memo.before_status` and `memo.after_status`:
`available` includes an empty document, `absent` means no revision ID, and
`unavailable` means a missing revision or a reference to a different arcade.
An unavailable snapshot must not be displayed as a newly added/deleted memo.

### `PUT /arcade/basic`

Request body may update any of these fields:

- `name`
- `address`
- `direction`
- `nickname`
- `subway_line`
- `location`

`items[]` contains one object with:
- `change_type`: `added`, `updated`, or `unchanged`
- `bullets[]`: translated bullet keys for each changed field
- `diff[]`: field-level before/after values

Fields inside each diff item:
- `name`
- `address`
- `direction`
- `nickname`
- `location`
- `subway_line`

### `PUT /arcade/hour`

`items[]` contains one object with:
- `change_type`
- `bullets[]`
- `diff[]`

Fields inside each diff item:
- `Monday`
- `Tuesday`
- `Wednesday`
- `Thursday`
- `Friday`
- `Saturday`
- `Sunday`
- `Note`

This log preserves the difference between `null`, `499`, and `{ start, end }`.

### `PUT /arcade/sns`

`items[]` contains one object per SNS atom. Each item includes:
- `atom_id`
- `prev_id`
- `sns_type`
- `link`
- `name`
- `change_type`
- `bullets[]`
- `diff[]`

Field-level diffs are usually:
- `link`
- `name`

### `PUT /arcade/gtk`

`items[]` contains one object per GTK atom. Each item includes:
- `atom_id`
- `prev_id`
- `gtk_type`
- `change_type`
- `bullets[]`
- `diff[]`

`gtk_type` is server-validated against the GTK catalog, including `ATM`,
`SellFood`, and `SeatingArea`. These use the normal `bool` and optional `note`
fields and have no special metadata. `SellFood` means food is available for
purchase; `SeatingArea` means the arcade has a place to sit, such as chairs or
sofas behind the games.

Field-level diffs are usually:
- `bool`
- `note`
- `meta` for `Parking` atoms

### `PUT /arcade/game`

The public request is a Delta payload, not the legacy full `games[]` payload:

```json
{
  "arcade": "arcade_123",
  "base_state_id": "batch_current",
  "add": [],
  "modify": [{
    "id": "arcade_game_123",
    "game": "version_2",
    "cabinet": "cabinet_dx",
    "location": "2F",
    "quantity": 2,
    "price": {"currency": "KRW", "type": "custom", "list": [{"value": 500}], "accept": []},
    "tag": [{"category": "기타", "note": "updated"}]
  }],
  "remove": []
}
```

All three arrays are required. `add` contains complete objects without `id`,
`modify` contains complete replacement objects with an active `id`, and
`remove` contains active `arcade_game_id` strings. A single modify does not
remove any other active game. A stale `base_state_id` returns `409`; clients
should reload the current game state and retry their intended delta. The old
`games[]` request must not be sent to this endpoint.

The server writes one immutable batch and one `game_diff` row for every
successful request. Each item records `before` and `after` snapshots and one of
`added`, `updated`, `unchanged`, or `deleted`. Removed entries are not deleted
from `arcade_game_id`, so historical flags remain attached and are returned as
`orphanFlags` until that entry becomes active again.

When an add has a verified cabinet, the server may reuse the newest inactive
entry for the same arcade, canonical series, and cabinet. It orders candidates
by matching history revision `created`, entry `created`, and entry id, all
descending. Empty/unverified cabinets never participate in reuse. Reusing an
entry also reactivates its durable flags. A different cabinet creates a new
entry. Version changes are allowed only within the entry's series.

`items[]` contains one object per stable game entry. Each item includes:
- `entry_id`
- `change_type`: `added`, `updated`, `unchanged`, or `deleted`
- `before`: the previous revision snapshot, or `null`
- `after`: the resulting revision snapshot, or `null`

Each non-null revision snapshot contains:

- `version`
- `cabinet`: canonical `game_cabinet` id, or an empty value when unverified
- `location`
- `quantity`
- `price`
- `tag`
- `uncertain`
- `previous_version`

A cabinet change is a normal game-state change and must appear in these
before/after snapshots. State-cloning mutations must preserve the cabinet so a
confirmation, rollback, or bulk version action cannot silently erase cabinet
identity.

The row-level `state_from` and `state_to` values identify the immutable
history batches selected before and after the mutation. This keeps the log
self-contained without any review-only game metadata.

### `POST /arcade/game/bulk_version`

This endpoint uses the normal `game_diff` shape described for `PUT /arcade/game`.
There is one changelog row per affected arcade. Its `state_from` and `state_to`
identify the immutable revision batches, and `items[]` contains entry-level
before/after snapshots. The operation is restricted to `developer`/`moderator`
accounts and supporters who have reached level 30; it does not award XP.

### `PUT /arcade/photo`

`items[]` contains one object per photo atom.

The mutation promotes pending atoms and awards XP only for atoms whose
`public` flag was false before the transaction. Each newly published atom is
worth 2 XP, capped over a rolling seven-day window at 10 XP for an automatic
photo-campaign target and 4 XP for another public arcade. Uploading a pending
atom, changing order, removing a gallery member, or re-adding an already
published atom does not award XP. Aggregate pointer, atom promotion, changelog,
and XP ledger updates commit atomically.

Each item includes:
- `atom_id`
- `prev_id`
- `photo`
- `change_type`
- `bullets[]`
- `diff[]`

Photo items are mostly membership changes:
- `added` when the atom is newly attached
- `unchanged` when the atom was already part of the same photo molecule
- `deleted` when an old atom is removed from the molecule

Removal means **removed from the arcade gallery**, not made private. Published
atoms remain immutable and public independently of current gallery membership.
Both timeline endpoints add `photo_assets: [{id, file_url}]` at read time, using
the same authorization as the custom photo-file route. Only referenced atoms
belonging to the row's arcade are eligible; missing atoms or file references, foreign atoms,
and inaccessible atoms are omitted. An empty array is not evidence of deletion.
The frontend uses its authenticated same-origin file proxy for these references.

Presentation uses structured diffs/snapshots as the primary detail. Translation
bullets are a legacy fallback, not an additional copy of each displayed diff.
`unchanged` items may be summarized; an all-unchanged record is distinct from
missing or unreadable historical detail. SNS/GTK deletions may embed the previous
atom in `diff[].from` under `field="deleted"`; clients extract its known fields.

### `POST /arcade/rollback`

New rollback envelopes include `source: "rollback"`. Older envelopes can be
recognized by the `arcade.changelog.rollback.applied` bullet key. Their diff
values are relation IDs, not user-facing field values or photo membership
changes. Display a restore event (and memo snapshots when available). Earlier
changelog rows remain valid immutable evidence; rollback does not invalidate them.

Rollback logs use the same pattern as the target part, but the `items[]` payload is usually a single `rollback_diff` item with:
- `change_type`: `updated`
- `message`: human-readable summary
- `bullets[]`: rollback-specific translation bullets
- `diff[]`: single field-level before/after entry

The `field` inside the diff is the rolled-back part, such as `basic`, `hour`, `sns`, `gtk`, `game`, `photo`, or `memo`.

## Non-Changelog Endpoints

These mutation endpoints currently do not write `arcade_changelog` rows:

- `PUT /arcade/public` (a supporter-tagged creator may bypass only the game, contact/hours, and Korea facility-photo requirements with explicit confirmation)
- `POST /arcade/flag`
- `POST /arcade/flag/delete`
- `POST /arcade/flag/reaction`
- `POST /arcade/request_admin`
- `POST /arcade/photo/upload`

## Practical Reading Order

If you are trying to understand one changelog row, read it in this order:

1. `changed` on the `arcade_changelog` row tells you which arcade part moved.
2. `log.type` tells you which schema to use.
3. `log.items[]` tells you whether the change was a part-level edit, an atom-level edit, or a request-level summary.
4. `bullets[]` is the user-facing explanation.
5. `diff[]` is the machine-readable before/after snapshot.

## How To Read The Log

- `basic`, `hour`, `sns`, `gtk`, `game`, `photo`, and `memo` represent the editable arcade parts.
- Memo diffs expose document snapshots under `memo.before` and `memo.after`; an empty document is a valid revision.
- `game_diff` items are entry-level before/after snapshots inside the current game state.
- Administrative bulk version swaps use the same `game_diff` log as ordinary game edits.
- `rollback` logs use the same `<part>_diff` naming pattern as the part that was rolled back.

## Practical Rule

If you change a mutation handler, check two questions:

1. Does it need to write `arcade_changelog`?
2. If yes, is the log shape stable enough for supporter score, audit views, and the frontend?

If the answer is yes, add the endpoint here when you implement it.


### Passport city classification

`PUT /arcade/basic` accepts `city_id` (empty clears it), validates the city's country,
and records its before/after value in the existing immutable `basic_diff` revision.
Address/location changes clear an omitted city reference. Generic basic rollback restores
the selected revision's city reference. `base_basic_id` optionally rejects a stale write
with 409 before changing any row. Full's reviewed assignment command uses these normal
mutations and authenticated editor attribution, including the existing basic-edit XP
cooldown. GeoNames catalog import is reference-data maintenance and grants no XP.
