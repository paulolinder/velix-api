// Package server wires together the HTTP listener and graceful shutdown.
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"velix/internal/config"
	"velix/internal/logger"
)

// Server wraps net/http.Server with helpers for non-blocking start and graceful shutdown.
type Server struct {
	http *http.Server
}

// New creates a Server from the given config and handler.
func New(cfg config.HTTPConfig, handler http.Handler) *Server {
	return &Server{
		http: &http.Server{
			Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			Handler:      handler,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
			IdleTimeout:  cfg.IdleTimeout,
		},
	}
}

// Start binds to the configured address and begins serving in a goroutine.
// It returns an error immediately if the port cannot be bound — so startup
// failures are detected synchronously rather than in a background goroutine.
func (s *Server) Start() error {
	slog := logger.New("server")

	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.http.Addr, err)
	}

	slog.Info().Str("addr", s.http.Addr).Msg("HTTP server listening")

	go func() {
		if err := s.http.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error().Err(err).Msg("HTTP server error")
		}
	}()

	return nil
}

// Shutdown gracefully drains active connections within the context deadline.
func (s *Server) Shutdown(ctx context.Context) error {
	slog := logger.New("server")
	slog.Info().Msg("Shutting down HTTP server")
	return s.http.Shutdown(ctx)
}

// Addr returns the bound address (useful in tests with port 0).
func (s *Server) Addr() string {
	return s.http.Addr
}
