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

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/logging"
	"at.ourproject/vfeeg-backend/logging/logcapture"
	"at.ourproject/vfeeg-backend/logging/logtest"
	"at.ourproject/vfeeg-backend/model"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	siTenant      = "TE000231"
	siOtherTenant = "TE000232"
)

// POST /eeg/settlementinterval (konzept-abrechnungsintervall.md): rule, record and the contract with web.
func TestChangeSettlementInterval(t *testing.T) {
	ctx := context.Background()
	db, err := database.GetDB(ctx)
	require.NoError(t, err)
	exec := func(stmt string, args ...interface{}) {
		_, err := execTestSQL(ctx, db, stmt, args...)
		require.NoError(t, err)
	}
	removeFixture := func() {
		for _, table := range []string{"base.settlement_interval_change", "base.eeg"} {
			exec(`DELETE FROM `+table+` WHERE tenant IN ($1, $2)`, siTenant, siOtherTenant)
		}
	}
	removeFixture()
	t.Cleanup(removeFixture)
	for tenant, communityId := range map[string]string{siTenant: "AT00300000000TC000231000000000001",
		siOtherTenant: "AT00300000000TC000232000000000001"} {
		exec(`INSERT INTO base.eeg
			(tenant, name, description, "rcNumber", area, gridoperator_code, gridoperator_name, "communityId",
			 street, "streetNumber", city, zip, email, "settlementInterval")
			VALUES ($1, 'INTERVALL', 'Intervall-Test', $2, 'LOCAL', 'AT003000', 'Netz OÖ', $3,
			 'Weg', '1', 'Linz', '4020', 'intervall@example.org', 'MONTHLY')`, tenant, tenant, communityId)
	}
	// every subtest starts from MONTHLY without a record, whatever ran before
	reset := func() {
		exec(`UPDATE base.eeg SET "settlementInterval" = 'MONTHLY' WHERE tenant IN ($1, $2)`, siTenant, siOtherTenant)
		exec(`DELETE FROM base.settlement_interval_change WHERE tenant IN ($1, $2)`, siTenant, siOtherTenant)
	}

	orig := middleware.VerifyTokenClaims
	t.Cleanup(func() { middleware.VerifyTokenClaims = orig })
	middleware.VerifyTokenClaims = func(_ context.Context, token string) (*middleware.PlatformClaims, error) {
		c := &middleware.PlatformClaims{Username: "admin-" + token, Tenants: []string{strings.ToLower(siTenant)},
			AccessGroups: middleware.AccessGroups{"/EEG_ADMIN"}}
		switch token {
		case "user":
			c.AccessGroups = middleware.AccessGroups{"/EEG_USER"}
		case "other":
			c.Tenants = []string{siOtherTenant}
		}
		return c, nil
	}
	// AccessLog as in server.go: it puts the checked tenant on the lines of the request
	router := logging.AccessLog(InitEegRouter(mux.NewRouter(), db))
	call := func(token, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Authorization", middleware.BEARER_SCHEMA+token)
		req.Header.Set("tenant", siTenant)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	post := func(token, body string) *httptest.ResponseRecorder {
		return call(token, "/eeg/settlementinterval", body)
	}
	decode := func(w *httptest.ResponseRecorder) map[string]interface{} {
		var m map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m), w.Body.String())
		return m
	}
	errorOf := func(w *httptest.ResponseRecorder) HttpError {
		var resp struct {
			Error HttpError `json:"error"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
		return resp.Error
	}
	interval := func() string {
		eeg, err := db.GetEegById(ctx, siTenant)
		require.NoError(t, err)
		return eeg.SettlementInterval
	}
	const billedToMar = `"billingState":{"finalPeriods":["Abr_YM-2026-3","Abr_YM-2026-2"],"runningPeriods":[]}`
	const billedToFeb = `"billingState":{"finalPeriods":["Abr_YM-2026-2"],"runningPeriods":[]}`

	t.Run("access", func(t *testing.T) {
		reset()
		assert.Equal(t, http.StatusUnauthorized, post("user", `{"settlementInterval":"QUARTER","dryRun":true,`+billedToMar+`}`).Code)
		assert.Equal(t, http.StatusForbidden, post("other", `{"settlementInterval":"QUARTER","dryRun":true,`+billedToMar+`}`).Code)
	})
	t.Run("400", func(t *testing.T) {
		reset()
		for body, code := range map[string]int{
			`{"settlementInterval":"YEARLY","dryRun":true,` + billedToMar + `}`:  1060,
			`{"settlementInterval":"","dryRun":true,` + billedToMar + `}`:        1060,
			`{"settlementInterval":"QUARTER","dryRun":true}`:                     1060,
			`{"settlementInterval":"QUARTER","dryRun":true,"billingState":null}`: 1060,
			// eegfaktura-dev#54 #9: incomplete or unreadable state, data after the body
			`{"settlementInterval":"ANNUAL","dryRun":false,"billingState":{}}`:                                                                     1060,
			`{"settlementInterval":"ANNUAL","dryRun":false,"billingState":{"finalPeriods":null,"runningPeriods":[]}}`:                              1060,
			`{"settlementInterval":"ANNUAL","dryRun":false,"billingState":{"finalPeriods":[],"runningPeriods":null}}`:                              1060,
			`{"settlementInterval":"ANNUAL","dryRun":false,"billingState":{"finalPeriods":["foo"],"runningPeriods":[]}}`:                           1060,
			`{"settlementInterval":"ANNUAL","dryRun":false,"billingState":{"finalPeriods":["Abr_YM-2026-1"],"runningPeriods":["Abr_YM-2026-13"]}}`: 1060,
			`{"settlementInterval":"ANNUAL","dryRun":false,"billingState":{"finalPeriods":["Abr_Y-9999"],"runningPeriods":[]}}`:                    1060,
			`{"settlementInterval":"QUARTER","dryRun":false,` + billedToMar + `}garbage`:                                                           2000,
			`{"settlementInterval":"QUARTER","dryRun":false,` + billedToMar + `}}`:                                                                 2000,
			`{"settlementInterval":"QUARTER","dryRun":false,` + billedToMar + `} {}`:                                                               2000,
			`{"settlementInterval":"MONTHLY","dryRun":true,` + billedToMar + `}`:                                                                   1061,
			`{"settlementInterval":`: 2000,
		} {
			w := post("admin", body)
			require.Equal(t, http.StatusBadRequest, w.Code, body)
			assert.Equal(t, code, errorOf(w).Code, body)
		}
		assert.Equal(t, "MONTHLY", interval())
	})
	t.Run("unreadable request: a warning, no Go names in the answer", func(t *testing.T) {
		reset()
		buf := logcapture.JSON(t)
		for _, body := range []string{`{"settlementInterval":"QUARTER","dryRun":"yes",` + billedToMar + `}`,
			`{"settlementInterval":"QUARTER","dryRun":true,` + billedToMar + `}x`} {
			w := post("admin", body)
			require.Equal(t, http.StatusBadRequest, w.Code, body)
			assert.Equal(t, HttpError{Code: 2000, Error: "Bad Process", Message: "Die Anfrage konnte nicht gelesen werden (kein gültiges JSON)"},
				errorOf(w), body)
		}
		// over the size limit: its own text
		w := post("admin", `{"settlementInterval":"QUARTER","dryRun":true,"billingState":{"finalPeriods":["`+
			strings.Repeat("x", maxSettlementIntervalBytes)+`"],"runningPeriods":[]}}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, HttpError{Code: 2000, Error: "Bad Process", Message: "Die Anfrage ist zu groß"}, errorOf(w))
		var warnings int
		for _, l := range logtest.Lines(t, buf) {
			assert.NotEqual(t, "error", l["log.level"], l)
			if l["event.action"] == "eeg.settlement_interval.refused" {
				assert.Equal(t, "warn", l["log.level"])
				assert.Equal(t, float64(2000), l["code"])
				assert.NotEmpty(t, l["error.message"], "the decoder's text stays in the log")
				warnings++
			}
		}
		assert.Equal(t, 3, warnings, buf.String())
	})
	// eegfaktura-dev#54 #5: a change and a refusal are logged with an event, old and new value, without the user name
	t.Run("events", func(t *testing.T) {
		reset()
		buf := logcapture.JSON(t)
		require.Equal(t, http.StatusConflict, post("admin", `{"settlementInterval":"QUARTER","dryRun":false,`+billedToFeb+`}`).Code)
		require.Equal(t, http.StatusBadRequest, post("admin", `{"settlementInterval":"MONTHLY","dryRun":false,`+billedToMar+`}`).Code)
		require.Equal(t, http.StatusOK, post("admin", `{"settlementInterval":"QUARTER","dryRun":true,`+billedToMar+`}`).Code)
		require.Equal(t, http.StatusOK, post("admin", `{"settlementInterval":"QUARTER","dryRun":false,`+billedToMar+`}`).Code)

		var events []map[string]any
		for _, l := range logtest.Lines(t, buf) {
			assert.NotContains(t, fmt.Sprint(l), "admin-admin", "no user name")
			if a, _ := l["event.action"].(string); strings.HasPrefix(a, "eeg.settlement_interval.") {
				delete(l, "@timestamp")
				delete(l, "request.id")
				for k := range l {
					if strings.HasPrefix(k, "log.origin") || strings.HasPrefix(k, "service.") {
						delete(l, k)
					}
				}
				events = append(events, l)
			}
		}
		require.Len(t, events, 3, buf.String())
		assert.Equal(t, map[string]any{"event.action": "eeg.settlement_interval.refused", "event.outcome": "failure",
			"log.level": "info", "message": "settlement interval change refused", "tenant.id": siTenant, "code": float64(1062),
			"event.reason":           "Zuerst muss noch der Zeitraum März 2026 im bisherigen Intervall abgerechnet werden. Ein Wechsel ist dann ab 1. April 2026 möglich.",
			"settlementInterval.old": "MONTHLY", "settlementInterval.new": "QUARTER"}, events[0])
		assert.Equal(t, map[string]any{"event.action": "eeg.settlement_interval.refused", "event.outcome": "failure",
			"log.level": "warn", "message": "settlement interval change refused", "tenant.id": siTenant, "code": float64(1061),
			"event.reason":           "Das Abrechnungsintervall ist bereits eingestellt",
			"settlementInterval.old": "MONTHLY", "settlementInterval.new": "MONTHLY"}, events[1])
		assert.Equal(t, map[string]any{"event.action": "eeg.settlement_interval.changed", "event.outcome": "success",
			"log.level": "info", "message": "settlement interval changed", "tenant.id": siTenant,
			"settlementInterval.old": "MONTHLY", "settlementInterval.new": "QUARTER", "settlementInterval.effectiveFrom": "2026-04-01"}, events[2])
	})
	t.Run("dry run allowed", func(t *testing.T) {
		reset()
		w := post("admin", `{"settlementInterval":"QUARTER","dryRun":true,`+billedToMar+"}\n")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, map[string]interface{}{"allowed": true, "effectiveFrom": "2026-04-01", "lastFinalPeriod": "Abr_YM-2026-3",
			"message": "Das neue Intervall gilt ab 1. April 2026.", "earliestFrom": nil, "eeg": nil}, decode(w))
		assert.Equal(t, "MONTHLY", interval())
	})
	t.Run("dry run blocked (R2, rule 6, R3)", func(t *testing.T) {
		reset()
		w := post("admin", `{"settlementInterval":"QUARTER","dryRun":true,`+billedToFeb+`}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, map[string]interface{}{"allowed": false, "effectiveFrom": nil, "lastFinalPeriod": "Abr_YM-2026-2",
			"message":      "Zuerst muss noch der Zeitraum März 2026 im bisherigen Intervall abgerechnet werden. Ein Wechsel ist dann ab 1. April 2026 möglich.",
			"earliestFrom": "2026-04-01", "eeg": nil}, decode(w))

		// rule 6 (eegfaktura-dev#54 #2): February between two final months blocks
		w = post("admin", `{"settlementInterval":"QUARTER","dryRun":true,"billingState":{"finalPeriods":["Abr_YM-2026-1","Abr_YM-2026-3"],"runningPeriods":[]}}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, map[string]interface{}{"allowed": false, "effectiveFrom": nil, "lastFinalPeriod": "Abr_YM-2026-3",
			"message": "Februar 2026 ist noch nicht endgültig abgerechnet.", "earliestFrom": nil, "eeg": nil}, decode(w))

		w = post("admin", `{"settlementInterval":"QUARTER","dryRun":true,"billingState":{"finalPeriods":["Abr_YM-2026-3"],"runningPeriods":["Abr_YM-2026-4"]}}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		m := decode(w)
		assert.Equal(t, false, m["allowed"])
		assert.Nil(t, m["earliestFrom"])
		assert.Equal(t, "Für April 2026 läuft gerade eine Berechnung. Bitte nach deren Ende erneut versuchen.", m["message"])
	})
	t.Run("blocked change: 409 with the reason", func(t *testing.T) {
		reset()
		w := post("admin", `{"settlementInterval":"QUARTER","dryRun":false,`+billedToFeb+`}`)
		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		e := errorOf(w)
		assert.Equal(t, 1062, e.Code)
		assert.Equal(t, "Zuerst muss noch der Zeitraum März 2026 im bisherigen Intervall abgerechnet werden. Ein Wechsel ist dann ab 1. April 2026 möglich.", e.Message)
		assert.Equal(t, "MONTHLY", interval())
	})
	t.Run("generic update drops settlementInterval", func(t *testing.T) {
		reset()
		w := call("admin", "/eeg", `{"settlementInterval":"ANNUAL","description":"Intervall-Test 2"}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, "MONTHLY", decode(w)["settlementInterval"])
		assert.Equal(t, "MONTHLY", interval())
	})
	// platform#229, E5: the type and the allocation mode are set at registration, otherwise only by the operator
	t.Run("generic update drops area and allocationMode", func(t *testing.T) {
		before, err := db.GetEegById(ctx, siTenant)
		require.NoError(t, err)
		require.NotEqual(t, "P2P", string(before.Area), "fixture")
		require.NotEqual(t, "STATIC", string(before.AllocationMode), "fixture")
		t.Cleanup(func() { exec(`UPDATE base.eeg SET description = $1 WHERE tenant = $2`, before.Description, siTenant) })
		w := call("admin", "/eeg", `{"area":"P2P","allocationMode":"STATIC","description":"Typ-Test"}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		after, err := db.GetEegById(ctx, siTenant)
		require.NoError(t, err)
		assert.Equal(t, "Typ-Test", after.Description)
		assert.Equal(t, before.Area, after.Area)
		assert.Equal(t, before.AllocationMode, after.AllocationMode)
	})
	t.Run("allowed change: stored, recorded, EEG returned", func(t *testing.T) {
		reset()
		w := post("admin", `{"settlementInterval":"QUARTER","dryRun":false,`+billedToMar+`}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		m := decode(w)
		assert.Equal(t, true, m["allowed"])
		assert.Equal(t, "2026-04-01", m["effectiveFrom"])
		require.IsType(t, map[string]interface{}{}, m["eeg"])
		assert.Equal(t, "QUARTER", m["eeg"].(map[string]interface{})["settlementInterval"])
		assert.Equal(t, "QUARTER", interval())

		var by []string
		require.NoError(t, db.Select(ctx, &by, `SELECT changed_by FROM base.settlement_interval_change WHERE tenant = $1`, siTenant))
		assert.Equal(t, []string{"admin-admin"}, by)
		other, err := db.GetEegById(ctx, siOtherTenant)
		require.NoError(t, err)
		assert.Equal(t, "MONTHLY", other.SettlementInterval, "the other tenant is not touched")
	})
	t.Run("stored change, reading back fails: 200 without the EEG, a warning", func(t *testing.T) {
		reset()
		buf := logcapture.JSON(t)
		failing := logging.AccessLog(InitEegRouter(mux.NewRouter(), reloadFails{db}))
		req := httptest.NewRequest(http.MethodPost, "/eeg/settlementinterval",
			strings.NewReader(`{"settlementInterval":"QUARTER","dryRun":false,`+billedToMar+`}`))
		req.Header.Set("Authorization", middleware.BEARER_SCHEMA+"admin")
		req.Header.Set("tenant", siTenant)
		w := httptest.NewRecorder()
		failing.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		m := decode(w)
		assert.Nil(t, m["eeg"])
		assert.Equal(t, "Das neue Intervall gilt ab 1. April 2026. Die Gemeinschaft konnte danach nicht neu geladen werden, bitte die Seite neu laden.", m["message"])
		assert.Equal(t, "QUARTER", interval(), "stored")
		var reload []map[string]any
		for _, l := range logtest.Lines(t, buf) {
			assert.NotEqual(t, "error", l["log.level"], l)
			if l["event.action"] == "eeg.settlement_interval.reload_failed" {
				reload = append(reload, l)
			}
		}
		require.Len(t, reload, 1, buf.String())
		assert.Equal(t, "warn", reload[0]["log.level"])
	})
}

// reloadFails is the database whose GetEegById fails, as after a lost connection.
type reloadFails struct{ database.Database }

func (reloadFails) GetEegById(context.Context, string) (*model.Eeg, error) {
	return nil, errors.New("connection lost")
}
