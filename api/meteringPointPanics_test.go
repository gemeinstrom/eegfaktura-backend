package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

// 261005-ca6: incomplete requests answer 400/404 instead of panicking.
func TestMeteringPointRoutesDoNotPanicOnIncompleteInput(t *testing.T) {
	const (
		tenant = "TE001006"
		pid    = "04583ce3-5802-40b9-afd9-29d09b908f1f"
		mid    = "AT0030000000000000000000000953013"
	)
	h := newTestMeteringHandler(t)
	claims := &middleware.PlatformClaims{Username: "test"}

	run := func(handler middleware.JWTHandlerFunc, method, body string, vars map[string]string) *httptest.ResponseRecorder {
		req, _ := http.NewRequest(method, "/meteringpoint", strings.NewReader(body))
		req = mux.SetURLVars(req, vars)
		w := httptest.NewRecorder()
		assert.NotPanics(t, func() { handler(w, req, claims, tenant) })
		return w
	}

	t.Run("update id without newId", func(t *testing.T) {
		w := run(h.updateMeteringPointId(), "PUT", `{}`, map[string]string{"pid": pid, "mid": mid})
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("register a metering point the participant does not have", func(t *testing.T) {
		w := run(h.registerMeteringPoint(), "POST", `{"meteringPoint":"AT0030000000000000000000099999999"}`, map[string]string{"pid": pid})
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("update without participantState", func(t *testing.T) {
		w := run(h.updateMeteringPoint(), "PUT", `{"direction":"CONSUMPTION","status":"ACTIVE"}`, map[string]string{"pid": pid, "mid": mid})
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// 261005-ca6: a null entry in a partition factor request answers 400 on both routes.
func TestPartitionFactorRoutesRefuseNullEntry(t *testing.T) {
	mh := newTestMeteringHandler(t)
	ah := NewApiHandler(mh.db)
	for name, handler := range map[string]middleware.JWTHandlerFunc{
		"changepartitionfactor": mh.requestChangePartitionFactor(),
		"updatepartfact":        ah.updateParticipantFactorAPI(),
	} {
		t.Run(name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/"+name, strings.NewReader(`{"meteringPoints":[null]}`))
			w := httptest.NewRecorder()
			assert.NotPanics(t, func() { handler(w, req, nil, "TE001006") })
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}
