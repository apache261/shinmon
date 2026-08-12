# Shinmon Deployment Guide

Shinmon provides an HTTP development profile and an HTTPS transport profile.
HTTP is restricted to isolated development and testing. HTTPS encrypts client,
dashboard, management, and optionally upstream traffic, but production use also
requires organizational identity, recovery, monitoring, and security approval.

## Architecture

`gateway-docker-compose.yml` starts PostgreSQL, Redis, `gateway-admin`, two
gateway replicas, and HAProxy. `dashboard-docker-compose.yml` starts the
independent Nginx dashboard. The launch scripts join the dashboard to the
private control network.

PostgreSQL is authoritative. `gateway-admin` applies embedded migrations under
an advisory lock before becoming ready. Redis contains only ephemeral counters,
circuit state, and best-effort notifications. Gateways continue polling
PostgreSQL while notifications are healthy.

HAProxy pre-binds the complete listener pool and replaces any client-supplied
`X-Gateway-Listener-Port` with the trusted destination port. Gateway replicas
have no direct host publication. Adding or activating a listener therefore
does not reload HAProxy or restart a gateway.

## Requirements

- Docker Engine with the Compose plugin.
- POSIX shell utilities.
- A host whose Docker/VPN routes do not overlap the selected Shinmon networks.
- For HTTPS: operator-supplied certificates and private keys.
- For external PostgreSQL or Redis: container-reachable endpoints and the
  corresponding credentials and TLS policy.

## Development deployment

Start the complete stack:

```sh
./deploy-dev.sh build
./deploy-dev.sh up
```

The scripts create missing `deploy-dev.env` and `dashboard.env` files from the
checked-in examples. Review and replace all placeholder secrets. If Docker
requires elevated privileges, invoke the scripts with the deployment account's
approved privilege mechanism.

Check status:

```sh
docker compose -p shinmon-stack \
  -f gateway-docker-compose.yml \
  --env-file deploy-dev.env ps
docker compose -p shinmon-dashboard \
  -f dashboard-docker-compose.yml \
  --env-file dashboard.env ps
```

The dashboard is available at `http://127.0.0.1:4042`. Management, PostgreSQL,
and Redis bind to loopback by default. Stop while preserving data with
`./deploy-dev.sh down`; `./deploy-dev.sh down -v` permanently deletes the
development PostgreSQL and Redis volumes.

## HTTPS transport deployment

Create private runtime configuration:

```sh
cp deploy-prod.env.example deploy-prod.env
cp dashboard-prod.env.example dashboard-prod.env
```

Replace every credential and provide these files through the configured TLS
mounts:

| File | Consumer | Requirement |
|---|---|---|
| `edge.pem` | HAProxy | Leaf certificate, intermediates, and private key |
| `dashboard.crt` / `dashboard.key` | Nginx dashboard | Dashboard server identity |
| `internal.crt` / `internal.key` | Management API | Internal server identity |
| `internal-ca.pem` | Dashboard proxy | CA that verifies `internal.crt` |
| `upstream-ca.pem` | Gateways, optional | Private CA for HTTPS upstreams |

Start and stop:

```sh
./deploy-prod.sh build
./deploy-prod.sh up
./deploy-prod.sh down
```

The production launcher rejects `down -v`. Mount certificates from a secrets
manager or protected host directory; never commit private keys.

## Ports and networks

| Component | Default host port or range |
|---|---:|
| Management API | `127.0.0.1:4041` |
| Dashboard | `4042` |
| PostgreSQL | `127.0.0.1:4043` |
| Redis | `127.0.0.1:4044` |
| Development pool | `4100-4199` |
| Staging pool | `4200-4299` |
| Production pool | `4300-4399` |

Compose defaults to edge `10.254.240.0/24`, control `10.254.241.0/24`, and
HAProxy `10.254.240.10`. If Docker reports that a pool overlaps another address
space, choose two unused subnets and update the entire group together:

```env
SHINMON_EDGE_SUBNET=10.254.240.0/24
SHINMON_CONTROL_SUBNET=10.254.241.0/24
HAPROXY_EDGE_IP=10.254.240.10
GATEWAY_1_EDGE_IP=10.254.240.21
GATEWAY_2_EDGE_IP=10.254.240.22
GATEWAY_TRUSTED_PROXY_CIDRS=10.254.240.10/32
```

Do not broaden `GATEWAY_TRUSTED_PROXY_CIDRS`; it controls who may supply
listener-routing metadata.

## Database and Redis targets

Docker containers use `POSTGRES_HOST`/`POSTGRES_SERVICE_PORT` and
`REDIS_HOST`/`REDIS_SERVICE_PORT`. Host publication is controlled separately by
`POSTGRES_BIND_ADDR`/`POSTGRES_PORT` and `REDIS_BIND_ADDR`/`REDIS_PORT`.

Explicit `GATEWAY_DATABASE_URL` and `GATEWAY_REDIS_URL` values override the
composed URLs. This is useful for encoded credentials, external services, or a
Redis TLS URL. Never log or commit these URLs when they contain credentials.

Use an immutable `SHINMON_IMAGE_TAG` outside development and follow the selected
container registry's tag syntax.

## Upstream transport

Each upstream explicitly selects `http` or `https`. Addresses must be literal
IPs inside `GATEWAY_UPSTREAM_ALLOWED_CIDRS`. HTTPS uses system roots plus the
optional `GATEWAY_UPSTREAM_TLS_CA_FILE`; the certificate must contain the
literal target IP in its subject alternative names.

Do not use broad RFC1918 upstream allowlists in secured environments. Supply
only the service network ranges required by that deployment.

## Dashboard networking and 502 errors

The deployment scripts use `DASHBOARD_STACK_API_TARGET` after attaching the
dashboard to the gateway control network. Development normally uses:

```env
DASHBOARD_STACK_API_TARGET=http://gateway-admin:4041
DASHBOARD_CONTROL_NETWORK=shinmon-stack_control
```

The HTTPS profile uses `https://gateway-admin:4041` and verifies it with
`internal-ca.pem`. A dashboard login `502` means Nginx cannot reach or verify
`gateway-admin`; check container health, shared-network attachment, target
scheme, certificate name, and CA configuration. A `401` instead means the
management service is reachable but the bearer token is invalid.

For a standalone dashboard targeting a host process, set
`DASHBOARD_API_TARGET=http://host.docker.internal:4041` or another resolvable,
container-reachable address.

## Validation

Before starting or updating a deployment:

```sh
sh -n scripts/*.sh
docker compose -f compose.yaml --env-file deploy-dev.env.example config --quiet
docker compose -f dashboard-docker-compose.yml --env-file dashboard.env.example config --quiet
docker compose -f gateway-docker-compose.yml --env-file deploy-dev.env.example config --quiet
docker compose -f gateway-docker-compose.yml --env-file deploy-prod.env.example config --quiet
./scripts/validate-deployment.sh deploy-dev.env.example
```

The validator renders Compose models and checks all HAProxy configurations.
HAProxy validation may print DNS notices for gateway service names because the
temporary validation container is not attached to the runtime network; a zero
exit status is the deciding result.

## Updates and rollback

Build the new images, then let Compose recreate only the changed services. The
management service applies forward-only migrations before readiness. Always
back up PostgreSQL before an update and test migrations against a restored copy.

Application configuration rollback is performed in Shinmon's Configurations
screen and creates a new active immutable snapshot. Container-image rollback is
a separate deployment operation using the previously approved immutable image
tag.
