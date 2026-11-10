package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// 261008-ca1: the revoke log names metering point ids, not the request with its free-text reason.
func TestRevokeLogsIdsOnly(t *testing.T) {
	var out bytes.Buffer
	orig := log.StandardLogger().Out
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(orig) })

	const pid = "ea1142dc-03da-11ee-b82b-5a985b4b0306"
	req, _ := http.NewRequest("POST", "/meteringpoint/"+pid+"/revokemeters",
		strings.NewReader(`{"meteringPoints":[{"meter":"AT0030000000000000000000099999998","direction":"CONSUMPTION"}],"from":1700000000000,"to":"Familie Obermueller zieht aus"}`))
	req = mux.SetURLVars(req, map[string]string{"pid": pid})
	w := httptest.NewRecorder()

	newTestMeteringHandler(t).requestRevokeMeteringPoint()(w, req, &middleware.PlatformClaims{Username: "test"}, "TE000006")

	assert.Contains(t, out.String(), "AT0030000000000000000000099999998")
	assert.NotContains(t, out.String(), "Obermueller")
}
