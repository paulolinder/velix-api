package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"golang.org/x/crypto/bcrypt"

	"velix/internal/logger"
)

const (
	bcryptCost          = 12
	apiKeyLength        = 32 // bytes → 64 hex chars
	keyPrefix           = "wapi_"
	loginMaxAttempts    = 10
	loginLockDuration   = 15 * time.Minute
)

// Service handles workspace registration, authentication, and API key management.
type Service struct {
	repo      Repository
	rdb       *redis.Client // for JWT blacklist (logout)
	jwtSecret []byte
	jwtExpiry time.Duration
	log       zerolog.Logger
}

// NewService creates a new auth service.
func NewService(repo Repository, jwtSecret string, jwtExpiry time.Duration, rdb ...*redis.Client) *Service {
	s := &Service{
		repo:      repo,
		jwtSecret: []byte(jwtSecret),
		jwtExpiry: jwtExpiry,
		log:       logger.New("auth-service"),
	}
	if len(rdb) > 0 {
		s.rdb = rdb[0]
	}
	return s
}

// ---------------------------------------------------------------------------
// Workspace registration
// ---------------------------------------------------------------------------

// HasAnyWorkspace returns true if at least one workspace exists in the database.
// Used to auto-close registration after the first workspace is created.
func (s *Service) HasAnyWorkspace(ctx context.Context) (bool, error) {
	return s.repo.HasAnyWorkspace(ctx)
}

// Register creates a new workspace and its first admin user atomically.
// Returns the workspace, user, and a signed JWT.
func (s *Service) Register(ctx context.Context, workspaceName, slug, email, password string) (*Workspace, *User, string, error) {
	if err := validatePassword(password); err != nil {
		return nil, nil, "", err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, nil, "", fmt.Errorf("hash password: %w", err)
	}

	ws, err := s.repo.CreateWorkspace(ctx, &Workspace{Name: workspaceName, Slug: slug})
	if err != nil {
		return nil, nil, "", fmt.Errorf("create workspace: %w", err)
	}

	user, err := s.repo.CreateUser(ctx, &User{
		WorkspaceID:  ws.ID,
		Email:        email,
		PasswordHash: string(hash),
		Role:         RoleAdmin,
		Permissions:  []string{},
	})
	if err != nil {
		return nil, nil, "", fmt.Errorf("create user: %w", err)
	}

	token, err := s.signToken(user)
	if err != nil {
		return nil, nil, "", err
	}

	s.log.Info().Str("workspace", ws.ID).Str("user", user.ID).Msg("Workspace registered")
	return ws, user, token, nil
}

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
	if err := s.repo.RevokeAPIKeysByUser(ctx, targetUserID); err != nil {
		return fmt.Errorf("revoke api keys: %w", err)
	}
	if err := s.repo.DeleteUser(ctx, targetUserID, workspaceID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Login
// ---------------------------------------------------------------------------

// dummyHash is a pre-computed bcrypt hash used to equalize response time when
// the queried email does not exist, preventing user enumeration via timing.
const dummyHash = "$2a$12$X4kv7j5ZcG39WgogSl16xuB4VUg8LmJw3RWqXfGpF0PTHsRj7OWsS"

// Login validates credentials and returns a signed JWT.
func (s *Service) Login(ctx context.Context, email, password string) (*User, string, error) {
	// Check account lockout before touching the DB or running bcrypt.
	if s.isLoginLocked(ctx, email) {
		return nil, "", ErrAccountLocked
	}

	user, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		// Equalize response time regardless of whether the email exists.
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
		s.recordLoginFailure(ctx, email)
		return nil, "", ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		s.recordLoginFailure(ctx, email)
		return nil, "", ErrInvalidCredentials
	}

	// Success — clear failure counter and update last-login timestamp.
	s.clearLoginFailures(ctx, email)

	if err := s.repo.UpdateLastLogin(ctx, user.ID); err != nil {
		s.log.Warn().Err(err).Str("user", user.ID).Msg("Failed to update last_login_at")
	}

	token, err := s.signToken(user)
	if err != nil {
		return nil, "", err
	}

	return user, token, nil
}

// isLoginLocked returns true when the email has exceeded loginMaxAttempts
// within the last loginLockDuration window.
func (s *Service) isLoginLocked(ctx context.Context, email string) bool {
	if s.rdb == nil {
		return false
	}
	key := "login:fail:" + strings.ToLower(email)
	ctx2, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	val, err := s.rdb.Get(ctx2, key).Int64()
	return err == nil && val >= loginMaxAttempts
}

// recordLoginFailure increments the per-email failure counter.
// When the counter reaches loginMaxAttempts the key TTL acts as the lockout window.
func (s *Service) recordLoginFailure(ctx context.Context, email string) {
	if s.rdb == nil {
		return
	}
	key := "login:fail:" + strings.ToLower(email)
	ctx2, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	pipe := s.rdb.Pipeline()
	incr := pipe.Incr(ctx2, key)
	pipe.Expire(ctx2, key, loginLockDuration)
	if _, err := pipe.Exec(ctx2); err != nil {
		return
	}
	if incr.Val() == loginMaxAttempts {
		s.log.Warn().Str("email", email).
			Int("attempts", loginMaxAttempts).
			Dur("lock_duration", loginLockDuration).
			Msg("Account temporarily locked after repeated failed logins")
	}
}

// clearLoginFailures removes the failure counter after a successful login.
func (s *Service) clearLoginFailures(ctx context.Context, email string) {
	if s.rdb == nil {
		return
	}
	key := "login:fail:" + strings.ToLower(email)
	ctx2, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_ = s.rdb.Del(ctx2, key).Err()
}

// ---------------------------------------------------------------------------
// JWT
// ---------------------------------------------------------------------------

// ParseToken validates a JWT string and returns the embedded claims.
func (s *Service) ParseToken(tokenStr string) (*Claims, error) {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.jwtSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer("velix-api"),
	)

	if err != nil || !t.Valid {
		return nil, ErrInvalidToken
	}

	mc, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrInvalidToken
	}

	return &Claims{
		UserID:      stringClaim(mc, "uid"),
		WorkspaceID: stringClaim(mc, "wid"),
		Email:       stringClaim(mc, "email"),
		Role:        Role(stringClaim(mc, "role")),
		Permissions: stringSliceClaim(mc, "perms"),
	}, nil
}

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

// Logout blacklists a JWT so it cannot be used again.
// The token is added to Redis with TTL matching its remaining expiry.
func (s *Service) Logout(ctx context.Context, tokenStr string) error {
	claims, err := s.ParseToken(tokenStr)
	if err != nil {
		return err
	}

	// Parse raw claims to get JTI and EXP for blacklist TTL.
	t, _ := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		return s.jwtSecret, nil
	})
	if t == nil {
		return ErrInvalidToken
	}
	mc, _ := t.Claims.(jwt.MapClaims)
	jti := stringClaim(mc, "jti")
	if jti == "" {
		// Tokens without JTI (issued before this change) can't be individually blacklisted.
		// Use the user ID as fallback — invalidates ALL tokens for this user.
		jti = "user:" + claims.UserID
	}

	if s.rdb == nil {
		return nil // no Redis = no blacklist (graceful)
	}

	// TTL = time until token expires.
	exp, _ := mc.GetExpirationTime()
	ttl := time.Until(exp.Time)
	if ttl <= 0 {
		return nil // already expired
	}

	return s.rdb.Set(ctx, "jwt:blacklist:"+jti, "1", ttl).Err()
}

// IsBlacklisted checks if a JWT's JTI is in the Redis blacklist.
func (s *Service) IsBlacklisted(ctx context.Context, tokenStr string) bool {
	if s.rdb == nil {
		return false
	}

	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		return s.jwtSecret, nil
	})
	if err != nil || t == nil {
		return false
	}
	mc, _ := t.Claims.(jwt.MapClaims)
	jti := stringClaim(mc, "jti")
	if jti == "" {
		return false
	}

	val, err := s.rdb.Exists(ctx, "jwt:blacklist:"+jti).Result()
	return err == nil && val > 0
}

// ---------------------------------------------------------------------------
// API Keys
// ---------------------------------------------------------------------------

// CreateAPIKey generates a new API key for a workspace.
// The raw secret is returned once and never stored — only its bcrypt hash is kept.
func (s *Service) CreateAPIKey(ctx context.Context, workspaceID, userID, name string, expiresAt *time.Time, scopes []string) (*APIKey, string, error) {
	raw, err := generateAPIKey()
	if err != nil {
		return nil, "", fmt.Errorf("generate key: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(raw), bcryptCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash key: %w", err)
	}

	prefix := raw[:len(keyPrefix)+12] // "wapi_" + first 12 hex chars

	key, err := s.repo.CreateAPIKey(ctx, &APIKey{
		WorkspaceID: workspaceID,
		UserID:      userID,
		KeyHash:     string(hash),
		KeyPrefix:   prefix,
		Name:        name,
		ExpiresAt:   expiresAt,
		Scopes:      scopes,
	})
	if err != nil {
		return nil, "", fmt.Errorf("persist key: %w", err)
	}

	return key, raw, nil
}

// ValidateAPIKey checks an API key string against stored hashes.
// It looks up candidates by key prefix (first 12 chars after "wapi_") for efficiency.
func (s *Service) ValidateAPIKey(ctx context.Context, raw string) (*APIKey, error) {
	if !strings.HasPrefix(raw, keyPrefix) || len(raw) < len(keyPrefix)+12 {
		return nil, ErrInvalidAPIKey
	}

	prefix := keyPrefix + raw[len(keyPrefix):len(keyPrefix)+12]
	key, err := s.repo.GetAPIKeyByPrefix(ctx, prefix)
	if err != nil || !key.IsValid() {
		return nil, ErrInvalidAPIKey
	}

	if err := bcrypt.CompareHashAndPassword([]byte(key.KeyHash), []byte(raw)); err != nil {
		return nil, ErrInvalidAPIKey
	}

	// Best-effort touch — don't fail the request if this errors.
	_ = s.repo.TouchAPIKey(ctx, key.ID)

	return key, nil
}

// ListAPIKeys returns all keys for a workspace (hashes are not included in response).
func (s *Service) ListAPIKeys(ctx context.Context, workspaceID string) ([]*APIKey, error) {
	return s.repo.ListAPIKeys(ctx, workspaceID)
}

// RevokeAPIKey marks a key as revoked.
func (s *Service) RevokeAPIKey(ctx context.Context, keyID, workspaceID string) error {
	return s.repo.RevokeAPIKey(ctx, keyID, workspaceID)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func generateAPIKey() (string, error) {
	b := make([]byte, apiKeyLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// Include the wapi_ prefix so the returned key is ready to use directly.
	return keyPrefix + hex.EncodeToString(b), nil
}

func validatePassword(p string) error {
	if len(p) < 8 {
		return fmt.Errorf("%w: minimum 8 characters required", ErrWeakPassword)
	}
	var hasUpper, hasLower, hasDigit bool
	for _, c := range p {
		switch {
		case unicode.IsUpper(c):
			hasUpper = true
		case unicode.IsLower(c):
			hasLower = true
		case unicode.IsDigit(c):
			hasDigit = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit {
		return fmt.Errorf("%w: must contain at least one uppercase letter, one lowercase letter, and one number", ErrWeakPassword)
	}
	return nil
}

func stringClaim(mc jwt.MapClaims, key string) string {
	if v, ok := mc[key].(string); ok {
		return v
	}
	return ""
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

// Sentinel errors.
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
