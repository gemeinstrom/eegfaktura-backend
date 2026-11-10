-- platform#107: set the stored grid operator of all metering points to the one derived from the
-- metering point number (first 8 characters, translated with the alias list).
-- Run check.sql first. Metering points that must not be changed yet (open questions to the EEG)
-- go into "excluded". The script does not commit: check the result, then COMMIT or ROLLBACK.
BEGIN;

WITH alias(old_code, new_code) AS (VALUES
  ('AT008200', 'AT008000'), ('AT008230', 'AT008000'), ('AT008320', 'AT008000'),
  ('AT008380', 'AT008000'), ('AT008480', 'AT008000'), ('AT008830', 'AT008000'),
  ('AT008960', 'AT008000')),
excluded(metering_point_id) AS (VALUES
  (NULL::varchar)  -- e.g. ('AT0040000530000000000000005026510'),
  ),
names AS (
  SELECT id, min(name) AS name FROM base.gridoperators GROUP BY id),
soll AS (
  SELECT m.tenant, m.metering_point_id,
         coalesce(a.new_code, upper(substr(trim(m.metering_point_id), 1, 8))) AS soll,
         upper(e.gridoperator_code) AS eeg_nb, e.gridoperator_name AS eeg_name
  FROM base.meteringpoint m
  JOIN base.eeg e ON e.tenant = m.tenant
  LEFT JOIN alias a ON a.old_code = upper(substr(trim(m.metering_point_id), 1, 8))
  -- like the backend: only numbers that start with AT + 6 digits are derived
  WHERE upper(trim(m.metering_point_id)) ~ '^AT[0-9]{6}'
    AND m.metering_point_id NOT IN (SELECT x.metering_point_id FROM excluded x WHERE x.metering_point_id IS NOT NULL))
UPDATE base.meteringpoint m
SET grid_operator_id   = s.soll,
    grid_operator_name = coalesce(n.name, CASE WHEN s.soll = s.eeg_nb THEN s.eeg_name END),
    "modifiedBy"       = 'nb-alias-107',
    "modifiedAt"       = now()
FROM soll s
LEFT JOIN names n ON n.id = s.soll
WHERE m.tenant = s.tenant
  AND m.metering_point_id = s.metering_point_id
  AND coalesce(upper(m.grid_operator_id), '') <> s.soll;

-- Afterwards check.sql must only return the excluded metering points.
-- COMMIT;   or   ROLLBACK;
