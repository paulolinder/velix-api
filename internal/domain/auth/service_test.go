package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockRepo implements Repository for testing.
type mockRepo struct {
	users      map[string]*User
	workspaces map[string]*Workspace
	apiKeys    map[string]*APIKey
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		users:      make(map[string]*User),
		workspaces: make(map[string]*Workspace),
		apiKeys:    make(map[string]*APIKey),
	}
}

func (m *mockRepo) CreateWorkspace(_ context.Context, ws *Workspace) (*Workspace, error) {
	ws.ID = "ws-" + ws.Slug
	ws.CreatedAt = time.Now()
	m.workspaces[ws.ID] = ws
	return ws, nil
}
func (m *mockRepo) GetWorkspaceByID(_ context.Context, id string) (*Workspace, error) {
	if ws, ok := m.workspaces[id]; ok {
		return ws, nil
	}
	return nil, ErrInvalidCredentials
}
func (m *mockRepo) GetWorkspaceBySlug(_ context.Context, slug string) (*Workspace, error) {
	for _, ws := range m.workspaces {
		if ws.Slug == slug {
			return ws, nil
		}
	}
	return nil, ErrInvalidCredentials
}
func (m *mockRepo) CreateUser(_ context.Context, u *User) (*User, error) {
	u.ID = "user-" + u.Email
	u.CreatedAt = time.Now()
	m.users[u.Email] = u
	return u, nil
}
func (m *mockRepo) GetUserByEmail(_ context.Context, email string) (*User, error) {
	if u, ok := m.users[email]; ok {
		return u, nil
	}
	return nil, ErrInvalidCredentials
}
func (m *mockRepo) GetUserByID(_ context.Context, id string) (*User, error) {
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, ErrInvalidCredentials
}
func (m *mockRepo) UpdateLastLogin(_ context.Context, _ string) error { return nil }
func (m *mockRepo) CreateAPIKey(_ context.Context, k *APIKey) (*APIKey, error) {
	k.ID = "key-" + k.KeyPrefix
	k.CreatedAt = time.Now()
	m.apiKeys[k.ID] = k
	return k, nil
}
func (m *mockRepo) ListAPIKeys(_ context.Context, _ string) ([]*APIKey, error) {
	var keys []*APIKey
	for _, k := range m.apiKeys {
		keys = append(keys, k)
	}
	return keys, nil
}
func (m *mockRepo) GetAPIKeyByPrefix(_ context.Context, prefix string) (*APIKey, error) {
	for _, k := range m.apiKeys {
		if k.KeyPrefix == prefix {
			return k, nil
		}
	}
	return nil, ErrInvalidAPIKey
}
func (m *mockRepo) RevokeAPIKey(_ context.Context, keyID, _ string) error {
	if k, ok := m.apiKeys[keyID]; ok {
		now := time.Now()
		k.RevokedAt = &now
		return nil
	}
	return ErrInvalidAPIKey
}
func (m *mockRepo) RevokeAPIKeysByUser(_ context.Context, userID string) error {
	for _, k := range m.apiKeys {
		if k.UserID == userID {
			now := time.Now()
			k.RevokedAt = &now
		}
	}
	return nil
}
func (m *mockRepo) TouchAPIKey(_ context.Context, _ string) error { return nil }
func (m *mockRepo) HasAnyWorkspace(_ context.Context) (bool, error) {
	return len(m.workspaces) > 0, nil
}
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

// --- Tests ---

func TestRegister(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	ws, user, token, err := svc.Register(context.Background(), "Test WS", "test-ws", "user@test.com", "Password1", true)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if ws.Name != "Test WS" {
		t.Errorf("workspace name = %q, want %q", ws.Name, "Test WS")
	}
	if user.Email != "user@test.com" {
		t.Errorf("user email = %q, want %q", user.Email, "user@test.com")
	}
	if user.Role != RoleAdmin {
		t.Errorf("role = %q, want %q", user.Role, RoleAdmin)
	}
	if token == "" {
		t.Error("token is empty")
	}
}

func TestRegister_WeakPassword(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	_, _, _, err := svc.Register(context.Background(), "WS", "ws", "u@t.com", "weak", true)
	if err == nil {
		t.Fatal("expected error for weak password")
	}
}

func TestRegister_PasswordComplexity(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	cases := []struct {
		password string
		wantErr  bool
	}{
		{"Password1", false},       // valid: upper + lower + digit
		{"SenhaForte123!", false},  // valid
		{"alllowercase1", true},    // missing uppercase
		{"ALLUPPERCASE1", true},    // missing lowercase
		{"NoDigitsHere", true},     // missing digit
		{"Short1", true},           // too short
		{"12345678", true},         // only digits
	}

	for _, c := range cases {
		_, _, _, err := svc.Register(context.Background(), "WS", "ws", "u@t.com", c.password, true)
		if c.wantErr && err == nil {
			t.Errorf("password %q: expected error but got none", c.password)
		}
		if !c.wantErr && err != nil {
			t.Errorf("password %q: unexpected error: %v", c.password, err)
		}
	}
}

func TestLogin(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	_, _, _, err := svc.Register(context.Background(), "WS", "ws", "login@test.com", "Password1", true)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	user, token, err := svc.Login(context.Background(), "login@test.com", "Password1")
	if err != nil {
		t.Fatalf("Login failed: %v", err)
	}
	if user.Email != "login@test.com" {
		t.Errorf("email = %q", user.Email)
	}
	if token == "" {
		t.Error("token is empty")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	svc.Register(context.Background(), "WS", "ws", "u@t.com", "Password1", true)

	_, _, err := svc.Login(context.Background(), "u@t.com", "WrongPass1")
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestLogin_NonexistentUser(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	_, _, err := svc.Login(context.Background(), "nobody@test.com", "Password1")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
}

func TestParseToken_Valid(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	svc.Register(context.Background(), "WS", "ws", "jwt@test.com", "Password1", true)
	_, token, _ := svc.Login(context.Background(), "jwt@test.com", "Password1")

	claims, err := svc.ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken failed: %v", err)
	}
	if claims.Email != "jwt@test.com" {
		t.Errorf("email = %q", claims.Email)
	}
	if claims.Role != RoleAdmin {
		t.Errorf("role = %q", claims.Role)
	}
	if claims.WorkspaceID == "" {
		t.Error("workspace ID is empty")
	}
}

func TestParseToken_Invalid(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	_, err := svc.ParseToken("invalid.token.here")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestParseToken_WrongSecret(t *testing.T) {
	svc1 := NewService(newMockRepo(), "secret-one-that-is-32-chars-long!", 24*time.Hour)
	svc2 := NewService(newMockRepo(), "secret-two-that-is-32-chars-long!", 24*time.Hour)

	repo := newMockRepo()
	svc1.repo = repo
	svc1.Register(context.Background(), "WS", "ws", "cross@test.com", "Password1", true)
	_, token, _ := svc1.Login(context.Background(), "cross@test.com", "Password1")

	_, err := svc2.ParseToken(token)
	if err == nil {
		t.Fatal("expected error for token signed with different secret")
	}
}

func TestAPIKey_CreateAndValidate(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	svc.Register(context.Background(), "WS", "ws", "key@test.com", "Password1", true)

	key, raw, err := svc.CreateAPIKey(context.Background(), "ws-ws", "user-key@test.com", "test-key", nil, []string{"*"})
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}
	if key.Name != "test-key" {
		t.Errorf("name = %q", key.Name)
	}
	if raw == "" {
		t.Error("raw key is empty")
	}

	// Verify the raw key is returned and has correct format.
	if len(raw) < 20 {
		t.Errorf("raw key too short: %d chars", len(raw))
	}
	if key.KeyPrefix == "" {
		t.Error("key prefix is empty")
	}
	if key.WorkspaceID != "ws-ws" {
		t.Errorf("workspace = %q, want ws-ws", key.WorkspaceID)
	}
	if len(key.Scopes) == 0 || key.Scopes[0] != "*" {
		t.Errorf("scopes = %v, want [*]", key.Scopes)
	}
}

func TestAPIKey_ValidateInvalid(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	_, err := svc.ValidateAPIKey(context.Background(), "wapi_invalidkeyhere123456789012345678901234567890")
	if err == nil {
		t.Fatal("expected error for invalid API key")
	}
}

func TestAPIKey_ValidateRevoked(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)

	svc.Register(context.Background(), "WS", "ws", "rev@test.com", "Password1", true)

	key, raw, _ := svc.CreateAPIKey(context.Background(), "ws-ws", "user-rev@test.com", "revokable", nil, []string{"*"})

	// Revoke.
	svc.repo.RevokeAPIKey(context.Background(), key.ID, "ws-ws")

	// Try to validate — should fail.
	_, err := svc.ValidateAPIKey(context.Background(), raw)
	if err == nil {
		t.Fatal("expected error for revoked API key")
	}
}

func TestCreateUser_Member(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, _, _, err := svc.Register(context.Background(), "WS", "ws", "admin@test.com", "Password1", true)
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
	_, _, _, _ = svc.Register(context.Background(), "WS", "ws", "a@test.com", "Password1", true)
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
	_, admin, _, _ := svc.Register(context.Background(), "WS", "ws", "admin3@test.com", "Password1", true)
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
	_, admin, _, _ := svc.Register(context.Background(), "WS", "ws", "admin4@test.com", "Password1", true)

	_, err := svc.UpdateUser(context.Background(), "ws-ws", admin.ID, admin.ID, RoleMember, nil, "")
	if !errors.Is(err, ErrSelfLockout) {
		t.Fatalf("expected ErrSelfLockout, got %v", err)
	}
}

func TestDeleteUser_SelfDeleteBlocked(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, admin, _, _ := svc.Register(context.Background(), "WS", "ws", "admin5@test.com", "Password1", true)

	err := svc.DeleteUser(context.Background(), "ws-ws", admin.ID, admin.ID)
	if !errors.Is(err, ErrSelfLockout) {
		t.Fatalf("expected ErrSelfLockout, got %v", err)
	}
}

func TestDeleteUser_RemovesOtherUser(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, admin, _, _ := svc.Register(context.Background(), "WS", "ws", "admin6@test.com", "Password1", true)
	member, _ := svc.CreateUser(context.Background(), "ws-ws", "member6@test.com", "Password1", RoleMember, nil)

	if err := svc.DeleteUser(context.Background(), "ws-ws", member.ID, admin.ID); err != nil {
		t.Fatalf("DeleteUser failed: %v", err)
	}
	if _, err := svc.repo.GetUserByID(context.Background(), member.ID); err == nil {
		t.Error("expected user to be gone after DeleteUser")
	}
}

func TestDeleteUser_RevokesAPIKeys(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, admin, _, _ := svc.Register(context.Background(), "WS", "ws", "admin8@test.com", "Password1", true)
	member, _ := svc.CreateUser(context.Background(), "ws-ws", "member8@test.com", "Password1", RoleMember, nil)

	key, _, err := svc.CreateAPIKey(context.Background(), "ws-ws", member.ID, "member-key", nil, []string{"*"})
	if err != nil {
		t.Fatalf("CreateAPIKey failed: %v", err)
	}

	if err := svc.DeleteUser(context.Background(), "ws-ws", member.ID, admin.ID); err != nil {
		t.Fatalf("DeleteUser failed: %v", err)
	}

	got, err := svc.repo.GetAPIKeyByPrefix(context.Background(), key.KeyPrefix)
	if err != nil {
		t.Fatalf("GetAPIKeyByPrefix failed: %v", err)
	}
	if got.RevokedAt == nil {
		t.Error("expected API key to be revoked after its owning user was deleted")
	}
}

func TestParseToken_CarriesPermissions(t *testing.T) {
	svc := NewService(newMockRepo(), "test-secret-that-is-32-chars-long!", 24*time.Hour)
	_, admin, _, _ := svc.Register(context.Background(), "WS", "ws", "admin7@test.com", "Password1", true)
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
