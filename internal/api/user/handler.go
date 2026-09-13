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
