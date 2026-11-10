-- Undo fix.sql: current entries (flag 1) the cleanup changed and nobody changed since (modifiedBy
-- still 'cleanup-2026-10-24') get their rejected process state back; last_process_state is cleared.
-- Entries changed later (e.g. a new registration, or flag 0 after a re-registration elsewhere) keep
-- their newer state.
--
-- Self-guarding like fix.sql; expected = the count of the query in the README ("Undo"):
--   pgz eegfaktura -v expected=<anzahl> < rollback.sql
\set ON_ERROR_STOP on
\if :{?expected}
\else
  \echo 'ABBRUCH: -v expected=<anzahl> fehlt. Nichts geaendert.'
  SELECT 1/0 AS expected_fehlt;
\endif

BEGIN;

WITH restored AS (
  UPDATE base.meteringpoint
  SET process_state      = last_process_state,
      last_process_state = NULL,
      "modifiedAt"       = now(),
      "modifiedBy"       = 'rollback-cleanup-2026-10-24'
  WHERE flag = 1
    AND "modifiedBy" = 'cleanup-2026-10-24'
    AND process_state IN ('INACTIVE', 'ACTIVE')
    AND last_process_state IN ('INVALID', 'REJECTED')
    AND ((status = 'ACTIVE' AND last_process_state = 'INVALID')
         OR (status = 'INACTIVE' AND inactivesince IS NOT NULL AND activesince IS NOT NULL))
  RETURNING tenant)
SELECT count(*) AS geaendert, count(DISTINCT tenant) AS tenants FROM restored \gset

\echo 'zurueckgesetzt' :geaendert 'in' :tenants 'Tenants, erwartet' :expected
SELECT (:geaendert = :expected) AS anzahl_ok \gset
\if :anzahl_ok
  COMMIT;
  \echo 'COMMIT: Rollback gespeichert.'
\else
  \echo 'ABBRUCH: Anzahl weicht ab, nichts gespeichert.'
  SELECT 1/0 AS anzahl_weicht_ab;
\endif
