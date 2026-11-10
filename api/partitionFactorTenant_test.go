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

// 261005-ca4: a partition factor change is only sent for metering points of the
// caller's community. AT...30041725 belongs to TE000002 in the fixture.
func TestRequestChangePartitionFactorRefusesForeignMeter(t *testing.T) {
	orig := mqttclient.ChangePartitionFactor
	t.Cleanup(func() { mqttclient.ChangePartitionFactor = orig })
	calls := 0
	mqttclient.ChangePartitionFactor = func(eeg *model.Eeg, meter []*model.ChangePartitionFactorRequest) error {
		calls++
		return nil
	}

	for _, tc := range []struct{ name, data string }{
		{"meter of another community", `{"meteringPoints":[{"meter":"AT0030000000000000000000030041725","direction":"GENERATION","activation":"2022-01-01","partFact":1}]}`},
		{"own and foreign meter", `{"meteringPoints":[{"meter":"AT0030000000000000000000000953016","direction":"GENERATION","activation":"2022-01-01","partFact":1},{"meter":"AT0030000000000000000000030041725","direction":"GENERATION","activation":"2022-01-01","partFact":1}]}`},
		{"unknown meter", `{"meteringPoints":[{"meter":"AT0030000000000000000000099999999","direction":"GENERATION","activation":"2022-01-01","partFact":1}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/meteringpoint/changepartitionfactor", strings.NewReader(tc.data))
			w := httptest.NewRecorder()

			newTestMeteringHandler(t).requestChangePartitionFactor()(w, req, nil, "TE001006")

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
	assert.Equal(t, 0, calls, "no partition factor change may be sent via MQTT")
}

// 261005-ca4: POST /master/updatepartfact applies the same check.
func TestUpdateParticipantFactorAPIRefusesForeignMeter(t *testing.T) {
	orig := mqttclient.ChangePartitionFactor
	t.Cleanup(func() { mqttclient.ChangePartitionFactor = orig })
	calls := 0
	mqttclient.ChangePartitionFactor = func(eeg *model.Eeg, meter []*model.ChangePartitionFactorRequest) error {
		calls++
		return nil
	}
	h := NewApiHandler(newTestMeteringHandler(t).db)

	for _, tc := range []struct{ name, data string }{
		{"meter of another community", `{"meteringPoints":[{"meter":"AT0030000000000000000000030041725","direction":"GENERATION","activation":"2022-01-01","partFact":1}]}`},
		{"own and foreign meter", `{"meteringPoints":[{"meter":"AT0030000000000000000000000953016","direction":"GENERATION","activation":"2022-01-01","partFact":1},{"meter":"AT0030000000000000000000030041725","direction":"GENERATION","activation":"2022-01-01","partFact":1}]}`},
		{"unknown meter", `{"meteringPoints":[{"meter":"AT0030000000000000000000099999999","direction":"GENERATION","activation":"2022-01-01","partFact":1}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/master/updatepartfact", strings.NewReader(tc.data))
			w := httptest.NewRecorder()

			h.updateParticipantFactorAPI()(w, req, nil, "TE001006")

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
	assert.Equal(t, 0, calls, "no partition factor change may be sent via MQTT")

	// an own metering point still goes through
	req, _ := http.NewRequest("POST", "/master/updatepartfact", strings.NewReader(`{"meteringPoints":[{"meter":"AT0030000000000000000000000953016","direction":"GENERATION","activation":"2022-01-01","partFact":1}]}`))
	w := httptest.NewRecorder()
	h.updateParticipantFactorAPI()(w, req, nil, "TE001006")
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, calls)
}

// 261005-ca4: direction and grid operator of a partition factor change come from the stored
// metering point; AT...00953016 is CONSUMPTION in the fixture, without a grid operator of its own.
func TestChangePartitionFactorTakesDirectionAndGridOperatorFromTheMeter(t *testing.T) {
	orig := mqttclient.ChangePartitionFactor
	t.Cleanup(func() { mqttclient.ChangePartitionFactor = orig })
	var sent []*model.ChangePartitionFactorRequest
	mqttclient.ChangePartitionFactor = func(eeg *model.Eeg, meter []*model.ChangePartitionFactorRequest) error {
		sent = meter
		return nil
	}

	data := `{"meteringPoints":[{"meter":"AT0030000000000000000000000953016","direction":"GENERATION","gridOperatorId":"AT999999","activation":"2022-01-01","partFact":50}]}`
	req, _ := http.NewRequest("POST", "/meteringpoint/changepartitionfactor", strings.NewReader(data))
	w := httptest.NewRecorder()

	newTestMeteringHandler(t).requestChangePartitionFactor()(w, req, nil, "TE001006")

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.Len(t, sent, 1)
	assert.Equal(t, model.CONSUMPTION, sent[0].Direction)
	assert.False(t, sent[0].GridOperatorId.Valid, "the fixture stores no grid operator")
	assert.Equal(t, 50, sent[0].PartFact)
}
