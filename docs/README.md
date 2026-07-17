# API Documentation Workflow

## Source Of Truth
- `docs/openapi.yaml` is the API v2 wire-format source of truth.
- [`architecture-contract.md`](architecture-contract.md) is the normative source for authorization, aggregate ownership, state transitions, and Core/Full migration boundaries.
- `docs/*.md` files are supporting notes and migration material.
- `/docs/` renders the OpenAPI spec through Stoplight Elements.

## Current Coverage
`docs/openapi.yaml` now covers every implemented route registered in `main.go`.

Excluded:
- `POST /arcade/nearby` because the route is registered with `nil` and is not implemented yet.

## Update Rules
When adding or changing an endpoint:
1. Update the handler and tests.
2. Update `docs/openapi.yaml` in the same change.
3. Update `architecture-contract.md` when policy, visibility, transaction, ownership, or migration boundaries change.
4. Load `/docs/` and confirm the endpoint renders correctly.
5. Keep request and response examples aligned with actual behavior.

## Local Usage
- OpenAPI spec: `/openapi.yaml`
- Stoplight docs page: `/docs/`
- Backend architecture notes: `/backend-architecture.md`
- API v2 architecture contract: `/architecture-contract.md`
- Arcade changelog rules: `/arcade-changelog.md`
- Supporter API notes: `/supporter-api.md`
