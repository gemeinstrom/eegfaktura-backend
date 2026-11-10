package database

import (
	"context"
	"fmt"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
)

const zoneLevelMigration = 20261106100000

// Migration 20261106100000 (platform#229): base.eeg.ec_zone_level (1–7) filled from area (GEA 1, LOCAL 3, REGIONAL 4,
// BEG 7, P2P/EVA empty), base.meteringpoint.tech_code/fuel_code empty; base.billing_masterdata stays as it is; down
// drops the three columns.
func TestEcZoneLevelMigrationUpDown(t *testing.T) {
	dbx, m := migrationDB(t, "ec_zone_level_migration_test")
	require.NoError(t, m.Migrate(eegAreaMigration))
	billing := viewColumns(t, dbx, "billing_masterdata")
	areas := map[string]string{"TE000051": "GEA", "TE000052": "LOCAL", "TE000053": "REGIONAL", "TE000054": "BEG",
		"TE000055": "P2P", "TE000056": "EVA"}
	for tenant, area := range areas {
		_, err := dbx.Exec(`INSERT INTO base.eeg (tenant, name, description, "rcNumber", area, gridoperator_code,
			gridoperator_name, "communityId", street, "streetNumber", city, zip, email)
			VALUES ($1, 'Zone', 'Zonen-Test', $4, $2, 'AT009999', 'Test Operator', $3, 'Weg', '1',
			'Linz', '4020', 'zone@example.org')`, tenant, area, "AT0099990000"+tenant, tenant)
		require.NoError(t, err)
	}

	require.NoError(t, m.Migrate(zoneLevelMigration))
	got := map[string]null.Int{}
	rows := []struct {
		Tenant string   `db:"tenant"`
		Zone   null.Int `db:"ec_zone_level"`
	}{}
	require.NoError(t, dbx.Select(&rows, `SELECT tenant, ec_zone_level FROM base.eeg`))
	for _, r := range rows {
		got[r.Tenant] = r.Zone
	}
	assert.Equal(t, map[string]null.Int{"TE000051": null.IntFrom(1), "TE000052": null.IntFrom(3),
		"TE000053": null.IntFrom(4), "TE000054": null.IntFrom(7), "TE000055": {}, "TE000056": {}}, got)
	assert.Equal(t, billing, viewColumns(t, dbx, "billing_masterdata"), "billing_masterdata unchanged")
	mp := viewColumns(t, dbx, "meteringpoint")
	assert.Equal(t, []viewColumn{{"tech_code", "text"}, {"fuel_code", "text"}}, mp[len(mp)-2:])
	_, err := dbx.Exec(`UPDATE base.eeg SET ec_zone_level = 8 WHERE tenant = 'TE000055'`)
	assert.Error(t, err, "CHECK 1–7")

	require.NoError(t, m.Migrate(eegAreaMigration))
	var n int
	require.NoError(t, dbx.Get(&n, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'base'
		AND column_name IN ('ec_zone_level', 'tech_code', 'fuel_code')`))
	assert.Zero(t, n, "down drops the columns")
	assert.Equal(t, billing, viewColumns(t, dbx, "billing_masterdata"))
}

// A new registration gets the Nahebereich of its type (GEA 1, LOCAL 3, REGIONAL 4, BEG 7), P2P and EVA none.
func TestInsertEegZoneLevelDefault(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)
	ctx := context.Background()
	want := map[model.AreaType]null.Int{model.GEA: null.IntFrom(1), model.LOCAL: null.IntFrom(3),
		model.REGIONAL: null.IntFrom(4), model.BEG: null.IntFrom(7), model.P2P: {}, model.EVA: {}}
	cleanup := func() {
		_, err := testDB.DbInstance.Exec(`DELETE FROM base.eeg WHERE tenant LIKE 'TE00006_'`)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
	i := 0
	for area, zone := range want {
		tenant := fmt.Sprintf("TE00006%d", i)
		i++
		eeg := registerEegFixture(tenant, tenant, string(area))
		eeg.Area = area
		require.NoError(t, db.InsertEeg(ctx, tenant, eeg))
		stored, err := db.GetEegById(ctx, tenant)
		require.NoError(t, err)
		assert.Equal(t, zone, stored.EcZoneLevel, area)
	}
}

// UpdateEcZoneLevel sets every tenant of the community id (two in a GEA), not another community; 1–7 only.
func TestUpdateEcZoneLevel(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)
	ctx := context.Background()
	const ecId = "AT00999900000GC000057000000000001"
	tenants := map[string]string{"GC000057": ecId, "GC000057-002": ecId, "GC000058": "AT00999900000GC000058000000000001"}
	cleanup := func() {
		for tenant := range tenants {
			_, err := testDB.DbInstance.Exec(`DELETE FROM base.eeg WHERE tenant = $1`, tenant)
			require.NoError(t, err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	for tenant, communityId := range tenants {
		eeg := registerEegFixture(tenant, "GC000057", "Zone")
		eeg.Area, eeg.CommunityId = model.GEA, communityId
		require.NoError(t, db.InsertEeg(ctx, tenant, eeg))
	}
	zone := func(tenant string) null.Int {
		e, err := db.GetEegById(ctx, tenant)
		require.NoError(t, err)
		return e.EcZoneLevel
	}

	require.NoError(t, db.UpdateEcZoneLevel(ctx, ecId, 6))
	assert.Equal(t, null.IntFrom(6), zone("GC000057"))
	assert.Equal(t, null.IntFrom(6), zone("GC000057-002"))
	assert.Equal(t, null.IntFrom(1), zone("GC000058"), "other community id: the GEA default of the registration")

	for _, invalid := range []int{0, 8, -1} {
		assert.Error(t, db.UpdateEcZoneLevel(ctx, ecId, invalid), invalid)
	}
	assert.Error(t, db.UpdateEcZoneLevel(ctx, "", 3), "no community id")
	assert.Equal(t, null.IntFrom(6), zone("GC000057"))

	// the generic update drops it (write protection like area)
	require.NoError(t, db.UpdateEegPartial(ctx, "GC000057", map[string]interface{}{"ecZoneLevel": 1, "description": "Z"}))
	assert.Equal(t, null.IntFrom(6), zone("GC000057"))
}

// UpdateEegAreaFromList switches LOCAL and REGIONAL at every tenant of the community id and reports the old value;
// GEA tenants of the same community id and other communities stay; only LOCAL or REGIONAL as target.
func TestUpdateEegAreaFromList(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)
	ctx := context.Background()
	const ecId = "AT00999900000RC000090000000000001"
	tenants := map[string][2]string{"TE000090": {ecId, "LOCAL"}, "TE000091": {ecId, "GEA"},
		"TE000092": {"AT00999900000RC000092000000000001", "LOCAL"}}
	cleanup := func() {
		_, err := testDB.DbInstance.Exec(`DELETE FROM base.eeg WHERE tenant IN ('TE000090', 'TE000091', 'TE000092')`)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
	for tenant, v := range tenants {
		eeg := registerEegFixture(tenant, tenant, "Typ")
		eeg.CommunityId, eeg.Area = v[0], model.AreaType(v[1])
		require.NoError(t, db.InsertEeg(ctx, tenant, eeg))
	}
	area := func(tenant string) model.AreaType {
		e, err := db.GetEegById(ctx, tenant)
		require.NoError(t, err)
		return e.Area
	}

	changes, err := db.UpdateEegAreaFromList(ctx, ecId, model.REGIONAL)
	require.NoError(t, err)
	assert.Equal(t, []AreaChange{{Tenant: "TE000090", Old: model.LOCAL}}, changes)
	assert.Equal(t, model.REGIONAL, area("TE000090"))
	assert.Equal(t, model.GEA, area("TE000091"))
	assert.Equal(t, model.LOCAL, area("TE000092"), "other community id")
	zone := func(tenant string) null.Int {
		e, err := db.GetEegById(ctx, tenant)
		require.NoError(t, err)
		return e.EcZoneLevel
	}
	assert.False(t, zone("TE000090").Valid, "the switch clears the Nahebereich (eegfaktura-dev#56 #10)")
	assert.Equal(t, null.IntFrom(1), zone("TE000091"), "GEA not switched, Nahebereich stays")
	assert.Equal(t, null.IntFrom(3), zone("TE000092"), "other community id")

	changes, err = db.UpdateEegAreaFromList(ctx, ecId, model.REGIONAL)
	require.NoError(t, err)
	assert.Empty(t, changes, "nothing to change")

	for _, target := range []model.AreaType{model.GEA, model.BEG, model.P2P, model.EVA, ""} {
		_, err := db.UpdateEegAreaFromList(ctx, ecId, target)
		assert.Error(t, err, target)
	}
	_, err = db.UpdateEegAreaFromList(ctx, "", model.LOCAL)
	assert.Error(t, err)
	assert.Equal(t, model.REGIONAL, area("TE000090"))
}
