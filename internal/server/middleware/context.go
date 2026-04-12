package middleware

import (
	"context"

	"velix/internal/domain/auth"
)

// WithClaims stores auth claims in the context.
func WithClaims(ctx context.Context, c *auth.Claims) context.Context {
	return context.WithValue(ctx, auth.ClaimsContextKey, c)
}

// ClaimsFrom retrieves auth claims from the context.
// Returns nil if no claims are present (unauthenticated request).
func ClaimsFrom(ctx context.Context) *auth.Claims {
	return auth.ClaimsFromContext(ctx)
}

// WorkspaceIDFrom is a convenience wrapper that returns the workspace ID or "".
func WorkspaceIDFrom(ctx context.Context) string {
	if c := ClaimsFrom(ctx); c != nil {
		return c.WorkspaceID
	}
	return ""
}
