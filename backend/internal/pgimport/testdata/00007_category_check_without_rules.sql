-- The category check no longer reads the space's rules: they have already run
-- on the row by the time it is checked. An automation that still runs the
-- template's own prompt stops asking for them; one whose prompt was rewritten
-- keeps what it asked for.

-- +goose Up
UPDATE assistant_automations
   SET context = context || '{"rules": false}'::jsonb
 WHERE template_key = 'check_category' AND prompt_from_template;

-- +goose Down
UPDATE assistant_automations
   SET context = context || '{"rules": true}'::jsonb
 WHERE template_key = 'check_category' AND prompt_from_template;
