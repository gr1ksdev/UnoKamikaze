-- Command names are now English-only in every group; all bot texts remain
-- Portuguese. The per-group language setting is obsolete: normalise existing
-- rows and the default, but keep the column for schema compatibility.
ALTER TABLE group_configs ALTER COLUMN command_language SET DEFAULT 'en';
UPDATE group_configs SET command_language='en';
