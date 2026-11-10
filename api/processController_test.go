package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"at.ourproject/vfeeg-backend/database/dbmock"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestParseHistoryPageSize(t *testing.T) {
	valid := map[string]uint{"0": 0, "1": 1, "50": 50, "1000": 1000, "1001": 1001, "+5": 5}
	for in, want := range valid {
		got, err := parseHistoryPageSize(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "abc", "-1", "-9223372036854775808", "-99999999999999999999", "99999999999999999999", "1.5"} {
		_, err := parseHistoryPageSize(in)
		require.Error(t, err, in)
	}
}

func TestFetchProcessHistory_pageSize(t *testing.T) {
	tests := []struct {
		ps     string
		status int
	}{
		{ps: "-1", status: http.StatusBadRequest},
		{ps: "abc", status: http.StatusBadRequest},
		{ps: "0", status: http.StatusOK},
		{ps: "1000", status: http.StatusOK},
		{ps: "1001", status: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.ps, func(t *testing.T) {
			db, mock, err := dbmock.New()
			require.NoError(t, err)
			if tt.status == http.StatusOK {
				mock.ExpectQuery(`SELECT (.+) FROM "base"."processhistory"`).
					WillReturnRows(sqlmock.NewRows([]string{"conversationId"}))
			}

			r := httptest.NewRequest(http.MethodGet, "/process/history?start=0&end=1000&protocol=CR_MSG&ps="+tt.ps, nil)
			w := httptest.NewRecorder()
			NewProcessHandler(db).fetchProcessHistory()(w, r, nil, "TE000001")

			require.Equal(t, tt.status, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
