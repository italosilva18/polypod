package rest

import (
	"net/http"

	"github.com/costa/polypod/internal/auth"
)

const apiKeyHeader = "X-API-Key"

// APIKeyAuth returns middleware that validates the API key.
func APIKeyAuth(authz *auth.Authorizer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(apiKeyHeader)
			if key == "" {
				http.Error(w, `{"error":"missing API key"}`, http.StatusUnauthorized)
				return
			}
			if !authz.ValidAPIKey(key) {
				http.Error(w, `{"error":"invalid API key"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
