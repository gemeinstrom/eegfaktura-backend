package database

import (
	"context"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// support ticket 66110076: after an owner change a metering point exists twice in a community — the
// old member's moved entry (flag 0, INACTIVE) and the new member's current entry (flag 1). A factor
// change must reach exactly one of them, and the member form must find one entry per member.
func TestMeteringPointChangePartFactor_afterOwnerChange(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)

	const tenant, meter = "TE000017", "AT0020000000000000000000100129467"
	const oldOwner, newOwner = "b1170000-0000-0000-0000-000000000001", "b1170000-0000-0000-0000-000000000002"
	t.Cleanup(func() {
		_, _ = testDB.DbInstance.Exec(`DELETE FROM base.participant WHERE id IN ($1, $2)`, oldOwner, newOwner)
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
		VALUES ($1, $2, $3, 'GENERATION', 'INACTIVE', 'INACTIVE', now(), 'test', '2023-07-06', '2023-07-06', '2025-03-02', 0)`,
		meter, oldOwner, tenant)
	exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
		"modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince, flag)
		VALUES ($1, $2, $3, 'GENERATION', 'ACTIVE', 'ACTIVE', now(), 'test', '2025-03-05', '2025-03-06', '2999-12-31', 1)`,
		meter, newOwner, tenant)
	for p, f := range map[string]int{oldOwner: 100, newOwner: 50} {
		exec(`INSERT INTO base.metering_partition_factor (metering_point_id, participant_id, tenant, "partFact", "createdBy")
			VALUES ($1, $2, $3, $4, 'test')`, meter, p, tenant, f)
	}
	factor := func(participant string) int {
		var f int
		require.NoError(t, testDB.DbInstance.Get(&f, `SELECT "partFact" FROM base.metering_partition_factor
			WHERE tenant = $1 AND metering_point_id = $2 AND participant_id = $3 ORDER BY version DESC LIMIT 1`,
			tenant, meter, participant))
		return f
	}
	change := func(participant string, f int) {
		require.NoError(t, db.MeteringPointChangePartFactor(ctx, tenant, participant, []model.Meter{
			{MeteringPoint: meter, Direction: model.GENERATOR, PartFact: f},
		}))
	}

	t.Run("ANTWORT_CPF (no participant) changes only the current entry", func(t *testing.T) {
		change("", 99)
		assert.Equal(t, 99, factor(newOwner))
		assert.Equal(t, 100, factor(oldOwner), "the old member's history keeps its factor")
	})
	t.Run("member form changes only that member's entry", func(t *testing.T) {
		before := factor(newOwner)
		change(oldOwner, 80)
		assert.Equal(t, 80, factor(oldOwner))
		assert.Equal(t, before, factor(newOwner))
	})
	t.Run("member form finds one entry per member", func(t *testing.T) {
		for _, p := range []string{oldOwner, newOwner} {
			ms, err := db.FindAllMeteringByTenant(ctx, tenant, p, []string{meter})
			require.NoError(t, err)
			require.Len(t, ms, 1, "one entry for member %s", p)
		}
	})
	t.Run("the current entry is found once (metering point update, inactive mail)", func(t *testing.T) {
		m, err := db.FindAssignedMeteringById(ctx, tenant, meter)
		require.NoError(t, err)
		assert.Equal(t, newOwner, m.ParticipantId)
	})
}
