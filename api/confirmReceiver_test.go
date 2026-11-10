package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// platform#229: a P2P community without a grid operator in its settings; one metering point has a grid operator, the
// other none. The confirmation is refused with 400 before anything is written or sent: no registration for the
// first metering point, the participant stays unconfirmed.
func TestConfirmParticipant_missingGridOperatorSendsNothing(t *testing.T) {
	const (
		tenant      = "TE000081"
		participant = "ca100000-0000-4000-8000-000000000081"
		withOp      = "AT0030000000000000000000000081001"
		withoutOp   = "AT0080000000000000000000000081002"
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
		"communityId", street, "streetNumber", city, zip, email, online)
		VALUES ($1, 'P2P', 'P2P', $2, 'P2P', '', '', 'ATPP99990000000000000000000000081', 'Weg', '1', 'Linz', '4020',
		'office@example.org', true)`, tenant, tenant)
	exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
		VALUES ($1, $2, 'Peer', 'Muster', 'NEW', 'test', 'test')`, participant, tenant)
	for _, typ := range []string{"BILLING", "RESIDENCE"} {
		exec(`INSERT INTO base.address (participant_id, type, street, "streetNumber", city, zip)
			VALUES ($1, $2, 'Weg', '1', 'Linz', '4020')`, participant, typ)
	}
	for meter, operator := range map[string]interface{}{withOp: "AT003000", withoutOp: nil} {
		exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
			"modifiedAt", "modifiedBy", "registeredSince", grid_operator_id)
			VALUES ($1, $2, $3, 'CONSUMPTION', 'INIT', 'NEW', now(), 'test', '2026-11-01', $4)`, meter, participant, tenant, operator)
		exec(`INSERT INTO base.metering_partition_factor (metering_point_id, participant_id, tenant, "partFact", "createdBy")
			VALUES ($1, $2, $3, 100, 'test')`, meter, participant, tenant)
	}

	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	calls := countRegistrations(t)
	body := `[{"meteringPoint":"` + withOp + `"},{"meteringPoint":"` + withoutOp + `"}]`
	req, _ := http.NewRequest("POST", "/participant/"+participant+"/confirm", strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"id": participant})
	w := httptest.NewRecorder()

	NewParticipantHandler(db).confirmParticipant()(w, req, &middleware.PlatformClaims{Username: "confirm-receiver-test"}, tenant)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, 0, *calls, "registrations sent via MQTT")
	var status, by string
	require.NoError(t, testDB.DbInstance.QueryRow(`SELECT status, "lastModifiedBy" FROM base.participant WHERE id = $1`,
		participant).Scan(&status, &by))
	assert.Equal(t, "NEW", status, "participant not confirmed")
	assert.Equal(t, "test", by, "participant not written")
}
