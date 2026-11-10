package database

import (
	"context"
	"errors"
	"testing"

	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 261005-ca10: the delete cascades, so it refuses a member with a metering point that took part.
func TestDeleteParticipantGuard(t *testing.T) {
	const (
		tenant      = "TE000032"
		participant = "ca100000-0000-4000-8000-000000000001"
		meter       = "AT0030000000000000000000000000321"
		other       = "AT0030000000000000000000000000322"
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
		VALUES ($1, 'Lösch-EEG', 'Lösch-EEG', $2, 'LOCAL', 'AT003000', 'Netz OÖ', 'AT00300000000RC10003200000000000', 'Weg', '1', 'Linz', '4020',
		'office@example.org')`, tenant, tenant)

	seed := func(meters map[string][3]interface{}) {
		exec(`DELETE FROM base.participant WHERE tenant = $1`, tenant)
		exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
			VALUES ($1, $2, 'Anna', 'Muster', 'PENDING', 'test', 'test')`, participant, tenant)
		for id, m := range meters {
			exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
				"modifiedAt", "modifiedBy", "registeredSince", activesince)
				VALUES ($1, $2, $3, 'CONSUMPTION', $4, $5, now(), 'test', '2026-01-01', $6)`, id, participant, tenant, m[0], m[1], m[2])
		}
	}
	members := func() int {
		var n int
		require.NoError(t, testDB.DbInstance.Get(&n, `SELECT count(*) FROM base.participant WHERE id = $1`, participant))
		return n
	}

	db, err := GetDB(context.Background())
	require.NoError(t, err)

	var never = [3]interface{}{"INIT", "NEW", nil}
	tests := []struct {
		name    string
		meters  map[string][3]interface{}
		refused bool
	}{
		{"no metering point", nil, false},
		{"new", map[string][3]interface{}{meter: never}, false},
		{"invalid", map[string][3]interface{}{meter: {"INIT", "INVALID", nil}}, false},
		{"answered by the grid operator, the member can still approve", map[string][3]interface{}{meter: {"INIT", "PENDING", nil}}, true},
		{"rejected by the grid operator", map[string][3]interface{}{meter: {"INIT", "REJECTED", nil}}, false},
		{"request running at the grid operator", map[string][3]interface{}{meter: {"INIT", "INIT", nil}}, true},
		{"approved", map[string][3]interface{}{meter: {"INIT", "APPROVED", nil}}, true},
		{"active", map[string][3]interface{}{meter: {"ACTIVE", "ACTIVE", "2026-01-01"}}, true},
		{"left the community", map[string][3]interface{}{meter: {"INACTIVE", "INACTIVE", "2026-01-01"}}, true},
		{"status back on INIT, but it was active", map[string][3]interface{}{meter: {"INIT", "NEW", "2026-01-01"}}, true},
		{"one of two was active", map[string][3]interface{}{meter: never, other: {"ACTIVE", "ACTIVE", "2026-01-01"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed(tt.meters)

			err := db.DeleteParticipant(context.Background(), tenant, participant)

			if !tt.refused {
				require.NoError(t, err)
				assert.Equal(t, 0, members())
				return
			}
			var ve *model.VfeegError
			require.True(t, errors.As(err, &ve), "%v", err)
			assert.Equal(t, model.ErrParticipantHasActivatedMeter(nil).Code, ve.Code)
			assert.Equal(t, 1, members(), "nothing deleted")
		})
	}
}
