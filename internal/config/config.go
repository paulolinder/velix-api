// Package config loads and validates application configuration from environment variables.
// The application fails fast at startup if required values are missing or invalid.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the root configuration tree.
type Config struct {
	App          AppConfig
	HTTP         HTTPConfig
	Database     DatabaseConfig
	Redis        RedisConfig
	Auth         AuthConfig
	Media        MediaConfig
	License      LicenseConfig
	Engine       EngineConfig
	Integrations IntegrationsConfig
}

// AppConfig holds general application settings.
type AppConfig struct {
	Name        string
	Version     string
	Environment string // development | staging | production
	LogLevel    string // debug | info | warn | error
}

// HTTPConfig holds HTTP server tuning parameters.
type HTTPConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// Addr returns the "host:port" string for the HTTP server.
func (c HTTPConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	URL             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// RedisConfig holds Redis connection settings.
type RedisConfig struct {
	URL      string
	Password string
	DB       int
}

// AuthConfig holds JWT and API key settings.
type AuthConfig struct {
	JWTSecret           string
	JWTExpiry           time.Duration
	APIKeyLength        int
	RegistrationEnabled bool // when false, POST /auth/register returns 403
}

// MediaConfig holds local file storage settings.
type MediaConfig struct {
	StoragePath string
	MaxFileSize int64 // bytes
}

// IntegrationsConfig holds optional third-party integration settings.
type IntegrationsConfig struct {
	// ChatwootWebhookSecret is an optional shared secret for validating inbound
	// Chatwoot webhook calls. When set, requests must include it as:
	//   ?secret=<value>  OR  Authorization: Bearer <value>
	// Configure the same secret in Chatwoot's webhook settings.
	ChatwootWebhookSecret string
}

// LicenseConfig holds license key settings.
type LicenseConfig struct {
	Key string // LICENSE_KEY — signed JWT from the Velix license server
}

// EngineConfig holds WhatsApp engine settings.
type EngineConfig struct {
	StorePath      string // base path for per-instance WA SQLite stores
	MediaStorePath string // directory where incoming media files are saved
	AutoReconnect  bool
	QRTimeout      time.Duration
	MaxInstances   int
}

// Load reads configuration from environment variables and validates it.
// Returns an error immediately if any required variable is missing or invalid.
func Load() (*Config, error) {
	cfg := &Config{
		App: AppConfig{
			Name:        env("APP_NAME", "Velix API"),
			Version:     env("APP_VERSION", "0.1.0"),
			Environment: env("APP_ENV", "development"),
			LogLevel:    env("LOG_LEVEL", "info"),
		},
		HTTP: HTTPConfig{
			Host:            env("HTTP_HOST", "0.0.0.0"),
			Port:            envInt("HTTP_PORT", 8080),
			ReadTimeout:     envDuration("HTTP_READ_TIMEOUT", 30*time.Second),
			WriteTimeout:    envDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:     envDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout: envDuration("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second),
		},
		Database: DatabaseConfig{
			URL:             os.Getenv("DATABASE_URL"),
			MaxOpenConns:    envInt("DATABASE_MAX_OPEN_CONNS", 100),
			MaxIdleConns:    envInt("DATABASE_MAX_IDLE_CONNS", 10),
			ConnMaxLifetime: envDuration("DATABASE_CONN_MAX_LIFETIME", 5*time.Minute),
		},
		Redis: RedisConfig{
			URL:      os.Getenv("REDIS_URL"),
			Password: env("REDIS_PASSWORD", ""),
			DB:       envInt("REDIS_DB", 0),
		},
		Auth: AuthConfig{
			JWTSecret:           os.Getenv("JWT_SECRET"),
			JWTExpiry:           envDuration("JWT_EXPIRY", 24*time.Hour),
			APIKeyLength:        envInt("API_KEY_LENGTH", 32),
			RegistrationEnabled: envBool("REGISTRATION_ENABLED", true),
		},
		Media: MediaConfig{
			StoragePath: env("MEDIA_STORAGE_PATH", "./data/media"),
			MaxFileSize: envInt64("MEDIA_MAX_FILE_SIZE", 64<<20), // 64 MB
		},
		License: LicenseConfig{
			Key: os.Getenv("LICENSE_KEY"),
		},
		Integrations: IntegrationsConfig{
			ChatwootWebhookSecret: os.Getenv("CHATWOOT_WEBHOOK_SECRET"),
		},
		Engine: EngineConfig{
			StorePath:      env("ENGINE_STORE_PATH", "./data/instances"),
			MediaStorePath: env("MEDIA_STORAGE_PATH", "./data/media"),
			AutoReconnect:  envBool("ENGINE_AUTO_RECONNECT", true),
			QRTimeout:      envDuration("ENGINE_QR_TIMEOUT", 60*time.Second),
			MaxInstances:   envInt("ENGINE_MAX_INSTANCES", 200),
		},
	}

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func validate(cfg *Config) error {
	var errs []string

	if cfg.Database.URL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}
	if cfg.Redis.URL == "" {
		errs = append(errs, "REDIS_URL is required")
	}
	if cfg.Auth.JWTSecret == "" {
		errs = append(errs, "JWT_SECRET is required")
	} else if len(cfg.Auth.JWTSecret) < 32 {
		errs = append(errs, "JWT_SECRET must be at least 32 characters")
	} else if strings.Contains(cfg.Auth.JWTSecret, "change-me") {
		errs = append(errs, "JWT_SECRET contains a default placeholder — set a real secret")
	}
	if cfg.HTTP.Port < 1 || cfg.HTTP.Port > 65535 {
		errs = append(errs, "HTTP_PORT must be between 1 and 65535")
	}

	if len(errs) > 0 {
		msg := "configuration errors:"
		for _, e := range errs {
			msg += "\n  - " + e
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func env(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}

func envInt64(key string, defaultVal int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return defaultVal
}

func envBool(key string, defaultVal bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return defaultVal
}

func envDuration(key string, defaultVal time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return defaultVal
}
