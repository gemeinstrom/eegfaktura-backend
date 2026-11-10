package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/energyrequest"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture TE001006: 953014 active 2024-01-01..2024-05-28, 953015 from 2024-05-20, 953016 from 2024-01-01. 953013 is
// used (and locked) by TestRequestMeteringPointValues, so these tests use other metering points and periods.
const (
	zp14 = "AT0030000000000000000000000953014"
	zp15 = "AT0030000000000000000000000953015"
	zp16 = "AT0030000000000000000000000953016"
)

// lastBody is the response body of the last syncEnergy call.
var lastBody string

// sentEntries are the metering point entries the last syncEnergy call sent requests for (in send order).
var sentEntries []*model.MeteringPoint

// failSend lets the MQTT mock of syncEnergy fail for these metering points.
var failSend = map[string]bool{}

type sentRequest struct {
	meter    string
	from, to int64
}

// syncResponse is the body of a syncenergy answer (online community).
type syncResponse struct {
	Requested int                     `json:"requested"`
	Skipped   []energyrequest.Skipped `json:"skipped"`
}

// syncEnergy posts body to the syncenergy handler of TE001006 and returns the status, the energy data requests
// sent via MQTT and the run stored last; the response body goes to resp.
func syncEnergy(t *testing.T, body string, resp ...*syncResponse) (int, []sentRequest, energyrequest.Run) {
	t.Helper()
	return syncEnergyWith(t, newTestMeteringHandler(t).db, body, resp...)
}

// syncEnergyWith is syncEnergy on db.
func syncEnergyWith(t *testing.T, db database.Database, body string, resp ...*syncResponse) (int, []sentRequest, energyrequest.Run) {
	t.Helper()
	isolateRuns(t)
	h := NewMeteringHandler(db)
	orig := mqttclient.RequestingEnergyData
	t.Cleanup(func() { mqttclient.RequestingEnergyData = orig })
	var sent []sentRequest
	sentEntries = nil
	mqttclient.RequestingEnergyData = func(eeg *model.Eeg, meter *model.MeteringPoint, fromDate, toDate int64) error {
		if failSend[meter.MeteringPoint] {
			return errors.New("broker not reachable")
		}
		sent = append(sent, sentRequest{meter: meter.MeteringPoint, from: fromDate, to: toDate})
		sentEntries = append(sentEntries, meter)
		return nil
	}
	req, _ := http.NewRequest("POST", "/meteringpoint/syncenergy", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.requestMeteringPointValues()(w, req, nil, "TE001006")
	lastBody = w.Body.String()
	if w.Code == http.StatusOK {
		var got syncResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		for _, r := range resp {
			*r = got
		}
	}

	return w.Code, sent, lastRun(t)
}

// isolateRuns removes, when the test ends, the rows of TE001006 it stored in base.notification (runs, answers).
// An answer goes to the oldest open request of the metering point, whatever its period, so a run another test
// left behind would take the answer of this one and the result would depend on the order of the tests.
func isolateRuns(t *testing.T) {
	t.Helper()
	var last int64
	require.NoError(t, testDB.DbInstance.Get(&last, `SELECT COALESCE(max(id), 0) FROM base.notification`))
	t.Cleanup(func() {
		_, err := testDB.DbInstance.Exec(`DELETE FROM base.notification WHERE tenant = 'TE001006' AND id > $1`, last)
		require.NoError(t, err)
	})
}

// lastRun returns the run of TE001006 stored last.
func lastRun(t *testing.T) energyrequest.Run {
	t.Helper()
	rows, _, err := newTestMeteringHandler(t).db.EnergyRequestCommunity(context.Background(), "TE001006")
	require.NoError(t, err)
	var last energyrequest.Run
	for _, r := range rows {
		if r.Type == energyrequest.RowTypeRun {
			last = energyrequest.Run{}
			require.NoError(t, json.Unmarshal(r.Message, &last))
		}
	}
	return last
}

func start(y int, m time.Month, d int) int64 {
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local).UnixMilli()
}

func end(y int, m time.Month, d int) int64 {
	return time.Date(y, m, d, 23, 45, 0, 0, time.Local).UnixMilli()
}

func TestSyncEnergy_periodsCompleteAndLock(t *testing.T) {
	// 953015 with its own period (times inside the day are cut to start and end of day), 953016 with the request's
	body := fmt.Sprintf(`{"meteringPoints": [
		{"meter": %q, "direction": "CONSUMPTION", "from": %d, "to": %d},
		{"meter": %q, "direction": "CONSUMPTION"}],
		"from": %d, "to": %d, "complete": [%q]}`,
		zp15, start(2025, 3, 5)+3_600_000, start(2025, 3, 7)+3_600_000, zp16,
		start(2025, 3, 1), start(2025, 3, 31), zp14)

	var resp syncResponse
	code, sent, first := syncEnergy(t, body, &resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, syncResponse{Requested: 2, Skipped: first.Skipped}, resp)
	assert.ElementsMatch(t, []sentRequest{
		{meter: zp15, from: start(2025, 3, 5), to: end(2025, 3, 7)},
		{meter: zp16, from: start(2025, 3, 1), to: end(2025, 3, 31)},
	}, sent)
	assert.Equal(t, []energyrequest.Skipped{{MeteringPoint: zp14, Reason: energyrequest.ReasonComplete}}, first.Skipped)
	assert.False(t, first.NoSummary, "complete metering points count for the summary")

	// the same request again within the hour: nothing is sent, the answer says why
	code, sent, second := syncEnergy(t, body, &resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, sent)
	assert.Equal(t, 0, resp.Requested)
	until := time.UnixMilli(first.CreatedAt).Add(energyrequest.LockWindow).UnixMilli()
	assert.ElementsMatch(t, []energyrequest.Skipped{
		{MeteringPoint: zp14, Reason: energyrequest.ReasonComplete},
		{MeteringPoint: zp15, Reason: energyrequest.ReasonAlreadyRequested, Until: until},
		{MeteringPoint: zp16, Reason: energyrequest.ReasonAlreadyRequested, Until: until},
	}, resp.Skipped)
	assert.Equal(t, first.RunId, second.RunId, "a run that sends nothing is not stored")

	// a rejected metering point is free at once
	h := newTestMeteringHandler(t)
	require.NoError(t, h.db.SaveNotification("TE001006", model.EBMS_ZP_REJ, []string{zp15}, []string{"86"}, model.CR_REQ_PT))
	_, sent, _ = syncEnergy(t, body)
	assert.Equal(t, []sentRequest{{meter: zp15, from: start(2025, 3, 5), to: end(2025, 3, 7)}}, sent)

	// a single request is locked too; a period that does not overlap is free
	single := func(from, to int64) string {
		return fmt.Sprintf(`{"meteringPoints": [{"meter": %q, "direction": "CONSUMPTION"}], "from": %d, "to": %d}`,
			zp16, from, to)
	}
	code, sent, _ = syncEnergy(t, single(start(2025, 3, 31), start(2025, 4, 30)), &resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, sent, "31.03. was requested already")
	assert.Equal(t, syncResponse{Requested: 0, Skipped: []energyrequest.Skipped{{MeteringPoint: zp16,
		Reason: energyrequest.ReasonAlreadyRequested, Until: until}}}, resp, "a locked single request says why")
	_, sent, _ = syncEnergy(t, single(start(2025, 4, 1), start(2025, 4, 30)))
	assert.Equal(t, []sentRequest{{meter: zp16, from: start(2025, 4, 1), to: end(2025, 4, 30)}}, sent)
}

// contract: a request without the new fields requests every metering point for the request period, as before.
func TestSyncEnergy_withoutNewFieldsAsBefore(t *testing.T) {
	body := fmt.Sprintf(`{"meteringPoints": [{"meter": %q, "direction": "CONSUMPTION"}, {"meter": %q, "direction": "CONSUMPTION"}], "from": %d, "to": %d}`,
		zp15, zp16, start(2025, 6, 1), start(2025, 6, 30))
	var resp syncResponse
	code, sent, run := syncEnergy(t, body, &resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, syncResponse{Requested: 2, Skipped: []energyrequest.Skipped{}}, resp)
	assert.ElementsMatch(t, []sentRequest{
		{meter: zp15, from: start(2025, 6, 1), to: end(2025, 6, 30)},
		{meter: zp16, from: start(2025, 6, 1), to: end(2025, 6, 30)},
	}, sent)
	assert.Equal(t, start(2025, 6, 1), run.From)
	assert.Equal(t, end(2025, 6, 30), run.To)
	assert.Empty(t, run.Skipped)
}

// web found every metering point complete: nothing is requested and no run is stored, the answer says so.
func TestSyncEnergy_allComplete(t *testing.T) {
	body := fmt.Sprintf(`{"meteringPoints": [], "from": %d, "to": %d, "complete": [%q, %q]}`,
		start(2025, 8, 1), start(2025, 8, 31), zp15, zp16)
	before := lastRun(t)
	var resp syncResponse
	code, sent, run := syncEnergy(t, body, &resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, sent)
	want := []energyrequest.Skipped{{MeteringPoint: zp15, Reason: energyrequest.ReasonComplete},
		{MeteringPoint: zp16, Reason: energyrequest.ReasonComplete}}
	assert.Equal(t, syncResponse{Requested: 0, Skipped: want}, resp)
	assert.Equal(t, before.RunId, run.RunId, "no run stored")
}

// a request that could not be sent is not locked: it can be sent again at once; only the sent ones are locked.
func TestSyncEnergy_failedSendIsNotLocked(t *testing.T) {
	t.Cleanup(func() { failSend = map[string]bool{} })
	body := fmt.Sprintf(`{"meteringPoints": [{"meter": %q, "direction": "CONSUMPTION"}, {"meter": %q, "direction": "CONSUMPTION"}], "from": %d, "to": %d}`,
		zp15, zp16, start(2025, 10, 1), start(2025, 10, 31))
	before := lastRun(t)

	failSend = map[string]bool{zp15: true, zp16: true}
	code, sent, run := syncEnergy(t, body)
	assert.Equal(t, http.StatusInternalServerError, code)
	assert.Empty(t, sent)
	assert.Contains(t, lastBody, zp15, "the 500 lists the failures as before")
	assert.Contains(t, lastBody, zp16)
	assert.Equal(t, before.RunId, run.RunId, "nothing sent: the run is deleted")

	failSend = map[string]bool{zp15: true}
	code, sent, run = syncEnergy(t, body)
	assert.Equal(t, http.StatusInternalServerError, code)
	assert.Equal(t, []sentRequest{{meter: zp16, from: start(2025, 10, 1), to: end(2025, 10, 31)}}, sent)
	assert.Equal(t, []energyrequest.MeterRange{{MeteringPoint: zp16, From: start(2025, 10, 1), To: end(2025, 10, 31)}}, run.Meters,
		"only the sent metering point stays in the run")

	failSend = map[string]bool{}
	code, sent, _ = syncEnergy(t, body)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, []sentRequest{{meter: zp15, from: start(2025, 10, 1), to: end(2025, 10, 31)}}, sent,
		"the failed one is sent again, the sent one is locked")
}

// with periods per metering point (here without a request period) the run carries the span actually requested
func TestSyncEnergy_runPeriodIsTheRequestedSpan(t *testing.T) {
	body := fmt.Sprintf(`{"meteringPoints": [
		{"meter": %q, "direction": "CONSUMPTION", "from": %d, "to": %d},
		{"meter": %q, "direction": "CONSUMPTION", "from": %d, "to": %d}], "from": 0, "to": 0}`,
		zp15, start(2025, 11, 3), start(2025, 11, 5), zp16, start(2025, 11, 10), start(2025, 11, 12))
	code, sent, run := syncEnergy(t, body)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, sent, 2)
	assert.Equal(t, start(2025, 11, 3), run.From)
	assert.Equal(t, end(2025, 11, 12), run.To)
}

// a metering point in meteringPoints and in complete: meteringPoints wins
func TestSyncEnergy_meteringPointsBeforeComplete(t *testing.T) {
	body := fmt.Sprintf(`{"meteringPoints": [{"meter": %q, "direction": "CONSUMPTION"}], "from": %d, "to": %d, "complete": [%q, %q]}`,
		zp15, start(2025, 12, 1), start(2025, 12, 31), zp15, zp14)
	var resp syncResponse
	_, sent, _ := syncEnergy(t, body, &resp)
	assert.Equal(t, []sentRequest{{meter: zp15, from: start(2025, 12, 1), to: end(2025, 12, 31)}}, sent)
	assert.Equal(t, []energyrequest.Skipped{{MeteringPoint: zp14, Reason: energyrequest.ReasonComplete}}, resp.Skipped)

	_, sent, _ = syncEnergy(t, body, &resp)
	assert.Empty(t, sent)
	require.Len(t, resp.Skipped, 2)
	assert.Equal(t, energyrequest.ReasonComplete, resp.Skipped[0].Reason)
	assert.Equal(t, zp15, resp.Skipped[1].MeteringPoint)
	assert.Equal(t, energyrequest.ReasonAlreadyRequested, resp.Skipped[1].Reason, "its own reason, not COMPLETE")
}

// failingRunStore cannot store a run.
type failingRunStore struct{ database.Database }

func (failingRunStore) SaveEnergyRequestRow(context.Context, string, string, interface{}) (int64, error) {
	return 0, errors.New("database not reachable")
}

// a run that cannot be stored is not sent: without it nothing locks the metering points or summarises the answers
func TestSyncEnergy_runNotStoredIsNotSent(t *testing.T) {
	body := fmt.Sprintf(`{"meteringPoints": [{"meter": %q, "direction": "CONSUMPTION"}, {"meter": %q, "direction": "CONSUMPTION"}], "from": %d, "to": %d}`,
		zp15, zp16, start(2026, 1, 1), start(2026, 1, 31))
	before := lastRun(t)
	code, sent, run := syncEnergyWith(t, failingRunStore{newTestMeteringHandler(t).db}, body)
	assert.Equal(t, http.StatusInternalServerError, code)
	assert.Empty(t, sent)
	assert.Equal(t, before.RunId, run.RunId)

	code, sent, _ = syncEnergy(t, body)
	assert.Equal(t, http.StatusOK, code)
	assert.Len(t, sent, 2, "not locked: sent on the next request")
}

// failingRowStore cannot read the energy request rows.
type failingRowStore struct{ database.Database }

func (failingRowStore) EnergyRequestCommunity(context.Context, string) ([]energyrequest.Row, map[string]bool, error) {
	return nil, nil, errors.New("database not reachable")
}

// the lock reads the rows only when a metering point can be sent: nothing to send, nothing read
func TestSyncEnergy_nothingToSendReadsNoRows(t *testing.T) {
	db := failingRowStore{newTestMeteringHandler(t).db}
	body := fmt.Sprintf(`{"meteringPoints": [{"meter": %q, "direction": "CONSUMPTION"}], "from": %d, "to": %d, "complete": [%q]}`,
		zp14, start(2026, 2, 1), start(2026, 2, 28), zp15)
	var resp syncResponse
	code, sent, _ := syncEnergyWith(t, db, body, &resp)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, sent)
	assert.Equal(t, syncResponse{Requested: 0, Skipped: []energyrequest.Skipped{
		{MeteringPoint: zp15, Reason: energyrequest.ReasonComplete},
		{MeteringPoint: zp14, Reason: energyrequest.ReasonNotActiveInPeriod}}}, resp)

	// a metering point that can be sent needs the rows
	body = fmt.Sprintf(`{"meteringPoints": [{"meter": %q, "direction": "CONSUMPTION"}], "from": %d, "to": %d}`,
		zp16, start(2026, 2, 1), start(2026, 2, 28))
	code, sent, _ = syncEnergyWith(t, db, body)
	assert.Equal(t, http.StatusInternalServerError, code)
	assert.Empty(t, sent)
}
