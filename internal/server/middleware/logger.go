// Package middleware provides HTTP middleware for the Chi router.
package middleware

import (
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"
)

// RequestLogger logs every request with method, path, status, latency and request-id.
// It stores a request-scoped logger in context so handlers can use logger.FromContext(r.Context()).
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		requestID := chimw.GetReqID(r.Context())

		// Enrich context with a request-scoped logger.
		reqLogger := log.Logger.With().Str("request_id", requestID).Logger()
		ctx := reqLogger.WithContext(r.Context())

		defer func() {
			log.Ctx(ctx).Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Str("query", r.URL.RawQuery).
				Int("status", ww.Status()).
				Int("bytes", ww.BytesWritten()).
				Dur("latency", time.Since(start)).
				Str("ip", r.RemoteAddr).
				Msg("→")
		}()

		next.ServeHTTP(ww, r.WithContext(ctx))
	})
}
