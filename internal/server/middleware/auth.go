package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	apipkg "velix/internal/api"
	"velix/internal/domain/auth"
)

// instanceScopePrefix is the prefix used to scope an API key to a single instance.
const instanceScopePrefix = "instance:"

// instanceScopeID extracts the instance UUID from claims scopes.
// Returns "" if no instance scope is set (meaning the key has full workspace access).
func instanceScopeID(claims *auth.Claims) string {
	for _, s := range claims.Scopes {
		if after, ok := strings.CutPrefix(s, instanceScopePrefix); ok {
			return after
		}
	}
	return ""
}

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
//
// If the API key carries an instance scope (e.g. "instance:uuid"), the request is
// additionally restricted to that exact instance — other instances return 404.
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
			// If the API key is scoped to a specific instance, enforce it.
			if scopedID := instanceScopeID(claims); scopedID != "" && scopedID != instanceID {
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
