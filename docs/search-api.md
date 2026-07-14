# Search API Manual

## Overview
This document describes the public combined search endpoint:

- `GET /search`

The endpoint searches across:
- `user.username`
- `user_info.nickname`
- current `arcade_basic.name`
- current `arcade_basic.nickname`
- current `arcade_basic.address`

The response is split into `users` and `arcades` buckets.

## Request
- Method: `GET`
- Path: `/search`
- Auth: not required

### Query
- `q` (required): search keyword
- `limit` (optional): positive integer, capped at `100`
- `lat` and `lon` (optional together): user location used to boost nearby arcade results within the same text match tier

Example:

```text
GET /search?q=seo&limit=5&lat=37.5665&lon=126.9780
```

## Success Response
- Status: `200 OK`

```json
{
  "users": [
    {
      "username": "searchhero",
      "nickname": "Alpha Nick",
      "avatar": ""
    }
  ],
  "arcades": [
    {
      "id": "ubbeq9qzfck5s4w",
      "country": "KR",
      "name": "Search Palace",
      "address": "101 Exact Road",
      "nickname": ["First Spot"],
      "closed": false,
      "distance_km": 0.4
    }
  ]
}
```

## Response DTO

```json
{
  "users": [
    {
      "username": "string",
      "nickname": "string",
      "avatar": "string"
    }
  ],
  "arcades": [
    {
      "id": "string",
      "country": "string",
      "name": "string",
      "address": "string",
      "nickname": ["string"],
      "closed": false,
      "distance_km": 0.0
    }
  ]
}
```

## Field Definitions

### User Search Item
- `username`: account username
- `nickname`: display nickname
- `avatar`: first avatar filename, or `""` when absent

### Arcade Search Item
- `id`: arcade id
- `country`: arcade country code
- `name`: current linked `arcade_basic.name`
- `address`: current linked `arcade_basic.address`
- `nickname`: current linked `arcade_basic.nickname`
- `closed`: arcade closed flag
- `distance_km`: distance from the provided user location in kilometers; only present when `lat` and `lon` are supplied

## Search Rules
- Search is case-insensitive.
- Search is based only on current records.
- Arcade search only includes `public=true` arcades.
- Closed public arcades are still searchable.
- Withdrawn users are excluded from results.
- Empty or whitespace-only `q` is rejected.
- Multi-word arcade queries are tokenized, but the tokens must match within the same arcade field to count as a result.
- Results are ordered by:
  1. exact match
  2. prefix match
  3. substring match
  4. stable secondary sort (`username` for users, `name` for arcades)
- When `lat` and `lon` are provided, arcade results use `distance_km` as a ranking boost so closer arcades can outrank farther arcades with the same text relevance, and `distance_km` is included on each arcade item.
- `lat` and `lon` must be provided together.

## Error Responses
- `400 Bad Request` when `q` is missing or blank:

```json
{
  "error": "missing required query param 'q'"
}
```

- `400 Bad Request` when `limit` is invalid:

```json
{
  "error": "invalid 'limit' value; expected positive integer"
}
```

- `502 Bad Gateway` when backend search fails:

```json
{
  "error": "failed to search users",
  "details": "..."
}
```

or

```json
{
  "error": "failed to search arcades",
  "details": "..."
}
```

## Frontend Integration Notes
- Treat this endpoint as a lightweight unified search, not a full-text search service.
- `avatar` is a filename only, not a full URL.
- `arcade.nickname` is always an array.
- The two buckets are independently limited by the same `limit` value.
- If `limit > 100`, the server caps it to `100`.
