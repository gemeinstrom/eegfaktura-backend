package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/database/dbtest"
	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testDB *dbtest.TestDatabase

func TestMain(m *testing.M) {
	testDB = dbtest.SetupTestDatabase()
	if err := testDB.Open(context.Background()); err != nil {
		panic(err)
	}
	db := database.NewDatabase(testDB.DbInstance)
	database.SetDB(db)

	// os.Exit skips deferred calls: close and remove the container before it
	code := m.Run()
	_ = db.CloseDB()
	testDB.TearDown()
	os.Exit(code)
}

func TestMarschaling(t *testing.T) {
	//jsonStr := `{"id":"","name":"Mein Einspeise Traif","type":"EZP","useVat":false,"baseFee":"0","accountGrossAmount":0,"participantFee":0,"accountNetAmount":0,"billingPeriod":"monthly","businessNr":0,"centPerKWh":"0.12","discount":0,"freeKWH":0,"vatInPercent":0}`
	jsonStr := `{"id":"",
"name":"Mein Einspeise Traif",
"type":"EZP",
"useVat":false,
"baseFee":"0",
"accountGrossAmount":"0",
"participantFee":0,
"accountNetAmount":"0",
"billingPeriod":"monthly",
"businessNr":"0",
"centPerKWh":12.0,
"discount":"0",
"freeKWH":"0",
"vatSupplementaryText": "",
"vatInPercent":"0"}`

	var r model.Tariff
	err := json.Unmarshal([]byte(jsonStr), &r)
	require.NoError(t, err)

	fmt.Printf("R: %+v\n", r)
}

// A rejected tariff tells the client why: the validation message is in the error body (it was "{}" before,
// because a plain error marshals to an empty JSON object).
func TestAddTariff_validationMessageInResponse(t *testing.T) {
	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	h := NewEegHandler(db)

	body := `{"name":"ZVT an EEG","type":"EEG","useTimeTariff":true}`
	req := httptest.NewRequest(http.MethodPost, "/eeg/tariff", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.addTariff()(w, req, &middleware.PlatformClaims{Username: "test"}, "TE000002")

	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp struct {
		Error HttpError `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 4001, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "zeitbasierter Tarif ist nur für Verbraucher- und Erzeuger-Tarife zulässig")
}

// POST /eeg/tariff with a joining fee: rejected with the reason when invalid, stored and echoed when valid
// (tariff JSON contract with eegfaktura-web).
func TestAddTariff_joiningFee(t *testing.T) {
	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	h := NewEegHandler(db)
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/eeg/tariff", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.addTariff()(w, req, &middleware.PlatformClaims{Username: "test"}, "TE000027")
		return w
	}
	errorOf := func(w *httptest.ResponseRecorder) HttpError {
		var resp struct {
			Error HttpError `json:"error"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return resp.Error
	}

	w := post(`{"name":"Verbrauch","type":"VZP","centPerKWh":12,"joiningFee":50}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "Beitrittsgebühr ist nur für Mitgliedsbeitrags-Tarife zulässig", errorOf(w).Message)

	w = post(`{"name":"Beitrag","type":"EEG","joiningFee":49.999}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "Beitrittsgebühr: höchstens 2 Nachkommastellen", errorOf(w).Message)

	w = post(`{"name":"Beitrag","type":"EEG","participantFee":2,"joiningFee":49.90,"joiningFeeUseVat":true,"joiningFeeVatInPercent":20}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	// a repeated run must find one tariff again (261105-mm6)
	t.Cleanup(func() {
		_, err := testDB.DbInstance.Exec(`DELETE FROM base.tariff WHERE tenant = 'TE000027' AND id = $1`, created["id"])
		require.NoError(t, err)
	})
	assert.Equal(t, 49.9, created["joiningFee"])
	assert.Equal(t, true, created["joiningFeeUseVat"])
	assert.Equal(t, float64(20), created["joiningFeeVatInPercent"])

	tariffs, err := db.GetTariff(context.Background(), "TE000027")
	require.NoError(t, err)
	require.Len(t, tariffs, 1)
	assert.Equal(t, 49.90, tariffs[0].JoiningFee)
}
