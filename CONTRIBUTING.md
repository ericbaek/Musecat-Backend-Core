# Contributing

Issues and pull requests for reusable backend features are welcome.

Before opening a pull request:

- keep the change independent of Musecat production data and infrastructure;
- update `docs/openapi.yaml` for externally visible API changes;
- add or update tests; and
- run `go test ./...`.

Do not include credentials, production IDs, personal data, scraped source datasets, caches, deployment configuration, or notification endpoints. Changes to authentication, authorization, migrations, uploads, or external API calls require maintainer security review.
