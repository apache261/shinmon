<p align="center"><img src="admin/assets/shinmon-logo.svg" width="112" alt="Shinmon logo"></p>

# Shinmon Administration Dashboard

The Shinmon dashboard is a dependency-light JavaScript single-page application
served by Nginx. It manages services, upstreams, listeners, consumers, API keys,
configuration publication, gateway health, and audit history through the
management API.

## Layout

- `admin/index.html` provides the application shell and login screen.
- `admin/js/` contains routing, views, reusable components, and API access.
- `admin/css/` contains the dashboard layout and component styles.
- `admin/tests/` contains dependency-free JavaScript and browser smoke tests.
- `admin/vendor/` contains pinned browser libraries served locally.

The dashboard does not store the administrator bearer token in browser storage.
It keeps the credential only in memory for the current tab and sends it to the
management API through the same-origin Nginx proxy.

## Dashboard launcher

Start the gateway stack first so its private control network and management API
are available. The dashboard can then be managed independently from the
repository root.

Development over HTTP:

```sh
./deploy-dashboard.sh dev config
./deploy-dashboard.sh dev up
```

Production listener profile over HTTP, restricted to isolated testing:

```sh
./deploy-dashboard.sh prod http config
./deploy-dashboard.sh prod http up
```

Production listener profile over HTTPS:

```sh
./deploy-dashboard.sh prod https config
./deploy-dashboard.sh prod https up
```

The supported actions are `config`, `pull`, `up`, and `down`. The launcher
creates the matching private environment file from its example when absent.
Open the dashboard on port `4042` using the selected `http` or `https` scheme.

The launcher selects the matching dashboard transport file:

- HTTP testing profile: `dashboard-prod-http.env.example`
- HTTPS profile: `dashboard-prod.env.example`

HTTP is restricted to isolated development and testing. HTTPS requires the
certificate files documented in `deploy/tls/README.md`.

## Client API keys

Issued client keys are displayed only once. API clients send the raw value on
protected requests using this header:

```http
X-API-Key: <issued-api-key>
```

Client API keys are not management bearer tokens. The gateway removes API keys
and authorization headers before proxying requests upstream.

## Test

Run the dependency-free dashboard tests from the repository root:

```sh
npm test --prefix web/admin
```

The browser smoke tests require a running dashboard and management API. See the
root `README.md` and `docs/testing.md` for the complete verification workflow.
