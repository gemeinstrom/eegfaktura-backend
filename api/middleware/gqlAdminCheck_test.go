package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/graph/generated"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

const gqlAdminCheckMsg = "gql-admin-check: non-admin token"

// runGQL sends one GraphQL request through GQLProtect and a gqlgen server with
// the real schema and GQLAdminCheckLog. The documents used only select
// __typename, so no resolver (and no database) is involved.
func runGQL(t *testing.T, claims *PlatformClaims, body string) (int, []*logrus.Entry) {
	t.Helper()
	orig := VerifyTokenClaims
	t.Cleanup(func() { VerifyTokenClaims = orig })
	VerifyTokenClaims = func(ctx context.Context, token string) (*PlatformClaims, error) {
		return claims, nil
	}

	prevHooks := logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))
	t.Cleanup(func() { logrus.StandardLogger().ReplaceHooks(prevHooks) })
	hook := logtest.NewLocal(logrus.StandardLogger())

	srv := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{}))
	srv.AroundOperations(GQLAdminCheckLog)

	req := httptest.NewRequest("POST", "/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", BEARER_SCHEMA+"token")
	req.Header.Set("tenant", "te100100")
	rw := httptest.NewRecorder()

	GQLProtect(srv).ServeHTTP(rw, req)

	var hits []*logrus.Entry
	for _, e := range hook.AllEntries() {
		if e.Message == gqlAdminCheckMsg {
			hits = append(hits, e)
		}
	}
	return rw.Code, hits
}

const (
	gqlQuery    = `{"query":"query LoadEeg { __typename }","operationName":"LoadEeg"}`
	gqlMutation = `{"query":"mutation SaveEeg { __typename }"}`
)

func TestGQLAdminCheck_NonAdminQueryIsLoggedAndPasses(t *testing.T) {
	code, hits := runGQL(t, &PlatformClaims{
		Tenants:          []string{"TE100100"},
		Username:         "member@example.org",
		Email:            "member@example.org",
		AccessGroups:     AccessGroups{"/EEG_USER"},
		Authorized:       "some-client",
		RegisteredClaims: jwt.RegisteredClaims{Subject: "sub-123"},
	}, gqlQuery)

	require.Equal(t, http.StatusOK, code)
	require.Len(t, hits, 1)
	e := hits[0]
	require.Equal(t, logrus.InfoLevel, e.Level)
	require.Equal(t, logrus.Fields{
		"tenant":                 "TE100100",
		"client.id":              "some-client",
		"access_groups.class":    "user",
		"superuser":              false,
		"operation_type":         "query",
		"graphql.operation.name": "LoadEeg",
	}, e.Data)
	// no user tracking: no user id, e-mail or name
	require.NotContains(t, e.Data, "sub")
	for _, v := range e.Data {
		require.NotContains(t, fmt.Sprint(v), "@")
		require.NotContains(t, fmt.Sprint(v), "sub-123")
	}
}

func TestGQLAdminCheck_NonAdminMutationIsLogged(t *testing.T) {
	code, hits := runGQL(t, &PlatformClaims{
		Tenants:          []string{"TE100100"},
		Authorized:       "x",
		RegisteredClaims: jwt.RegisteredClaims{Subject: "sub-x"},
	}, gqlMutation)

	require.Equal(t, http.StatusOK, code)
	require.Len(t, hits, 1)
	require.Equal(t, "mutation", hits[0].Data["operation_type"])
	require.Equal(t, "SaveEeg", hits[0].Data["graphql.operation.name"])
	require.Equal(t, "none", hits[0].Data["access_groups.class"])
	require.NotContains(t, hits[0].Data, "sub")
}

func TestGQLAdminCheck_SuperuserWithoutAdminIsLogged(t *testing.T) {
	code, hits := runGQL(t, &PlatformClaims{
		RealmAccess: RealmRoles{Roles: []string{"superuser"}},
	}, gqlQuery)

	require.Equal(t, http.StatusOK, code)
	require.Len(t, hits, 1)
	require.Equal(t, true, hits[0].Data["superuser"])
}

func TestGQLAdminCheck_AdminIsNotLogged(t *testing.T) {
	for _, body := range []string{gqlQuery, gqlMutation} {
		code, hits := runGQL(t, &PlatformClaims{
			Tenants:      []string{"TE100100"},
			AccessGroups: AccessGroups{"/EEG_ADMIN"},
		}, body)
		require.Equal(t, http.StatusOK, code)
		require.Empty(t, hits)
	}
}

func TestGQLAdminCheck_WrongTenantStillRejected(t *testing.T) {
	code, hits := runGQL(t, &PlatformClaims{
		Tenants:      []string{"TE999999"},
		AccessGroups: AccessGroups{"/EEG_USER"},
	}, gqlQuery)
	require.Equal(t, http.StatusUnauthorized, code)
	require.Empty(t, hits)
}
