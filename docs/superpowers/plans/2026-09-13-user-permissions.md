# User Permissions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the workspace admin manage other users from the admin panel with granular permissions (view/send/schedule messages, manage instances, manage contacts/groups), and hide the public "Criar conta" tab once a workspace admin already exists.

**Architecture:** Retire the unused `developer`/`viewer` roles down to two: `admin` (full access) and `member` (gated by a new `permissions text[]` column). Add a `RequirePermission` middleware (same shape as the existing `RequireRole`) and wire it onto the message/instance/contact/group routes. Add an admin-only `/v1/users` CRUD API and a `users.html` admin panel page. Add a public `GET /v1/auth/registration-status` endpoint so the login page can hide "Criar conta" once registration is closed.

**Tech Stack:** Go 1.25, chi router, pgx/Postgres, golang-migrate (embedded SQL migrations), Alpine.js + Tailwind (static admin panel), JWT (golang-jwt/v5).

**Spec:** `docs/superpowers/specs/2026-09-13-user-permissions-design.md`

## Global Constraints

- Permissions are global within a workspace — no per-instance scoping (per spec, decided with user).
- Managing users (create/edit/delete) is **always** admin-only — never a toggleable permission (per spec).
- API keys (`internal/domain/auth/entity.go` `APIKey.Scopes`) are a separate system and must not be touched — `RequirePermission` only affects JWT sessions (`claims.Role != ""`); API key requests (`claims.Role == ""`) pass through unaffected, exactly like today.
- `/audit-logs` and `/admin/stats` stay open to any authenticated workspace user, exactly as today — not gated by the new permission system (per corrected spec).
- Existing non-admin users (`role IN ('developer','viewer')`) must be grandfathered to `role='member'` with **all 5 permissions granted** on migration, so nobody already using the system loses access.
- Every new/changed Go file must compile with the existing `auth.Repository` interface — updating the interface means updating both `AuthRepo` (real) and `mockRepo` (test) implementations in the same task.

---

### Task 1: Migration `012_user_permissions` — retire `developer`/`viewer`, add `permissions`

**Files:**
- Create: `internal/infra/database/migrations/012_user_permissions.up.sql`
- Create: `internal/infra/database/migrations/012_user_permissions.down.sql`

**Interfaces:**
- Produces: a `permissions text[] NOT NULL DEFAULT '{}'` column on `users`, and a `role` CHECK constraint restricted to `('admin','member')`. All later tasks assume this column and constraint exist.

- [ ] **Step 1: Write the up migration**

```sql
-- internal/infra/database/migrations/012_user_permissions.up.sql
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD COLUMN IF NOT EXISTS permissions text[] NOT NULL DEFAULT '{}';

-- Grandfather existing non-admin users: they had full access before permissions
-- existed, so give them every permission instead of silently locking them out.
UPDATE users SET role = 'member' WHERE role IN ('developer', 'viewer');
UPDATE users
SET permissions = ARRAY['messages:view','messages:send','messages:schedule','instances:manage','contacts:manage']
WHERE role = 'member';

ALTER TABLE users ALTER COLUMN role SET DEFAULT 'member';
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'member'));
```

- [ ] **Step 2: Write the down migration**

```sql
-- internal/infra/database/migrations/012_user_permissions.down.sql
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ALTER COLUMN role SET DEFAULT 'developer';
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'developer', 'viewer'));
UPDATE users SET role = 'developer' WHERE role = 'member';
ALTER TABLE users DROP COLUMN IF EXISTS permissions;
```

- [ ] **Step 3: Verify the migration applies cleanly against the running local stack**

The project already has a local Docker stack (`postgres`, `redis`, `api`) running via `docker compose --profile full up -d`. Migrations are embedded in the binary (`internal/infra/database/migrate.go`) and run automatically on API startup.

Run:
```bash
docker compose --profile full up -d --build api
sleep 3
docker compose exec postgres psql -U velix -d velix -c "\d users"
```
Expected: the `users` table description shows a `permissions` column of type `text[]` and the `role` check constraint listed as `CHECK (((role)::text = ANY ((ARRAY['admin'::character varying, 'member'::character varying])::text[])))`.

- [ ] **Step 4: Commit**

```bash
git add internal/infra/database/migrations/012_user_permissions.up.sql internal/infra/database/migrations/012_user_permissions.down.sql
git commit -m "feat(db): add user permissions column, retire developer/viewer roles"
```

---

### Task 2: Domain model — `Role`, `Permission`, `User.Permissions`, `Claims.Permissions`

**Files:**
- Modify: `internal/domain/auth/entity.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `auth.RoleAdmin`, `auth.RoleMember` (replacing `RoleDeveloper`/`RoleViewer`), `auth.Permission` type with 5 constants, `auth.AllPermissions []Permission`, `User.Permissions []string`, `Claims.Permissions []string`. Every later task that references roles/permissions uses these exact names.

- [ ] **Step 1: Update `entity.go`**

Replace the `Role` block and `User`/`Claims` structs:

```go
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
```

And in `Claims`, add the `Permissions` field:

```go
// Claims is the payload embedded in a JWT token.
type Claims struct {
	UserID      string   `json:"uid"`
	WorkspaceID string   `json:"wid"`
	Email       string   `json:"email"`
	Role        Role     `json:"role"`
	Permissions []string `json:"perms,omitempty"` // populated for JWT sessions of "member" users
	Scopes      []string `json:"scopes,omitempty"` // populated for API key requests
}
```

- [ ] **Step 2: Compile-check (this task alone will not compile yet — that's expected)**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go build ./... 2>&1 | head -40`
Expected: FAIL — errors about `RoleDeveloper`/`RoleViewer` undefined in `service.go`/`service_test.go`, and `scanUser` missing a `Permissions` scan target in `auth_repo.go`. This confirms the compiler is pointing at exactly the call sites the next tasks fix. Do not fix them here — Task 3/4 do that with tests.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/auth/entity.go
git commit -m "feat(auth): replace developer/viewer roles with member + granular permissions"
```

---

### Task 3: Repository layer — persist and read `permissions`

**Files:**
- Modify: `internal/domain/auth/repository.go`
- Modify: `internal/infra/repo/auth_repo.go`

**Interfaces:**
- Consumes: `auth.User.Permissions []string` (Task 2).
- Produces: `Repository.ListUsersByWorkspace(ctx, workspaceID) ([]*User, error)`, `Repository.UpdateUser(ctx, id string, role Role, permissions []string, passwordHash string) (*User, error)`, `Repository.DeleteUser(ctx, id, workspaceID string) error`. Task 4's `mockRepo` and `Service` methods call these exact signatures.

- [ ] **Step 1: Add the 3 new methods to the `Repository` interface**

In `internal/domain/auth/repository.go`, inside the `// --- Users ---` block:

```go
	// --- Users ---
	CreateUser(ctx context.Context, user *User) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, id string) (*User, error)
	UpdateLastLogin(ctx context.Context, userID string) error
	ListUsersByWorkspace(ctx context.Context, workspaceID string) ([]*User, error)
	UpdateUser(ctx context.Context, id string, role Role, permissions []string, passwordHash string) (*User, error)
	DeleteUser(ctx context.Context, id, workspaceID string) error
```

- [ ] **Step 2: Update `scanUser` and every existing query to include `permissions`**

In `internal/infra/repo/auth_repo.go`, change `scanUser`:

```go
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
```

Update `CreateUser`, `GetUserByEmail`, and `GetUserByID` to select/insert `permissions` in that same column order:

```go
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
```

- [ ] **Step 3: Add `ListUsersByWorkspace`, `UpdateUser`, `DeleteUser`**

Add these after `UpdateLastLogin` in `internal/infra/repo/auth_repo.go`:

```go
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
```

- [ ] **Step 4: Compile-check**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go build ./internal/infra/... 2>&1`
Expected: PASS. (`internal/domain/auth` itself still fails to build until Task 4 fixes `service.go`/`service_test.go` — that's fine, this task only touches `internal/infra/repo`, which depends on the `auth` package's exported types, not its internal compile errors in `service.go`. If `go build ./internal/infra/...` fails because it also compiles `internal/domain/auth`, that's expected until Task 4 — re-run this same command again at the end of Task 4 to confirm.)

- [ ] **Step 5: Commit**

```bash
git add internal/domain/auth/repository.go internal/infra/repo/auth_repo.go
git commit -m "feat(auth): persist and query user permissions in Postgres"
```

---

### Task 4: Service layer — JWT permissions + user management (TDD)

**Files:**
- Modify: `internal/domain/auth/service.go`
- Modify: `internal/domain/auth/service_test.go`

**Interfaces:**
- Consumes: `auth.Repository` (Task 3), `auth.User.Permissions`, `auth.Claims.Permissions`, `auth.AllPermissions` (Task 2).
- Produces: `Service.CreateUser(ctx, workspaceID, email, password string, role Role, permissions []string) (*User, error)`, `Service.ListUsers(ctx, workspaceID string) ([]*User, error)`, `Service.UpdateUser(ctx, workspaceID, targetUserID, callerUserID string, role Role, permissions []string, newPassword string) (*User, error)`, `Service.DeleteUser(ctx, workspaceID, targetUserID, callerUserID string) error`, sentinel errors `ErrUserNotFound`, `ErrSelfLockout`, `ErrInvalidRole`. Task 9 (the `internal/api/user` handler) calls these exact signatures.

- [ ] **Step 1: Update `mockRepo` in `service_test.go` to satisfy the new interface**

Add these 3 methods after the existing `HasAnyWorkspace` mock method in `internal/domain/auth/service_test.go`:

```go
func (m *mockRepo) ListUsersByWorkspace(_ context.Context, workspaceID string) ([]*User, error) {
	var out []*User
	for _, u := range m.users {
		if u.WorkspaceID == workspaceID {
			out = append(out, u)
		}
	}
	return out, nil
}
func (m *mockRepo) UpdateUser(_ context.Context, id string, role Role, permissions []string, passwordHash string) (*User, error) {
	for _, u := range m.users {
		if u.ID == id {
			u.Role = role
			u.Permissions = permissions
			if passwordHash != "" {
				u.PasswordHash = passwordHash
			}
			return u, nil
		}
	}
	return nil, ErrUserNotFound
}
func (m *mockRepo) DeleteUser(_ context.Context, id, _ string) error {
	for email, u := range m.users {
		if u.ID == id {
			delete(m.users, email)
			return nil
		}
	}
	return ErrUserNotFound
}
```

- [ ] **Step 2: Write the failing tests for `CreateUser`/`ListUsers`/`UpdateUser`/`DeleteUser`**

Append to `internal/domain/auth/service_test.go`:

```go
func TestCreateUser_Member(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, _, _, err := svc.Register(context.Background(), "WS", "ws", "admin@test.com", "Password1")
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	u, err := svc.CreateUser(context.Background(), "ws-ws", "member@test.com", "Password1", RoleMember,
		[]string{"messages:view", "messages:send", "not-a-real-permission"})
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if u.Role != RoleMember {
		t.Errorf("role = %q, want %q", u.Role, RoleMember)
	}
	if len(u.Permissions) != 2 {
		t.Errorf("permissions = %v, want exactly [messages:view messages:send] (invalid one filtered out)", u.Permissions)
	}
}

func TestCreateUser_AdminIgnoresPermissions(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	u, err := svc.CreateUser(context.Background(), "ws-ws", "admin2@test.com", "Password1", RoleAdmin,
		[]string{"messages:view"})
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	if len(u.Permissions) != 0 {
		t.Errorf("admin permissions = %v, want empty (ignored)", u.Permissions)
	}
}

func TestCreateUser_InvalidRole(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	_, err := svc.CreateUser(context.Background(), "ws-ws", "x@test.com", "Password1", Role("owner"), nil)
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}
}

func TestListUsers(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, _, _, _ = svc.Register(context.Background(), "WS", "ws", "a@test.com", "Password1")
	svc.CreateUser(context.Background(), "ws-ws", "b@test.com", "Password1", RoleMember, []string{"messages:view"})

	users, err := svc.ListUsers(context.Background(), "ws-ws")
	if err != nil {
		t.Fatalf("ListUsers failed: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("got %d users, want 2", len(users))
	}
}

func TestUpdateUser_ChangesPermissions(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, admin, _, _ := svc.Register(context.Background(), "WS", "ws", "admin3@test.com", "Password1")
	member, _ := svc.CreateUser(context.Background(), "ws-ws", "member3@test.com", "Password1", RoleMember, []string{"messages:view"})

	updated, err := svc.UpdateUser(context.Background(), "ws-ws", member.ID, admin.ID, RoleMember,
		[]string{"messages:view", "instances:manage"}, "")
	if err != nil {
		t.Fatalf("UpdateUser failed: %v", err)
	}
	if len(updated.Permissions) != 2 {
		t.Errorf("permissions = %v, want 2 entries", updated.Permissions)
	}
}

func TestUpdateUser_SelfLockoutBlocked(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, admin, _, _ := svc.Register(context.Background(), "WS", "ws", "admin4@test.com", "Password1")

	_, err := svc.UpdateUser(context.Background(), "ws-ws", admin.ID, admin.ID, RoleMember, nil, "")
	if !errors.Is(err, ErrSelfLockout) {
		t.Fatalf("expected ErrSelfLockout, got %v", err)
	}
}

func TestDeleteUser_SelfDeleteBlocked(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, admin, _, _ := svc.Register(context.Background(), "WS", "ws", "admin5@test.com", "Password1")

	err := svc.DeleteUser(context.Background(), "ws-ws", admin.ID, admin.ID)
	if !errors.Is(err, ErrSelfLockout) {
		t.Fatalf("expected ErrSelfLockout, got %v", err)
	}
}

func TestDeleteUser_RemovesOtherUser(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, admin, _, _ := svc.Register(context.Background(), "WS", "ws", "admin6@test.com", "Password1")
	member, _ := svc.CreateUser(context.Background(), "ws-ws", "member6@test.com", "Password1", RoleMember, nil)

	if err := svc.DeleteUser(context.Background(), "ws-ws", member.ID, admin.ID); err != nil {
		t.Fatalf("DeleteUser failed: %v", err)
	}
	if _, err := svc.repo.GetUserByID(context.Background(), member.ID); err == nil {
		t.Error("expected user to be gone after DeleteUser")
	}
}

func TestParseToken_CarriesPermissions(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, admin, _, _ := svc.Register(context.Background(), "WS", "ws", "admin7@test.com", "Password1")
	member, _ := svc.CreateUser(context.Background(), "ws-ws", "member7@test.com", "Password1", RoleMember, []string{"messages:view"})
	_ = admin

	_, token, err := svc.Login(context.Background(), "member7@test.com", "Password1")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	claims, err := svc.ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken failed: %v", err)
	}
	if len(claims.Permissions) != 1 || claims.Permissions[0] != "messages:view" {
		t.Errorf("claims.Permissions = %v, want [messages:view]", claims.Permissions)
	}
	_ = member
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go test ./internal/domain/auth/... 2>&1 | head -60`
Expected: FAIL to compile — `svc.CreateUser`, `svc.ListUsers`, `svc.UpdateUser`, `svc.DeleteUser`, `ErrUserNotFound`, `ErrSelfLockout`, `ErrInvalidRole` undefined.

- [ ] **Step 4: Implement the service methods**

In `internal/domain/auth/service.go`, add to the sentinel error block:

```go
var (
	ErrInvalidCredentials = fmt.Errorf("invalid email or password")
	ErrInvalidToken       = fmt.Errorf("invalid or expired token")
	ErrInvalidAPIKey      = fmt.Errorf("invalid or revoked API key")
	ErrWeakPassword       = fmt.Errorf("password too weak")
	ErrAccountLocked      = fmt.Errorf("account temporarily locked — too many failed attempts")
	ErrUserNotFound       = fmt.Errorf("user not found")
	ErrSelfLockout        = fmt.Errorf("cannot change your own role away from admin, or delete your own account")
	ErrInvalidRole        = fmt.Errorf(`role must be "admin" or "member"`)
)
```

Add this new section right after the `Register` function (still inside the "Workspace registration" section is fine, or its own section — add a `// User management` section after `Login`):

```go
// ---------------------------------------------------------------------------
// User management (admin-only — enforced by the HTTP handler, not here)
// ---------------------------------------------------------------------------

// normalizePermissions filters requested permissions down to valid, deduplicated
// values. Admins always get an empty slice — permissions are irrelevant for them.
func normalizePermissions(role Role, requested []string) []string {
	if role == RoleAdmin {
		return []string{}
	}
	valid := make(map[string]bool, len(AllPermissions))
	for _, p := range AllPermissions {
		valid[string(p)] = true
	}
	out := make([]string, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	for _, p := range requested {
		if valid[p] && !seen[p] {
			out = append(out, p)
			seen[p] = true
		}
	}
	return out
}

// CreateUser creates an additional user in an existing workspace. Only callable
// by an admin (enforced by the HTTP handler via RequireRole).
func (s *Service) CreateUser(ctx context.Context, workspaceID, email, password string, role Role, permissions []string) (*User, error) {
	if role != RoleAdmin && role != RoleMember {
		return nil, ErrInvalidRole
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	user := &User{
		WorkspaceID:  workspaceID,
		Email:        email,
		PasswordHash: string(hash),
		Role:         role,
		Permissions:  normalizePermissions(role, permissions),
	}
	created, err := s.repo.CreateUser(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return created, nil
}

// ListUsers returns every user belonging to a workspace.
func (s *Service) ListUsers(ctx context.Context, workspaceID string) ([]*User, error) {
	return s.repo.ListUsersByWorkspace(ctx, workspaceID)
}

// UpdateUser changes a user's role/permissions and, optionally, resets their
// password (pass newPassword="" to leave it unchanged). Blocks an admin from
// demoting themselves away from admin (self-lockout).
func (s *Service) UpdateUser(ctx context.Context, workspaceID, targetUserID, callerUserID string, role Role, permissions []string, newPassword string) (*User, error) {
	if role != RoleAdmin && role != RoleMember {
		return nil, ErrInvalidRole
	}
	if targetUserID == callerUserID && role != RoleAdmin {
		return nil, ErrSelfLockout
	}
	target, err := s.repo.GetUserByID(ctx, targetUserID)
	if err != nil || target.WorkspaceID != workspaceID {
		return nil, ErrUserNotFound
	}

	passwordHash := ""
	if newPassword != "" {
		if err := validatePassword(newPassword); err != nil {
			return nil, err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
		if err != nil {
			return nil, fmt.Errorf("hash password: %w", err)
		}
		passwordHash = string(hash)
	}

	updated, err := s.repo.UpdateUser(ctx, targetUserID, role, normalizePermissions(role, permissions), passwordHash)
	if err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}
	return updated, nil
}

// DeleteUser removes a user from a workspace. Blocks deleting yourself.
func (s *Service) DeleteUser(ctx context.Context, workspaceID, targetUserID, callerUserID string) error {
	if targetUserID == callerUserID {
		return ErrSelfLockout
	}
	target, err := s.repo.GetUserByID(ctx, targetUserID)
	if err != nil || target.WorkspaceID != workspaceID {
		return ErrUserNotFound
	}
	if err := s.repo.DeleteUser(ctx, targetUserID, workspaceID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}
```

Update `Register` to explicitly store an empty permission slice (admins ignore it, but keeps the DB row consistent):

```go
	user, err := s.repo.CreateUser(ctx, &User{
		WorkspaceID:  ws.ID,
		Email:        email,
		PasswordHash: string(hash),
		Role:         RoleAdmin,
		Permissions:  []string{},
	})
```

Update `signToken` to embed permissions:

```go
func (s *Service) signToken(user *User) (string, error) {
	claims := jwt.MapClaims{
		"jti":   uuid.NewString(),
		"uid":   user.ID,
		"wid":   user.WorkspaceID,
		"email": user.Email,
		"role":  string(user.Role),
		"perms": user.Permissions,
		"iss":   "velix-api",
		"exp":   time.Now().Add(s.jwtExpiry).Unix(),
		"iat":   time.Now().Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return signed, nil
}
```

Update `ParseToken` to read permissions back out, and add the small helper it needs:

```go
	return &Claims{
		UserID:      stringClaim(mc, "uid"),
		WorkspaceID: stringClaim(mc, "wid"),
		Email:       stringClaim(mc, "email"),
		Role:        Role(stringClaim(mc, "role")),
		Permissions: stringSliceClaim(mc, "perms"),
	}, nil
}

func stringSliceClaim(mc jwt.MapClaims, key string) []string {
	raw, ok := mc[key].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
```
(Place `stringSliceClaim` right after the existing `stringClaim` function, and make sure `ParseToken`'s closing brace lines up — you're inserting the `Permissions:` field into the existing returned `&Claims{...}` literal, not replacing the whole function.)

Also add `"errors"` to the test file's imports (needed for `errors.Is` in the new tests) — `internal/domain/auth/service_test.go` currently imports `context`, `testing`, `time`; add `errors` to that block.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go test ./internal/domain/auth/... -v 2>&1 | tail -80`
Expected: PASS — all tests including the new ones (`TestCreateUser_Member`, `TestCreateUser_AdminIgnoresPermissions`, `TestCreateUser_InvalidRole`, `TestListUsers`, `TestUpdateUser_ChangesPermissions`, `TestUpdateUser_SelfLockoutBlocked`, `TestDeleteUser_SelfDeleteBlocked`, `TestDeleteUser_RemovesOtherUser`, `TestParseToken_CarriesPermissions`).

- [ ] **Step 6: Full build check across the whole module**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go build ./... 2>&1`
Expected: PASS (this confirms Task 3's repo layer + this task's service layer compile together correctly).

- [ ] **Step 7: Commit**

```bash
git add internal/domain/auth/service.go internal/domain/auth/service_test.go
git commit -m "feat(auth): add user management service methods and JWT permission claims"
```

---

### Task 5: `RequirePermission` middleware (TDD)

**Files:**
- Modify: `internal/server/middleware/auth.go`
- Create: `internal/server/middleware/permission_test.go`

**Interfaces:**
- Consumes: `auth.Claims.Role`, `auth.Claims.Permissions`, `auth.Permission` (Task 2/4).
- Produces: `middleware.RequirePermission(perm auth.Permission) func(http.Handler) http.Handler`. Task 6/7 wire this onto routes.

- [ ] **Step 1: Write the failing tests**

Create `internal/server/middleware/permission_test.go`:

```go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"velix/internal/domain/auth"
)

func TestRequirePermission_AdminBypasses(t *testing.T) {
	called := false
	h := RequirePermission(auth.PermInstancesManage)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithClaims(req.Context(), &auth.Claims{Role: auth.RoleAdmin}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected admin request to reach the handler")
	}
}

func TestRequirePermission_MemberWithPermission(t *testing.T) {
	called := false
	h := RequirePermission(auth.PermMessagesView)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithClaims(req.Context(), &auth.Claims{
		Role:        auth.RoleMember,
		Permissions: []string{"messages:view"},
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected member with the required permission to reach the handler")
	}
}

func TestRequirePermission_MemberWithoutPermission(t *testing.T) {
	called := false
	h := RequirePermission(auth.PermMessagesSend)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithClaims(req.Context(), &auth.Claims{
		Role:        auth.RoleMember,
		Permissions: []string{"messages:view"},
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Fatal("expected member without the required permission to be rejected")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestRequirePermission_APIKeyBypassesUnaffected(t *testing.T) {
	called := false
	h := RequirePermission(auth.PermInstancesManage)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// API key claims never set Role — this must keep today's behavior (unaffected).
	req = req.WithContext(WithClaims(req.Context(), &auth.Claims{Scopes: []string{"*"}}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected API key request to reach the handler, unaffected by RequirePermission")
	}
}

func TestRequirePermission_NoClaims(t *testing.T) {
	called := false
	h := RequirePermission(auth.PermMessagesView)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Fatal("expected unauthenticated request to be rejected")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go test ./internal/server/middleware/... 2>&1`
Expected: FAIL to compile — `RequirePermission` undefined.

- [ ] **Step 3: Implement `RequirePermission`**

In `internal/server/middleware/auth.go`, add `"slices"` to the import block and append this function after `RequireRole`:

```go
// RequirePermission returns middleware that rejects requests where the
// authenticated user's role is "member" and lacks perm in its permission set.
// "admin" always passes. API key requests (claims.Role == "") are not affected
// by this middleware — they keep today's behavior (gated only where
// RequireScope is explicitly applied, which today is nowhere).
func RequirePermission(perm auth.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFrom(r.Context())
			if claims == nil {
				apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
				return
			}
			if claims.Role == "" || claims.Role == auth.RoleAdmin {
				next.ServeHTTP(w, r)
				return
			}
			if !slices.Contains(claims.Permissions, string(perm)) {
				apipkg.WriteError(w, r, apipkg.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go test ./internal/server/middleware/... -v 2>&1`
Expected: PASS — all 5 new tests.

- [ ] **Step 5: Commit**

```bash
git add internal/server/middleware/auth.go internal/server/middleware/permission_test.go
git commit -m "feat(middleware): add RequirePermission for granular member access control"
```

---

### Task 6: Gate message routes by permission

**Files:**
- Modify: `internal/api/message/handler.go`

**Interfaces:**
- Consumes: `middleware.RequirePermission`, `auth.PermMessagesView`, `auth.PermMessagesSend`, `auth.PermMessagesSchedule` (Task 5/2).

- [ ] **Step 1: Update the `Routes` function**

Add imports `"velix/internal/domain/auth"` and `"velix/internal/server/middleware"` to `internal/api/message/handler.go`, then replace `Routes`:

```go
// Routes mounts all message sub-routes under /v1/instances/{instanceID}/messages.
func Routes(svc *message.Service) http.Handler {
	h := NewHandler(svc)
	r := chi.NewRouter()

	send := middleware.RequirePermission(auth.PermMessagesSend)
	view := middleware.RequirePermission(auth.PermMessagesView)
	schedule := middleware.RequirePermission(auth.PermMessagesSchedule)

	r.With(send).Post("/text", h.SendText)
	r.With(send).Post("/media", h.SendMedia)
	r.With(send).Post("/reaction", h.SendReaction)
	r.With(send).Post("/read", h.MarkAsRead)
	r.With(send).Post("/batch", h.BatchSend)
	r.With(send).Post("/location", h.SendLocation)
	r.With(send).Post("/poll", h.SendPoll)
	r.With(send).Post("/contact", h.SendContact)

	r.With(view).Get("/", h.ListByChat)
	r.With(schedule).Get("/scheduled", h.ListScheduled)
	r.With(view).Get("/search", h.SearchMessages)

	r.With(send).Delete("/{msgID}", h.RevokeMessage)
	r.With(schedule).Delete("/{msgID}/schedule", h.CancelScheduled)

	return r
}
```

Note: creating a scheduled message is still gated by `send` (not `schedule`) — scheduling is just the `scheduled_at` field on the same send endpoints, per the confirmed design decision. `schedule` only gates viewing/cancelling the scheduled queue.

- [ ] **Step 2: Build check**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go build ./internal/api/message/... 2>&1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/api/message/handler.go
git commit -m "feat(messages): gate message routes by messages:view/send/schedule permissions"
```

---

### Task 7: Gate contact/group routes, instances tree, media, and fix `/admin/stats`

**Files:**
- Modify: `internal/api/contact/handler.go`
- Modify: `internal/api/group/handler.go`
- Modify: `internal/server/router.go`

**Interfaces:**
- Consumes: `middleware.RequirePermission`, `auth.PermContactsManage`, `auth.PermInstancesManage`, `auth.PermMessagesSend`, `auth.PermMessagesView`, `auth.RoleAdmin`, `auth.RoleMember` (Task 2/5).

- [ ] **Step 1: Gate `contact.Routes`**

In `internal/api/contact/handler.go`, add imports `"velix/internal/domain/auth"` and `"velix/internal/server/middleware"`, then update `Routes`:

```go
func Routes(eng engine.Engine) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequirePermission(auth.PermContactsManage))
	r.Post("/check", checkHandler(eng))
	r.Get("/{jid}", infoHandler(eng))
	r.Get("/{jid}/picture", pictureHandler(eng))
	return r
}
```

- [ ] **Step 2: Gate `group.Routes`**

In `internal/api/group/handler.go`, same pattern:

```go
func Routes(eng engine.Engine) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequirePermission(auth.PermContactsManage))
	r.Get("/", listHandler(eng))
	r.Post("/", createHandler(eng))
	r.Get("/{groupID}", infoHandler(eng))
	r.Post("/{groupID}/participants", updateParticipantsHandler(eng))
	r.Delete("/{groupID}/leave", leaveHandler(eng))
	return r
}
```

- [ ] **Step 3: Fix `/admin/stats` for the retired `developer` role, and gate `/instances` + `/media`**

In `internal/server/router.go`, add the import `"velix/internal/domain/auth"` to the import block (alongside the other `velix/internal/...` imports).

Replace the `/admin/stats` line (preserves current behavior — every workspace member could already see stats, "developer" just no longer exists as a role name):

```go
			// Admin API — open to every authenticated workspace member (unchanged
			// behavior — the retired "developer" role used to cover this too).
			r.With(middleware.RequireRole(auth.RoleAdmin, auth.RoleMember)).
				Get("/admin/stats", adminapi.Stats(deps.InstanceService, deps.AuthService))
```

Replace the whole `/instances` route block:

```go
			// Instance management — single Route tree to avoid chi trie conflicts
			// that arise when r.Mount and r.Route share the same path prefix.
			instH := instanceapi.NewHandler(deps.InstanceService, deps.AuthService)
			manageInstances := middleware.RequirePermission(auth.PermInstancesManage)
			r.Route("/instances", func(r chi.Router) {
				r.With(manageInstances).Post("/", instH.Create)
				r.With(manageInstances).Get("/", instH.List)

				r.Route("/{instanceID}", func(r chi.Router) {
					// All instance routes require ownership — dual layer:
					// middleware verifies workspace + scope, service verifies again internally.
					r.Use(middleware.RequireInstanceOwner(deps.InstanceService))

					// Core CRUD + lifecycle — gated by instances:manage.
					r.Group(func(r chi.Router) {
						r.Use(manageInstances)
						r.Get("/", instH.Get)
						r.Delete("/", instH.Delete)
						r.Post("/connect", instH.Connect)
						r.Post("/disconnect", instH.Disconnect)
						r.Post("/logout", instH.Logout)
						r.Get("/status", instH.GetStatus)
						r.Get("/qr", instH.GetQR)
						r.Post("/pair-code", instH.PairCode)
						r.Mount("/settings", instanceapi.SettingsRoutes(deps.InstanceService))
						r.Post("/presence", instanceapi.PresenceHandler(deps.InstanceService))
						r.Patch("/profile", instanceapi.ProfileHandler(deps.InstanceService))

						// Chatwoot history sync.
						chatwootSync := chatwootapi.NewSyncHandler(deps.ChatwootService)
						r.Post("/chatwoot/sync", chatwootSync.Sync)
					})

					// Sub-resources — each enforces its own permission internally.
					r.Mount("/messages", messageapi.Routes(deps.MessageService))
					r.Mount("/contacts", contactapi.Routes(deps.Engine))
					r.Mount("/groups", groupapi.Routes(deps.Engine))
				})
			})
```

Gate media upload/download (leave `/audit-logs` untouched, per spec):

```go
			// Media upload and retrieval.
			mediaHandler := mediaapi.NewHandler(deps.MediaStorePath)
			r.With(middleware.RequirePermission(auth.PermMessagesSend)).Post("/media", mediaHandler.Upload)
			r.With(middleware.RequirePermission(auth.PermMessagesView)).Get("/media/{mediaID}", mediaHandler.Download)
```

- [ ] **Step 2: Build check**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go build ./... 2>&1`
Expected: PASS.

- [ ] **Step 3: Run the full test suite**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go test ./... 2>&1 | tail -40`
Expected: PASS (no regressions across the whole module).

- [ ] **Step 4: Commit**

```bash
git add internal/api/contact/handler.go internal/api/group/handler.go internal/server/router.go
git commit -m "feat(router): gate instances/contacts/groups/media routes by permission"
```

---

### Task 8: Admin-only `/v1/users` API

**Files:**
- Create: `internal/api/user/dto.go`
- Create: `internal/api/user/handler.go`
- Modify: `internal/server/router.go`

**Interfaces:**
- Consumes: `auth.Service.CreateUser/ListUsers/UpdateUser/DeleteUser`, `auth.ErrSelfLockout`, `auth.ErrUserNotFound`, `auth.ErrInvalidRole`, `auth.ErrWeakPassword` (Task 4), `middleware.RequireRole`, `middleware.ClaimsFrom` (existing).
- Produces: `userapi.Routes(svc *auth.Service) http.Handler`, mounted at `/v1/users`, admin-only.

- [ ] **Step 1: Write `dto.go`**

```go
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
```

- [ ] **Step 2: Write `handler.go`**

```go
package user

import (
	"errors"
	"net/http"
	"net/mail"

	"github.com/go-chi/chi/v5"

	apipkg "velix/internal/api"
	"velix/internal/domain/auth"
	"velix/internal/server/middleware"
)

// Handler holds the auth service dependency for workspace user management.
type Handler struct {
	svc *auth.Service
}

// NewHandler creates a new user-management HTTP handler.
func NewHandler(svc *auth.Service) *Handler {
	return &Handler{svc: svc}
}

// Routes mounts /v1/users. The caller (router.go) is responsible for wrapping
// this with middleware.RequireRole(auth.RoleAdmin) — every route here is
// admin-only, never toggleable via permissions.
func Routes(svc *auth.Service) http.Handler {
	h := NewHandler(svc)
	r := chi.NewRouter()
	r.Post("/", h.Create)
	r.Get("/", h.List)
	r.Patch("/{userID}", h.Update)
	r.Delete("/{userID}", h.Delete)
	return r
}

func parseRole(s string) (auth.Role, bool) {
	switch auth.Role(s) {
	case auth.RoleAdmin, auth.RoleMember:
		return auth.Role(s), true
	default:
		return "", false
	}
}

// Create handles POST /v1/users.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
		return
	}
	var req CreateUserRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{
		"email":    req.Email,
		"password": req.Password,
		"role":     req.Role,
	}) {
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "invalid email address"))
		return
	}
	role, ok := parseRole(req.Role)
	if !ok {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, `role must be "admin" or "member"`))
		return
	}

	created, err := h.svc.CreateUser(r.Context(), claims.WorkspaceID, req.Email, req.Password, role, req.Permissions)
	if err != nil {
		if errors.Is(err, auth.ErrWeakPassword) {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, err.Error()))
			return
		}
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeConflict, err.Error()))
		return
	}
	apipkg.WriteJSON(w, r, http.StatusCreated, userFromDomain(created))
}

// List handles GET /v1/users.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
		return
	}
	users, err := h.svc.ListUsers(r.Context(), claims.WorkspaceID)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "list users")
		return
	}
	out := make([]*UserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, userFromDomain(u))
	}
	apipkg.WriteJSON(w, r, http.StatusOK, out)
}

// Update handles PATCH /v1/users/{userID}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
		return
	}
	var req UpdateUserRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	role, ok := parseRole(req.Role)
	if !ok {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, `role must be "admin" or "member"`))
		return
	}

	updated, err := h.svc.UpdateUser(r.Context(), claims.WorkspaceID, apipkg.Param(r, "userID"), claims.UserID, role, req.Permissions, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrSelfLockout):
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeForbidden, err.Error()))
		case errors.Is(err, auth.ErrUserNotFound):
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeNotFound, err.Error()))
		case errors.Is(err, auth.ErrWeakPassword):
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, err.Error()))
		default:
			apipkg.LogAndFail(w, r, err, "update user")
		}
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, userFromDomain(updated))
}

// Delete handles DELETE /v1/users/{userID}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
		return
	}
	err := h.svc.DeleteUser(r.Context(), claims.WorkspaceID, apipkg.Param(r, "userID"), claims.UserID)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrSelfLockout):
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeForbidden, err.Error()))
		case errors.Is(err, auth.ErrUserNotFound):
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeNotFound, err.Error()))
		default:
			apipkg.LogAndFail(w, r, err, "delete user")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 3: Mount `/v1/users` in `router.go`**

Add the import `userapi "velix/internal/api/user"` to `internal/server/router.go`'s import block, and add this right after the `/instances` `r.Route` block closes (still inside the authenticated `r.Group`):

```go
			// User management — admin-only, never toggleable via permissions.
			r.Route("/users", func(r chi.Router) {
				r.Use(middleware.RequireRole(auth.RoleAdmin))
				r.Mount("/", userapi.Routes(deps.AuthService))
			})
```

- [ ] **Step 4: Build check**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go build ./... 2>&1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/user/dto.go internal/api/user/handler.go internal/server/router.go
git commit -m "feat(users): add admin-only /v1/users CRUD API"
```

---

### Task 9: Public registration-status endpoint

**Files:**
- Modify: `internal/api/auth/dto.go`
- Modify: `internal/api/auth/handler.go`

**Interfaces:**
- Consumes: `h.registrationEnabled`, `h.svc.HasAnyWorkspace` (existing).
- Produces: `GET /v1/auth/registration-status` → `{"success":true,"data":{"open":bool}}`. Task 11 (frontend `index.html`) calls this.

- [ ] **Step 1: Add the response type**

In `internal/api/auth/dto.go`, add:

```go
// RegistrationStatusResponse tells the login page whether registering a new
// workspace is currently allowed.
type RegistrationStatusResponse struct {
	Open bool `json:"open"`
}
```

- [ ] **Step 2: Add the handler and route**

In `internal/api/auth/handler.go`, add the route (public, no auth, right after `/register`/`/login`):

```go
	// Public — no token required, but rate-limited per IP.
	r.With(loginMW).Post("/register", h.Register)
	r.With(loginMW).Post("/login", h.Login)
	r.Get("/registration-status", h.RegistrationStatus)
```

Add the handler function after `Register`:

```go
// RegistrationStatus handles GET /v1/auth/registration-status — public, used
// by the login page to decide whether to show the "Criar conta" tab.
func (h *Handler) RegistrationStatus(w http.ResponseWriter, r *http.Request) {
	open := h.registrationEnabled
	if !open {
		exists, err := h.svc.HasAnyWorkspace(r.Context())
		open = err == nil && !exists
	}
	apipkg.WriteJSON(w, r, http.StatusOK, &RegistrationStatusResponse{Open: open})
}
```

- [ ] **Step 3: Build check**

Run: `cd "/home/paulolinder/Área de trabalho/Velix-Api" && go build ./... 2>&1`
Expected: PASS.

- [ ] **Step 4: Manual verification against the running stack**

Run:
```bash
docker compose --profile full up -d --build api
curl -sS http://localhost:8080/v1/auth/registration-status
```
Expected: `{"success":true,"data":{"open":false},...}` if a workspace already exists in your local DB (it does, from earlier testing), or `{"open":true}` on a fresh database.

- [ ] **Step 5: Commit**

```bash
git add internal/api/auth/dto.go internal/api/auth/handler.go
git commit -m "feat(auth): add public registration-status endpoint for the login page"
```

---

### Task 10: Frontend — hide "Criar conta" when registration is closed

**Files:**
- Modify: `internal/ui/web/admin/index.html`

**Interfaces:**
- Consumes: `GET /v1/auth/registration-status` (Task 9).
- Produces: `localStorage.wa_role`, read by Task 12's `isAdmin()` helper.

- [ ] **Step 1: Add `registrationOpen` state and fetch it on init**

In the `auth()` Alpine component, add `registrationOpen: false,` to the returned object (right after `loading: false, error: '',`), and change `init()`:

```js
      init() {
        if (localStorage.getItem('wa_token')) {
          window.location.href = '/admin/dashboard.html';
          return;
        }
        this.checkRegistrationStatus();
      },

      async checkRegistrationStatus() {
        try {
          const r = await fetch('/v1/auth/registration-status');
          const d = await r.json();
          this.registrationOpen = !!(d.success && d.data && d.data.open);
        } catch (e) {
          this.registrationOpen = false;
        }
      },
```

- [ ] **Step 2: Hide the tab button and the register panel when closed**

Change the "Criar conta" tab button to add `x-show="registrationOpen"`:

```html
        <button x-show="registrationOpen" @click="tab='register'; error=''"
                :class="tab==='register' ? 'text-white border-b-2 border-green-500' : 'text-gray-500 hover:text-gray-300'"
                class="flex-1 px-6 py-4 text-sm font-medium transition">Criar conta</button>
```

Change the register panel's `x-show`:

```html
      <div x-show="tab==='register' && registrationOpen" class="p-6">
```

- [ ] **Step 3: Store the role on successful login/register (needed by the sidebar/users.html later)**

In `login()`, after `localStorage.setItem('wa_email', ...)`:

```js
          localStorage.setItem('wa_token', d.data.token);
          localStorage.setItem('wa_email', d.data.user?.email || this.loginEmail);
          localStorage.setItem('wa_role', d.data.user?.role || '');
          window.location.href = '/admin/dashboard.html';
```

In `register()`, same addition:

```js
          localStorage.setItem('wa_token', d.data.token);
          localStorage.setItem('wa_email', d.data.user?.email || this.regEmail);
          localStorage.setItem('wa_role', d.data.user?.role || '');
          window.location.href = '/admin/dashboard.html';
```

- [ ] **Step 4: Manual verification in a browser**

With the local stack already running (`docker compose --profile full up -d`) and a workspace already registered from earlier testing:
1. Open `http://localhost:8080/admin` in a browser (or `curl -s http://localhost:8080/admin | grep -c "Criar conta"` to confirm the markup is still present — it's hidden via `x-show`, not removed, so this grep will still find it; the real check is visual/JS).
2. Confirm only the "Entrar" tab is visible (no "Criar conta" tab).
3. Log in with the existing admin credentials, confirm `localStorage.wa_role === "admin"` (check via browser devtools console: `localStorage.getItem('wa_role')`).

- [ ] **Step 5: Commit**

```bash
git add internal/ui/web/admin/index.html
git commit -m "feat(admin-ui): hide Criar conta tab once registration is closed"
```

---

### Task 11: Frontend — `isAdmin()` helper + sidebar "Usuários" link

**Files:**
- Modify: `internal/ui/web/admin/shared.js`
- Modify: `internal/ui/web/admin/dashboard.html`
- Modify: `internal/ui/web/admin/instances.html`
- Modify: `internal/ui/web/admin/messages.html`
- Modify: `internal/ui/web/admin/scheduled.html`
- Modify: `internal/ui/web/admin/usage.html`
- Modify: `internal/ui/web/admin/guide.html`
- Modify: `internal/ui/web/admin/api-keys.html`

**Interfaces:**
- Consumes: `localStorage.wa_role` (Task 10).
- Produces: `isAdmin()` global JS function, used by Task 12's `users.html` too.

- [ ] **Step 1: Add `isAdmin()` to `shared.js`**

Add this function anywhere in `internal/ui/web/admin/shared.js` (e.g. right after `getUserEmail()`):

```js
function isAdmin() {
  return localStorage.getItem('wa_role') === 'admin';
}
```

- [ ] **Step 2: Add the sidebar link to each of the 7 pages**

In each of `dashboard.html`, `instances.html`, `messages.html`, `scheduled.html`, `usage.html`, `guide.html`, `api-keys.html`, find this exact block (identical in all 7 — verified present at line 43 in every file):

```html
      <a href="/admin/usage.html" class="nav-item">
        <svg class="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"/></svg>
        Uso
      </a>
```

(In `usage.html` specifically, this block instead reads `class="nav-item active"` — use that exact variant as the anchor for that one file.)

Insert this new link immediately after that block (before the `<div class="border-t border-[#1f2937] my-2"></div>` divider that follows it):

```html
      <a href="/admin/users.html" x-show="isAdmin()" class="nav-item">
        <svg class="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M17 20h5v-2a4 4 0 00-3-3.87M9 20H4v-2a4 4 0 013-3.87m6-1.13a4 4 0 100-8 4 4 0 000 8zm6 3.13a4 4 0 010 7.75"/></svg>
        Usuários
      </a>
```

Apply this same insertion to all 7 files (in `usage.html`, its own "Uso" link keeps `class="nav-item active"` unchanged — only the new "Usuários" link is inserted after it).

- [ ] **Step 3: Manual verification**

Run:
```bash
for f in dashboard instances messages scheduled usage guide api-keys; do
  grep -c "users.html" "internal/ui/web/admin/$f.html"
done
```
Expected: `1` printed 7 times (one "Usuários" link added per file).

- [ ] **Step 4: Commit**

```bash
git add internal/ui/web/admin/shared.js internal/ui/web/admin/dashboard.html internal/ui/web/admin/instances.html internal/ui/web/admin/messages.html internal/ui/web/admin/scheduled.html internal/ui/web/admin/usage.html internal/ui/web/admin/guide.html internal/ui/web/admin/api-keys.html
git commit -m "feat(admin-ui): add admin-only Usuários link to the sidebar"
```

---

### Task 12: Frontend — `users.html` page

**Files:**
- Create: `internal/ui/web/admin/users.html`

**Interfaces:**
- Consumes: `GET/POST /v1/users`, `PATCH/DELETE /v1/users/{id}` (Task 8); `apiCall`/`apiDelete`/`requireAuth`/`getUserEmail`/`getEmailInitial`/`doLogout`/`fmtDate`/`isAdmin` (shared.js, Task 11).

- [ ] **Step 1: Write the page**

Create `internal/ui/web/admin/users.html`, following the exact sidebar structure of `api-keys.html` (same nav, same "Usuários" link now present and active on this page) plus a list + create/edit modal:

```html
<!DOCTYPE html>
<html lang="pt-BR">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <link rel="icon" type="image/svg+xml" href="/admin/dist/favicon.svg">
  <title>Velix API — Usuários</title>
  <link rel="stylesheet" href="/admin/dist/main.css">
  <script defer src="/admin/dist/alpine.min.js"></script>
  <script src="/admin/shared.js"></script>
</head>
<body class="bg-[#0d1117] text-gray-100" x-data="page()" x-init="init()" x-cloak>
<div class="flex h-screen overflow-hidden">

  <!-- Sidebar -->
  <aside class="w-56 bg-[#0d1117] border-r border-[#1f2937] flex flex-col flex-shrink-0">
    <div class="px-4 py-5 border-b border-[#1f2937]">
      <div class="flex items-center gap-2.5">
        <img src="/admin/dist/velix-logo.svg" alt="Velix" class="w-8 h-8 rounded-lg shadow-lg shadow-green-900/50">
        <div>
          <p class="font-semibold text-white text-sm leading-tight">Velix API</p>
          <p class="text-[10px] text-gray-500">Admin</p>
        </div>
      </div>
    </div>
    <nav class="flex-1 p-2.5 space-y-0.5">
      <a href="/admin/dashboard.html" class="nav-item">
        <svg class="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>
        Painel
      </a>
      <a href="/admin/instances.html" class="nav-item">
        <svg class="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M12 18h.01M8 21h8a2 2 0 002-2V5a2 2 0 00-2-2H8a2 2 0 00-2 2v14a2 2 0 002 2z"/></svg>
        Instâncias
      </a>
      <a href="/admin/messages.html" class="nav-item">
        <svg class="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z"/></svg>
        Mensagens
      </a>
      <a href="/admin/scheduled.html" class="nav-item">
        <svg class="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>
        Agendamentos
      </a>
      <a href="/admin/usage.html" class="nav-item">
        <svg class="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"/></svg>
        Uso
      </a>
      <a href="/admin/users.html" x-show="isAdmin()" class="nav-item active">
        <svg class="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M17 20h5v-2a4 4 0 00-3-3.87M9 20H4v-2a4 4 0 013-3.87m6-1.13a4 4 0 100-8 4 4 0 000 8zm6 3.13a4 4 0 010 7.75"/></svg>
        Usuários
      </a>
      <div class="border-t border-[#1f2937] my-2"></div>
      <a href="/admin/guide.html" class="nav-item">
        <svg class="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253"/></svg>
        Guia de Uso
      </a>
      <a href="/docs" target="_blank" class="nav-item">
        <svg class="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75"><path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"/></svg>
        Docs da API
        <svg class="w-3 h-3 ml-auto opacity-40" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14"/></svg>
      </a>
    </nav>
    <div class="p-3 border-t border-[#1f2937]">
      <div class="flex items-center gap-2.5 px-2 py-2 rounded-lg">
        <div class="w-7 h-7 rounded-full bg-gradient-to-br from-green-500 to-emerald-700 flex items-center justify-center text-xs font-bold text-white flex-shrink-0" x-text="getEmailInitial()"></div>
        <p class="text-xs text-gray-400 truncate flex-1" x-text="email"></p>
        <button @click="doLogout()" class="text-gray-600 hover:text-red-400 transition" title="Sair">
          <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/></svg>
        </button>
      </div>
    </div>
  </aside>

  <!-- Main -->
  <main class="flex-1 overflow-auto">
    <div class="max-w-4xl mx-auto p-6">

      <!-- Header -->
      <div class="flex items-center justify-between mb-7">
        <div>
          <h1 class="text-xl font-semibold text-white">Usuários</h1>
          <p class="text-gray-500 text-sm mt-0.5">Quem tem acesso a este workspace</p>
        </div>
        <button @click="openCreate()" class="btn btn-primary">
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4"/></svg>
          Novo Usuário
        </button>
      </div>

      <!-- Error -->
      <div x-show="error" x-transition class="mb-5 flex items-start gap-3 bg-red-950/60 border border-red-900/60 rounded-xl p-4 text-red-400 text-sm">
        <svg class="w-4 h-4 mt-0.5 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>
        <span x-text="error"></span>
        <button @click="error=''" class="ml-auto text-red-600 hover:text-red-400">
          <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12"/></svg>
        </button>
      </div>

      <!-- Table -->
      <div x-show="!loading && users.length > 0" class="bg-[#161b22] border border-[#1f2937] rounded-2xl overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-[#1f2937]">
              <th class="text-left px-5 py-3 text-xs font-semibold text-gray-500 uppercase tracking-wider">E-mail</th>
              <th class="text-left px-5 py-3 text-xs font-semibold text-gray-500 uppercase tracking-wider">Papel</th>
              <th class="text-left px-5 py-3 text-xs font-semibold text-gray-500 uppercase tracking-wider">Permissões</th>
              <th class="text-left px-5 py-3 text-xs font-semibold text-gray-500 uppercase tracking-wider">Último Login</th>
              <th class="text-right px-5 py-3 text-xs font-semibold text-gray-500 uppercase tracking-wider">Ação</th>
            </tr>
          </thead>
          <tbody>
            <template x-for="u in users" :key="u.id">
              <tr class="border-b border-[#1f2937] last:border-0 hover:bg-white/[.02] transition">
                <td class="px-5 py-4 font-medium text-white" x-text="u.email"></td>
                <td class="px-5 py-4">
                  <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium"
                        :class="u.role === 'admin' ? 'bg-green-900/40 text-green-400' : 'bg-gray-800 text-gray-400'"
                        x-text="u.role === 'admin' ? 'admin' : 'membro'"></span>
                </td>
                <td class="px-5 py-4 text-gray-500 text-xs" x-text="u.role === 'admin' ? 'Acesso total' : (u.permissions.length ? u.permissions.join(', ') : 'Nenhuma')"></td>
                <td class="px-5 py-4 text-gray-500 text-xs" x-text="fmtDate(u.last_login_at)"></td>
                <td class="px-5 py-4">
                  <div class="flex justify-end gap-1">
                    <button @click="openEdit(u)" class="px-3 py-1.5 text-xs font-medium text-gray-500 hover:text-white border border-transparent hover:border-[#2a3441] rounded-lg transition">
                      Editar
                    </button>
                    <button @click="removeUser(u)" x-show="u.email !== email"
                            class="px-3 py-1.5 text-xs font-medium bg-transparent hover:bg-red-950/60 text-gray-500 hover:text-red-400 border border-transparent hover:border-red-900/50 rounded-lg transition">
                      Remover
                    </button>
                  </div>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>

      <!-- Empty state -->
      <div x-show="!loading && users.length === 0" class="flex flex-col items-center justify-center py-24 text-center">
        <p class="text-white font-medium mb-1">Nenhum usuário ainda</p>
        <p class="text-gray-500 text-sm">Isso não deveria acontecer — você está logado.</p>
      </div>

    </div>
  </main>
</div>

<!-- Create/Edit Modal -->
<div x-show="modalOpen" x-transition:enter="transition ease-out duration-200"
     x-transition:enter-start="opacity-0" x-transition:enter-end="opacity-100"
     x-transition:leave="transition ease-in duration-150"
     x-transition:leave-start="opacity-100" x-transition:leave-end="opacity-0"
     class="fixed inset-0 bg-black/70 backdrop-blur-sm flex items-center justify-center z-50 p-4"
     @keydown.escape.window="modalOpen=false">
  <div class="bg-[#161b22] border border-[#1f2937] rounded-2xl w-full max-w-md shadow-2xl"
       x-transition:enter="transition ease-out duration-200"
       x-transition:enter-start="opacity-0 scale-95" x-transition:enter-end="opacity-100 scale-100"
       @click.stop>

    <div class="flex items-center justify-between px-6 py-4 border-b border-[#1f2937]">
      <h2 class="text-base font-semibold text-white" x-text="editingUser ? 'Editar Usuário' : 'Novo Usuário'"></h2>
      <button @click="modalOpen=false" class="text-gray-600 hover:text-gray-400 transition">
        <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12"/></svg>
      </button>
    </div>

    <div class="p-6">
      <form @submit.prevent="save">
        <div class="mb-4" x-show="!editingUser">
          <label class="block text-xs font-medium text-gray-400 mb-1.5">E-mail</label>
          <input x-model="form.email" type="email" class="field" placeholder="usuario@empresa.com">
        </div>
        <div class="mb-4">
          <label class="block text-xs font-medium text-gray-400 mb-1.5" x-text="editingUser ? 'Nova senha (opcional)' : 'Senha'"></label>
          <input x-model="form.password" type="password" class="field" placeholder="••••••••">
        </div>
        <div class="mb-4">
          <label class="block text-xs font-medium text-gray-400 mb-1.5">Papel</label>
          <select x-model="form.role" class="field">
            <option value="member">Membro</option>
            <option value="admin">Admin</option>
          </select>
        </div>
        <div class="mb-5" x-show="form.role === 'member'">
          <label class="block text-xs font-medium text-gray-400 mb-2">Permissões</label>
          <div class="space-y-2">
            <label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
              <input type="checkbox" value="messages:view" x-model="form.permissions" class="w-3.5 h-3.5 rounded accent-green-500">
              Visualizar mensagens
            </label>
            <label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
              <input type="checkbox" value="messages:send" x-model="form.permissions" class="w-3.5 h-3.5 rounded accent-green-500">
              Enviar/responder mensagens
            </label>
            <label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
              <input type="checkbox" value="messages:schedule" x-model="form.permissions" class="w-3.5 h-3.5 rounded accent-green-500">
              Agendar mensagens
            </label>
            <label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
              <input type="checkbox" value="instances:manage" x-model="form.permissions" class="w-3.5 h-3.5 rounded accent-green-500">
              Gerenciar instâncias
            </label>
            <label class="flex items-center gap-2 text-sm text-gray-300 cursor-pointer">
              <input type="checkbox" value="contacts:manage" x-model="form.permissions" class="w-3.5 h-3.5 rounded accent-green-500">
              Gerenciar contatos e grupos
            </label>
          </div>
        </div>

        <div x-show="formError" class="mb-4 p-3 bg-red-950/60 border border-red-900/40 rounded-lg text-red-400 text-xs" x-text="formError"></div>

        <div class="flex gap-3">
          <button type="button" @click="modalOpen=false" class="btn btn-secondary flex-1">Cancelar</button>
          <button type="submit" :disabled="formLoading" class="btn btn-primary flex-1">
            <span x-show="!formLoading" x-text="editingUser ? 'Salvar' : 'Criar Usuário'"></span>
            <span x-show="formLoading">Salvando…</span>
          </button>
        </div>
      </form>
    </div>
  </div>
</div>

<script>
function page() {
  return {
    email: getUserEmail(),
    users: [],
    loading: false,
    error: '',
    modalOpen: false,
    editingUser: null,
    form: { email: '', password: '', role: 'member', permissions: [] },
    formLoading: false,
    formError: '',

    init() {
      if (!requireAuth()) return;
      if (!isAdmin()) { window.location.href = '/admin/dashboard.html'; return; }
      this.load();
    },

    async load() {
      this.loading = true;
      try {
        const d = await apiCall('GET', '/users');
        this.users = d || [];
      } catch (e) {
        this.error = e.message;
      } finally {
        this.loading = false;
      }
    },

    openCreate() {
      this.editingUser = null;
      this.form = { email: '', password: '', role: 'member', permissions: [] };
      this.formError = '';
      this.modalOpen = true;
    },

    openEdit(u) {
      this.editingUser = u;
      this.form = { email: u.email, password: '', role: u.role, permissions: [...u.permissions] };
      this.formError = '';
      this.modalOpen = true;
    },

    async save() {
      this.formLoading = true;
      this.formError = '';
      try {
        if (this.editingUser) {
          const body = { role: this.form.role, permissions: this.form.permissions };
          if (this.form.password) body.password = this.form.password;
          await apiCall('PATCH', '/users/' + this.editingUser.id, body);
        } else {
          await apiCall('POST', '/users', {
            email: this.form.email,
            password: this.form.password,
            role: this.form.role,
            permissions: this.form.permissions,
          });
        }
        this.modalOpen = false;
        await this.load();
      } catch (e) {
        this.formError = e.message;
      } finally {
        this.formLoading = false;
      }
    },

    async removeUser(u) {
      if (!confirm(`Remover "${u.email}"? Esta ação não pode ser desfeita.`)) return;
      try {
        await apiDelete('/users/' + u.id);
        await this.load();
      } catch (e) {
        this.error = e.message;
      }
    }
  };
}
</script>
</body>
</html>
```

- [ ] **Step 2: Manual verification against the running stack**

```bash
docker compose --profile full up -d --build api
```
Then in a browser: log into `http://localhost:8080/admin` with the existing admin account, click "Usuários" in the sidebar, create a member user with only "Visualizar mensagens" checked, confirm it appears in the table with `Nenhuma` replaced by `messages:view` in the Permissões column, then edit it to add "Enviar/responder mensagens" and confirm the table updates.

- [ ] **Step 3: Commit**

```bash
git add internal/ui/web/admin/users.html
git commit -m "feat(admin-ui): add Usuários page for creating/editing workspace users"
```

---

### Task 13: End-to-end verification

**Files:** none (verification only).

- [ ] **Step 1: Rebuild and restart the full stack**

```bash
cd "/home/paulolinder/Área de trabalho/Velix-Api"
docker compose --profile full up -d --build
sleep 3
curl -sS http://localhost:8080/health
```
Expected: `{"status":"ok",...}`.

- [ ] **Step 2: Confirm registration is closed (workspace already exists from earlier testing)**

```bash
curl -sS http://localhost:8080/v1/auth/registration-status
```
Expected: `{"success":true,"data":{"open":false},...}`.

- [ ] **Step 3: Log in as the existing admin and create a restricted member**

```bash
ADMIN_TOKEN=$(curl -sS -X POST http://localhost:8080/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@meudominio.com","password":"SenhaForte123!"}' | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['token'])")

curl -sS -X POST http://localhost:8080/v1/users \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"email":"viewer@test.com","password":"ViewerPass1","role":"member","permissions":["messages:view"]}'
```
Expected: `201` with a user object showing `"role":"member","permissions":["messages:view"]`.

(If the admin credentials from the earlier session aren't `admin@meudominio.com` / `SenhaForte123!`, use whichever admin account already exists — check with `docker compose exec postgres psql -U velix -d velix -c "SELECT email, role FROM users;"`.)

- [ ] **Step 4: Confirm the restricted member can view but not send**

```bash
MEMBER_TOKEN=$(curl -sS -X POST http://localhost:8080/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"viewer@test.com","password":"ViewerPass1"}' | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['token'])")

INSTANCE_ID=$(curl -sS http://localhost:8080/v1/instances -H "Authorization: Bearer $ADMIN_TOKEN" | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print(d[0]['id'] if d else '')")

if [ -n "$INSTANCE_ID" ]; then
  echo "--- view (expect 200 or empty list, not 403) ---"
  curl -sS -o /dev/null -w "%{http_code}\n" "http://localhost:8080/v1/instances/$INSTANCE_ID/messages" -H "Authorization: Bearer $MEMBER_TOKEN"
  echo "--- send (expect 403) ---"
  curl -sS -o /dev/null -w "%{http_code}\n" -X POST "http://localhost:8080/v1/instances/$INSTANCE_ID/messages/text" \
    -H "Authorization: Bearer $MEMBER_TOKEN" -H "Content-Type: application/json" \
    -d '{"to":"5511999999999","text":"hi"}'
  echo "--- manage instance, e.g. disconnect (expect 403) ---"
  curl -sS -o /dev/null -w "%{http_code}\n" -X POST "http://localhost:8080/v1/instances/$INSTANCE_ID/disconnect" -H "Authorization: Bearer $MEMBER_TOKEN"
else
  echo "No instance exists yet in this workspace — create one as admin first, then re-run this step."
fi
```
Expected: view → `200`; send → `403`; disconnect (instances:manage) → `403`.

- [ ] **Step 5: Confirm self-lockout guards work**

```bash
ADMIN_ID=$(curl -sS http://localhost:8080/v1/auth/me -H "Authorization: Bearer $ADMIN_TOKEN" | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['uid'])")
echo "--- admin demoting self (expect 403) ---"
curl -sS -o /dev/null -w "%{http_code}\n" -X PATCH "http://localhost:8080/v1/users/$ADMIN_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"role":"member","permissions":[]}'
echo "--- admin deleting self (expect 403) ---"
curl -sS -o /dev/null -w "%{http_code}\n" -X DELETE "http://localhost:8080/v1/users/$ADMIN_ID" -H "Authorization: Bearer $ADMIN_TOKEN"
```
Expected: both `403`.

- [ ] **Step 6: Run the full automated test suite one last time**

```bash
cd "/home/paulolinder/Área de trabalho/Velix-Api" && go test ./... 2>&1 | tail -40
```
Expected: PASS, no failures.

- [ ] **Step 7: Clean up the test member user (optional)**

```bash
MEMBER_ID=$(curl -sS http://localhost:8080/v1/users -H "Authorization: Bearer $ADMIN_TOKEN" | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print([u['id'] for u in d if u['email']=='viewer@test.com'][0])")
curl -sS -o /dev/null -w "%{http_code}\n" -X DELETE "http://localhost:8080/v1/users/$MEMBER_ID" -H "Authorization: Bearer $ADMIN_TOKEN"
```
Expected: `204`.

(No commit — this task only verifies Tasks 1-12 together.)
