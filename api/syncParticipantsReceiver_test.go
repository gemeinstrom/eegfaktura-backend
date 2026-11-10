package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eegfaktura-dev#56 #7: the metering point list of a P2P community is requested only from the grid operator of one of
// its metering points or its own; blank or another one answers 400 (before: 204 and sent as given).
func TestSyncParticipantsEda_receiverOfTheCommunityOnly(t *testing.T) {
	const tenant = "TE000082"
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
		"communityId", street, "streetNumber", city, zip, email, online)
		VALUES ($1, 'P2P', 'P2P', $2, 'P2P', 'AT001000', 'Netz', 'ATPP99990000000000000000000000082', 'Weg', '1', 'Linz',
		'4020', 'office@example.org', true)`, tenant, tenant)
	exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
		VALUES ('ca100000-0000-4000-8000-000000000082', $1, 'Peer', 'Muster', 'ACTIVE', 'test', 'test')`, tenant)
	exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
		"modifiedAt", "modifiedBy", "registeredSince", grid_operator_id)
		VALUES ('AT0030000000000000000000000082001', 'ca100000-0000-4000-8000-000000000082', $1, 'CONSUMPTION',
		'ACTIVE', 'ACTIVE', now(), 'test', '2026-11-01', 'at003000')`, tenant)

	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	operators, err := db.MeteringPointGridOperators(context.Background(), tenant)
	require.NoError(t, err)
	assert.Equal(t, []string{"AT003000"}, operators, "upper case")

	for _, oid := range []string{"AT999999", " ", ""} {
		req, _ := http.NewRequest("POST", "/eeg/sync/participants/"+oid, nil)
		req = mux.SetURLVars(req, map[string]string{"oid": oid})
		w := httptest.NewRecorder()
		NewEegHandler(db).syncParticipantsEda()(w, req, &middleware.PlatformClaims{}, tenant)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%q: %s", oid, w.Body.String())
	}
}
