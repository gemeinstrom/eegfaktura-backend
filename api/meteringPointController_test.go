package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestMeteringHandler returns the metering point handler on the test
// database that TestMain (eegController_test.go) set up.
func newTestMeteringHandler(t *testing.T) *MeteringHandler {
	t.Helper()
	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	return NewMeteringHandler(db)
}

func TestRequestMeteringPointValues(t *testing.T) {
	type args struct {
		tenant      string
		request     string
		mqttReqFunc func(eeg *model.Eeg, meter *model.MeteringPoint, fromDate, toDate int64) error
	}

	tests := []struct {
		name      string
		args      args
		wantCalls int // how often the handler must ask MQTT for energy data
		check     func(t *testing.T, recorder *httptest.ResponseRecorder)
	}{
		{
			name: "Update EEG",
			args: args{
				tenant:  "TE001006",
				request: `{"meteringPoints": [{"meter": "AT000000000000000000001", "direction": "CONSUMPTION"}], "from": 1212001200120012, "to": 23423434243234234}`,
				mqttReqFunc: func(eeg *model.Eeg, meter *model.MeteringPoint, fromDate, toDate int64) error {
					return nil
				},
			},
			check: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, recorder.Code)
				// the old request shape gets the answer body: nothing requested, the unknown metering point skipped
				assert.JSONEq(t, `{"requested": 0, "skipped": [{"meteringPoint": "AT000000000000000000001", "reason": "NOT_FOUND"}]}`, recorder.Body.String())
			},
		},
		{
			// The requested period starts before the metering point is active
			// (fixture: activesince 2024-01-01), so the request starts at activation: the start of that day in local
			// time (before: UTC midnight, 01:00 local).
			name: "Update EEG - sepp",
			args: args{
				tenant:  "TE001006",
				request: fmt.Sprintf(`{"meteringPoints": [{"meter": "AT0030000000000000000000000953013", "direction": "CONSUMPTION"}], "from": %d, "to": 23423434243234234}`, time.Date(2023, time.Month(11), 1, 0, 0, 0, 0, time.Local).UnixMilli()),
				mqttReqFunc: func(eeg *model.Eeg, meter *model.MeteringPoint, fromDate, toDate int64) error {
					fmt.Printf("FromDate %s\n", time.UnixMilli(fromDate).String())
					assert.Equal(t, time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local).UnixMilli(), fromDate)
					return nil
				},
			},
			wantCalls: 1,
			check: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				fmt.Printf("recorder: %v\n", recorder)
				assert.Equal(t, http.StatusOK, recorder.Code)
			},
		},
	}

	h := newTestMeteringHandler(t)
	orig := mqttclient.RequestingEnergyData
	t.Cleanup(func() { mqttclient.RequestingEnergyData = orig })
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// the stored run would skip the metering point as ALREADY_REQUESTED in a repeated run (261105-mm6)
			isolateRuns(t)
			req, _ := http.NewRequest("POST", "/meteringpoint/syncenergy", strings.NewReader(tt.args.request))
			w := httptest.NewRecorder()
			calls := 0
			mqttclient.RequestingEnergyData = func(eeg *model.Eeg, meter *model.MeteringPoint, fromDate, toDate int64) error {
				calls++
				return tt.args.mqttReqFunc(eeg, meter, fromDate, toDate)
			}
			h.requestMeteringPointValues()(w, req, nil, tt.args.tenant)
			tt.check(t, w)
			assert.Equal(t, tt.wantCalls, calls, "energy data requests sent via MQTT")
		})
	}
}

func TestRequestChangePartitionFactor(t *testing.T) {
	data := `{"meteringPoints":[{"meter":"AT0030000000000000000000000953016","direction":"GENERATION","activation":"2022-01-01","partFact":1}]}`
	req, _ := http.NewRequest("POST", "/meteringpoint/changepartitionfactor", strings.NewReader(data))
	w := httptest.NewRecorder()
	orig := mqttclient.ChangePartitionFactor
	t.Cleanup(func() { mqttclient.ChangePartitionFactor = orig })
	calls := 0
	mqttclient.ChangePartitionFactor = func(eeg *model.Eeg, meter []*model.ChangePartitionFactorRequest) error {
		calls++
		assert.Equal(t, "TE001006", eeg.Id)
		return nil
	}

	newTestMeteringHandler(t).requestChangePartitionFactor()(w, req, nil, "TE001006")

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, 1, calls, "partition factor change requests sent via MQTT")
}
