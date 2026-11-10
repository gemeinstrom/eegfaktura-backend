package database

import (
	"context"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// base.tariff."baseFee" is double precision: one tariff with decimals must not fail the whole list (261026-mm1).
func TestTariffListWithDecimalBaseFee(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)

	const tenant = "TE000032"
	const decimal, whole = "5f0c1a2e-0b7d-4c55-9a51-7d3f2b6a0021", "5f0c1a2e-0b7d-4c55-9a51-7d3f2b6a0022"
	cleanup := func() {
		_, err := testDB.DbInstance.Exec(`DELETE FROM base.tariff WHERE tenant = $1`, tenant)
		require.NoError(t, err)
		_, err = testDB.DbInstance.Exec(`DELETE FROM base.eeg WHERE tenant = $1`, tenant)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
	require.NoError(t, db.InsertEeg(context.Background(), tenant, registerEegFixture(tenant, tenant, "Tarif-Test")))
	_, err = testDB.DbInstance.Exec(`INSERT INTO base.tariff (id, tenant, type, name, version, "baseFee")
		VALUES ($1, $3, 'EEG', 'Mit Komma', 0, 2.25), ($2, $3, 'EEG', 'Ganz', 0, 3)`, decimal, whole, tenant)
	require.NoError(t, err)

	tariffs, err := db.GetTariff(context.Background(), tenant)
	require.NoError(t, err)
	fees := map[string]model.FloatOrString{}
	for _, tariff := range tariffs {
		fees[tariff.Id.String()] = tariff.BaseFee
	}
	assert.Equal(t, map[string]model.FloatOrString{decimal: 2.25, whole: 3}, fees)

	// the web sends the whole tariff back on save: the amount stays
	var update model.Tariff
	for _, tariff := range tariffs {
		if tariff.Id.String() == decimal {
			update = tariff
		}
	}
	require.NoError(t, db.AddTariff(context.Background(), tenant, "test", &update))
	history, err := db.GetTariffHistory(context.Background(), tenant, decimal)
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, model.FloatOrString(2.25), history[0].BaseFee)
	assert.Equal(t, model.FloatOrString(2.25), history[1].BaseFee)
}
