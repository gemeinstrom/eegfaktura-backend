package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database/dbmock"
	"at.ourproject/vfeeg-backend/logging"
	"at.ourproject/vfeeg-backend/logging/logtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 261008-ca1: without LOG_LEVEL the backend logs at info, not debug; an
// unknown value now stops the start (logging concept, 2026-10-30).
func TestLogLevelDefaultsToInfo(t *testing.T) {
	cfg, err := logging.ConfigFromEnv(func(string) (string, bool) { return "", false })
	require.NoError(t, err)
	assert.Equal(t, slog.LevelInfo, cfg.Level)

	cfg, err = logging.ConfigFromEnv(func(k string) (string, bool) { return "debug", k == "LOG_LEVEL" })
	require.NoError(t, err)
	assert.Equal(t, slog.LevelDebug, cfg.Level)

	_, err = logging.ConfigFromEnv(func(k string) (string, bool) { return "verbose", k == "LOG_LEVEL" })
	assert.Error(t, err)
}

// accessLines sends requests through the real router (InitRouters + InitGraphQL,
// wrapped in AccessLog as in StartServer) and returns the access lines.
func accessLines(t *testing.T, claims *middleware.PlatformClaims, reqs ...*http.Request) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, m := range logLines(t, claims, reqs...) {
		if m["event.action"] == "http.access" {
			out = append(out, m)
		}
	}
	return out
}

// logLines is accessLines without the filter: every line logged.
func logLines(t *testing.T, claims *middleware.PlatformClaims, reqs ...*http.Request) []map[string]any {
	t.Helper()
	orig := middleware.VerifyTokenClaims
	t.Cleanup(func() { middleware.VerifyTokenClaims = orig })
	middleware.VerifyTokenClaims = func(context.Context, string) (*middleware.PlatformClaims, error) {
		return claims, nil
	}
	buf := logtest.Capture(t, func(w io.Writer) {
		logging.Setup(w, logging.Config{Level: slog.LevelInfo, JSON: true, Version: "test"})
	})

	db, _, err := dbmock.New()
	require.NoError(t, err)
	r := InitRouters(db)
	InitGraphQL(r)
	h := logging.AccessLog(r)
	for _, req := range reqs {
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	return logtest.Lines(t, buf)
}

func authed(req *http.Request) *http.Request {
	req.Header.Set("Authorization", middleware.BEARER_SCHEMA+"secret-token")
	req.Header.Set("tenant", "te100100")
	req.Header.Set("User-Agent", "secret-agent")
	return req
}

func TestAccessLine_ThroughRouter(t *testing.T) {
	claims := &middleware.PlatformClaims{
		Tenants:      []string{"TE100100"},
		Username:     "max.mustermann",
		Email:        "max.mustermann@example.org",
		AccessGroups: middleware.AccessGroups{"/EEG_ADMIN"},
		Authorized:   "at.ourproject.vfeeg.web",
	}
	gql := httptest.NewRequest("POST", "/query", strings.NewReader(`{"query":"query LoadEeg { __typename }"}`))
	gql.Header.Set("Content-Type", "application/json")

	ls := accessLines(t, claims,
		authed(httptest.NewRequest("GET", "/eeg/tariff/4711?secret=query-value", nil)),
		authed(gql),
		httptest.NewRequest("GET", "/eeg/tariff/4711", nil), // no token
		httptest.NewRequest("GET", "/healthz", nil),
	)
	require.Len(t, ls, 3, "one line per request, none for /healthz")

	rest := ls[0]
	assert.Equal(t, "GET", rest["http.request.method"])
	assert.Equal(t, "/eeg/tariff/{id}", rest["http.route"])
	assert.Equal(t, "TE100100", rest["tenant.id"])
	assert.Equal(t, "at.ourproject.vfeeg.web", rest["client.id"])
	assert.Equal(t, "eegfaktura-backend", rest["service.name"])

	q := ls[1]
	assert.Equal(t, "/query", q["http.route"])
	assert.EqualValues(t, 200, q["http.response.status_code"])
	assert.Equal(t, "LoadEeg", q["graphql.operation.name"])
	assert.Equal(t, "TE100100", q["tenant.id"])

	refused := ls[2]
	assert.EqualValues(t, 403, refused["http.response.status_code"])
	assert.Equal(t, "refused", refused["event.outcome"])
	assert.NotContains(t, refused, "tenant.id")

	// no user tracking: no name, e-mail, token, user agent or query value
	// (generated values are left out: a random request id can contain "4711")
	for _, l := range ls {
		delete(l, "request.id")
		delete(l, "event.duration")
		delete(l, "@timestamp")
	}
	raw, _ := json.Marshal(ls)
	for _, s := range []string{"max.mustermann", "example.org", "secret-token", "secret-agent", "query-value", "4711"} {
		assert.NotContains(t, string(raw), s)
	}
}

// A request the router answers itself (no route; the backend's subrouter also
// answers a wrong method with 404) gets an access line without http.route.
// 405 from a plain mux router: logging/access_test.go.
func TestAccessLine_UnmatchedThroughRouter(t *testing.T) {
	ls := accessLines(t, &middleware.PlatformClaims{},
		authed(httptest.NewRequest("GET", "/no/such/route/4711", nil)),
		authed(httptest.NewRequest("PATCH", "/eeg/tariff/4711", nil)),
	)
	require.Len(t, ls, 2)
	assert.Equal(t, "GET - 404", ls[0]["message"])
	assert.Equal(t, "PATCH - 404", ls[1]["message"])
	for _, l := range ls {
		assert.EqualValues(t, 404, l["http.response.status_code"])
		assert.NotContains(t, l, "http.route")
		assert.Equal(t, "failure", l["event.outcome"])
		assert.Regexp(t, `^[0-9a-f]{32}$`, l["request.id"])
	}
}

// An anonymous GraphQL operation leaves graphql.operation.name unset (same
// convention in energystore).
func TestAccessLine_AnonymousGraphQLOperation(t *testing.T) {
	gql := httptest.NewRequest("POST", "/query", strings.NewReader(`{"query":"{ __typename }"}`))
	gql.Header.Set("Content-Type", "application/json")
	ls := accessLines(t, &middleware.PlatformClaims{Tenants: []string{"TE100100"}, AccessGroups: middleware.AccessGroups{"/EEG_ADMIN"}}, authed(gql))
	require.Len(t, ls, 1)
	assert.EqualValues(t, 200, ls[0]["http.response.status_code"])
	assert.NotContains(t, ls[0], "graphql.operation.name")
}

// The gql-admin-check line carries the request's request.id and trace.id.
func TestGQLAdminCheckLine_HasRequestContext(t *testing.T) {
	gql := authed(httptest.NewRequest("POST", "/query", strings.NewReader(`{"query":"query LoadEeg { __typename }"}`)))
	gql.Header.Set("Content-Type", "application/json")
	gql.Header.Set("X-Request-Id", "req-admin-check")
	gql.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	ls := logLines(t, &middleware.PlatformClaims{Tenants: []string{"TE100100"}, Authorized: "at.ourproject.vfeeg.web"}, gql)

	var found bool
	for _, l := range ls {
		if l["message"] == "gql-admin-check: non-admin token" {
			found = true
			assert.Equal(t, "req-admin-check", l["request.id"])
			assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", l["trace.id"])
		}
	}
	assert.True(t, found, "gql-admin-check line logged")
}
