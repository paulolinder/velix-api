package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	apipkg "velix/internal/api"
	"velix/internal/domain/auth"
)

// AuthService is the interface the Authenticate middleware depends on.
// auth.Service satisfies this interface.
type AuthService interface {
	ParseToken(tokenStr string) (*auth.Claims, error)
	ValidateAPIKey(ctx context.Context, raw string) (*auth.APIKey, error)
}

// Authenticate returns an HTTP middleware that accepts either:
//
//   - Bearer JWT:  Authorization: Bearer <jwt>
//   - API Key:     Authorization: Bearer wapi_<key>
//                  or X-API-Key: wapi_<key>
//
// On success it stores *auth.Claims in the request context.
// On failure it returns 401.
func Authenticate(svc AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := extractToken(r)
			if raw == "" {
				apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
				return
			}

			var claims *auth.Claims

			if strings.HasPrefix(raw, "wapi_") {
				// API Key path.
				key, err := svc.ValidateAPIKey(r.Context(), raw)
				if err != nil {
					apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
					return
				}
				claims = &auth.Claims{
					WorkspaceID: key.WorkspaceID,
					UserID:      key.UserID,
					Scopes:      key.Scopes,
				}
			} else {
				// JWT path.
				var err error
				claims, err = svc.ParseToken(raw)
				if err != nil {
					apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
					return
				}
			}

			next.ServeHTTP(w, r.WithContext(WithClaims(r.Context(), claims)))
		})
	}
}

// RequireRole returns middleware that rejects requests where the user's role
// is not in the allowed set.
func RequireRole(allowed ...auth.Role) func(http.Handler) http.Handler {
	set := make(map[auth.Role]bool, len(allowed))
	for _, r := range allowed {
		set[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFrom(r.Context())
			if claims == nil || !set[claims.Role] {
				apipkg.WriteError(w, r, apipkg.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// InstanceOwnership is an interface that checks if an instance belongs to a workspace.
type InstanceOwnership interface {
	InstanceBelongsToWorkspace(ctx context.Context, workspaceID, instanceID string) bool
}

// RequireInstanceOwner returns middleware that verifies the {instanceID} URL param
// belongs to the authenticated user's workspace. Blocks cross-tenant access.
func RequireInstanceOwner(checker InstanceOwnership) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFrom(r.Context())
			if claims == nil {
				apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
				return
			}
			instanceID := chi.URLParam(r, "instanceID")
			if instanceID == "" {
				apipkg.WriteError(w, r, apipkg.ErrInstanceNotFound)
				return
			}
			if !checker.InstanceBelongsToWorkspace(r.Context(), claims.WorkspaceID, instanceID) {
				apipkg.WriteError(w, r, apipkg.ErrInstanceNotFound)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if after, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(after)
		}
	}
	if k := r.Header.Get("X-API-Key"); k != "" {
		return strings.TrimSpace(k)
	}
	return ""
}
