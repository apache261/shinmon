# Shinmon

Shinmon is a port-only API management platform written in Go. HAProxy owns the
external listener pools, two Go gateway replicas enforce traffic policies, and
PostgreSQL stores authoritative configuration, credentials, approvals, and
audit history. Redis is used only for ephemeral distributed coordination.

The independent administration dashboard is a native JavaScript SPA served by
Nginx. It has no frontend build step.

## Capabilities

- Routing by trusted destination-listener port, never by `Host` or client
  forwarding headers.
- API-key HMAC verification, consumer permissions, one-time key display,
  rotation, expiration, and revocation.
- Editable services, whitespace-free version identifiers, HTTP/HTTPS literal-IP
  upstreams, listeners, clients, permissions, and distributed policies.
- Optional administrator-defined RE2 expressions for unprotected documentation
  and static routes. Only matching `GET` and `HEAD` requests bypass API-key and
  permission checks; operation methods remain protected.
- Immutable configuration snapshots with validation, optional independent
  approvals, activation, and rollback.
- Weighted healthy-upstream selection, request limits, timeouts, content-type
  and method policies, distributed rate limits and quotas, and shared circuits.
- Structured redacted logs, fixed-cardinality metrics, correlation IDs, health
  endpoints, and immutable audit events.
- HTTP for isolated development and HTTPS transport profiles for secured
  environments.

Shinmon is approved here for development and testing. The included HTTPS
profile provides encrypted transport but does not constitute organizational
production approval.

## Quick start

Requirements: Docker with Compose, POSIX shell utilities, and ports `4041-4044`
plus `4100-4199` available on the host.

```sh
./deploy-dev.sh build
./deploy-dev.sh up
```

The launcher creates `deploy-dev.env` and `dashboard.env` from their examples
when absent, starts the database, Redis, management API, two gateways, HAProxy,
and dashboard, then waits for health checks.

Open `http://127.0.0.1:4042`. The example development administrator token is:

```text
shinmon-dev-bootstrap-token-change-me-4041
```

Replace every placeholder secret before using the stack outside isolated local
development. The dashboard keeps its bearer token only in the browser tab's
memory.

Stop the stack while preserving PostgreSQL and Redis data:

```sh
./deploy-dev.sh down
```

Development-only destructive reset:

```sh
./deploy-dev.sh down -v
```

## Default ports

| Component | Port |
|---|---:|
| Gateway internal HTTP | `4040` |
| Management API | `4041` |
| Dashboard | `4042` |
| PostgreSQL host publication | `4043` |
| Redis host publication | `4044` |
| Development listeners | `4100-4199` |
| Staging listeners | `4200-4299` |
| Production listeners | `4300-4399` |

The management, PostgreSQL, and Redis host publications bind to loopback by
default. Gateway replicas are not published directly; HAProxy supplies trusted
`X-Gateway-Listener-Port` metadata and removes any client-supplied value.

## Administration workflow

1. Register a service and version.
2. Add one or more allowlisted literal-IP upstreams using HTTP or verified
   HTTPS.
3. Create an access rule and assign it to an API client.
4. Allocate a listener and configure methods, content types, and policies.
5. Create, validate, approve when required, and activate a configuration.
6. Issue an API key and copy the raw value from its one-time response.

See [Administrator guide](docs/administration.md) for editing, unprotected route
regexes, and publication behavior.

## Documentation

- [Deployment](docs/deployment.md)
- [Administration](docs/administration.md)
- [Management API reference](docs/management-api.md)
- [Operations runbook](docs/operations.md)
- [Testing and release checks](docs/testing.md)
- [Distributed coordination and approvals](docs/distributed-coordination.md)
- [TLS file layout](deploy/tls/README.md)

## Development verification

```sh
gofmt -w cmd internal
go test -race ./...
go vet ./...
go build ./cmd/gateway ./cmd/gateway-admin ./cmd/shinmon-loadtest
npm test --prefix web/admin
sh -n scripts/*.sh
./scripts/validate-deployment.sh deploy-dev.env.example
```

PostgreSQL and Redis integration tests must use disposable data stores. See
[Testing and release checks](docs/testing.md) for the full commands and security
scan requirements.
