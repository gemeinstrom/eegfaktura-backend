package database

import (
	"context"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/model"
	"github.com/jjeffery/civil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// After an owner change a metering point has two entries in a community: the old member's moved entry
// (flag 0, INACTIVE) and the new member's current entry (flag 1). Every entry must carry its own period —
// the energy data request (syncenergy) sent the new member's period twice. (syncenergy requests each entry's part,
// the old member's too: api/syncEnergy_ownerChange_test.go.)
func TestFindMeteringByIdAndState_ownPeriodPerParticipant(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)

	const tenant, meter = "TE000017", "AT0020000000000000000000100129468"
	const oldOwner, newOwner = "b1180000-0000-0000-0000-000000000001", "b1180000-0000-0000-0000-000000000002"
	t.Cleanup(func() {
		// cascades to the metering point and partition factor rows; TE000017 is shared with other tests
		_, err := testDB.DbInstance.Exec(`DELETE FROM base.participant WHERE id IN ($1, $2)`, oldOwner, newOwner)
		assert.NoError(t, err)
	})
	exec := func(q string, args ...interface{}) {
		_, err := testDB.DbInstance.Exec(q, args...)
		require.NoError(t, err)
	}
	for _, p := range []string{oldOwner, newOwner} {
		exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
			VALUES ($1, $2, 'Owner', 'Change', 'ACTIVE', 'test', 'test')`, p, tenant)
	}
	exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
		"modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince, flag)
		VALUES ($1, $2, $3, 'CONSUMPTION', 'INACTIVE', 'INACTIVE', now(), 'test', '2026-01-01', '2026-01-01', '2026-06-15', 0)`,
		meter, oldOwner, tenant)
	exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
		"modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince, flag)
		VALUES ($1, $2, $3, 'CONSUMPTION', 'ACTIVE', 'ACTIVE', now(), 'test', '2026-06-16', '2026-06-16', '2999-12-31', 1)`,
		meter, newOwner, tenant)
	for _, p := range []string{oldOwner, newOwner} {
		exec(`INSERT INTO base.metering_partition_factor (metering_point_id, participant_id, tenant, "partFact", "createdBy")
			VALUES ($1, $2, $3, 100, 'test')`, meter, p, tenant)
	}

	type period struct {
		since, until civil.Date
		flag         model.ProcessFlag
	}
	own := map[string]period{
		oldOwner: {civil.DateFor(2026, 1, 1), civil.DateFor(2026, 6, 15), model.F_MOVED},
		newOwner: {civil.DateFor(2026, 6, 16), civil.DateFor(2999, 12, 31), model.F_ASSIGNED},
	}
	// each participant once, each with its own period and flag
	assertOwnPeriods := func(t *testing.T, ms []*model.MeteringPoint, participants ...string) {
		t.Helper()
		var found []string
		for _, m := range ms {
			if m.MeteringPoint != meter {
				continue
			}
			found = append(found, m.ParticipantId)
			want := own[m.ParticipantId]
			assert.Equal(t, want.since, m.State.ActiveSince.Date, "activesince of %s", m.ParticipantId)
			assert.Equal(t, want.until, m.State.InactiveSince.Date, "inactivesince of %s", m.ParticipantId)
			assert.Equal(t, want.flag, m.State.Flag, "flag of %s", m.ParticipantId)
		}
		assert.ElementsMatch(t, participants, found)
	}

	t.Run("energy data request (FindMeteringByIds)", func(t *testing.T) {
		ms, err := db.FindMeteringByIds(ctx, tenant, []string{meter})
		require.NoError(t, err)
		assertOwnPeriods(t, ms, oldOwner, newOwner)
	})
	t.Run("master data by period (FindMeteringPointsActivePeriod)", func(t *testing.T) {
		from := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC).UnixMilli()
		to := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC).UnixMilli()
		ms, err := db.FindMeteringPointsActivePeriod(ctx, tenant, from, to)
		require.NoError(t, err)
		assertOwnPeriods(t, ms, oldOwner, newOwner)
	})
	t.Run("master data of the community (FindMeteringPointsForTenant)", func(t *testing.T) {
		ms, err := db.FindMeteringPointsForTenant(ctx, tenant)
		require.NoError(t, err)
		assertOwnPeriods(t, ms, oldOwner, newOwner)
	})
	t.Run("revocation (FindActiveMeteringByIds) finds the current entry once", func(t *testing.T) {
		ms, err := db.FindActiveMeteringByIds(ctx, tenant, []string{meter})
		require.NoError(t, err)
		assertOwnPeriods(t, ms, newOwner)
	})
	t.Run("status INACTIVE (FindInactiveMeteringById) does not find the moved entry", func(t *testing.T) {
		// the filter means the entry's own flag: the moved entry (flag 0) is not current, the current one is ACTIVE
		ms, err := db.FindInactiveMeteringById(ctx, tenant, meter)
		require.NoError(t, err)
		assertOwnPeriods(t, ms)
	})
}
