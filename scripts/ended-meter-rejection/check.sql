-- Metering points whose rejection undid a participation (konzept-zaehlpunkt-ausblenden.md, part 2).
-- Read only. Current entries (flag 1) with process state INVALID/REJECTED and no last_process_state.
--
-- 1) One row per tenant plus a total row (tenant NULL):
--   anzahl       rows fix.sql changes = beendet + aktiv; the total row is the "expected" value for fix.sql
--   beendet      status INACTIVE with activesince and inactivesince (any code)     -> fix.sql: INACTIVE
--   aktiv        status ACTIVE, INVALID with statusCode 172, 173, 183, 188         -> fix.sql: ACTIVE
--   tenants      tenants with such rows
--   ohne_datum   status INACTIVE without activesince or inactivesince: reported only, left alone
--   aktiv_andere status ACTIVE with another code (56, 57, 185, …) or REJECTED (e.g. an abort, which keeps an
--                old code): reported only, stays INVALID/REJECTED
--   beispiele    up to five changed rows per tenant (metering point, status, process state, code, inactivesince)
-- 2) Counts per variant and statusCode.
-- Keep the code list in sync with model.REJECTED_KEEP_ACTIVE_CODES.
WITH r AS (
  SELECT m.*,
         CASE
           WHEN m.status = 'INACTIVE' AND m.inactivesince IS NOT NULL AND m.activesince IS NOT NULL THEN 'beendet'
           WHEN m.status = 'INACTIVE' THEN 'ohne_datum'
           WHEN m.process_state = 'INVALID' AND m."statusCode" IN (172, 173, 183, 188) THEN 'aktiv'
           ELSE 'aktiv_andere'
         END AS variante
  FROM base.meteringpoint m
  WHERE m.flag = 1
    AND m.status IN ('INACTIVE', 'ACTIVE')
    AND m.process_state IN ('INVALID', 'REJECTED')
    AND m.last_process_state IS NULL)
SELECT r.tenant,
       count(*) FILTER (WHERE r.variante IN ('beendet', 'aktiv')) AS anzahl,
       count(*) FILTER (WHERE r.variante = 'beendet') AS beendet,
       count(*) FILTER (WHERE r.variante = 'aktiv') AS aktiv,
       count(DISTINCT r.tenant) FILTER (WHERE r.variante IN ('beendet', 'aktiv')) AS tenants,
       count(*) FILTER (WHERE r.variante = 'ohne_datum') AS ohne_datum,
       count(*) FILTER (WHERE r.variante = 'aktiv_andere') AS aktiv_andere,
       (array_agg(r.metering_point_id || ' ' || r.status || ' ' || r.process_state || ' '
                  || coalesce(r."statusCode"::text, '-') || ' ' || coalesce(r.inactivesince::text, '-')
                  ORDER BY r.metering_point_id) FILTER (WHERE r.variante IN ('beendet', 'aktiv')))[1:5] AS beispiele
FROM r
GROUP BY GROUPING SETS ((r.tenant), ())
ORDER BY r.tenant IS NULL DESC, anzahl DESC, r.tenant;

SELECT CASE
         WHEN m.status = 'INACTIVE' AND m.inactivesince IS NOT NULL AND m.activesince IS NOT NULL THEN 'beendet'
         WHEN m.status = 'INACTIVE' THEN 'ohne_datum'
         WHEN m.process_state = 'INVALID' AND m."statusCode" IN (172, 173, 183, 188) THEN 'aktiv'
         ELSE 'aktiv_andere'
       END AS variante,
       m."statusCode", count(*) AS anzahl
FROM base.meteringpoint m
WHERE m.flag = 1
  AND m.status IN ('INACTIVE', 'ACTIVE')
  AND m.process_state IN ('INVALID', 'REJECTED')
  AND m.last_process_state IS NULL
GROUP BY 1, 2
ORDER BY 1, 3 DESC, 2;
