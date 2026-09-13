// Package user contains HTTP handlers for admin-only workspace user management.
package user

import (
	"time"

	"velix/internal/domain/auth"
)

// CreateUserRequest is the body for POST /v1/users.
type CreateUserRequest struct {
	Email       string   `json:"email"`
	Password    string   `json:"password"`
	Role        string   `json:"role"` // "admin" or "member"
	Permissions []string `json:"permissions,omitempty"`
}

// UpdateUserRequest is the body for PATCH /v1/users/{userID}.
type UpdateUserRequest struct {
	Role        string   `json:"role"`
	Permissions []string `json:"permissions,omitempty"`
	Password    string   `json:"password,omitempty"` // optional reset
}

// UserResponse is the JSON shape of a workspace user (no password).
type UserResponse struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	Permissions []string   `json:"permissions"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func userFromDomain(u *auth.User) *UserResponse {
	perms := u.Permissions
	if perms == nil {
		perms = []string{}
	}
	return &UserResponse{
		ID:          u.ID,
		Email:       u.Email,
		Role:        string(u.Role),
		Permissions: perms,
		LastLoginAt: u.LastLoginAt,
		CreatedAt:   u.CreatedAt,
	}
}
