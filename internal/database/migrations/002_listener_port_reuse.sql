ALTER TABLE listeners DROP CONSTRAINT listeners_environment_id_listen_port_key;

CREATE UNIQUE INDEX listeners_one_current_port_assignment
ON listeners(environment_id, listen_port)
WHERE status <> 'disabled';
