package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/model"
	mqttclient "at.ourproject/vfeeg-backend/mqtt"
	"github.com/stretchr/testify/assert"
)

// countingWriter records every WriteHeader call; httptest.ResponseRecorder keeps only the first.
type countingWriter struct {
	*httptest.ResponseRecorder
	codes []int
}

func (c *countingWriter) WriteHeader(code int) {
	c.codes = append(c.codes, code)
	c.ResponseRecorder.WriteHeader(code)
}

// 261015-ca2: when the MQTT call fails, both partition factor routes answer 500 and
// write no success status afterwards.
func TestPartitionFactorRoutesStopAfterMqttError(t *testing.T) {
	orig := mqttclient.ChangePartitionFactor
	t.Cleanup(func() { mqttclient.ChangePartitionFactor = orig })
	mqttclient.ChangePartitionFactor = func(eeg *model.Eeg, meter []*model.ChangePartitionFactorRequest) error {
		return errors.New("broker down")
	}

	mh := newTestMeteringHandler(t)
	ah := NewApiHandler(mh.db)
	for name, handler := range map[string]middleware.JWTHandlerFunc{
		"changepartitionfactor": mh.requestChangePartitionFactor(),
		"updatepartfact":        ah.updateParticipantFactorAPI(),
	} {
		t.Run(name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/"+name, strings.NewReader(`{"meteringPoints":[{"meter":"AT0030000000000000000000000953016","direction":"GENERATION","activation":"2022-01-01","partFact":1}]}`))
			w := &countingWriter{ResponseRecorder: httptest.NewRecorder()}

			handler(w, req, nil, "TE001006")

			assert.Equal(t, []int{http.StatusInternalServerError}, w.codes)
		})
	}
}
