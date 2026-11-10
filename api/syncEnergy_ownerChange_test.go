package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/energyrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// User decision 2026-10-24: after an owner change the old member's period is requested from the grid operator too
// (for the billing all current data are needed, later the old data cannot be got anymore). A metering point with an
// owner change has two entries in the community: the old member's moved entry (flag 0, INACTIVE) and the new
// member's current entry (flag 1). Each entry's part of the request period is requested; parts that overlap or touch
// are sent as one request.

// ownerChange creates the metering point meter in TE001006 with an old member active since..oldUntil (moved, flag 0)
// and a new member active from newSince (flag 1); flag is the old entry's flag. It is removed after the test.
func ownerChange(t *testing.T, meter, since, oldUntil, newSince string, flag int) {
	t.Helper()
	db := testDB.DbInstance
	oldOwner, newOwner := "c1050000-0000-0000-0000-"+meter[len(meter)-12:], "c1050001-0000-0000-0000-"+meter[len(meter)-12:]
	t.Cleanup(func() {
		// cascades to the metering point and partition factor rows
		_, err := db.Exec(`DELETE FROM base.participant WHERE id IN ($1, $2)`, oldOwner, newOwner)
		assert.NoError(t, err)
	})
	exec := func(q string, args ...interface{}) {
		t.Helper()
		_, err := db.Exec(q, args...)
		require.NoError(t, err)
	}
	for _, p := range []string{oldOwner, newOwner} {
		exec(`INSERT INTO base.participant (id, tenant, firstname, lastname, status, "createdBy", "lastModifiedBy")
			VALUES ($1, 'TE001006', 'Owner', 'Change', 'ACTIVE', 'test', 'test')`, p)
	}
	exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
		"modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince, flag)
		VALUES ($1, $2, 'TE001006', 'CONSUMPTION', 'INACTIVE', 'INACTIVE', now(), 'test', $3, $3, $4, $5)`,
		meter, oldOwner, since, oldUntil, flag)
	exec(`INSERT INTO base.meteringpoint (metering_point_id, participant_id, tenant, direction, status, process_state,
		"modifiedAt", "modifiedBy", "registeredSince", activesince, inactivesince, flag)
		VALUES ($1, $2, 'TE001006', 'CONSUMPTION', 'ACTIVE', 'ACTIVE', now(), 'test', $3, $3, '2999-12-31', 1)`,
		meter, newOwner, newSince)
	for _, p := range []string{oldOwner, newOwner} {
		exec(`INSERT INTO base.metering_partition_factor (metering_point_id, participant_id, tenant, "partFact", "createdBy")
			VALUES ($1, $2, 'TE001006', 100, 'test')`, meter, p)
	}
}

func syncBody(meter string, from, to int64) string {
	return fmt.Sprintf(`{"meteringPoints": [{"meter": %q, "direction": "CONSUMPTION"}], "from": %d, "to": %d}`,
		meter, from, to)
}

// contiguous periods (old until 15.06., new from 16.06.): one request covering both parts
func TestSyncEnergy_ownerChangeContiguous(t *testing.T) {
	const zp = "AT0030000000000000000000000953090"
	ownerChange(t, zp, "2025-01-01", "2025-06-15", "2025-06-16", 0)

	var resp syncResponse
	code, sent, run := syncEnergy(t, syncBody(zp, start(2025, 6, 5), start(2025, 6, 20)), &resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []sentRequest{{meter: zp, from: start(2025, 6, 5), to: end(2025, 6, 20)}}, sent)
	assert.Equal(t, syncResponse{Requested: 1, Skipped: []energyrequest.Skipped{}}, resp)
	assert.Equal(t, []energyrequest.MeterRange{{MeteringPoint: zp, From: start(2025, 6, 5), To: end(2025, 6, 20)}}, run.Meters)
	// the merged request goes out with the current entry (BEG: its grid operator is the receiver)
	require.Len(t, sentEntries, 1)
	assert.Equal(t, "c1050001-0000-0000-0000-"+zp[len(zp)-12:], sentEntries[0].ParticipantId)
}

// a gap between the owners (old until 02.09., new from 06.09.): two requests, the run records the covering span. An
// ended entry is requested up to the end of its last day (inactivesince is its last active day), a new one from the
// start of its first day (local time)
func TestSyncEnergy_ownerChangeWithGap(t *testing.T) {
	const zp = "AT0030000000000000000000000953091"
	ownerChange(t, zp, "2025-01-01", "2025-09-02", "2025-09-06", 0)

	code, sent, run := syncEnergy(t, syncBody(zp, start(2025, 9, 1), start(2025, 9, 10)))
	assert.Equal(t, http.StatusOK, code)
	assert.ElementsMatch(t, []sentRequest{
		{meter: zp, from: start(2025, 9, 1), to: end(2025, 9, 2)},
		{meter: zp, from: start(2025, 9, 6), to: end(2025, 9, 10)},
	}, sent)
	assert.Equal(t, []energyrequest.MeterRange{{MeteringPoint: zp, From: start(2025, 9, 1), To: end(2025, 9, 10)}}, run.Meters)

	// both parts are locked now
	code, sent, _ = syncEnergy(t, syncBody(zp, start(2025, 9, 1), start(2025, 9, 10)))
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, sent)
}

// an entry that ends on the day summer time starts (30.03.2025) is requested up to that day's last quarter hour
func TestSyncEnergy_ownerChangeEndsOnDstDay(t *testing.T) {
	const zp = "AT0030000000000000000000000953096"
	ownerChange(t, zp, "2025-01-01", "2025-03-30", "2025-04-05", 0)

	_, sent, _ := syncEnergy(t, syncBody(zp, start(2025, 3, 25), start(2025, 4, 2)))
	assert.Equal(t, []sentRequest{{meter: zp, from: start(2025, 3, 25), to: end(2025, 3, 30)}}, sent)
}

// a request only in the old member's period is sent (before: skipped as NOT_ACTIVE_IN_PERIOD)
func TestSyncEnergy_ownerChangeOldOnly(t *testing.T) {
	const zp = "AT0030000000000000000000000953092"
	ownerChange(t, zp, "2025-01-01", "2025-06-15", "2025-06-16", 0)

	var resp syncResponse
	code, sent, _ := syncEnergy(t, syncBody(zp, start(2025, 3, 1), start(2025, 3, 31)), &resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []sentRequest{{meter: zp, from: start(2025, 3, 1), to: end(2025, 3, 31)}}, sent)
	assert.Equal(t, syncResponse{Requested: 1, Skipped: []energyrequest.Skipped{}}, resp)

	// before the old member's period: no entry is active, as before
	code, sent, _ = syncEnergy(t, syncBody(zp, start(2024, 12, 1), start(2024, 12, 31)), &resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, sent)
	assert.Equal(t, []energyrequest.Skipped{{MeteringPoint: zp, Reason: energyrequest.ReasonNotActiveInPeriod}}, resp.Skipped)
}

// the lock is checked per part: a part requested within the hour is not sent again, the other part is
func TestSyncEnergy_ownerChangePartLocked(t *testing.T) {
	const oldLocked, newLocked = "AT0030000000000000000000000953093", "AT0030000000000000000000000953094"
	ownerChange(t, oldLocked, "2025-01-01", "2025-06-15", "2025-06-16", 0)
	ownerChange(t, newLocked, "2025-01-01", "2025-06-15", "2025-06-16", 0)

	// the old part was requested: only the new part is sent
	_, sent, _ := syncEnergy(t, syncBody(oldLocked, start(2025, 6, 1), start(2025, 6, 10)))
	require.Len(t, sent, 1)
	var resp syncResponse
	_, sent, run := syncEnergy(t, syncBody(oldLocked, start(2025, 6, 5), start(2025, 6, 20)), &resp)
	assert.Equal(t, []sentRequest{{meter: oldLocked, from: start(2025, 6, 16), to: end(2025, 6, 20)}}, sent)
	assert.Equal(t, syncResponse{Requested: 1, Skipped: []energyrequest.Skipped{}}, resp)
	assert.Equal(t, []energyrequest.MeterRange{{MeteringPoint: oldLocked, From: start(2025, 6, 16), To: end(2025, 6, 20)}}, run.Meters)

	// the new part was requested: only the old part is sent
	_, sent, first := syncEnergy(t, syncBody(newLocked, start(2025, 6, 16), start(2025, 6, 20)))
	require.Len(t, sent, 1)
	_, sent, _ = syncEnergy(t, syncBody(newLocked, start(2025, 6, 5), start(2025, 6, 20)))
	assert.Equal(t, []sentRequest{{meter: newLocked, from: start(2025, 6, 5), to: end(2025, 6, 15)}}, sent)

	// locked in the new member's period, the old entry not active there: the answer says locked
	until := time.UnixMilli(first.CreatedAt).Add(energyrequest.LockWindow).UnixMilli()
	_, sent, _ = syncEnergy(t, syncBody(newLocked, start(2025, 6, 17), start(2025, 6, 18)), &resp)
	assert.Empty(t, sent)
	assert.Equal(t, []energyrequest.Skipped{{MeteringPoint: newLocked, Reason: energyrequest.ReasonAlreadyRequested,
		Until: until}}, resp.Skipped)
}

// an archived entry (flag 2) is still skipped
func TestSyncEnergy_archivedEntryNotRequested(t *testing.T) {
	const zp = "AT0030000000000000000000000953095"
	ownerChange(t, zp, "2025-01-01", "2025-06-15", "2025-06-16", 2)

	var resp syncResponse
	code, sent, _ := syncEnergy(t, syncBody(zp, start(2025, 3, 1), start(2025, 3, 31)), &resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, sent)
	assert.Equal(t, []energyrequest.Skipped{{MeteringPoint: zp, Reason: energyrequest.ReasonNotActiveInPeriod}}, resp.Skipped,
		"the current entry's reason counts, the archived entry's NOT_ASSIGNED gives way")
}

// an archived entry still in INIT (first registration failed) does not decide the reason over the current entry
func TestSyncEnergy_archivedInitEntryGivesWay(t *testing.T) {
	const zp = "AT0030000000000000000000000953097"
	ownerChange(t, zp, "2025-01-01", "2025-06-15", "2025-06-16", 2)
	_, err := testDB.DbInstance.Exec(`UPDATE base.meteringpoint SET status = 'INIT' WHERE metering_point_id = $1 AND flag = 2`, zp)
	require.NoError(t, err)

	var resp syncResponse
	_, sent, _ := syncEnergy(t, syncBody(zp, start(2025, 3, 1), start(2025, 3, 31)), &resp)
	assert.Empty(t, sent)
	assert.Equal(t, []energyrequest.Skipped{{MeteringPoint: zp, Reason: energyrequest.ReasonNotActiveInPeriod}}, resp.Skipped)
}
