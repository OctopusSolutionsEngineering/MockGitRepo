package cleanup

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

var cleanupMu sync.Mutex

const (
	cleanupInterval = 1 * time.Hour
	maxTempDirAge   = 8 * time.Hour
)

// StartTempDirCleanup launches a background goroutine that runs every hour and
// removes any top-level directories inside os.TempDir() that are older than 8 hours.
func StartTempDirCleanup() {
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		for range ticker.C {
			cleanOldTempDirs()
		}
	}()
}

// cleanOldTempDirs deletes top-level directories in os.TempDir() that are older
// than maxTempDirAge.
func cleanOldTempDirs() {
	if !cleanupMu.TryLock() {
		return
	}
	defer cleanupMu.Unlock()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-maxTempDirAge)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(os.TempDir(), entry.Name()))
		}
	}
}
