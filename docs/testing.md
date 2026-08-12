# Shinmon Testing and Release Checks

Test commands that open listeners require local socket access. PostgreSQL and
Redis integration tests mutate state and must use disposable services or a
fresh disposable database. Never point them at a maintained environment.

## Core checks

```sh
gofmt -w cmd internal
go test -race ./...
go vet ./...
go build ./cmd/gateway ./cmd/gateway-admin ./cmd/shinmon-loadtest
npm test --prefix web/admin
sh -n scripts/*.sh
```

For database and coordination changes, provide both disposable URLs:

```sh
SHINMON_TEST_DATABASE_URL='postgres://...' \
SHINMON_TEST_REDIS_URL='redis://...' \
go test -race -count=1 \
  ./internal/controlplane ./internal/dataplane ./internal/coordination
```

## Browser smoke

Start the independent dashboard and management API. Chrome automation exercises
all seven routes five times and checks for duplicate DOM IDs and W2UI registry
growth. Firefox automation uses the browser's built-in Marionette protocol.

```sh
SHINMON_DASHBOARD_TEST_TOKEN="$GATEWAY_ADMIN_BEARER_TOKEN" node web/admin/tests/browser-smoke.js
SHINMON_DASHBOARD_TEST_TOKEN="$GATEWAY_ADMIN_BEARER_TOKEN" node web/admin/tests/firefox-smoke.js
```

## HAProxy and load

Use only a disposable database:

```sh
docker compose -f gateway-docker-compose.yml --env-file deploy-dev.env --profile smoke up -d --build
./scripts/smoke-stack.sh
./scripts/integration-stack.sh
mkdir -p bin
go build -o bin/shinmon-loadtest ./cmd/shinmon-loadtest
./scripts/load-stack.sh
```

The load test distributes traffic across all 100 development listener ports and
compares HAProxy/gateway latency with a direct test-upstream baseline. The
development acceptance thresholds are at least 100 requests/second, p95 at or
below 100 ms, p99 at or below 250 ms, and zero HTTP errors. These are engineering
smoke thresholds, not production capacity commitments.

## Security

Install `govulncheck` v1.6.0 and Grype v0.116.1 or newer, build both images, then run
`scripts/security-scan.sh`. The scan fails for reachable Go vulnerabilities,
committed key patterns, or critical container CVEs.

```sh
./scripts/security-scan.sh
```

## PostgreSQL and Redis coordination

Run PostgreSQL and Redis integration tests with disposable services:

```sh
SHINMON_TEST_DATABASE_URL="$GATEWAY_DATABASE_URL" \
SHINMON_TEST_REDIS_URL="$GATEWAY_REDIS_URL" \
go test -race -count=1 ./internal/controlplane ./internal/dataplane ./internal/coordination
./scripts/integration-coordination.sh
```

The live script verifies a shared rate limit through both HAProxy-balanced
replicas, immediate revocation, conservative Redis-outage rejection,
PostgreSQL-polling fallback, Redis recovery, and cleanup of the test policy.

## HTTP and HTTPS transport

`go test -race ./...` includes a verified HTTPS upstream proxy test and rejects
unsupported upstream schemes and unpaired server certificate settings. Run
`scripts/validate-deployment.sh` to validate both the HTTP listener pools and
the TLS 1.2+ production HAProxy pool with an ephemeral validation certificate.

Render both dashboard profiles as part of deployment verification:

```sh
docker compose -f dashboard-docker-compose.yml --env-file dashboard.env.example config --quiet
docker compose -f dashboard-docker-compose.yml -f deploy/dashboard/docker-compose.stack.yml --env-file dashboard-prod.env.example config --quiet
```

Live TLS smoke testing requires operator-supplied test certificates with the
same IP and DNS subject alternative names documented in `docs/deployment.md`.
