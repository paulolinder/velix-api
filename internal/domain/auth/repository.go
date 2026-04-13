package auth

import "context"

// Repository defines persistence for workspaces, users, and API keys.
type Repository interface {
	// --- Workspaces ---
	CreateWorkspace(ctx context.Context, ws *Workspace) (*Workspace, error)
	GetWorkspaceByID(ctx context.Context, id string) (*Workspace, error)
	GetWorkspaceBySlug(ctx context.Context, slug string) (*Workspace, error)
	HasAnyWorkspace(ctx context.Context) (bool, error)

	// --- Users ---
	CreateUser(ctx context.Context, user *User) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, id string) (*User, error)
	UpdateLastLogin(ctx context.Context, userID string) error

	// --- API Keys ---
	CreateAPIKey(ctx context.Context, key *APIKey) (*APIKey, error)
	ListAPIKeys(ctx context.Context, workspaceID string) ([]*APIKey, error)
	GetAPIKeyByPrefix(ctx context.Context, prefix string) (*APIKey, error)
	RevokeAPIKey(ctx context.Context, keyID, workspaceID string) error
	TouchAPIKey(ctx context.Context, keyID string) error // update last_used_at
}
