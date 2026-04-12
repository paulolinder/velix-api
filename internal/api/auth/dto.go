// Package auth contains HTTP handlers for registration, login, and API key management.
package auth

import (
	"time"

	"velix/internal/domain/auth"
)

// RegisterRequest is the body for POST /v1/auth/register.
type RegisterRequest struct {
	WorkspaceName string `json:"workspace_name"`
	Slug          string `json:"slug"`
	Email         string `json:"email"`
	Password      string `json:"password"`
}

// LoginRequest is the body for POST /v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// CreateAPIKeyRequest is the body for POST /v1/auth/api-keys.
type CreateAPIKeyRequest struct {
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Scopes    []string   `json:"scopes,omitempty"`
}

// RegisterResponse is returned after a successful registration.
type RegisterResponse struct {
	Workspace *WorkspaceResponse `json:"workspace"`
	User      *UserResponse      `json:"user"`
	Token     string             `json:"token"`
}

// LoginResponse is returned after a successful login.
type LoginResponse struct {
	Token string        `json:"token"`
	User  *UserResponse `json:"user"`
}

// WorkspaceResponse is the JSON shape of a workspace.
type WorkspaceResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

// UserResponse is the JSON shape of a user (no password).
type UserResponse struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// APIKeyResponse is the JSON shape of an API key listing (hash is never returned).
type APIKeyResponse struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	KeyPrefix   string     `json:"key_prefix"`
	Name        string     `json:"name,omitempty"`
	Scopes      []string   `json:"scopes"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CreateAPIKeyResponse includes the plaintext key (shown once only).
type CreateAPIKeyResponse struct {
	Key    string          `json:"key"` // raw secret — shown once
	Detail *APIKeyResponse `json:"detail"`
}

func workspaceFromDomain(ws *auth.Workspace) *WorkspaceResponse {
	return &WorkspaceResponse{ID: ws.ID, Name: ws.Name, Slug: ws.Slug, CreatedAt: ws.CreatedAt}
}

func userFromDomain(u *auth.User) *UserResponse {
	return &UserResponse{
		ID:          u.ID,
		WorkspaceID: u.WorkspaceID,
		Email:       u.Email,
		Role:        string(u.Role),
		LastLoginAt: u.LastLoginAt,
		CreatedAt:   u.CreatedAt,
	}
}

func apiKeyFromDomain(k *auth.APIKey) *APIKeyResponse {
	scopes := k.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return &APIKeyResponse{
		ID:          k.ID,
		WorkspaceID: k.WorkspaceID,
		KeyPrefix:   k.KeyPrefix,
		Name:        k.Name,
		Scopes:      scopes,
		LastUsedAt:  k.LastUsedAt,
		ExpiresAt:   k.ExpiresAt,
		RevokedAt:   k.RevokedAt,
		CreatedAt:   k.CreatedAt,
	}
}
