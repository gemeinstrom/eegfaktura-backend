package logging

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// accessFields is everything an access line may hold. A new field is a
// decision (no user tracking, 2026-10-29), not a side effect.
var accessFields = []string{
	"@timestamp", "log.level", "message", "service.name", "service.version",
	"event.action", "event.outcome", "event.duration",
	"http.request.method", "http.route", "http.response.status_code",
	"request.id", "trace.id", "span.id", "tenant.id", "client.id", "graphql.operation.name",
}

// router is set up as in server.go: MuxRoute on the router, AccessLog around it.
func router(h http.HandlerFunc) http.Handler {
	r := mux.NewRouter()
	s := r.PathPrefix("/").Subrouter()
	s.HandleFunc("/participant/{id}", h).Methods("GET")
	s.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {}).Methods("GET")
	s.Use(MuxRoute)
	return AccessLog(s)
}

func serve(r http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAccessLog_Fields(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true, Version: "1.2.3"})
	r := router(func(w http.ResponseWriter, r *http.Request) {
		SetClient(r.Context(), "at.ourproject.vfeeg.web")
		SetTenant(r.Context(), "TE100100")
		w.WriteHeader(http.StatusCreated)
	})

	req := httptest.NewRequest("GET", "/participant/5c0a2d6e-1111-2222-3333-444455556666", nil)
	req.Header.Set("X-Request-Id", "abc-123")
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	w := serve(r, req)

	assert.Equal(t, "abc-123", w.Header().Get("X-Request-Id"))
	ls := lines(t, buf)
	require.Len(t, ls, 1)
	l := ls[0]
	assert.Equal(t, "http.access", l["event.action"])
	assert.Equal(t, "success", l["event.outcome"])
	assert.Equal(t, "info", l["log.level"])
	assert.Equal(t, "GET", l["http.request.method"])
	assert.Equal(t, "/participant/{id}", l["http.route"])
	assert.EqualValues(t, 201, l["http.response.status_code"])
	assert.Contains(t, l, "event.duration")
	assert.Equal(t, "abc-123", l["request.id"])
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", l["trace.id"])
	assert.Equal(t, "00f067aa0ba902b7", l["span.id"])
	assert.Equal(t, "TE100100", l["tenant.id"])
	assert.Equal(t, "at.ourproject.vfeeg.web", l["client.id"])
	assert.Equal(t, "GET /participant/{id} 201", l["message"])
}

func TestAccessLog_KeepsValidRequestID(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true})
	id := "urn:x." + strings.Repeat("a", 122)
	req := httptest.NewRequest("GET", "/participant/1", nil)
	req.Header.Set("X-Request-Id", id)
	serve(router(func(w http.ResponseWriter, r *http.Request) {}), req)
	assert.Equal(t, id, lines(t, buf)[0]["request.id"])
}

func TestParseTraceparent(t *testing.T) {
	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	tr, sp := parseTraceparent(valid)
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", tr)
	assert.Equal(t, "00f067aa0ba902b7", sp)
	for _, in := range []string{
		"", "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",
	} {
		tr, sp := parseTraceparent(in)
		assert.Empty(t, tr+sp, in)
	}
}

func TestAccessLog_Outcome(t *testing.T) {
	for status, want := range map[int]string{200: "success", 302: "success", 400: "failure", 401: "refused", 403: "refused", 404: "failure", 500: "failure"} {
		buf := setup(t, Config{Level: slog.LevelInfo, JSON: true})
		serve(router(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }), httptest.NewRequest("GET", "/participant/1", nil))
		assert.Equal(t, want, lines(t, buf)[0]["event.outcome"], status)
	}
}

func TestAccessLog_RequestIDGeneratedWhenMissingOrInvalid(t *testing.T) {
	r := router(func(w http.ResponseWriter, r *http.Request) {})
	for _, in := range []string{"", "has space", strings.Repeat("a", 129), "a\"b"} {
		buf := setup(t, Config{Level: slog.LevelInfo, JSON: true})
		req := httptest.NewRequest("GET", "/participant/1", nil)
		req.Header.Set("X-Request-Id", in)
		req.Header.Set("traceparent", "00-00000000000000000000000000000000-00f067aa0ba902b7-01")
		w := serve(r, req)

		l := lines(t, buf)[0]
		assert.Regexp(t, `^[0-9a-f]{12}4[0-9a-f]{3}[89ab][0-9a-f]{15}$`, l["request.id"], in, "uuid4 as 32 hex")
		assert.Equal(t, l["request.id"], w.Header().Get("X-Request-Id"))
		assert.NotContains(t, l, "trace.id", "an all-zero trace id is invalid")
	}
}

func TestAccessLog_RefusalWithoutTenant(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true})
	r := router(func(w http.ResponseWriter, r *http.Request) {
		SetClient(r.Context(), "at.ourproject.vfeeg.web")
		w.WriteHeader(http.StatusForbidden)
	})
	serve(r, httptest.NewRequest("GET", "/participant/1", nil))

	l := lines(t, buf)[0]
	assert.Equal(t, "refused", l["event.outcome"])
	assert.Equal(t, "warn", l["log.level"])
	assert.NotContains(t, l, "tenant.id")
	assert.Equal(t, "at.ourproject.vfeeg.web", l["client.id"])
}

// Requests that match no route (404) or no method (405) get a line and an
// X-Request-Id too, without http.route and without the raw path.
func TestAccessLog_UnmatchedRoutes(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true})
	w404 := serve(router(nil), httptest.NewRequest("GET", "/nowhere/AT0030000000000000000000000012345", nil))
	// a plain mux router answers a wrong method with 405 (the backend's
	// subrouter answers 404, see server_test.go)
	plain := mux.NewRouter()
	plain.HandleFunc("/participant/{id}", func(w http.ResponseWriter, r *http.Request) {}).Methods("GET")
	plain.Use(MuxRoute)
	w405 := serve(AccessLog(plain), httptest.NewRequest("DELETE", "/participant/1", nil))

	assert.Equal(t, http.StatusNotFound, w404.Code)
	assert.Equal(t, http.StatusMethodNotAllowed, w405.Code)
	assert.NotEmpty(t, w404.Header().Get("X-Request-Id"))
	assert.NotEmpty(t, w405.Header().Get("X-Request-Id"))
	ls := lines(t, buf)
	require.Len(t, ls, 2)
	assert.Equal(t, "GET - 404", ls[0]["message"])
	assert.Equal(t, "DELETE - 405", ls[1]["message"])
	for _, l := range ls {
		assert.Equal(t, "http.access", l["event.action"])
		assert.Equal(t, "failure", l["event.outcome"])
		assert.NotContains(t, l, "http.route")
	}
	assert.NotContains(t, buf.String(), "nowhere")
	assert.NotContains(t, buf.String(), "AT0030000000000000000000000012345")
}

func TestAccessLog_ProbesAreSkipped(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true})
	serve(router(nil), httptest.NewRequest("GET", "/healthz", nil))
	assert.Empty(t, buf.String())
}

// Never log (concept point 3/6): no token, user agent, IP, raw path ids or
// query values in the access line, and no field outside accessFields.
func TestAccessLog_NeverLogsRequestData(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true})
	r := router(func(w http.ResponseWriter, r *http.Request) {
		SetTenant(r.Context(), "TE100100")
	})
	secrets := []string{
		"eyJhbGciOiJSUzI1NiJ9.secret-token", "Mozilla/5.0 secret-agent", "203.0.113.7", "198.51.100.9",
		"AT0030000000000000000000000012345", "AT611904300234573201", "max.mustermann@example.org",
	}
	req := httptest.NewRequest("GET", "/participant/AT0030000000000000000000000012345?iban=AT611904300234573201&mail=max.mustermann@example.org", nil)
	req.RemoteAddr = "203.0.113.7:4711"
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiJ9.secret-token")
	req.Header.Set("User-Agent", "Mozilla/5.0 secret-agent")
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	serve(r, req)

	out := buf.String()
	for _, s := range secrets {
		assert.NotContains(t, out, s)
	}
	l := lines(t, buf)[0]
	var extra []string
	for k := range l {
		if !slices.Contains(accessFields, k) {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	assert.Empty(t, extra, "fields outside the access-line allowlist")
}

// A handler panic: one error line with error.* (no remote address), an access
// line with 500, and the client gets 500.
func TestAccessLog_HandlerPanic(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true})
	req := httptest.NewRequest("GET", "/participant/1", nil)
	req.RemoteAddr = "203.0.113.7:4711"
	w := serve(router(func(w http.ResponseWriter, r *http.Request) { panic("boom") }), req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	ls := lines(t, buf)
	require.Len(t, ls, 2)
	assert.Equal(t, "error", ls[0]["log.level"])
	assert.Equal(t, "http.panic", ls[0]["event.action"])
	assert.Equal(t, "string", ls[0]["error.type"])
	assert.Equal(t, "boom", ls[0]["error.message"])
	assert.Contains(t, ls[0]["error.stack_trace"], "TestAccessLog_HandlerPanic")
	assert.Equal(t, ls[1]["request.id"], ls[0]["request.id"])
	assert.Equal(t, "http.access", ls[1]["event.action"])
	assert.EqualValues(t, 500, ls[1]["http.response.status_code"])
	assert.Equal(t, "failure", ls[1]["event.outcome"])
	assert.Equal(t, "GET /participant/{id} 500", ls[1]["message"])
	assert.NotContains(t, buf.String(), "203.0.113.7")
}

// After the response has begun, the connection is aborted with
// http.ErrAbortHandler (net/http logs nothing for it); the access line says 500.
func TestAccessLog_HandlerPanicAfterWrite(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true})
	h := router(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("late")
	})
	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/participant/1", nil))
	})
	ls := lines(t, buf)
	require.Len(t, ls, 2)
	assert.Equal(t, "late", ls[0]["error.message"])
	assert.EqualValues(t, 500, ls[1]["http.response.status_code"])
}

// headerRecorder notes every WriteHeader call that reaches the connection.
type headerRecorder struct {
	*httptest.ResponseRecorder
	codes []int
}

func (r *headerRecorder) WriteHeader(code int) {
	r.codes = append(r.codes, code)
	r.ResponseRecorder.WriteHeader(code)
}

// Q7: Flush sends the header, so a panic after it aborts the connection like
// one after a write, instead of a WriteHeader(500) on a started response.
func TestAccessLog_HandlerPanicAfterFlush(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true})
	h := router(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		panic("late")
	})
	rec := &headerRecorder{ResponseRecorder: httptest.NewRecorder()}
	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/participant/1", nil))
	})
	assert.True(t, rec.Flushed)
	assert.Empty(t, rec.codes, "no WriteHeader after the flush")
	ls := lines(t, buf)
	require.Len(t, ls, 2)
	assert.Equal(t, "late", ls[0]["error.message"])
	assert.EqualValues(t, 500, ls[1]["http.response.status_code"])
}

// L12: every line of a request carries request.id and, once checked, tenant.id.
func TestRequestLines_CarryRequestAndTenant(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true, Version: "1.2.3"})
	r := router(func(w http.ResponseWriter, r *http.Request) {
		logrus.WithContext(r.Context()).Warn("before the check")
		SetTenant(r.Context(), "TE100100")
		logrus.WithContext(r.Context()).WithError(errors.New("boom")).Error("logrus")
		slog.WarnContext(r.Context(), "slog")
	})

	req := httptest.NewRequest("GET", "/participant/1", nil)
	req.Header.Set(headerRequestID, "req-1")
	req.Header.Set(headerTraceparent, "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	serve(r, req)

	got := lines(t, buf)
	require.Len(t, got, 4)
	assert.NotContains(t, got[0], "tenant.id")
	for _, l := range got {
		assert.Equal(t, "req-1", l["request.id"], l["message"])
		assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", l["trace.id"], l["message"])
	}
	for _, l := range got[1:] {
		assert.Equal(t, "TE100100", l["tenant.id"], l["message"])
	}
	// one key only: a duplicate would make the JSON line ambiguous
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n")[1:] {
		assert.Equal(t, 1, strings.Count(l, `"tenant.id"`), l)
	}
}

// In a request a tenant field of the record does not count, only what passed
// the check. Outside a request (MQTT, jobs) the field stays.
func TestTenantField_InARequestOnlyWhatPassedTheCheck(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true, Version: "1.2.3"})
	r := router(func(w http.ResponseWriter, r *http.Request) {
		logrus.WithContext(r.Context()).WithField("tenant", "UNCHECKED").Warn("before the check")
		SetTenant(r.Context(), "TE100100")
		logrus.WithContext(r.Context()).WithField("tenant", "OTHER").Warn("after the check")
	})
	serve(r, httptest.NewRequest("GET", "/participant/1", nil))
	logrus.WithField("tenant", "TE200200").Info("mqtt handler")

	got := lines(t, buf)
	require.Len(t, got, 4)
	assert.NotContains(t, got[0], "tenant.id")
	assert.Equal(t, "TE100100", got[1]["tenant.id"])
	assert.Equal(t, "TE200200", got[3]["tenant.id"])
}

// Q4: request.id, trace.id, span.id and tenant.id come from the context handler
// only, one key each; the same keys from a record or from With are dropped.
func TestCorrelationFields_OnlyFromTheContext(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true, Version: "1.2.3"})
	forged := []any{"request.id", "forged", "trace.id", "forged", "span.id", "forged", "tenant.id", "FORGED"}
	r := router(func(w http.ResponseWriter, r *http.Request) {
		SetTenant(r.Context(), "TE100100")
		slog.Default().With(forged...).InfoContext(r.Context(), "with")
		slog.InfoContext(r.Context(), "record", forged...)
	})
	req := httptest.NewRequest("GET", "/participant/1", nil)
	req.Header.Set(headerRequestID, "req-1")
	req.Header.Set(headerTraceparent, "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	serve(r, req)
	slog.Info("outside", forged[:6]...)
	slog.Default().With("tenant.id", "te200200").Info("outside with")

	raw := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, raw, 5)
	assert.NotContains(t, buf.String(), "forged")
	assert.NotContains(t, buf.String(), "FORGED")
	for _, l := range raw[:2] {
		for _, k := range []string{"request.id", "trace.id", "span.id", "tenant.id"} {
			assert.Equal(t, 1, strings.Count(l, `"`+k+`"`), l)
		}
	}
	got := lines(t, buf)
	assert.Equal(t, "req-1", got[0]["request.id"])
	assert.Equal(t, "TE100100", got[1]["tenant.id"])
	assert.Equal(t, "b7ad6b7169203331", got[1]["span.id"])
	for _, k := range []string{"request.id", "trace.id", "span.id", "tenant.id"} {
		assert.NotContains(t, got[3], k)
	}
	assert.Equal(t, "TE200200", got[4]["tenant.id"])
}

func TestSetTenant_OnlyIdShapedValues(t *testing.T) {
	for tenant, kept := range map[string]bool{
		"TE100100": true, "GC123456-001": true, "te100100": true, "RC_100": true, strings.Repeat("A", 64): true,
		"": false, "a b": false, "a\nb": false, "a.b": false, "-TE100100": false, "_TE100100": false,
		`x","y":"z`: false, strings.Repeat("A", 65): false,
	} {
		ai := &accessInfo{}
		SetTenant(context.WithValue(context.Background(), accessCtxKey{}, ai), tenant)
		assert.Equal(t, kept, ai.tenant != "", tenant)
		if kept {
			assert.Equal(t, strings.ToUpper(tenant), ai.tenant)
		}
	}
}

// Outside a request, on a path without a context (SendMail), the value of the
// tenant field can be the header as sent: what fails the rule gets no tenant.id.
func TestTenantField_OutsideARequestFollowsTheRule(t *testing.T) {
	buf := setup(t, Config{Level: slog.LevelInfo, JSON: true, Version: "1.2.3"})
	logrus.WithField("tenant", "GC123456-001").Info("fine")
	logrus.WithField("tenant", "Erika Mustermann").Info("not an id")

	got := lines(t, buf)
	require.Len(t, got, 2)
	assert.Equal(t, "GC123456-001", got[0]["tenant.id"])
	assert.NotContains(t, got[1], "tenant.id")
}
