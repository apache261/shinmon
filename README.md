<p align="center"><img src="web/admin/assets/shinmon-logo.svg" width="120" alt="Shinmon logo"></p>

# Shinmon

Shinmon is a port-only API management platform I am building in Go. It gives
you one place to register services, assign listener ports, create consumers,
issue API keys, and publish gateway configurations.

The stack uses HAProxy at the edge, two Go gateway replicas for traffic policy,
PostgreSQL for persistent configuration, Redis for short-lived distributed
state, and a dependency-light JavaScript dashboard served by Nginx.

This is a project to explore and try in an environment you control. Run it,
open the dashboard, and see whether the approach fits the APIs you handle.

## Run locally

You need Docker with Compose, POSIX shell utilities, ports `4041-4044`, and the
listener range `4100-4199` available on your machine.

```sh
./deploy-dev.sh build
./deploy-dev.sh up
```

The launcher creates `deploy-dev.env` and `dashboard.env` from the included
examples when they do not exist. It then starts PostgreSQL, Redis, the
management API, two gateway replicas, HAProxy, and the dashboard.

Open the dashboard at `http://127.0.0.1:4042` and sign in with the example
development administrator token:

```text
shinmon-dev-bootstrap-token-change-me-4041
```

Open **Help** in the dashboard for the guided workflow. It walks through
registering a service and version, adding upstreams, allocating a port, creating
a consumer and permission, publishing a configuration, and issuing a key. The
dashboard also explains the available fields while you explore them.

Copy an issued API key when it is shown. The raw key is displayed only once.
Clients place it in the `X-API-Key` header on protected requests:

```http
X-API-Key: <issued-api-key>
```

`Authorization: Bearer ...` is for administrative and metrics endpoints, not
for client API keys.

Stop the stack without deleting PostgreSQL and Redis data:

```sh
./deploy-dev.sh down
```

Delete the local development data and start fresh:

```sh
./deploy-dev.sh down -v
```

## Run the production listener examples

The production listener examples use ports `4300-4399`. Both HTTP and HTTPS
profiles are included so you can test the setup that matches your environment.

### HTTPS

```sh
cp deploy-prod.env.example deploy-prod.env
cp dashboard-prod.env.example dashboard-prod.env
./deploy-prod.sh build
./deploy-prod.sh up
```

Before starting HTTPS, place these private files in `deploy/tls` or point the
environment variables at a private directory:

- `edge.pem` contains the edge certificate chain and private key.
- `internal.crt` and `internal.key` secure the management API.
- `internal-ca.pem` lets HAProxy and the dashboard verify internal HTTPS.
- `dashboard.crt` and `dashboard.key` secure the dashboard.

Do not commit certificates or private keys. Replace every placeholder password,
token, and pepper in the copied environment files. HTTPS startup stops when a
required file is missing and does not fall back to HTTP.

### HTTP

```sh
cp deploy-prod-http.env.example deploy-prod.env
cp dashboard-prod-http.env.example dashboard-prod.env
./deploy-prod.sh build
./deploy-prod.sh up
```

HTTP is convenient for an isolated test, but it sends API keys, administrator
credentials, and traffic without transport encryption. Keep that in mind when
choosing where to run it.

## Run only the dashboard

Start the gateway stack first so its control network exists. You can then
manage the dashboard separately:

```sh
# Development over HTTP
./deploy-dashboard.sh dev up

# Production listener profile over HTTP
./deploy-dashboard.sh prod http up

# Production listener profile over HTTPS
./deploy-dashboard.sh prod https up
```

Replace `up` with `down` to stop a profile. Use `config` to validate it or
`pull` to fetch the Nginx image.

## Run across multiple hosts

The multi-host setup supports one or two edge hosts, two to eight gateway
hosts, and one or two management and dashboard hosts. Every gateway and
management host must connect to the same PostgreSQL and Redis services.

First build and push images that every host can pull:

```sh
SHINMON_REGISTRY=registry.example/shinmon \
SHINMON_IMAGE_TAG=release-2026.08 \
docker compose -f deploy/multihost/images.compose.yml build

SHINMON_REGISTRY=registry.example/shinmon \
SHINMON_IMAGE_TAG=release-2026.08 \
docker compose -f deploy/multihost/images.compose.yml push
```

Create private deployment files from the examples, then update the host
addresses, image registry, database URL, Redis URL, tokens, and key pepper:

```sh
cp deploy/multihost/inventory.example.env inventory.env
cp deploy/multihost/common.example.env common.env
chmod 600 common.env
./scripts/validate-multihost.sh inventory.env
./scripts/validate-multihost.sh inventory.env --reachability
./scripts/render-multihost.sh inventory.env common.env build/multihost
```

The renderer creates one directory for each host under `build/multihost`. It
does not connect to the hosts or deploy anything. Copy each matching directory
to `/opt/shinmon/releases/<release>` on its host, point
`/opt/shinmon/current` to that release, install the included
`shinmon.service`, and enable the service.

Multi-host mode uses HTTP by default. For HTTPS, set
`SHINMON_TRANSPORT_MODE=https` and `SHINMON_TLS_SOURCE_DIR` in `inventory.env`.
The TLS directory must contain `edge.pem`, `internal.crt`, `internal.key`,
`internal-ca.pem`, `dashboard.crt`, and `dashboard.key`. The validator rejects
missing, expired, or incorrectly named certificates.

## Default ports

- Gateway internal HTTP: `4040`
- Management API: `4041`
- Dashboard: `4042`
- PostgreSQL host publication: `4043`
- Redis host publication: `4044`
- Development listeners: `4100-4199`
- Staging listeners: `4200-4299`
- Production listeners: `4300-4399`

The management API, PostgreSQL, and Redis bind to loopback by default. Gateway
replicas are reached through HAProxy rather than published directly.

## Run the checks

```sh
gofmt -w cmd internal
go test -race ./...
go vet ./...
go build ./cmd/gateway ./cmd/gateway-admin ./cmd/shinmon-loadtest
npm test --prefix web/admin
sh -n scripts/*.sh
./scripts/validate-deployment.sh deploy-dev.env.example
./scripts/validate-deployment.sh deploy-prod-http.env.example
./scripts/validate-deployment.sh deploy-prod.env.example
```

Integration tests need disposable PostgreSQL and Redis instances. The stack
scripts under `scripts/` cover integration, coordination, load, smoke, and
security checks when you want to dig further.

## Why I built it

Development of Shinmon began as a private project local git in December 2024.

I am a software developer who became annoyed by the number of APIs I had to
track across different addresses, ports, credentials, permissions, health
checks, and deployment notes. Shinmon grew from wanting those boundaries and
the publishing workflow in one place.

I built much of it during spare time while travelling, waiting in airports,
staying in hotels, and during the occasional wait while my girlfriend was
getting ready. It gave me a practical way to explore Go beyond tutorials.

The name **Shinmon** comes from the Japanese word **神門** (*shinmon*), which
refers to a gate at a Shinto shrine. I found it while googling names, and the
idea of a guarded boundary fit the project. The logo uses the same idea with a
gate and an S-shaped route through the center.

Kubernetes is not supported yet because I am still early in learning it. The
current setup focuses on Docker Compose and multi-host deployment, while I keep
working toward Kubernetes support.
