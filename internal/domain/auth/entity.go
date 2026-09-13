// Package auth contains domain types and logic for workspaces, users, and API keys.
package auth

import (
	"context"
	"time"
)

// contextKey is the type used for storing claims in context.
type contextKey string

// ClaimsContextKey is the key used to store/retrieve Claims in a context.
const ClaimsContextKey contextKey = "auth_claims"

// ClaimsFromContext retrieves auth claims from a context.
// Returns nil if no claims are present. This function can be called from any
// package without creating import cycles.
func ClaimsFromContext(ctx context.Context) *Claims {
	c, _ := ctx.Value(ClaimsContextKey).(*Claims)
	return c
}

// Workspace is a tenant — every instance and user belongs to one workspace.
type Workspace struct {
	ID              string
	Name            string
	Slug            string
	TermsAcceptedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Role controls what a user can do inside their workspace.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// Permission is a granular capability that can be granted to a "member" user.
// It has no effect on "admin" users, who always have full access.
type Permission string

const (
	PermMessagesView     Permission = "messages:view"
	PermMessagesSend     Permission = "messages:send"
	PermMessagesSchedule Permission = "messages:schedule"
	PermInstancesManage  Permission = "instances:manage"
	PermContactsManage   Permission = "contacts:manage"
)

// AllPermissions lists every valid permission value — used to validate and
// filter permissions requested for a "member" user.
var AllPermissions = []Permission{
	PermMessagesView, PermMessagesSend, PermMessagesSchedule,
	PermInstancesManage, PermContactsManage,
}

// User is a human operator belonging to a workspace.
type User struct {
	ID           string
	WorkspaceID  string
	Email        string
	PasswordHash string
	Role         Role
	Permissions  []string // ignored/empty when Role == RoleAdmin
	LastLoginAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// APIKey is a long-lived credential for programmatic access.
// The actual secret is only returned once at creation time.
type APIKey struct {
	ID          string
	WorkspaceID string
	UserID      string
	KeyHash     string // bcrypt hash stored in DB
	KeyPrefix   string // first ~12 chars for display
	Name        string
	Scopes      []string   // allowed scopes; empty = all access (backwards compat)
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
}

// IsValid returns true when the key has not been revoked and is not expired.
func (k *APIKey) IsValid() bool {
	if k.RevokedAt != nil {
		return false
	}
	if k.ExpiresAt != nil && time.Now().After(*k.ExpiresAt) {
		return false
	}
	return true
}

// Claims is the payload embedded in a JWT token.
type Claims struct {
	UserID      string   `json:"uid"`
	WorkspaceID string   `json:"wid"`
	Email       string   `json:"email"`
	Role        Role     `json:"role"`
	Permissions []string `json:"perms,omitempty"` // populated for JWT sessions of "member" users
	Scopes      []string `json:"scopes,omitempty"` // populated for API key requests
}
