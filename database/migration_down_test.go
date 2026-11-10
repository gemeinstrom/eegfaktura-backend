package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Tracking 261105-mm3: the down migration of 20250603150318_create_views dropped base.base.billing_masterdata
// (a cross-database name) and dropped activeTariff while billing_masterdata still depended on it. Only this
// step: later down migrations (e.g. 20250627194445) fail on their own while billing_masterdata exists.
func TestCreateViewsMigrationDown(t *testing.T) {
	dbx, m := migrationDB(t, "create_views_down_test")
	require.NoError(t, m.Migrate(20250603150318))
	require.NoError(t, m.Steps(-1))
	var views int
	require.NoError(t, dbx.Get(&views, `SELECT count(*) FROM pg_views WHERE schemaname = 'base'
		AND viewname IN ('activemeteringpartition', 'activetariff', 'billing_masterdata')`))
	require.Zero(t, views)
}
