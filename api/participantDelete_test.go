package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 261005-ca10: web tells this refusal apart by status and code.
func TestDeleteParticipantWithAnActiveMeterAnswers409(t *testing.T) {
	const (
		tenant      = "TE000033"
		participant = "ca100000-0000-4000-8000-000000000002"
		meter       = "AT0030000000000000000000000000331"
	)
	exec := func(q string, args ...interface{}) {
		t.Helper()
		_, err := testDB.DbInstance.Exec(q, args...)
		require.NoError(t, err)
	}
	cleanup := func() {
		exec(`DELETE FROM base.participant WHERE tenant = $1`, tenant)
		exec(`DELETE FROM base.eeg WHERE tenant = $1`, tenant)
	}
	cleanup()
	t.Cleanup(cleanup)
	exec(`INSERT INTO base.eeg (tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name,
		"communityId", street, "streetNumber", city, zip, email)
		VALUES ($1, 'Lösch-EEG', 'Lösch-EEG', $2, 'LOCAL', 'AT003000', 'Netz OÖ', 'AT00300000000RC10003300000000000', 'Weg', '1', 'Linz', '4020',
		'office@example.org')`, tenant, tenant)
	exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
		VALUES ($1, $2, 'Anna', 'Muster', 'ACTIVE', 'test', 'test')`, participant, tenant)
	exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
		"modifiedAt", "modifiedBy", "registeredSince", activesince)
		VALUES ($1, $2, $3, 'CONSUMPTION', 'ACTIVE', 'ACTIVE', now(), 'test', '2026-01-01', '2026-01-01')`, meter, participant, tenant)

	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	req, _ := http.NewRequest("DELETE", "/participant/v2/"+participant, nil)
	req = mux.SetURLVars(req, map[string]string{"id": participant})
	w := httptest.NewRecorder()

	NewParticipantHandler(db).deleteParticipant()(w, req, &middleware.PlatformClaims{Username: "test"}, tenant)

	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	var body struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1109, body.Error.Code)
}
