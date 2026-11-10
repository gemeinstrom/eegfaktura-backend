package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/model"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TE001006 has one member (Peter, 04583ce3-…) with the ACTIVE metering point AT…953013 (seed.sql). Each test uses its
// own last name and deletes its members (bank account, contact, addresses and metering points cascade).
const (
	umTenant      = "TE001006"
	umActiveMeter = "AT0030000000000000000000000953013"
)

func umMember(lastname, accountInfo string) string {
	return `{"firstname":"Uma","lastname":"` + lastname + `","businessRole":"EEG_PRIVATE","role":"EEG_USER",` +
		`"accountInfo":` + accountInfo
}

const umNoDebit = `{"owner":"Uma","sepaDirectDebit":"NONE"}`

func registerForTest(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/participant", strings.NewReader(body))
	w := httptest.NewRecorder()
	NewParticipantHandler(db).registerParticipant()(w, req, &middleware.PlatformClaims{Username: "test"}, umTenant)
	return w
}

func errorBody(t *testing.T, w *httptest.ResponseRecorder) HttpError {
	t.Helper()
	var body struct {
		Error HttpError `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), w.Body.String())
	return body.Error
}

func deleteMembersNamed(t *testing.T, lastname string) {
	t.Helper()
	t.Cleanup(func() {
		_, err := testDB.DbInstance.Exec(`DELETE FROM base.participant WHERE tenant = $1 AND lastname = $2`, umTenant, lastname)
		assert.NoError(t, err)
	})
}

func memberId(t *testing.T, lastname string) string {
	t.Helper()
	var id string
	require.NoError(t, testDB.DbInstance.Get(&id,
		`SELECT id FROM base.participant WHERE tenant = $1 AND lastname = $2`, umTenant, lastname))
	return id
}

func storedBank(t *testing.T, lastname string) (iban, bankName *string) {
	t.Helper()
	require.NoError(t, testDB.DbInstance.QueryRow(`SELECT b.iban, b."bankName" FROM base.bankaccount b
		JOIN base.participant p ON p.id = b.participant_id WHERE p.tenant = $1 AND p.lastname = $2`, umTenant, lastname).
		Scan(&iban, &bankName))
	return iban, bankName
}

// A4: a member without direct debit and without IBAN ("Kein SEPA", platform#143) is stored with an empty IBAN, not
// NULL: production keeps NOT NULL on base.bankaccount.iban. The bank name is stored without spaces (platform#249).
func TestRegisterParticipant_withoutIbanStoresEmptyIban(t *testing.T) {
	deleteMembersNamed(t, "MeldungA4")
	w := registerForTest(t, umMember("MeldungA4", `{"owner":"Uma","sepaDirectDebit":"NONE","bankName":" "}`)+`}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	iban, bankName := storedBank(t, "MeldungA4")
	require.NotNil(t, iban, "IBAN is NULL")
	assert.Equal(t, "", *iban)
	require.NotNil(t, bankName)
	assert.Equal(t, "", *bankName, "the space of the former required field is removed")
}

// A4 (review): direct debit without IBAN is refused with code 1112 instead of being stored with an empty IBAN; the
// debit type is read like web reads it (trimmed, any case).
func TestRegisterParticipant_debitWithoutIban(t *testing.T) {
	deleteMembersNamed(t, "MeldungDebit")
	w := registerForTest(t, umMember("MeldungDebit", `{"owner":"Uma","sepaDirectDebit":" core"}`)+`}`)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	e := errorBody(t, w)
	assert.Equal(t, 1112, e.Code)
	assert.Equal(t, model.DebitWithoutIbanText, e.Message)
}

// A4 (review): a partial update of the bank data of a member without bank account row inserts the row; its IBAN is
// empty, not NULL.
func TestUpdateParticipantPartial_insertsBankAccountWithEmptyIban(t *testing.T) {
	deleteMembersNamed(t, "MeldungPartial")
	require.Equal(t, http.StatusCreated, registerForTest(t, umMember("MeldungPartial", umNoDebit)+`}`).Code)
	id := memberId(t, "MeldungPartial")
	_, err := testDB.DbInstance.Exec(`DELETE FROM base.bankaccount WHERE participant_id = $1`, id)
	require.NoError(t, err)

	db, err := database.GetDB(context.Background())
	require.NoError(t, err)
	req, _ := http.NewRequest("PUT", "/participant/v2/"+id, strings.NewReader(`{"path":"accountInfo.owner","value":"Uma M"}`))
	req = mux.SetURLVars(req, map[string]string{"id": id})
	w := httptest.NewRecorder()
	NewParticipantHandler(db).updateParticipantPartial()(w, req, &middleware.PlatformClaims{Username: "test"}, umTenant)

	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	iban, _ := storedBank(t, "MeldungPartial")
	require.NotNil(t, iban, "IBAN is NULL")
	assert.Equal(t, "", *iban)
}

// A2: registering a member with a metering point that is assigned to another member answered the generic 1104. Now
// code 1207 with a German text naming the metering point; the member is not stored.
func TestRegisterParticipant_meterActiveAtOtherMember(t *testing.T) {
	deleteMembersNamed(t, "MeldungA2")
	w := registerForTest(t, umMember("MeldungA2", umNoDebit)+`,"meters":[{"meteringPoint":"`+umActiveMeter+
		`","direction":"CONSUMPTION","status":"NEW","processState":"NEW"}]}`)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	e := errorBody(t, w)
	assert.Equal(t, 1207, e.Code)
	assert.Equal(t, fmt.Sprintf(model.MeterActiveAtOtherParticipantText, umActiveMeter), e.Message)

	var n int
	require.NoError(t, testDB.DbInstance.Get(&n,
		`SELECT count(*) FROM base.participant WHERE tenant = $1 AND lastname = 'MeldungA2'`, umTenant))
	assert.Equal(t, 0, n, "the member is not stored")
}

// A3: adding the metering point with "+" at another member answered 400 / 1111 with the database text. Now 409 / 1207.
func TestCreateMeteringPoint_activeAtOtherMemberIs409(t *testing.T) {
	deleteMembersNamed(t, "MeldungA3")
	require.Equal(t, http.StatusCreated, registerForTest(t, umMember("MeldungA3", umNoDebit)+`}`).Code)
	participant := memberId(t, "MeldungA3")
	calls := countRegistrations(t)

	body := `{"meteringPoint":"` + umActiveMeter + `","direction":"CONSUMPTION","status":"INIT","processState":"NEW"}`
	req, _ := http.NewRequest("PUT", "/meteringpoint/"+participant+"/create", strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"pid": participant})
	w := httptest.NewRecorder()
	newTestMeteringHandler(t).createMeteringPoint()(w, req, &middleware.PlatformClaims{Username: "test"}, umTenant)

	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	e := errorBody(t, w)
	assert.Equal(t, 1207, e.Code)
	assert.Equal(t, fmt.Sprintf(model.MeterActiveAtOtherParticipantText, umActiveMeter), e.Message)
	assert.Equal(t, 0, *calls, "nothing is sent to the grid operator")
}
