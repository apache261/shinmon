# Shinmon Administrator Guide

The dashboard is the supported management interface. It calls the bearer-
protected `/admin/v1` API through the dashboard's same-origin Nginx proxy.
Management changes are stored in PostgreSQL immediately but do not affect data
plane traffic until a new configuration is activated.

## Services, versions, and upstreams

Create a service with a stable lowercase identifier. Its display name, owner,
and enabled state can be edited later.

Version names may contain any 1–128 Unicode characters except whitespace. They
do not need a `v` prefix. Examples include `v1`, `stable`, `2026.08`, and
`release_candidate`. Version name, health path, timeout, maximum request size,
and enabled state are editable.

Every enabled version used by a listener needs at least one enabled upstream.
Upstreams use a literal IPv4 or IPv6 address and must be inside
`GATEWAY_UPSTREAM_ALLOWED_CIDRS`. Protocol, address, port, weight, health path,
and enabled state are editable. HTTPS certificate verification uses the literal
IP, so the certificate must include that IP in its subject alternative names.

## Clients, access rules, and keys

Access rules are friendly dashboard selections backed by technical permission
identifiers. API clients can be edited to change their display name, enabled
state, and assigned access rules.

Raw API keys are shown once. Store them in the client's secret manager before
closing the dialog. Later views expose only masked metadata. Rotation creates a
new key and revokes the old one atomically; revocation is distributed
immediately with PostgreSQL polling as fallback.

Clients with key history cannot be deleted. Revoke their keys and disable the
client instead. Unused clients and access rules may be removed when no database
references remain.

## Listeners and unprotected routes

Each listener maps one external port to one service version. It still requires
an access rule even when selected documentation routes are public.

`Unprotected route regex` is an optional Go RE2 expression matched against the
decoded URL path. A practical documentation/static example is:

```regex
^/(swagger|docs)(/.*)?$|\.(js|css|ya?ml|jpe?g|png|svg)$
```

Only matching `GET` and `HEAD` requests bypass API-key and permission checks.
`POST`, `PUT`, `PATCH`, `DELETE`, and other operation methods remain protected,
including requests sent from Swagger's “Try it out” interface. Anonymous
requests still obey allowed methods, request-size limits, rate and quota limits,
upstream health, and circuit policies.

Keep expressions anchored and narrow. Avoid expressions such as `.*`, `^/`, or
file-extension rules that overlap ordinary API response endpoints. An empty
expression disables unprotected routes.

Listener policy edits require a newly activated configuration before gateways
load them.

## Publishing configuration

The publishing lifecycle is:

1. **Prepare** management changes.
2. **Capture** them in a new immutable draft snapshot.
3. **Validate** references, upstream availability, and listener readiness.
4. **Approve** with distinct actors when approvals are configured. The creator
   cannot approve their own snapshot.
5. **Activate** the validated snapshot atomically across gateway replicas.

Rollback does not modify history. It creates a new active configuration from a
previous snapshot. Confirm both gateway replicas report the expected loaded
configuration before distributing a new client address.
