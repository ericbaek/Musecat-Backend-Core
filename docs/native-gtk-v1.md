# Native GTK v1 contract

`GET|PATCH /native/v1/arcades/{id}/gtk` is the first-party native (iOS) editing
contract for the arcade facility ("GTK") section. It is a fixed, versioned
delta contract on top of the existing GTK molecule/atom pipeline. The web
`PUT /arcade/gtk` replacement API, its response shape and the stored schema are
unchanged; no migration is involved.

Implementation: `handlers/arcade/gtk/native_v1.go`. Routes are registered by
`nativeauth.RegisterRoutes` under the native bearer guard and the active-user
guard. Regression test: `tests/user/native_gtk_test.go` (real HTTP, native
session).

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
- `contract_version` is `1`; `required_capabilities` is `["gtk.delta.v1"]`. A
  client that does not support the version or every required capability must
  not offer editing. Unknown optional response members must be ignored.
- Every 4xx body is the `ErrorResponse` envelope `{"error": <stable code>}`.
  The handler decodes the body locally and treats any decode failure as a
  non-nil error, so a 400 can never be followed by a mutation.

## GET snapshot

```json
{
  "contract_version": 1,
  "required_capabilities": ["gtk.delta.v1"],
  "arcade": "arcade_id",
  "revision": "arcade_gtk_id or empty string",
  "items": [
    {"id": "atom_id", "type": "Parking", "bool": true, "note": "B1", "meta": {"geo": {"lat": 37.567, "lon": 126.9785}, "availability": "somewhat_plenty"}},
    {"id": "atom_id", "type": "FreeWifi", "bool": true, "note": "", "meta": null}
  ]
}
```

- `revision` is the current `arcade.gtk` molecule id. An absent section returns
  `""` and `items: []`; the first PATCH must send `"revision": ""`.
- `items` are the atoms of exactly that revision, in stored order. `id` is the
  atom id inside this immutable revision; `note` is always a string; `meta` is
  the stored JSON value as-is (object with every member, or `null`).
- Items whose `type` is not in the v1 catalog below (a future server type) are
  returned unchanged. The client shows them read-only.
- A relation pointing at a molecule of another arcade, or an unreadable
  revision, returns 500 `{"error":"gtk_unavailable"}` and is never exposed.

## PATCH delta

```json
{
  "revision": "arcade_gtk_id or empty string",
  "add": [{"type": "FreeWifi", "bool": true, "note": "fast"}],
  "update": [{"id": "atom_id", "changes": {"bool": false, "note": "", "meta": {"availability": "difficult"}}}],
  "remove": ["atom_id"]
}
```

Success (200):

```json
{"arcade": "arcade_id", "revision": "new_arcade_gtk_id", "xp_feedback": {"diff_exp": 2, "...": "..."}}
```

Every save creates a new immutable `arcade_gtk` revision and copies every atom
into it, so **all atom ids change after a save**. The client must GET a fresh
snapshot before the next edit; ids from an earlier revision are rejected with
400 `invalid_gtk_id`.

### Operation semantics

- `revision` is mandatory (the key must be present, `""` only for the first
  save). It is compared with `arcade.gtk` inside the mutation transaction before
  any write; a mismatch is 409 `revision_conflict` with no revision, history or
  XP. Clients keep their draft and reload explicitly; they never rebase silently.
- At least one of `add`, `update`, `remove` must be non-empty. Otherwise 400
  `revision_and_operations_required`.
- **Omission never deletes.** Atoms not named in `update`/`remove` are copied
  verbatim into the new revision, including columns, types and metadata members
  this contract does not know. Molecule-level columns are copied the same way.
- `add[]` creates one atom per entry. `type` must be in the v1 catalog and
  `bool` must be a JSON boolean. `note` (optional) must be a string; it is
  trimmed. `meta` is allowed only for `Parking`.
- `update[]` identifies one atom of the base revision by `id` and changes only
  the provided keys of `changes`: `bool` (boolean), `note` (string, trimmed, `""`
  clears) and/or `meta` (Parking only). `type` is immutable; changing a type is
  an explicit `remove` plus `add`. `changes` must not be empty.
- `remove[]` deletes atoms by explicit id. Removing every atom is valid and
  yields a revision with no items.
- `add` may re-create a type that the same request removes (replace), but a
  type that would still exist after removals is rejected with
  `duplicate_gtk_type`; duplicate types inside `add` are rejected the same way.
- Each atom id may occur in at most one `update` or `remove` entry.
- Atoms of a type outside the v1 catalog can be kept (untouched) or explicitly
  removed, but not updated or added: 400 `unsupported_gtk_type`.
- Setting `bool: false` does not implicitly drop `note` or `meta` (same as
  `PUT /arcade/gtk`). A client that wants the web editor's behaviour sends
  `"note": ""` and `"meta": null` explicitly.
- No-op detection: after trimming and metadata normalization, a request whose
  operations change nothing (updates only) is rejected with 400 `no_changes`
  and writes nothing. Any `add` or `remove` is a change.

### v1 GTK catalog

`Basket`, `FreeGloves`, `FreeSlipper`, `FreeWater`, `FreeDrumStick`,
`FreeBachi`, `FreeWifi`, `Locker`, `Toilet`, `AC`, `Fan`, `Refrigerator`,
`PowerOutlet`, `SellGloves`, `SellDrumStick`, `SellBachi`, `SellSlipper`,
`SellDrink`, `SellFood`, `SellAmusementIC`, `SellAMPASS`, `Parking`,
`SmokingRoom`, `ATM`, `SeatingArea`.

This is the same closed catalog `PUT /arcade/gtk` and the web editor use.
Values are exact (case-sensitive, untrimmed).

### Parking metadata

`meta` is accepted only when the atom type is `Parking` (add or update);
otherwise 400 `meta_not_allowed`, even for `meta: null`.

Supported members, validated exactly like the web editor
(`DataKit/web/services/arcade/gtk/updateArcadeGtk.server.ts`) and Core
`PUT /arcade/gtk`:

| member | value | rule | error |
| --- | --- | --- | --- |
| `geo` | `{"lat": number, "lon": number}` | both members required, JSON numbers, `|lat| <= 90`, `|lon| <= 180`; no other members | `invalid_parking_geo`, `unsupported_field` |
| `availability` | string | exactly one of `always_plenty`, `somewhat_plenty`, `somewhat_difficult`, `difficult` | `invalid_parking_availability` |
| `options` | array of strings | each exactly one of `free_parking_lot`, `paid_parking_lot`, `free_street_parking`, `paid_street_parking`; no duplicates; `[]` allowed | `invalid_parking_options` |
| `ev_charging` | boolean | | `invalid_parking_flag` |
| `gov_parking` | boolean | settable only when `arcade.country` is Korean (`KR`, `KOREA`, `대한민국`, case-insensitive); `null` clears anywhere | `invalid_parking_flag`, `gov_parking_not_available` |

- Any other member of `meta` or of `geo` in a request is 400
  `unsupported_field`. `meta` that is not an object or `null` is
  `invalid_gtk_value`.
- **Patch semantics (update):** only the provided members change. Omitted
  members, members this contract does not know, and unknown members inside the
  stored `geo` object are preserved. A provided `geo` replaces only `lat` and
  `lon` inside the stored `geo`.
- **Explicit null:** `"meta": null` clears the whole metadata object (including
  unknown members). `"geo": null`, `"availability": null`, `"options": null`,
  `"ev_charging": null`, `"gov_parking": null` remove only that member.
  Clearing something that is already absent is not a change.
- An empty resulting object is stored as no metadata (`meta: null` on GET).
- **Distance rule (web parity):** when the resulting `geo` differs from the
  base atom's stored `geo` (or the atom is new) and the arcade has a basic
  location, the entrance must be within 300 m (haversine,
  `arcadeinternal.DistanceKm`) of that location, else 400
  `parking_geo_too_far`. An unchanged stored entrance is never revalidated by
  an unrelated edit, so legacy data stays editable.
- **Unreadable stored shape:** if the stored `meta` (or its `geo`) is not an
  object, member-wise patches are rejected with 400 `unsupported_meta_shape`;
  v1 never produces a hybrid and never silently replaces what it cannot read.
  `"meta": null` remains a deliberate whole-object replacement.
- On `add`, `meta: null` and `meta: {}` both mean "no metadata".

### Error codes

| status | `error` | meaning |
| --- | --- | --- |
| 400 | `invalid_patch` | body is not one strictly-shaped JSON object (unknown member, trailing data, wrong member type, empty body) |
| 400 | `revision_and_operations_required` | `revision` key missing/null, or no operation |
| 400 | `unsupported_gtk_type` | `add` type outside the v1 catalog, or `update` on an atom whose type is outside it |
| 400 | `duplicate_gtk_type` | type repeated inside `add`, or already present after removals |
| 400 | `invalid_gtk_id` | unknown/empty id, id used twice, id from another revision, empty `changes` |
| 400 | `unsupported_field` | unknown key in `changes`, `meta` or `geo` |
| 400 | `invalid_gtk_value` | `bool`/`note` missing or wrong JSON type (including null), `meta` neither object nor null |
| 400 | `meta_not_allowed` | `meta` for a non-Parking atom |
| 400 | `invalid_parking_geo` | see table above |
| 400 | `invalid_parking_availability` | see table above |
| 400 | `invalid_parking_options` | see table above |
| 400 | `invalid_parking_flag` | `ev_charging`/`gov_parking` not boolean |
| 400 | `gov_parking_not_available` | boolean `gov_parking` on a non-Korean arcade |
| 400 | `parking_geo_too_far` | changed entrance farther than 300 m from the arcade |
| 400 | `unsupported_meta_shape` | member patch against a stored non-object `meta`/`geo` |
| 400 | `no_changes` | operations leave every atom equal |
| 401 | (PocketBase envelope) | missing/invalid native session |
| 403 | `arcade_write_forbidden` | account or arcade write access denied |
| 404 | `arcade_not_found` | |
| 409 | `revision_conflict` | stale base revision |
| 413 | (PocketBase envelope) | body over 4096 bytes |
| 500 / 502 | `gtk_unavailable` | GET / PATCH: revision unreadable or belongs to another arcade |
| 502 | `transaction failed` (+`details`) | persistence failure inside the transaction |

Shape errors (`invalid_patch`, catalog/enum/type/duplicate checks) are
evaluated before the transaction; identity, ownership, revision, country,
distance, and no-op checks run inside it. Neither path writes anything on
failure.

## History, XP, cache

A successful PATCH reuses the shared GTK pipeline inside one transaction:

1. new `arcade_gtk` molecule (previous molecule columns copied);
2. one new `arcade_gtk_atoms` row per kept/added atom (source columns copied,
   then explicit changes applied);
3. `arcade.gtk` repointed through `arcadeinternal.UpdateArcadeFieldsTxWithLogs`,
   which writes exactly one `arcade_changelog` row with the existing `gtk_diff`
   envelope. Items are matched by explicit source atom id: untouched atoms log
   `unchanged`, updates `updated` with `prev_id`, adds `added`, removals
   `deleted` with the previous value embedded;
4. the existing `gtk` edit XP grant (`xp.AwardArcadeEditExpTx`, +2, public
   arcades only, with the shared per-arcade/part cooldown). Private drafts
   return `diff_exp: 0`.

Stale, invalid and no-op requests create no revision, history or XP.

## Compatibility promise

- v1 is additive only. New server columns on molecule/atoms and new `meta`
  members survive old-client saves via the revision copier and member-wise
  patching; new GTK types survive untouched and are read-only in old apps.
- No new required input and no type/meaning change inside v1. A breaking change
  publishes v2 alongside v1; v1 stays until an explicitly reviewed retirement.
- Backend Full pins the Core revision whose native contract tests pass; Full
  adds no alternate implementation and no schema migration for this contract.
