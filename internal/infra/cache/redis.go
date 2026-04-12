// Package cache provides the Redis client used by all caching and queue operations.
package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"velix/internal/config"
)

// New creates a Redis client and verifies the connection with a PING.
// The caller is responsible for calling client.Close() when done.
func New(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}

	if cfg.Password != "" {
		opts.Password = cfg.Password
	}
	opts.DB = cfg.DB

	client := redis.NewClient(opts)

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping Redis: %w", err)
	}

	return client, nil
}
