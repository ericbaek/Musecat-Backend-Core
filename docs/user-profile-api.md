# User Profile API Manual

## Overview
This document describes the newly added user profile APIs:
- `GET /user` (public lookup by id or username)
- `GET /user/me` (authenticated self profile)
- `GET /user/activity` (public activity heatmap lookup)
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

## Privacy and Masking Rules
- Sensitive fields are not exposed: `email`, `emailVisibility`, password/token fields
- If a user is withdrawn:
  - `username` and `nickname` are masked as the withdrawn display name
  - `bio` and `avatar` are returned as empty strings
  - `series_public` is returned as `false`
  - `series` is omitted
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
  - do not derive profile visibility from hidden backend fields
- `GET /user` and `GET /user/me` are schema-compatible; one shared frontend model can be used.
- `GET /user/activity` returns zero-filled daily buckets ordered from oldest to newest, so the client can render a GitHub-style grid directly.
