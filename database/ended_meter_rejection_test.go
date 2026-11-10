package database

import (
	"context"
	"database/sql"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/jjeffery/civil"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// konzept-zaehlpunkt-ausblenden.md, part 2: a rejection of a metering point whose participation has
// ended keeps the process state INACTIVE (any code), one of an ACTIVE metering point keeps it ACTIVE
// for codes about the request only (model.REJECTED_KEEP_ACTIVE_CODES, decision 24.10.2026); the
// rejected state goes to last_process_state, the code to statusCode. Never-active (INIT) rows, and
// active ones with other codes, take the rejected state as before.

const endedRejectParticipant = "b1180000-0000-0000-0000-000000000001"

type endedRejectRow struct {
	ProcessState     string         `db:"process_state"`
	LastProcessState sql.NullString `db:"last_process_state"`
	Status           string         `db:"status"`
	StatusCode       sql.NullInt64  `db:"statusCode"`
	ModifiedBy       sql.NullString `db:"modifiedBy"`
	ActiveSince      sql.NullTime   `db:"activesince"`
}

func insertEndedRejectFixture(t *testing.T, tenant string, rows map[string][3]string) {
	insertEndedRejectFixtureIn(t, testDB.DbInstance, tenant, rows)
}

func insertEndedRejectFixtureIn(t *testing.T, dbx *sqlx.DB, tenant string, rows map[string][3]string) {
	exec := func(q string, args ...interface{}) {
		_, err := dbx.Exec(q, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
		VALUES ($1, $2, 'Beendet', 'Abgelehnt', 'ACTIVE', 'test', 'test')`, endedRejectParticipant, tenant)
	t.Cleanup(func() {
		_, _ = dbx.Exec(`DELETE FROM base.participant WHERE id = $1`, endedRejectParticipant)
	})
	// rows: meter -> {status, process_state, inactivesince ("" = NULL)}
	for meter, r := range rows {
		var inactive interface{}
		if r[2] != "" {
			inactive = r[2]
		}
		exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
			"modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince)
			VALUES ($1, $2, $3, 'CONSUMPTION', $4, $5, now(), 'test', '2024-03-06', '2024-03-06', $6)`,
			meter, endedRejectParticipant, tenant, r[0], r[1], inactive)
	}
}

func readEndedRejectRow(t *testing.T, q interface {
	QueryRowx(string, ...interface{}) *sqlx.Row
}, tenant, meter string) endedRejectRow {
	t.Helper()
	var r endedRejectRow
	require.NoError(t, q.QueryRowx(`SELECT process_state, last_process_state, status, "statusCode", "modifiedBy", activesince
		FROM base.meteringpoint WHERE tenant = $1 AND metering_point_id = $2 AND flag = 1`, tenant, meter).StructScan(&r))
	return r
}

func TestMeteringPointsSetStatus_endedMeterRejection(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)

	const tenant = "TE000018"
	const (
		endedInvalid  = "AT0030000000000000000000990018001"
		endedRejected = "AT0030000000000000000000990018002"
		neverActive   = "AT0030000000000000000000990018003"
		active        = "AT0030000000000000000000990018004"
		endedNoDate   = "AT0030000000000000000000990018005"
		active188     = "AT0030000000000000000000990018006"
		activePending = "AT0030000000000000000000990018007"
		activeRevoked = "AT0030000000000000000000990018008"
		revokedNever  = "AT0030000000000000000000990018009"
	)
	insertEndedRejectFixture(t, tenant, map[string][3]string{
		endedInvalid:  {"INACTIVE", "INIT", "2025-01-22"},
		endedRejected: {"INACTIVE", "PENDING", "2025-01-22"},
		neverActive:   {"INIT", "PENDING", ""},
		active:        {"ACTIVE", "ACTIVE", "2999-12-31"},
		endedNoDate:   {"INACTIVE", "INIT", ""},
		active188:     {"ACTIVE", "ACTIVE", "2999-12-31"},
		activePending: {"ACTIVE", "INIT", "2999-12-31"},
		activeRevoked: {"ACTIVE", "REVOKED", "2999-12-31"},
		revokedNever:  {"INACTIVE", "INIT", "2025-01-22"},
	})
	// never active (no activesince), ended by a revocation (CCMS): not an ended participation
	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET activesince = NULL WHERE tenant = $1 AND metering_point_id = $2`,
		tenant, revokedNever)
	require.NoError(t, err)

	code57, code99, code188 := int16(57), int16(99), int16(188)
	require.NoError(t, db.MeteringPointsSetStatus(ctx, tenant, model.PENDING, nil, []string{activePending}, nil, nil))
	require.NoError(t, db.MeteringPointsSetStatus(ctx, tenant, model.INVALID, &code188,
		[]string{active188, activePending, activeRevoked}, nil, nil))
	require.NoError(t, db.MeteringPointsSetStatus(ctx, tenant, model.INVALID, &code57,
		[]string{endedInvalid, neverActive, active, endedNoDate, revokedNever}, nil, nil))
	require.NoError(t, db.MeteringPointsSetStatus(ctx, tenant, model.REJECTED, &code99,
		[]string{endedRejected}, nil, nil))

	r := readEndedRejectRow(t, testDB.DbInstance, tenant, endedInvalid)
	assert.Equal(t, "INACTIVE", r.ProcessState, "ended meter stays INACTIVE")
	assert.Equal(t, "INVALID", r.LastProcessState.String, "rejected state kept for traceability")
	assert.Equal(t, "INACTIVE", r.Status)
	assert.Equal(t, int64(57), r.StatusCode.Int64)
	assert.Equal(t, "EVU", r.ModifiedBy.String)

	r = readEndedRejectRow(t, testDB.DbInstance, tenant, endedRejected)
	assert.Equal(t, "INACTIVE", r.ProcessState)
	assert.Equal(t, "REJECTED", r.LastProcessState.String)
	assert.Equal(t, int64(99), r.StatusCode.Int64)

	r = readEndedRejectRow(t, testDB.DbInstance, tenant, neverActive)
	assert.Equal(t, "INVALID", r.ProcessState, "never-active meter: as before")
	assert.False(t, r.LastProcessState.Valid)
	assert.Equal(t, "INIT", r.Status)
	assert.Equal(t, int64(57), r.StatusCode.Int64)

	r = readEndedRejectRow(t, testDB.DbInstance, tenant, revokedNever)
	assert.Equal(t, "INVALID", r.ProcessState, "INACTIVE without activesince (never active): as before")
	assert.False(t, r.LastProcessState.Valid)

	r = readEndedRejectRow(t, testDB.DbInstance, tenant, endedNoDate)
	assert.Equal(t, "INVALID", r.ProcessState, "INACTIVE without inactivesince: as before")
	assert.False(t, r.LastProcessState.Valid)

	// an ACTIVE row stays ACTIVE when the code concerns the request, not the metering point (188
	// participation factor > 100 %, decision 24.10.2026); activesince is not touched
	r = readEndedRejectRow(t, testDB.DbInstance, tenant, active188)
	assert.Equal(t, "ACTIVE", r.ProcessState, "active meter stays ACTIVE on code 188")
	assert.Equal(t, "INVALID", r.LastProcessState.String)
	assert.Equal(t, "ACTIVE", r.Status)
	assert.Equal(t, int64(188), r.StatusCode.Int64)
	assert.Equal(t, "2024-03-06", r.ActiveSince.Time.Format("2006-01-02"))
	// a restore after code 86 keeps it ACTIVE (the stored rejection is not restored)
	require.NoError(t, db.RestoreMeteringPointProcessState(ctx, tenant, active188))
	assert.Equal(t, "ACTIVE", readEndedRejectRow(t, testDB.DbInstance, tenant, active188).ProcessState)

	// INIT → PENDING of the request just rejected goes back to ACTIVE; REVOKED (or any other state) stays
	r = readEndedRejectRow(t, testDB.DbInstance, tenant, activePending)
	assert.Equal(t, "ACTIVE", r.ProcessState, "active meter, request INIT→PENDING + 188: ACTIVE")
	assert.Equal(t, "INVALID", r.LastProcessState.String)
	assert.Equal(t, int64(188), r.StatusCode.Int64)
	r = readEndedRejectRow(t, testDB.DbInstance, tenant, activeRevoked)
	assert.Equal(t, "REVOKED", r.ProcessState, "active meter, REVOKED + 188: unchanged")
	assert.Equal(t, "INVALID", r.LastProcessState.String)

	// a code about the metering point itself (57 not supplied) shows the inconsistency: INVALID as before
	r = readEndedRejectRow(t, testDB.DbInstance, tenant, active)
	assert.Equal(t, "INVALID", r.ProcessState, "active meter + code 57: INVALID as before")
	assert.False(t, r.LastProcessState.Valid)
	assert.Equal(t, "ACTIVE", r.Status)
	assert.Equal(t, int64(57), r.StatusCode.Int64)

	// a later rejection with COMPETING_PROCESSES restores the state before the registration attempt:
	// an ended meter goes back to INACTIVE, not to the stored rejection
	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET process_state = 'INIT'
		WHERE tenant = $1 AND metering_point_id = $2`, tenant, endedInvalid)
	require.NoError(t, err)
	require.NoError(t, db.RestoreMeteringPointProcessState(ctx, tenant, endedInvalid))
	r = readEndedRejectRow(t, testDB.DbInstance, tenant, endedInvalid)
	assert.Equal(t, "INACTIVE", r.ProcessState)

	// other process states (e.g. PENDING) on an ended meter are unchanged
	require.NoError(t, db.MeteringPointsSetStatus(ctx, tenant, model.PENDING, nil, []string{endedRejected}, nil, nil))
	r = readEndedRejectRow(t, testDB.DbInstance, tenant, endedRejected)
	assert.Equal(t, "PENDING", r.ProcessState)
}

// Review backend#107: a successful re-registration clears last_process_state, so a later restore
// after code 86 does not set the now ACTIVE meter back to the old rejection.
func TestEndedMeterRejection_reRegisteredActiveIgnoresOldRejection(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)

	const tenant, meter = "TE000020", "AT0030000000000000000000990020001"
	insertEndedRejectFixture(t, tenant, map[string][3]string{meter: {"INACTIVE", "INIT", "2025-01-22"}})

	code57 := int16(57)
	require.NoError(t, db.MeteringPointsSetStatus(ctx, tenant, model.INVALID, &code57, []string{meter}, nil, nil))
	require.Equal(t, "INVALID", readEndedRejectRow(t, testDB.DbInstance, tenant, meter).LastProcessState.String)

	require.NoError(t, db.MeteringPointsSetStatus(ctx, tenant, model.INIT, nil, []string{meter}, nil, nil))
	today := civil.Today()
	require.NoError(t, db.MeteringPointsSetStatus(ctx, tenant, model.ACTIVE, nil, []string{meter}, &today, nil))
	r := readEndedRejectRow(t, testDB.DbInstance, tenant, meter)
	require.Equal(t, "ACTIVE", r.ProcessState)
	assert.False(t, r.LastProcessState.Valid, "the old rejection is cleared")
	// as on the "already assigned" path (code ZP_ALREADY_ASSIGNED → ACTIVE): activesince stays (COALESCE)
	assert.Equal(t, "2024-03-06", r.ActiveSince.Time.Format("2006-01-02"))

	require.NoError(t, db.RestoreMeteringPointProcessState(ctx, tenant, meter))
	assert.Equal(t, "ACTIVE", readEndedRejectRow(t, testDB.DbInstance, tenant, meter).ProcessState)
}

// Review backend#107: the restore after code 86 sets an ended meter back to INACTIVE only when the
// competing attempt left it at INIT; a re-registration in flight (PENDING/APPROVED) stays, and a
// stored rejection is never taken for an ended meter.
func TestEndedMeterRejection_restoreKeepsRegistrationInFlight(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)

	const tenant = "TE000021"
	const pending, approved, initial = "AT0030000000000000000000990021001", "AT0030000000000000000000990021002",
		"AT0030000000000000000000990021003"
	insertEndedRejectFixture(t, tenant, map[string][3]string{
		pending:  {"INACTIVE", "PENDING", "2025-01-22"},
		approved: {"INACTIVE", "APPROVED", "2025-01-22"},
		initial:  {"INACTIVE", "INIT", "2025-01-22"},
	})
	// a stale rejection must not be restored
	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET last_process_state = 'INVALID' WHERE tenant = $1`, tenant)
	require.NoError(t, err)

	for _, m := range []string{pending, approved, initial} {
		require.NoError(t, db.RestoreMeteringPointProcessState(ctx, tenant, m))
	}
	assert.Equal(t, "PENDING", readEndedRejectRow(t, testDB.DbInstance, tenant, pending).ProcessState)
	assert.Equal(t, "APPROVED", readEndedRejectRow(t, testDB.DbInstance, tenant, approved).ProcessState)
	assert.Equal(t, "INACTIVE", readEndedRejectRow(t, testDB.DbInstance, tenant, initial).ProcessState)
}

// Review backend#107: a revocation (AUFHEBUNG_CCMI/CCMC) of an entry that still carries a rejection
// ends it INACTIVE like the rule above, instead of producing the INACTIVE + INVALID state the
// cleanup fixes. WasActive (the mail) still follows the status only.
func TestEndedMeterRejection_revokeByConsentEndsInactive(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)

	const tenant = "TE000022"
	const activeInvalid, neverActive = "AT0030000000000000000000990022001", "AT0030000000000000000000990022002"
	const inactiveNever = "AT0030000000000000000000990022003"
	_, err = testDB.DbInstance.Exec(`INSERT INTO base.eeg
		(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
		 street, "streetNumber", city, zip, email)
		VALUES ($1, 'ENDED-REJECT', 'Ended-Reject-Test', $2, 'LOCAL', 'AT003000', 'Netz OÖ',
		 'AT00300000000TC000022000000000001', 'Weg', '1', 'Linz', '4020', 'ended-reject@example.org')
		ON CONFLICT (tenant) DO NOTHING`, tenant, tenant)
	require.NoError(t, err)
	t.Cleanup(func() { removeTestTenant(t, tenant) })
	insertEndedRejectFixture(t, tenant, map[string][3]string{
		activeInvalid: {"ACTIVE", "INVALID", "2999-12-31"},
		neverActive:   {"INIT", "REJECTED", ""},
		inactiveNever: {"INACTIVE", "INVALID", "2025-01-22"},
	})
	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET consent_id = 'C-' || right(metering_point_id, 3),
		activesince = CASE WHEN metering_point_id = $2 THEN NULL ELSE activesince END
		WHERE tenant = $1`, tenant, inactiveNever)
	require.NoError(t, err)

	end := civil.DateFor(2026, 10, 1)
	consent := "C-001"
	revoked, err := db.MeteringPointRevokeByConsentId(ctx, "", &consent, activeInvalid, end)
	require.NoError(t, err)
	require.Len(t, revoked, 1)
	assert.True(t, revoked[0].WasActive)
	r := readEndedRejectRow(t, testDB.DbInstance, tenant, activeInvalid)
	assert.Equal(t, "INACTIVE", r.Status)
	assert.Equal(t, "INACTIVE", r.ProcessState)
	assert.Equal(t, "INVALID", r.LastProcessState.String)

	consent = "C-002"
	revoked, err = db.MeteringPointRevokeByConsentId(ctx, "", &consent, neverActive, end)
	require.NoError(t, err)
	require.Len(t, revoked, 1)
	assert.False(t, revoked[0].WasActive)
	r = readEndedRejectRow(t, testDB.DbInstance, tenant, neverActive)
	assert.Equal(t, "INIT", r.Status)
	assert.Equal(t, "REJECTED", r.ProcessState, "never-active entry: as before")
	assert.False(t, r.LastProcessState.Valid)

	// INACTIVE without activesince (never active, revoked before via CCMS): as before
	consent = "C-003"
	_, err = db.MeteringPointRevokeByConsentId(ctx, "", &consent, inactiveNever, end)
	require.NoError(t, err)
	r = readEndedRejectRow(t, testDB.DbInstance, tenant, inactiveNever)
	assert.Equal(t, "INVALID", r.ProcessState)
	assert.False(t, r.LastProcessState.Valid)
}

// Re-review backend#107 (4): an applied rejection leaves no stale last_process_state. A rejection
// without code (ABBRUCH) keeps the old statusCode (web shows it); the cleanup takes only INVALID as
// the active variant, so an abort (REJECTED) with an old 188 is not counted there.
func TestEndedMeterRejection_appliedRejectionClearsStaleValues(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)

	const tenant = "TE000023"
	const neverActive, active = "AT0030000000000000000000990023001", "AT0030000000000000000000990023002"
	insertEndedRejectFixture(t, tenant, map[string][3]string{
		neverActive: {"INIT", "PENDING", ""},
		active:      {"ACTIVE", "PENDING", "2999-12-31"},
	})
	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET last_process_state = 'INVALID', "statusCode" = 188
		WHERE tenant = $1`, tenant)
	require.NoError(t, err)

	require.NoError(t, db.MeteringPointsSetStatus(ctx, tenant, model.REJECTED, nil, []string{neverActive, active}, nil, nil))
	for _, m := range []string{neverActive, active} {
		r := readEndedRejectRow(t, testDB.DbInstance, tenant, m)
		assert.Equal(t, "REJECTED", r.ProcessState, m)
		assert.False(t, r.LastProcessState.Valid, "no stale last_process_state: %s", m)
		assert.Equal(t, int64(188), r.StatusCode.Int64, "abort without code keeps the old statusCode: %s", m)
	}

	code57 := int16(57)
	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET last_process_state = 'REJECTED' WHERE tenant = $1`, tenant)
	require.NoError(t, err)
	require.NoError(t, db.MeteringPointsSetStatus(ctx, tenant, model.INVALID, &code57, []string{neverActive}, nil, nil))
	r := readEndedRejectRow(t, testDB.DbInstance, tenant, neverActive)
	assert.Equal(t, "INVALID", r.ProcessState)
	assert.False(t, r.LastProcessState.Valid)
	assert.Equal(t, int64(57), r.StatusCode.Int64)
}
