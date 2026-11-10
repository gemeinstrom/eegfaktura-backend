package middleware

import (
	"net/http"
)

// JWTHandlerFunc Protected HTTP Callback function containing JWT Claims and the tenant.
type JWTHandlerFunc func(http.ResponseWriter, *http.Request, *PlatformClaims, string)
