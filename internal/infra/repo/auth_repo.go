package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"velix/internal/domain/auth"
)

// AuthRepo is the PostgreSQL implementation of auth.Repository.
type AuthRepo struct {
	db *pgxpool.Pool
}

// NewAuthRepo creates a new PostgreSQL-backed auth repository.
func NewAuthRepo(db *pgxpool.Pool) *AuthRepo {
	return &AuthRepo{db: db}
}

// ---------------------------------------------------------------------------
// Workspaces
// ---------------------------------------------------------------------------

func (r *AuthRepo) CreateWorkspace(ctx context.Context, ws *auth.Workspace) (*auth.Workspace, error) {
	const q = `
		INSERT INTO workspaces (name, slug)
		VALUES ($1, $2)
		RETURNING id, name, slug, created_at, updated_at`

	return scanWorkspace(r.db.QueryRow(ctx, q, ws.Name, ws.Slug))
}

func (r *AuthRepo) GetWorkspaceByID(ctx context.Context, id string) (*auth.Workspace, error) {
	const q = `SELECT id, name, slug, created_at, updated_at FROM workspaces WHERE id = $1`
	ws, err := scanWorkspace(r.db.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("workspace not found")
		}
		return nil, err
	}
	return ws, nil
}

func (r *AuthRepo) HasAnyWorkspace(ctx context.Context) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workspaces LIMIT 1)`).Scan(&exists)
	return exists, err
}

func (r *AuthRepo) GetWorkspaceBySlug(ctx context.Context, slug string) (*auth.Workspace, error) {
	const q = `SELECT id, name, slug, created_at, updated_at FROM workspaces WHERE slug = $1`
	ws, err := scanWorkspace(r.db.QueryRow(ctx, q, slug))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("workspace not found")
		}
		return nil, err
	}
	return ws, nil
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

func (r *AuthRepo) CreateUser(ctx context.Context, user *auth.User) (*auth.User, error) {
	const q = `
		INSERT INTO users (workspace_id, email, password_hash, role, permissions)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, workspace_id, email, password_hash, role, permissions, last_login_at, created_at, updated_at`

	perms := user.Permissions
	if perms == nil {
		perms = []string{}
	}
	return scanUser(r.db.QueryRow(ctx, q, user.WorkspaceID, user.Email, user.PasswordHash, user.Role, perms))
}

func (r *AuthRepo) GetUserByEmail(ctx context.Context, email string) (*auth.User, error) {
	const q = `
		SELECT id, workspace_id, email, password_hash, role, permissions, last_login_at, created_at, updated_at
		FROM users WHERE email = $1`

	u, err := scanUser(r.db.QueryRow(ctx, q, email))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	return u, nil
}

func (r *AuthRepo) GetUserByID(ctx context.Context, id string) (*auth.User, error) {
	const q = `
		SELECT id, workspace_id, email, password_hash, role, permissions, last_login_at, created_at, updated_at
		FROM users WHERE id = $1`

	u, err := scanUser(r.db.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	return u, nil
}

func (r *AuthRepo) UpdateLastLogin(ctx context.Context, userID string) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET last_login_at=NOW(), updated_at=NOW() WHERE id=$1`, userID)
	return err
}

func (r *AuthRepo) ListUsersByWorkspace(ctx context.Context, workspaceID string) ([]*auth.User, error) {
	const q = `
		SELECT id, workspace_id, email, password_hash, role, permissions, last_login_at, created_at, updated_at
		FROM users WHERE workspace_id = $1
		ORDER BY created_at ASC`

	rows, err := r.db.Query(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []*auth.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *AuthRepo) UpdateUser(ctx context.Context, id string, role auth.Role, permissions []string, passwordHash string) (*auth.User, error) {
	const q = `
		UPDATE users
		SET role = $2,
		    permissions = $3,
		    password_hash = CASE WHEN $4 <> '' THEN $4 ELSE password_hash END,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, workspace_id, email, password_hash, role, permissions, last_login_at, created_at, updated_at`

	if permissions == nil {
		permissions = []string{}
	}
	u, err := scanUser(r.db.QueryRow(ctx, q, id, role, permissions, passwordHash))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	return u, nil
}

func (r *AuthRepo) DeleteUser(ctx context.Context, id, workspaceID string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM users WHERE id = $1 AND workspace_id = $2`, id, workspaceID)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// ---------------------------------------------------------------------------
// API Keys
// ---------------------------------------------------------------------------

func (r *AuthRepo) CreateAPIKey(ctx context.Context, key *auth.APIKey) (*auth.APIKey, error) {
	const q = `
		INSERT INTO api_keys (workspace_id, user_id, key_hash, key_prefix, name, expires_at, scopes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, workspace_id, COALESCE(user_id::text,''), key_hash, key_prefix,
		          COALESCE(name,''), scopes, last_used_at, expires_at, revoked_at, created_at`

	scopes := key.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return scanAPIKey(r.db.QueryRow(ctx, q,
		key.WorkspaceID,
		nullString(key.UserID),
		key.KeyHash,
		key.KeyPrefix,
		nullString(key.Name),
		key.ExpiresAt,
		scopes,
	))
}

func (r *AuthRepo) ListAPIKeys(ctx context.Context, workspaceID string) ([]*auth.APIKey, error) {
	const q = `
		SELECT id, workspace_id, COALESCE(user_id::text,''), key_hash, key_prefix,
		       COALESCE(name,''), scopes, last_used_at, expires_at, revoked_at, created_at
		FROM api_keys WHERE workspace_id = $1
		ORDER BY created_at DESC`

	rows, err := r.db.Query(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var keys []*auth.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (r *AuthRepo) GetAPIKeyByPrefix(ctx context.Context, prefix string) (*auth.APIKey, error) {
	const q = `
		SELECT id, workspace_id, COALESCE(user_id::text,''), key_hash, key_prefix,
		       COALESCE(name,''), scopes, last_used_at, expires_at, revoked_at, created_at
		FROM api_keys WHERE key_prefix = $1 AND revoked_at IS NULL
		LIMIT 1`

	k, err := scanAPIKey(r.db.QueryRow(ctx, q, prefix))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("api key not found")
		}
		return nil, err
	}
	return k, nil
}

func (r *AuthRepo) RevokeAPIKey(ctx context.Context, keyID, workspaceID string) error {
	const q = `UPDATE api_keys SET revoked_at=NOW() WHERE id=$1 AND workspace_id=$2 AND revoked_at IS NULL`
	tag, err := r.db.Exec(ctx, q, keyID, workspaceID)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("api key not found or already revoked")
	}
	return nil
}

func (r *AuthRepo) TouchAPIKey(ctx context.Context, keyID string) error {
	_, err := r.db.Exec(ctx, `UPDATE api_keys SET last_used_at=NOW() WHERE id=$1`, keyID)
	return err
}

// ---------------------------------------------------------------------------
// Scanners
// ---------------------------------------------------------------------------

func scanWorkspace(row rowScanner) (*auth.Workspace, error) {
	ws := &auth.Workspace{}
	err := row.Scan(&ws.ID, &ws.Name, &ws.Slug, &ws.CreatedAt, &ws.UpdatedAt)
	return ws, err
}

func scanUser(row rowScanner) (*auth.User, error) {
	u := &auth.User{}
	var lastLogin *time.Time
	err := row.Scan(&u.ID, &u.WorkspaceID, &u.Email, &u.PasswordHash, &u.Role, &u.Permissions, &lastLogin, &u.CreatedAt, &u.UpdatedAt)
	u.LastLoginAt = lastLogin
	if u.Permissions == nil {
		u.Permissions = []string{}
	}
	return u, err
}

func scanAPIKey(row rowScanner) (*auth.APIKey, error) {
	k := &auth.APIKey{}
	err := row.Scan(
		&k.ID, &k.WorkspaceID, &k.UserID, &k.KeyHash, &k.KeyPrefix,
		&k.Name, &k.Scopes, &k.LastUsedAt, &k.ExpiresAt, &k.RevokedAt, &k.CreatedAt,
	)
	if k.Scopes == nil {
		k.Scopes = []string{}
	}
	return k, err
}
