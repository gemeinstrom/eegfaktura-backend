-- One-time cleanup (konzept-zaehlpunkt-ausblenden.md, part 2): a rejection no longer undoes a
-- participation. Current entries (flag 1) with process state INVALID/REJECTED get
--   status INACTIVE with activesince and inactivesince (any code)      -> process state INACTIVE
--   status ACTIVE, INVALID with statusCode 172, 173, 183, 188           -> process state ACTIVE
--   (REJECTED is left alone: an abort keeps an old code, so the code says nothing there)
-- (keep the code list in sync with check.sql and model.REJECTED_KEEP_ACTIVE_CODES)
-- The rejected state goes to last_process_state; statusCode, status and dates stay.
--
-- Self-guarding, one psql run (see README): pass the total "anzahl" of check.sql as expected,
--   pgz eegfaktura -v expected=<anzahl> < fix.sql
-- The script commits only if exactly that many rows changed; otherwise it stops with an error and
-- nothing is stored. A re-run changes nothing (last_process_state IS NULL): expected=0.
\set ON_ERROR_STOP on
\if :{?expected}
\else
  \echo 'ABBRUCH: -v expected=<anzahl der Summenzeile von check.sql> fehlt. Nichts geaendert.'
  SELECT 1/0 AS expected_fehlt;
\endif

BEGIN;

WITH changed AS (
  UPDATE base.meteringpoint
  SET last_process_state = process_state,
      process_state      = CASE WHEN status = 'ACTIVE' THEN 'ACTIVE' ELSE 'INACTIVE' END,
      "modifiedAt"       = now(),
      "modifiedBy"       = 'cleanup-2026-10-24'
  WHERE flag = 1
    AND process_state IN ('INVALID', 'REJECTED')
    AND last_process_state IS NULL
    AND ((status = 'ACTIVE' AND process_state = 'INVALID' AND "statusCode" IN (172, 173, 183, 188))
         OR (status = 'INACTIVE' AND inactivesince IS NOT NULL AND activesince IS NOT NULL))
  RETURNING tenant, status)
SELECT count(*) AS geaendert,
       count(*) FILTER (WHERE status = 'INACTIVE') AS beendet,
       count(*) FILTER (WHERE status = 'ACTIVE') AS aktiv,
       count(DISTINCT tenant) AS tenants
FROM changed \gset

\echo 'geaendert' :geaendert '(beendet' :beendet ', aktiv' :aktiv ') in' :tenants 'Tenants, erwartet' :expected
SELECT (:geaendert = :expected) AS anzahl_ok \gset
\if :anzahl_ok
  COMMIT;
  \echo 'COMMIT: Bereinigung gespeichert.'
\else
  \echo 'ABBRUCH: Anzahl weicht ab, nichts gespeichert. check.sql erneut ausfuehren.'
  SELECT 1/0 AS anzahl_weicht_ab;
\endif
