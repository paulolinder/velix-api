package database

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // pgx5 driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// RunMigrations applies all pending UP migrations embedded in the binary.
// It is safe to call on every startup — already-applied migrations are skipped.
func RunMigrations(databaseURL string) error {
	// The pgx/v5 driver requires the scheme to be "pgx5://"
	// Convert "postgres://" or "postgresql://" → "pgx5://"
	dsn := toPGX5DSN(databaseURL)

	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("sub migrations fs: %w", err)
	}

	src, err := iofs.New(sub, ".")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return nil
}

// toPGX5DSN converts a standard postgres DSN to the pgx5 scheme.
func toPGX5DSN(url string) string {
	for _, prefix := range []string{"postgresql://", "postgres://"} {
		if len(url) >= len(prefix) && url[:len(prefix)] == prefix {
			return "pgx5://" + url[len(prefix):]
		}
	}
	return url
}
