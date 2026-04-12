package middleware

import (
	"net/http"
	"strings"

	apipkg "velix/internal/api"
)

// RequireScope returns middleware that checks the API key has a required scope.
// JWT-authenticated requests always pass (scopes only apply to API keys).
// If scopes is empty on the key ("{}"), all access is allowed (backwards compatible).
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFrom(r.Context())
			// JWT tokens (role is set) bypass scope check.
			if claims == nil {
				apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
				return
			}
			// If role is set, it's a JWT — skip scope check.
			if claims.Role != "" {
				next.ServeHTTP(w, r)
				return
			}
			// API key: check scopes stored in claims.Scopes.
			// If no scopes set (empty), allow all (backwards compat).
			if len(claims.Scopes) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			for _, s := range claims.Scopes {
				if s == scope || s == "*" {
					next.ServeHTTP(w, r)
					return
				}
			}
			apipkg.WriteError(w, r, apipkg.ErrForbidden)
		})
	}
}

// HasScope checks if a scope string slice contains the required scope.
func HasScope(scopes []string, required string) bool {
	for _, s := range scopes {
		if s == required || s == "*" || strings.HasPrefix(required, strings.TrimSuffix(s, "*")) {
			return true
		}
	}
	return false
}
