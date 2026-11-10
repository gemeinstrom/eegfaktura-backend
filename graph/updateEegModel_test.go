package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"at.ourproject/vfeeg-backend/api/middleware"
	"at.ourproject/vfeeg-backend/database"
	"at.ourproject/vfeeg-backend/database/dbmock"
	"at.ourproject/vfeeg-backend/graph/generated"
	"at.ourproject/vfeeg-backend/graph/gmodel"
	"at.ourproject/vfeeg-backend/logging/logcapture"
	"at.ourproject/vfeeg-backend/logging/logtest"
	"at.ourproject/vfeeg-backend/model"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var admin = middleware.AccessGroups{"/EEG_ADMIN"}

// updateEegModel calls the resolver behind GQLProtect, so the tenant comes from the checked header.
func updateEegModel(t *testing.T, groups middleware.AccessGroups, arg string, in *gmodel.EegModel) (*model.Eeg, error) {
	t.Helper()
	orig := middleware.VerifyTokenClaims
	t.Cleanup(func() { middleware.VerifyTokenClaims = orig })
	middleware.VerifyTokenClaims = func(context.Context, string) (*middleware.PlatformClaims, error) {
		return &middleware.PlatformClaims{Tenants: []string{"te000233"}, AccessGroups: groups}, nil
	}
	var eeg *model.Eeg
	var err error
	handler := middleware.GQLProtect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eeg, err = (&mutationResolver{&Resolver{}}).UpdateEegModel(r.Context(), arg, in)
	}))
	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	req.Header.Set("Authorization", middleware.BEARER_SCHEMA+"token")
	req.Header.Set("tenant", "TE000233")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return eeg, err
}

// 261105-mm2: updateEegModel stores sepaActiv through the guarded update of POST /eeg for the tenant of the request,
// drops settlementInterval and returns the EEG read back. The tenant arguments match the header case-insensitively.
func TestUpdateEegModelStores(t *testing.T) {
	db, mock, err := dbmock.New()
	require.NoError(t, err)
	database.SetDB(db)
	t.Cleanup(func() { database.SetDB(nil) })

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "base"."eeg" SET "sepa"=TRUE WHERE ("tenant" = 'TE000233')`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT .* FROM "base"."eeg" WHERE \("tenant" = \$1\)`).WithArgs("TE000233").
		WillReturnRows(sqlmock.NewRows([]string{"tenant", "settlementInterval", "sepa"}).AddRow("TE000233", "MONTHLY", true))

	interval, sepa, lower := "ANNUAL", true, "te000233"
	eeg, err := updateEegModel(t, admin, lower, &gmodel.EegModel{Tenant: &lower, SettlementInterval: &interval, SepaActiv: &sepa})
	require.NoError(t, err)
	assert.Equal(t, "TE000233", eeg.Id)
	assert.True(t, eeg.Sepa)
	assert.Equal(t, "MONTHLY", eeg.SettlementInterval)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Only settlementInterval (or nothing): no UPDATE, the stored EEG comes back.
func TestUpdateEegModelOnlySettlementInterval(t *testing.T) {
	db, mock, err := dbmock.New()
	require.NoError(t, err)
	database.SetDB(db)
	t.Cleanup(func() { database.SetDB(nil) })

	for _, in := range []*gmodel.EegModel{nil, {SettlementInterval: new(string)}} {
		mock.ExpectQuery(`SELECT .* FROM "base"."eeg"`).WithArgs("TE000233").
			WillReturnRows(sqlmock.NewRows([]string{"tenant", "settlementInterval"}).AddRow("TE000233", "MONTHLY"))
		eeg, err := updateEegModel(t, admin, "TE000233", in)
		require.NoError(t, err)
		assert.Equal(t, "MONTHLY", eeg.SettlementInterval)
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

// eegfaktura-dev#54 #6: a tenant argument (or EegModel.tenant) other than the header is an error, nothing is written.
func TestUpdateEegModelTenantArgument(t *testing.T) {
	db, mock, err := dbmock.New()
	require.NoError(t, err)
	database.SetDB(db)
	t.Cleanup(func() { database.SetDB(nil) })

	sepa, other, own := true, "TE000234", "TE000233"
	for _, c := range []struct {
		arg string
		in  *gmodel.EegModel
	}{
		{"TE000234", &gmodel.EegModel{SepaActiv: &sepa}},
		{"TE000234", nil},
		{"", &gmodel.EegModel{SepaActiv: &sepa}},
		{"TE000233", &gmodel.EegModel{Tenant: &other, SepaActiv: &sepa}},
		{"TE000234", &gmodel.EegModel{Tenant: &own, SepaActiv: &sepa}},
	} {
		eeg, err := updateEegModel(t, admin, c.arg, c.in)
		assert.EqualError(t, err, "updateEegModel: the tenant argument differs from the tenant header", c.arg)
		assert.Nil(t, eeg)
	}
	assert.NoError(t, mock.ExpectationsWereMet(), "no statement expected")
}

// eegfaktura-dev#54 #13: a refused updateEegModel is one warning with an event; settlementInterval is marked
// deprecated in the schema (it changes nothing).
func TestUpdateEegModelRefusalIsLogged(t *testing.T) {
	db, _, err := dbmock.New()
	require.NoError(t, err)
	database.SetDB(db)
	t.Cleanup(func() { database.SetDB(nil) })
	buf := logcapture.JSON(t)

	sepa := true
	_, err = updateEegModel(t, middleware.AccessGroups{"/EEG_USER"}, "TE000233", &gmodel.EegModel{SepaActiv: &sepa})
	require.Error(t, err)
	_, err = updateEegModel(t, admin, "TE000234", &gmodel.EegModel{SepaActiv: &sepa})
	require.Error(t, err)
	var reasons []any
	for _, l := range logtest.Lines(t, buf) {
		if l["event.action"] == "graphql.update_eeg_model.refused" {
			assert.Equal(t, "warn", l["log.level"])
			assert.Equal(t, "failure", l["event.outcome"])
			reasons = append(reasons, l["event.reason"])
		}
	}
	assert.Equal(t, []any{"updateEegModel requires the EEG admin group",
		"updateEegModel: the tenant argument differs from the tenant header"}, reasons, buf.String())

	field := generated.NewExecutableSchema(generated.Config{Resolvers: &Resolver{}}).Schema().
		Types["EegModel"].Fields.ForName("settlementInterval")
	require.NotNil(t, field)
	assert.NotNil(t, field.Directives.ForName("deprecated"))
	assert.Contains(t, field.Description, "Ignored")
}

// A member token (no EEG admin group) is refused like on POST /eeg, nothing is written; /query stays log-only for the
// other operations.
func TestUpdateEegModelRequiresAdmin(t *testing.T) {
	db, mock, err := dbmock.New()
	require.NoError(t, err)
	database.SetDB(db)
	t.Cleanup(func() { database.SetDB(nil) })

	sepa := true
	for _, groups := range []middleware.AccessGroups{{"/EEG_USER"}, nil} {
		eeg, err := updateEegModel(t, groups, "TE000233", &gmodel.EegModel{SepaActiv: &sepa})
		assert.Error(t, err)
		assert.Nil(t, eeg)
	}
	assert.NoError(t, mock.ExpectationsWereMet(), "no statement expected")
}
