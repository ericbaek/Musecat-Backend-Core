# Developer Center: development registration and production review

The Developer Center registers confidential server applications, external record providers,
and reciprocal connections immediately in development mode. Production use by all users requires operator approval. Applications remain subject to user
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

A valid create/update in development mode immediately installs the active form. Only its owner and up to ten designated test accounts can consent or use its tokens/connections. The existing wire/storage
names `status: approved`, `approved_form`, `approved_hash`, `approved_by` and `approved_at`
are retained for compatibility; they now record validated registration, normally by the
owner. `publication` separately distinguishes `development` from operator-approved `production`; `review_status` reports `none`, `submitted` or `rejected`. Registration does not certify a provider.

| Capability | Required before use |
| --- | --- |
| Direction A: partner uses Musecat APIs | Exact HTTPS redirects (development also allows exact HTTP localhost/127.0.0.1/[::1]) and an owner-generated Musecat client secret |
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

`PUT /developer/apps/{id}` validates a replacement atomically. Development changes activate
immediately. Production changes remain in `submitted_form` while the reviewed
`approved_form` continues serving users. Editing clears any pending review; operators cannot
approve an obsolete requested snapshot. `partner_id` cannot change after registration.

`PUT /developer/apps/{id}/testers` accepts `{usernames: [exactUsername, ...]}` for up to ten
active Musecat accounts. The owner is implicitly included and must not be repeated. Unknown,
duplicate, withdrawn or restricted accounts are rejected. IDs are stored internally and
returned with `tester_usernames` for editing. Audience changes invalidate development OAuth
epochs and provider fingerprints; existing tokens and pending flows cannot bypass removal.
Production access is unaffected by its optional tester list.

`POST /developer/apps/{id}/submit` requests production review. All Direction-A callbacks
must be public HTTPS URLs: HTTP loopback exceptions are rejected. Direction-B issuer,
authorization/token/userinfo/records and Joint server endpoints remain public HTTPS even
in development on hosted Musecat; use a tunnel or hosted partner server. Loopback is a
callback exception, never permission for the hosted backend to fetch private addresses.

Operators approve a pending request through `/moderation/developer/apps/{id}/approve`.
Approval atomically installs the requested form, sets `publication: production` and clears
review status. Rejection records a note and preserves the previously active audience/form.
Conformance reports are visible independently and never fabricate user consent. Existing
pre-policy active registrations are migrated as production to preserve live grants.

Changing the live OAuth fingerprint invalidates grants through persistent epochs. Changing
provider identity, endpoints, scopes or development audience can invalidate connections.
Secret rotation replaces the OAuth credential immediately. Suspended applications cannot
be edited or generate credentials; user consent is always required.

Operators retain the application list, details, conformance reports and audit history under
`/moderation/developer/apps`. They may suspend and reinstate registrations. Suspension
removes live clients/providers; owner withdrawal disables owned registrations atomically.
Reinstatement cannot bypass an inactive owner. Each mutation appends an immutable event.

The legacy `status` field remains for active/suspended compatibility. Production review
uses `review_status`, not `status`. `GET /moderation/developer/apps?status=submitted`
selects pending production requests. Suspend/reinstate does not silently grant production.

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
