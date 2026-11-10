# Changelog

All notable changes to **eegfaktura-backend (Go REST/GraphQL API)** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/), and
versioning follows the deployment release tags. Detailed diffs stay in the `git log`;
this changelog highlights the changes relevant for overview and operations.

## [Unreleased]

## [1.18.0] – 2026-11-10

### Changed
- **Master data export ordered by member number** (08.11.2026): the sheet „Mitglieder“ of `GET/POST
  /eeg/export/masterdata` lists the members by member number — numbers numerically („2“ before „10“), then numbers
  with other characters alphabetically, members without a number last; a member's rows stay together. Before, the
  order of the participant query. Columns and values are unchanged.

### Changed
- **CI: no image build for a commit that only changes `CHANGELOG.md`** (platform#239): pushes and pull requests whose
  only change is the CHANGELOG, such as the release PR, skip the image build and the checks (PR checks, security
  scan, Snyk Code; the weekly schedules still run). Tag pushes still build. A release promotes
  the image of the last commit before the release PR; it has the same content.

### Fixed
- **Member without IBAN could not be created** (platform#248, A4): with „Kein SEPA“ (platform#143) web leaves the IBAN
  empty, but databases created before the migrations keep NOT NULL on `base.bankaccount.iban` (production), so
  `POST /participant` without `iban` failed with code 1104 (6 times in two weeks). A new bank account without IBAN is now
  stored with an empty IBAN instead of NULL — also the row a partial update of the bank data creates for a member
  without one; clearing the IBAN in a partial update stores an empty one, too. No schema change (decision 09.11.2026).
  billing prints and exports an empty IBAN like a missing one. A new member with direct debit (CORE, B2B) and without
  IBAN, which NOT NULL refused by chance before, is refused with **code 1112** „Für die Einzugsart CORE oder B2B braucht
  das Mitglied eine IBAN.“ (registration only; the Excel import already stored an empty IBAN and is unchanged).
- **Bank name without spaces** (platform#249): the bank name („Bankverbindung“, printed on the invoice) is saved without
  spaces around it — a single space, entered because the form required the field, made the invoice read
  „Bankverbindung:   | …“. Stored values stay until the member is saved again.
- **Clear messages for the community admin** (platform#248):
  - **Invalid e-mail address** (A1): new **code 1110** instead of 2000 on member create and update (also the partial
    update of `contact.email`), with the German text „Die E-Mail-Adresse „…“ ist ungültig.“ instead of „invalid email
    (…)“. The text is German also on the EEG (code 1002) and in the notifications of the Excel import and the mail
    sending.
  - **Metering point at another member** (A2, A3): new **code 1207** with „Der Zählpunkt … ist schon einem anderen
    Mitglied dieser Gemeinschaft zugeordnet. Verschiebe ihn dort zu diesem Mitglied oder melde ihn dort ab. …“ —
    `POST /participant` answers 400 / 1207 instead of the generic 1104, `PUT /meteringpoint/{pid}/create` 409 / 1207
    instead of 400 / 1111 with the database text; the Excel import names the case in its notification. The status
    codes follow the existing endpoints (registration errors are 400). Only `PUT …/create` logs the case as a warning.
  - **Mail refused by the mail server** (A5): the notification „Mailversand fehlgeschlagen“ puts a German explanation
    before the server's own text, which stays for finding the cause, e.g. „Der Mailserver hat sein
    Versandkontingent erreicht, die Mail wurde nicht verschickt. Bitte melde das dem Support. (…: 454 4.7.1 Quotas
    exceeded)“; also for a refused recipient address (x.1.1–x.1.6), a recipient mailbox that does not take the mail
    (x.2.x, e.g. full), a temporary (4xx) and a permanent (5xx) refusal. The log gets the explanation and the status
    code only, without the server text, which can name the recipient address.
  - **Import sheet missing** (A6): `POST /eeg/import/masterdata` with a sheet name that is not in the file answers
    **code 1064** with „Das Blatt „…“ gibt es in der Datei nicht. Trage unter „Sheet-Name“ den Namen des Blatts mit
    den Stammdaten ein.“ instead of 1052 „Bad Process“.
  Contract: codes 1110, 1112, 1207 and 1064 are new, an invalid e-mail no longer answers 2000; the callers are web
  (eegfaktura-web, shows these texts) and API clients.
- **Adding a metering point the member already has** (platform#247): `PUT /meteringpoint/{pid}/create` for a metering
  point that is stored at this member (e.g. inactive after a revoke) answered 400 / code 1111 „Bad Process“ with the
  raw key violation, which web showed as „E_1111_Bad Process“ (10 times from 07.11. to 09.11. in prod). Now it answers
  **409 / code 1206** with the German text „Dieser Zählpunkt ist bei diesem Mitglied schon vorhanden. Einen inaktiven
  Zählpunkt meldest du unter Prozesse → Zählpunkt aktivieren wieder an.“ and writes and sends nothing. A moved or
  archived row of the member (hidden from its list) gets a text that points to the support instead. Two requests at
  the same time get the same answer; the case is logged as a warning. Other save errors keep 400 / 1111. Contract:
  new status and code for this case (caller: eegfaktura-web).

## [1.17.0] – 2026-11-07

### Security
- **Inbound EDA messages** (backend 261105-mm5): security relevant — details not published.

### Added
- **Log events of the interval change** (eegfaktura-dev#54 #5): `event.action=eeg.settlement_interval.changed` (INFO)
  with `settlementInterval.old`, `settlementInterval.new` and `settlementInterval.effectiveFrom` (empty: at once);
  `eeg.settlement_interval.refused` with `code` and the reason in `event.reason` (INFO for a change the rule blocks,
  409; WARN for invalid input, 400); `eeg.settlement_interval.failed` (ERROR, 500). `tenant.id` from the checked
  request, no user name. A dry run logs only a refusal of invalid input (400), not its decision. If reading the EEG
  back fails after the change is stored, the answer is 200 with `eeg: null` and a note in `message`, logged as
  `eeg.settlement_interval.reload_failed` (WARN); before, 500 (1063) although the change was stored.

### Changed
- **Deregistering a metering point: inactive only after the grid operator's answer** (platform#238, covers
  platform#125). **Behaviour change for the EEGs:** sending a deregistration (`AUFHEBUNG_CCMS`) no longer sets the
  metering point INACTIVE; it stays as it is, the process history and the notification show the request. The answer
  decides: `ANTWORT_CCMS` 176 and `ABLEHNUNG_CCMS` 177 (no data release) or 180 (consent expired) set it INACTIVE,
  since the consent end of the EEG's request (found in the process history by tenant and conversation id; without it
  the consent end the answer carries, which eda-xp copies from its stored request; only then the day the answer
  arrives; logged as `eda.revoke.request_not_found`, `eda.revoke.request_without_end` or
  `eda.revoke.history_lookup_failed`). Only an ACTIVE metering point changes: a repeated answer, an earlier
  revocation by grid operator or member (CCMI/CCMC) or a new participation keep their status and date
  (`eda.revoke.unchanged`). A rejection with 82, 187, another or no code leaves the status; notification and history
  as before. Before, a rejected deregistration left the point INACTIVE. The member mail (platform#116) goes out on
  176 only and only when the point changed; none on 177/180 (decided 07.11.2026). Log
  `event.action=eda.revoke.inactive` (INFO, tenant, conversation id, codes, no personal data);
  `eda.revoke.date_mismatch` (WARN) when the answer carries another date than the request. Points already set
  INACTIVE by a rejected deregistration are not reset. Known limit: if neither the stored request nor the answer carries a date, today is used;
  then a late answer after a re-registration whose new participation started on or before today can still end it
  (logged as `eda.revoke.request_not_found` or `eda.revoke.request_without_end`).
- **`POST /eeg/settlementinterval`: a gap in the final periods blocks the change** (rule 6, eegfaktura-dev#54 #2):
  A month without a final period in the 12 months up to and
  including the last final month L (L-11 … L) blocks the change, before the other checks; the message names it, e.g.
  „Februar 2026 ist noch nicht endgültig abgerechnet.“ An older gap does not block: an allowed change adds a hint to
  its message, e.g. „Hinweis: März bis Mai 2024 wurden nicht endgültig abgerechnet.“ (at most five gaps named, then
  „und N weitere Zeiträume“) (rule 6, eegfaktura-dev#54 #2,
  decided 05.11.2026, window decided 06.11.2026 from prod data: without it 13 communities would be blocked, five of
  them for good by gaps in 2023/24; months before the first final period do not count).
- **`POST /eeg/settlementinterval`: an unreadable `billingState` is refused** (eegfaktura-dev#54 #9): an invalid
  value, the current value or a missing `billingState` is 400 (1060, 1061); so is (1060, eegfaktura-dev#54 #9) a
  `billingState` with a list missing or `null`, or with an identifier the rule cannot read (another form than billing's
  `Abr_(Y|YH|YQ|YM)-yyyy[-n]`, a period out of range, a year outside 2000–2100; the message names them, at most five,
  each cut to 40 characters), and (2000) data after the JSON body;
  before, such a state read as "nothing billed" and the change applied at once.
- **The start-up log names the migration result** (eegfaktura-dev#54 #4): one line `event.action=db.migrate.done` with
  `migration.versionBefore`, `migration.versionAfter` (0: no version) and `migration.applied` (0 with the message
  `database schema unchanged (no_change)`), so a run that skipped migrations is visible. A failed migration is one
  ERROR line `db.migrate.failed` (before: `log.Fatal` with the bare error, then nothing). The migrations run on a
  connection of their own, closed afterwards (before: one connection of the pool stayed held).
- **Migrations wait at most 60 s for a lock** (eegfaktura-dev#54 #1): the migration connection sets `lock_timeout`
  (60 s per lock wait of the connection, also the wait for the migration lock while the driver opens; golang-migrate's
  own wait for that lock in `Up` ends after 15 s). Before, a migration behind a long transaction or a
  `pg_dump` kept the start waiting with no log after `Start migration ...`, and the API down (one replica,
  `Recreate`). Now the start fails with `event.action=db.migrate.failed` and a message naming the lock wait and
  `pg_stat_activity` (detected by SQLSTATE 55P03); the pod restarts. A migration file that was started stays dirty:
  recovery in the README, section "Failed migration (dirty)". `error.message` of a failed migration is the first line
  of the error, at most 200 characters (golang-migrate's text holds the whole file).
- **A dirty migration version points to its recovery** (eegfaktura-dev#54 #3): one ERROR line
  `event.action=db.migrate.dirty` with `migration.version` and the pointer to the README section "Failed migration
  (dirty)", at start on a dirty version and when a migration file fails. Before, only golang-migrate's
  `Dirty database version … Fix and force version.`, which names a CLI that is not in the image and is wrong for an
  invalid index; that text is no longer logged (a failed file keeps its own error in `error.message`).
- **`POST /eeg/settlementinterval`: an unreadable body is a warning** (eegfaktura-dev#54 #12): 400 (code 2000) with
  the text „Die Anfrage konnte nicht gelesen werden (kein gültiges JSON)“ and `"error":"Bad Process"` like the other
  answers of the route (a body over 1 MiB: „Die Anfrage ist zu groß“); the decoder's text (Go type and field names)
  only in the WARN line. Before, ERROR and the
  decoder's text in the answer.
- **A refused GraphQL `updateEegModel` is logged** (eegfaktura-dev#54 #13): one WARN line
  `event.action=graphql.update_eeg_model.refused` with the reason (no admin group, tenant argument); the access line
  still says 200, the error is in the GraphQL answer. `EegModel.settlementInterval` is marked `@deprecated` in the
  schema with a description that it is ignored; the behaviour is unchanged (dropped, no error).
- **Consent revocations by grid operator or customer** (AUFHEBUNG_CCMC/CCMI, backend 261105-mm5): a revocation is
  applied only to metering points of the communities of the receiving RC number. A revocation that matches none of
  them changes nothing and sends no mail; it is recorded in the history of the receiving community, as an unmatched
  revocation was so far.

### Fixed
- **`ANTWORT_CCMS` 176 without a stored request no longer sets `inactivesince` to 01.01.1970** (platform#238): when
  the answer carried no consent end (eda-xp had no stored request), the date became 01.01.1970. Now the date
  follows the chain above, at the latest the day the answer arrives.
- **`ABLEHNUNG_CCMS` with a `meter` element keeps its reason codes** (platform#238): such a rejection was read like
  the echo of the EEG's own deregistration and got the code 99 („Meldung erhalten“); its codes from `responseData`
  were lost. Now the codes always come from `responseData`, the metering point too (the `meter` element only when
  it is missing there). eda-xp does not deliver such a rejection today.
- **GraphQL `updateEegModel` refuses a foreign tenant argument** (eegfaktura-dev#54 #6):
  A `tenant` argument or `EegModel.tenant` other than the `tenant` header (case-insensitive) is an error and nothing
  is written (eegfaktura-dev#54 #6); before, the argument was ignored and the write went to the header's community.
- **Rollout and recovery of migrations** (eegfaktura-dev#54 #1 #3), for the release notes of 1.16.0 (index
  `20261105110100`): **Rollout:** plan a short outage window outside the backup window: the backend runs one replica (`Recreate`) and
  migrates before it serves, so the API is down (502) while the migrations run or wait (eegfaktura-dev#54 #1).
  Right before, check for long transactions or a running `pg_dump`, which `CREATE OR REPLACE VIEW` and
  `CREATE INDEX CONCURRENTLY` wait behind (expected: no row older than a few seconds):
  `SELECT pid, state, now() - xact_start AS age, left(query, 60) FROM pg_stat_activity WHERE datname =
  current_database() AND xact_start IS NOT NULL AND pid <> pg_backend_pid() ORDER BY xact_start;`. Run the release
  outside the backup window (`pg_dump`). A lock wait longer than 60 s ends the migration (see Changed: lock
  timeout) and the backend exits. A timeout before a migration file starts (the migration lock, the version check)
  changes nothing: the restart simply tries again. A timeout inside a file leaves its version dirty, and the pod
  keeps restarting until it is recovered by hand as in the README, section "Failed migration (dirty)"; a restart
  alone does not heal it. If the pod was stopped
  while the build of `metering_partition_factor_tenant_idx` waited (eegfaktura-dev#54 #3), PostgreSQL may finish the build anyway: `dirty = true` with
  `indisvalid = true`; then only `UPDATE base.schema_migrations SET version = 20261105110100, dirty = false` and
  restart. All cases: README, section "Failed migration (dirty)".
- **Rollback without the migrate CLI** (eegfaktura-dev#54 #14), for the three migrations of backend#145/#146 (`20261105110000`,
  `20261105110100`, `20261105120000`), by hand without the migrate CLI (not in the image): the schema can stay, an
  older image (1.15.x) works with the view, the index and the table. It does not start, though, while
  `base.schema_migrations` holds a version it does not know (golang-migrate: `no migration found for version …`), so
  before deploying it: `UPDATE base.schema_migrations SET version = 20261028130000, dirty = false;`. Rolling forward
  again runs the three files again (`CREATE OR REPLACE VIEW`, `IF NOT EXISTS`); the records in
  `base.settlement_interval_change` stay. Taking the schema back too is not needed; if wanted, after the version
  reset: `DROP INDEX CONCURRENTLY IF EXISTS base.metering_partition_factor_tenant_idx;` and the content of
  `20261105110000_activemeteringpartition_tenant.down.sql`; `DROP TABLE base.settlement_interval_change` would delete
  the records of the interval changes. 1.16.0 also holds the P2P/EVA migrations (`20261106090000`, `20261106100000`):
  a rollback behind them follows the README, section "Rollback to an older image".
- **Two api tests pass when run again** (tracking backend 261105-mm6): `TestAddTariff_joiningFee` removes the tariff it
  creates, `TestRequestMeteringPointValues` the energy request rows it stores; `go test -count=2` failed on both.

## [1.16.0] – 2026-11-06

### Security
- **Base image `debian:trixie-20261027-slim`** (published 2026-10-28) instead of `trixie-20261020-slim`: current Debian security updates of the runtime image.

### Added
- **Start-up line and `deployment.environment`** (dev#45 L14, Q5): one line `service start` (`event.action=service.start`)
  with version and effective log level (`log.effective_level`), written by `logging.Setup` whatever `LOG_LEVEL` says, no
  secrets. `deployment.environment` on every line
  from the new optional env `DEPLOYMENT_ENVIRONMENT`; the field is left out when unset.
- **Never-log test** (dev#45 L11, `logging/neverlog_test.go`, no new dependency): type-checks the source and fails on a
  struct or `http.Header` printed whole in a log call (verb, bare argument, field value), `%+v`/`%#v` of an interface,
  and names of personal data or secrets among the identifiers of log calls, split into words (camelCase, snake_case) and
  compared as whole words with the list common to all services (also with a plural s; in an all lower-case identifier a
  list word of five or more letters also inside it), and a list word right before a format verb (`password=%s`); other
  string literals are not scanned, numbers, booleans and errors are skipped by type (dev#45 Q1). An
  exception needs an entry, keyed by file and finding, with its reason in `neverLogAllowed`. `-short` skips the scan.
- **EEG admins change the settlement interval** (konzept-abrechnungsintervall.md, platform#230; caller: eegfaktura-web):
  `POST /eeg/settlementinterval` (EEG_ADMIN, tenant from the checked header) with `settlementInterval`, `dryRun` and
  `billingState` (billing's `settlementState`, passed on by web). The rule: without a final period the change applies
  at once unless a run is running; otherwise from the month after the last final period, if a period of the new interval starts there, and no
  run is running for a period ending then or later. Answer 200 `{allowed, effectiveFrom, lastFinalPeriod, message,
  earliestFrom, eeg}`; a blocked change without `dryRun` is 409 (code 1062) with the reason; an invalid value, the
  current value or a missing `billingState` is 400 (1060, 1061). The change runs in one transaction on the locked
  `base.eeg` row and is recorded with user, time, old and new value in the new table `base.settlement_interval_change`
  (migration `20261105120000_settlement_interval_change`, down drops the table).
- **Community types P2P and EVA** (Peer-to-Peer, Eigenversorgung; konzept-buergerenergie-eigenversorgung-p2p.md,
  vfeeg-development/eegfaktura-platform#229 part 2): `area` knows `LOCAL`, `REGIONAL`, `BEG`, `GEA`, `P2P`, `EVA`, kept in
  one list (`model.AreaTypes()`, `model.ValidateArea`, checked in `insertEeg` for both registrations). P2P and EVA
  run centrally over ebUtilities like the BEG and may span several grid operators (konzept section J, 05.11.2026):
  one rule `model.HasMultipleGridOperators` (BEG, P2P, EVA) replaces the BEG checks — messages go to the grid
  operator of the metering point (the community's when the metering point has none; without either nothing is sent),
  the metering point list is requested from the chosen grid operator (one of the community's, see Fixed). The RC number
  stays unique (only GEA is excepted).
  **Contract:**
  - gRPC `register.proto` `GRID_AREA`: `P2P = 4`, `EVA = 5` appended (caller: eegfaktura-admin-backend, keep its copy
    the same). A value the backend does not know (its name is the number) is refused with reply status 400 and no
    gRPC error, like 409; nothing is written. Before, it was stored as the number.
  - MQTT `register-eeg` (`registration.schema.json`): the `area` enum gets `P2P`, `EVA` (decode refuses other values;
    `insertEeg` checks the same list).
  - MQTT `eda/request` `ecType` (ANFORDERUNG_CPF, to eegfaktura-eda-xp): field unchanged, the new values pass through.
  - View `base.billing_masterdata` (caller: eegfaktura-billing): new last column `eeg_area` (text, = `base.eeg.area`);
    all other columns keep name, type and order. Migration `20261106090000_billing_masterdata_eeg_area`
    (`CREATE OR REPLACE VIEW`; down restores the view as after `20261028130000_joining_fee`). No data change.
  - `GET /eeg` `area` (web, admin portal): the new values, no new field.
  **Deploy order:** eegfaktura-eda-xp → backend → eegfaktura-billing, eegfaktura-admin-backend → eegfaktura-admin,
  eegfaktura-web → operator SQL (PP100784, PP102502 from LOCAL and PP102524 from BEG to P2P). Backend before
  admin-backend: otherwise a P2P registration arrives as an unknown number; backend before billing: the new billing
  reads `eeg_area`. **Rollback** (README, *Rollback to an older image*): the older image does not start on the
  migrated database (`no migration found for version 20261106100000`), so the migrations go down **before** it starts.
  Order: revert the operator SQL, roll back eegfaktura-billing#103 (it reads `eeg_area`), migrate down with the
  new backend stopped (`migrate … down 2`; also behind backend#145/#146 `down 5`), then start the older image. The
  `migrate down 2` of backend#146 alone no longer fits once this release is in.
- **Nahebereich and TechCode/FuelCode from the metering point list** (konzept section J, platform#229): new columns
  `base.eeg.ec_zone_level` (1–7, CHECK, empty = unknown) and `base.meteringpoint.tech_code`, `fuel_code` (text).
  Migration `20261106100000_ec_zone_level_tech_fuel_code` fills `ec_zone_level` once from `area` (GEA 1, LOCAL 3,
  REGIONAL 4, BEG 7; P2P/EVA stay empty until the first list), a new registration gets the same value
  (`model.DefaultEcZoneLevel`); down drops the three columns.
  `base.billing_masterdata` is unchanged. Only a metering point list (`SENDEN_ECP`) sets them; `ABSCHLUSS_ECON`
  carries zone and type as well, but the backend does not read them there (eegfaktura-dev#56 #11). The list sets the
  Nahebereich at every tenant of its community id (an absent value keeps the stored one, an invalid one is dropped
  with a warning) and the codes at the metering points of these tenants (an empty code never overwrites). ECType
  `RC_L`/`RC_R` sets `LOCAL`/`REGIONAL` at the tenants of the community id that have the other of the two (log event
  `eeg.area.updated_from_list` with old and new value); the switch sets their Nahebereich to the list's
  `ECZoneLevel`, or to empty when the list has none (eegfaktura-dev#56 #10; the old type's level would not fit). Any
  other ECType that does not fit the community type (GEA GC, LOCAL RC_L, REGIONAL RC_R, BEG CC, P2P PP, EVA SC) only
  logs a warning and changes nothing (eegfaktura-dev#56 #16). All three values are read only, and the generic updates
  still drop `area`. Hardening of inbound metering point lists: security relevant — details not published.
  **Contract:**
  - MQTT from eegfaktura-eda-xp (EC_PODLIST): optional `ecZoneLevel` (number), `ecTypeEda` (text, EDA code as
    received) and per `meterList` entry `techCode`, `fuelCode` (text); an old backend ignores them, an old eda-xp
    does not send them. eda-xp's copy `src/test/resources/contract/backend/mqtt.go.txt` follows `model/mqtt.go`.
  - `GET /eeg`: new field `ecZoneLevel` (number or null); metering points: `techCode`, `fuelCode` (text or
    null). Callers: eegfaktura-web.
  **Deploy order:** eegfaktura-eda-xp → backend (migration on start) → eegfaktura-web.

### Changed
- **An ERROR message of eegfaktura-eda-xp is logged with its reason** (eegfaktura-dev#56 #4): one ERROR line with
  `event.action=eda.error.received`, the tenant, the code, the conversation in the text and eda-xp's `errorMessage`
  as `error.message`. Before, the reason was dropped. A refused request (e.g. a CPF with an unknown `ecType`) still
  answers 204 and leaves no process history; whether the community sees it is a follow-up.
- **Log lines of the inbound metering point list and of a refused registration carry `event.action`**
  (eegfaktura-dev#56 #12): `eda.podlist.no_community_id`, `eda.podlist.community_check_failed`,
  `eda.podlist.foreign_community` (with the community id as `ecId`), `eda.podlist.zone_level_dropped`,
  `eda.podlist.ectype_mismatch`, `eda.podlist.code_dropped` (no metering point number, the conversation instead),
  `eeg.area.update_from_list_failed`, `eeg.register.refused`.
- **`area` and `allocationMode` are dropped silently by the generic EEG updates** (platform#229, E5): `POST /eeg`, gRPC
  `AdminEegService.updateValue` (class EEG) and GraphQL `updateEegModel` (no field for them; it maps only
  `sepaActiv` and `settlementInterval`, the latter dropped) no longer change them, like `settlementInterval`. They
  are set at registration, otherwise only by the operator; a P2P/EVA community can no longer switch itself to an
  EEG type and around the billing block. No current caller changes them (web shows
  `allocationMode` read-only and has no field for `area`; the admin tool edits only the settlement).
- **`settlementInterval` is dropped silently by the generic EEG updates** (konzept-abrechnungsintervall.md): `POST /eeg`
  and gRPC `AdminEegService.updateValue` (class EEG, caller: eegfaktura-admin-backend for the admin tool) no longer
  change it, like the other protected fields; it changes only through `POST /eeg/settlementinterval`. Registration
  still sets the initial value. GraphQL `updateEegModel` drops it the same way (see Fixed).
  **Deploy order:** release eegfaktura-admin#79 (removes the edit field of the admin tool) together with or before this
  backend; otherwise the old admin tool reports success over gRPC class EEG while nothing changes.
- **`request.id` and `tenant.id` on the lines of an HTTP request** (dev#45 L12), no longer on the access line only;
  `trace.id` and `span.id` come along. In a request `tenant.id` is only what passed the tenant check, a tenant field
  of the line does not count. Not on: the notification DAO (`SaveNotification`, `SaveNotificationFromMap`,
  `SaveHistory`), `SendMail`, the HTML mail parser and the lines of the MQTT send loop. No propagation over MQTT or
  gRPC.
- **One owner for the correlation fields** (dev#45 Q4): `request.id`, `trace.id`, `span.id` and `tenant.id` are written
  by the context handler only, one key each; the same keys set on a line or through `With` are dropped (outside a
  request a tenant field still counts, checked as above).
- **Token check failures** (dev#45 Q6) log the error as `error.type`/`error.message` under a fixed message
  (`token verification failed`, `no access token in the request or invalid Authorization header`) instead of the field
  `error=JWT-Token` and the error in the text.
- **Logging tests** (dev#45 Q2): one capture helper `logging/logtest` restores slog, logrus and the `log` package.
- **Generated code is no longer tracked** (BE-CQ-23): `proto/*.pb.go` and `model/registration_model.go` are generated
  by `make tools generate` (protoc on the `PATH`, generators at the `go.mod` versions); CI, the Dockerfile and the
  README use it. A fresh clone did not compile before. The image build no longer runs gqlgen (`graph/generated` stays
  tracked).
- **Test helpers out of the service binary** (BE-CQ-30): Testcontainers, the Docker client and go-sqlmock are no
  longer linked into it (`database/dbtest`, `database/dbmock`, test-only). Container tests: README notes
  `go test -p 1 ./api ./database ./eda` for a shared Docker.
- **Cleanup** (code-quality review 2026-11-03): removed unused code and files — `MigrateDatabase`, the `repository`
  package, `UpdateTariff`/`OpenDbXConnection`, `GetTemplateFor`, unused response helpers in `api/common.go`
  (`respondWithData` is `respondWithJSON`), unused middleware types (`BackendClaims`, `Authenticate`, `ClientCreds`,
  `HasRole`, the superuser context key), `graph/gmodel/rate.go`, two unused error variables, the blank pgx import,
  commented-out code in `server.go` and the DAOs, the unread `migration.sql`, `schema.hcl`,
  `database/migration/000001_init-setup.up.sql` and config key `file-content.basedir`; stale Makefile targets
  (`atlas-hash`/`atlas-diff` spelled right). The test seed data is `database/testdata/seed.sql`.

### Fixed
- **Inbound metering point list** (backend 261105-mm4): security relevant — details not published.
- **Metering point list request only to a grid operator of the community** (eegfaktura-dev#56 #7):
  `POST /eeg/sync/participants/{oid}` for BEG, P2P and EVA takes `{oid}` trimmed and upper-cased and sends only when it
  is the grid operator of one of the community's metering points or the community's own; blank or another one answers
  400 (code 5000) and sends nothing. Before, `{oid}` went out as given (lower case, blank or any operator, answer 204).
  BEG had the same behaviour before this release and is fixed with it (same code path). A refusal of this route,
  also a missing grid operator of an EEG, now answers 400 instead of 500. Caller eegfaktura-web offers only these
  operators.
- **Saving a metering point answers with the stored TechCode/FuelCode** (eegfaktura-dev#56 #14):
  `PUT /meteringpoint/{pid}/update/{mid}` never stores them; its answer echoed the codes the client sent and now
  carries the stored ones (empty when none or no row was updated). eegfaktura-web sends the codes unchanged and keeps
  showing them, now from the stored values.
  `PUT /meteringpoint/{pid}/create` (register) likewise no longer echoes them: a new metering point stores no codes, so the
  answer carries none (eegfaktura-dev#56 #14, register).
- **Confirming a member sends nothing when a grid operator is missing** (platform#229): every metering point's
  receiver is determined first; one missing answers 400 before the member is confirmed.
- **gRPC registration refuses an unknown allocation or settlement interval** (found in the review of backend#147): a
  `GRID_ALLOCATION` or `EEG_SETTELMENT` value the backend does not know was stored as its number. Now reply status
  400 without a gRPC error, nothing written (admin-backend treats every status but 201 as failed).
- **gRPC registration refuses an unknown legal form** (found in the review of backend#147): an `EEG_LEGAL` value the
  backend does not know was stored as its number. Now reply status 400 without a gRPC error, nothing written.
- **Metering point master data of larger communities within the deadline again** (support ticket 6611200): the
  view `base.activeMeteringPartition` numbered the partition factors of all communities before the community filter
  of the query applied; as the inner side of a nested loop (chosen since 1.13) it did so once per metering point, and
  `MasterData_MeteringPoint` (gRPC, caller eegfaktura-energystore) ran past its 5 s deadline
  (`error fetch Meteringpoints`). Migration `20261105110000` partitions the window by `tenant` as well, so the filter
  reaches the table: same columns, same rows (a member belongs to one community). Migration `20261105110100` adds
  the index `metering_partition_factor_tenant_idx` (`CONCURRENTLY`). The members list (`completeParticipants`) now
  also filters the view by tenant. No data change; no contract change.
  **Rollout:** outside the backup window — check `pg_stat_activity` for long transactions or a running `pg_dump`
  first: `CREATE OR REPLACE VIEW` and `CREATE INDEX CONCURRENTLY` wait behind them (old pods keep serving). After the
  rollout `pg_index.indisvalid` of the new index must be true. If the index build failed or the index is invalid:
  `DROP INDEX CONCURRENTLY IF EXISTS base.metering_partition_factor_tenant_idx`, then by hand
  `UPDATE base.schema_migrations SET version = 20261105110000, dirty = false` (the migrate CLI is not in the image),
  then restart. Never only reset the version: `IF NOT EXISTS` would keep the invalid index.
- **GraphQL `updateEegModel` stores again** (tracking backend 261105-mm2): it returned the changed EEG but never stored
  anything, also not `sepaActiv`. It now goes through the guarded update of `POST /eeg` (`updateEegPartial`, protected
  fields dropped, `settlementInterval` included) for the tenant of the checked request, and returns the EEG read back.
  `updateEegModel` requires the EEG admin group like `POST /eeg`; the other GraphQL operations stay in log-only mode.
  No known caller (web's `updateEegModel` calls `POST /eeg`).
- **A failed COMMIT is reported as an error** (BE-CQ-7, BE-CQ-8): registering and moving a metering point, adding a
  tariff and activating metering points from an EDA message reported success when the COMMIT failed. Activating
  metering points also went on to COMMIT after a failed step instead of rolling back. One helper (`inTx`) now runs the
  transactions of the DAOs; a failed BEGIN in them is `ErrOpenTx` (code 998) — before, some wrappers returned the raw
  driver error. Registering a metering point, adding a tariff and activating metering points do not take the cancel
  of the caller's context, as before.
- **`POST /participant`: a failed registration answers with a code** (caller: eegfaktura-web): code 1104 and a
  generic message, as the member update does; not found and invalid input keep their own code and text (an invalid
  e-mail address: code 2000 with the reason). Before, the body was `{}` (the error was joined with the rollback's and
  lost its type). No database text goes to the client.
- **No names or cell values in the warn lines of the Excel import** (dev#45 L1). The import result shown to the user
  is unchanged; the lines no longer repeat names, status, energy direction or community id of the row.
- **The raw `tenant` header stays out of the log** (dev#45 L2): a failed check logs `tenant check failed`. A superuser
  skips the check, so `tenant.id` takes only a value that passes `^[A-Z0-9][A-Z0-9_-]{0,63}$` after upper-casing, on
  every line; the tenant value is out of the message texts. A refusal for a missing admin group now carries the checked
  `tenant.id`, also on its access line.
- **No debug print of field values** in `util.ConvertMapToStruct` (dev#45 L11, found by the never-log test).
- **`tenant.id` outside a request is written upper-cased** (dev#45 Q3), as it is checked; before, `te100100` passed
  the check and was written as given.
- **A handler panic after a flush aborts the connection** (dev#45 Q7) instead of a `WriteHeader(500)` on a response that
  had already begun.

## [1.15.1] – 2026-11-02

### Fixed
- **Partition factor changes** (backend 261005-ca4): security relevant — details not published.

## [1.15.0] – 2026-11-02

### Changed
- **The tax number of a member is out of the master data Excel** (decided 2026-11-01). The sheet „Mitglieder“ of the
  export no longer has the column „SteuerNr.“, so the columns after it move one to the left; the import ignores the
  column `SteuerNr`. There is no field for it in the app, and documents carry the UID. Stored numbers stay in the
  database and the billing view; the tax number of the community itself is unchanged.

## [1.14.0] – 2026-10-31

### Added
- **Health endpoints for Kubernetes probes:** `GET /healthz` (liveness, always `ok`, no dependency checks) and
  `GET /readyz` (readiness, `ok` when Postgres answers `SELECT 1`, otherwise 503 `unavailable`), served
  on the main HTTP port without a bearer token. The public ingress blocks `/api/healthz` and `/api/readyz`
  (vfeeg-development/eegfaktura-gitops#159); kubelet probes reach the pods directly. They answer only
  `ok`/`unavailable`. `/readyz` pings on its own short-lived connection outside the
  application pool (driver timeouts: connect 2 s, statement 2 s); at most one ping runs at a time and calls within
  1 s share its result, so callers cannot multiply the database load. If Postgres refuses the ping with
  `too_many_connections` (SQLSTATE 53300), `/readyz` still answers `ok` and logs a warning at most once a minute: the
  application pool can still serve, and saturation of the shared Postgres must not take the service out of service.

### Changed
- **Every mail send waits 30 s for the mail server** (261030-mm7): before 5 s (30 s only above 256 KB of files;
  the metering point list mail always 5 s). One constant `services.MailSendTimeout` for all sends. Before the
  request, the backend now waits up to 5 s for the connection to the mail server; a server that does not get ready
  in that time is a failed send. A slow mail server therefore holds the test mail of the mail editor up to 35 s
  instead of 5 s, and the mail worker (next entry) up to 35 s per mail.
- **Mails from the EDA handlers go through a background worker** (261030-mm7, user decision 2026-10-31): the activation,
  zp-active, zp-inactive and metering point list mails were sent inside the single MQTT broker loop, so one slow mail
  held every EDA message and API-triggered EDA request behind it. One in-process worker now sends them one at a
  time, in order, after the handler's writes (`services.MailQueueSize` 1000 jobs, no new dependency or table). A GEA
  zp-inactive mail is one job, so a member still gets it once. Results are reported as before, only later.
  **In normal operation members see no change; during a relay outage their mails arrive later:** a job can take up to
  35 s, so 100 queued mails can take about an hour (with the eda-xp#60 timeouts usually less) and nothing goes out
  in between. A full queue blocks nothing: the mail is not sent, and the community gets an error notification "Die
  Mail wurde nicht verschickt, weil gerade zu viele Mails anstehen oder der Dienst neu startet. Bitte später noch
  einmal versuchen." plus an error log line. On SIGTERM the server gives running requests 8 s (the rest is cut off and
  logged), waits up to 2 s for the broker handler, sends what is queued for up to 14 s (`services.MailStopLimit`) and
  reports what is left the same way for up to 3 s (`services.MailReportLimit`), all before the database closes. The
  pod has 30 s.
  **The queue is in memory:** a crash or kill loses the queued mails, without a notification and without a retry; the
  mail being sent then has no known result. There are no retries.
  **Rollout: eegfaktura/eegfaktura-eda-xp#60 before this release.** With an old eda-xp, which has no SMTP timeouts, a
  relay hanging before DATA runs into the backend's 30 s and reads as "vermutlich verschickt", although nothing was
  delivered.
- **Log output is now one JSON object per line on stdout, with ECS field names** (logging concept W-261008-ca2).
  **This changes what operators, `kubectl logs` and Dynatrace receive:** before, logrus text (`time=… level=… msg=…`)
  on stderr; now e.g. `{"@timestamp":"…Z","log.level":"info","message":"…","service.name":"eegfaktura-backend",
  "service.version":"<commit>","tenant.id":"…"}` on stdout. Parse rules or dashboards on the old text format must be
  redone. One logging module (`logging/`, Go `log/slog`) writes every line; the existing logrus calls are routed into it
  unchanged in content. Field mapping: logrus `tenant` → `tenant.id`, `WithError` → `error.type` + `error.message`, the
  caller → `log.origin.function`/`log.origin.file.name`/`log.origin.file.line`. Only listed fields are written
  (`logging/logging.go`, `allowedFields`): a field a log call adds that is not on the list is dropped — the list holds
  the fields the existing calls use. No new dependency.
  - `LOG_FORMAT`: `json` (default) or `text` (`key=value`, for local runs).
  - `LOG_LEVEL`: `trace`, `debug`, `info` (default), `warn`, `error`. **An unknown value now stops the start**
    (exit code 2, message on stderr) — this includes `fatal` and `panic`, which logrus accepted before; before, an unknown value fell back to `info` with a warning. An empty `LOG_LEVEL` or
    `LOG_FORMAT` is the default (same rule as energystore). Prod/pilot set `info`.
  - `SERVICE_VERSION` → `service.version`: set in the image (Dockerfile `ARG`/`ENV`, passed by the release workflow: the
    commit, or the version for a `v*` tag build); `dev` otherwise. A promoted prod/pilot image is the same digest
    re-tagged, so it keeps its commit: the release version is the image tag, not `service.version`.
- **Access line: one line per HTTP request** (`event.action=http.access`), written after the token check: method,
  route template (`http.route`, e.g. `/eeg/tariff/{id}` — no ids, no query), status, `event.duration` (nanoseconds,
  ECS), `event.outcome` (`success` for 2xx/3xx, `refused` for 401/403 at level warn, `failure` for any other status ≥ 400),
  message `<method> <route> <status>`, `request.id`,
  `tenant.id` (the tenant the request was allowed for; absent on refusals), `client.id` (the token's `azp` — the
  Keycloak client, not a person) and, on `/query`, `graphql.operation.name`. `trace.id`/`span.id` when the request
  has a W3C `traceparent`. **No user id, user name, e-mail, user agent, IP or query value** (no user tracking,
  2026-10-29); a test guards the field list. `/healthz` and `/readyz` are not logged. `X-Request-Id` is taken from
  the request when it is 1–128 characters of `[A-Za-z0-9._:-]` (ingress-nginx sends one), otherwise generated (UUID v4 as
  32 hex characters), and returned in the response header. `traceparent` is checked strictly (no version `ff`, no
  all-zero ids). The former per-request line `Access granted for tenant …` of `/query` is
  replaced by the access line. The backend had no access log with IP or user agent before.
  Requests that match no route (404) get an access line and `X-Request-Id` too, without `http.route` (message
  `GET - 404`); the backend's router also answers a wrong method with 404. An anonymous GraphQL operation has no
  `graphql.operation.name`. **Known limit:** GraphQL answers a refused or failed operation with HTTP 200 and an
  `errors` array; its access line says `event.outcome=success` — the outcome reflects the HTTP status only (same in
  energystore). More known limits (follow-ups, unchanged now): CORS preflights (`OPTIONS`, answered by the CORS wrapper
  outside the access log) get no access line and no `X-Request-Id`; `X-Request-Id` is not an exposed CORS header and
  `X-Request-Id`/`traceparent` are not in the allowed request headers, so browsers cannot send or read them; the
  GraphQL websocket transport on `/query` is logged as one line with status 200 and the socket's lifetime as duration,
  and operations on one socket share the operation name.
- **A panic in a REST handler** is logged as one error line (`event.action=http.panic`, `error.type`, `error.message`,
  `error.stack_trace`) and answered with 500; the access line says 500. Before, net/http logged `http: panic serving
  <remote address>` with a plain stack (an IP in the log) and there was no access line. If the response had already
  begun, the connection is aborted.
- **A panic in a GraphQL resolver is logged as one error line** (`event.action=graphql.panic`, `error.type`,
  `error.message`, `error.stack_trace`) through the logging module, instead of gqlgen's plain-text stack on stderr. The
  client gets the same generic `internal system error` as before.
- **The temporary line `gql-admin-check: non-admin token` no longer logs the user id (`sub`)** (no user tracking,
  2026-10-29). It now holds `tenant.id`, `client.id`, `graphql.operation.name`, `operation_type`, `superuser` and
  `access_groups.class` (`user`/`none`) instead of `azp`, `sub`, `access_groups_count`, `is_user` and `operation_name`;
  the count of non-admin GraphQL calls stays available.

### Removed
- **Offline registration of metering points (`ANFORDERUNG_ECOF` with activation code)** — the grid operators no
  longer support it (decision 2026-10-30; behaviour change towards the grid operators). The backend no longer sends
  `ANFORDERUNG_ECOF`. On `PUT /meteringpoint/{pid}/create`, `POST /meteringpoint/{pid}/register` and
  `POST /participant/{id}/confirm` a missing or empty `activationMode` now means `ONLINE` (before: `register`
  answered 400 code 1204; `create` for an active member of an online EEG did the same, but only after the metering
  point was saved; `confirm` sent an offline registration). For an **online EEG**, `activationMode: "OFFLINE"` is
  answered with 400, code **1205**, message "Offline-Registrierung wird von den Netzbetreibern nicht mehr
  unterstützt"; any other unknown value with 400, code 1204. In both cases nothing is written and nothing is sent —
  on `confirm` the whole request is checked before the participant is confirmed; on `create` only a NEW metering
  point of an active member is checked (other metering points are not registered, so the mode is ignored, as
  before). **Non-online EEGs** never send a registration and keep ignoring the mode on all three routes.
  eegfaktura-web has no text for code 1205 and shows the raw key
  `E_1205_Offline-Registrierung wird von den Netzbetreibern nicht mehr unterstützt`; web 1.19.0 always sends `ONLINE`,
  so only a tab still open on an older web gets it (**roll out web 1.19.0 first**). `activationCode` is no longer read (removed from
  the request models; a client that still sends it is not rejected). Inbound `ANTWORT_ECOF`, `ZUSTIMMUNG_ECOF`,
  `ABLEHNUNG_ECOF`, `ABSCHLUSS_ECOF` and `ABBRUCH_ECOF` are still processed, so offline registrations sent before
  this release still complete. Web counterpart: eegfaktura/eegfaktura-web#246.

### Contract
- `DELETE /participant/v2/{id}`: new answer 409 with code 1109. Caller: eegfaktura-web, which shows its own text with
  the matching version; an older web shows its general error message. No rollout order needed.
- gRPC `RegisterEegService.register`: `RegisteredEegReply.status` can be **409** (tenant exists, no gRPC error). Caller:
  admin-backend, which stops the registration on it (admin-backend PR of 261015-ca1). The proto is unchanged. Rollout
  order: admin-backend, then backend, then admin. A new backend with an old admin-backend treats 409 as a failed
  registration, without a Keycloak change, but still writes Ponton for the existing community.
- REST `/eeg/tariff` (and the tariff history): `baseFee` can carry decimals, in the answer and on save (number or
  string, as before). Whole amounts are unchanged. Web has no input for it and no display reads it (`TariffHelper`'s
  `amountInEuro` has no caller), so there is no functional effect there. Billing reads the view, unchanged.
- **Mail server answer status 202 = unconfirmed** (261030-mm7, with eegfaktura/eegfaktura-eda-xp#60, decided
  2026-10-30): the xp-connector answers `SendMail` / `SendMailWithInlineAttachment` with status 202 ("SMTP
  unconfirmed: …") when its SMTP read timeout hit after the mail body was sent (the relay probably accepted it). The
  backend treats 202 like its own timeout: `ErrMailUnconfirmed`, notification code `W_MAIL_UNCONFIRMED`, test mail 504.
  200 = sent; every other status stays a failure with the server's message, including 504 ("SMTP timeout: …",
  nothing delivered).
- **`activationMode` on `create`/`register`/`confirm` (see Removed):** callers are eegfaktura-web (before 1.19.0 it
  offers the offline checkbox and sends `OFFLINE` + `activationCode`; 1.19.0, eegfaktura-web#246, always sends `ONLINE`).
  eegfaktura-admin, eegfaktura-admin-backend and eegfaktura-eda-xp do not call these routes. Tests:
  `api/offlineRegistration_test.go`, `eda/edaEcofInbound_test.go`.
- **New caller of `GET /participant`: eegfaktura-energystore** (energystore 261025-ca1). energystore forwards a
  member's bearer token (header `tenant`, once per tenant of the token) and reads `meters[].meteringPoint`, `meters[].status` (`ACTIVE`/`INACTIVE`/`INIT`/…, absent when empty), `meters[].registeredSince` and
  `meters[].participantState.activeSince`/`inactiveSince` (yyyy-mm-dd or null) to find the member's own metering
  points and their participation windows. A change to these fields, the route or its status codes (200 with a JSON
  array) is a cross-component change. Contract test: `model/participant_contract_test.go`.

### Security
- **CI: the release workflow pins its actions by commit SHA** (eegfaktura-dev tracking W-261028-mm6). `rolling-release.yml`
  builds and pushes the shipped image, but used floating tags (`@v4`, `@v3`, …); a moved tag ran in the job that holds
  the push credentials. Now every action is pinned to a commit, the same SHAs `pr-checks.yml` and `security-scan.yml` already
  use where the action is the same, otherwise the commit the floating tag pointed to on 2026-10-28.
- **Keycloak sample configuration** (tracking 1): security relevant — details not published. The `keycloak.json` in
  the repository is now a sample. Deployments that mount their own file via `KEYCLOAK_CONFIG` are not affected; a
  local run without `KEYCLOAK_CONFIG` needs its own `keycloak.json` (see README, not to be committed).
- **excelize pinned to `v2.11.1-0.20261003002531-6258dcebc4e2`** (commit 6258dceb of 2026-10-03, pseudo-version up to
  v2.12.0; the latest release v2.11.0 of 2026-07-06 lacks the fixes). Closes the Dependabot alerts GHSA-mx22-3794-2vpv,
  GHSA-jw42-f3rr-4cc3, GHSA-r7x5-4969-99p7, GHSA-x2q3-8cjh-766f, GHSA-wp2g-vpjj-g53r, GHSA-2j4c-ffch-9f23,
  GHSA-c85p-xxjj-2r75, GHSA-fq3v-74gv-27gm, GHSA-jrfj-fhj2-jjvm (high) and GHSA-8mcq-6wmr-jrjv, GHSA-5h23-36rv-pm65,
  GHSA-fw94-4wwp-w8pw, GHSA-g27h-8qhm-6pff, GHSA-rxcj-4pj5-74gr, GHSA-8jjq-8j9w-m2v6 (medium): panics, endless loops
  and unbounded allocations on crafted workbooks — relevant for the master-data Excel import. Indirect modules moved
  with it: `xuri/efp` v0.0.1 → v0.0.2 (2026-09-21), `richardlehane/mscfb` v1.0.7 → v1.0.8 (2026-08-19); `go.sum` also
  records `golang.org/x/image` v0.44.0 (2026-07-08, excelize test-only). No change to import or export output.
- **Bearer tokens: RS256 only** (platform#191). Both go-oidc verifiers took the allowed signing algorithms from
  the Keycloak discovery (HS* and `none` were already dropped); they now accept RS256 only, as required.
  - App verifier (`Protect`, `GQLProtect`, `ConditionProtect`): a client-supplied bearer token signed with
    RS384/RS512/PS*/ES*/EdDSA under a realm key was accepted; it is now rejected (401).
  - API verifier (`ProtectApi`): it only checks the ID token the backend fetches itself via password grant, so a
    client cannot choose its algorithm. Effect: if the ID-token signature algorithm of the api client in
    Keycloak were ever set to something other than RS256, every `ProtectApi` call would fail with 403.
  - The Keycloak realms sign with one RS256 key, so logins are not affected.
- **JWT library: `github.com/golang-jwt/jwt` v3.2.2+incompatible → `github.com/golang-jwt/jwt/v5` v5.3.1**
  (platform#191). v3 is unmaintained (GO-2025-3553, no fix in v3). The library only decodes the token claims;
  token validation (signature, `iss`, `exp`, `nbf`) is done by go-oidc and is unchanged by this entry (for the
  allowed algorithms see "RS256 only" above). Two differences from the claim decoding: a token with `aud` as an
  array and a fractional `exp` now decode and reach the tenant and admin checks. v3 failed to decode them: 401 on the bearer
  paths (`Protect`, `GQLProtect`, `ConditionProtect`), 403 on `ProtectApi` (Basic auth). Keycloak issues integer `exp`.
  **Behaviour change (accepted 2026-10-30): tokens of the admin frontend client `at.ourproject.vfeeg.admin` are now
  accepted.** Its audience mapper gives `aud = [at.ourproject.vfeeg.admin, account]`; v3 refused these tokens with
  401 only because the claims struct could not decode an array, not by design. The Go services deliberately check
  neither `aud` nor `azp` (eegfaktura-docs `docs/architecture/auth.md`); filestore already accepts such tokens
  (`aud` contains `account`). No new privilege for the user: the tenant and admin checks on the token's `tenant` and
  roles are unchanged. A uniform `azp`/client rule across the services is an open point (eegfaktura-dev tracking
  W-261030-mm2). The unused `jwt.TimeFunc` override is removed (nothing called the v3 validation). New regression
  tests cover the validation paths without a live Keycloak.
- `github.com/go-jose/go-jose/v4` 4.1.4 → 4.1.6 (indirect, Dependabot).

### Fixed
- **Registering an existing tenant is refused** (261015-ca1). The gRPC `register` answered 201 for a tenant that already
  existed, dropped the given community data and admin-backend attached the new admin to that community. Now nothing is
  written and the reply carries status 409; the comparison is case-insensitive. The same goes for a new tenant on an
  existing RC number, except when it is registered with area GEA (stem and sub communities share the RC number). The MQTT
  command `register-eeg` uses the same insert: it now
  logs an error for an existing tenant instead of nothing, it writes and publishes nothing either way.
- **Tariff list with a decimal `baseFee`** (261026-mm1). `base.tariff."baseFee"` is `double precision`, the model read it
  as an integer, so one tariff with e.g. 2.25 failed the list of the whole community. The field is now a decimal type:
  the amount is read and saved unchanged, which is also what billing reads from the view. A whole amount looks the same
  in the JSON as before.
- **A failing tariff query no longer looks like an empty list.** `GetTariff` returned an empty list without an error when
  the query failed on the first row (with 261026-mm1 this hid the scan error for a community whose first tariff had
  decimals). **This also reaches the Excel master-data export:** `GetTariffNameMap` reads the same list, so when the first
  tariff of a community cannot be read, `GET /eeg/tariff` and the export answer an error instead of working with no tariffs.

- **A member with a metering point that took part can no longer be deleted** (261005-ca10). `DELETE /participant/v2/{id}`
  had no check and the delete cascaded to active and past metering points with their partition factors. It now answers
  **409** with error code 1109 and deletes nothing, unless every metering point has status `INIT`, no `activesince` and
  process state `INVALID`, `NEW` or `REJECTED`. `PENDING` is refused: the grid operator answered, the activation mail is out
  and the member can still approve. A member without metering points can be deleted as before.
- **Removing a single metering point no longer reports success when nothing was removed** (261005-ca10). In state `PENDING`
  it is now refused for the same reason. Every refused state answered 202 and web dropped the metering point from the view
  until the next load; it now answers 400 with code 1201 and web shows its error text.
- Test only: `TestAccessLine_ThroughRouter` failed at random when a generated `request.id` contained `4711`, the value the test forbids in a line (261031-mm1). Generated values are now left out of that check.
- Test only: six `eda` tests left fixed rows behind and failed at `-count=2` (261031-mm3); they now clean up.
- **The consent-request mail goes out once per metering point** (261031-mm4): an answer with two metering points
  carries code 99 twice and sent two mails per metering point.
- **A partial delivery counts as sent for the next GEA tenant** (261031-mm5): the valid recipients already have the mail, so the second tenant no longer sends it again. The notification about the rejected address stays.
- **A mail server failure answer without a message no longer panics** (261031-mm6).
- **No e-mail addresses in the log from rejected or invalid addresses** (261031-mm7): the rejected-recipient error and
  the `ValidateEmailList` text were logged in full. API answers on the REST routes and admin notifications keep the
  address. **Changed for gRPC callers (admin-backend):** their error now reads "invalid email" without the address.
- **A mail server timeout is no longer reported as "Mailversand fehlgeschlagen"** (261030-mm7): when the request
  reached the mail server (xp-connector) but it did not answer within 30 s, the mail was probably sent anyway, and
  communities that sent it again produced duplicates. Such a send is now logged as a warning and the admin
  notification entry reads **"Zeitüberschreitung – die Mail wurde vermutlich trotzdem verschickt. Vor einem erneuten
  Versand bitte beim Empfänger nachfragen."** (message code `W_MAIL_UNCONFIRMED`, kind `WARNING`, same metering point /
  identifier as before) instead of the gRPC error `rpc error: code = DeadlineExceeded …` (code `E_UNDEFINED_0`).
  The same applies when the mail server answers status 202 (see Contract).
  Affects the zp-active / consent-request mail, the zp-inactive mail and the metering point list mail. The test mail
  of the mail editor answers **504** with the same text instead of 502 "Die Testmail konnte nicht verschickt
  werden.". In a GEA, a zp-inactive mail that was not confirmed counts as sent, so the second tenant does not send it
  again. Real failures (connection refused, status other than 200, rejected recipients) keep their texts and codes;
  a mail server that does not get ready within 5 s reads "mail server not reachable within 5s (state …)" (code
  `E_UNDEFINED_0`) instead of the gRPC timeout error. A send error is logged once, by the caller. web shows the new entry after the prefix "Mailversand nicht
  bestätigt:" (eegfaktura/eegfaktura-web#251); eda-xp gets SMTP timeouts in eegfaktura/eegfaktura-eda-xp#60.

- **`service.version` in the log names the build.** Images built from a branch carried the branch name (`master`), which
  cannot tell builds apart, and a promoted prod/pilot image keeps what was set at build time. It is now the commit, or
  the version without `v` for a `v*` tag build; same as billing.

- **`POST /participant/{id}/confirm` with a `null` element in the metering point array** answered with a panic; it
  now answers 400, code 2000, before anything is written or sent (found in the review of
  eegfaktura/eegfaktura-backend#129).
- **Member list no longer fails when one member has no address, bank or contact row** (261029-mm1): the list LEFT JOINs
  both addresses, the bank account and the contact row; a member without a billing or residence address made the
  whole list of the community fail with a scan error. Such a member is now listed with empty fields, and saving the
  member (`PUT /participant/{id}`) creates the missing row instead of losing the edit. The current write
  paths always create all four rows; members without them come from older versions (before #51 a request without
  `residentAddress` stored a residence row without type) or from rows written directly into the database.
- **Member update** (261005-ca1): security relevant — details not published. `PUT /participant/{id}` now answers 400
  when the id in the path and the id in the body differ, and the update is written in one transaction.
- **Member and partition factor checks** (261005-ca4): security relevant — details not published. Requests for a member
  id that does not exist now answer 400 (error code 1106) instead of succeeding without effect. Unknown and foreign
  member ids get the same answer (code 1106, "participant not found"; code 1108 is now only used for a request without
  tenant). `PUT /participant/{id}` and `PUT /participant/v2/{id}` answer errors as JSON with a code and without
  database text (before: plain text, partial update 500); an invalid e-mail in the full update answers code 2000;
  `POST /meteringpoint/changepartitionfactor` and `POST /master/updatepartfact` answer 400 (error code 3100) for a
  metering point that is not found.
- **Metering point routes answer incomplete requests with 400/404 instead of crashing the request** (261005-ca6):
  `PUT /meteringpoint/v2/{pid}/updateid/{mid}` without `newId` and `PUT /meteringpoint/{pid}/update/{mid}` without
  `participantState` answer 400; `POST /meteringpoint/{pid}/register` for a metering point the member does not have
  answers 404; a partition factor request with an empty (`null`) metering point entry answers 400 on both routes.
  Before, the request panicked and the proxy answered 502.
- **Logging** (261008-ca1): security relevant — details not published. Without `LOG_LEVEL`
  the backend now logs at `info` instead of `debug`; an unknown value stops the start (see Changed). Log lines name ids
  (tenant, member, metering point, conversation, tariff) or counts instead of names, whole objects, request bodies,
  free-text reasons, mail-server answers, message payloads or SQL statements.
- **Partition factor routes stop after a failed MQTT send** (261015-ca2, pre-existing): `POST
  /meteringpoint/changepartitionfactor` and `POST /master/updatepartfact` answered 500 and then still wrote the success
  status (Go logged `superfluous response.WriteHeader`). The client keeps getting the 500; the second write is gone.

## [1.13.0] – 2026-10-28

### Fixed
- **Image build: code generators pinned to go.mod** (pre-existing): the Dockerfile installed `protoc-gen-go`,
  `protoc-gen-go-grpc` and `go-jsonschema` with `@latest`, so the image could be generated with other versions than
  the CI checked. They are now installed after `go mod download` at the versions in `go.mod`, as the CI does.
- **GEA: inbound EDA messages reach the tenant of the metering point** (261028-mm1): in a GEA several tenants share
  one community id, and the inbound handlers (CR_MSG, CR_REQ_PT, EC_REQ_ONL/EC_REQ_OFF, the CCMS revocation messages,
  EC_PRTFACT_CHANGE, member mails) applied status, notification, history, mail and partition factor to whichever
  tenant the community id lookup returned. They now use the tenant that holds the message's metering point (a tenant
  with a live assigned row before one whose row ended (revoked, INACTIVE), before one with only a moved or archived
  row). When no tenant of a GEA holds the metering
  point, the message goes to the GEA's base tenant (named like the RC number) instead of an arbitrary one, and this is
  logged; with one tenant per community id nothing changes. The metering point list (SENDEN_ECP) of a GEA now updates
  the metering points of every tenant of the community id; its Excel mail, notification and history go to the tenant
  that asked for the list (remembered in memory when the request is sent), else to the base tenant — never to every
  tenant, the list holds the members of all of them (decided 28.10.2026); a rejection (ABLEHNUNG_ECP) forgets the
  remembered request. Nothing sent to grid operators changes.
- **Metering point list of a community whose only tenant is named unlike its RC number** (pre-existing): SENDEN_ECP
  synced the tenant named like the RC number of the MQTT topic, which does not exist then, so the metering points
  stayed INIT/PENDING. Every tenant of the community id is synced now.

### Security
- **Go 1.26.10** (platform#180): the build image (`golang:1.26.10`), CI (`GO_VERSION`) and `go.mod`
  (`toolchain go1.26.10`; the `go 1.26.0` directive is unchanged) move from 1.26.9 to 1.26.10 (released 2026-10-19).
  It fixes CVE-2026-111943 / GO-2026-6809 (HIGH, `crypto/x509` trusted untrusted intermediate certificates; reached
  through the outgoing HTTPS calls). A patch release: language version, GODEBUG defaults and garbage collector are
  unchanged.
- **Slim runtime image** (prod image scan 2026-10-28): the final stage was the full `golang` image (Go toolchain,
  git, ssh, curl, gnupg, compilers); it is now `debian:trixie-20261020-slim` (the Debian release of the build image)
  with only `ca-certificates` and `tzdata`. The image keeps the binary `/usr/local/bin/vfeeg-backend`,
  `/etc/backend/config.yaml`, `/usr/local/bin/entrypoint.sh` (needs `/bin/sh`), `TZ=Europe/Berlin`, user 1000, the
  volume `/opt/storage`, the working directory, entry point and command; mail templates and migrations are embedded
  in the binary. **Not in the image any more:** `curl`, `git`, `ssh`, `go`. eegfaktura-docker-compose wraps the
  entry point in `url-poller.sh`, which calls `curl` inside this container: it must be adapted before it moves to
  an image with this change (the Kubernetes manifests do not use it).

## [1.12.0] – 2026-10-28

### Added
- **Joining fee on the EEG tariff** (konzept-beitrittsgebuehr.md, platform#217): a one-time fee per member with its
  own VAT setting. Migration `20261028130000_joining_fee` adds to `base.tariff` the columns `"joiningFee"`
  (`numeric(10,2)`, not null, default 0 — exact, unlike the `real` of `participantFee`), `"joiningFeeUseVat"`
  (`boolean`, not null, default false) and `"joiningFeeVatInPercent"` (`numeric`, not null, default 0). No backfill:
  every existing tariff has no fee. Versioned like the other tariff fields. `POST /eeg/tariff` rejects (400, code
  4001) an amount below 0 or with more than 2 decimals, a VAT rate outside 0–100, and any value other than 0/false
  on a tariff that is not of type `EEG`. The backend has no billing logic for the fee; eegfaktura-billing decides
  when it is due.
- Contract (additive, for **eegfaktura-web**): the tariff JSON (`GET /eeg/tariff`, `GET /eeg/tariff/{id}`,
  `POST /eeg/tariff`) has the fields `joiningFee` (number, ≥ 0, 2 decimals), `joiningFeeUseVat` (bool) and
  `joiningFeeVatInPercent` (number 0–100); missing fields are read as 0/false, so an older web keeps working (it
  saves 0 for a new tariff version).
- Contract (for **eegfaktura-billing** and **eegfaktura-v3**): the view `base.billing_masterdata` has three new
  **last** columns from the member's EEG tariff, with fallback like the other `tariff_participant_fee_*` columns:
  `tariff_joining_fee` (`numeric`, `COALESCE(tp."joiningFee", 0)`), `tariff_joining_fee_use_vat` (`boolean`,
  `COALESCE(…, false)`), `tariff_joining_fee_vat_in_percent` (`numeric`, `COALESCE(…, 0)`). `base.activeTariff`
  gets the three tariff columns at its end. Both views are replaced in place (`CREATE OR REPLACE`). Deploy the
  backend before a billing that reads the columns (an older billing ignores them). **v3:** add the three columns
  (0/false) to `v3.billing_masterdata_v3` before installing its UNION (`billing-view-union.sql` aborts on a missing
  column); where the UNION is installed, this migration replaces it with the plain definition — remove it with
  `billing-view-rollback.sql` first and reinstall it afterwards.
- Rollback: the down migration restores both views as after ZVT and **keeps** the three tariff columns with their
  values (no data loss; a new rollout finds them again).
- **Member export shows the joining fee:** the sheet „Mitglieder“ of the master data export has two new columns
  directly after „Mitgliedstarif“, in every row of the member like „Mitgliedstarif“: „Beitrittsgebühr (€)“ (the fee of
  the member's EEG tariff as a number, empty without a fee) and „Beitrittsgebühr Status“ (konzept-beitrittsgebuehr.md,
  Erweiterung 27.10.2026; same texts as the member details in web), first match wins: „verrechnet mit RG… vom
  TT.MM.JJJJ“ (final billing document), „außerhalb bezahlt am TT.MM.JJJJ“ (plus „ (Notiz)“ when billing has a note),
  „zur Verrechnung im nächsten Abrechnungslauf vorgemerkt“, „offen – wird mit der ersten Energieabrechnung
  verrechnet“ (never billed with energy, tariff fee > 0; also a member without an entry in the list), „nicht
  verrechnet – Mitglied wurde schon abgerechnet“ (billed with energy, tariff fee > 0), empty when the tariff has no fee
  and nothing is recorded. Without a list from web the status column stays empty. All later columns move two to the
  right. The note is the community's own free text and appears only in its own export.
- Contract (for **eegfaktura-web**): `/eeg/export/masterdata` also accepts `POST` with an optional JSON body — the
  array eegfaktura-billing returns from `GET /api/billingDocuments/tenant/{tenantId}/joiningFeeState`, unchanged:
  `[{"participantId": "<uuid>", "energyBilled": bool, "joiningFee": {"documentNumber": "…", "documentDate":
  "YYYY-MM-DD"} | null, "requested": bool, "paidExternally": {"paidOn": "YYYY-MM-DD", "note": "…"} | null}]`. The
  backend does not call billing. Without a body (or with `GET`, which stays until web has switched) the status column
  stays empty; a posted empty array `[]` means no member has a final document yet. A malformed body answers 400 (code
  2000). Entries for participants that are not members of the tenant from the `tenant` header are ignored; the list
  only fills the column. Auth unchanged (`Protect`, tenant from the checked header).

### Fixed
- **CI: the Snyk Code scan fails visibly when it cannot run.** An invalid `SNYK_TOKEN` ended the weekly and
  push scan green without any result (`continue-on-error`); now only „issues found“ keeps the run green (the
  issues go to Code Scanning), an authentication or tool error fails it. The Snyk CLI is pinned to 1.1308.1
  instead of the latest version.
- **Tariff validation messages reach the client (261027-mm1):** `POST /eeg/tariff` answered a rejected time-based tariff (ZVT)
  with 400 and the body `{}` — the reason was lost because a plain Go error marshals to an empty object. It now
  answers in the usual error format (`{"error": {"code": 4001, "message": "…"}}`) with the German reason. Pre-existing
  defect, met while adding the joining fee validation.

## [1.11.0] – 2026-10-28

### Changed
- **Messages: an energy request for several metering points is one entry, also after it closed** (user decision
  28.10.2026, after a request for a whole community in prod). The notification list for admins
  (`GET /eeg/notifications/{start}`) leaves out the request echoes (`ANFORDERUNG_PT`) and the answers
  (`ANTWORT_PT`, `ABLEHNUNG_PT`) a live entry or a stored summary of the same tenant stands for; the live entry while
  the run is open, the stored summary after it closed. Before, only the answers of an open run were left out, every
  request echo was listed, and once the summary was written all answers came back. Answers carry no period and no
  reference to their request, so the rule (`energyrequest.Collapser`) is conservative: a message is left out only if,
  for each of its metering points, every run that can still own it (older, created at most 25 h before, sent the
  metering point, open or with its summary after the message) is a run with a summary of the listed tenant. Kept:
  messages of a request for a single metering point, messages a single request and a run compete for, in a GEA the
  messages of another tenant's run (that tenant has the live entry and the summary), an answer after the summary (the
  summary does not count it) and the messages of a run that got no summary 24 h after it started. Response shape
  unchanged, no web change.
- The admin list reads in batches (30, then 500, at most 40) until a page is full; reaching the limit is logged. When
  the rows of the live entries, the runs or the summaries cannot be read, it is the plain list as before. Known limit:
  in a GEA whose EDA messages are stored under another tenant of the community, the requesting tenant's live entry is
  placed at its row id as before (nothing to place it by).

### Added
- Migrations `20261028120000_notification_energy_request_index` and `20261028120100_notification_summary_index`:
  partial indexes on `base.notification (tenant, id)` for the hidden RUN rows (`process = 'ENERGY_REQUEST'`, one per
  request) and the stored summaries (`notification->>'type' = 'ZUSAMMENFASSUNG_PT'`, one per closed request), built
  `CONCURRENTLY`, so the list finds both at any age. No data change; the down migrations drop them.

### Fixed
- **The live entry of a request for a whole community did not show** (28.10.2026, prod): it was placed at the run's
  row id, below the request echoes written after it, and the list returns the newest 30 entries only. It is now placed
  with the newest message it stands for (its `id` stays the run's row id), so web finds it and refreshes it every
  minute.

## [1.10.0] – 2026-10-26

### Added
- **Fixed footer in every member mail:** `consent-request`, `zp-active` and `zp-inactive` end with
  `versandt durch EEG Faktura, ein Service des VFEEG - https://vfeeg.org/` — in the sent mail (saved text and
  built-in default), the preview and the test mail. The server appends it after sanitising; it is not editable and
  not stored. The text/plain part (derived from the HTML by eda-xp) contains it too.
- Contract (additive, for **eegfaktura-web**): `GET /eeg/mailtemplates` and `GET /eeg/mailtemplates/{key}` return the
  new string field `fixedFooter` with that text. Deploy before or together with web (web shows nothing without it).
- **Font sizes in mail texts:** `mail-html-policy.json` version 2 allows `font-size` `13px` and `20px` on `p`,
  `h1`–`h3` and `li`, alone or together with `text-align`; other sizes and units are dropped. New shared cases in
  `mail-html-policy-cases.json` (font sizes, image widths 200/400/600); both files stay byte-identical with
  eegfaktura-billing.
- **Link rules for member mails (Variante A, decided 2026-10-26):** links only to `https:`/`mailto:` (as before), no
  IP address host (also decimal/hex forms), no URL shortener (list `links.blockedHosts` in `mail-html-policy.json`,
  host and subdomains), a link text that shows an address must show the target's (an e-mail address as text of a web
  link and a web address as text of a `mailto` link never match; a file name like `anleitung.pdf` is no address), at
  most 3 links and 2 main domains per text (`portal.salzburgnetz.at` counts as `salzburgnetz.at`; three labels under
  `co.at`, `or.at`, `gv.at`, `ac.at`; `mailto` not counted as a domain; the fixed footer never counts). Save (PUT), preview and test mail answer 400
  with one German message per broken rule in the existing `errors` format. New shared cases
  `mail-link-cases.json` (byte-identical with eegfaktura-billing).
  Link texts are read as the reader sees them, with the same algorithm as eegfaktura-billing: zero-width characters
  are removed, every Unicode space (also NBSP) separates words, and every word that is an address (`…://…`, `www.…`,
  an e-mail address, a bare domain; surrounding quotes and punctuation stripped) must match the target —
  „Mehr unter https://eeg.at“ is fine, `a@b`, `office@eeg` and `z.B.` are no address, a `mailto:` with `%40` matches
  its text. The host dots `。` `．` `｡` count as `.` (`bit。ly` is a shortener); a single hex word like `cafe` is a host
  name, not an IP. **No placeholder inside a link** (new key `placeholder-in-link`, „Platzhalter dürfen nicht in einem
  Link stehen: Website der EEG“): at send time `eeg.website`/`eeg.email` would show another address than the target.
  The sanitiser also drops an https link with `%` in its host part (as eegfaktura-billing).
  **Host characters:** a link whose host has other characters than ASCII letters, digits, `-` and `.` (fullwidth
  letters or digits, soft hyphen, zero-width characters, umlauts) is refused (new key `bad-host`, „Die Linkadresse
  enthält ungültige Zeichen: münchen.at“); an umlaut domain has to be written in its `xn--` form
  (`xn--mnchen-3ya.at`). Link texts: invisible format characters (Unicode `Cf`, e.g. soft hyphen) are removed,
  fullwidth ASCII counts as ASCII, a word also ends at a `:` not followed by `//` (`Website:bank.at`), punctuation
  and symbols at both ends of a word are stripped (`»bank.at«`, `bank.at…`, `→bank.at`; a final `/` stays), the
  alt text of an image inside a link counts as link text, and `www.eeg.at:443` shows the host `eeg.at`. Hosts and
  `mailto` addresses are compared with ASCII-only lower-casing (`İnfo@` is not `info@`); a `mailto` address is
  trimmed of Unicode spaces after percent-decoding. The sanitiser trims Unicode spaces (also NBSP) around a URL,
  as eegfaktura-billing does.
- Contract (additive, for **eegfaktura-web**): the `limits` of `GET /eeg/mailtemplates/{key}` carry `maxLinks` (3) and
  `maxLinkDomains` (2), read from the policy file.
- **Uploaded mail images are re-encoded** (security hardening): images wider than 1200 px, higher than 4000 px or over
  4.8 million pixels, or that do not decode, are refused (400); the rest is decoded and encoded again (PNG lossless,
  JPEG quality 80) and only that is stored, so EXIF/GPS and hidden or appended data are dropped. Images stored before
  are not touched.

### Changed
- **Member mail limits:** at most 3 images (was 10) and no PDF attachment for now (was 1): a PDF upload or a save with
  `attachmentFileId` is refused with a message. A saved text that breaks the link rules, has more than 3 images or a
  PDF attachment is not changed; at send it counts as "can not render": the member gets the default text and the
  community the existing error notification with the reason.
- `cmd/mailtemplate-import`: links the save would refuse and a PDF attachment are a STOP (3 of the 10 hand-made
  templates in `public/`); the legacy closing „versandt durch“ + logo is dropped with a Hinweis.
- The default texts no longer contain „versandt durch“ and the eegfaktura logo (catalog and the embedded default
  templates, which also no longer attach the logo); the fixed footer replaces them. `DefaultVersion` 1 → 2 for the
  three mails, so communities with an own text get the "Standardtext geändert" notice once. Saved texts are not
  changed; the built-in logo (`mailfile:eegfaktura-logo`) is still served and sent for texts that use it.

### Fixed
- The fixed footer was inserted at a wrong position (inside a tag) when the mail contained characters that change
  their length in lower case (e.g. „İ“); the footer is now placed by a byte-exact search, also in the frame of saved
  texts.

## [1.9.0] – 2026-10-26

### Changed
- **No migration of the hand-made templates (decided 2026-10-26):** communities with volume templates send the
  default text from the rollout on and set up their own text in the editor themselves. On start, 1.9.0 logs a
  warning listing every member-mail template file still on the volume that is now ignored
  (`… hand-made mail template file(s) on the volume are ignored since 1.9.0 …`). `cmd/mailtemplate-import` stays
  available but is not part of the rollout.
- A saved text that can not be rendered now falls back to the built-in default (before: to the volume template, if
  any); the community's error notice already said "Standardtext".
- **Hand-made mail templates on the data volume are no longer used.** A member mail (`consent-request`, `zp-active`,
  `zp-inactive`) is either the community's saved text (`base.mail_template`) or the built-in default embedded in the
  binary; `<file-content.templates>/<TENANT>/templates/*` and `<file-content.templates>/templates/*` are ignored by
  the send path, the preview and the test mail. Communities without a saved text and without volume templates get the
  same mail as before. Contract change with **eegfaktura-web**: `legacyTemplateActive` is removed from
  `GET /eeg/mailtemplates/{key}`, and `useDefault` of preview/test mail is no longer read (it only selected the volume
  template); web ignores both.
  **Operations note:** communities with volume templates send the default text from the rollout on, until they
  save their own text.

## [1.8.0] – 2026-10-26

### Added
- **Editable member mail texts** (konzept-mail-texte-editierbar.md): a community customises subject and text of
  the three member mails `consent-request` (Freigabe-Aufforderung), `zp-active` and `zp-inactive`. New REST API
  under `/eeg/mailtemplates` (EEG_ADMIN, tenant header): catalog, read, save (`version`, 409 on a stale version),
  reset, image/PDF upload (type checked on the content: PNG/JPEG ≤ 1 MB, max. 10; one PDF ≤ 3 MB; all files
  ≤ 6 MB), preview (sample member, real community data) and test mail (to the login's e-mail or the community
  address, no Cc, subject prefixed `[Test] `).
  Uploads are drafts and are never sent; saving (`attachmentFileId` names the PDF) makes the used files part of the
  version, deletes files the version no longer uses and drafts older than 24 h; a file the saved version uses can
  not be deleted (409); an image the text references must exist when saving (400 otherwise).
  `legacyTemplateActive` tells the editor that a hand-made volume template still applies; preview and test mail
  of the unchanged default (compared after sanitising) or with `useDefault: true` then use that template.
  Texts are sanitised with bluemonday driven by `mailtemplate/mail-html-policy.json`, tested against the case list
  `mailtemplate/mail-html-policy-cases.json` (both shared with eegfaktura-billing). Placeholders are
  `<span|div data-ph>` in the text (blocks only at top level) and `{key}` in the subject (only the catalog key
  syntax; `{EEG}` stays text); they are replaced as escaped values, never through a template engine. "{{" in
  text is written as "{<!-- -->{" (client-side template engines); link URLs are kept as written.
  `eeg.contactBlock` shows name, contact person, address, phone, e-mail and website of the community, each only
  when present (decision G.1; today's signature has no contact person). zp-inactive also has
  `directionText`/`endDateText`, which carry their own wording and are empty without a value.
  Sending: a stored version of the tenant goes first (only its committed, referenced files; images inline as
  `cid:`, optional PDF, size checked before eda-xp); otherwise the existing resolution (volume per tenant → volume
  global → embedded) is unchanged. A stored version that can not be rendered (including a missing image) sends
  that default and an error notification to the community (validation/size text or a generic sentence; details only
  in the log). On startup, communities whose customised mail is based on an older default get one notification
  (written together with its "notified" mark).
  Migrations `20261022120000_mail_templates` (two new tables) and `20261023120000_mail_template_file_drafts`
  (`committed`, `attachment_file_id`), both additive with down; `schema.sql`, `schema.hcl` updated, `atlas.sum`
  regenerated with `atlas migrate hash` (it was hashed over CRLF files before and did not validate).
- **`cmd/mailtemplate-import`**: one-off operator tool that converts the hand-made mail templates of a volume copy
  (`<TENANT>/templates`) into the new format and imports them through the REST API (dry run, `-test-mail eeg`).
  Unmappable template expressions (also inside attributes) stop the import of that tenant; `-overwrite` replaces a
  saved version only by the save itself. See `cmd/mailtemplate-import/README.md`.

### Changed
- The mail send log line no longer contains the subject (a customised subject can contain member data).
- **`{eeg.name}` is the community's designation** (`base.eeg.description`, as the issuer on documents and in the
  billing mail; the short name only when the designation is empty), decision 25.10.2026. `eeg.description` is no
  longer offered (same value); texts saved with it still render. The import tool maps `.Eeg.Description` to
  `eeg.name`. Changed member mail wording: the built-in zp-inactive mail says "nicht mehr Teil der Energiegemeinschaft
  <designation>" instead of the short name (every community without its own template), and saved texts that use
  `{eeg.name}` now show the designation. The default text itself is unchanged, so no "new default" notice is sent
  (`DefaultVersion` stays 1). Hand-made volume templates with `{{.Eeg.Name}}` keep the short name until imported.
- The gRPC call to eda-xp for a mail with inline images and attachment gets 30 s instead of 5 s when its files
  exceed 256 KB (a customised mail may carry up to 6 MB); smaller mails keep 5 s.

### Fixed
- Mail texts (review 25.10.2026): a save without `attachmentFileId` is a 400 (it removed a saved PDF silently; `null`
  = no PDF); the preview and test mail show a hand-made volume template only when nothing is customised (also no own
  image or PDF); `legacyTemplateActive` also reports the global volume template, as the send path uses it.
- Error notifications of a failed member mail (consent request / metering point active) and of a failed metering
  point list mail were saved under the community id (ecId) instead of the tenant and never reached the community's
  admins. They are saved under the tenant now.

## [1.7.0] – 2026-10-25

### Added
- **Energy data requests: per-meter periods, lock against double requests, live status in Messages**
  (`konzept-smart-nachfordern.md`). Contract change with **eegfaktura-web** (caller of `POST /meteringpoint/syncenergy`
  and `GET /eeg/notifications/{id}`); the request additions are optional, older web versions keep working.
  - `syncenergy`: each entry of `meteringPoints` takes optional `from`/`to` (ms, like the request's own). Set, they
    are the period of that metering point (cut to start/end of day and to its active period as before); missing, the
    request period applies. An optional top-level `complete: [string]` lists metering points web found complete:
    they are not requested and appear in the run/summary as skipped with reason `COMPLETE`; a metering point also
    listed in `meteringPoints` is handled by that entry (requested, or skipped for its own reason). With periods per
    metering point, or without a request period, the run (and so summary and live entry) carries the span of the
    requested metering points as its period.
  - **Lock (1 hour):** a metering point requested by a run of the same tenant in the last hour for a period sharing
    at least one day, and not rejected (`ABLEHNUNG_PT`) there, is not requested again: skipped with reason
    `ALREADY_REQUESTED` and `until` (ms, run creation + 1 h). Single and bulk requests alike; no override. A rejected
    metering point is free at once. GEA tenants are locked separately. A metering point whose request could not be
    sent (MQTT error, answer `500`) is taken out of the run again and is not locked; of a metering point with several
    participations only the span of the sent ones stays.
  - **A request that sends nothing stores no run** (everything skipped: locked, complete, not registered, …): no
    live entry and no summary; the response body reports the skipped metering points. Before, a request for two or
    more metering points that were all skipped got a summary with 0 requested.
  - **Response: `200` with a body instead of `204`** (online community):
    `{"requested": <metering points requested from the grid operator>, "skipped": [{"meteringPoint", "reason",
    "until"?}]}`, the skipped entries the run records (`COMPLETE`, `ALREADY_REQUESTED`, `NOT_REGISTERED`, …), also for
    a single request that is locked. Error status codes are unchanged; an offline community still gets `204`. Web
    (`syncMeteringPoint`) only checks for a 2xx answer and does not read the body, so older web versions keep working.
    A request with only `complete` (every metering point complete) is accepted (nothing stored); before, a request without
    metering points got `500` („no Meter selected“), which it still gets without `complete`.
  - **A run that cannot be stored is not sent:** if the run row cannot be written (database error), nothing goes to
    the grid operator and the request gets `500` (error code `5100`, as a failed send); it can be repeated at once.
    Before, the requests went out without a run, so nothing locked or summarised them. (Rare: if the row is written
    but the answer is lost, the metering points stay locked for the hour although nothing was sent.) The lock and
    the stored requests are only read when at least one metering point can be sent: a request where every metering
    point is complete or skipped for another reason answers `200` even when that read would fail.
  - `skipped[]` entries in the run row and in `ZUSAMMENFASSUNG_PT` gain the optional field `until`.
  - **Notification list (admins):** each open run with a summary (two or more metering points, younger than 24 h,
    not yet summarised) is listed as one entry shaped like a stored `ZUSAMMENFASSUNG_PT` (type `MESSAGE`, process
    `EDA_PROCESS`), computed at request time, with `live: true` in the message, the run's row id as `id` and its
    creation time as `date`; it is ordered by id like the other entries. The `ANTWORT_PT` / `ABLEHNUNG_PT` of such a
    run are left out of the list. Once the stored summary is written it replaces the live entry, and the single
    answers are listed again as before. Single requests are unchanged. The rows behind it are kept for 15 s per
    backend instance (cleared when the instance writes a run or a summary; new answers show after 15 s at the
    latest, so an answer arriving within these 15 s can show once as a single entry before it moves into the live
    entry). If they cannot be read, the list comes without live entries instead of failing. For a tenant without a
    run younger than 24 h only a short probe runs, the rows are not read.
  - **Answers are attributed by period** (summary, lock and live entry alike). Answers carry no period and no
    reference to the request, so per metering point: a later run supersedes an earlier request only for a period
    sharing a day (before: any later run with that metering point, which marked e.g. a May request as „requested
    again“ after an April request and gave it all answers); within its range an answer goes to the oldest request
    without an answer, else to the newest. A rejection frees the period of the request it belongs to.
  - **One row set for summary, lock and live entry:** all three read the run rows and summaries of the last 49 hours
    (2 x deadline + 1 h) and the answers from the oldest of these runs on, of the tenants of the community id, and
    attribute them alike; an answer to an open run keeps its owner in the set even if that run is older than the
    deadline. The 5-minute check reads this window on every run (before: from the oldest open bulk run on, which left
    out single requests after a restart and could disagree with the live entry and the lock). After a long outage
    a request is now still summarised up to about a day after its deadline (before: about an hour).
  - No schema change: the notification JSON is extended additively; old runs have no `until`.
  - **Deploy order:** backend first is safe; web shows the new reasons and the live entry only with its matching
    release.

### Changed
- **Energy data request after an owner change: the old member's part is requested too** (decision of 2026-10-24:
  the billing needs all current data, and the old member's data cannot be got later). **Behaviour towards the grid
  operators changes.** Before, `syncenergy` skipped the old member's moved entry (flag 0, reason `NOT_ASSIGNED`)
  and requested only the new member's part. Now every entry of the metering point is requested for its part of the
  period (cut to its own active period); an archived entry (flag 2) is still skipped (`NOT_ASSIGNED`). Note: when
  an archived metering point is registered again, `saveMeteringPoint` sets its earlier INACTIVE entries to flag 0, so
  their periods are requested too; only archived entries of metering points that never came back stay skipped.
  - Grid operator side (ebUtilities process CR_REQ_PT, https://www.ebutilities.at/prozesse/503, PDF:
    https://www.ebutilities.at/api/export/pdf/processes/503): the grid operator checks „ob der Zählpunkt durch den
    Anforderer im Anforderungszeitraum beliefert wird“ (rejection 55/94). The requester is the community, so the old
    member's period qualifies while the metering point belonged to the community.
  - A merged request goes out with the current entry (flag 1), else the later part's; for BEG its grid operator is
    the receiver.
  - Parts that overlap or touch (old until 15.06., new from 16.06.) go out as **one** ANFORDERUNG_PT over both parts;
    a gap between the owners (old until 02.09., new from 06.09.) gives **two** requests, one per part. No period is
    sent twice. A request only in the old member's period is sent (before: skipped as `NOT_ACTIVE_IN_PERIOD`).
  - The 1 h lock is checked per part: a part requested within the hour is skipped, the other part is sent. The run
    records one period per metering point, the span of the sent parts (as for several participations before), so
    with a gap the days between the owners are locked too (no entry is active there, so none would be sent).
    A part is skipped whole when the lock shares at least one day with it, as for every metering point since the
    lock was introduced (no trimming): when the old member's last day is the new member's first day and only the
    new part was requested, a request over both parts sends nothing for the old part either until the lock ends
    (at most 1 h, the answer says `ALREADY_REQUESTED` with `until`).
  - Skipped reason with several entries: independent of their order, the reason that tells most counts
    (`ALREADY_REQUESTED` before `NOT_REGISTERED`, `NO_ACTIVE_PERIOD`, `NOT_ACTIVE_IN_PERIOD`, `NOT_ASSIGNED`); locked
    for two entries, the later `until`. The old member's entry outside the period gives way to the new member's
    reason, as `NOT_ASSIGNED` did before.
  - Open: whether the grid operators answer a request for the old member's period (whose consent has ended) is not
    verified in production yet (the spec above says it should be accepted). Known limit with a gap (two requests
    for one metering point in a run): the latest answer counts for the summary and the lock, so a rejection of one
    part frees the lock for the whole span and the summary can show the metering point as rejected although the
    other part was accepted; the summary can also be written after the first of the two answers (the second one is
    then listed as a single answer).

### Security
- **`/query` no longer returns the token verifier's error text.** A request to the GraphQL endpoint with an
  invalid or expired token got the verifier message in the 401 body (e.g. `oidc: id token signed with
  unsupported algorithm …`). The body is now empty, as on the JWT-protected REST endpoints; the message stays in the log.
- **Unauthenticated `GET /master/test` removed.** The route had no auth check and only printed „TestApi“ to
  stdout; nothing calls it. `GET /master/test` now answers 404.

### Fixed

- **A rejection no longer undoes a participation** (`konzept-zaehlpunkt-ausblenden.md`, part 2, decisions
  24.10.2026). Example: a metering point active 06.03.2024–22.01.2025 (`AUFHEBUNG_CCMI`), re-registered on 05.05.2025
  and rejected with code 57 („Zählpunkt nicht versorgt“), got process state `INVALID` while its status stayed
  `INACTIVE`. web then showed it in every period at the top of the problem cases, and it could not be deleted (it has
  energy data and invoices). Now an answer that sets `INVALID` or `REJECTED` (rejection, refused or missing consent,
  answer codes 182/183, abort) for a current entry (`flag = 1`):
  - **ended** (status `INACTIVE` with `activesince` and `inactivesince`): process state stays `INACTIVE`, whatever
    the code. Without `activesince` the entry never took part (e.g. revoked via CCMS before activation) and gets
    `INVALID`/`REJECTED` as before;
  - **active** (status `ACTIVE`) and **all** codes of the message concern the request, not the metering point
    (`model.REJECTED_KEEP_ACTIVE_CODES`: 172/173 consent refused / not answered, 183 allocation keys > 100 %, 188
    participation factor > 100 %): stays `ACTIVE`; `INIT`/`PENDING`/`APPROVED` left by the rejected request go back
    to `ACTIVE`, `REVOKED` and other states stay. This includes ZUSTIMMUNG with 172/173 for an active metering point
    (platform#180 below sets `INVALID` for one still in registration). For ABLEHNUNG the rule needs all deciding
    codes to be such codes: if the message also has a code about the metering point that the old logic considered
    (e.g. [188, 57]), that code is stored and the entry gets `INVALID`/`REJECTED` as before, so the admin sees the
    inconsistency; a code the old logic ignored (e.g. 82 in [172, 82]) changes nothing, 172 stays stored.
    ZUSTIMMUNG and ANTWORT keep their existing single-code logic. Any other code (56, 57, 185, 104, 159, …) gives
    `INVALID`/`REJECTED` as before;
  - where the state is kept, `last_process_state` holds the rejected state and `statusCode` the code; `activesince`
    is not touched;
  - never-active (`INIT`) entries get `INVALID`/`REJECTED` as before. An applied rejection clears
    `last_process_state`; a rejection without code (ABBRUCH) keeps the stored `statusCode` as before.

  The notification for the admins is unchanged. **Visible change:** a fresh rejection of such a metering point is
  shown only in the messages (Nachrichten); the metering point keeps its inactive/active card in web, without the
  `INVALID` card and code, and is no longer among the problem cases. The Excel master data export shows
  `INACTIVE`/`ACTIVE` instead of `INVALID`/`REJECTED` in „ZP-Status“.
  - **Revocation:** AUFHEBUNG_CCMI/CCMC (`meteringPointRevokeByConsentId`) of an entry that ends there (status not
    `INIT`, `activesince` and end date set) and still carries `INVALID`/`REJECTED` ends it at `INACTIVE` (the
    rejection goes to `last_process_state`) instead of `INACTIVE` + `INVALID`. This also applies to history entries
    (`flag = 0`) that the revocation matches by consent id, consistent with the current entry. Never-active entries
    as before; the „no longer part of the community“ mail still follows the status only.
  - **Restore after code 86** (competing processes): an ended entry at `INIT` goes back to `INACTIVE`; every other
    state stays. An ended entry rejected with 86 while its re-registration is `PENDING` therefore stays `PENDING`: 86
    reports a competing process at the grid operator, and we cannot tell whether it is our own. `last_process_state`
    is not used for the restore any more (it was never written before, so this was a no-op).
  - `last_process_state` is cleared by every other status message (a new registration attempt or its result).
  - **Operator, after deploying (prod and pilot):** clean up the existing cases once with
    `scripts/ended-meter-rejection/` (README there): `pgz <ns> < check.sql` (read only; counts per variant and code,
    and the variants left alone: `ohne_datum`, `aktiv_andere`), `pgz <ns> -v expected=<anzahl> < fix.sql` (commits
    only if exactly that many rows changed), `rollback.sql` the same way to undo. The active variant covers only
    `ACTIVE` + `INVALID` with code 172/173/183/188 (an abort gives `REJECTED` and keeps an old code, so it is only
    reported); an optional read-only query in the README lists these with their latest EDA message. Rough size,
    counted on 24.10.2026 before the `activesince` condition: about 377 ended entries in about 100 tenants, about 53
    active ones (188/173). `expected` always comes from `check.sql`, never from these numbers.
- **Restoring the process state after code 86 no longer logs an error on success.**
  `restoreMeteringPointProcessState` always returned an error value (also without a database error), so every
  rejection with code 86 logged „can not restore MeteringPointProcessState“. The update itself ran; only the log
  changes.
- **EDA: ZUSTIMMUNG_ECON/ECOF with code 172 sets the metering point to INVALID again** (platform#180). Code 172
  („Kunde hat Datenfreigabe abgelehnt“) had an empty `case` since 2025-09-22: the line for 173 was inserted between
  the label and the body of 172, and Go has no fallthrough. A consent message with 172 only went to the history; the
  metering point stayed in its state (usually PENDING) and the community got no notification. 172 and 173 now share
  the body again, as introduced on 2024-12-09: process state INVALID, status code 172, a notification for the
  admins. ABLEHNUNG_ECON/ECOF is unchanged. Metering points that received 172 before stay as they are.
- **Metering points with an owner change: each entry gets its own period.** After an owner change a
  metering point has two entries in the community (old member, new member). The lookup behind the energy data
  request and the master data for energystore joined the period only by metering point number, so
  both entries got the new member's period (or every combination of both). Effect: an energy data request over the
  change date sent the request for the new member's part twice to the grid operator. Now every entry carries its
  own period and flag, and the request is sent once. Metering points with one entry return the same as before
  (the old member's part is now requested too, see Changed).
- **Energy data request: an ended entry gets its full last day.** `syncenergy` cut the requested period at the
  entry's `inactivesince` as UTC midnight (e.g. 31.05. 02:00 local), so the last active day was mostly not requested
  from the grid operator; `activesince` started at UTC midnight (01:00 / 02:00 local). `inactivesince` is the last
  active day (energystore and billing count it as a whole day): the period now ends at 23:45 local time of that day
  and starts at 00:00 local time of `activesince`, as the request period itself (summer time included). Affects every
  metering point whose active period starts or ends inside the requested period (single entries too, e.g. a
  metering point that left the community); one with an open end is unchanged — to be confirmed by the user, as it
  changes what goes to the grid operators for all such entries. Pre-existing; with the old member's part now
  requested (see Changed) it reaches the grid operators on every owner change.
- **Archiving a tariff that is still in use is refused** (`DELETE /eeg/tariff/{id}`). The check in `archiveTariff`
  only failed on a database error and ignored the rows it found, so a tariff still assigned to a member
  (`participant.tariffId`) or a metering point (`meteringpoint.tariff_id`) was archived. Now the tariff stays as it is
  and the call answers `400` with code `4002` („Tariff is currently used“). Counted are entries whose tariff can still
  be changed (decision 24.10.2026): members of the tenant in every status, metering points only as the current entry
  (`flag = 1`, any status), not their moved (`0`) or archived (`2`) history rows. A later re-billing of an old period
  may then miss the archived tariff (as before this fix, when every tariff could be archived). Check and archive are
  one `UPDATE`. Not prevented: an assignment committed at the same moment, and saving a member or metering point does
  not check the tariff's status (known gap). The member check is now tenant-scoped like the metering point check. An
  unknown tariff id, or one of another community, now answers `400` with code `4001` („unknown tariff“, as when saving
  a tariff) instead of `202`. eegfaktura-web checks this client-side as well (web#191).

### CI
- `rolling-release.yml`: no image build on a draft pull request — it runs when the pull request is marked ready for review (`ready_for_review`) and on every later push to it; `pr-checks.yml` still checks drafts. Pushes, tags and the deploy dispatch are unchanged.

## [1.6.0] – 2026-10-21

### Added
- **Time-of-use tariffs (ZVT)**: `base.tariff` gains `useTimeTariff` plus two
  optional named time windows (`timeTariff{1,2}Active/Name/From/To/CentPerKWh`;
  `time` columns, 15-min raster, `From > To` crosses midnight). `centPerKWh`
  stays the base price; existing tariffs are unchanged (`useTimeTariff=false`).
  Both views are extended: `base.activeTariff` passes the new fields through
  (From/To as `HH:MM` text) and `base.billing_masterdata` exposes them as
  `tariff_use_time_tariff` / `tariff_time{1,2}_*` for the billing service.
  Saving a tariff validates the windows server-side: only consumer/producer
  tariffs, active windows need from/to/price on the 15-min raster,
  `Von ≠ Bis`, the two windows must not overlap (cyclic check, midnight
  crossing allowed) and free kWh are rejected in time-based mode.
  Migration `20260711120000_zvt_time_tariff` (up/down; `schema.sql`,
  `schema.hcl` and `atlas.sum` updated in sync).

### Changed
- Mail deliverability: the global default activation mail template declares `lang="de"`
  (was `en`) so clients don't mis-detect the language (minor spam-score signal). Per-tenant
  templates on the data volume are unaffected (operator cleanup). The parser test expects `lang="de"` (it still
  expected `en`; env/billing builds ran no PR checks).
- **Docker image: Go pinned to 1.25.14** (platform#180). Both stages of the `Dockerfile` used the floating tag
  `golang:1.25`; they now use `golang:1.25.14`, the version of the CI.
- **Tests: PostgreSQL 18 in the database tests** (platform#180). The Testcontainers database of the tests used
  `postgres:15-alpine`, while every zone runs PostgreSQL 18; the tests now use `postgres:18.6-alpine`. PostgreSQL 18
  also logs "ready to accept connections" twice (initdb server, then the real server), so the wait for the second line
  stays. No change to the service.
- **Go 1.26: toolchain raised to 1.26.9 and pinned** (platform#180). Both `Dockerfile` stages used the floating
  tag `golang:1.25`; `go.mod` (`go 1.26.0`), CI (`GO_VERSION`) and both stages (`golang:1.26.9`) now use 1.26.9, the newest 1.26 patch release older than
  7 days. It fixes the standard library findings of govulncheck in `net/http`, `net/textproto`, `crypto/tls` and
  `html/template` (GO-2026-6599, -6600, -6603, -6605, -6607, -6608, -6610 to -6613, -6617). Still open:
  GO-2026-6809 (`crypto/x509`), fixed only in Go 1.26.10 (released 2026-10-19, not yet 7 days old).

### Fixed
- **Tests: the middleware tests run without Keycloak** (platform#180). `TestMain` in `api/middleware` called
  `InitKeycloak()` against localhost:7080, so the Protect, ConditionProtect, auth and GQLAdminCheck tests never ran
  (they mock `VerifyTokenClaims`). `TestMain` is removed; `api/middleware` now runs in the blocking unit and full CI
  jobs. The database test helper waits for the second "ready to accept connections" log line of PostgreSQL (the first
  one comes from the temporary initdb server). No change to the service.
- **MQTT broker: data race on the stop channel** (platform#180). `Listen` replaced the stop channel created by
  `NewMessageBroker` inside its own goroutine, while `Stop` reads it from another goroutine. `Listen`
  now uses the channel from the constructor. The broker is started once and stopped at shutdown, as before.
  `mqtt` runs in the blocking full CI job (race detector).
- **Tests: the `api` tests compile again** (platform#180). The metering point and participant controller tests called
  the handler functions from before the `MeteringHandler`/`ParticipantHandler` refactoring and the old
  `QueryParticipant` signature; they now build the handlers with the test database. `api` runs in the blocking full CI
  job; the report-only "Known errors" job is removed, as no package is left in it. No change to the service.
- **CI: `go vet` blocks the unit job** (platform#180). The four findings are fixed without a change in behaviour: keyed
  fields in the struct literals in `api/meteringPointController.go`, `mqtt/subscriptionHandler.go` and
  `eda/edaSubscription.go`, and the unreachable `return` in `util/time.go`.
- **CI: `gofmt` check in the unit job** (platform#180). Six files were not formatted (import order, alignment); they
  are formatted now, and the unit job fails on a tracked `.go` file that `gofmt -l` reports.

### Security
- **Dependencies: `golang.org/x/net` v0.60.0, `golang.org/x/crypto` v0.57.0** (platform#180). Fixes the govulncheck
  findings GO-2026-6603, -6610, -6611, -6612, -6617 (x/net) and GO-2026-6354, -6355 (x/crypto). x/net v0.60.0
  needs x/crypto v0.57.0 and Go 1.26; `go mod tidy` also raises x/mod, x/sync, x/sys, x/text and x/tools (minor).
  Still open: `github.com/golang-jwt/jwt` v3 (GO-2025-3553, no fix in v3) and `github.com/xuri/excelize/v2` v2.11.0
  (GO-2026-6684, -6685, -6688, -6689, fixed only in unreleased commits).

### Notes
- Go 1.26: with `go 1.26.0` the GODEBUG defaults of 1.26 apply (`cryptocustomrand`, `httpservecontentmaxranges`,
  `tlssecpmlkem`, `urlstrictcolons`); none affects the backend. The Green Tea garbage collector is the default:
  watch the pod memory after the first rollout (opt-out at build time: `GOEXPERIMENT=nogreenteagc`). (platform#180)

## [1.5.1] – 2026-10-20

### Changed
- **Master data export: member status from the metering points** (platform#165, decision 20.10.2026). Both „Status“
  columns showed the stored member status, which stays ACTIVE after a member left. They now show „aktiv“ (at least one
  active metering point), „in Anmeldung“ (a metering point not yet active whose registration is running; a metering point
  that left counts as inactive until it is active again) or „inaktiv“ (all left, archived or rejected). A revocation counts once it is confirmed (the metering point's
  status turns INACTIVE then, also with a later end date). Members without metering points have no row in the export,
  as before. Stored data is unchanged; the import does not read the column.

## [1.4.0] – 2026-10-20

### Fixed
- **Excel exports: the filter covers all columns and rows** (customer request 19.10.2026). The master data export set
  its auto filter to `A1:AH10`: the columns AI–AO (Ort, Straße, HausNr., aktiviert, deaktiviert, Zp. Tarifname,
  Umspannwerk) had no filter button, and rows after 10 were outside the range. The metering point list export had the
  same fixed range. Both now span every header column and all rows.

### Added
- **Summary of an energy data request in Messages** (platform#173). A request for two or more metering points
  (dialog „Werte anfordern“) gets one summary notification once every metering point has answered, at the latest
  24 hours after the request: requested, accepted (with the requested period, for the energy data check in web),
  rejected (with the answer texts), no answer, not requested (with the reason: not registered, not assigned, no or no
  active period, not found; these were skipped silently before), and requested again later. The single answer
  notifications stay unchanged. The energy data itself is not tracked: the backend drops CR_MSG since 12.06.2026.
  - No schema change: the request is an additional notification row (process `ENERGY_REQUEST`), hidden from
    Messages; reads are id ranges on the primary key.
  - A check every 5 minutes (and at start) writes the summaries; an advisory lock keeps it to one summary per request.
  - Answers are matched by community id and metering point (GEA tenants sharing a community id included). An answer
    belongs to the request before it; a later request for the same metering point (also a single one, stored without
    summary) takes the answers after it.
  - A request gets no summary if the backend (re)starts more than about an hour after its deadline (the start looks
    back 25 hours).
  - Answers carry no period: an answer that arrives after a later request for the same metering point (also for
    another period) counts for the later one; the earlier summary lists the metering point as requested again.
  - **Deploy order:** web with the matching release first (platform#173), else Messages shows the summary as an
    empty entry.
- CI: PR checks (tests on every pull request) and a security scan (leaked secrets in new commits,
  vulnerable dependencies, Dockerfile/Kubernetes misconfigurations). Dependency and misconfiguration
  findings are only reported and do not block; a leaked secret in new commits fails the check. Jobs for
  known failing tests are report-only.
- CI: every run keeps its scan and test results as artifacts for 30 days (`scan-*`,
  `test-results-<job>`, `known-errors-<job>`) for a CI report collector; the secret scan keeps
  outcomes and counts only.

## [1.3.2] – 2026-10-17

### Added
- GraphQL requests without the admin role are logged (preparation for enforcing the role). Requests
  pass as before; one INFO line `gql-admin-check: non-admin token` per GraphQL operation, with tenant,
  client id (`azp`), `sub`, number of access groups, member flag (`is_user`), superuser flag and
  operation type/name. This log is temporary and is removed when the role is enforced.

### Fixed
- **MQTT command with an invalid online flag** (F2). A `pontononlinestate` command whose `online` field is
  not a boolean is now logged and dropped. Before, it stopped the message broker and with it the service.
- **Negative page size of the process history** (F4). In `GET /process/history` a negative page size (`ps`)
  is now rejected with 400; before, it crashed the request (recovered per request). Paging behaviour is unchanged.
- **Size limit for the master data import** (F5). An uploaded Excel workbook may now unzip to at most
  128 MiB in total (before: the excelize default of 16 GB); larger files are rejected. The shipped test
  workbooks and typical templates unzip to a few MB.
  Temporary files of an import are written to a directory of their own, which is removed afterwards, also
  when the file is rejected.

## [1.3.1] – 2026-10-16

### Fixed
- **Participation factor after an owner change** (support ticket 66110076). After an owner change a
  metering point exists twice in a community: the old member's entry (moved, inactive) and the new
  member's.
  - The confirmation of the grid operator (ANTWORT_CPF) wrote the new factor to both entries, so the old
    member's history got it too. Now only the current entry (flag 1) is updated; the EDA answer names no
    member, so an answer arriving after an owner change goes to the new member.
  - The factor field in the member form failed for such a metering point („No metering factor found
    N:2“), because the lookup matched the entries by number only. It now finds the member's own entry
    and changes only that one.
  - A factor answer that matches no entry is logged instead of being dropped silently.
  - Editing such a metering point (save in the metering point form) answered 400 „More as one or zero
    Meteringpoint are found!“ after the change was already stored, and the mail on deactivation left out
    the direction: the lookup of the current entry also matched the moved entry. It now finds the
    current entry once.

## [1.3.0] – 2026-10-14

### Changed
- **Excel import reports new members without a direct debit type** (platform#143, review F): one
  warning with the number of members the import created with an IBAN but without „Einzugsart“
  (column missing, empty or unknown) — they are not debited until a type is set (web: an empty type
  is no longer collected as CORE, team decision 14.10.).
  Members that already exist are not counted; the import only adds metering points to them.
- Import messages for an unknown or conflicting direct debit type are warnings with their own codes
  (`W_DEBIT_TYPE_UNKNOWN`, `W_DEBIT_TYPE_CONFLICT`) instead of the generic error `E_UNDEFINED_0`.
- **EEG master data export without the column „SEPA“** (platform#143, review H): the EEG switch
  „SEPA aktiv“ is no longer read since web#132 (`base.eeg.sepa` is still written, not read).

### Added
- **Excel master data import reads the direct debit type** (platform#143). Optional column
  „Einzugsart“ (header „DebitType“ works too): `CORE`, `B2B`, `NONE` or „Kein SEPA“; case, blanks
  and hyphens are ignored. Without the column, or with an empty cell, the type stays empty; such a
  member is not debited. An unknown value is reported in the import
  notification and left empty. A member on several rows takes the first non-empty type; a different
  one on a later row is reported. Like every master data column, it only applies to members the
  import creates: for an existing member the import only adds metering points.

## [1.2.0] – 2026-10-11

### Added
- **Mail to the member when a metering point is no longer part of the community**
  (platform#116, concept `konzept-zaehlpunkt-inaktiv-mail.md`). Subject "Dein Zählpunkt ist nicht
  mehr Teil der Energiegemeinschaft"; it names the metering point, its direction, the end date
  (consent end) and who ended the data release. The community gets a copy (Cc), same sending path
  as the activation mail. Triggers:
  - `AUFHEBUNG_CCMI` (grid operator) and `AUFHEBUNG_CCMC` (member): only when the metering point
    really went from status ACTIVE to INACTIVE; a redelivery, a metering point that was not active
    yet, or a revocation that could not be applied sends no mail.
  - EEG deregistration: only with the grid operator's confirmation (`ANTWORT_CCMS` with code 176),
    not with the community's own request (`AUFHEBUNG_CCMS`) and not with a rejection.
  - No mail for manual changes (archiving, editing in the UI or admin).

  One mail per metering point and member address (a GEA with several tenants per RC number sends it
  once). Parallel MQTT handlers cannot send it twice: the revoked rows are locked (`FOR UPDATE`)
  before their previous status is read, and the commit error is checked before any mail goes out.
  A failed mail never undoes the revocation; the community gets an error notification. Without a
  consent end the mail names no date. New
  global template `zp-inactive-mail-template` (embedded, can be overridden per tenant like the other
  templates).

### Fixed
- **Revocations of the data release were dropped when a metering point exists in several
  communities.** `AUFHEBUNG_CCMI`/`AUFHEBUNG_CCMC` carry no community id; the lookup also matched
  rows without consent id in other communities and gave up with "Meteringpoint … is not unique"
  (13× in Prod within 30 days). The metering point stayed active, and neither history nor
  notification was written. The lookup now tries the receiving community (MQTT topic) and the exact
  consent id first. The receiving community is resolved via its RC number, so a GEA with several
  tenants per RC number (`GC100019-001`, `-002`, …) is revoked in all of them; a hit in several
  tenants is only accepted when they share one RC number. The migration placeholder consent id
  `Migration` counts as "no consent id". A row of another community without consent id is only
  used when the receiving community is unknown, so a revocation can no longer end another
  community's participation. If it still fails, the message is kept in the history of the
  receiving community (all its tenants, without notification) instead of being lost.
- `ANTWORT_CCMS` without code 176 is now written to the history as well (without notification).
- eda tests compile again (`FindMeteringByStatus` without context).

### Changed
- **Grid operator of a metering point is derived from its number** (platform#107). On create,
  participant registration, Excel import, update and when the metering point number changes, the
  backend sets `grid_operator_id` to the first 8 characters of the metering point number, for EEG
  and BEG alike. Values sent by the client are never taken over: a full update derives from the
  number in the path, and a partial update of `gridOperatorId`/`gridOperatorName` is refused (400).
  Any other partial update fills in the grid operator when none is stored yet; this includes
  system updates such as `activesince`/`inactivesince` from EDA answers, and a failing lookup there
  does not stop the update. A metering point number whose first 8 characters are not `AT` + 6
  digits is not derived: an existing value is kept, a new metering point gets none (the web
  dialog no longer accepts such numbers). The name comes from `base.gridoperators` (lowest name
  per id; fallback: the EEG's own grid operator name).
  **Effect on the EDA receiver:** for **BEG**, `getReceiverFrom` takes the metering point's stored
  value, so every BEG metering point written after the release is sent to the derived operator.
  For **EEG** the receiver stays the EEG's grid operator, except for the participation factor
  change (CPF), where the web sends the stored metering point value.
  **Deploy order:** this backend before eegfaktura-web with platform#107 (the web no longer
  prefills the grid operator; with an old backend a new BEG metering point would get none).
- New config `grid-operator-alias` translates grid operator numbers that are only reachable under
  another number (Energienetze Steiermark `AT008200` … → `AT008000`). Read once at start; invalid,
  chained or circular entries are logged as ERROR and ignored, an empty list as WARN. Sub-operators
  that are reachable themselves (e.g. Netz OÖ `AT003470`) are not on the list.
- Excel import: the column "Netzbetreiber" is optional and only compared. A different value or an
  alias translation is reported as `W_GRID_OPERATOR_IGNORED` in the import log; rows are no longer
  skipped because column A is empty (`E_PARTICIPANT_1002` is gone). The header row is also found
  via the "Zählpunkt" column.
- `scripts/nb-alias-107/`: check and correction scripts for existing metering points (run by the
  operator).
- `config.yaml`: default `eda-process-versions` raised to the schema sets valid since 2026-10-05
  (ANFORDERUNG_ECON 02.40, ECOF 02.30, ECP 02.10, CPF 01.10). The grid operators deactivated the
  old sets, so the old defaults were rejected by the Ponton messenger. Needs eda-xp >= 1.0.7.

## [1.1.4] – 2026-10-05

### Security
- The `PARTICIPANT_TENANT_ENFORCE` switch is gone: access to a participant of another
  community is now always refused. The switch (introduced in 1.1.1 for a log-only rollout
  phase) could turn the tenant check into logging only; every environment runs with the check
  on. An environment that still sets `PARTICIPANT_TENANT_ENFORCE=false` is no longer affected
  by it.

## [1.1.3] – 2026-10-05

### Security
- The admin gRPC participant update (`UpdateParticipantValues`) applies the same
  rules as the REST partial update: the participant must belong to the given
  tenant, and every key must name an updatable field; all keys are checked
  before the first write.
- Adding a new version of a tariff (`POST /eeg/tariff` with an existing `id`)
  is only possible for a tariff of the caller's own tenant, and deactivating
  the previous version is scoped to the tenant as well.

### Fixed
- `Test_RegisterMeteringPoint` expects the participant tenant query added in
  #59.

## [1.1.2] – 2026-10-05

### Security
- Partial-update endpoints (metering point, participant) now resolve the
  client-supplied field name against the target model and reject names that are
  unknown or not updatable, instead of passing them to the SQL builder verbatim.
  New helper `model.AllowedUpdateColumn` / `model.IsAllowedParticipantUpdatePath`
  with unit tests.
- GraphQL `updateEegModel` and `masterDataUpload` now take the tenant from the
  verified request context (as the `eeg` query already does) and ignore the
  tenant passed as an argument.
- The EEG update (`POST /eeg`, GraphQL and the admin gRPC call) now maps every
  field to a known, updatable column of the EEG and rejects anything else; until
  now an unknown key was passed to the SQL builder as a column name verbatim. New
  helper `model.ResolveFlatUpdateColumn`, which also covers the embedded address,
  account, contact and website fields and accepts column names such as
  `creditor_id` that the web sends.
- The generic EEG update drops write-protected fields (`tenant`, `rcNumber`,
  `communityId`, `online`, `createdAt`) instead of writing them; the web sends
  single fields, other callers may round-trip the whole object. (#58)
- The full participant update (`PUT /participant/{id}`) checks the tenant before
  any write, including the child tables (addresses, contact, bank data). (#57)
- Moving a metering point and registering one on a participant check the tenant
  of the target participant as well. (#59)
- The `/master` API (basic auth) requires the `EEG_ADMIN` group, like the
  token-based endpoints. (#59)

### Fixed
- After the Ponton registration admin-backend could no longer switch a community
  online: the generic EEG update drops `online` since the write-protection change,
  so the admin gRPC call now sets it through the dedicated online-state update.

## [1.1.1] – 2026-10-04

### Security
- **Five single-participant operations ignored the tenant.** `GET`/`PUT`/`DELETE` on a
  participant, the partial update and the confirm step all resolved the row by `id` alone, so
  an authenticated user who knew a participant ID of a *different* community could read,
  change or delete that record — including bank details, contact data and addresses. The
  middleware did validate the `tenant` header against the token, but the verified value was
  only ever written to the log, never applied to the query. IDs offer no protection either:
  they come from `uuid.NewUUID()`, which is time-based rather than random.
  All five now go through `assertParticipantTenant` first. Reported by an external
  contributor who reviewed the code and reported privately rather than opening an issue.
  A staged rollout is possible: `PARTICIPANT_TENANT_ENFORCE=false` logs cross-tenant access
  without rejecting it, so an environment can be observed before the check is switched on.
  The full `PUT /participant/{id}` and the metering-point updates were already scoped
  correctly and served as the template.
- `google.golang.org/grpc` 1.81.0 -> 1.83.1, closing CVE-2026-84304 (HIGH): heap memory
  exhaustion through HTTP/2 DATA frame fragmentation. 1.82.1 — the version Dependabot
  originally proposed — only closes the earlier GHSA-hrxh-6v49-42gf, which is why the bump
  went straight to 1.83.1. The gRPC server is cluster-internal rather than exposed at the
  ingress, which limits who can reach it, but does not remove the exposure. (#41)
- `google.golang.org/grpc` 1.83.1 -> 1.83.2 (Dependabot #50), patch release on top of the
  CVE-2026-84304 fix above.

### Fixed
- Two database connections were leaked on every `archiveTariff` call: both lookup queries
  discarded their `*sql.Rows` without closing them, and Go sets no finalizer on `Rows`, so
  those connections never returned to the pool. `getGridOperators` leaked the same way on its
  scan-error path and ignored `rows.Err()`. Both are admin-triggered and rare, so they do not
  by themselves explain the production pool exhaustions of 2026-07-19 and 2026-08-11 — see #45.
- A participant created through the API without a `residentAddress` block no longer produces an
  address row with an empty `type`. Every read path joins `base.address` on
  `type = 'RESIDENCE'` / `'BILLING'`, so such a row is invisible: the member list of the *whole
  tenant* fails with `converting NULL to string is unsupported`, and the update path matches no
  rows, so the address cannot be repaired through the UI either. Seen in production on
  2026-09-08 and 2026-09-11 in two tenants. Rows already broken need a separate data repair.

### Added
- CI builds `env/**` branches and deploys the resulting image into the matching feature
  environment (ADR-0008): a push to `env/<name>` pins this service in namespace `env-<name>`
  to that branch's `sha-…` image. Previously only the default branch, tags and `preview/**`
  produced an image at all. The environment itself is still provisioned manually.
- The connection pool counters (`open`, `inUse`, `idle`, `maxOpen`, `waitCount`,
  `waitDuration`) are now logged once a minute, and at `WARN` once `inUse` reaches 80% of
  `maxOpen`. This is diagnostic groundwork for #45: it distinguishes a leaking pool
  (`inUse` climbing monotonically and never falling back) from a merely saturated one
  (`waitCount` growing while `inUse` fluctuates) — a question two production incidents left
  open. Interval configurable via `database.statsLogInterval`; a negative value disables it.

## [1.1.0] – 2026-09-07

### Security
- `github.com/xuri/excelize/v2` 2.10.1 -> 2.11.0, closing CVE-2026-54063 (CVSS 7.5). The
  library parses the master-data spreadsheets users upload, so the vulnerable code is on a
  path reachable with attacker-supplied input — unlike the x/crypto advisories, this one is
  worth taking seriously. Resolution also moved `x/crypto` 0.52.0 -> 0.53.0 and `x/net`
  0.54.0 -> 0.56.0 plus the usual indirects.

### Changed
- Excel master-data import: the "Gemeinschafts-ID" column (marked required in the template)
  is now actually enforced. Every data row must carry the community id of the EEG the file
  is uploaded into (case-insensitive); rows with a different id — the classic "wrong file /
  wrong community selected" mistake — and rows with an **empty** cell are rejected and
  reported in the import notification (one message per distinct wrong id). Previously the
  column was ignored entirely, so a file for another community imported without any warning.
  **Note for existing files:** templates that left the column empty must fill it in once.
  Test fixture alignment: the test-DB EEG `TE100200` now carries the same community id as
  the fixture workbook and the env-billing seed (`AT00999900000TC100200000000000002` —
  the SQL fixture was the lone outlier).

### Changed
- Excel master-data import hardening (follow-up to the field fixes):
  - "Zählpunktstatus" now tolerates case and surrounding spaces (` active ` no longer
    rejects the row).
  - An unknown "Energierichtung" value (e.g. a typo like `GENERATON`) now rejects the
    row with an import notification instead of silently importing the metering point as
    CONSUMPTION — a producer imported as consumer corrupts billing. An empty value keeps
    the documented CONSUMPTION default.
  - Duplicate "MitgliedsNr" values are reported as warnings in the import notification:
    numbers used by several members within the file, and numbers already assigned to a
    *different* existing member (re-import rows carrying the member's own number stay
    silent). Rows are still imported — the warning is informational.
- New cleaned import template `tests/260716-vorlage-import-stammdaten.xlsx`: the nine
  columns the importer never reads (Ortsgebiet, Stiege/Stock/Tür/Adresszusatz,
  Überschusseinspeisung, Energiequelle, Verteilungsmodell, Meter Codes) are removed so
  admins no longer fill fields that silently go nowhere. Import stays header-driven, so
  older templates keep working; a regression test pins the shipped template to the
  importer.

### Fixed
- Excel master-data import: three fields from the current import template
  ("250310-vorlage-import-stammdaten") were imported wrongly or not at all:
  - **"Mitglied seit"** is now stored as the member's `participantSince`. Previously the
    importer read a "Dokument unterschrieben" column that does not exist in the template,
    and `saveParticipant` unconditionally overwrote the value with the import date — every
    imported member appeared to have joined "today". The overwrite now only applies as a
    default when no date is provided (also honors a caller-supplied date on registration).
  - **"registriert seit"** (metering point registered-since) now accepts real Excel date
    cells. Rows are read in raw mode, so date-formatted cells arrive as Excel serial
    numbers (e.g. `45292`), which the previous `d.m.yyyy`-only parser rejected — the value
    silently fell back to Jan 1 of the current year. Serial and text dates are now both
    parsed (same for the mandate date). The metering point's `registeredSince` also comes
    from this column now instead of "Mitglied seit" (crossed wiring with the fix above).
  - **"Zugeteilte Menge in Prozent"** now feeds the participation factor (`partFact`).
    The importer only knew the legacy "Teilnehmerfaktor"/"PartFact" headers, so the
    template column never matched and every metering point got 100 %. Plain numbers,
    `%`-suffixed values, decimal commas and percent-formatted cells (raw fraction, e.g.
    `0.5` = 50 %) are handled; the legacy headers still work as fallback.
- Excel master-data import robustness:
  - A failing member no longer aborts the whole import. Each member runs in its own
    transaction; errors are collected and reported in the import notification while the
    remaining rows are still imported (previously everything after the first failure —
    e.g. a duplicate active metering point — was silently dropped).
  - Silently skipped rows now surface in the import notification: rows that look like
    data but have a missing/invalid "Netzbetreiber" (column A), and rows whose name
    cannot be derived ("Name 1" empty and "Name 2" not splittable). A trailing space in
    the "Netzbetreiber" value no longer discards the row (value is trimmed).
- Test suite: the `database` package tests had drifted uncompilable (missing
  `context.Context` arguments after the DAO signature change, stale `createdAt`
  expectation) and are fixed to compile and pass again; new regression tests cover the
  import fixes (incl. an end-to-end continue-on-error/re-import test on its own tenant).
- EDA: `eda-process-versions.AUFHEBUNG_CCMS` bumped `01.10` → `01.30` in the committed
  (local-dev) `config.yaml`. This string is stamped onto the outbound `MessageCodeVersion`
  (`mqtt/messageBroker.go`) and eda-xp uses it to pick the CMRevoke XSD + `schemaLocation`
  (`CMRevokeRequest.getVersion`): `01.10` builds the superseded `cmrevoke/01p00` schema,
  `01.30` the current `cmrevoke/01p10` (`CM_REV_SP/01.30`). Prod already ran `01.30`; the
  repo default and dev overlays had drifted behind — aligned so new environments don't
  emit revocations under an outdated EDA process version.

## [1.0.7] – 2026-07-05

### Added
- The EEG entity now exposes its creation date (`base.eeg.createdat`) via the API as
  `createdAt` (ISO `YYYY-MM-DD`). The column already existed; it is now mapped read-only
  (`skipinsert`/`skipupdate`, DB default `now()` stays authoritative). The web billing
  period selector uses it as the lower bound for EEGs without energy data, so quarterly
  billing runs (e.g. the platform-fee EEG `RC000000`) stay selectable after the quarter
  they belong to has passed.

## [1.0.6] – 2026-07-05

### Fixed
- Mail delivery no longer fails on recipient addresses with leading/trailing whitespace
  (a prod log review found 73 failed sends across 11 tenants in one week, most of them
  addresses like `' mail@x.at'`): both send paths (`SendMail` and — previously completely
  unvalidated — `SendMailWithAttachment`, the ZP list mail) now normalize to/cc per
  `;`-separated part (unicode trim incl. NBSP) and send the **normalized** value, validated
  against a shared address rule (`model.ValidateEmailList`: ASCII local part, TLD ≥ 2 letters,
  no TLD allowlist). A failed ZP list mail now raises an `N_TYPE_ERROR` admin notification
  instead of being log-only.

### Added
- Server-side e-mail enforcement on every write path (the web form alone was the only guard):
  participant create/update/partial-update (`contact.email`), the EEG master data e-mail
  (recipient of the ZP list mail) and the Excel master-data import all normalize and validate
  the address before persisting. Invalid addresses are rejected (API) or imported without
  e-mail plus a visible import-log entry (Excel); an address that is empty after trimming is
  stored as NULL so the send-path guard (`Contact.Email.Valid`) stays meaningful.
- `mail.proto`: additive `SendMailReply.rejectedRecipients` field — the mail server (eda-xp)
  can report recipients it refused; both senders surface them as an error so callers raise the
  existing admin notification instead of losing recipients silently. Backward compatible (old
  eda-xp simply never sets the field); Go stubs regenerated.

### Changed
- CI: Preview-Deployments (ADR-0007) — Push auf `preview/**` baut+deployt on-demand in die Dev-Zone (sha-pinned, kein `:latest`), Auto-Reset bei Branch-Delete.
- Mail templates are now embedded in the binary (`public/templates`) as defaults and resolved
  through an `fs.FS`: at runtime a per-tenant templates dir on the data volume still overrides
  them first, then the global dir; only when neither holds the requested file are the embedded
  defaults used. A fresh deployment therefore renders the activation and ZP-completion mails
  (template, config and inline logo) without any template being hand-seeded onto the PVC, while
  operators keep full per-tenant/global override control on the volume. `ParseTemplate` and
  `ReadActivationMailTemplateConfig` now take an `fs.FS`; the stale unused `parser/templates`
  embed (old logo) was dropped in favour of the single `public/templates` source.

### Fixed
- `TestReadActivationMailTemplateConfig` asserted the wrong inline picture name (`Logo_Faktura.png`);
  the global activation template references `eegfaktura-logo.png`.

## [1.0.5] – 2026-07-04

### Fixed
- ZP completion ("Zählpunkt aktiv") mail: removed a redundant `<br>` before "Mit besten Grüßen".
  Combined with the paragraph's own margin it produced two blank lines in a row; the normal
  single paragraph gap remains.
- ZP completion ("Zählpunkt aktiv") mail never rendered: the `zp-complete-mail-template`
  references `{{.MeteringPoint}}`, but the template data only exposed `Meteringpoints []string`
  → `can't evaluate field MeteringPoint` → "Error Sending Mail" on every completion. Add a
  `MeteringPoint` field to the template data so the mail renders. (#19)
- Mail template resolution now falls back to the global templates dir when a tenant is missing
  the *specific* template file (previously only when the whole tenant template dir was missing),
  fixing "Config file is missing" for the completion mail on tenants that only have the
  activation template. (#19)
- ZP completion mail: `{{.Eeg.ContactPerson}}` rendered the raw `null.String` struct
  (`{{value true}}`); use `.String` with a `Valid` guard like the phone line. (#19)

### Changed
- ZP completion mail template now matches the activation mail: informal "du" wording,
  identical signature/footer (description, address, phone/email/website, "versandt durch"),
  and the logo capped at `max-height: 90px`. (#19)
- ZP completion mail gets its own subject "Dein Zählpunkt ist aktiv" instead of reusing
  "Aktivierung im Serviceportal"; `meteringPointPerformAnswerMsg` now takes the subject as a
  parameter (activation mail keeps its subject). (#19)
- Tests: `trimString` now also strips `\r` so golden template comparisons are CRLF-insensitive;
  `TestGetTemplateFor` builds its expected path with `filepath.Join` (OS-independent);
  `TestManualSending` is skipped unless `RUN_MANUAL_MAIL_TESTS` is set (needs a live mail service). (#19)

## [1.0.4] – 2026-07-01

### Fixed
- Admin master update: the `INACTIVESINCE` update never took effect because the
  parsed inactive-since timestamp was scanned into the `activeSince` variable, so
  `inactiveSince` stayed invalid and the handler returned 501. Scan it into
  `inactiveSince` (also fixes the process-state → INACTIVE path). (#17)

## [1.0.3] – 2026-06-30

### Fixed
- Register goqu's postgres dialect so prepared queries bind `$1` placeholders instead of `?` (fixes EEG loading failing with `pq: syntax error`). (#14)
- SQL injection: bind the `json_to_recordset` input in `MeteringPointChangePartFactor` instead of string-interpolating it. (#15)
- Security: `getEegById`/`getEegByEcId` now build their queries with goqu
  prepared statements (bind parameters) instead of interpolated SQL, removing
  the Snyk Code SQL-injection findings on `database/eegDao.go`. (Snyk `go/Sqli`)

## [1.0.2] – 2026-06-29

### Fixed
- EDA Consent Management (`CM_REV_SP`): a rejection (`ABLEHNUNG_CCMS`) arrives
  without a `<meter>` element, which dereferenced a nil pointer and crashed the
  whole backend; the MQTT broker then crash-looped (QoS-1 redelivery) for every
  tenant. The metering point and reason codes are now read from `responseData`,
  the rejection is recorded as a notification, and the data release is kept
  active (the metering point is no longer revoked on a rejection). Additionally,
  any panic inside an MQTT protocol handler is now recovered so a single message
  can never take down the process. (#10)

## [1.0.1] – 2026-06-28

### Fixed
- Notifications: `notification.date` is stored in UTC instead of the server's local
  wall-clock time; fixes a TZ-offset shift in the displayed time. (#6)

## [1.0.0] – 2026-06-28

First production release built entirely from public source (unified
source-build cutover of the eegfaktura suite).

### Fixed
- Auth: authorize via `access_groups` (`/EEG_ADMIN`, `/EEG_USER`) instead of realm
  roles. (#5)

### Changed
- CI: self-building Dockerfile from a fresh clone (stage-1 source build); push to the
  registry's development tier with an auto-rollout bridge (dispatch-deploy). (#2, #3)
- Added README with service overview and tech stack. (#4)
