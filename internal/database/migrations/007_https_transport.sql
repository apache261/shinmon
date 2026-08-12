ALTER TABLE upstreams
ADD COLUMN IF NOT EXISTS scheme TEXT NOT NULL DEFAULT 'http'
CHECK (scheme IN ('http', 'https'));

ALTER TABLE upstreams
DROP CONSTRAINT IF EXISTS upstreams_service_version_id_address_port_key;

CREATE UNIQUE INDEX IF NOT EXISTS upstreams_service_version_address_port_scheme_key
ON upstreams(service_version_id, address, port, scheme);
