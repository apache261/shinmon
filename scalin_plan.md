# Multi-Host and Kubernetes Scalability Plan

## Summary

Scale Shinmon to 3–10 edge nodes, 5–50 gateway replicas, and 100–1000 listeners while preserving existing port-based clients.

- Keep dedicated listener ports.
- Add shared port `443` with path-prefix routing.
- Support both Kubernetes and multi-host VM deployments.
- Use managed multi-zone PostgreSQL and Redis.
- Run one active region across availability zones, with documented warm-standby recovery.
- Treat dynamic ports as allocation within approved, expandable ranges—not arbitrary firewall changes per listener.

## Architecture and Runtime Changes

- Place at least three HAProxy edge instances behind an external L4 VIP/load balancer exposing `443` and approved port ranges.
- Continue replacing client-supplied routing metadata at HAProxy. Authenticate edge-to-gateway traffic with mTLS in addition to trusted CIDRs.
- Keep path routing inside Go:
  - Port `443` selects the shared entrypoint.
  - Longest segment-boundary prefix selects the route.
  - `/payments` matches `/payments` and `/payments/...`, not `/payments-old`.
  - Reject ambiguous prefixes, traversal segments, encoded slash ambiguity, and duplicate routes.
  - Preserve the public path by default; allow an explicit `stripPrefix` option per route.
  - Continue ignoring `Host` for service selection.
- Replace fixed gateway backend names with DNS service discovery:
  - Kubernetes: headless Service and HAProxy `server-template`.
  - VMs: DNS SRV or Consul-backed gateway records.
- Reduce database load by adding per-environment configuration and credential generations. Gateways poll the lightweight generation row with jitter and reload snapshots or credentials only when the relevant generation changes.
- Reduce duplicate upstream health traffic by leasing probes per upstream through Redis. Gateways fall back to bounded local probes if Redis is unavailable.
- Add fixed-cardinality in-flight, snapshot reload, convergence, active-route, and edge/gateway readiness metrics.

## Data Model and Management Interfaces

- Add `listener_pools` supporting multiple non-overlapping ranges per environment with `pending`, `active`, and `retiring` states.
  - Listener allocation uses only active pools.
  - Pool expansion is initiated through management but activated only after infrastructure exposes the range and every edge node reports it bound.
  - Retiring a pool prevents new allocations; removal requires every port to be unused.
- Keep existing dedicated listeners unchanged.
- Add shared path routes under port `443`, including:
  - `pathPrefix`
  - `stripPrefix`, default `false`
  - service version and required permission
  - allowed methods/content types
  - unprotected-route regex
  - rate, quota, circuit, timeout, and request-size policies
  - lifecycle status and optimistic `rowVersion`
- Add management endpoints for listing, creating, editing, draining, and removing shared routes and listener pools.
- Include shared routes and pool generations in immutable configuration snapshots, validation, approvals, audit events, activation, and rollback.
- Update the dashboard with:
  - Dedicated Ports and Shared Routes views.
  - Prefix collision validation and effective upstream-path preview.
  - Listener-pool capacity, edge readiness, and expansion state.
  - Gateway replica freshness and configuration convergence indicators.

## Deployment Models

- Kubernetes:
  - HAProxy DaemonSet using `hostNetwork` on three dedicated, tainted edge nodes.
  - External L4 load balancer or BGP/MetalLB advertisement for `443` and approved ranges; do not create one Kubernetes Service per listener.
  - Gateway Deployment with HPA defaults of 5–50 replicas, topology spread, pod anti-affinity, PDB, readiness-driven draining, and a headless discovery Service.
  - Two `gateway-admin` and two dashboard replicas behind internal Services.
  - One-shot migration Job before application rollout.
  - NetworkPolicies, CSI/external secret mounts, Prometheus ServiceMonitors, and restricted service accounts.
- Multi-host VMs:
  - Three HAProxy edge hosts behind an external L4 load balancer or VRRP VIP.
  - At least three gateway hosts registered through DNS SRV/Consul.
  - Two management and dashboard instances behind an internal load balancer.
  - Systemd/container units, health-driven rolling replacement, and the same mTLS and discovery contracts as Kubernetes.
- Use managed multi-zone PostgreSQL with PgBouncer:
  - Separate migration, management, and restricted gateway database roles.
  - Small per-gateway pools to prevent connection multiplication.
  - PITR backups and a cross-region restore/replica procedure.
- Use managed Redis with TLS and automatic primary failover. Redis remains non-authoritative and may start empty after disaster recovery.
- Keep the API-key pepper and TLS identities synchronized securely into the warm-standby region; pre-provision listener pools and certificates there.

## Verification and Rollout

- Test path-prefix boundaries, longest-prefix selection, preserve/strip behavior, traversal rejection, Host isolation, authentication, and `GET`/`HEAD` unprotected-route behavior.
- Test concurrent allocation across multiple listener pools, overlap rejection, expansion activation, retirement, rollback, and edge-generation acknowledgements.
- Run 50 simulated gateway runtimes and prove polling performs lightweight generation reads without repeated full snapshot loads.
- Verify Redis probe leasing prevents duplicate health-check storms and local probing resumes safely during Redis failure.
- Validate Kubernetes node, edge pod, gateway pod, PostgreSQL endpoint, and Redis endpoint failures without listener misrouting.
- Require configuration convergence under five seconds with Redis healthy and within one jittered poll interval when notifications are unavailable.
- Demonstrate at least 80% throughput scaling efficiency from 5 to 10 gateway replicas, no scaling-induced 5xx responses, and no more than 20% p95 latency regression.
- Roll out in order: runtime polling/discovery improvements, edge mTLS, shared `443` routes, expandable pools, VM packaging, Kubernetes packaging, then warm-standby recovery drills.

## Assumptions

- Path prefixes—not hostnames—select shared-port services; `Host` remains non-routing metadata.
- Existing dedicated-port contracts remain backward compatible.
- Path rewriting is configurable per shared route and defaults to preserving the original path.
- Pool expansion is an approved infrastructure operation with seamless edge rollout; individual listener allocation remains immediate.
- Kubernetes Ingress is not used because it does not fit destination-port routing or large port ranges.
- Production approval remains separate from implementing scalable encrypted transport.
