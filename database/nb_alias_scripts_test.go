package database

import (
	"context"

	"at.ourproject/vfeeg-backend/model"
	"gopkg.in/guregu/null.v4"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// platform#107: scripts/nb-alias-107/check.sql finds wrong stored grid operators, fix.sql corrects
// them (alias list, name fallback to the EEG) and leaves excluded metering points alone.
func TestNbAliasScripts(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)

	const tenant = "TE000010"
	const communityId = "AT00800000000TC000010000000000001"
	t.Cleanup(func() { removeTestTenant(t, tenant) })
	_, err = testDB.DbInstance.Exec(`INSERT INTO base.eeg
		(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
		 street, "streetNumber", city, zip, email)
		VALUES ('TE000010','NB-ALIAS-TEST','Testgemeinschaft #107','TE000010','LOCAL','AT008000','Energienetze Steiermark',
		 '` + communityId + `','Weg','1','Weiz','8160','nb-alias-test@example.org')
		ON CONFLICT (tenant) DO NOTHING`)
	require.NoError(t, err)

	row := func(name, zp string) []interface{} {
		return []interface{}{"", communityId, "8160", "Weiz", "Weg", "1",
			zp, "CONSUMPTION", name, "NbAlias", "privat", "", "", "", "ACTIVE", "", ""}
	}
	f := buildImportSheet(t, [][]interface{}{
		row("Alt", "AT0082000816000000000000000107001"),
		row("Eigen", "AT0080000816000000000000000107002"),
		row("Zurueck", "AT0082000816000000000000000107003"),
	})
	buf, err := f.WriteToBuffer()
	require.NoError(t, err)
	require.NoError(t, db.ImportMasterdataFromExcel(ctx, buf, "test.xlsx", "EEG Stammdaten", tenant))

	// simulate the state before #107: old grid operator numbers stored
	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET grid_operator_id = 'AT008200', grid_operator_name = 'alt'
		WHERE tenant = 'TE000010' AND metering_point_id IN ('AT0082000816000000000000000107001', 'AT0082000816000000000000000107003')`)
	require.NoError(t, err)
	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET grid_operator_id = NULL
		WHERE tenant = 'TE000010' AND metering_point_id = 'AT0080000816000000000000000107002'`)
	require.NoError(t, err)

	// fix.sql corrects ALL tenants: run everything in one transaction and roll it back, so the
	// fixture tenants of other tests stay untouched
	tx, err := testDB.DbInstance.Beginx()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	check := func() []string {
		sql, err := os.ReadFile("../scripts/nb-alias-107/check.sql")
		require.NoError(t, err)
		rows, err := tx.Queryx(string(sql))
		require.NoError(t, err)
		defer rows.Close()
		var found []string
		for rows.Next() {
			r := map[string]interface{}{}
			require.NoError(t, rows.MapScan(r))
			if r["tenant"] == tenant {
				found = append(found, r["metering_point_id"].(string))
			}
		}
		return found
	}
	assert.ElementsMatch(t, []string{
		"AT0082000816000000000000000107001", "AT0080000816000000000000000107002", "AT0082000816000000000000000107003",
	}, check())

	fix, err := os.ReadFile("../scripts/nb-alias-107/fix.sql")
	require.NoError(t, err)
	stmt := strings.Replace(string(fix), "BEGIN;", "", 1)

	// the script as shipped (empty exclusion list) must run as well
	_, err = tx.Exec("SAVEPOINT shipped")
	require.NoError(t, err)
	_, err = tx.Exec(stmt)
	require.NoError(t, err)
	_, err = tx.Exec("ROLLBACK TO SAVEPOINT shipped")
	require.NoError(t, err)

	stmt = strings.Replace(stmt, "(NULL::varchar)  -- e.g. ('AT0040000530000000000000005026510'),",
		"(NULL::varchar), ('AT0082000816000000000000000107003')", 1)
	_, err = tx.Exec(stmt)
	require.NoError(t, err)

	assert.Equal(t, []string{"AT0082000816000000000000000107003"}, check())

	var id, name string
	require.NoError(t, tx.QueryRowx(`SELECT grid_operator_id, grid_operator_name FROM base.meteringpoint
		WHERE tenant = 'TE000010' AND metering_point_id = 'AT0082000816000000000000000107001'`).Scan(&id, &name))
	assert.Equal(t, "AT008000", id)
	assert.NotEqual(t, "alt", name)
}

// removeTestTenant deletes a tenant created by a test (participants cascade to their metering
// points, addresses, bank accounts and contact details; the import writes a notification), so it
// does not stay in the shared test database.
func removeTestTenant(t *testing.T, tenant string) {
	for _, stmt := range []string{
		`DELETE FROM base.notification WHERE tenant = $1`,
		`DELETE FROM base.participant WHERE tenant = $1`,
		`DELETE FROM base.eeg WHERE tenant = $1`,
	} {
		_, err := testDB.DbInstance.Exec(stmt, tenant)
		require.NoError(t, err)
	}
}

// platform#107: a partial update re-derives the grid operator when the metering point number
// changes, fills it in when nothing is stored yet, and leaves a stored value alone otherwise.
func TestUpdateMeteringPointPartial_gridOperator(t *testing.T) {
	ctx := context.Background()
	db, err := GetTestDB(ctx, testDB)
	require.NoError(t, err)

	defer func(orig func() map[string]string) { gridOperatorAlias = orig }(gridOperatorAlias)
	gridOperatorAlias = func() map[string]string { return map[string]string{"AT008200": "AT008000"} }

	const tenant = "TE000014"
	const communityId = "AT00800000000TC000014000000000001"
	t.Cleanup(func() { removeTestTenant(t, tenant) })
	_, err = testDB.DbInstance.Exec(`INSERT INTO base.eeg
		(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
		 street, "streetNumber", city, zip, email)
		VALUES ('TE000014','NB-PARTIAL-TEST','Testgemeinschaft #107','TE000014','LOCAL','AT008000','Energienetze Steiermark',
		 '` + communityId + `','Weg','1','Weiz','8160','nb-partial-test@example.org')
		ON CONFLICT (tenant) DO NOTHING`)
	require.NoError(t, err)

	const empty, stored, renamed = "AT0082000816000000000000000107011", "AT0030000000000000000000000107012", "AT0082000816000000000000000107013"
	row := func(zp string) []interface{} {
		return []interface{}{"", communityId, "8160", "Weiz", "Weg", "1",
			zp, "CONSUMPTION", "Partial", "NbAlias", "privat", "", "", "", "ACTIVE", "", ""}
	}
	f := buildImportSheet(t, [][]interface{}{row(empty), row(stored)})
	buf, err := f.WriteToBuffer()
	require.NoError(t, err)
	require.NoError(t, db.ImportMasterdataFromExcel(ctx, buf, "test.xlsx", "EEG Stammdaten", tenant))

	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET grid_operator_id = NULL, grid_operator_name = NULL
		WHERE tenant = 'TE000014' AND metering_point_id = $1`, empty)
	require.NoError(t, err)
	_, err = testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET grid_operator_id = 'AT009999', grid_operator_name = 'manuell'
		WHERE tenant = 'TE000014' AND metering_point_id = $1`, stored)
	require.NoError(t, err)

	gridOperator := func(zp string) (id, participantId string, name *string) {
		require.NoError(t, testDB.DbInstance.QueryRowx(`SELECT COALESCE(grid_operator_id, ''), grid_operator_name, participant_id
			FROM base.meteringpoint WHERE tenant = 'TE000014' AND metering_point_id = $1`, zp).Scan(&id, &name, &participantId))
		return
	}
	update := func(zp string, values map[string]interface{}) {
		_, participantId, _ := gridOperator(zp)
		require.NoError(t, db.UpdateMeteringPointPartial(ctx, tenant, "test", participantId, zp, values))
	}

	update(empty, map[string]interface{}{"equipmentName": "WP"})
	id, _, _ := gridOperator(empty)
	assert.Equal(t, "AT008000", id, "empty value is filled in")

	update(stored, map[string]interface{}{"equipmentName": "WP"})
	id, _, name := gridOperator(stored)
	assert.Equal(t, "AT009999", id, "stored value stays")
	require.NotNil(t, name)
	assert.Equal(t, "manuell", *name)

	update(stored, map[string]interface{}{"metering_point_id": renamed})
	id, _, _ = gridOperator(renamed)
	assert.Equal(t, "AT008000", id, "new metering point number derives again")

	// full update: derived from the number in the path, the client's value is ignored even with
	// an empty or different number in the body
	_, participantId, _ := gridOperator(renamed)
	require.NoError(t, db.UpdateMeteringPoint(ctx, tenant, "test", participantId, renamed, &model.MeteringPoint{
		MeteringPoint: "", GridOperatorId: null.StringFrom("AT009999"), GridOperatorName: null.StringFrom("manuell"),
		Status: model.S_ACTIVE, State: &model.MeterState{},
	}))
	id, _, _ = gridOperator(renamed)
	assert.Equal(t, "AT008000", id, "full update derives from the path number")
}
