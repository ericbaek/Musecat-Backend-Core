# Musecat Backend Core agent rules

## Migration boundary

Backend Core is a fresh-start schema/API source, not the production database
migration target. Core tests and local verification MUST assume a new Core
bootstrap database. Do not point Core's migration runner at a Backend Full,
production, or restored backup database, and do not add production data
migrations to this repository.

Backend Full has an independent migration history and is the only repository
that owns production data migrations. A change that needs to transform an
existing Full database belongs in Backend Full's guarded forward migration;
Core may define only the reusable schema/API contract and fresh-bootstrap
coverage. Never import Core's bootstrap migration package into Full.

Before changing authentication, schema, public API, or migrations, read:

1. [`docs/architecture-contract.md`](docs/architecture-contract.md)
2. [`docs/openapi.yaml`](docs/openapi.yaml)
3. [`docs/arcade-changelog.md`](docs/arcade-changelog.md) for any arcade mutation

Required rules:

- The custom API is the only frontend contract. Do not add PocketBase raw collection REST calls, rules, or filters as a shortcut.
- Treat `arcade` as the aggregate root and `arcade_changelog` as immutable history.
- Preserve the public/closed/private visibility matrix. Private data must use the draft route and must not leak from public handlers or aggregates.
- Use guarded forward migrations. Core bootstrap migrations are never imported into Backend Full.
- Keep external network calls outside transactions. Keep aggregate mutation, changelog, XP, and required cache invalidation atomic.
- Any API or policy change updates handler tests, OpenAPI, this architecture contract when relevant, and changelog documentation when mutation history changes.

Run the focused tests while editing, then `go test ./...` before handoff. Do not modify unrelated files in a dirty worktree.
