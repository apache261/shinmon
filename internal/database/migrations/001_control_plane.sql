CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE environments (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (name IN ('development', 'staging', 'production')),
    port_min INTEGER NOT NULL CHECK (port_min BETWEEN 1 AND 65535),
    port_max INTEGER NOT NULL CHECK (port_max BETWEEN 1 AND 65535 AND port_max >= port_min),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO environments (name, port_min, port_max) VALUES
    ('development', 4100, 4199),
    ('staging', 4200, 4299),
    ('production', 4300, 4399);

CREATE TABLE services (
    id TEXT PRIMARY KEY,
    environment_id BIGINT NOT NULL REFERENCES environments(id),
    name TEXT NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{1,62}$'),
    display_name TEXT NOT NULL,
    owner TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    row_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (environment_id, name)
);

CREATE TABLE service_versions (
    id TEXT PRIMARY KEY,
    service_id TEXT NOT NULL REFERENCES services(id),
    version TEXT NOT NULL CHECK (char_length(version) BETWEEN 1 AND 128 AND version !~ '[[:space:]]'),
    health_check_path TEXT NOT NULL DEFAULT '/health',
    request_timeout_ms INTEGER NOT NULL DEFAULT 30000 CHECK (request_timeout_ms BETWEEN 1 AND 300000),
    max_request_bytes BIGINT NOT NULL DEFAULT 2097152 CHECK (max_request_bytes BETWEEN 1 AND 1073741824),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    row_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (service_id, version)
);

CREATE TABLE upstreams (
    id TEXT PRIMARY KEY,
    service_version_id TEXT NOT NULL REFERENCES service_versions(id),
    address INET NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    weight INTEGER NOT NULL DEFAULT 100 CHECK (weight BETWEEN 1 AND 10000),
    health_check_path TEXT NOT NULL DEFAULT '/health',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    row_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (service_version_id, address, port)
);

CREATE TABLE port_inventory (
    environment_id BIGINT NOT NULL REFERENCES environments(id),
    listen_port INTEGER NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
    status TEXT NOT NULL DEFAULT 'available' CHECK (status IN ('available', 'reserved', 'active', 'draining', 'cooldown', 'blocked')),
    listener_id TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (environment_id, listen_port)
);

INSERT INTO port_inventory (environment_id, listen_port)
SELECT environment.id, generated.port
FROM environments environment
CROSS JOIN LATERAL generate_series(environment.port_min, environment.port_max) AS generated(port);

CREATE TABLE configuration_versions (
    id BIGSERIAL PRIMARY KEY,
    environment_id BIGINT NOT NULL REFERENCES environments(id),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'validated', 'active', 'superseded')),
    snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_version_id BIGINT REFERENCES configuration_versions(id),
    created_by TEXT NOT NULL,
    validated_at TIMESTAMPTZ,
    activated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX configuration_versions_one_active
ON configuration_versions(environment_id) WHERE status = 'active';

CREATE TABLE listeners (
    id TEXT PRIMARY KEY,
    environment_id BIGINT NOT NULL REFERENCES environments(id),
    service_version_id TEXT NOT NULL REFERENCES service_versions(id),
    listen_port INTEGER NOT NULL,
    required_permission TEXT NOT NULL CHECK (required_permission <> ''),
    allowed_methods TEXT[] NOT NULL DEFAULT ARRAY['GET']::TEXT[],
    unprotected_route_regex TEXT NOT NULL DEFAULT '',
    auth_policy TEXT NOT NULL DEFAULT 'api-key-required' CHECK (auth_policy = 'api-key-required'),
    status TEXT NOT NULL DEFAULT 'reserved' CHECK (status IN ('reserved', 'active', 'draining', 'disabled')),
    configuration_version BIGINT REFERENCES configuration_versions(id),
    row_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (environment_id, listen_port),
    FOREIGN KEY (environment_id, listen_port) REFERENCES port_inventory(environment_id, listen_port)
);

ALTER TABLE port_inventory
ADD CONSTRAINT port_inventory_listener_fk FOREIGN KEY (listener_id) REFERENCES listeners(id);

CREATE TABLE consumers (
    id TEXT PRIMARY KEY,
    environment_id BIGINT NOT NULL REFERENCES environments(id),
    name TEXT NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{1,62}$'),
    display_name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    row_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (environment_id, name)
);

CREATE TABLE permissions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (name ~ '^[a-z][a-z0-9-]*:[a-zA-Z0-9.-]+:[a-z][a-z0-9-]*$'),
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE consumer_permissions (
    consumer_id TEXT NOT NULL REFERENCES consumers(id) ON DELETE CASCADE,
    permission_id TEXT NOT NULL REFERENCES permissions(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (consumer_id, permission_id)
);

CREATE TABLE api_keys (
    id TEXT PRIMARY KEY,
    consumer_id TEXT NOT NULL REFERENCES consumers(id),
    name TEXT NOT NULL,
    key_prefix TEXT NOT NULL UNIQUE,
    verifier BYTEA NOT NULL,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    rotated_from_id TEXT REFERENCES api_keys(id),
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE gateway_instances (
    id TEXT PRIMARY KEY,
    environment_id BIGINT NOT NULL REFERENCES environments(id),
    address TEXT NOT NULL,
    loaded_configuration_version BIGINT REFERENCES configuration_versions(id),
    ready BOOLEAN NOT NULL DEFAULT FALSE,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE audit_events (
    id BIGSERIAL PRIMARY KEY,
    environment_id BIGINT REFERENCES environments(id),
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE OR REPLACE FUNCTION prevent_audit_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit events are immutable';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_events_no_update
BEFORE UPDATE OR DELETE ON audit_events
FOR EACH ROW EXECUTE FUNCTION prevent_audit_mutation();

CREATE INDEX services_environment_idx ON services(environment_id);
CREATE INDEX service_versions_service_idx ON service_versions(service_id);
CREATE INDEX listeners_environment_idx ON listeners(environment_id);
CREATE INDEX consumers_environment_idx ON consumers(environment_id);
CREATE INDEX audit_events_created_idx ON audit_events(created_at DESC);
