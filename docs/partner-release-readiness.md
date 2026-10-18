# Partner integration release readiness

Reviewed against `partner-integration-spec.md` v1 and the feature-branch Core/Full/WebCore
implementations on 2026-10-18. This records engineering preparation, not a production deployment
or a claim that a real partner has passed interoperability checks.

## Musecat implementation

| Boundary | Implemented and verified |
| --- | --- |
| Direction A | PKCE S256, session-bound consent, narrow developer API audience, rotating refresh families, revoke and discovery; tests in `delegation` and its handlers |
| Direction B | Issuer/state/session binding, verified userinfo subject, per-service consent, encrypted tokens, scope narrowing; tests in `connections` |
| Records and privacy | Strict typed envelope, verified subject, private defaults, both-side publication gate, stale/disabled public-cache suppression, persisted 429 backoff, post-I/O account checks |
| Reciprocal connection | Lookup, one consent, independent PKCE code exchanges, matching generation evidence, partial failure rollback, reconnect; `jointflow_test.go` |
| Revocation | Exact-generation local revoke, persistent outbox, stable retry ID/body, duplicate/old-generation protection, withdrawal delivery preservation |
| Developer Center | Structural registration, independent operator approval, one-time secrets, inbound key overlap, immediate local activation and cross-instance polling |
| Conformance | Durable bounded worker, server probes, encrypted one-use fixtures, owner/reviewer access, results/history/staleness UI, no fabricated passes |
| Lifecycle | Owner withdrawal atomically suspends apps/disables registrations/wipes credentials and queued fixtures; restricted owners excluded from registries |
| Delivery | Full-owned guarded migrations through `1792030009`; Core fresh bootstrap only; Full pinned to committed Core and tested with `GOWORK=off` |

Independent one-sided unlink intentionally removes local credentials without claiming remote RFC7009
revocation; users choose the joint disconnect action to revoke both directions. An issuer migration
requires explicit reconnection; no email/name-based identity transfer is supported.

## Deployment configuration

Set these in the deployment secret/config store (both repository `.env.example` files list them):

- `MUSECAT_OAUTH_ISSUER`: public HTTPS API issuer, matching discovery and token validation.
- `MUSECAT_OAUTH_CONSENT_URL`: public web consent URL, e.g. `https://WEB_HOST/en/oauth/authorize`.
- `MUSECAT_WEB_ORIGIN`: public HTTPS web origin; callbacks derive from it.
- `MUSECAT_CONNECTION_ENCRYPTION_KEY`: base64 of exactly 32 random bytes. Persist and back up
  separately from the DB; changing it requires re-entering partner credentials/reconnecting users.
- `MUSECAT_OAUTH_CLIENTS`, `MUSECAT_EXTERNAL_PROVIDERS`, `MUSECAT_JOINT_PARTNERS`: empty for
  third-party DB onboarding; optional static bootstrap/first-party registrations must use distinct IDs.

Allow outbound DNS/TLS to public partner endpoints. Private/loopback/metadata destinations,
environment proxies and HTTP redirects are rejected by the default partner client. Run Core/Full
with their normal executable jobs enabled: registry sync, conformance worker, record refresh,
OAuth/request cleanup and joint revocation delivery. Core's library configure function starts no jobs.

Before production migration, follow Full `AGENTS.md`: verified backup, stop writers, read-only
preflight, exact pinned Full binary against a disposable copy, post-migration invariants, then
explicit production rollout. This review does not authorize a production rollout or secret change.
After rollout, verify `/health/ready`, OAuth discovery, the public developer spec, the registered
web callback, owner/reviewer access and a queued check completing through the normal worker.

## Evidence requiring the partner

1. Partner registers fixed public HTTPS endpoints and exact Musecat callback, implements v1,
   and supplies Direction-B credentials plus its receiving-side server key.
2. Operator reviews the staging application. Owner reveals Musecat-issued credentials once and
   installs them in the partner server. Initial approval is separate from check completion because
   real OAuth/joint evidence requires an active registration.
3. A dedicated test user completes normal consent on both sites. Submit disposable public/private
   tokens, a separate refresh/revoke family, intent fixtures and the resulting joint attempt ID in
   Developer Center. Missing or interactive evidence stays unverified; it is not a successful pass.
4. Exercise partner-login PKCE rejection, wrong subject, partial failure/restart, duplicate and old
   revocation against staging. Server unit tests cover Musecat behavior; a live partner must also
   demonstrate its implementation. Never use real user tokens as destructive fixtures.
5. Repeat with separate production registration/credentials after explicit deployment approval.

No partner endpoints, credentials or production deployment access have been assumed. Domain/issuer
migration, collaborator management, daily usage analytics, email notifications and operator-assisted
ownership transfer are optional separate product work; they are not prerequisites for implementing
or onboarding a v1 partner. The current center is owner-managed and exposes audit history and
conformance reports; it does not claim these optional controls exist.
