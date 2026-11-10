package database

import (
	"at.ourproject/vfeeg-backend/model"
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v4"
	"testing"
)

//func openTestDb() (*sqlx.DB, error) {
//	testDB.Open(context.Background())
//	return testDB.DbInstance, nil
//}

//func TestMain(m *testing.M) {
//	testDB = SetupTestDatabase()
//	_, err := GetTestDB(context.Background())
//	if err != nil {
//		panic(err)
//	}
//	defer func() {
//		//db.Close()
//		testDB.TearDown()
//	}()
//	os.Exit(m.Run())
//}

// A failing query is an error, not an empty tariff list.
func TestGetTariffReportsAQueryError(t *testing.T) {
	dbx, mock := newTxMock(t)

	mock.ExpectQuery("SELECT (.+) FROM base.activetariff (.+)").WillReturnError(errors.New("query failed"))
	_, err := GetTariff(context.Background(), dbx, "TE000001")
	assert.Error(t, err)

	mock.ExpectQuery("SELECT (.+) FROM base.activetariff (.+)").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	tariffs, err := GetTariff(context.Background(), dbx, "TE000001")
	assert.NoError(t, err)
	assert.Equal(t, []model.Tariff{}, tariffs)
}

func TestAddTariff(t *testing.T) {
	tariff := model.Tariff{Version: 0, Name: "Sepp", UseVat: false, BillingPeriod: "monthly", FreeKWh: null.IntFrom(100), CentPerKWh: 12}
	dbx, mock := newTxMock(t)

	//stmt := "INSERT INTO (.+) VALUES \\(0, 0, 0, 'monthly', 0, 12, 0, 100, DEFAULT, 'Sepp', 0, 'sepp', '', FALSE, 0, 1\\)"
	//stmt := "INSERT INTO \"base\".\"tariff\" \\(\"accountGrossAmount\", \"accountNetAmount\", \"baseFee\", \"billingPeriod\", \"businessNr\", \"centPerKWh\", \"createdBy\", \"createdDate\", \"discount\", \"freeKWh\", \"id\", \"lastModifiedDate\", \"meteringPointFee\", \"name\", \"participantFee\", \"tenant\", \"type\", \"useMeteringPointFee\", \"useVat\", \"vatInPercent\", \"vatSupplementaryText\", \"version\"\\) VALUES (.+)"

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO \"base\".\"tariff\" (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	//mock.ExpectExec("Update \"base\".\"tariff\" SET (.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := AddTariff(context.Background(), dbx, "TE000001", "sepp", &tariff)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A changed tariff is stored as a new version of the same id.
func TestAddTariff_newVersion(t *testing.T) {
	tariff := model.Tariff{Version: 1, Name: "Sepp", UseVat: false, BillingPeriod: "monthly", FreeKWh: null.IntFrom(100), CentPerKWh: 12}

	db, err := GetDB(context.Background())
	require.NoError(t, err)
	// other tests add tariffs to TE000001 too: count relative to before, find the tariff by its id
	before, err := db.GetTariff(context.Background(), "TE000001")
	require.NoError(t, err)
	byId := func(tariffs []model.Tariff, id string) *model.Tariff {
		for i := range tariffs {
			if tariffs[i].Id.String() == id {
				return &tariffs[i]
			}
		}
		return nil
	}

	err = db.AddTariff(context.Background(), "TE000001", "sepp", &tariff)
	require.NoError(t, err)

	tariffSlice, err := db.GetTariff(context.Background(), "TE000001")
	require.NoError(t, err)
	require.Equal(t, len(before)+1, len(tariffSlice))

	added := byId(tariffSlice, tariff.Id.String())
	require.NotNil(t, added)
	updateTariff := *added
	updateTariff.UseMeteringFee = true
	updateTariff.MeteringFee = null.FloatFrom(200.11)

	err = db.AddTariff(context.Background(), "TE000001", "sepp", &updateTariff)
	require.NoError(t, err)
	updatedTariff, err := db.GetTariff(context.Background(), "TE000001")
	require.NoError(t, err)

	require.Equal(t, len(before)+1, len(updatedTariff), "the new version replaces the active one")
	updated := byId(updatedTariff, tariff.Id.String())
	require.NotNil(t, updated)
	assert.Equal(t, 2, updated.Version)
	assert.Equal(t, true, updated.UseMeteringFee)
	assert.Equal(t, null.FloatFrom(200.11), updated.MeteringFee)
}
