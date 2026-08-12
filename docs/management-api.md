# Management API Reference

The management API is served under `/admin/v1`. Send the configured bearer
credential and an audit actor on every request:

```http
Authorization: Bearer <administrator-token>
X-Admin-Actor: operator-name
Content-Type: application/json
```

The API rejects unknown JSON fields and request bodies larger than 1 MiB.
Client-facing API keys are not management credentials.

## Resources

| Method and path | Purpose |
|---|---|
| `GET /services` | List services |
| `POST /services` | Create a service |
| `PATCH /services/{name}` | Edit display name, owner, or enabled state |
| `GET /services/{name}/versions` | List service versions |
| `POST /services/{name}/versions` | Create a version |
| `PATCH /service-versions/{id}` | Edit a version and its limits |
| `GET /service-versions/{id}/upstreams` | List upstreams |
| `POST /service-versions/{id}/upstreams` | Create an HTTP/HTTPS upstream |
| `PATCH /service-versions/{id}/upstreams/{upstreamId}` | Edit an upstream |
| `GET /ports` | List port inventory; optional `status` query |
| `PATCH /ports/{port}` | Change an eligible port state |
| `GET /listeners` | List listeners |
| `POST /listeners/allocate-port` | Allocate a listener port |
| `PATCH /listeners/{id}` | Change listener lifecycle state |
| `PATCH /listeners/{id}/policies` | Edit traffic and unprotected-route policies |
| `GET /permissions` | List access rules |
| `POST /permissions` | Create an access rule |
| `PATCH /permissions/{id}` | Edit access-rule notes |
| `DELETE /permissions/{id}` | Delete an unused access rule |
| `GET /consumers` | List API clients |
| `POST /consumers` | Create an API client |
| `PATCH /consumers/{id}` | Edit client display name, state, and access |
| `DELETE /consumers/{id}` | Delete a client without key history |
| `GET /consumers/{id}/keys` | List masked key metadata |
| `POST /consumers/{id}/keys` | Issue a one-time API key |
| `POST /keys/{id}/rotate` | Rotate and revoke the previous key |
| `POST /keys/{id}/revoke` | Revoke a key |
| `GET /configurations` | List immutable configurations |
| `POST /configurations` | Capture a new draft |
| `POST /configurations/{id}/validate` | Validate a draft |
| `POST /configurations/{id}/approve` | Add a distinct approval |
| `POST /configurations/{id}/activate` | Activate a validated snapshot |
| `POST /configurations/{id}/rollback` | Restore a snapshot as a new active version |
| `GET /gateway-instances` | List replica readiness and loaded versions |
| `GET /audit-events` | List immutable audit events |

## Optimistic updates

Edits to services, versions, upstreams, consumers, listeners, and listener
policies include the resource's current `rowVersion` as `expectedVersion`.
A stale value returns `409` and the caller must reload before retrying.

## Listener allocation example

```json
{
  "service": "payments",
  "serviceVersion": "stable",
  "requiredPermission": "payments:stable:invoke",
  "allowedMethods": ["GET", "HEAD", "POST"],
  "allowedContentTypes": ["application/json"],
  "unprotectedRouteRegex": "^/(swagger|docs)(/.*)?$|\\.(js|css|ya?ml|jpe?g|png|svg)$",
  "rateLimitPerSecond": 0,
  "rateLimitBurst": 0,
  "quotaRequestsPerMinute": 0,
  "circuitFailureThreshold": 5,
  "circuitOpenMs": 30000
}
```

The regex uses Go RE2, is limited to 2048 characters, and matches only the URL
path. A match bypasses authentication only for `GET` and `HEAD`.

## Errors and secrets

Errors use concise sanitized JSON and do not expose database or upstream
details. `400` means invalid input, `404` means the resource is absent, `409`
means a conflict or stale version, and `500` is an internal failure recorded in
server logs.

Raw API keys appear only in successful issue and rotation responses. Do not log,
persist in browser storage, or attempt to retrieve them later.
