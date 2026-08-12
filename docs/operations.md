# Shinmon Operations Runbook

These procedures cover the isolated HTTP development profile and HTTPS
transport profile. HTTPS availability does not replace organizational identity,
backup, recovery, monitoring, and independent security-review requirements.

## Register and activate a service

1. Create the permission, service, version, and literal-IP upstream through the
   dashboard or `/admin/v1`.
2. Allocate a listener from the environment's HAProxy pool.
3. Create a configuration draft and validate it.
4. Review the immutable audit entries and activate with the expected current
   configuration version.
5. Wait for both gateway instances to report the new loaded version. HAProxy
   already owns the complete pool, so no reload is required.

## Rotate an API key

1. Select the consumer and existing key metadata; the raw old key is never
   recoverable.
2. Rotate the key. Copy the new key from the one-time response into the client's
   secret manager.
3. Confirm the old key returns `401` after the configured snapshot poll interval.
4. Confirm the audit event contains only masked key metadata.

## Disable a client

Edit the API client and set its status to Disabled, then revoke each active key.
Do not modify the database directly. Confirm rejection on every gateway replica
and retain the audit evidence. A client with key history is disabled rather than
deleted.

## Change public documentation access

Edit the listener policy and set a narrow `Unprotected route regex`. Create,
validate, approve when required, and activate a new configuration. Verify an
unauthenticated matching `GET` succeeds, a nonmatching `GET` returns `401`, and
an operation method such as `POST` still requires a valid key. An empty regex
restores authentication for every route.

## Roll back a configuration

1. Select a previously active or superseded configuration.
2. Use the rollback action, which creates a new active version referencing the
   selected source snapshot.
3. Confirm both replicas report the new version and exercise one request per
   affected listener.

## Drain and release a port

1. Set the listener to `draining`; new traffic receives the controlled gateway
   response while existing requests finish within their timeout.
2. Disable the listener, moving the inventory entry to cooldown.
3. After the environment's approved cooldown period and client confirmation,
   return the port to `available`. Never reassign an active or draining port.

## PostgreSQL outage

Gateways retain their last valid immutable snapshot and continue serving it.
Management mutations fail safely. Restore PostgreSQL, verify migrations and
replica refreshes, then reconcile gateway-instance versions before further
changes.

## Redis outage

PostgreSQL polling and management mutations remain available. Pub/Sub delivery,
distributed rate limits, quotas, and circuit state are unavailable. Requests on
listeners using any distributed policy fail closed with `503`; do not disable
policies merely to mask an outage. Restore Redis, confirm `PING`, watch
`shinmon_coordination_failures_total`, and verify traffic on each affected
listener. Redis contains no authoritative configuration or raw credential data.

## Upstream or replica outage

Unhealthy upstreams are excluded by bounded health checks. If every upstream is
unhealthy, clients receive sanitized `503`; timeouts produce `504`. HAProxy
removes an unready gateway after its health-check threshold and keeps the other
replica active.

## Metrics and logs

Scrape `GET /metrics` with `Authorization: Bearer $GATEWAY_METRICS_BEARER_TOKEN`
from the protected network. Metrics use fixed label sets only. Access logs record
bounded route categories, status, duration, correlation ID, and peer address;
they never record query strings, headers, credentials, or payloads.

## Pre-release verification

```sh
go test -race ./...
go vet ./...
npm test --prefix web/admin
./scripts/validate-deployment.sh deploy-dev.env
./scripts/security-scan.sh
./scripts/integration-coordination.sh
```

Run the browser, integration, and load procedures described in
`docs/testing.md` only against disposable test data.
