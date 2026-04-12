// Package logger wraps zerolog and provides application-wide logging helpers.
package logger

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Setup configures the global zerolog logger.
// In non-production environments it uses a human-readable console writer.
func Setup(level, environment string) {
	zerolog.TimeFieldFormat = time.RFC3339

	logLevel, err := zerolog.ParseLevel(level)
	if err != nil {
		logLevel = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(logLevel)

	if environment != "production" {
		log.Logger = log.Output(zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "15:04:05",
		}).With().Caller().Logger()
	} else {
		log.Logger = zerolog.New(os.Stdout).
			With().
			Timestamp().
			Logger()
	}
}

// New returns a sub-logger tagged with a component name.
// Use this at package level: var log = logger.New("instance-service")
func New(component string) zerolog.Logger {
	return log.Logger.With().Str("component", component).Logger()
}

// FromContext retrieves the request-scoped logger stored by the HTTP middleware.
// Falls back to the global logger when no logger is in context.
func FromContext(ctx context.Context) *zerolog.Logger {
	return log.Ctx(ctx)
}
