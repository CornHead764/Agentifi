-- A model's run records the confidence its proposal states for itself. A run
-- whose answer wrote the figure in a form the reader missed ("- Confidence
-- 0.97") recorded the payee history's vote instead; this re-reads the figure
-- from the summary of the run's last proposal that states one.

-- +goose Up
UPDATE assistant_automation_runs r
   SET confidence = p.value
  FROM (
        SELECT DISTINCT ON (a.conversation_id) a.conversation_id,
               m[1]::double precision / CASE WHEN m[2] = '%' THEN 100 ELSE 1 END AS value
          FROM assistant_actions a,
               LATERAL regexp_match(a.summary,
                 '(?i)confidence[*_`[:space:]]*[:=]?[*_`[:space:]]*([0-9]*\.?[0-9]+)[[:space:]]*(%?)') AS m
         WHERE m IS NOT NULL
         ORDER BY a.conversation_id, a.created_at DESC, a.id DESC
       ) p
 WHERE r.conversation_id = p.conversation_id
   AND r.decided_by = 'model'
   AND p.value BETWEEN 0 AND 1
   AND r.confidence IS DISTINCT FROM p.value;

-- +goose Down
-- The figures replaced were the wrong ones, and are not kept to put back.
SELECT 1;
