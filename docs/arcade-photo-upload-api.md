# Arcade Photo Upload API Manual

## Overview
This endpoint uploads arcade photo files and creates `arcade_photo_atoms` records.
It does not update `arcade.photo` directly.

- Upload endpoint: `POST /arcade/photo/upload`
- Confirm endpoint: `PUT /arcade/photo` (existing endpoint; pass uploaded atom ids)

## Auth
- Required: `Authorization: Bearer <token>`
- User must be active (not withdrawn).

## Request
- Method: `POST`
- Path: `/arcade/photo/upload`
- Content-Type: `multipart/form-data`
- Form fields:
  - `arcade` (string, required): target arcade id
  - `photos` (file, required, repeated): 1 to 10 files

### File Constraints
- Allowed MIME types:
  - `image/png`
  - `image/vnd.mozilla.apng`
  - `image/jpeg`
- Max file size: 20MB per file (collection schema rule)

## Response Shape

```json
{
  "arcade": "arcade_id",
  "summary": {
    "total": 2,
    "success": 1,
    "failed": 1
  },
  "uploaded": [
    {
      "index": 0,
      "atomId": "photo_atom_id",
      "filename": "ok.png"
    }
  ],
  "failed": [
    {
      "index": 1,
      "filename": "bad.gif",
      "reason": "unsupported mime type: image/gif"
    }
  ]
}
```

## Status Codes
- `200 OK`: all files uploaded successfully
- `207 Multi-Status`: partial success (some uploaded, some failed)
- `422 Unprocessable Entity`: all files failed
- `400 Bad Request`: invalid multipart body / missing required fields / too many files
- `404 Not Found`: arcade not found
- `401 Unauthorized`: missing or invalid token

## Frontend Usage (FormData)

```ts
const form = new FormData();
form.append("arcade", arcadeId);
for (const file of files) {
  form.append("photos", file);
}
```

## Progress UI Guide (Total Progress)
Use total upload progress from the request transport (`XMLHttpRequest` or Axios `onUploadProgress`).
Show one progress bar for the entire request.

- Progress formula: `percent = Math.round((loaded / total) * 100)`
- This endpoint does not provide per-file server progress events.

## Partial Success Retry Guide
1. Read `failed[]` from response.
2. Map `failed[].index` back to original file list.
3. Retry only failed files in a new request.
4. After all required files are uploaded, call `PUT /arcade/photo` with final atom id list.

## Finalize Uploaded Photos
After upload completes, call the existing `PUT /arcade/photo`:

```json
{
  "arcade": "arcade_id",
  "photos": ["atom_id_1", "atom_id_2"]
}
```

This creates/updates the molecule and sets `arcade.photo`.
