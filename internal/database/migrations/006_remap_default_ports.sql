-- Remap only installations that still use the original default pools. Custom
-- pools are left untouched. New inventory rows exist before listener updates
-- so the composite listener/inventory foreign key remains valid throughout.
INSERT INTO port_inventory(environment_id, listen_port, status, updated_at)
SELECT e.id,
       CASE e.name WHEN 'development' THEN generated.port - 13900
                   WHEN 'staging' THEN generated.port - 14800
                   WHEN 'production' THEN generated.port - 15700 END,
       'available', NOW()
FROM environments e
CROSS JOIN LATERAL generate_series(e.port_min, e.port_max) AS generated(port)
WHERE (e.name='development' AND e.port_min=18000 AND e.port_max=18099)
   OR (e.name='staging' AND e.port_min=19000 AND e.port_max=19099)
   OR (e.name='production' AND e.port_min=20000 AND e.port_max=20099)
ON CONFLICT DO NOTHING;

UPDATE port_inventory target
SET status=source.status, listener_id=source.listener_id, updated_at=NOW()
FROM environments e, port_inventory source
WHERE target.environment_id=e.id AND source.environment_id=e.id
  AND target.listen_port = source.listen_port + CASE e.name WHEN 'development' THEN -13900 WHEN 'staging' THEN -14800 WHEN 'production' THEN -15700 END
  AND ((e.name='development' AND e.port_min=18000 AND e.port_max=18099)
    OR (e.name='staging' AND e.port_min=19000 AND e.port_max=19099)
    OR (e.name='production' AND e.port_min=20000 AND e.port_max=20099));

UPDATE listeners listener
SET listen_port = listener.listen_port + CASE e.name WHEN 'development' THEN -13900 WHEN 'staging' THEN -14800 WHEN 'production' THEN -15700 END,
    updated_at=NOW()
FROM environments e
WHERE listener.environment_id=e.id
  AND ((e.name='development' AND e.port_min=18000 AND e.port_max=18099)
    OR (e.name='staging' AND e.port_min=19000 AND e.port_max=19099)
    OR (e.name='production' AND e.port_min=20000 AND e.port_max=20099));

UPDATE configuration_versions configuration
SET snapshot=jsonb_set(configuration.snapshot, '{listeners}', COALESCE((
    SELECT jsonb_agg(listener || jsonb_build_object('listenPort',
        (listener->>'listenPort')::integer + CASE e.name WHEN 'development' THEN -13900 WHEN 'staging' THEN -14800 WHEN 'production' THEN -15700 END
    ) ORDER BY (listener->>'listenPort')::integer)
    FROM jsonb_array_elements(COALESCE(configuration.snapshot->'listeners','[]'::jsonb)) listener
), '[]'::jsonb))
FROM environments e
WHERE configuration.environment_id=e.id
  AND ((e.name='development' AND e.port_min=18000 AND e.port_max=18099)
    OR (e.name='staging' AND e.port_min=19000 AND e.port_max=19099)
    OR (e.name='production' AND e.port_min=20000 AND e.port_max=20099));

DELETE FROM port_inventory inventory USING environments e
WHERE inventory.environment_id=e.id
  AND ((e.name='development' AND e.port_min=18000 AND inventory.listen_port BETWEEN 18000 AND 18099)
    OR (e.name='staging' AND e.port_min=19000 AND inventory.listen_port BETWEEN 19000 AND 19099)
    OR (e.name='production' AND e.port_min=20000 AND inventory.listen_port BETWEEN 20000 AND 20099));

UPDATE environments SET port_min=4100,port_max=4199 WHERE name='development' AND port_min=18000 AND port_max=18099;
UPDATE environments SET port_min=4200,port_max=4299 WHERE name='staging' AND port_min=19000 AND port_max=19099;
UPDATE environments SET port_min=4300,port_max=4399 WHERE name='production' AND port_min=20000 AND port_max=20099;
