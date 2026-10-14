-- A series imported from Simplifi carried its name only as its matching text,
-- so it showed as unnamed. Simplifi's matching text for a series is the name
-- the person saw there, so each unnamed series takes it as its display name.
-- A name already set is never touched; matching still reads the description.

-- +goose Up
UPDATE series
   SET display_name = btrim(description)
 WHERE btrim(COALESCE(display_name, '')) = ''
   AND btrim(COALESCE(description, '')) <> '';

-- +goose Down
-- The names written here cannot be told from names a person set.
SELECT 1;
