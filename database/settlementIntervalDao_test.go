package database

import (
	"context"
	"sync"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const settlementTenant = "TE000230"

type settlementChangeRow struct {
	OldValue        string  `db:"old_value"`
	NewValue        string  `db:"new_value"`
	EffectiveFrom   *string `db:"effective_from"`
	LastFinalPeriod *string `db:"last_final_period"`
	ChangedBy       string  `db:"changed_by"`
}

// settlementFixture creates the tenant with interval MONTHLY and no record; removed afterwards.
func settlementFixture(t *testing.T) {
	t.Helper()
	cleanup := func() {
		_, _ = testDB.DbInstance.Exec(`DELETE FROM base.settlement_interval_change WHERE tenant = $1`, settlementTenant)
		_, _ = testDB.DbInstance.Exec(`DELETE FROM base.eeg WHERE tenant = $1`, settlementTenant)
	}
	cleanup()
	_, err := testDB.DbInstance.Exec(`INSERT INTO base.eeg
		(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
		 street, "streetNumber", city, zip, email, "settlementInterval")
		VALUES ($1, 'INTERVALL', 'Intervall-Test', $2, 'LOCAL', 'AT003000', 'Netz OÖ', 'AT00300000000TC000230000000000001',
		 'Weg', '1', 'Linz', '4020', 'intervall@example.org', 'MONTHLY')`, settlementTenant, settlementTenant)
	require.NoError(t, err)
	t.Cleanup(cleanup)
}

func settlementState(t *testing.T) (string, []settlementChangeRow) {
	t.Helper()
	var interval string
	require.NoError(t, testDB.DbInstance.Get(&interval, `SELECT "settlementInterval" FROM base.eeg WHERE tenant = $1`, settlementTenant))
	var rows []settlementChangeRow
	require.NoError(t, testDB.DbInstance.Select(&rows, `SELECT old_value, new_value, effective_from::text AS effective_from,
		last_final_period, changed_by FROM base.settlement_interval_change WHERE tenant = $1 ORDER BY id`, settlementTenant))
	return interval, rows
}

func TestChangeSettlementInterval(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)
	settlementFixture(t)
	billedToFeb := model.BillingState{FinalPeriods: []string{"Abr_YM-2026-1", "Abr_YM-2026-2"}}
	billedToMar := model.BillingState{FinalPeriods: []string{"Abr_YM-2026-3"}, RunningPeriods: []string{}}

	t.Run("dry run writes nothing", func(t *testing.T) {
		d, err := db.ChangeSettlementInterval(ctx, settlementTenant, model.SettlementQuarter, "admin", billedToMar, true)
		require.NoError(t, err)
		assert.True(t, d.Allowed)
		interval, rows := settlementState(t)
		assert.Equal(t, model.SettlementMonthly, interval)
		assert.Empty(t, rows)
	})
	t.Run("blocked change writes nothing", func(t *testing.T) {
		d, err := db.ChangeSettlementInterval(ctx, settlementTenant, model.SettlementQuarter, "admin", billedToFeb, false)
		require.NoError(t, err)
		assert.False(t, d.Allowed)
		assert.Equal(t, "2026-04-01", *d.EarliestFrom)
		assert.Equal(t, model.SettlementMonthly, d.OldInterval, "for the log")
		interval, rows := settlementState(t)
		assert.Equal(t, model.SettlementMonthly, interval)
		assert.Empty(t, rows)
	})
	t.Run("current interval is refused", func(t *testing.T) {
		_, err := db.ChangeSettlementInterval(ctx, settlementTenant, model.SettlementMonthly, "admin", billedToMar, true)
		assert.ErrorIs(t, err, ErrSettlementIntervalUnchanged)
	})
	t.Run("allowed change is stored with its record", func(t *testing.T) {
		d, err := db.ChangeSettlementInterval(ctx, settlementTenant, model.SettlementQuarter, "admin", billedToMar, false)
		require.NoError(t, err)
		assert.True(t, d.Allowed)
		interval, rows := settlementState(t)
		assert.Equal(t, model.SettlementQuarter, interval)
		require.Len(t, rows, 1)
		assert.Equal(t, settlementChangeRow{OldValue: "MONTHLY", NewValue: "QUARTER", EffectiveFrom: d.EffectiveFrom,
			LastFinalPeriod: d.LastFinalPeriod, ChangedBy: "admin"}, rows[0])
		assert.Equal(t, "2026-04-01", *rows[0].EffectiveFrom)
	})
	t.Run("without final period: at once, no date in the record", func(t *testing.T) {
		_, err := db.ChangeSettlementInterval(ctx, settlementTenant, model.SettlementAnnual, "admin2", model.BillingState{}, false)
		require.NoError(t, err)
		interval, rows := settlementState(t)
		assert.Equal(t, model.SettlementAnnual, interval)
		require.Len(t, rows, 2)
		assert.Equal(t, settlementChangeRow{OldValue: "QUARTER", NewValue: "ANNUAL", ChangedBy: "admin2"}, rows[1])
	})
	t.Run("unknown tenant", func(t *testing.T) {
		_, err := db.ChangeSettlementInterval(ctx, "TE000239", model.SettlementQuarter, "admin", billedToMar, true)
		assert.ErrorIs(t, err, ErrSettlementIntervalNoEeg)
	})
}

// Two changes at the same time decide one after the other (FOR UPDATE): one is stored, the other sees the new value.
func TestChangeSettlementIntervalConcurrent(t *testing.T) {
	ctx := context.Background()
	db, err := GetDB(ctx)
	require.NoError(t, err)
	settlementFixture(t)

	const n = 4
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = db.ChangeSettlementInterval(ctx, settlementTenant, model.SettlementQuarter, "admin",
				model.BillingState{FinalPeriods: []string{"Abr_YM-2026-3"}}, false)
		}(i)
	}
	wg.Wait()
	stored := 0
	for _, err := range errs {
		if err == nil {
			stored++
		} else {
			assert.ErrorIs(t, err, ErrSettlementIntervalUnchanged)
		}
	}
	assert.Equal(t, 1, stored)
	_, rows := settlementState(t)
	assert.Len(t, rows, 1)
}

// Migration 20261105120000_settlement_interval_change: the table comes with up and goes with down.
func TestSettlementIntervalChangeMigrationUpDown(t *testing.T) {
	dbx, m := migrationDB(t, "settlement_interval_migration_test")
	table := func() *string {
		var name *string
		require.NoError(t, dbx.Get(&name, `SELECT to_regclass('base.settlement_interval_change')::text`))
		return name
	}
	require.NoError(t, m.Migrate(20261105120000))
	require.NotNil(t, table())
	_, err := dbx.Exec(`INSERT INTO base.settlement_interval_change (tenant, old_value, new_value, changed_by)
		VALUES ('TE000230', 'MONTHLY', 'QUARTER', 'admin')`)
	require.NoError(t, err)
	require.NoError(t, m.Migrate(20261028130000))
	assert.Nil(t, table())
	require.NoError(t, m.Migrate(20261105120000))
	assert.NotNil(t, table())
}
