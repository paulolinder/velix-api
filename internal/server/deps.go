package server

import (
	"github.com/redis/go-redis/v9"

	"velix/internal/domain/auth"
	"velix/internal/domain/chatwoot"
	"velix/internal/domain/instance"
	"velix/internal/domain/message"
	"velix/internal/engine"
	"velix/internal/infra/repo"
)

// Deps holds all application-level dependencies injected into the HTTP router.
type Deps struct {
	Engine           engine.Engine
	Health           *HealthChecker
	AuthService      *auth.Service
	InstanceService  *instance.Service
	MessageService   *message.Service
	ChatwootService  *chatwoot.Service
	AuditRepo        *repo.AuditRepo
	MediaStorePath   string        // directory for uploaded media files
	Redis            *redis.Client // distributed rate limiting
}
