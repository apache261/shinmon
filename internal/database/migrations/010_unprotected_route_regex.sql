ALTER TABLE listeners
ADD COLUMN IF NOT EXISTS unprotected_route_regex TEXT NOT NULL DEFAULT '';

UPDATE listeners
SET unprotected_route_regex = array_to_string(public_paths, '|')
WHERE unprotected_route_regex = '' AND cardinality(public_paths) > 0;

ALTER TABLE listeners
DROP COLUMN IF EXISTS public_paths;
