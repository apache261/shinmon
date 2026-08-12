ALTER TABLE listeners
ADD COLUMN allowed_content_types TEXT[] NOT NULL DEFAULT ARRAY[
    'application/json',
    'application/xml',
    'text/xml',
    'application/x-www-form-urlencoded'
]::TEXT[];
