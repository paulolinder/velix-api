package auth

import (
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	apipkg "velix/internal/api"
	"velix/internal/domain/auth"
	"velix/internal/server/middleware"
)

// slugRegexp allows only lowercase letters, numbers, and hyphens (3–50 chars),
// with no leading/trailing hyphens.
var slugRegexp = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,48}[a-z0-9]$`)

// Handler holds the auth service dependency.
type Handler struct {
	svc                 *auth.Service
	registrationEnabled bool
}

// NewHandler creates a new auth HTTP handler.
func NewHandler(svc *auth.Service, registrationEnabled bool) *Handler {
	return &Handler{svc: svc, registrationEnabled: registrationEnabled}
}

// Routes returns all auth routes under a single router.
// Protected routes (api-keys, me) are gated by the provided authenticate middleware.
// loginLimit, if provided, is applied to /login and /register to prevent brute-force.
func Routes(svc *auth.Service, registrationEnabled bool, authenticate func(http.Handler) http.Handler, loginLimit ...func(http.Handler) http.Handler) http.Handler {
	h := NewHandler(svc, registrationEnabled)
	r := chi.NewRouter()

	// Resolve optional login rate-limit middleware.
	var loginMW func(http.Handler) http.Handler
	if len(loginLimit) > 0 && loginLimit[0] != nil {
		loginMW = loginLimit[0]
	} else {
		loginMW = func(next http.Handler) http.Handler { return next }
	}

	// Public — no token required, but rate-limited per IP.
	r.With(loginMW).Post("/register", h.Register)
	r.With(loginMW).Post("/login", h.Login)
	r.Get("/registration-status", h.RegistrationStatus)

	// Protected — require valid JWT or API key.
	r.Group(func(r chi.Router) {
		r.Use(authenticate)
		r.Get("/me", h.Me)
		r.Post("/logout", h.Logout)

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireRole(auth.RoleAdmin))
			r.Get("/api-keys", h.ListAPIKeys)
			r.Post("/api-keys", h.CreateAPIKey)
			r.Delete("/api-keys/{keyID}", h.RevokeAPIKey)
		})
	})

	return r
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// Register handles POST /v1/auth/register.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	// Registration is open when:
	//   a) REGISTRATION_ENABLED=true (explicit multi-tenant override), OR
	//   b) no workspace exists yet (first-run, single-tenant default)
	// After the first workspace is created, registration closes automatically.
	if !h.registrationEnabled {
		exists, err := h.svc.HasAnyWorkspace(r.Context())
		if err != nil || exists {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeForbidden,
				"Registration is closed. Contact your administrator."))
			return
		}
	}

	var req RegisterRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{
		"workspace_name": req.WorkspaceName,
		"slug":           req.Slug,
		"email":          req.Email,
		"password":       req.Password,
	}) {
		return
	}

	// Validate email format.
	if _, err := mail.ParseAddress(req.Email); err != nil {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, "invalid email address"))
		return
	}
	// Validate slug format.
	if !slugRegexp.MatchString(req.Slug) {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation,
			"slug must be 3–50 characters, lowercase letters/numbers/hyphens only, no leading/trailing hyphens"))
		return
	}

	ws, user, token, err := h.svc.Register(r.Context(), req.WorkspaceName, req.Slug, req.Email, req.Password, req.TermsAccepted)
	if err != nil {
		if errors.Is(err, auth.ErrTermsNotAccepted) || errors.Is(err, auth.ErrWeakPassword) {
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeValidation, err.Error()))
			return
		}
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeConflict, err.Error()))
		return
	}

	apipkg.WriteJSON(w, r, http.StatusCreated, &RegisterResponse{
		Workspace: workspaceFromDomain(ws),
		User:      userFromDomain(user),
		Token:     token,
	})
}

// Login handles POST /v1/auth/login.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}
	if !apipkg.RequireFields(w, r, map[string]string{
		"email":    req.Email,
		"password": req.Password,
	}) {
		return
	}

	user, token, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrAccountLocked) {
			w.Header().Set("Retry-After", "900") // 15 minutes
			apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeRateLimit,
				"Account temporarily locked due to too many failed attempts. Try again in 15 minutes."))
			return
		}
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeUnauthorized, "Invalid email or password"))
		return
	}

	apipkg.WriteJSON(w, r, http.StatusOK, &LoginResponse{
		Token: token,
		User:  userFromDomain(user),
	})
}

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

// Me handles GET /v1/auth/me — returns the current user's claims.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, claims)
}

// Logout handles POST /v1/auth/logout — blacklists the current JWT.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	raw := extractBearerToken(r)
	if raw == "" || strings.HasPrefix(raw, "wapi_") {
		// API keys don't have logout — they must be revoked via DELETE /api-keys/{id}.
		apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"message": "ok"})
		return
	}
	if err := h.svc.Logout(r.Context(), raw); err != nil {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeInternal, "logout failed"))
		return
	}
	apipkg.WriteJSON(w, r, http.StatusOK, map[string]string{"message": "logged out"})
}

func extractBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// CreateAPIKey handles POST /v1/auth/api-keys.
func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
		return
	}

	var req CreateAPIKeyRequest
	if !apipkg.DecodeJSON(w, r, &req) {
		return
	}

	// If scoped to an instance: auto-add scope, clear expiry (never expires).
	if req.InstanceID != "" {
		scope := "instance:" + req.InstanceID
		req.Scopes = append(req.Scopes, scope)
		req.ExpiresAt = nil
	}

	// Default: explicit full access. This makes the permission visible in the key record
	// instead of relying on "empty = all" implicit behavior.
	if len(req.Scopes) == 0 {
		req.Scopes = []string{"*"}
	}

	key, rawSecret, err := h.svc.CreateAPIKey(r.Context(), claims.WorkspaceID, claims.UserID, req.Name, req.ExpiresAt, req.Scopes)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "create API key")
		return
	}

	apipkg.WriteJSON(w, r, http.StatusCreated, &CreateAPIKeyResponse{
		Key:    rawSecret,
		Detail: apiKeyFromDomain(key),
	})
}

// ListAPIKeys handles GET /v1/auth/api-keys.
func (h *Handler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
		return
	}

	keys, err := h.svc.ListAPIKeys(r.Context(), claims.WorkspaceID)
	if err != nil {
		apipkg.LogAndFail(w, r, err, "list API keys")
		return
	}

	out := make([]*APIKeyResponse, 0, len(keys))
	for _, k := range keys {
		out = append(out, apiKeyFromDomain(k))
	}
	apipkg.WriteJSON(w, r, http.StatusOK, out)
}

// RevokeAPIKey handles DELETE /v1/auth/api-keys/{keyID}.
func (h *Handler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		apipkg.WriteError(w, r, apipkg.ErrUnauthorized)
		return
	}

	if err := h.svc.RevokeAPIKey(r.Context(), apipkg.Param(r, "keyID"), claims.WorkspaceID); err != nil {
		apipkg.WriteError(w, r, apipkg.NewError(apipkg.ErrCodeNotFound, err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
