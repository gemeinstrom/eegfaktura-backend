package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProtectMiddleware_ValidToken_AllowsRequest(t *testing.T) {
	orig := VerifyTokenClaims
	defer func() { VerifyTokenClaims = orig }()

	VerifyTokenClaims = func(ctx context.Context, token string) (*PlatformClaims, error) {
		return &PlatformClaims{
			Tenants:      []string{"TE100100"},
			AccessGroups: AccessGroups{"/EEG_ADMIN"},
			RealmAccess: struct {
				Roles []string `json:"roles"`
			}(struct{ Roles []string }{Roles: []string{}}),
		}, nil
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", BEARER_SCHEMA+"token")
	req.Header.Set("tenant", "TE100100")

	rw := httptest.NewRecorder()
	h := Protect(func(w http.ResponseWriter, r *http.Request, claims *PlatformClaims, tenant string) {
		w.WriteHeader(http.StatusOK)
	})

	h(rw, req)
	require.Equal(t, http.StatusOK, rw.Code)
}

func TestProtectMiddleware_InvalidToken_ReturnsUnauthorized(t *testing.T) {
	orig := VerifyTokenClaims
	defer func() { VerifyTokenClaims = orig }()

	VerifyTokenClaims = func(ctx context.Context, token string) (*PlatformClaims, error) {
		return nil, http.ErrNoCookie // arbitrary error to simulate verification failure
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", BEARER_SCHEMA+"token")
	req.Header.Set("tenant", "TE100100")

	rw := httptest.NewRecorder()
	h := Protect(func(w http.ResponseWriter, r *http.Request, claims *PlatformClaims, tenant string) {
		w.WriteHeader(http.StatusOK)
	})

	h(rw, req)
	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestConditionProtect_RoutesToAdminOrUser(t *testing.T) {
	orig := VerifyTokenClaims
	defer func() { VerifyTokenClaims = orig }()

	// Admin case
	VerifyTokenClaims = func(ctx context.Context, token string) (*PlatformClaims, error) {
		return &PlatformClaims{
			Tenants:      []string{"TEA"},
			AccessGroups: AccessGroups{"/EEG_ADMIN"},
			RealmAccess: struct {
				Roles []string `json:"roles"`
			}(struct{ Roles []string }{Roles: []string{}}),
		}, nil
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", BEARER_SCHEMA+"token")
	req.Header.Set("tenant", "TEA")
	rw := httptest.NewRecorder()

	adminCalled := false
	userCalled := false
	h := ConditionProtect(func(w http.ResponseWriter, r *http.Request, claims *PlatformClaims, tenant string) {
		adminCalled = true
		w.WriteHeader(http.StatusOK)
	}, func(w http.ResponseWriter, r *http.Request, claims *PlatformClaims, tenant string) {
		userCalled = true
		w.WriteHeader(http.StatusOK)
	})

	h(rw, req)
	require.True(t, adminCalled)
	require.False(t, userCalled)

	// User case
	VerifyTokenClaims = func(ctx context.Context, token string) (*PlatformClaims, error) {
		return &PlatformClaims{
			Tenants:      []string{"TEA"},
			AccessGroups: AccessGroups{"/EEG_USER"},
			RealmAccess: struct {
				Roles []string `json:"roles"`
			}(struct{ Roles []string }{Roles: []string{}}),
		}, nil
	}
	adminCalled = false
	rw2 := httptest.NewRecorder()
	h(rw2, req)
	require.True(t, userCalled)
	require.False(t, adminCalled)
}

func TestGQLProtect_InvalidToken_DoesNotLeakVerifierError(t *testing.T) {
	orig := VerifyTokenClaims
	defer func() { VerifyTokenClaims = orig }()

	const verifierErr = "oidc: id token signed with unsupported algorithm, expected [\"RS256\"] got \"HS256\""
	VerifyTokenClaims = func(ctx context.Context, token string) (*PlatformClaims, error) {
		return nil, errors.New(verifierErr)
	}

	req := httptest.NewRequest("POST", "/query", nil)
	req.Header.Set("Authorization", BEARER_SCHEMA+"token")
	req.Header.Set("tenant", "TE100100")

	rw := httptest.NewRecorder()
	GQLProtect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rw, req)

	require.Equal(t, http.StatusUnauthorized, rw.Code)
	require.Empty(t, rw.Body.String())
}
