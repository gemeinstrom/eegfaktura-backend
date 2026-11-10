package database

import (
	"context"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
)

func registerEegFixture(tenant, rcNumber, name string) *model.Eeg {
	return &model.Eeg{
		Id: tenant, RcNumber: rcNumber, Name: name, Description: "Registrierungstest",
		Area: "LOCAL", Legal: "verein", AllocationMode: "DYNAMIC", SettlementInterval: "MONTHLY",
		GridOperator: "AT009999", OperatorName: "Test Operator", CommunityId: "AT00999900000TC000011000000000001",
		EegAddress: model.EegAddress{Street: "Solarweg", StreetNumber: "1", Zip: "4020", City: "Linz"},
		Contact:    model.Contact{Email: null.StringFrom("register-test@example.org")},
	}
}

// A second registration of a tenant must be refused and must not touch the first one (261015-ca1).
func TestInsertEegRefusesAnExistingTenant(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)
	ctx := context.Background()
	cleanup := func() {
		_, err := testDB.DbInstance.Exec(`DELETE FROM base.eeg WHERE upper(tenant) LIKE 'TE000031%'`)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)

	require.NoError(t, db.InsertEeg(ctx, "TE000031", registerEegFixture("TE000031", "TE000031", "Erste Registrierung")))

	t.Run("same tenant", func(t *testing.T) {
		err := db.InsertEeg(ctx, "TE000031", registerEegFixture("TE000031", "TE000031", "Zweite Registrierung"))
		assert.ErrorIs(t, err, ErrEegExists)
	})

	t.Run("same tenant in another spelling", func(t *testing.T) {
		err := db.InsertEeg(ctx, "te000031", registerEegFixture("te000031", "TE000031", "Dritte Registrierung"))
		assert.ErrorIs(t, err, ErrEegExists)
	})

	t.Run("same tenant with an invalid mail address", func(t *testing.T) {
		eeg := registerEegFixture("TE000031", "TE000031", "Vierte Registrierung")
		eeg.Contact.Email = null.StringFrom("foo@")
		assert.ErrorIs(t, db.InsertEeg(ctx, "TE000031", eeg), ErrEegExists)
	})

	t.Run("a new tenant on an existing RC number outside GEA", func(t *testing.T) {
		err := db.InsertEeg(ctx, "TE000031", registerEegFixture("TE000031X", "te000031", "Tippfehler im Tenant"))
		assert.ErrorIs(t, err, ErrEegExists)
	})

	// platform#229: only GEA is excepted from the RC number rule, P2P and EVA are not
	for _, area := range []model.AreaType{model.P2P, model.EVA} {
		t.Run("a new "+string(area)+" tenant on an existing RC number", func(t *testing.T) {
			eeg := registerEegFixture("TE000031-"+string(area), "TE000031", string(area)+" auf fremder Nummer")
			eeg.Area = area
			assert.ErrorIs(t, db.InsertEeg(ctx, "TE000031", eeg), ErrEegExists)
		})
	}

	t.Run("the first registration stays as it was", func(t *testing.T) {
		var names []string
		require.NoError(t, testDB.DbInstance.Select(&names, `SELECT name FROM base.eeg WHERE upper(tenant) LIKE 'TE000031%'`))
		assert.Equal(t, []string{"Erste Registrierung"}, names)
	})

	// GEA: stem and sub communities share the RC number, each one is a tenant of its own
	t.Run("a new GEA tenant of an existing RC number stays possible", func(t *testing.T) {
		sub := registerEegFixture("TE000031-001", "TE000031", "GEA-Teilgemeinschaft")
		sub.Area = "GEA"
		assert.NoError(t, db.InsertEeg(ctx, "TE000031", sub))
	})
}

// platform#229: a P2P or EVA registration stores exactly this type, and the view hands it to billing as eeg_area.
func TestInsertEegStoresP2PAndEva(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)
	ctx := context.Background()
	tenants := map[string]model.AreaType{"TE000041": model.P2P, "TE000042": model.EVA}
	cleanup := func() {
		_, err := testDB.DbInstance.Exec(`DELETE FROM base.participant WHERE tenant IN ('TE000041', 'TE000042')`)
		require.NoError(t, err)
		_, err = testDB.DbInstance.Exec(`DELETE FROM base.eeg WHERE tenant IN ('TE000041', 'TE000042')`)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)

	for tenant, area := range tenants {
		eeg := registerEegFixture(tenant, tenant, string(area))
		eeg.Area = area
		require.NoError(t, db.InsertEeg(ctx, tenant, eeg))

		stored, err := db.GetEegById(ctx, tenant)
		require.NoError(t, err)
		assert.Equal(t, area, stored.Area)

		_, err = testDB.DbInstance.Exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, "createdBy",
			"lastModifiedBy") VALUES (gen_random_uuid(), $1, 'P2P', 'Mitglied', 'test', 'test')`, tenant)
		require.NoError(t, err)
		var areas []string
		require.NoError(t, testDB.DbInstance.Select(&areas, `SELECT eeg_area FROM base.billing_masterdata
			WHERE tenant_id = $1`, tenant))
		assert.Equal(t, []string{string(area)}, areas)
	}
}

// platform#229: both registrations (gRPC, MQTT) refuse an unknown community type in insertEeg; nothing is written.
// "6" is how a gRPC GRID_AREA value unknown to this backend arrives.
func TestInsertEegRefusesAnUnknownArea(t *testing.T) {
	db, err := GetDB(context.Background())
	require.NoError(t, err)
	for _, area := range []model.AreaType{"6", "", "PP"} {
		eeg := registerEegFixture("TE000046", "TE000046", "Unbekannter Typ")
		eeg.Area = area
		assert.ErrorIs(t, db.InsertEeg(context.Background(), "TE000046", eeg), ErrInvalidEeg, string(area))
	}
	var n int
	require.NoError(t, testDB.DbInstance.Get(&n, `SELECT count(*) FROM base.eeg WHERE tenant = 'TE000046'`))
	assert.Zero(t, n, "nothing written")
}
