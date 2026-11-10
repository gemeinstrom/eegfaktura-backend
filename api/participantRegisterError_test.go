package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database/dbmock"
	"at.ourproject/vfeeg-backend/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failed registration answers with the code and a generic message, without the database text
// (constraint and table names) — as the member update does.
func TestRegisterParticipant_failureHidesDatabaseText(t *testing.T) {
	db, mock, err := dbmock.New()
	require.NoError(t, err)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT (.+)").
		WillReturnError(errors.New(`pq: duplicate key value violates unique constraint "participant_pkey"`))
	mock.ExpectRollback()

	req := httptest.NewRequest(http.MethodPost, "/participant",
		strings.NewReader(`{"firstname":"Anna","lastname":"Berger","businessRole":"EEG_PRIVATE","role":"EEG_USER"}`))
	w := httptest.NewRecorder()
	NewParticipantHandler(db).registerParticipant()(w, req, &middleware.PlatformClaims{Username: "annab"}, "TE000001")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":1104`)
	assert.Contains(t, w.Body.String(), "participant could not be registered")
	assert.NotContains(t, w.Body.String(), "pq:")
	assert.NotContains(t, w.Body.String(), "participant_pkey")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// An invalid e-mail answers code 1110 with the reason, as on the member update.
func TestRegisterParticipant_invalidEmailKeepsTheReason(t *testing.T) {
	db, mock, err := dbmock.New()
	require.NoError(t, err)
	mock.ExpectBegin()
	mock.ExpectRollback()

	req := httptest.NewRequest(http.MethodPost, "/participant",
		strings.NewReader(`{"firstname":"Anna","lastname":"Berger","contact":{"email":"foo@"}}`))
	w := httptest.NewRecorder()
	NewParticipantHandler(db).registerParticipant()(w, req, &middleware.PlatformClaims{Username: "annab"}, "TE000001")

	_, verr := model.ValidateEmailList("foo@")
	require.Error(t, verr)
	var body struct {
		Error HttpError `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, model.ErrInvalidParticipantEmail(nil).Code, body.Error.Code)
	assert.Equal(t, model.UserText(verr), body.Error.Message)
	assert.NoError(t, mock.ExpectationsWereMet())
}
