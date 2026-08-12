ALTER TABLE listeners
    ADD COLUMN rate_limit_per_second INTEGER NOT NULL DEFAULT 0 CHECK (rate_limit_per_second BETWEEN 0 AND 1000000),
    ADD COLUMN rate_limit_burst INTEGER NOT NULL DEFAULT 0 CHECK (rate_limit_burst BETWEEN 0 AND 1000000),
    ADD COLUMN quota_requests_per_minute INTEGER NOT NULL DEFAULT 0 CHECK (quota_requests_per_minute BETWEEN 0 AND 100000000),
    ADD COLUMN circuit_failure_threshold INTEGER NOT NULL DEFAULT 5 CHECK (circuit_failure_threshold BETWEEN 0 AND 1000),
    ADD COLUMN circuit_open_ms INTEGER NOT NULL DEFAULT 30000 CHECK (circuit_open_ms BETWEEN 1000 AND 3600000);

CREATE TABLE configuration_approvals (
    configuration_version BIGINT NOT NULL REFERENCES configuration_versions(id) ON DELETE CASCADE,
    actor TEXT NOT NULL CHECK (actor <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (configuration_version, actor)
);

CREATE INDEX configuration_approvals_version_idx ON configuration_approvals(configuration_version);
