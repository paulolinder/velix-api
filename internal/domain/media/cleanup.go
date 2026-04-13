// Package media provides media file management utilities.
package media

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"velix/internal/logger"
)

// StartCleanupWorker periodically deletes media files older than maxAge.
// Runs every hour. Call with a cancellable context.
func StartCleanupWorker(ctx context.Context, storagePath string, maxAge time.Duration) {
	if storagePath == "" {
		return
	}
	log := logger.New("media-cleanup")

	go func() {
		// First run after 5 minutes.
		timer := time.NewTimer(5 * time.Minute)
		defer timer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				deleted, freed := cleanup(storagePath, maxAge)
				if deleted > 0 {
					log.Info().
						Int("files", deleted).
						Int64("freed_mb", freed/(1024*1024)).
						Dur("max_age", maxAge).
						Msg("Media cleanup completed")
				}
				timer.Reset(1 * time.Hour)
			}
		}
	}()
}

func cleanup(dir string, maxAge time.Duration) (int, int64) {
	cutoff := time.Now().Add(-maxAge)
	deleted := 0
	var freed int64

	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			size := info.Size()
			if os.Remove(path) == nil {
				deleted++
				freed += size
			}
		}
		return nil
	})

	return deleted, freed
}
