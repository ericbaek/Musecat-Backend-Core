# Musecat Backend Core

Musecat Backend Core is a PocketBase-based API server for arcade, user, and contribution workflows. It is the reusable server core only: production data, data-ingestion pipelines, bulk backfills, deployment configuration, and operational notifications are intentionally maintained outside this repository.

## Status

This repository is prepared for public release. The included license is source-available, not an OSI-approved open-source license: commercial use is not permitted and redistributed modifications must be published under the same terms.

## Run locally

1. Copy `.env.example` to `.env` and fill only the provider keys you intend to use.
2. Run `go test ./...`.
3. Run `go run . serve --http=0.0.0.0:8090`.

PocketBase data is stored in `./pb_data` by default and is ignored by Git. A fresh instance starts with the versioned schema migrations in this repository. The committed `testdata/pb_data` fixture contains schema only and no production records.

## Docker

```sh
docker build -t musecat-backend-core .
docker run --rm -p 8090:8090 -v "$(pwd)/pb_data:/pb_data" musecat-backend-core
```

The health endpoint is available at `/hello`. The API contract is maintained in `docs/openapi.yaml` and served at `/openapi.yaml`.

## Contribution model

Submit reusable API, schema, documentation, and test changes here. Production-only integrations belong in a separate private operations repository. Keep secrets, user data, venue-source exports, caches, and notification destinations out of issues, pull requests, commits, and test fixtures.

See `CONTRIBUTING.md` for the pull-request requirements and `SECURITY.md` for private vulnerability reporting.
