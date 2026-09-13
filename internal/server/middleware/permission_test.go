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
