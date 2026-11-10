-- platform#107: metering points whose stored grid operator differs from the one derived from the
-- metering point number (first 8 characters, translated with the alias list).
-- Read only. Keep the alias list in sync with grid-operator-alias in the backend config.
WITH alias(old_code, new_code) AS (VALUES
  ('AT008200', 'AT008000'), ('AT008230', 'AT008000'), ('AT008320', 'AT008000'),
  ('AT008380', 'AT008000'), ('AT008480', 'AT008000'), ('AT008830', 'AT008000'),
  ('AT008960', 'AT008000')),
soll AS (
  SELECT m.tenant, e.area, upper(e.gridoperator_code) AS eeg_nb, m.metering_point_id,
         m.grid_operator_id, m.status,
         coalesce(a.new_code, upper(substr(trim(m.metering_point_id), 1, 8))) AS soll
  FROM base.meteringpoint m
  JOIN base.eeg e ON e.tenant = m.tenant
  LEFT JOIN alias a ON a.old_code = upper(substr(trim(m.metering_point_id), 1, 8))
  -- like the backend: only numbers that start with AT + 6 digits are derived
  WHERE upper(trim(m.metering_point_id)) ~ '^AT[0-9]{6}')
SELECT s.tenant, s.area, s.eeg_nb, s.metering_point_id, s.grid_operator_id AS ist, s.soll,
       s.status,
       EXISTS (SELECT 1 FROM base.gridoperators g WHERE g.id = s.soll) AS soll_bekannt,
       (s.soll <> s.eeg_nb) AS weicht_von_eeg_ab
FROM soll s
WHERE coalesce(upper(s.grid_operator_id), '') <> s.soll
ORDER BY s.area, s.tenant, s.metering_point_id;
