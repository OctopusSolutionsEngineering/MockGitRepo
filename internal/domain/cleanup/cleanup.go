package cleanup

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/mcasperson/MockGitRepo/internal/domain/files"
)

// GitHTTPBackendMu coordinates active git HTTP requests with the cleanup goroutine.
// GitHTTPBackend holds a read lock for each in-flight request; cleanOldTempDirs
// attempts the write lock and skips if it cannot be obtained.
var GitHTTPBackendMu sync.RWMutex

const (
	cleanupInterval = 1 * time.Hour
	maxTempDirAge   = 8 * time.Hour
)

// StartTempDirCleanup launches a background goroutine that runs every hour and
// removes any top-level directories inside the temp roots that are older than 8 hours.
func StartTempDirCleanup() {
	go func() {
		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		for range ticker.C {
			cleanOldTempDirs()
		}
	}()
}

// cleanOldTempDirs deletes top-level directories in the temp roots that are older
// than maxTempDirAge. Repositories are copied to local disk as well as to the remote
// file system, so both roots have to be swept.
func cleanOldTempDirs() {
	if !GitHTTPBackendMu.TryLock() {
		return
	}
	defer GitHTTPBackendMu.Unlock()

	cutoff := time.Now().Add(-maxTempDirAge)

	for _, root := range tempRoots() {
		cleanOldTempDirsIn(root, cutoff)
	}
}

// tempRoots returns the distinct directories that hold repository copies. Copies are
// made on both the remote file system and local disk, and the two are only the same
// directory when the remote root is left unconfigured.
func tempRoots() []string {
	var roots []string

	for _, root := range []string{os.TempDir(), files.RemoteTempRoot(), files.LocalTempRoot} {
		if !slices.Contains(roots, root) {
			roots = append(roots, root)
		}
	}

	return roots
}

// cleanOldTempDirsIn deletes top-level directories in root last modified before cutoff.
func cleanOldTempDirsIn(root string, cutoff time.Time) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
}
