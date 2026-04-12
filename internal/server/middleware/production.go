package middleware

import (
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"golang.org/x/time/rate"
)

// SecureHeaders adds standard security headers to every response.
func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		next.ServeHTTP(w, r)
	})
}

// BodyLimit returns middleware that rejects requests larger than maxBytes.
// Use 0 for no limit (not recommended in production).
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return chimw.RequestSize(maxBytes)
}

// Timeout returns middleware that cancels the request context after d.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return chimw.Timeout(d)
}

// CORS returns a CORS middleware. AllowCredentials is only enabled when
// explicit origins are set (not "*"), preventing credential leaks.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"*"}
	}
	// Only allow credentials with explicit origins, never with "*".
	allowCreds := true
	for _, o := range allowedOrigins {
		if o == "*" {
			allowCreds = false
			break
		}
	}
	return cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-API-Key", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: allowCreds,
		MaxAge:           300,
	})
}

// RateLimiter returns a simple in-process token-bucket rate limiter.
// r = requests per second allowed; b = burst size.
// This is per-process, not distributed. For multi-instance deployments,
// replace with a Redis-backed limiter.
func RateLimiter(r rate.Limit, b int) func(http.Handler) http.Handler {
	limiter := rate.NewLimiter(r, b)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !limiter.Allow() {
				http.Error(w, `{"success":false,"error":{"code":"RATE_LIMIT_EXCEEDED","message":"Too many requests"}}`,
					http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}
