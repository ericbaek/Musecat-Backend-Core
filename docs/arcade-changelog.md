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
| `PUT /arcade/game` | `game` | one row per request | `game_diff` | Validates `base_state_id`, creates an immutable history batch, then moves `arcade.game_v2` to it. Item IDs are persistent `arcade_game_id` IDs. |
| `POST /arcade/game/bulk_version` | `game` | one row per affected arcade | `game_diff` | Developer/moderator-only administrative version swap. It uses the normal immutable game-state batch flow. |
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

`gtk_type` is server-validated against the GTK catalog, including `ATM`.
ATM uses the normal `bool` and optional `note` fields and has no special
metadata.

Field-level diffs are usually:
- `bool`
- `note`
- `meta` for `Parking` atoms

### `PUT /arcade/game`

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
before/after snapshots. The operation is restricted to `developer` and
`moderator` accounts and does not award XP.

### `PUT /arcade/photo`

`items[]` contains one object per photo atom.

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

### `POST /arcade/rollback`

Rollback logs use the same pattern as the target part, but the `items[]` payload is usually a single `rollback_diff` item with:
- `change_type`: `updated`
- `message`: human-readable summary
- `bullets[]`: rollback-specific translation bullets
- `diff[]`: single field-level before/after entry

The `field` inside the diff is the rolled-back part, such as `basic`, `hour`, `sns`, `gtk`, `game`, `photo`, or `memo`.

## Non-Changelog Endpoints

These mutation endpoints currently do not write `arcade_changelog` rows:

- `PUT /arcade/public`
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
