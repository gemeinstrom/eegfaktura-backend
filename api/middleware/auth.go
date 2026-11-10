package middleware

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strings"
)

var (
	ErrNoAuthorization      = errors.New("authorization header missing")
	ErrInvalidAuthSchema    = errors.New("invalid authorization schema")
	ErrInvalidBasicEncoding = errors.New("invalid basic auth encoding")
	ErrInvalidBasicFormat   = errors.New("invalid basic auth format")
)

const (
	BASIC_SCHEMA  string = "Basic "
	BEARER_SCHEMA string = "Bearer "
)

type contextKey struct {
	name string
}

var tenantCtxKey = &contextKey{"tenant"}

// ParseBearerTokenFromHeader extracts the Bearer token from the Authorization header.
// Returns a non-empty token or an error describing the problem.
func ParseBearerTokenFromHeader(r *http.Request) (string, error) {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return "", ErrNoAuthorization
	}
	if !strings.HasPrefix(auth, BEARER_SCHEMA) {
		return "", ErrInvalidAuthSchema
	}
	token := strings.TrimSpace(auth[len(BEARER_SCHEMA):])
	if token == "" {
		return "", ErrInvalidAuthSchema
	}
	return token, nil
}

// ParseBasicCredentialsFromHeader extracts username and password from a Basic
// Authorization header. Credentials are expected to be base64.StdEncoding(user:pass).
func ParseBasicCredentialsFromHeader(r *http.Request) (string, string, error) {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return "", "", ErrNoAuthorization
	}
	if !strings.HasPrefix(auth, BASIC_SCHEMA) {
		return "", "", ErrInvalidAuthSchema
	}
	encoded := strings.TrimSpace(auth[len(BASIC_SCHEMA):])
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", ErrInvalidBasicEncoding
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", ErrInvalidBasicFormat
	}
	return parts[0], parts[1], nil
}

func contains(s []string, str string) bool {
	for _, v := range s {
		if strings.ToUpper(v) == strings.ToUpper(str) {
			return true
		}
	}
	return false
}

func ForContextTenant(ctx context.Context) string {
	raw, _ := ctx.Value(tenantCtxKey).(string)
	return raw
}

func hasRole(roles []string, role string) bool {
	return slices.Contains(roles, role)
}

func IsSuperuser(roles []string) bool {
	return hasRole(roles, "superuser")
}
