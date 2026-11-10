package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"github.com/gorilla/mux"
	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 261005-ca1: PUT /participant/{id} updates the member named in the path. A body that
// carries another id is refused before any write.
func TestUpdateParticipantPathIdMustMatchBody(t *testing.T) {
	const (
		tenant  = "TE000006"
		pathId  = "ea1142dc-03da-11ee-b82b-5a985b4b0306"
		otherId = "ea1142dc-03da-11ee-b82b-5a985b4b0316" // same community
	)
	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	h := NewParticipantHandler(db)
	claims := &middleware.PlatformClaims{Username: "test"}

	firstNames := func() []string {
		names := []string{}
		for _, id := range []string{pathId, otherId} {
			p, err := db.GetParticipant(context.Background(), tenant, id)
			require.NoError(t, err)
			names = append(names, p.FirstName)
		}
		return names
	}
	before := firstNames()

	for _, tc := range []struct{ name, path, body string }{
		{"other id in body", pathId, `{"id":"` + otherId + `","firstname":"CHANGED"}`},
		{"no id in body", pathId, `{"firstname":"CHANGED"}`},
		{"no valid id in path", "x", `{"id":"` + pathId + `","firstname":"CHANGED"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("PUT", "/participant/"+tc.path, strings.NewReader(tc.body))
			req = mux.SetURLVars(req, map[string]string{"id": tc.path})
			w := httptest.NewRecorder()

			h.updateParticipant()(w, req, claims, tenant)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, before, firstNames(), "no member may be changed")
		})
	}
}

// 261005-ca1/ca4: full and partial update of a member id that does not exist, or that
// belongs to another community, answer the same JSON 400 (code 1106) without database
// text, so a caller cannot tell the two apart.
func TestUpdateParticipantUnknownAndForeignIdAnswerAlike(t *testing.T) {
	const (
		tenant    = "TE000006"
		foreignId = "ea9942da-03da-11ee-b82b-5a985b4b033a" // TE000001
	)
	unknownId := uuid.NewRandom().String()
	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	h := NewParticipantHandler(db)
	claims := &middleware.PlatformClaims{Username: "test"}

	full := func(id string) *httptest.ResponseRecorder {
		req, _ := http.NewRequest("PUT", "/participant/"+id, strings.NewReader(`{"id":"`+id+`","firstname":"CHANGED"}`))
		req = mux.SetURLVars(req, map[string]string{"id": id})
		w := httptest.NewRecorder()
		h.updateParticipant()(w, req, claims, tenant)
		return w
	}
	partial := func(id string) *httptest.ResponseRecorder {
		req, _ := http.NewRequest("PUT", "/participant/v2/"+id, strings.NewReader(`{"path":"contact.phone","value":"0000"}`))
		req = mux.SetURLVars(req, map[string]string{"id": id})
		w := httptest.NewRecorder()
		h.updateParticipantPartial()(w, req, claims, tenant)
		return w
	}

	for name, call := range map[string]func(string) *httptest.ResponseRecorder{"full": full, "partial": partial} {
		t.Run(name, func(t *testing.T) {
			unknown, foreign := call(unknownId), call(foreignId)
			for _, w := range []*httptest.ResponseRecorder{unknown, foreign} {
				assert.Equal(t, http.StatusBadRequest, w.Code)
				var body struct {
					Error struct {
						Code    int    `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
				assert.Equal(t, 1106, body.Error.Code)
				assert.NotContains(t, strings.ToLower(w.Body.String()), "sql")
			}
			assert.Equal(t, unknown.Body.String(), foreign.Body.String())
		})
	}
}

// A partial update with an invalid e-mail answers code 1110 with the reason, as the full
// update does, instead of the generic 1102.
func TestUpdateParticipantPartialInvalidEmailNamesTheReason(t *testing.T) {
	const (
		tenant = "TE000006"
		id     = "ea1142dc-03da-11ee-b82b-5a985b4b0306"
	)
	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	h := NewParticipantHandler(db)

	req, _ := http.NewRequest("PUT", "/participant/v2/"+id, strings.NewReader(`{"path":"contact.email","value":"no-address"}`))
	req = mux.SetURLVars(req, map[string]string{"id": id})
	w := httptest.NewRecorder()
	h.updateParticipantPartial()(w, req, &middleware.PlatformClaims{Username: "test"}, tenant)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var body struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
	assert.Equal(t, 1110, body.Error.Code)
	assert.Equal(t, "Die E-Mail-Adresse „no-address“ ist ungültig.", body.Error.Message, "the person correcting it sees the value")
}
