INSERT INTO configuration_versions (
    environment_id,
    status,
    snapshot,
    created_by,
    validated_at,
    activated_at
)
SELECT
    environment.id,
    'active',
    jsonb_build_object(
        'environment', environment.name,
        'services', jsonb_build_array(),
        'serviceVersions', jsonb_build_array(),
        'upstreams', jsonb_build_array(),
        'listeners', jsonb_build_array()
    ),
    'system-bootstrap',
    NOW(),
    NOW()
FROM environments environment
WHERE NOT EXISTS (
    SELECT 1
    FROM configuration_versions configuration
    WHERE configuration.environment_id = environment.id
      AND configuration.status = 'active'
);
