# User Profile API Manual

## Overview
This document describes the newly added user profile APIs:
- `GET /user` (public lookup by id or username)
- `GET /user/me` (authenticated self profile)
- `GET /user/activity` (public activity heatmap lookup)
- `PUT /user/countries` (authenticated profile-country selection)
- `GET /supporter/score` and `POST /supporter/request` are documented separately in `docs/supporter-api.md`

Both endpoints return the same normalized profile shape that merges data from:
- `user` collection (account/auth state)
- `user_info` collection (editable profile fields)

## Base Profile DTO
All successful responses return this JSON object:

```json
{
  "id": "string",
  "created": "string",
  "username": "string",
  "nickname": "string",
  "level": 0,
  "countries": ["KR"],
  "primary_country": "KR",
  "bio": "string",
  "avatar": "string",
  "sns": {
    "items": [
      {
        "type": "twitter",
        "link": "https://x.com/example",
        "name": "optional display name"
      }
    ]
  },
  "withdrawn": false,
  "series_public": false,
  "series": [
    {
      "id": "string",
      "seriesNumber": 1,
      "en": "string",
      "kr": "string",
      "jp": "string",
      "en_short": "string",
      "kr_short": "string",
      "jp_short": "string",
      "manufacturer": "string"
    }
  ]
}
```

## Field Definitions
- `id`: user id
- `created`: account creation datetime from `user.created`
- `username`: account username
- `nickname`: display nickname
- `level`: current level computed from `user_level.exp`
- `countries`: ordered ISO 3166-1 alpha-2 profile countries; the first item is primary
- `primary_country`: first item in `countries`, or `""` when no country is selected
- `bio`: user bio text
- `avatar`: single avatar filename (not a full URL)
- `sns`: normalized SNS collection from `user_info.sns`
- `sns.items`: array of `{ type, link, name? }`
- `withdrawn`: withdrawal state
- `series_public`: whether the user chose to expose preferred series
- `warp`: user warp preference (included for `GET /user/me`)
- `series`: optional `game_series` array, included only when `series_public = true`

Endpoint-specific visibility:
- `GET /user`: `series` is included only when `series_public = true`
- `GET /user/me`: `series_public` value is still returned, `series` is included for the authenticated user even when `series_public = false`, and `warp` is always included

## Profile countries

Profile countries are optional. Active accounts below level 15 may store one
country, while accounts at level 15 or above may store up to three. This limit
is based only on level; supporter and staff tags do not change it. The list is
ordered, and its first code is the representative country. Codes are normalized
to ISO 3166-1 alpha-2 uppercase values.

- Public profile reads expose the selected countries for accounts at level 15 or
  above. If that access ends, public reads expose only the first stored country;
  `GET /user/me` retains the full saved list.
- Compact user results expose only `primary_country`. Clients use the existing
  Dashboard `icon source country` renderer, never an emoji or backend image URL.
- A country icon appears next to a nickname whenever `primary_country` is
  present, including below level 15 and in changelog/timeline profile displays.
  It is rendered before any supporter or staff badge.

### Endpoint: PUT /user/countries

Replaces the authenticated active user's full ordered selection.

```json
{"countries":["KR","JP"]}
```

An empty array clears the selection. Duplicate, invalid, or non-alpha-2 codes
return `400`; a non-supporter attempting multiple values returns `403`. The
successful response is `{ "countries": ["KR", "JP"], "primary_country": "KR" }`.

## Country-picker recommendations

The country picker requires no new recommendation endpoint. Build its groups
in this order, removing duplicates after every group:

1. `GET /user/me` `countries`, in stored order.
2. `GET /user/visits` `stats.countries`, already ordered by verified distinct
   arcade count descending and code ascending.
3. When location permission is available, `GET /arcades/nearby?lat=&lon=`
   `country_totals`, ordered by `nearest_arcade.distance_km`, then total
   descending, then country code.
4. The remaining localized country catalog entries.

Search filters the complete catalog without changing selection semantics. If
location is unavailable or no nearby venues exist, omit only group 3.

## Visit records

`visit_stats` is the profile's only visit-record payload. It reports total
visit verifications, distinct visited arcades, country counts by distinct
arcade, total straight-line travel distance, and the visited arcade list.
Each arcade item has its id, current name and country, first published image
URL from the current photo molecule when one exists, visit count, and last
local visit date. Image URLs always use the custom `/arcade/photo/file` route.
Items are ordered by visit count descending and then by most recent visit
descending.

- `visit_visibility=private` omits `visit_stats` from public profiles; the
  owner still receives it from `GET /user/me`.
- `visit_visibility=summary` exposes the aggregate fields and arcade items,
  but omits each item's `last_visit_day` and `visit_days`.
- `visit_visibility=full` additionally exposes each arcade's complete
  `visit_days` list and last visit day, newest first.
- Only currently public arcades are included. This prevents a venue that is
  later made private from being disclosed through a visitor profile.

The payload never exposes raw visit record ids, timestamps, GPS distance or
accuracy, or visit XP. Total travel distance follows chronological verified
visits and sums straight-line distances between the current locations of
consecutive arcades. A visit with no current arcade location breaks that
distance sequence.

## Privacy and Masking Rules
- Sensitive fields are not exposed: `email`, `emailVisibility`, password/token fields
- If a user is withdrawn:
  - `username` and `nickname` are masked as the withdrawn display name
  - `bio` and `avatar` are returned as empty strings
  - `series_public` is returned as `false`
  - `series` is omitted
  - `countries` is `[]` and `primary_country` is `""`
  - `withdrawn` is `true`

## Data Merge Rules
- `user` record must exist
- `user_info` may be missing
- If `user_info` is missing:
  - no DB write is performed
  - response falls back to:
    - `nickname = username`
    - `bio = ""`
    - `avatar = ""`
    - `sns = { "items": [] }`
    - `series_public = false`
    - `series` omitted
    - `countries = []`, `primary_country = ""`

## Endpoint: GET /user
Public profile lookup by user id or username.

### Request
- Method: `GET`
- Path: `/user`
- Query:
  - `id` (optional): target user id
  - `username` (optional): target username
  - at least one of `id` or `username` is required
- Auth: not required

### Success Response
- Status: `200 OK`
- Body: Profile DTO

Example:

```json
{
  "id": "toq3to4ncbf5ubd",
  "created": "2026-03-30 10:15:00.000Z",
  "username": "public_user_toq3to4ncbf5ubd",
  "nickname": "public_nick",
  "bio": "public bio",
  "avatar": "",
  "sns": {
    "items": []
  },
  "withdrawn": false,
  "series_public": false,
  "level": 0
}
```

### Error Responses
- `400 Bad Request` when both `id` and `username` are missing:

```json
{
  "error": "missing required query param 'id' or 'username'"
}
```

- `404 Not Found` when user is not found:

```json
{
  "error": "user not found"
}
```

- `502 Bad Gateway` for backend fetch failures:

```json
{
  "error": "failed to load user profile",
  "details": "..."
}
```

## Endpoint: GET /user/me
Authenticated self profile lookup.

### Request
- Method: `GET`
- Path: `/user/me`
- Auth: required (`Authorization: Bearer <token>`)

### Success Response
- Status: `200 OK`
- Body: Profile DTO
- `series` is always included for the authenticated user when stored in `user_info.series`, even if `series_public = false`
- `owns` is included only for the authenticated user and lists arcade IDs they may manage.

### Error Responses
- `401 Unauthorized` when token is missing/invalid (PocketBase auth middleware shape):

```json
{
  "data": {},
  "message": "The request requires valid record authorization token.",
  "status": 401
}
```

- `404 Not Found` when authenticated user record is missing:

```json
{
  "error": "user not found"
}
```

- `502 Bad Gateway` for backend fetch failures:

```json
{
  "error": "failed to load user profile",
  "details": "..."
}
```

## Endpoint: GET /user/activity
Public user activity heatmap lookup.

### Request
- Method: `GET`
- Path: `/user/activity`
- Query:
  - exactly one of:
    - `id`: target user id
    - `username`: target username
  - `tz` (optional): IANA timezone, default `UTC`
  - `days` (optional): integer `1..365`, default `365`
- Auth: not required

### Success Response
- Status: `200 OK`
- Body:

```json
{
  "user": {
    "id": "toq3to4ncbf5ubd",
    "created": "2026-03-30 10:15:00.000Z",
    "username": "public_user_toq3to4ncbf5ubd",
    "nickname": "public_nick",
    "bio": "public bio",
    "avatar": "",
    "sns": {
      "items": []
    },
    "level": 0,
    "withdrawn": false,
    "series_public": false
  },
  "range": {
    "start_date": "2025-03-30",
    "end_date": "2026-03-29",
    "tz": "Asia/Seoul",
    "days": 365
  },
  "totals": {
    "total_count": 18,
    "changelog_count": 9,
    "flag_count": 5,
    "legacy_ticket_count": 4,
    "attendance_count": 0,
    "max_daily_count": 3
  },
  "days": [
    {
      "date": "2026-03-29",
      "total_count": 2,
      "level": 3,
      "changelog_count": 1,
      "flag_count": 1,
      "legacy_ticket_count": 0,
      "attendance_count": 0
    }
  ]
}
```

### Count Mapping
- `changelog_count`: rows from `arcade_changelog` where `by = user.id`
- `flag_count`: rows from `arcade_flag` plus `arcade_flag_reaction` where `createdBy = user.id`
- `legacy_ticket_count`: rows from `z_legacy_tickets` where `createdBy = user.id`
- `attendance_count`: rows from `user_level_log` with attendance check-in kinds
- `total_count`: sum of the four category counts
- `level`: relative heatmap level `0..4` derived from `total_count` within the requested range

### Error Responses
- `400 Bad Request` when `id` and `username` are both missing or both supplied:

```json
{
  "error": "exactly one of query param 'id' or 'username' is required"
}
```

- `400 Bad Request` for invalid timezone:

```json
{
  "error": "invalid 'tz' value; expected IANA timezone",
  "details": "..."
}
```

- `400 Bad Request` for invalid `days`:

```json
{
  "error": "invalid 'days' value; expected integer between 1 and 365"
}
```

- `404 Not Found` when user is not found:

```json
{
  "error": "user not found"
}
```

- `502 Bad Gateway` for activity aggregation failures:

```json
{
  "error": "failed to load user activity",
  "details": "..."
}
```

## Frontend Integration Notes
- Treat `avatar` as a filename from PocketBase file field, not an absolute URL.
- If you need an image URL, build it with your PocketBase file URL rule on the client.
- For UI consistency:
  - always read `nickname` from API response
  - render `primary_country` with the existing Dashboard country icon source;
    do not use a flag emoji or backend image URL
  - render the compact country icon wherever `primary_country` is present, before
    any supporter or staff badge
  - do not derive profile visibility from hidden backend fields
- `GET /user` and `GET /user/me` are schema-compatible; one shared frontend model can be used.
- `GET /user/activity` returns zero-filled daily buckets ordered from oldest to newest, so the client can render a GitHub-style grid directly.


## Passport

`GET /user/passport?year=all|YYYY` and `GET /user/passport/stamps` accept an optional
`user` ID. Omit it for the authenticated active owner; an explicit ID also permits
anonymous reading according to that user's visibility. Both APIs are no-store and
use `LoadPassport`, as does legacy public `visit_stats`. Only current public arcades,
including closed venues, are included. Private venues disappear from every aggregate.

`private` returns 404 to other viewers. `summary` exposes totals, country/city maps,
period statistics, filters and venue stamps, but omits first/latest visit dates and
all per-venue date arrays from both APIs (including top venues) and legacy profile
stats. `full` additionally exposes first/latest dates and every selected-period local
visit date in `visit_dates`, newest first. Select `year=all` for all dates. The owner
has the same complete fields regardless of public visibility. Raw GPS, accuracy,
precise timestamps and visit-record IDs remain private in every scope. Existing
summary/full settings take the new scope immediately; there is no versioned opt-in.
`visibility` in the response tells the web which projection was applied.
`city_catalog_available=false` distinguishes missing reference data from zero cities.

Arcade country/timezone detection uses the vendored offline bundle (or an
explicit `MUSECAT_GEO_DATA_DIR` override); see
[`docs/offline-geo.md`](offline-geo.md). City detection then selects the nearest
imported GeoNames city in the resolved country using a deterministic tie-break.
This same rule is used by the one-time Full backfill, so ordinary creation and
edits do not depend on a reverse-geocoding server. Full seeds an embedded
`cities500` catalog when the Passport city collection is empty.

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
stamp or XP. The first visit awards 5 XP and a revisit awards 2 XP. Passport is GPS visit evidence, not play evidence.
