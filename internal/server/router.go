package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"golang.org/x/time/rate"

	adminapi    "velix/internal/api/admin"
	auditapi    "velix/internal/api/audit"
	authapi     "velix/internal/api/auth"
	contactapi  "velix/internal/api/contact"
	groupapi    "velix/internal/api/group"
	instanceapi "velix/internal/api/instance"
	mediaapi    "velix/internal/api/media"
	messageapi  "velix/internal/api/message"
	wsapi       "velix/internal/api/ws"
	"velix/internal/metrics"
	"velix/internal/server/middleware"
	"velix/internal/ui"
)

// NewRouter builds and returns the main application router.
func NewRouter(deps *Deps) http.Handler {
	r := chi.NewRouter()

	// ---- Global middleware (order matters) ----
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(middleware.SecureHeaders)
	r.Use(middleware.RequestLogger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.StripSlashes)

	// CORS — origins from CORS_ALLOWED_ORIGINS env var (comma-separated), default "*"
	r.Use(middleware.CORS(allowedOrigins()))

	// Body size limit: 64 MB (covers media uploads)
	r.Use(middleware.BodyLimit(64 << 20))

	// Request timeout: 60s (SSE and WebSocket endpoints override via their own context)
	r.Use(middleware.Timeout(60 * time.Second))

	// Global rate limit: 200 req/s burst 500 (per process)
	r.Use(middleware.RateLimiter(rate.Limit(200), 500))

	// Count every request for metrics.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metrics.M.HTTPRequests.Add(1)
			next.ServeHTTP(w, r)
		})
	})

	// Root redirect → admin panel.
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusFound)
	})

	// ---- Infra endpoints (no auth required) ----
	r.Get("/health", healthHandler)
	r.Get("/ready", deps.Health.ReadyHandler)
	r.Get("/metrics", metrics.M.Handler())

	// ---- API docs (Swagger UI + raw spec) ----
	r.Get("/docs", swaggerUIHandler)
	r.Get("/docs/openapi.yaml", openapiSpecHandler)

	// ---- Admin panel (static HTML/JS/CSS embedded in binary) ----
	adminFS, _ := fs.Sub(ui.FS, "web/admin")
	r.Get("/admin", adminFileHandler(adminFS, "index.html"))
	r.Get("/admin/*", func(w http.ResponseWriter, r *http.Request) {
		file := chi.URLParam(r, "*")
		if file == "" {
			file = "index.html"
		}
		http.ServeFileFS(w, r, adminFS, file)
	})

	// ---- WebSocket real-time events (auth via ?token=<jwt>) ----
	wsHub := wsapi.NewHub(deps.Engine, deps.InstanceService)
	wsHandler := wsapi.NewHandler(wsHub, deps.AuthService)
	r.Get("/v1/ws", wsHandler.ServeHTTP)

	// ---- API v1 ----
	authMiddleware := middleware.Authenticate(deps.AuthService)

	r.Route("/v1", func(r chi.Router) {
		// Auth routes — public (register, login) + protected (me, api-keys) in one mount.
		r.Mount("/auth", authapi.Routes(deps.AuthService, authMiddleware))

		// Everything below requires a valid JWT or API Key.
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Use(middleware.WorkspaceRateLimit(deps.Redis))

			// Admin API — only admin and developer roles.
			r.With(middleware.RequireRole("admin", "developer")).
				Get("/admin/stats", adminapi.Stats(deps.InstanceService, deps.AuthService))

			// Instance management — single Route tree to avoid chi trie conflicts
			// that arise when r.Mount and r.Route share the same path prefix.
			instH := instanceapi.NewHandler(deps.InstanceService)
			r.Route("/instances", func(r chi.Router) {
				r.Post("/", instH.Create)
				r.Get("/", instH.List)

				r.Route("/{instanceID}", func(r chi.Router) {
					// Core CRUD + lifecycle (ownership checked inside service).
					r.Get("/", instH.Get)
					r.Delete("/", instH.Delete)
					r.Post("/connect", instH.Connect)
					r.Post("/disconnect", instH.Disconnect)
					r.Post("/logout", instH.Logout)
					r.Get("/status", instH.GetStatus)
					r.Get("/qr", instH.GetQR)
					r.Post("/pair-code", instH.PairCode)
					r.Mount("/settings", instanceapi.SettingsRoutes(deps.InstanceService))

					// Sub-resources — ownership enforced by middleware.
					r.Group(func(r chi.Router) {
						r.Use(middleware.RequireInstanceOwner(deps.InstanceService))
						r.Mount("/messages", messageapi.Routes(deps.MessageService))
						r.Mount("/contacts", contactapi.Routes(deps.Engine))
						r.Mount("/groups",   groupapi.Routes(deps.Engine))
						r.Post("/presence", instanceapi.PresenceHandler(deps.InstanceService))
						r.Patch("/profile", instanceapi.ProfileHandler(deps.InstanceService))
					})
				})
			})


			// Audit log queries.
			r.Get("/audit-logs", auditapi.NewHandler(deps.AuditRepo).List)

			// Media upload and retrieval.
			mediaHandler := mediaapi.NewHandler(deps.MediaStorePath)
			r.Post("/media", mediaHandler.Upload)
			r.Get("/media/{mediaID}", mediaHandler.Download)
		})
	})

	return r
}

// healthHandler is a liveness probe — always 200 if the process is alive.
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"timestamp": time.Now().UTC(),
	})
}

// swaggerUIHandler serves the Swagger UI HTML page pointing at our spec.
func swaggerUIHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Velix API – Documentação</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
  SwaggerUIBundle({
    url: "/docs/openapi.yaml",
    dom_id: "#swagger-ui",
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
    layout: "BaseLayout",
    deepLinking: true,
    tryItOutEnabled: true,
  });
</script>
</body>
</html>`))
}

// openapiSpecHandler serves the raw OpenAPI YAML file.
func openapiSpecHandler(w http.ResponseWriter, _ *http.Request) {
	data, err := ui.FS.ReadFile("web/openapi.yaml")
	if err != nil {
		http.Error(w, "spec not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(data)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func adminFileHandler(fsys fs.FS, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, fsys, name)
	}
}

func allowedOrigins() []string {
	v := os.Getenv("CORS_ALLOWED_ORIGINS")
	if v == "" {
		return []string{"*"}
	}
	parts := strings.Split(v, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			origins = append(origins, s)
		}
	}
	return origins
}
