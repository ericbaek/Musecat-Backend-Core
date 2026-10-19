# One-step reciprocal account connection

The normative protocol is [Partner Integration Specification v1](partner-integration-spec.md),
sections 4–6. Partners register through the Developer Center, declare the joint manifest and fixed
HTTPS endpoints, then enter their Direction B credentials and server key. After validated registration and credential setup,
the developer issues the Musecat server key once; the joint registration activates automatically.

The web consent entry is `/{locale}/connect/{partner}?intent=<handle>`. Settings offer both joint
connection and bilateral disconnect alongside independent one-sided OAuth controls. Both account
identities and all three manifest bullets are shown before one approval. First-time login is
required on both services. A changed session cannot reuse an intent. Records remain private until
the owner explicitly publishes them, subject to the partner's own privacy settings.

The Musecat implementation includes authenticated lookup/receipt calls, independent PKCE code
exchange, current-generation evidence checks, durable request replay, bearer rotation and the
revocation outbox. Failed or ambiguous approval requires a fresh intent. The settings page reports
partial connections and remote revocations still awaiting delivery. Real partner integration,
credential provisioning and deployment remain separate operational steps.
