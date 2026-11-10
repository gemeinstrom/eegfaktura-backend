package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	movedTenant = "TE000099"
	movedMeter  = "AT0030000000000000000000000000991"
	lowUUID     = "ca990000-0000-4000-8000-000000000002"
	highUUID    = "ca990000-0000-4000-8000-000000000009"
)

// An owner change leaves two rows of one metering point: the moved one (flag 0) and the current one. The order of
// the rows follows the participant ids, so both orders are tested.
func TestChangePartitionFactorUsesTheCurrentEntryAfterAnOwnerChange(t *testing.T) {
	for _, tc := range []struct{ name, oldOwner, newOwner string }{
		{"old owner sorts first", lowUUID, highUUID},
		{"old owner sorts last", highUUID, lowUUID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := setupMovedMeterCommunity(t, tc.oldOwner, tc.newOwner)
			insertMeter(exec, tc.oldOwner, "CONSUMPTION", "AT001111", "INACTIVE", model.F_MOVED)
			insertMeter(exec, tc.newOwner, "GENERATION", "AT002222", "ACTIVE", model.F_ASSIGNED)

			code, sent := sendPartitionFactor(t)

			assert.Equal(t, http.StatusNoContent, code)
			require.Len(t, sent, 1)
			assert.Equal(t, model.GENERATOR, sent[0].Direction)
			assert.Equal(t, "AT002222", sent[0].GridOperatorId.String)
		})
	}
}

func TestChangePartitionFactorRefusesAMeterWithoutCurrentEntry(t *testing.T) {
	exec := setupMovedMeterCommunity(t, lowUUID, highUUID)
	insertMeter(exec, lowUUID, "CONSUMPTION", "AT001111", "INACTIVE", model.F_MOVED)

	code, sent := sendPartitionFactor(t)

	assert.Equal(t, http.StatusBadRequest, code)
	assert.Empty(t, sent)
}

func setupMovedMeterCommunity(t *testing.T, participants ...string) func(string, ...interface{}) {
	t.Helper()
	exec := func(query string, args ...interface{}) {
		t.Helper()
		_, err := testDB.DbInstance.Exec(query, args...)
		require.NoError(t, err)
	}
	clean := func() {
		exec(`DELETE FROM base.participant WHERE tenant = $1`, movedTenant)
		exec(`DELETE FROM base.eeg WHERE tenant = $1`, movedTenant)
	}
	clean()
	t.Cleanup(clean)
	exec(`INSERT INTO base.eeg (tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name,
		"communityId", street, "streetNumber", city, zip, email, online)
		VALUES ($1, 'R', 'R', 'RC100099', 'LOCAL', 'AT001111', 'Netz', 'AT00300000000RC10009900000000000', 'Weg', '1', 'Linz', '4020', 'o@example.org', true)`, movedTenant)
	for _, p := range participants {
		exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
			VALUES ($1, $2, 'A', 'B', 'ACTIVE', 'test', 'test')`, p, movedTenant)
	}
	return exec
}

func insertMeter(exec func(string, ...interface{}), participant, direction, gridOperator, status string, flag model.ProcessFlag) {
	exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, grid_operator_id, status, process_state,
		"modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince, flag)
		VALUES ($1, $2, $3, $4, $5, $6, $6, now(), 'test', '2026-01-01', '2026-01-01', '2999-12-31', $7)`,
		movedMeter, participant, movedTenant, direction, gridOperator, status, int(flag))
	exec(`INSERT INTO base.metering_partition_factor (metering_point_id, participant_id, tenant, "partFact", "createdBy")
		VALUES ($1, $2, $3, 100, 'test')`, movedMeter, participant, movedTenant)
}

func sendPartitionFactor(t *testing.T) (int, []*model.ChangePartitionFactorRequest) {
	t.Helper()
	orig := mqttclient.ChangePartitionFactor
	t.Cleanup(func() { mqttclient.ChangePartitionFactor = orig })
	var sent []*model.ChangePartitionFactorRequest
	mqttclient.ChangePartitionFactor = func(eeg *model.Eeg, meter []*model.ChangePartitionFactorRequest) error {
		sent = meter
		return nil
	}
	body := `{"meteringPoints":[{"meter":"` + movedMeter + `","direction":"GENERATION","activation":"2026-01-01","partFact":50}]}`
	req, _ := http.NewRequest("POST", "/meteringpoint/changepartitionfactor", strings.NewReader(body))
	w := httptest.NewRecorder()
	newTestMeteringHandler(t).requestChangePartitionFactor()(w, req, nil, movedTenant)
	return w.Code, sent
}
