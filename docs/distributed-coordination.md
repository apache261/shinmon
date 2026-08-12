# Distributed Coordination and Approvals

PostgreSQL remains Shinmon's authoritative configuration and credential store.
Redis stores only short-lived counters, circuit state, and best-effort change
notifications. PostgreSQL polling remains enabled even while Pub/Sub is healthy.

## Listener policies

Listener allocation and `PATCH /admin/v1/listeners/{id}/policies` accept:

- `rateLimitPerSecond` and `rateLimitBurst`: an environment/listener/consumer
  fixed-second distributed limit. Zero disables the rate limit.
- `quotaRequestsPerMinute`: a distributed consumer quota. Zero disables it.
- `circuitFailureThreshold`: consecutive upstream failures before opening the
  shared circuit. Zero disables the circuit.
- `circuitOpenMs`: how long the circuit remains open before traffic may retry.
- `unprotectedRouteRegex`: an optional RE2 expression, up to 2048 characters,
  matched against the URL path. The administrator defines the complete scope;
  for example, `^/(swagger|docs)(/.*)?$|\.(js|css|ya?ml|jpe?g|png|svg)$`.
  Only `GET` and `HEAD` skip API-key and permission checks; all other methods
  remain authenticated. Anonymous traffic still uses configured listener rate,
  quota, circuit, method, size, and content policies. Anchor expressions and
  avoid matching ordinary API response paths.

Policy edits are included only after creating, validating, approving when
required, and activating a new immutable configuration.

## Notifications and fallback

Key rotation or revocation publishes a `credentials` event. Activation and
rollback publish a `configuration` event. Every gateway receiving the event
immediately reloads a complete PostgreSQL snapshot. Events contain no keys,
consumer identifiers, snapshots, or credentials.

Redis publication failure never rolls back a committed PostgreSQL mutation.
Gateways continue polling at `GATEWAY_CONFIG_POLL_INTERVAL`, so Redis is an
acceleration mechanism rather than a source of truth.

## Redis failure behavior

When a listener has a rate, quota, or circuit policy enabled and Redis cannot
confirm the distributed state, the request fails closed with a sanitized `503`
and `GATEWAY_POLICY_UNAVAILABLE`. Controls are never silently bypassed. A
listener with every distributed policy disabled can continue using its valid
in-memory snapshot.

## Configuration approvals

Set `GATEWAY_CONFIGURATION_APPROVALS_REQUIRED` on `gateway-admin` from `0` to
`5`. A validated configuration then requires that many unique calls to
`POST /admin/v1/configurations/{version}/approve`. The configuration creator
cannot approve their own change, duplicate approvals conflict, and every
approval is written to the immutable audit log. Rollback remains an audited
emergency action.
