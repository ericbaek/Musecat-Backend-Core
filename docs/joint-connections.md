# One-step reciprocal account connection

Status: server-only preparation on `feat/external-account-connections`. The wire protocol is now
fixed by Musecat in [`partner-integration-spec.md`](partner-integration-spec.md) §4–6 (intent /
receipt / revocation endpoints, schemas, authentication, error codes) and partners register through
the [Developer Center](developer-center.md). This document keeps the rationale and the internal
coordinator contract; where the two differ, the specification wins. Development URL and credentials
are issued per partner on approval. See also the [partner agent brief](partners/chuniinfo/agent.md).

## User experience and consent authority

The user starts “Connect Musecat” in CHUNITHM Info (or the reverse entry point), authenticates
both accounts as needed, sees both account identities and one joint permission summary, then
approves once. Reuse the Musecat login-style consent page and bullet list:

- CHUNITHM Info can use general Musecat features with my account; management is excluded.
- Musecat can retrieve my approved CHUNITHM Info records.
- Record publication follows the partner's privacy policy and the Musecat publication setting.

One approval is possible only if both operators explicitly agree that this final consent page
may collect their respective authorization, with a versioned manifest of purposes and scopes.
Musecat approval alone is not a partner grant. A generic OAuth provider without this agreement
still needs its own consent. Authentication to either account cannot be skipped on a first visit.
Account switching restarts identity verification and invalidates previous confirmation evidence.

Each authorization server still owns its own grant, scopes, expiry, tokens and revocation.
No combined bearer token, universal user ID, shared login session or cross-app grant copying is used.
Public record display is separate from connecting accounts: new game grants remain private.
A later publication control must not exceed the partner's current disclosure rules.

## Bilateral protocol (specified; logical view)

Concrete endpoints and payloads are in the specification §5. The logical sequence below is what
the specification encodes; Musecat's HTTP adapter for it is not yet implemented.

1. The initiating service creates a session-bound attempt with random state, PKCE and a correlation
   ID. The partner prepares an opaque, short-lived one-use intent bound to its logged-in account,
   registered counterparty, expected callback, game services, scopes and consent-manifest version.
2. Musecat receives only that intent handle through the browser. Musecat's server retrieves and
   validates the intent using authenticated server-to-server communication to fixed registered URLs.
   The verified partner issuer/subject and safe display name come from this exchange, never a
   browser UID, email, nickname or unsigned receipt. Login is not authorization to disclose records.
3. Musecat authenticates its account and binds the joint attempt to that exact session. Both account
   identities, the receiving services and each direction's permissions appear in the final summary.
   Changing account, client, issuer, requested scopes/services or manifest invalidates the attempt.
4. The single approve action records both directions of consent only under the agreed authority.
   The partner must verify the Musecat approval through the authenticated attempt/receipt exchange,
   then issue its own one-use OAuth code for Musecat. Musecat issues its own code for the partner.
   Each code is bound to its direction's registered client, callback, state and PKCE challenge.
   Neither browser-supplied booleans nor an unverified signed payload can create either grant.
5. Each client exchanges only its own code at the issuing server. Tokens stay on their recipient's
   server. Verified callbacks record the corresponding grant/connection evidence against this
   attempt. Retries of receipt acknowledgments are idempotent; code redemption is not blindly retried.
6. Completion is shown only after both identities, manifest, current grants and acknowledgments
   match. Cross-service completion is not a database transaction; timeouts leave a visible partial
   state. Do not report successful integration when only Musecat authorization completed.

Intent/receipt requirements to finalize: at most five-minute lifetime; cryptographic random handle
with only a hash persisted; one-use atomic claim; bound issuer, authenticated client, both account
contexts, correlation ID, exact registered callback and approved manifest; endpoint authentication
with a dedicated purpose/audience; expiry/replay/session/config checks on receipt and finalization.
Existing OAuth client authentication does not automatically authorize a new receipt endpoint.
Prefer opaque receipts redeemed by authenticated server exchange. If signatures are chosen, agree
algorithm, issuer, audience, key distribution/rotation and unique receipt ID verification first.
Keep intent handles, codes, tokens and exact subjects out of logs and analytics.

## Implemented now: internal coordinator, no new public authority

`connections.JointManager` and locked `external_joint_connection` persist and correlate attempts.
The executable must inject an explicit `JointPartner` mapping of partner ID → OAuth client ID →
external provider → selected game services. No endpoint URL or partner is inferred/hardcoded.
This component has no HTTP routes, no consent receipt verifier and no token issuance/automatic
approval. The existing independent OAuth flows remain unchanged.

Executables opt in through `connections.ParseJointConfig(MUSECAT_JOINT_PARTNERS, connections, delegation)`
and pass the result as `coreapp.Config.Joint`; `connections.RegisterJointCleanupCron` bounds expired
attempts. An empty variable keeps coordination disabled. Each entry must reference an already
configured `MUSECAT_EXTERNAL_PROVIDERS` provider/service and a `MUSECAT_OAUTH_CLIENTS` client that
permits `account:access`; anything else fails startup without echoing the value. Illustrative only:

```json
[{"id": "chuniinfo", "client_id": "<registered Musecat OAuth client ID>", "provider": "chuniinfo", "services": ["chunithm"]}]
```

Configuring the registry only prepares the ledger. It exposes no endpoint until the partner-gated
adapter below exists, so a configured partner is not a user-visible feature.

The trusted future adapter must call `Start` only after verifying the partner account context,
then call `RecordVerified` only after that attempt's bilateral approval/receipt and OAuth checks.
It must not expose either function as a generic browser “mark complete” API. The coordinator:

- Revalidates the current Musecat session and account guard, binds confirmation to its exact session,
  and cancels older pending/partial attempts for the same user and partner.
- Checks the partner/provider/service registry and stored configuration fingerprint.
- Requires a current `account:access` grant for the same Musecat user and registered partner client,
  its exact generation, current client epoch/account key and a redeemed, unexpired refresh family.
  A consent code that has not been exchanged does not count as a completed direction.
- Requires the same user's external connection, expected provider/issuer and verified partner
  subject, locally decryptable credentials, required granted scopes and requested private service grants.
  A locally expired partner token without a refresh token does not count as usable evidence.
- Pins the connection revision and grant generation, rejects replacement proof, and makes identical
  confirmation idempotent within the five-minute attempt window.
- Returns no credentials, subjects or proof IDs. Status rechecks the ledgers; revocation, reapproval,
  changed connection revision or configuration invalidates completion rather than trusting a label.
- Cascades on user deletion and is explicitly deleted on account withdrawal. Bounded cleanup can
  remove up to 100 expired non-connected attempts per call. Executables opt in when joint flows ship.

These checks correlate existing local grants. They do not prove that a remote token is still valid,
store a partner's joint-consent receipt, or prevent an independently approved direction from being
usable while the other is incomplete. Those need the adapter/protocol work above. `connected`
metadata never substitutes for live token and domain permission checks on actual API calls.

| Effective state | Meaning |
| --- | --- |
| `pending` | Neither direction confirmed for this attempt |
| `partial` | Only part of the required local evidence is valid; not a completed pair |
| `connected` | Both pinned directions validated locally |
| `degraded` | A previously completed pair lost current local grant/connection evidence |
| `cancelled` | Coordination cancelled; no further confirmations accepted |
| `expired` | Incomplete attempt exceeded five minutes; start a new attempt |
| `invalidated` | Trusted partner/provider configuration no longer matches |

`Cancel` cancels coordination only. It does not silently delete independently approved or pre-existing
grants. This internal method is not yet a user-facing “disconnect both” action.

## Bilateral disconnect and failure recovery (not implemented yet)

The product action should be “Disconnect both services”. Locally disable access immediately and
hide derived partner records, then deliver a durable authenticated revocation request to the partner.
A remote outage must not restore local access. Expose remote revocation pending separately from
fully disconnected, retry safely, and accept duplicate/reordered notifications idempotently.

Notifications must refer to the exact pair/generation; an old attempt's cancellation must never
revoke a later reconnection. Abort cleanup may revoke only grants created by that attempt under the
agreed protocol. Preserve pre-existing independent authorizations unless the user explicitly chose
to revoke them. Existing one-way unlink is still local-only; do not claim remote revocation today.
A durable outbox, remote status reconciliation and refresh/expiry invalidation policy need to be
implemented with the agreed partner endpoints before enabling this product action.

## Multi-platform and rhythm.info

A partner registration is not a game or domain name. A verified provider identity can hold several
game-service grants; independent issuers/clients remain separate. A rhythm.info
issuer/client/subject transition needs a verified migration or fresh connection, not email matching
or copying grants. The configuration fingerprint and per-generation binding force re-verification.
The consent host, supported manifest versions and receipt/revocation protocol are partner capabilities;
ordinary OAuth-only providers continue to work without one-step reciprocal support.

## Ship gates

1. Partner declares support for `musecat-joint/v1` in the Developer Center form and passes the
   automated checks and conformance kit (specification §8–9). No bilateral negotiation is needed.
2. Implement Musecat's specification §4–6 endpoints, session/receipt storage and issuance gates;
   connect the existing login/consent UI to the joint summary and server-only coordinator.
3. Add partial-state UI, safe retry/abort, durable revocation outbox and reconciliation.
4. Test real two-server flows: both entry points, both accounts logged out, account switch,
   denial, replay, wrong issuer/client, one-sided network failure, refresh race, reapproval and
   delayed disconnect after a new connection. Verify private records never become public on linking.
5. Pin this Core revision in Full. Full's prepared `1792030002_joint_connections` migration freezes
   the same locked schema and rejects incompatible existing tables without importing Core's
   bootstrap registry. Validate/apply only Full's migration on disposable existing DBs,
   then deploy with separately provided development URLs/client registrations. Never run Core's
   bootstrap migration runner on an existing Full/production database.
