# Supporter API Manual

## Overview
This document covers the supporter verification flow:
- `GET /supporter/score` to inspect the current XP ledger and qualification state
- `POST /supporter/request` to create a verification request when the threshold is met

The score endpoint is ledger-based. It reads `user_level_log` and exposes the XP timeline directly instead of aggregating by arcade.

## XP Rules
- `public` arcade creation: `10` XP
- Arcade edit bonus: `3` XP per granted edit log
- `photo_submission`: `5` XP
- `flag`: `5` XP
- `flag_reaction`: `3` XP
- attendance check-in: `2` XP per successful KST-day first check-in

Edit grants can repeat after the same user has left that arcade unchanged for more than 7 days.

## Ledger Entry Shape
`entries[]` is the machine-readable timeline used by the frontend, ordered from latest to oldest.

| Field | Meaning |
| --- | --- |
| `kind` | Exact `user_level_log.kind` value |
| `source` | High-level source bucket such as `arcade`, `flag`, `flag_reaction`, or `attendance` |
| `action` | Specific action name such as `public`, `basic`, `game`, `photo_submission`, or `check_in` |
| `exp` | XP delta for that ledger entry |
| `previous_exp` | XP before the grant |
| `new_exp` | XP after the grant |
| `created` | Ledger timestamp |
| `arcade_id` | Arcade linked to the action, when applicable |
| `arcade_name` | Arcade display name, when resolvable |
| `target_id` | Primary target id, such as a flag id or reaction id |
| `detail` | Extra parsed context, such as attendance day or edit grant key |

`xp_feedback.previous_percent_to_next_level` and `xp_feedback.new_percent_to_next_level` show the level-progress percentage before and after the XP change.

## Score Response Shape
`GET /supporter/score` returns:

```json
{
  "total_exp": 341,
  "attendance_exp": 2,
  "qualified": true,
  "threshold": 300,
  "can_request": true,
  "entries": [
    {
      "id": "log_124",
      "kind": "xp:attendance:service:2026-04-30",
      "source": "attendance",
      "action": "check_in",
      "exp": 2,
      "previous_exp": 10,
      "new_exp": 12,
      "created": "2026-04-30 10:05:00.000Z",
      "detail": {
        "day": "2026-04-30"
      }
    },
    {
      "id": "log_123",
      "kind": "xp:arcade-public:arcade_123",
      "source": "arcade",
      "action": "public",
      "exp": 10,
      "previous_exp": 0,
      "new_exp": 10,
      "created": "2026-04-30 10:00:00.000Z",
      "arcade_id": "arcade_123",
      "arcade_name": "Sample Arcade",
      "target_id": "arcade_123"
    }
  ],
  "latest_request": {
    "id": "req_123",
    "status": "rejected",
    "exp_total": 341,
    "qualified": true,
    "created": "2026-04-30 10:30:00.000Z"
  }
}
```

## Endpoint: GET /supporter/score
Authenticated current-user lookup for supporter verification progress.

### Request
- Method: `GET`
- Path: `/supporter/score`
- Auth: required (`Authorization: Bearer <token>`)

### Success Response
- Status: `200 OK`
- Body: XP response with a ledger timeline

### Error Responses
- `401 Unauthorized` when the token is missing or invalid
- `502 Bad Gateway` if ledger loading fails

## Endpoint: POST /supporter/request
Creates a supporter verification request.

### Request
- Method: `POST`
- Path: `/supporter/request`
- Auth: required (`Authorization: Bearer <token>`)

### Behavior
- The server recomputes the current supporter XP at request time.
- If the user has at least `300` XP, a request record is created with `pending` status.
- Telegram and Discord notifications are emitted immediately after creation.
- If the XP is below `300`, the request is rejected.

### Success Response
- Status: `200 OK`
- Body:

```json
{
  "id": "req_123",
  "status": "pending",
  "qualified": true,
  "exp": {
    "total_exp": 341,
    "attendance_exp": 2,
    "qualified": true,
    "threshold": 300,
    "can_request": true,
    "entries": []
  }
}
```

### Error Responses
- `400 Bad Request` when the XP threshold is not met
- `401 Unauthorized` when the token is missing or invalid
- `409 Conflict` when an active pending or approved request already exists
- `502 Bad Gateway` for request creation failures

## Frontend Notes
- Render `entries[]` as a timeline from latest to oldest rather than a per-arcade scoreboard.
- Use `source`, `action`, and `kind` as the primary translation keys.
- `exp` remains the immediate XP delta for the action that just occurred.
- The latest request block is optional and may be omitted when the user has never applied.
