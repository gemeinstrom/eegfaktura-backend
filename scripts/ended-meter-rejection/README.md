# A rejection no longer undoes a participation: cleanup (2026-10-24)

Since this release the backend no longer lets a rejection from the grid operator undo a participation
(`konzept-zaehlpunkt-ausblenden.md`, part 2):

- a current entry (`flag = 1`) whose participation has ended (status `INACTIVE`, `activesince` and
  `inactivesince` set) stays at process state `INACTIVE`, whatever the code;
- an `ACTIVE` entry stays `ACTIVE` when the stored code concerns the request, not the metering point: 172/173
  (consent refused / not answered), 183 (allocation keys > 100 %), 188 (participation factor > 100 %)
  (`model.REJECTED_KEEP_ACTIVE_CODES`); `REVOKED` and other non-registration states stay. For ABLEHNUNG all codes
  of the message that decide the outcome must be such codes (otherwise the code about the metering point is
  stored); ZUSTIMMUNG and ANTWORT keep their existing single-code logic. Other codes (56, 57, 185, …) still set
  `INVALID`/`REJECTED`.

The rejected state goes to `last_process_state`, the code stays in `statusCode`. Before, such entries got
`INVALID` or `REJECTED`, and web showed them in every period among the problem cases.

These scripts bring the existing entries in line. The operator runs them once in **prod and pilot, after the
deploy** (so no new cases come in with the old image), with the `pgz` helper (psql via stdin, `ON_ERROR_STOP=1`).
Namespaces: prod `eegfaktura`, pilot `eegfaktura-pilot`.

## 1. Check (read only)

```bash
pgz eegfaktura < check.sql          # prod
pgz eegfaktura-pilot < check.sql    # pilot
```

First result: one row per tenant and a total row (`tenant` empty):

| column | meaning |
|---|---|
| `anzahl` | rows `fix.sql` changes (`beendet` + `aktiv`) — **the total row's value is `expected`** |
| `beendet` | status `INACTIVE` with `activesince` and `inactivesince`, any code → `INACTIVE` |
| `aktiv` | status `ACTIVE`, process state `INVALID`, code 172/173/183/188 → `ACTIVE` |
| `tenants` | tenants with such rows |
| `ohne_datum` | status `INACTIVE` without `activesince` or `inactivesince`: reported only, left alone (never active, or the participation period is unclear; clarify with the community — a never-active entry can be removed with part 1 of the concept) |
| `aktiv_andere` | status `ACTIVE` with another code (56, 57, 185, …), or `REJECTED` (an abort keeps an old code, so the code says nothing): reported only, stays `INVALID`/`REJECTED` (clarify with the community) |
| `beispiele` | up to five changed rows per tenant |

Second result: counts per variant and `statusCode`.

**Optional, read only — the active variant with its latest EDA message.** Before `fix.sql`, look at the `aktiv`
rows with their latest registration message (type and response codes), to spot messages with several codes or an
abort. One scan of `base.notification`, limited to the tenants of these rows:

```bash
pgz eegfaktura <<'SQL'
WITH m AS (
  SELECT tenant, metering_point_id, "statusCode"
  FROM base.meteringpoint
  WHERE flag = 1 AND status = 'ACTIVE' AND process_state = 'INVALID'
    AND "statusCode" IN (172, 173, 183, 188) AND last_process_state IS NULL),
n AS (
  SELECT DISTINCT ON (m.tenant, m.metering_point_id)
         m.tenant, m.metering_point_id, x.date, x.notification->>'type' AS typ, x.notification->'responseCodes' AS codes
  FROM base.notification x
  JOIN m ON m.tenant = x.tenant AND x.notification::text LIKE '%' || m.metering_point_id || '%'
  WHERE x.tenant IN (SELECT tenant FROM m)
    AND x.notification->>'type' ~ '_ECO[NF]$'
  ORDER BY m.tenant, m.metering_point_id, x.date DESC)
SELECT m.tenant, m.metering_point_id, m."statusCode", n.date, n.typ, n.codes
FROM m LEFT JOIN n USING (tenant, metering_point_id)
ORDER BY m.tenant, m.metering_point_id;
SQL
```

A row whose latest message has more codes than the request-level one, or is an `ABBRUCH_ECON/ECOF`, is better
clarified first; `fix.sql` would set it to `ACTIVE`.

## 2. Correct (self-guarding)

Pass the total row's `anzahl` as `expected`:

```bash
pgz eegfaktura -v expected=<anzahl> < fix.sql          # prod
pgz eegfaktura-pilot -v expected=<anzahl> < fix.sql    # pilot
```

The script updates inside one transaction and commits only if exactly `expected` rows changed. Otherwise (or
without `expected`) it stops with `ABBRUCH …` and an error (exit code 3); nothing is stored. Then run `check.sql`
again (an EDA answer may have changed a row in between) and repeat. Running it again changes nothing
(`expected=0`).

Changed per row: `last_process_state` = the current process state, process state `INACTIVE` or `ACTIVE`,
`"modifiedAt" = now()`, `"modifiedBy" = 'cleanup-2026-10-24'`.

## 3. Check again

`check.sql` returns `anzahl` 0 in the total row (`ohne_datum` and `aktiv_andere` stay).

## Undo

Count the rows to restore, then pass that count:

```bash
echo "SELECT count(*) FROM base.meteringpoint WHERE flag = 1 AND \"modifiedBy\" = 'cleanup-2026-10-24'
  AND process_state IN ('INACTIVE','ACTIVE') AND last_process_state IN ('INVALID','REJECTED')
  AND ((status = 'ACTIVE' AND last_process_state = 'INVALID')
       OR (status = 'INACTIVE' AND inactivesince IS NOT NULL AND activesince IS NOT NULL));" | pgz eegfaktura -At
pgz eegfaktura -v expected=<count> < rollback.sql      # pilot: eegfaktura-pilot
```

Current entries still marked `cleanup-2026-10-24` get their process state back from `last_process_state`
(`last_process_state` is cleared, `modifiedBy` = `rollback-cleanup-2026-10-24`). Entries changed since (a new
registration, or `flag 0` after a registration at another member) keep their newer state. Same guard as `fix.sql`.

Nothing else changes: status, `activesince`/`inactivesince`, `statusCode`, the notifications and the billing data
stay as they are. No message goes to the grid operator, no mail is sent.
