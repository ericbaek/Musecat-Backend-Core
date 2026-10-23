# Native games v1 contract

`GET|PATCH /native/v1/arcades/{id}/games` is the first-party native (iOS)
editing contract for the arcade game inventory. It is a fixed, versioned delta
contract on top of the existing immutable game state pipeline
(`arcade_game_id` / `arcade_game_history_batch` / `arcade_game_history`,
selected by `arcade.game_v2`). The web `PUT /arcade/game` delta API
(`docs/arcade-game-delta-api.md`), its response shape and the stored schema
are unchanged; no migration is involved.

Implementation: `handlers/arcade/game/native_v1.go`. The mutation itself is the
shared web path (`materializeUpdateGameDelta` + `UpdateArcadeGameTx` + the game
XP grant) inside one transaction; the native layer only decodes, validates
shape, merges `changes` onto the stored revision and maps errors to stable
codes. Regression test: `tests/user/native_games_test.go` (real HTTP, native
session).

## Integration status

The routes are registered in `nativeauth/routes.go` next to the other native
v1 adapters (same `/native` group, `apis.BodyLimit(4096)`, bearer guard via
`nativeauth.Service.Authenticate`, `user.RequireActiveUser()`), and
`tests/user/native_games_test.go` exercises them through
`nativeauth.RegisterRoutes` exactly like the GTK regression. OpenAPI
(`docs/openapi.yaml`, `/native/v1/arcades/{id}/games` plus the
`NativeGamePrice` / `NativeGameTag` schemas), the architecture contract
("Native game inventory v1") and `docs/arcade-changelog.md` ("Native game
inventory v1": native saves write the existing `game_diff` v2 envelope, no new
history shape) describe the same contract. Backend Full pins Core and adds no
alternate implementation or schema migration.

## Shared rules

- Authentication: an active first-party native session (`Authorization:
  Bearer <native access token>`). Web JWTs and partner tokens are rejected with
  401. Withdrawn/banned accounts are rejected by the shared active-user guard.
- Authorization: the same arcade write rule as every web mutation
  (`arcadeinternal.CanWriteArcade`): any active user may edit a public arcade;
  a private draft requires its creator or a developer/moderator. Violations
  return 403 `{"error":"arcade_write_forbidden"}`.
- Unknown arcade: 404 `{"error":"arcade_not_found"}`.
- Request ceiling: 4096 encoded bytes (shared native group body limit, 413).
  See "Body limit evidence" below.
- `contract_version` is `1`; `required_capabilities` is `["games.delta.v1"]`.
  A client that does not support the version or every required capability
  must not offer editing. Unknown optional response members must be ignored.
- Every 4xx body is the `ErrorResponse` envelope `{"error": <stable code>}`.
  The handler decodes the body locally and treats any decode failure as a
  non-nil error, so a 400 can never be followed by a mutation.

## GET snapshot

```json
{
  "contract_version": 1,
  "required_capabilities": ["games.delta.v1"],
  "arcade": "arcade_id",
  "revision": "arcade_game_history_batch id or empty string",
  "items": [
    {
      "id": "arcade_game_id",
      "series": "game_series id",
      "game": "game_series_version id",
      "cabinet": "game_cabinet id or \"\"",
      "location": "1F",
      "quantity": 2,
      "price": {"currency": "KRW", "type": "credit", "list": [{"title": "1 credit", "value": 500}], "accept": ["Cash"]},
      "tag": [{"category": "모니터", "note": "120Hz"}],
      "updated": "2026-10-18 09:00:00.000Z",
      "catalog": {
        "version": {"id": "...", "series": "...", "released_on": null, "en": "Maimai 2026", "kr": "...", "jp": "...", "price_default": null},
        "series": {"id": "...", "seriesNumber": 7001, "en": "Maimai", "kr": "...", "jp": "...", "en_short": "...", "kr_short": "...", "jp_short": "...", "manufacturer": ""},
        "cabinet": {"id": "...", "en": "DX", "kr": "DX", "jp": "DX"}
      }
    }
  ]
}
```

- `revision` is the current `arcade.game_v2` batch id. An absent inventory
  returns `""` and `items: []`; the first PATCH must send `"revision": ""`.
- `items` are the revisions of exactly that batch, in stored (revision
  `created`) order; the client sorts for display.
- **`id` is the durable `arcade_game_id` and is stable across saves** (unlike
  the contacts/GTK atom ids). After a successful PATCH the same ids remain
  valid against the new `revision`; only `revision` changes. Removed ids leave
  the active set and are rejected with `invalid_game_id` until the web
  identity-reuse rule re-activates them.
- `series` is the entry's series (an entry can never move to another series).
  `cabinet` is `""` for an unverified cabinet.
- `price` and `tag` are the stored JSON values as-is (the legacy tag
  `quantity` member is stripped, as on every read). Known shapes are below;
  anything else is a legacy or future shape the client shows read-only and may
  only replace wholesale.
- `catalog` is a read-only display convenience built from the current catalog;
  each member is `null` when the record is missing. It is never sent back.
- A batch pointing at another arcade, or an unreadable revision, returns 500
  `{"error":"games_unavailable"}` and is never exposed.
- Pickers use the existing public `GET /game/catalog?locale=…` (series with
  cabinets, versions with their compatible `cabinets[]`).

## PATCH delta

```json
{
  "revision": "batch id or empty string",
  "add": [{"game": "version_id", "cabinet": "cabinet_id", "location": "1F", "quantity": 2, "price": {"currency": "KRW", "type": "credit", "list": [{"title": "1 credit", "value": 500}], "accept": ["Cash"]}, "tag": []}],
  "update": [{"id": "arcade_game_id", "changes": {"quantity": 3, "price": {"currency": "KRW", "type": "credit", "list": [{"value": 600}], "accept": []}}}],
  "remove": ["arcade_game_id"]
}
```

Success (200):

```json
{"arcade": "arcade_id", "revision": "new_batch_id", "count": 2, "xp_feedback": {"diff_exp": 4, "...": "..."}}
```

`count` is the number of active entries after the save. The client replaces
`revision` from the response; a GET afterwards is recommended to pick up
server normalization (trimming) but is not required for id validity.

### Operation semantics

- `revision` is mandatory (the key must be present, `""` only for the first
  save). It is compared with `arcade.game_v2` inside the mutation transaction
  before any write; a mismatch is 409 `revision_conflict` with no batch,
  history or XP. Clients keep their draft and reload explicitly.
- At least one of `add`, `update`, `remove` must be non-empty. Otherwise 400
  `revision_and_operations_required`.
- **Omission never deletes and never flattens.** Entries not named in
  `update`/`remove` are copied verbatim into the new batch: raw price/tag JSON
  (including members this contract does not know) and any `arcade_game_history`
  column the pipeline does not know (see "Future columns").
- `add[]` creates one entry per object. `game` (string version id),
  `quantity` (integer ≥ 1) and `price` are required; `cabinet` (default `""`,
  unverified), `location` (default `""`, max 500 characters) and `tag`
  (default `[]`) are optional. No `id` is ever sent; the server applies the web
  identity-reuse rule (same series + verified cabinet of a removed entry).
- `update[]` identifies one active entry by `id` and changes **only the keys
  present in `changes`**: `game`, `cabinet`, `location`, `quantity`, `price`,
  `tag`. Omitted keys keep their stored value. `changes` must not be empty. A
  provided `price` or `tag` replaces that stored value wholesale (web parity);
  there is no member-wise price patching in v1.
- `remove[]` removes entries by explicit id. Removing every entry is valid and
  yields a revision with no items. Entry records and past history are never
  deleted (flags keep their durable reference, as on the web).
- Each id may occur in at most one `update` or `remove` entry.
- No null values: every provided value must have the documented type. Clear
  cabinet with `""`, location with `""`, tags with `[]`.
- Apply order inside the transaction is remove, then update, then add. `add`
  may re-create a version+cabinet pair that the same request removes.
- No-op detection: a request with no add, no remove, and updates that leave
  every entry equal after normalization is rejected with 400 `no_changes` and
  writes nothing. (The web API would create an `unchanged` batch; native does
  not.)
- Unknown keys anywhere inside an add object, `changes`, `price`, a price list
  row or a tag item are rejected with 400 `unsupported_field` instead of being
  dropped. Unknown keys on the top level, an update wrapper, or a malformed
  document are 400 `invalid_patch`.

### Validation (web policy, no new rules)

Shape checks run before any lookup; catalog and identity checks run inside the
transaction. The merged full object of an updated entry and every add object
go through the same `validateGameAtomFields` / `materializeUpdateGameDelta`
path as a web `modify`/`add`:

| rule | error |
| --- | --- |
| `game` is a string naming an existing `game_series_version` | `invalid_game_version` |
| update `game` stays in the entry's series | `series_mismatch` |
| non-empty `cabinet` exists in `game_cabinet` | `invalid_cabinet` |
| non-empty `cabinet` is linked to the resulting version in `game_series_version_cabinet` | `cabinet_not_supported` |
| `location` is a string of at most 500 characters | `invalid_location` |
| `quantity` is an integral JSON number ≥ 1 | `invalid_quantity` |
| `price` passes the shared price policy (below) | `invalid_price` |
| `tag` passes the shared tag policy (below) | `invalid_tag` |
| resulting list has no duplicate version+cabinet pair | `duplicate_game` |

**Consequence of web parity (documented limitation):** updating any field of
an entry whose *stored* price or tag no longer passes the current policy (for
example a legacy `type`) is rejected with `invalid_price`/`invalid_tag`, exactly
like a web `modify`. The client must send a valid full `price`/`tag` in the
same update. Untouched entries are never revalidated, so legacy data elsewhere
in the inventory never blocks an edit.

#### Price object

`{"currency", "type", "list", "accept"}` — all four keys required, no others.

- `currency`: non-empty string. The server enforces no enum (web sends
  uppercase ISO codes such as `KRW`, `JPY`, `USD`).
- `type`: one of `gamemode`, `credit`, `song`, `time`, `free`, `custom`,
  `custom_v1`, `custom_v2`.
- `list`: for `custom`/`custom_v2` an array of non-blank strings (`[]` allowed
  for `custom`); for every other type an array of ≥ 1 objects
  `{title?: string|number, value: number > 0 | null, mode_key?: string,
  represent?: boolean}`. Mixed string/object lists are rejected; a list object
  with any other member is `unsupported_field`.
- `accept`: array (may be `[]`) of exactly `Credit Card`, `Prepaid Card`,
  `Cash`.
- Storage normalization (web parity): `currency`, `type`, titles, `mode_key`
  and `accept` entries are trimmed; blank custom lines are dropped; omitted
  `represent` compares equal to `false` for no-op detection.

#### Tag array

Array of `{"category", "note"}` rows, no other members. `category` is exactly
one of `케비넷 타입`, `로테이션 버전`, `모니터`, `스위치`, `사운드`, `녹화방송`,
`기타`; `note` is a non-blank string. Values are stored untrimmed (web parity).

### Error codes

| status | `error` | meaning |
| --- | --- | --- |
| 400 | `invalid_patch` | body is not one strictly-shaped JSON object (unknown top-level/update-wrapper member, wrong member type, `null` add item, trailing data, empty body) |
| 400 | `revision_and_operations_required` | `revision` key missing/null, or no operation |
| 400 | `unsupported_field` | unknown key inside an add object, `changes`, `price`, a price list row, or a tag item |
| 400 | `invalid_game_id` | update/remove id empty, unknown, not active in the base revision, used twice, or `changes` empty |
| 400 | `invalid_game_version` | `game` missing / not a string / blank, or version not found |
| 400 | `series_mismatch` | update `game` belongs to another series |
| 400 | `invalid_cabinet` | `cabinet` not a string, or non-empty cabinet not found |
| 400 | `cabinet_not_supported` | cabinet not linked to the resulting version |
| 400 | `invalid_location` | `location` not a string or over 500 characters |
| 400 | `invalid_quantity` | `quantity` missing, non-integral, `< 1`, or not a JSON number |
| 400 | `invalid_price` | price missing/not an object, or violates the price policy (including a revalidated stored legacy price) |
| 400 | `invalid_tag` | tag not an array of `{category, note}` objects, or violates the policy |
| 400 | `duplicate_game` | same version+cabinet twice in the resulting list |
| 400 | `no_changes` | operations leave every entry equal |
| 401 | (PocketBase envelope) | missing/invalid native session |
| 403 | `arcade_write_forbidden` | account or arcade write access denied |
| 404 | `arcade_not_found` | |
| 409 | `revision_conflict` | stale base revision |
| 413 | (PocketBase envelope) | body over 4096 bytes |
| 500 / 502 | `games_unavailable` | GET / PATCH: batch unreadable or belongs to another arcade |
| 502 | `transaction failed` (+`details`) | persistence failure inside the transaction |

Pipeline validation messages (the web `details` strings) are mapped to these
codes by `nativeGameCodeForMessage`; the native layer never exposes the raw
message.

## History, XP, cache

A successful PATCH reuses the shared game pipeline inside one transaction:

1. CAS of `revision` against `arcade.game_v2`;
2. `materializeUpdateGameDelta`: the current batch is cloned in memory (raw
   price/tag per entry), removals dropped, merged updates and adds applied,
   the full list validated (duplicates, references);
3. `UpdateArcadeGameTx`: one new `arcade_game_history_batch` (`reason: edit`)
   and one `arcade_game_history` row per active entry, entries created or
   reused by the web identity rule, `last_modified_*` kept for unchanged rows;
4. `arcade.game_v2` repointed through `UpdateArcadeFieldsTxWithLogs`, which
   writes exactly one `arcade_changelog` row with the existing `game_diff`
   version 2 envelope (`added`/`updated`/`unchanged`/`deleted` items with
   `before`/`after` snapshots, `state_from`/`state_to`);
5. the existing game XP grant (`xp.AwardArcadeGameEditExpTx`: `min(10, 2n)`
   over distinct changed entries in a rolling 7-day window per user+arcade,
   public arcades only). Private drafts return `diff_exp: 0`.

Stale, invalid and no-op requests create no batch, history or XP. Previous
batches and their revisions are never modified (verified by the test).

### Future columns

`updateArcadeGameTx` now copies every `arcade_game_history` column it does not
know (`copyGameRevisionExtras`, anything outside `id`, `created`, `updated`,
`batch`, `entry`, `version`, `cabinet`, `location`, `quantity`, `price`,
`tag`, `last_modified_at`, `last_modified_by`, `legacy_imported`) from the
entry's previous revision into the new one, before the known fields are set.
This applies to the web `PUT /arcade/game` path as well, so neither a web
full-object `modify` nor a native changes-only update can drop a column added
after the client shipped. `legacy_imported` keeps its existing behaviour (it
is not carried forward) because it drives the partial unique index on unknown
cabinets.

## Body limit evidence

The shared `/native` group limit is 4096 bytes and was **not** changed. The
regression test measures a realistic single-add PATCH (verified cabinet, a
Korean location, two gamemode price rows with `mode_key`/`represent`, two
accept methods, two tags): 442 bytes. Roughly nine such adds, or more simple
ones, fit in one request; updates are smaller. If the native client needs
larger batches (for example seeding a new arcade with a long inventory in one
save), the recommended change is a route-specific
`.Bind(apis.BodyLimit(16 << 10))` on the two games routes only, which keeps
every other native route at 4096 and still bounds a games request at about
35 adds. The client should otherwise split large inventories into several
PATCHes, each against the latest `revision`.

## Compatibility promise

- v1 is additive only. New response members may appear and must be ignored.
  New `arcade_game_history` columns and unknown price/tag members survive
  old-client saves via the revision copier and changes-only merging.
- No new required input and no type/meaning change inside v1. A breaking
  change publishes v2 alongside v1; v1 stays until an explicitly reviewed
  retirement.
- Backend Full pins the Core revision whose native contract tests pass; Full
  adds no alternate implementation and no schema migration for this contract.

## Verification

```bash
go test ./tests/user -run TestNativeGamesV1 -count=1 -v
go test ./handlers/arcade/game ./handlers/arcade/internal ./tests/arcade -count=1
go test ./... -count=1
```

Numeric prices accept the optional `paseli_value` row member and `PASELI`
payment method with the shared web rules: the amount must be positive, use JPY
and explicitly accept PASELI. Omitted values remain unknown; old-client saves
that omit price keep the complete stored price, including PASELI amounts.
