package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"at.ourproject/vfeeg-backend/logging"
	"at.ourproject/vfeeg-backend/logging/logtest"
	"github.com/stretchr/testify/assert"
)

// logCapture routes slog, logrus and log into a buffer for one test and restores them after.
func logCapture(t *testing.T) *bytes.Buffer {
	t.Helper()
	return logtest.Capture(t, func(w io.Writer) {
		logging.Setup(w, logging.Config{Level: slog.LevelInfo, JSON: true, Version: "test"})
	})
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	return logtest.Lines(t, buf)
}

func withClaims(t *testing.T, claims *PlatformClaims) {
	t.Helper()
	orig := VerifyTokenClaims
	t.Cleanup(func() { VerifyTokenClaims = orig })
	VerifyTokenClaims = func(context.Context, string) (*PlatformClaims, error) { return claims, nil }
}

func serveTenant(h http.Handler, header, tenant string) int {
	req := httptest.NewRequest("POST", "/any", nil)
	req.Header.Set("Authorization", BEARER_SCHEMA+"token")
	req.Header.Set(header, tenant)
	rw := httptest.NewRecorder()
	logging.AccessLog(h).ServeHTTP(rw, req)
	return rw.Code
}

func passes(handled *bool) JWTHandlerFunc {
	return func(http.ResponseWriter, *http.Request, *PlatformClaims, string) { *handled = true }
}

// L2: a tenant header that fails the check reaches no log line. tenant.id is
// only ever what passed it.
func TestTenantCheckFailed_HeaderIsNotLogged(t *testing.T) {
	const forged = "forged-by-erika"
	var handled bool
	for name, h := range map[string]http.Handler{
		"GQLProtect":       GQLProtect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { handled = true })),
		"Protect":          Protect(passes(&handled)),
		"ConditionProtect": ConditionProtect(passes(&handled), passes(&handled)),
	} {
		for _, header := range []string{"tenant", "X-Tenant"} {
			t.Run(name+"/"+header, func(t *testing.T) {
				withClaims(t, &PlatformClaims{Tenants: []string{"TE100100"}, AccessGroups: AccessGroups{"/EEG_ADMIN"}})
				buf := logCapture(t)

				code := serveTenant(h, header, forged)

				assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, code)
				assert.False(t, handled)
				assert.NotContains(t, strings.ToLower(buf.String()), forged)
				var failed bool
				for _, l := range logLines(t, buf) {
					assert.NotContains(t, l, "tenant.id")
					failed = failed || l["message"] == "tenant check failed"
				}
				assert.True(t, failed, "the failed check itself is logged")
			})
		}
	}
}

// A token without the admin group passed the tenant check: the refusal names the tenant.
func TestNoAdminGroup_LogsCheckedTenant(t *testing.T) {
	withClaims(t, &PlatformClaims{Tenants: []string{"TE100100"}, AccessGroups: AccessGroups{"/EEG_USER"}})
	buf := logCapture(t)
	var handled bool

	code := serveTenant(Protect(passes(&handled)), "tenant", "te100100")

	assert.Equal(t, http.StatusUnauthorized, code)
	for _, l := range logLines(t, buf) {
		assert.Equal(t, "TE100100", l["tenant.id"], l["message"])
		assert.NotEmpty(t, l["request.id"], l["message"])
	}
}

// A superuser skips the tenant check, so the header arrives as sent: only an
// id-shaped value is logged.
func TestSuperuser_HeaderOnlyLoggedWhenShapedLikeATenant(t *testing.T) {
	superuser := &PlatformClaims{RealmAccess: RealmRoles{Roles: []string{"superuser"}}, AccessGroups: AccessGroups{"/EEG_USER"}}
	var handled bool

	t.Run("not an id", func(t *testing.T) {
		withClaims(t, superuser)
		buf := logCapture(t)
		serveTenant(Protect(passes(&handled)), "tenant", "Erika Mustermann\nx")
		assert.NotContains(t, strings.ToUpper(buf.String()), "ERIKA")
		for _, l := range logLines(t, buf) {
			assert.NotContains(t, l, "tenant.id")
		}
	})
	t.Run("an id", func(t *testing.T) {
		withClaims(t, superuser)
		buf := logCapture(t)
		serveTenant(Protect(passes(&handled)), "tenant", "GC123456-001")
		for _, l := range logLines(t, buf) {
			assert.Equal(t, "GC123456-001", l["tenant.id"], l["message"])
		}
	})
}

// Q6: a failed token check logs the error as error.type and error.message under
// a fixed message, no constant label in the error field.
func TestTokenCheckFailed_LogsTheError(t *testing.T) {
	var handled bool
	for name, h := range map[string]http.Handler{
		"GQLProtect": GQLProtect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { handled = true })),
		"Protect":    Protect(passes(&handled)),
	} {
		t.Run(name, func(t *testing.T) {
			orig := VerifyTokenClaims
			t.Cleanup(func() { VerifyTokenClaims = orig })
			VerifyTokenClaims = func(context.Context, string) (*PlatformClaims, error) {
				return nil, errors.New("token is expired")
			}
			buf := logCapture(t)

			assert.Equal(t, http.StatusUnauthorized, serveTenant(h, "tenant", "TE100100"))
			assert.False(t, handled)
			ls := logLines(t, buf)
			assert.Equal(t, "error", ls[0]["log.level"])
			assert.NotEmpty(t, ls[0]["error.type"])
			assert.Contains(t, ls[0]["error.message"], "expired", "the error is in its field")
			assert.NotContains(t, ls[0]["message"], "expired", "not in the text")
			assert.NotContains(t, buf.String(), "JWT-Token")
		})
	}
}

func TestNoToken_LogsTheError(t *testing.T) {
	var handled bool
	buf := logCapture(t)
	req := httptest.NewRequest("POST", "/any", nil)
	rw := httptest.NewRecorder()
	logging.AccessLog(Protect(passes(&handled))).ServeHTTP(rw, req)

	assert.Equal(t, http.StatusForbidden, rw.Code)
	ls := logLines(t, buf)
	assert.Equal(t, "info", ls[0]["log.level"])
	assert.Equal(t, ErrNoAuthorization.Error(), ls[0]["error.message"])
	assert.NotContains(t, buf.String(), "JWT-Token")
}
