package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/glebarez/go-sqlite" // registers pure-Go SQLite as "sqlite3" driver

	"github.com/rs/zerolog/log"

	waengine "velix/internal/engine/whatsmeow"
	"velix/internal/config"
	"velix/internal/domain/auth"
	"velix/internal/domain/media"
	"velix/internal/domain/chatwoot"
	"velix/internal/domain/instance"
	"velix/internal/domain/message"
	"velix/internal/domain/webhook"
	"velix/internal/infra/cache"
	"velix/internal/infra/database"
	"velix/internal/infra/repo"
	"velix/internal/logger"
	"velix/internal/server"
)

func main() {
	// Health check mode: used by Docker HEALTHCHECK to probe the running server.
	if len(os.Args) > 1 && os.Args[1] == "--health" {
		resp, err := http.Get("http://localhost:" + envOrDefault("HTTP_PORT", "8080") + "/health")
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// 1. Load and validate configuration — fail fast on bad config.
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("Invalid configuration")
	}

	// 2. Configure global logger.
	logger.Setup(cfg.App.LogLevel, cfg.App.Environment)

	appLog := logger.New("main")
	appLog.Info().
		Str("app", cfg.App.Name).
		Str("version", cfg.App.Version).
		Str("env", cfg.App.Environment).
		Msg("Starting Velix API")

	ctx := context.Background()

	// 3. Run database migrations (embedded SQL, safe to run on every startup).
	appLog.Info().Msg("Running database migrations...")
	if err := database.RunMigrations(cfg.Database.URL); err != nil {
		appLog.Fatal().Err(err).Msg("Database migration failed")
	}
	appLog.Info().Msg("Migrations up to date")

	// 4. Connect to PostgreSQL.
	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		appLog.Fatal().Err(err).Msg("Failed to connect to PostgreSQL")
	}
	defer db.Close()
	appLog.Info().Msg("PostgreSQL connected")

	// 5. Connect to Redis.
	rdb, err := cache.New(ctx, cfg.Redis)
	if err != nil {
		appLog.Fatal().Err(err).Msg("Failed to connect to Redis")
	}
	defer func() { _ = rdb.Close() }()
	appLog.Info().Msg("Redis connected")
	if cfg.Redis.Password == "" && cfg.App.Environment != "development" {
		appLog.Warn().Msg("SECURITY: REDIS_PASSWORD is not set — Redis is unauthenticated. Set a strong password in production to prevent unauthorized access.")
	}

	// 6. Boot the WhatsApp engine with a cancellable context.
	engineCtx, engineCancel := context.WithCancel(ctx)
	eng := waengine.New(cfg.Engine)
	if err := eng.Start(engineCtx); err != nil {
		engineCancel()
		appLog.Fatal().Err(err).Msg("Failed to start WhatsApp engine")
	}
	defer func() {
		engineCancel()
		if err := eng.Stop(context.Background()); err != nil {
			appLog.Error().Err(err).Msg("Error stopping engine")
		}
	}()

	// 7. Wire up domain services.
	authRepo      := repo.NewAuthRepo(db)
	instanceRepo  := repo.NewInstanceRepo(db)
	messageRepo   := repo.NewMessageRepo(db)
	auditRepo     := repo.NewAuditRepo(db)
	chatwootRepo  := repo.NewChatwootRepo(db)

	authSvc     := auth.NewService(authRepo, cfg.Auth.JWTSecret, cfg.Auth.JWTExpiry, rdb)
	instanceSvc := instance.NewService(instanceRepo, eng, cfg.Engine.MaxInstances)
	messageSvc  := message.NewService(messageRepo, eng)

	// Webhook service subscribes to engine events and delivers to per-instance URLs.
	// The engine holds a reference via the handler closure — no need to keep it in Deps.
	webhook.NewService(eng, instanceSvc)

	// Chatwoot service: bridges WhatsApp events ↔ Chatwoot inboxes.
	chatwootSvc := chatwoot.NewService(eng, instanceSvc, nil, chatwootRepo)

	// 8. Restore persisted instances (auto-reconnect previously connected ones).
	if err := instanceSvc.RestoreFromDB(ctx); err != nil {
		appLog.Warn().Err(err).Msg("Failed to restore instances — continuing without restoration")
	}

	// 9. Start background workers.
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	messageSvc.StartSchedulerWorker(workerCtx)

	// 9b. Start media cleanup worker (deletes files older than 7 days, runs hourly).
	media.StartCleanupWorker(workerCtx, cfg.Media.StoragePath, 7*24*time.Hour)

	// 10. Build router and start HTTP server.
	deps := &server.Deps{
		Engine:                eng,
		Health:                server.NewHealthChecker(db, rdb),
		AuthService:           authSvc,
		InstanceService:       instanceSvc,
		MessageService:        messageSvc,
		ChatwootService:       chatwootSvc,
		AuditRepo:             auditRepo,
		MediaStorePath:        cfg.Media.StoragePath,
		Redis:                 rdb,
		ChatwootWebhookSecret: cfg.Integrations.ChatwootWebhookSecret,
		RegistrationEnabled:   cfg.Auth.RegistrationEnabled,
	}
	router := server.NewRouter(deps)
	srv := server.New(cfg.HTTP, router)

	if err := srv.Start(); err != nil {
		appLog.Fatal().Err(err).Msg("Failed to start HTTP server")
	}
	appLog.Info().Str("addr", cfg.HTTP.Addr()).Msg("HTTP server listening")

	// 11. Block until SIGINT or SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	appLog.Info().Str("signal", sig.String()).Msg("Shutdown signal received")
	appLog.Info().Msg("Shutting down gracefully...")

	// Give in-flight requests 15s to finish.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	// 12. Stop accepting new connections and drain in-flight requests.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		appLog.Error().Err(err).Msg("HTTP server shutdown error")
	}

	// 13. Cancel worker context (stops scheduler, etc.).
	workerCancel()

	// 14. Stop the engine gracefully.
	if err := eng.Stop(shutdownCtx); err != nil {
		appLog.Warn().Err(err).Msg("Engine stop error")
	}

	appLog.Info().Msg("Shutdown complete")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
