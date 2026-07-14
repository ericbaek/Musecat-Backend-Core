# Arcade Changelog Rules

## Overview
`arcade_changelog` is the audit trail for arcade mutations.

The important rule is simple:
- only the mutation endpoints listed below create `arcade_changelog` rows
- if an endpoint is not listed, it does not currently write an arcade changelog row

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
| `PUT /arcade/game` | `game` | one row per request | `game_diff` | Creates a new `arcade_game` molecule and new atom rows. |
| `POST /arcade/game/confirm` | `game` | one row per request | `game_diff` | Confirm flow for selected uncertain atoms. |
| `POST /arcade/game/information/confirm` | `game` | one row per request | `game_information_confirm_diff` | Marks one atom as freshly confirmed and refreshes its `updated` timestamp. |
| `POST /arcade/game/bulk_version` | `bulk_game_version` | one row per request | `bulk_game_version_diff` | Bulk version swap for many atoms at once. |
| `PUT /arcade/photo` | `photo` | one row per request | `photo_diff` | Replaces the current `arcade_photo` relation. |
| `POST /arcade/rollback` | the requested part | one row per request | `<part>_diff` | Generic rollback for `basic`, `hour`, `sns`, `gtk`, `game`, or `photo`. |

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
- `game_information_confirm_diff`
- `bulk_game_version_diff`
- `photo_diff`
- `<part>_diff` for rollback rows

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

Field-level diffs are usually:
- `bool`
- `note`
- `meta` for `Parking` atoms

### `PUT /arcade/game`

`items[]` contains one object per game atom. Each item includes:
- `atom_id`
- `prev_id`
- `game`
- `change_type`
- `bullets[]`
- `diff[]`

Field-level diffs are usually:
- `game`
- `location`
- `quantity`
- `price`
- `tag`
- `uncertain`

When a game atom is deleted, the `diff[]` payload uses a synthetic `deleted` field with the removed snapshot.

### `POST /arcade/game/confirm`

Uses the same `game_diff` shape as `PUT /arcade/game`.
The important part is the `uncertain.confirm` bullet for atoms that were confirmed without rollback.

### `POST /arcade/game/information/confirm`

`items[]` contains exactly one object:
- `atom_id`
- `game_id`
- `updated_from`
- `updated_to`

This is a lightweight confirmation log. It does not describe content changes in the game data itself.

### `POST /arcade/game/bulk_version`

The log is request-level, not atom-level.

Outer fields:
- `type`: always `bulk_game_version_diff`
- `version`: always `1`
- `before_game`: the shared old version id
- `after_game`: the shared new version id
- `items[]`: one entry per atom updated by the request

Each item contains:
- `atom_id`
- `arcade_id`
- `arcade_name`

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

The `field` inside the diff is the rolled-back part, such as `basic`, `hour`, `sns`, `gtk`, `game`, or `photo`.

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

- `basic`, `hour`, `sns`, `gtk`, `game`, and `photo` represent the editable arcade parts.
- `game_diff` items are atom-level diffs inside the current game molecule.
- `game_information_confirm_diff` is a lightweight confirmation log for a single atom.
- `bulk_game_version_diff` is a request-level summary log, not an atom-by-atom diff.
- `rollback` logs use the same `<part>_diff` naming pattern as the part that was rolled back.

## Practical Rule

If you change a mutation handler, check two questions:

1. Does it need to write `arcade_changelog`?
2. If yes, is the log shape stable enough for supporter score, audit views, and the frontend?

If the answer is yes, add the endpoint here when you implement it.
