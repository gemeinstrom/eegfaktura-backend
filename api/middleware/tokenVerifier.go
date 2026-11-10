package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"at.ourproject/vfeeg-backend/logging"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
)

var (
	// VerifyTokenClaims pluggable verification function used by middlewares and tests.
	// InitKeycloak should set this to the real verifier at startup.
	// Tests override it to avoid network calls.
	VerifyTokenClaims func(ctx context.Context, token string) (*PlatformClaims, error)
)

type PlatformClaims struct {
	Tenants      []string     `json:"tenant"`
	Username     string       `json:"preferred_username"`
	Email        string       `json:"email"`
	AccessGroups AccessGroups `json:"access_groups"`
	Authorized   string       `json:"azp"`
	RealmAccess  RealmRoles   `json:"realm_access"`
	jwt.RegisteredClaims
}

type RealmRoles struct {
	Roles []string `json:"roles"`
}

type AccessGroups []string

type VerifyError struct {
	StatusCode int
	Err        error
}

func (eve *VerifyError) Error() string {
	return fmt.Sprintf("status %d: err %v", eve.StatusCode, eve.Err)
}

func (ag AccessGroups) IsAdmin() bool {
	for _, s := range ag {
		if s == "/EEG_ADMIN" {
			return true
		}
	}
	return false
}

func (ag AccessGroups) IsUser() bool {
	for _, s := range ag {
		if s == "/EEG_USER" {
			return true
		}
	}
	return false
}

// verifyAndExtractClaims tries the test-hook VerifyTokenClaims (if present) and
// falls back to the actual OIDC verifier otherwise. It always returns a populated
// PlatformClaims or an error.
func verifyAndExtractClaims(ctx context.Context, token string) (*PlatformClaims, error) {
	// If a test hook / custom verifier is provided, use it.
	if VerifyTokenClaims != nil {
		return VerifyTokenClaims(ctx, token)
	}

	// Fallback to OIDC verifier
	if verifier == nil {
		return nil, fmt.Errorf("oidc verifier not initialized")
	}
	idToken, err := verifier.Verify(ctx, token)
	if err != nil {
		return nil, err
	}
	claims := &PlatformClaims{}
	if err := idToken.Claims(claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func GQLProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jwtToken, err := ParseBearerTokenFromHeader(r)
		if err != nil {
			logrus.WithContext(r.Context()).Printf("No Access_token in request or invalid Authorization: %v\n", err)
			w.WriteHeader(http.StatusForbidden)
			return
		}

		claims, err := verifyAndExtractClaims(context.Background(), jwtToken)
		if err != nil {
			logrus.WithContext(r.Context()).WithError(err).Error("token verification failed")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		logging.SetClient(r.Context(), claims.Authorized)

		//fmt.Printf("Claims: %+v\n", claims)
		tenant := r.Header.Get("tenant")
		if len(tenant) == 0 {
			tenant = r.Header.Get("X-Tenant")
		}
		superuser := hasRole(claims.RealmAccess.Roles, "superuser")
		if !superuser {
			if contains(claims.Tenants, tenant) == false {
				logrus.WithContext(r.Context()).Warn("tenant check failed")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}

		// put it in context
		ctx := context.WithValue(r.Context(), tenantCtxKey, strings.ToUpper(tenant))

		// F1, step 1 (log only): REST write endpoints require the EEG_ADMIN group,
		// /query does not check it yet. Tokens without it are only marked here and
		// logged per operation by GQLAdminCheckLog; the request passes as before.
		// Enforcing the role follows after the log period.
		if !claims.AccessGroups.IsAdmin() {
			ctx = withGQLNonAdmin(ctx, claims, superuser)
		}

		logging.SetTenant(ctx, tenant)
		// and call the next with our new context
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

func retrieveClaims(r *http.Request) (string, *PlatformClaims, error) {
	jwtToken, err := ParseBearerTokenFromHeader(r)
	if err != nil {
		logrus.WithContext(r.Context()).WithError(err).Info("no access token in the request or invalid Authorization header")
		return "", nil, &VerifyError{http.StatusForbidden, errors.New("no access token available")}
	}

	claims, err := verifyAndExtractClaims(context.Background(), jwtToken)
	if err != nil {
		logrus.WithContext(r.Context()).WithError(err).Error("token verification failed")
		return "", nil, &VerifyError{http.StatusUnauthorized, err}
	}
	logging.SetClient(r.Context(), claims.Authorized)

	tenant := r.Header.Get("tenant")
	if len(tenant) == 0 {
		tenant = r.Header.Get("X-Tenant")
	}
	superuser := hasRole(claims.RealmAccess.Roles, "superuser")
	if !superuser {
		if contains(claims.Tenants, tenant) == false {
			logrus.WithContext(r.Context()).Warn("tenant check failed")
			return tenant, nil, &VerifyError{http.StatusForbidden, errors.New("unauthorized access")}
		}
	}

	return tenant, claims, nil
}

// ConditionProtect Routes respectively to AccessGroups. Distinguish between admin route and user Route. ToDo: check body for refactoring.
func ConditionProtect(admin JWTHandlerFunc, user JWTHandlerFunc) http.HandlerFunc {
	toUpper := func(ss []string) []string {
		rss := make([]string, len(ss))
		for i, s := range ss {
			rss[i] = strings.ToUpper(s)
		}
		return rss
	}

	return func(w http.ResponseWriter, r *http.Request) {

		tenant, claims, err := retrieveClaims(r)
		if err != nil {
			w.WriteHeader(err.(*VerifyError).StatusCode)
			return
		}

		claims.Tenants = toUpper(claims.Tenants)
		if claims.AccessGroups.IsAdmin() {
			logging.SetTenant(r.Context(), tenant)
			admin(w, r, claims, strings.ToUpper(tenant))
		} else if claims.AccessGroups.IsUser() {
			logging.SetTenant(r.Context(), tenant)
			user(w, r, claims, strings.ToUpper(tenant))
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}
}

func Protect(handler JWTHandlerFunc) http.HandlerFunc {
	return verifyRequest(handler)
}

func ProtectApi(handler JWTHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, password, err := ParseBasicCredentialsFromHeader(r)
		if err != nil {
			logrus.WithContext(r.Context()).WithField("error", err.Error()).Warn("invalid Authorization header (basic)")
			// Distinguish between missing header and invalid schema/encoding if needed
			if errors.Is(err, ErrNoAuthorization) {
				w.WriteHeader(http.StatusForbidden)
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}
			return
		}

		idToken, err := kcClientAPI.AuthenticateUserWithPassword(username, password)
		if err != nil {
			logrus.WithContext(r.Context()).WithError(err).Error("authentication failed")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		var claims PlatformClaims
		if err := idToken.Claims(&claims); err != nil {
			logrus.WithContext(r.Context()).WithError(err).Error("failed to parse claims")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		logging.SetClient(r.Context(), claims.Authorized)

		tenant := r.Header.Get("X-Tenant")
		if !contains(claims.Tenants, tenant) {
			logrus.WithContext(r.Context()).Warn("tenant check failed")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		logging.SetTenant(r.Context(), tenant)

		// Same admin-group requirement as the JWT path (verifyRequest): these
		// endpoints expose member data and trigger EDA messages, so a plain
		// EEG_USER must not reach them.
		if !claims.AccessGroups.IsAdmin() {
			logrus.WithContext(r.Context()).Warn("unauthorized access - request has no admin access group")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		handler(w, r, &claims, strings.ToUpper(tenant))
	}
}

func verifyRequest(handler JWTHandlerFunc) func(w http.ResponseWriter, r *http.Request) {
	toUpper := func(ss []string) []string {
		rss := make([]string, len(ss))
		for i, s := range ss {
			rss[i] = strings.ToUpper(s)
		}
		return rss
	}

	return func(w http.ResponseWriter, r *http.Request) {
		tenant, claims, err := retrieveClaims(r)
		if err != nil {
			w.WriteHeader(err.(*VerifyError).StatusCode)
			return
		}

		logging.SetTenant(r.Context(), tenant)
		if claims.AccessGroups.IsAdmin() {
			claims.Tenants = toUpper(claims.Tenants)
			handler(w, r, claims, strings.ToUpper(tenant))
		} else {
			logrus.WithContext(r.Context()).Warn("unauthorized access - request has no admin access group")
			w.WriteHeader(http.StatusUnauthorized)
		}
	}
}
