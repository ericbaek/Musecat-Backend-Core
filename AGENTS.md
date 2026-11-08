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

## Validation scope

- Before handoff or push, run the tests for the changed owner packages and their
  importing consumers, including the relevant handler, permission and wire-contract
  regressions. Record the selected scope and results. Unknown ownership or unclear
  impact requires broader checks rather than silently omitting consumers.
- Documentation-only changes use the applicable reference and OpenAPI checks.
  Workflow-only changes use actionlint and an initial successful remote run; do not
  repeat unrelated application suites locally for these changes.
- Shared authentication, XP/transaction policy, schema or migration infrastructure,
  dependencies, module/toolchain and build changes require `GOWORK=off go vet ./...`
  and `GOWORK=off go test ./...`. Published-module/release validation also requires
  those complete checks. Core migration verification always uses new Core databases,
  never Full, production or backup databases.
- CI retains the complete Go vet/test gate for every PR and for pushes to `main`
  and `dev`; feature branches use PR runs, and superseded runs are cancelled.
  Scoped local success and remote CI status are separate: resolve known failures
  and never report pending checks as passed.
- Do not modify unrelated files in a dirty worktree.
