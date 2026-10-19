# Developer Center: immediate self-service registration

The Developer Center registers confidential server applications, external record providers,
and reciprocal connections without moderator approval. Applications remain subject to user
consent, structural validation, credential requirements, automated conformance reports and
operator suspension. Each Musecat deployment has its own registrations and credentials.
See [OpenAPI](openapi.yaml), [the partner protocol](partner-integration-spec.md), and
[Sign in with Musecat](sign-in-with-musecat.md).

## Register and activate

An active Musecat user may own up to eight applications. Submit the
`musecat-partner-onboarding/v1` form to `POST /developer/apps` with a unique immutable
`partner_id`, localized display name, contact email and at least one direction. All form
URLs must satisfy the HTTPS/host/path rules in the protocol. Credentials never belong in
this document. Unknown fields and multiple JSON documents are rejected.

A valid create/update immediately installs the active form. The existing wire/storage
names `status: approved`, `approved_form`, `approved_hash`, `approved_by` and `approved_at`
are retained for compatibility; they now record validated registration, normally by the
owner, rather than an operator endorsement. Registration does not certify a provider.

| Capability | Required before use |
| --- | --- |
| Direction A: partner uses Musecat APIs | Exact HTTPS redirects and an owner-generated Musecat client secret |
| Sign in with Musecat | Direction A plus `direction_a.login_enabled: true`; explicit `openid profile` consent |
| Direction B: Musecat reads partner records | Declared OAuth/records endpoints and services; partner-issued Musecat client credentials |
| B-only provider | Direction B alone; omit Direction A and Joint; no Musecat OAuth client or client secret |
| Reciprocal Joint connection | Both directions, Joint declaration, partner-issued outbound key, Musecat-issued inbound server key |

Use `POST /developer/apps/{id}/secret` for the Direction-A secret. The plaintext is shown
once and only its hash is stored. Store partner-issued Direction-B credentials through
`PUT /developer/apps/{id}/credentials`; Musecat seals them using the deployment connection
key and never returns the plaintext. `DELETE` on that path deactivates Direction B.

`live` means at least one direction is executable. Check `direction_a_live`,
`direction_b_live` and `joint_live` separately. A newly registered app without credentials
is configured but cannot yet authenticate. Missing deployment keys are reported explicitly.

## Updates, suspension and compatibility

`PUT /developer/apps/{id}` validates and activates a replacement atomically, including
merged registry collision checks against executable-configured and other live providers.
It never exposes a partially changed configuration. Once registered, `partner_id` cannot
change. Suspended applications cannot be edited or generate new credentials.

Changes to the live OAuth client fingerprint invalidate existing grants through persistent
registration epochs; a partner must obtain user consent again. Changing provider identity,
endpoints or scopes may invalidate existing connections. Secret rotation replaces the
OAuth credential immediately. Changing display metadata can also change the OAuth client
fingerprint; consult the returned live configuration rather than assuming old grants survive.

Operators retain the application list, details, conformance reports and audit history under
`/moderation/developer/apps`. They may suspend and reinstate registrations. Suspension
removes live clients/providers; owner withdrawal disables owned registrations atomically.
Reinstatement cannot bypass an inactive owner. Each mutation appends an immutable event.

Legacy `POST /developer/apps/{id}/submit` activates valid old drafts/rejected forms and is
idempotent for registered apps. Legacy operator `/approve` activates queued forms and is
idempotent for registered apps. Neither action is required for new applications. `/reject`
is retained only for old submitted forms, not as a gate for current registration.

Registries reload immediately on the handling instance; other instances converge within
one minute. Static executable IDs remain reserved. Registration needs no deployment.

## Record-cache invalidation server key

Generate a server key with `POST /developer/apps/{id}/records-key`. This supports B-only
apps and returns `{partner_id, key}` once with `Cache-Control: no-store` and
`Referrer-Policy: no-referrer`. Send it as a Bearer credential to
`POST /partner/v1/records/invalidate` with `{sub, service}` after a user's transmitted
selection or record privacy changes. Unknown subjects also return 204; no user identity
or match count is returned. See the record contract for cache refresh behavior.

This is a Musecat-issued incoming credential, distinct from the partner-issued OAuth
credentials Musecat uses to fetch records. Store it only on your server. When Joint is
also enabled, `/records-key` and `/partner-key` rotate the same incoming server principal;
update every use of that key. The previous key remains valid for the configured Joint
rotation window, or 24 hours by default. A B-only key does not enable Joint routes.
Authentication still requires a live Direction-B provider and active owner.

## Conformance and troubleshooting

Use `POST /developer/apps/{id}/checks` to queue server-measured checks; poll
`GET /developer/apps/{id}/checks` for bounded results. Checks are diagnostics, not an
activation gate or proof of user consent. Missing fixtures remain `action_required`;
untested scenarios are not reported as successful. Credentials/test fixtures are sealed,
erased after processing and never included in reports.

Use separate disposable staging tokens for refresh/revocation probes. The checker guards
remote requests against private network destinations and never follows redirects. A live
Joint test must originate from normal user consent; the checker does not create grants.

| Result | Recovery |
| --- | --- |
| `invalid_form` 400 | Correct the returned field errors; use the current onboarding spec |
| `partner_id_conflict` 409 | Choose a different ID or remove a conflicting provider issuer/callback |
| `invalid_state` 409 | Check direction declaration, suspension, immutable ID and eight-app limit |
| `connections_not_configured` 503 | Deployment operator must configure the encryption key |
| `oauth_not_configured` 503 | Deployment operator must configure the issuer/consent manager |
| `checks_busy` 429 | Wait for the existing run/cooldown, then retry |

## Console and access boundaries

`GET /developer/apps/{id}/console?page=1` is owner-only. It returns deployed issuer,
active callbacks/scopes, 25 app-specific accounts per page and 14 days of UTC counters.
Names require profile consent; emails and secret material are excluded. Attributed API,
UserInfo, code and refresh attempts/errors are counted; unknown authentication cannot
be attributed. Counters retain 30 days and begin at deployment of the usage feature.

All management routes require Musecat user authentication, use custom APIs, and return
`no-store`. They are excluded from the delegated `account:access` namespace; a connected
partner cannot manage applications on the user's behalf. Raw collection REST rules stay
locked. Collaborators, ownership transfer and contact-email delivery are not implemented
parts of this contract.
