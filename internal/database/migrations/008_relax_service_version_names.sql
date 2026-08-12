ALTER TABLE service_versions
    DROP CONSTRAINT IF EXISTS service_versions_version_check;

ALTER TABLE service_versions
    ADD CONSTRAINT service_versions_version_check
    CHECK (char_length(version) BETWEEN 1 AND 128 AND version !~ '[[:space:]]');
