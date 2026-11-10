package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
)

// The unauthenticated GET /master/test route was removed; it must not answer anymore.
func TestApiRouter_MasterTestRouteRemoved(t *testing.T) {
	r := InitApiRouter(mux.NewRouter(), nil)

	rw := httptest.NewRecorder()
	r.ServeHTTP(rw, httptest.NewRequest("GET", "/master/test", nil))

	require.Equal(t, http.StatusNotFound, rw.Code)
}
