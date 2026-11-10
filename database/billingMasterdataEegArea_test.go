package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const eegAreaMigration = 20261106090000

// Migration 20261106090000 (platform#229): base.billing_masterdata gets eeg_area (= base.eeg.area) as its last
// column; all existing columns keep name, type and position (billing reads the view with m.*). Down restores the
// view as after the joining fee.
func TestBillingMasterdataEegAreaMigrationUpDown(t *testing.T) {
	dbx, m := migrationDB(t, "eeg_area_migration_test")
	require.NoError(t, m.Migrate(joiningFeeMigration))
	before := viewColumns(t, dbx, "billing_masterdata")

	require.NoError(t, m.Migrate(eegAreaMigration))
	after := viewColumns(t, dbx, "billing_masterdata")
	require.Len(t, after, len(before)+1)
	assert.Equal(t, before, after[:len(before)], "existing columns unchanged, same order")
	assert.Equal(t, viewColumn{"eeg_area", "text"}, after[len(after)-1], "new column at the end")

	_, err := dbx.Exec(`INSERT INTO base.eeg (tenant, name, description, "rcNumber", area, gridoperator_code,
		gridoperator_name, "communityId", street, "streetNumber", city, zip, email)
		VALUES ('TE000043', 'P2P', 'P2P-Test', 'TE000043', 'P2P', 'AT009999', 'Test Operator',
		'AT00999900000TC000043000000000001', 'Weg', '1', 'Linz', '4020', 'p2p@example.org')`)
	require.NoError(t, err)
	_, err = dbx.Exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, "createdBy", "lastModifiedBy")
		VALUES ('5f0c1a2e-0b7d-4c55-9a51-7d3f2b6a0043', 'TE000043', 'Peer', 'Mitglied', 'test', 'test')`)
	require.NoError(t, err)
	var area string
	require.NoError(t, dbx.Get(&area, `SELECT eeg_area FROM base.billing_masterdata WHERE tenant_id = 'TE000043'`))
	assert.Equal(t, "P2P", area)

	require.NoError(t, m.Migrate(joiningFeeMigration))
	assert.Equal(t, before, viewColumns(t, dbx, "billing_masterdata"), "down: view as after the joining fee")

	require.NoError(t, m.Migrate(eegAreaMigration))
	assert.Equal(t, after, viewColumns(t, dbx, "billing_masterdata"), "up again")
}
