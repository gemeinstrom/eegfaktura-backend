package database

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"at.ourproject/vfeeg-backend/database/dbtest"
	"at.ourproject/vfeeg-backend/model"
	"github.com/golang-migrate/migrate/v4"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// version before 20261028130000_joining_fee
const migrationBeforeJoiningFee = 20261028120100
const joiningFeeMigration = 20261028130000

var joiningFeeViewColumns = []string{"tariff_joining_fee", "tariff_joining_fee_use_vat", "tariff_joining_fee_vat_in_percent"}

type viewColumn struct {
	Name     string `db:"column_name"`
	DataType string `db:"data_type"`
}

func viewColumns(t *testing.T, dbx *sqlx.DB, table string) []viewColumn {
	t.Helper()
	var cols []viewColumn
	require.NoError(t, dbx.Select(&cols, `SELECT column_name, data_type FROM information_schema.columns
		WHERE table_schema = 'base' AND table_name = $1 ORDER BY ordinal_position`, table))
	require.NotEmpty(t, cols, table)
	return cols
}

func columnNames(cols []viewColumn) []string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return names
}

// migrationDB creates an empty database in the test container and returns it with a migrator on it.
func migrationDB(t *testing.T, name string) (*sqlx.DB, *migrate.Migrate) {
	t.Helper()
	_, dbx := testDB.CreateDatabase(t, name)
	_, path, _, ok := runtime.Caller(0)
	require.True(t, ok)
	m, err := migrate.New("file:"+filepath.Dir(path)+"/../migrations",
		fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable", dbtest.DbUser, dbtest.DbPass, testDB.DbAddress, name))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	return dbx, m
}

// Migration 20261028130000_joining_fee: three columns on base.tariff, appended at the end of both views; the
// down migration restores the views as after ZVT and keeps the columns with their values (/grill-me 27.10.2026).
func TestJoiningFeeMigrationUpDown(t *testing.T) {
	dbx, m := migrationDB(t, "joining_fee_migration_test")
	// exactly up to this migration: a later one that appends view columns must not break the checks below
	require.NoError(t, m.Migrate(joiningFeeMigration))

	var tariffCols []struct {
		Name      string  `db:"column_name"`
		DataType  string  `db:"data_type"`
		Precision *int    `db:"numeric_precision"`
		Scale     *int    `db:"numeric_scale"`
		Nullable  string  `db:"is_nullable"`
		Default   *string `db:"column_default"`
	}
	require.NoError(t, dbx.Select(&tariffCols, `SELECT column_name, data_type, numeric_precision, numeric_scale,
		is_nullable, column_default FROM information_schema.columns
		WHERE table_schema = 'base' AND table_name = 'tariff' AND column_name LIKE 'joiningFee%' ORDER BY column_name`))
	require.Len(t, tariffCols, 3)
	assert.Equal(t, "joiningFee", tariffCols[0].Name)
	assert.Equal(t, "numeric", tariffCols[0].DataType)
	require.NotNil(t, tariffCols[0].Precision)
	require.NotNil(t, tariffCols[0].Scale)
	assert.Equal(t, 10, *tariffCols[0].Precision)
	assert.Equal(t, 2, *tariffCols[0].Scale)
	assert.Equal(t, "joiningFeeUseVat", tariffCols[1].Name)
	assert.Equal(t, "boolean", tariffCols[1].DataType)
	assert.Equal(t, "joiningFeeVatInPercent", tariffCols[2].Name)
	assert.Equal(t, "numeric", tariffCols[2].DataType)
	for _, c := range tariffCols {
		assert.Equal(t, "NO", c.Nullable, c.Name)
		assert.NotNil(t, c.Default, c.Name)
	}

	billing := viewColumns(t, dbx, "billing_masterdata")
	n := len(billing)
	assert.Equal(t, []viewColumn{
		{"eec_bank_creditor_id", "text"},
		{"tariff_joining_fee", "numeric"},
		{"tariff_joining_fee_use_vat", "boolean"},
		{"tariff_joining_fee_vat_in_percent", "numeric"},
	}, billing[n-4:], "new columns at the end of base.billing_masterdata")
	active := viewColumns(t, dbx, "activetariff")
	assert.Equal(t, []string{"timeTariff2CentPerKWh", "joiningFee", "joiningFeeUseVat", "joiningFeeVatInPercent"},
		columnNames(active[len(active)-4:]))

	// a tariff with a fee survives the down migration
	const tariffId = "5f0c1a2e-0b7d-4c55-9a51-7d3f2b6a0001"
	_, err := dbx.Exec(`INSERT INTO base.tariff (id, tenant, type, name, version, "joiningFee", "joiningFeeUseVat",
		"joiningFeeVatInPercent") VALUES ($1, 'TE000027', 'EEG', 'Beitrag', 0, 49.90, true, 20)`, tariffId)
	require.NoError(t, err)

	require.NoError(t, m.Migrate(migrationBeforeJoiningFee))
	assert.Equal(t, columnNames(billing[:n-3]), columnNames(viewColumns(t, dbx, "billing_masterdata")),
		"down: base.billing_masterdata as after ZVT")
	assert.Equal(t, columnNames(active[:len(active)-3]), columnNames(viewColumns(t, dbx, "activetariff")),
		"down: base.activeTariff as after ZVT")
	var kept string
	require.NoError(t, dbx.Get(&kept, `SELECT concat("joiningFee", '/', "joiningFeeUseVat", '/', "joiningFeeVatInPercent")
		FROM base.tariff WHERE id = $1`, tariffId))
	assert.Equal(t, "49.90/t/20", kept, "down keeps the columns and their values")

	// up again finds the existing columns
	require.NoError(t, m.Migrate(joiningFeeMigration))
	assert.Equal(t, columnNames(billing), columnNames(viewColumns(t, dbx, "billing_masterdata")))
	var fee string
	require.NoError(t, dbx.Get(&fee, `SELECT "joiningFee"::text FROM base.activetariff WHERE id = $1`, tariffId))
	assert.Equal(t, "49.90", fee)
}

// Tariff write and read paths carry the joining fee exactly; versions keep their own value; the view hands the
// member's EEG tariff fee to billing with fallback 0/false; another tenant does not see the tariff.
func TestJoiningFeeTariffAndBillingView(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)
	const tenant = "TE000027"

	tariff := model.Tariff{Type: model.EEG, Name: "Beitrag mit Beitrittsgebühr", ParticipantFee: 2,
		JoiningFee: 49.90, JoiningFeeUseVat: true, JoiningFeeVatInPercent: 20}
	require.NoError(t, db.AddTariff(context.Background(), tenant, "test", &tariff))
	id := tariff.Id.String()

	var stored string
	require.NoError(t, testDB.DbInstance.Get(&stored, `SELECT "joiningFee"::text FROM base.tariff WHERE id = $1`, id))
	assert.Equal(t, "49.90", stored, "stored exactly")

	find := func(tariffs []model.Tariff) *model.Tariff {
		for i := range tariffs {
			if tariffs[i].Id.String() == id {
				return &tariffs[i]
			}
		}
		return nil
	}
	tariffs, err := db.GetTariff(context.Background(), tenant)
	require.NoError(t, err)
	got := find(tariffs)
	require.NotNil(t, got)
	assert.Equal(t, 49.90, got.JoiningFee)
	assert.True(t, got.JoiningFeeUseVat)
	assert.Equal(t, model.IntOrString(20), got.JoiningFeeVatInPercent)

	foreign, err := db.GetTariff(context.Background(), "TE000004")
	require.NoError(t, err)
	assert.Nil(t, find(foreign), "tariff of another tenant")

	// new version with another fee; the history keeps both
	update := *got
	update.JoiningFee = 25.5
	update.JoiningFeeUseVat = false
	update.JoiningFeeVatInPercent = 0
	require.NoError(t, db.AddTariff(context.Background(), tenant, "test", &update))
	history, err := db.GetTariffHistory(context.Background(), tenant, id)
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, 1, history[0].Version)
	assert.Equal(t, 25.5, history[0].JoiningFee)
	assert.False(t, history[0].JoiningFeeUseVat)
	assert.Equal(t, 0, history[1].Version)
	assert.Equal(t, 49.90, history[1].JoiningFee)
	assert.Equal(t, model.IntOrString(20), history[1].JoiningFeeVatInPercent)

	// billing_masterdata: member with the EEG tariff gets the active version, member without falls back to 0/false
	const withFee, withoutFee = "5f0c1a2e-0b7d-4c55-9a51-7d3f2b6a0011", "5f0c1a2e-0b7d-4c55-9a51-7d3f2b6a0012"
	_, err = testDB.DbInstance.Exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, "createdBy",
		"lastModifiedBy", "tariffId") VALUES ($1, $3, 'Mit', 'Gebühr', 'test', 'test', $4),
		($2, $3, 'Ohne', 'Gebühr', 'test', 'test', NULL)`, withFee, withoutFee, tenant, id)
	require.NoError(t, err)
	type row struct {
		Fee    string `db:"tariff_joining_fee"`
		UseVat bool   `db:"tariff_joining_fee_use_vat"`
		Vat    string `db:"tariff_joining_fee_vat_in_percent"`
	}
	view := func(participant string) row {
		var r row
		require.NoError(t, testDB.DbInstance.Get(&r, `SELECT tariff_joining_fee::text, tariff_joining_fee_use_vat,
			tariff_joining_fee_vat_in_percent::text FROM base.billing_masterdata WHERE participant_id = $1`, participant))
		return r
	}
	assert.Equal(t, row{"25.50", false, "0"}, view(withFee))
	assert.Equal(t, row{"0", false, "0"}, view(withoutFee))
}
