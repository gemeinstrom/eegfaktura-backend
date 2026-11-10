package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/database/dbmock"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
)

// probeRouter registers the probes the way InitRouters (server.go) does: on the
// same "/" subrouter as the protected routers (here only the EEG router), but
// with a fake ping. server_health_test.go covers the real InitRouters.
func probeRouter(t *testing.T, ping func(context.Context) error) *mux.Router {
	db, _, err := dbmock.New()
	require.NoError(t, err)
	s := mux.NewRouter().PathPrefix("/").Subrouter()
	s = InitEegRouter(s, db)
	return InitHealthRouter(s, ping)
}

func serve(r *mux.Router, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestHealthz_unauthenticatedOkWithoutDatabase(t *testing.T) {
	var pings atomic.Int32
	r := probeRouter(t, func(context.Context) error { pings.Add(1); return errors.New("down") })

	w := serve(r, "/healthz")

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ok", w.Body.String())
	require.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	require.Zero(t, pings.Load()) // liveness never touches the database
}

func TestReadyz_databaseAnswers(t *testing.T) {
	w := serve(probeRouter(t, func(context.Context) error { return nil }), "/readyz")

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ok", w.Body.String())
}

func TestReadyz_databaseDown(t *testing.T) {
	r := probeRouter(t, func(context.Context) error { return errors.New("connection refused host=db.internal") })

	w := serve(r, "/readyz")

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Equal(t, "unavailable", w.Body.String()) // no error text in the body
}

func TestReadyz_concurrentCallsShareOnePing(t *testing.T) {
	var pings atomic.Int32
	r := probeRouter(t, func(context.Context) error {
		pings.Add(1)
		time.Sleep(50 * time.Millisecond)
		return nil
	})

	codes := make(chan int, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- serve(r, "/readyz").Code
		}()
	}
	wg.Wait()
	close(codes)

	for c := range codes {
		require.Equal(t, http.StatusOK, c)
	}
	require.Equal(t, int32(1), pings.Load())
}

func TestReadyz_pingsAgainAfterReuseWindow(t *testing.T) {
	var pings atomic.Int32
	r := probeRouter(t, func(context.Context) error { pings.Add(1); return nil })

	serve(r, "/readyz")
	time.Sleep(readyzReuse + 50*time.Millisecond)
	serve(r, "/readyz")

	require.Equal(t, int32(2), pings.Load())
}

func TestReadyz_tooManyConnectionsCountsAsReady(t *testing.T) {
	r := probeRouter(t, func(context.Context) error { return database.ErrTooManyConnections })

	w := serve(r, "/readyz")

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "ok", w.Body.String())
}

func TestProbes_otherPathsStillNeedToken(t *testing.T) {
	w := serve(probeRouter(t, func(context.Context) error { return nil }), "/eeg")

	require.Equal(t, http.StatusForbidden, w.Code) // missing token answers 403 here
}
