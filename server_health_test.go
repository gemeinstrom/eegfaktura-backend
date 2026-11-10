package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"at.ourproject/vfeeg-backend/config/configtest"
	"at.ourproject/vfeeg-backend/database/dbmock"
	"github.com/stretchr/testify/require"
)

// The probes as wired by InitRouters, with the real database.Ping.
func TestInitRouters_probes(t *testing.T) {
	db, _, err := dbmock.New()
	require.NoError(t, err)
	// Nothing listens on port 1: the dedicated ping fails at once.
	configtest.Set(t, "database.host", "127.0.0.1")
	configtest.Set(t, "database.port", 1)
	r := InitRouters(db)

	tests := []struct {
		path   string
		status int
		body   string
	}{
		{path: "/healthz", status: http.StatusOK, body: "ok"},
		{path: "/readyz", status: http.StatusServiceUnavailable, body: "unavailable"},
		{path: "/eeg", status: http.StatusForbidden}, // still needs a token
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))
			require.Equal(t, tt.status, w.Code)
			if tt.body != "" {
				require.Equal(t, tt.body, w.Body.String())
			}
		})
	}
}
