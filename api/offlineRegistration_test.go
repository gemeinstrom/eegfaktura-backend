package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Offline registration (ANFORDERUNG_ECOF with activation code) is no longer supported by the grid
// operators (decision 2026-10-30). A missing activationMode registers online; OFFLINE is answered with
// 400 / code 1205 before anything is written or sent.

const (
	offTenant      = "TE000002" // online EEG
	offParticipant = "ea9942db-03da-11ee-b82b-5a985b4b033a"
	offMeter       = "AT0030000000000000000000000054554" // INIT/NEW meter of offParticipant
)

// countRegistrations replaces the only outgoing registration (ANFORDERUNG_ECON) with a counter.
func countRegistrations(t *testing.T) *int {
	t.Helper()
	orig := mqttclient.RegistrationForParticipation
	t.Cleanup(func() { mqttclient.RegistrationForParticipation = orig })
	calls := 0
	mqttclient.RegistrationForParticipation = func(eeg *model.Eeg, meter *model.MeteringPoint, from *int64) error {
		calls++
		return nil
	}
	return &calls
}

func assertErrorCode(t *testing.T, w *httptest.ResponseRecorder, code int, message string) {
	t.Helper()
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	var body struct {
		Error HttpError `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, code, body.Error.Code)
	assert.Contains(t, body.Error.Message, message)
}

func TestRegisterMeteringPoint_activationMode(t *testing.T) {
	tests := []struct {
		name      string
		mode      string // "-" = field absent
		wantHTTP  int
		wantCode  int
		wantCalls int
	}{
		{name: "missing mode registers online", mode: "-", wantHTTP: http.StatusCreated, wantCalls: 1},
		{name: "empty mode registers online", mode: "", wantHTTP: http.StatusCreated, wantCalls: 1},
		{name: "ONLINE registers online", mode: "ONLINE", wantHTTP: http.StatusCreated, wantCalls: 1},
		{name: "OFFLINE is rejected", mode: "OFFLINE", wantHTTP: http.StatusBadRequest, wantCode: 1205},
		{name: "unknown mode is rejected", mode: "SOMETHING", wantHTTP: http.StatusBadRequest, wantCode: 1204},
	}
	h := newTestMeteringHandler(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := countRegistrations(t)
			body := `{"meteringPoint":"` + offMeter + `","direction":"CONSUMPTION","activationCode":"ignored"`
			if tt.mode != "-" {
				body += `,"activationMode":"` + tt.mode + `"`
			}
			body += `}`
			req, _ := http.NewRequest("POST", "/meteringpoint/"+offParticipant+"/register", strings.NewReader(body))
			req = mux.SetURLVars(req, map[string]string{"pid": offParticipant})
			w := httptest.NewRecorder()

			h.registerMeteringPoint()(w, req, &middleware.PlatformClaims{Username: "test"}, offTenant)

			require.Equal(t, tt.wantHTTP, w.Code, w.Body.String())
			if tt.wantCode == 1205 {
				assertErrorCode(t, w, 1205, offlineRegistrationUnsupported)
			} else if tt.wantCode != 0 {
				assertErrorCode(t, w, tt.wantCode, "unknown activationMode")
			}
			assert.Equal(t, tt.wantCalls, *calls, "registrations sent via MQTT")
		})
	}
}

func TestCreateMeteringPoint_offlineRejectedWithoutWrite(t *testing.T) {
	const meter = "AT0030000000000000000000000099001"
	calls := countRegistrations(t)
	body := `{"meteringPoint":"` + meter + `","direction":"CONSUMPTION","status":"INIT","processState":"NEW","activationMode":"OFFLINE","activationCode":"ABC123"}`
	req, _ := http.NewRequest("PUT", "/meteringpoint/"+offParticipant+"/create", strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"pid": offParticipant})
	w := httptest.NewRecorder()

	newTestMeteringHandler(t).createMeteringPoint()(w, req, &middleware.PlatformClaims{Username: "test"}, offTenant)

	assertErrorCode(t, w, 1205, offlineRegistrationUnsupported)
	assert.Equal(t, 0, *calls, "registrations sent via MQTT")
	var n int
	require.NoError(t, testDB.DbInstance.Get(&n, `SELECT count(*) FROM base.meteringpoint WHERE metering_point_id = $1`, meter))
	assert.Equal(t, 0, n, "metering point must not be written")
}

func TestCreateMeteringPoint_missingModeRegistersOnline(t *testing.T) {
	// TE001006: online EEG with an ACTIVE participant whose meter count no other test asserts.
	const tenant = "TE001006"
	const participant = "04583ce3-5802-40b9-afd9-29d09b908f1f"
	const meter = "AT0030000000000000000000000099002"
	deleteMeter(t, meter)
	calls := countRegistrations(t)
	body := `{"meteringPoint":"` + meter + `","direction":"CONSUMPTION","status":"INIT","processState":"NEW"}`
	req, _ := http.NewRequest("PUT", "/meteringpoint/"+participant+"/create", strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"pid": participant})
	w := httptest.NewRecorder()

	newTestMeteringHandler(t).createMeteringPoint()(w, req, &middleware.PlatformClaims{Username: "test"}, tenant)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, 1, *calls, "online registration sent via MQTT")
}

func TestConfirmParticipant_activationMode(t *testing.T) {
	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	h := NewParticipantHandler(db)
	confirm := func(t *testing.T, username, body string) *httptest.ResponseRecorder {
		req, _ := http.NewRequest("POST", "/participant/"+offParticipant+"/confirm", strings.NewReader(body))
		req = mux.SetURLVars(req, map[string]string{"id": offParticipant})
		w := httptest.NewRecorder()
		h.confirmParticipant()(w, req, &middleware.PlatformClaims{Username: username}, offTenant)
		return w
	}
	lastModifiedBy := func(t *testing.T) string {
		var by string
		require.NoError(t, testDB.DbInstance.Get(&by, `SELECT "lastModifiedBy" FROM base.participant WHERE id = $1`, offParticipant))
		return by
	}

	t.Run("OFFLINE is rejected before the participant is confirmed", func(t *testing.T) {
		calls := countRegistrations(t)
		// the first meter is valid; the OFFLINE one must stop the whole request before any write or send
		w := confirm(t, "offline-confirm-test", `[{"meteringPoint":"`+offMeter+`","activationMode":"ONLINE"},`+
			`{"meteringPoint":"AT0030000000000000000000000533083","activationMode":"OFFLINE","activationCode":"ABC123"}]`)

		assertErrorCode(t, w, 1205, offlineRegistrationUnsupported)
		assert.Equal(t, 0, *calls, "registrations sent via MQTT")
		assert.NotEqual(t, "offline-confirm-test", lastModifiedBy(t), "participant must not be written")
	})

	t.Run("null element is a parse error, nothing written", func(t *testing.T) {
		calls := countRegistrations(t)
		var w *httptest.ResponseRecorder
		require.NotPanics(t, func() { w = confirm(t, "null-confirm-test", `[{"meteringPoint":"`+offMeter+`"},null]`) })

		assertErrorCode(t, w, 2000, "null metering point")
		assert.Equal(t, 0, *calls, "registrations sent via MQTT")
		assert.NotEqual(t, "null-confirm-test", lastModifiedBy(t), "participant must not be written")
	})

	t.Run("missing mode registers online", func(t *testing.T) {
		calls := countRegistrations(t)
		w := confirm(t, "test", `[{"meteringPoint":"`+offMeter+`"}]`)

		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		assert.Equal(t, 1, *calls, "online registration sent via MQTT")
	})
}

// deleteMeter removes a metering point a test created, so the test also passes with -count=2.
func deleteMeter(t *testing.T, meter string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = testDB.DbInstance.Exec(`DELETE FROM base.metering_partition_factor WHERE metering_point_id = $1`, meter)
		_, _ = testDB.DbInstance.Exec(`DELETE FROM base.meteringpoint WHERE metering_point_id = $1`, meter)
	})
}

// A non-online EEG never sends a registration, so the mode is ignored there as before.
func TestActivationMode_ignoredForNonOnlineEeg(t *testing.T) {
	const tenant = "TE000003" // online = false
	const participant = "ea9942dc-03da-11ee-b82b-5a985b4b033a"
	claims := &middleware.PlatformClaims{Username: "test"}

	t.Run("create with OFFLINE is saved", func(t *testing.T) {
		const meter = "AT0030000000000000000000000099003"
		deleteMeter(t, meter)
		calls := countRegistrations(t)
		body := `{"meteringPoint":"` + meter + `","direction":"CONSUMPTION","status":"INIT","processState":"NEW","activationMode":"OFFLINE"}`
		req, _ := http.NewRequest("PUT", "/meteringpoint/"+participant+"/create", strings.NewReader(body))
		req = mux.SetURLVars(req, map[string]string{"pid": participant})
		w := httptest.NewRecorder()

		newTestMeteringHandler(t).createMeteringPoint()(w, req, claims, tenant)

		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		assert.Equal(t, 0, *calls, "registrations sent via MQTT")
	})

	t.Run("register with OFFLINE sends nothing", func(t *testing.T) {
		calls := countRegistrations(t)
		body := `{"meteringPoint":"AT0030000000000000000000000003011","direction":"CONSUMPTION","activationMode":"OFFLINE"}`
		req, _ := http.NewRequest("POST", "/meteringpoint/"+participant+"/register", strings.NewReader(body))
		req = mux.SetURLVars(req, map[string]string{"pid": participant})
		w := httptest.NewRecorder()

		newTestMeteringHandler(t).registerMeteringPoint()(w, req, claims, tenant)

		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		assert.Equal(t, 0, *calls, "registrations sent via MQTT")
	})

	t.Run("confirm with OFFLINE confirms", func(t *testing.T) {
		db, err := database.GetDB(context.Background())
		require.NoError(t, err)
		calls := countRegistrations(t)
		body := `[{"meteringPoint":"AT0030000000000000000000000003011","activationMode":"OFFLINE","activationCode":"ABC123"}]`
		req, _ := http.NewRequest("POST", "/participant/"+participant+"/confirm", strings.NewReader(body))
		req = mux.SetURLVars(req, map[string]string{"id": participant})
		w := httptest.NewRecorder()

		NewParticipantHandler(db).confirmParticipant()(w, req, claims, tenant)

		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		assert.Equal(t, 0, *calls, "registrations sent via MQTT")
	})
}
