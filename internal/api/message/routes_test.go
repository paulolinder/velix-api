package message

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"velix/internal/domain/auth"
	"velix/internal/domain/message"
	"velix/internal/server/middleware"
)

// TestRoutes_PermissionWiring guards against a future refactor accidentally
// dropping a middleware.RequirePermission(...) from a route registration in
// Routes() — such a regression would compile fine and pass every other test,
// since nothing else exercises the actual chi wiring.
func TestRoutes_PermissionWiring(t *testing.T) {
	// A nil *message.Service is fine here: the permission gate runs before the
	// handler, so the 403 case never reaches svc, and the non-403 case below
	// is set up to fail its own validation before touching svc too.
	var svc *message.Service
	router := Routes(svc)

	memberViewOnly := &auth.Claims{
		Role:        auth.RoleMember,
		Permissions: []string{"messages:view"},
	}

	t.Run("POST /text requires messages:send", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/text", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), memberViewOnly))
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("POST /text with only messages:view = %d, want %d", rec.Code, http.StatusForbidden)
		}
	})

	t.Run("GET / (list by chat) passes with messages:view", func(t *testing.T) {
		// Deliberately omit the "chat" query parameter: the handler itself
		// returns a 400 for that before ever touching h.svc, so this proves
		// the permission gate let the request through without depending on
		// a real message.Service behind it.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), memberViewOnly))
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code == http.StatusForbidden {
			t.Errorf("GET / with messages:view got %d, want anything but %d (permission gate should have passed)", rec.Code, http.StatusForbidden)
		}
	})
}
