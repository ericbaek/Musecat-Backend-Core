# Musecat Backend Core

Musecat Backend Core is a PocketBase-based API server for arcade, user, and contribution workflows. It is the reusable server core only: production data, data-ingestion pipelines, bulk backfills, deployment configuration, and operational notifications are intentionally maintained outside this repository.

## Status

This repository is prepared for public release. The included license is source-available, not an OSI-approved open-source license: commercial use is not permitted and redistributed modifications must be published under the same terms.

## About Musecat

[Musecat](https://musecat.app) is a community service for finding nearby arcades and keeping their information current. It helps players browse arcade locations and game lineups, then contribute corrections, arcade edits, and fault reports. Arcade pages can include practical information such as machine counts and condition, pricing, opening hours, facilities, location notes, and update history.

The reusable backend code is published here; Musecat's production database, ingestion workflow, operational configuration, credentials, and provider-source exports are not included.

## Project Goal

Musecat exists to make the world of arcades more connected and accessible.

Our 2026 founding goal is to build a unified platform where arcade information can be discovered, understood, and shared beyond the boundaries of language, location, region, and country. From game availability and machine conditions to pricing, venue facilities, and community updates, Musecat brings together the details that help every visit feel more informed.

We aim to support rhythm-game players throughout their journey: discovering new places to play, planning a trip with confidence, sharing first-hand knowledge, and helping the community keep arcade information alive and current.

## Musecat Data

The service data is not licensed under this repository's source-code license. In particular, do not use the public service or API to bulk extract, reconstruct, redistribute, or commercially reuse Musecat's database without permission.

See [DATA_LICENSE.md](DATA_LICENSE.md) for the permitted uses, contribution licence, automated-access restrictions, and commercial or research permission process.

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

Submit reusable API, schema, documentation, and test changes here. Production-only integrations belong in a separate private operations repository. The core can format lifecycle events, but it ships with no Telegram/Discord transport or notification credentials. Keep secrets, user data, venue-source exports, caches, and notification destinations out of issues, pull requests, commits, and test fixtures.

See `CONTRIBUTING.md` for the pull-request requirements and `SECURITY.md` for private vulnerability reporting.
