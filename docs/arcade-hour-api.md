# Arcade Hour API Manual

## Overview
This endpoint creates a new `arcade_hour` record and updates `arcade.hour`.

- Endpoint: `PUT /arcade/hour`
- Auth: `Authorization: Bearer <token>`
- User must be active.

## Day Value Rules
Each weekday field accepts one of the following shapes:

- `null`: unknown / unset
- `499`: closed
- `{"start":<HHMM>,"end":<HHMM>}`: open hours

### 24-Hour Operation
24-hour operation may be sent as either:

```json
{"start":0,"end":0}
```

or

```json
{"start":0,"end":2400}
```

Stored form remains:

```json
{"start":0,"end":0}
```

### Overnight Operation
Closing after midnight is allowed.

Example:

```json
{"start":1000,"end":200}
```

## Request Example

```json
{
  "arcade": "arcade_id",
  "Thursday": {"start": 0, "end": 0},
  "note": "24 hours on Thursday"
}
```

## Success Response
성공 응답의 `hour`는 `GET /arcade?expand=hour`에서 내려주는 것과 같은 형식이다.

```json
{
  "arcade": "arcade_id",
  "hour": {
    "id": "hour_rel",
    "Monday": {"start": 1000, "end": 2300},
    "Tuesday": null,
    "Wednesday": null,
    "Thursday": {"start": 0, "end": 0},
    "Friday": null,
    "Saturday": null,
    "Sunday": 499,
    "Note": "Weekdays only"
  }
}
```

## Validation Guide
- Use `499` for closed.
- Use `null` or omit the field for unknown / unset.
- `start` and `end` must both be present when using an object.
- Times must be in `HHMM`.
- Minutes must be `< 60`.
- `end = 2400` is allowed only for 24-hour operation.
- Equal `start` and `end` are rejected except `00:00-00:00` which is treated as 24-hour operation.

## Changelog Semantics
`arcade_changelog.log` for `changed="hour"` uses:

```json
{
  "type": "hour_diff",
  "version": 1,
  "items": [
    {
      "change_type": "added|updated|unchanged",
      "bullets": [],
      "diff": []
    }
  ]
}
```

Important comparison rules:

- `null -> {"start":0,"end":0}` must log as `updated`
- `null -> 499` must log as `updated`
- retries with the same stored values must log as `unchanged`
- changelog treats PocketBase JSON `null` as real `null`, not as `{"start":0,"end":0}`

## Error Response Examples

Invalid closed marker:

```json
{
  "error": "validation failed",
  "details": "day hours must be 499 for closed, null for unknown, or {\"start\":...,\"end\":...} for open hours"
}
```

Invalid equal start/end:

```json
{
  "error": "validation failed",
  "details": "Monday start and end must differ; use 499 for closed or 00:00-00:00 / 00:00-24:00 for 24-hour operation"
}
```

Missing start/end:

```json
{
  "error": "validation failed",
  "details": "Thursday must be 499 for closed, or an object with both start and end"
}
```

## Notion Copy
This file is intended to be copied into the backend Notion spec page for the arcade hour endpoint and changelog rules.
