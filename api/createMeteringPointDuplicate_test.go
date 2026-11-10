package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/model"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// platform#247: adding a metering point the member already has (e.g. inactive after a revoke) answered
// 400 / 1111 "Bad Process" with the raw key violation. Now: 409 / 1206 with a German text that names the way
// ("Zählpunkt aktivieren"), and nothing is written or sent.
func TestCreateMeteringPoint_existingAtParticipantIs409(t *testing.T) {
	// TE001006: online EEG with an ACTIVE participant (as in TestCreateMeteringPoint_missingModeRegistersOnline)
	const tenant = "TE001006"
	const participant = "04583ce3-5802-40b9-afd9-29d09b908f1f"
	const meter = "AT0030000000000000000000000099004" // used by no other test
	deleteMeter(t, meter)
	calls := countRegistrations(t)
	create := func() *httptest.ResponseRecorder {
		body := `{"meteringPoint":"` + meter + `","direction":"CONSUMPTION","status":"INIT","processState":"NEW"}`
		req, _ := http.NewRequest("PUT", "/meteringpoint/"+participant+"/create", strings.NewReader(body))
		req = mux.SetURLVars(req, map[string]string{"pid": participant})
		w := httptest.NewRecorder()
		newTestMeteringHandler(t).createMeteringPoint()(w, req, &middleware.PlatformClaims{Username: "test"}, tenant)
		return w
	}

	first := create()
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	require.Equal(t, 1, *calls, "the first create registers at the grid operator")

	second := create()
	require.Equal(t, http.StatusConflict, second.Code, second.Body.String())
	var body struct {
		Error HttpError `json:"error"`
	}
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &body))
	assert.Equal(t, 1206, body.Error.Code)
	assert.Equal(t, model.MeterExistsAtParticipantText, body.Error.Message)
	assert.Equal(t, 1, *calls, "the second create sends nothing")

	var n int
	require.NoError(t, testDB.DbInstance.Get(&n,
		`SELECT count(*) FROM base.meteringpoint WHERE metering_point_id = $1 AND participant_id = $2`, meter, participant))
	assert.Equal(t, 1, n, "still one row")
}
