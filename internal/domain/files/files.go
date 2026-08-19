package files

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mcasperson/MockGitRepo/internal/domain/configuration"
	"github.com/mcasperson/MockGitRepo/internal/domain/logging"
	"github.com/mcasperson/MockGitRepo/internal/domain/security"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	gitRepoPrefix = "git-repo-"

	// LocalTempRoot is the local disk directory used for repository copies that
	// only have to outlive the requests reading them. Copies that must survive a
	// request live under RemoteTempRoot instead.
	LocalTempRoot = "/tmp"

	// copyConcurrency bounds the number of file copies in flight at once.
	// Copying a repository is dominated by per-file round trips rather than by
	// the volume of data moved, because the templates are made up of many small
	// files. On a network filesystem such as an Azure Files mount each create,
	// write and close is a separate round trip, so overlapping the copies is
	// what makes the copy fast.
	copyConcurrency = 32
)

// RemoteTempRoot returns the directory that holds the repository copies which have
// to outlive a single request. In production this is a mounted network file share,
// which is why the first copy into it is slow.
func RemoteTempRoot() string {
	if root := configuration.GetGitTempRoot(); root != "" {
		return root
	}
	return os.TempDir()
}

// TempRepoPath returns the path of the fixed repository copy for fixedPath beneath
// destRoot, without creating or copying anything.
func TempRepoPath(destRoot string, fixedPath string) string {
	return filepath.Join(resolveDestRoot(destRoot), fixedPath)
}

// TempRepoExists reports whether the fixed repository copy for fixedPath already
// exists beneath destRoot.
func TempRepoExists(destRoot string, fixedPath string) (bool, error) {
	if !security.IsValidUsernameOrPath(fixedPath) {
		return false, errors.New("invalid repository path: special characters are not allowed")
	}

	_, err := os.Stat(TempRepoPath(destRoot, fixedPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// CopyRepoToTemp copies the repository directory to a temporary directory
// repoPath is the path to the original repository
// destRoot is the directory the copy is created in, such as RemoteTempRoot or
// LocalTempRoot. An empty string means the system temp directory.
// fixedLocation indicates whether to use a fixed location for the temp directory
// fixedPath is the name of the fixed directory to use if fixedLocation is true
// Returns the path to the temporary directory
func CopyRepoToTemp(repoPath string, destRoot string, fixedLocation bool, fixedPath string) (string, bool, error) {
	if fixedLocation && !security.IsValidUsernameOrPath(fixedPath) {
		return "", false, errors.New("invalid repository path: special characters are not allowed")
	}

	destRoot = resolveDestRoot(destRoot)

	var tempDir string
	if fixedLocation {
		var exists bool
		var err error
		tempDir, exists, err = getOrCreateFixedTempDir(destRoot, fixedPath)
		if err != nil {
			return "", false, err
		}
		if exists {
			return tempDir, false, nil
		}
	} else {
		// Create a temporary directory
		var err error
		tempDir, err = os.MkdirTemp(destRoot, gitRepoPrefix+"*")

		if err != nil {
			logging.Logger.Error("Failed to create temp directory", zap.Error(err))
			return "", false, err
		}
	}

	logging.Logger.Info("Copying repository to temp directory",
		zap.String("repoPath", repoPath),
		zap.String("tempDir", tempDir))

	copyStart := time.Now()

	// Copy the repository to the temp directory
	err := CopyDir(repoPath, tempDir)
	if err != nil {
		logging.Logger.Error("Failed to copy repository",
			zap.String("src", repoPath),
			zap.String("dst", tempDir),
			zap.Duration("duration", time.Since(copyStart)),
			zap.Error(err))
		os.RemoveAll(tempDir)
		return "", false, err
	}

	logging.Logger.Info("Repository copied successfully",
		zap.String("tempDir", tempDir),
		zap.Duration("duration", time.Since(copyStart)))

	return tempDir, true, nil
}

// resolveDestRoot returns the directory a copy is created in, defaulting to the
// system temp directory so that an empty destRoot behaves the way os.MkdirTemp does.
func resolveDestRoot(destRoot string) string {
	if destRoot == "" {
		return os.TempDir()
	}
	return destRoot
}

// getOrCreateFixedTempDir resolves a fixed temp directory path for fixedPath beneath destRoot.
// It returns the path, a boolean indicating whether the directory already existed,
// and any error encountered. If the directory already exists, the caller can skip copying.
func getOrCreateFixedTempDir(destRoot string, fixedPath string) (string, bool, error) {
	tempDir := filepath.Join(destRoot, fixedPath)

	// Early exit if the directory already exists to avoid unnecessary copying and potential conflicts
	_, err := os.Stat(tempDir)
	if err != nil {
		// We expect an error when the directory doesn't exist,
		// but if it's a different error, we should return it
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}

		// Create the directory if it doesn't exist
		if mkdirErr := os.MkdirAll(tempDir, 0755); mkdirErr != nil {
			return "", false, mkdirErr
		}

		return tempDir, false, nil
	}

	// The directory already exists, so we can skip copying and return it directly
	return tempDir, true, nil
}

// scannedEntry is a single directory or file found beneath the source tree,
// recorded as a path relative to the root of that tree.
type scannedEntry struct {
	relPath string
	mode    os.FileMode
	// depth is the number of path separators in relPath, and is only used to
	// order directory creation.
	depth int
}

// CopyDir recursively copies a directory from src to dst.
//
// The source tree is scanned up front, then directories are created one depth
// level at a time and the files are copied concurrently. Walking the source is
// cheap compared to writing to the destination, so doing the traversal
// separately lets every write to the destination overlap with the others.
func CopyDir(src, dst string) error {
	// Get source directory info
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	// Create destination directory
	err = os.MkdirAll(dst, srcInfo.Mode())
	if err != nil {
		return err
	}

	dirs, entryFiles, err := scanDir(src)
	if err != nil {
		return err
	}

	if err := createDirs(dst, dirs); err != nil {
		return err
	}

	return copyFiles(src, dst, entryFiles)
}

// isMacMetadata reports whether name is a macOS metadata file that must never be
// copied into a repository.
//
// An AppleDouble file is a sidecar named "._" plus the name of the file it
// belongs to, and macOS writes one whenever a file carrying extended attributes
// lands on a filesystem that cannot store them natively, such as an SMB mounted
// file share. Git finds packfiles by scanning objects/pack and treats every .idx
// it sees there as a pack index, so a ._pack-*.idx sidecar is read as a truncated
// index and reported as "index file is too small". That breaks every fetch from
// the repository, and clones made from it inherit the sidecar.
func isMacMetadata(name string) bool {
	return name == ".DS_Store" || strings.HasPrefix(name, "._")
}

// scanDir walks src and returns the directories and files beneath it, excluding
// src itself. Anything that is not a directory is treated as a file, so symlinks
// are copied by content in the same way the destination sees them. macOS metadata
// files are left behind rather than copied.
func scanDir(src string) (dirs []scannedEntry, entryFiles []scannedEntry, err error) {
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if isMacMetadata(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		// The root is created by the caller
		if relPath == "." {
			return nil
		}

		// DirEntry.Info is served from the directory read, so this does not cost
		// an extra stat of the source
		info, err := d.Info()
		if err != nil {
			return err
		}

		entry := scannedEntry{
			relPath: relPath,
			mode:    info.Mode(),
			depth:   strings.Count(relPath, string(os.PathSeparator)),
		}

		if d.IsDir() {
			dirs = append(dirs, entry)
		} else {
			entryFiles = append(entryFiles, entry)
		}

		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	return dirs, entryFiles, nil
}

// createDirs creates dirs beneath dst. Directories are grouped by depth so that
// every parent exists before its children are attempted, while siblings at the
// same depth are created concurrently.
func createDirs(dst string, dirs []scannedEntry) error {
	maxDepth := 0
	for _, dir := range dirs {
		if dir.depth > maxDepth {
			maxDepth = dir.depth
		}
	}

	for depth := 0; depth <= maxDepth; depth++ {
		var group errgroup.Group
		group.SetLimit(copyConcurrency)

		for _, dir := range dirs {
			if dir.depth != depth {
				continue
			}

			group.Go(func() error {
				err := os.Mkdir(filepath.Join(dst, dir.relPath), dir.mode)
				// A directory left over from an earlier copy is not a failure
				if err != nil && !os.IsExist(err) {
					return err
				}
				return nil
			})
		}

		if err := group.Wait(); err != nil {
			return err
		}
	}

	return nil
}

// copyFiles copies entryFiles from src to dst concurrently. The directories they
// live in must already exist.
func copyFiles(src, dst string, entryFiles []scannedEntry) error {
	var group errgroup.Group
	group.SetLimit(copyConcurrency)

	for _, file := range entryFiles {
		group.Go(func() error {
			return copyFileWithMode(
				filepath.Join(src, file.relPath),
				filepath.Join(dst, file.relPath),
				file.mode)
		})
	}

	return group.Wait()
}

// CopyFile copies a single file from src to dst, preserving its permissions
func CopyFile(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	return copyFileWithMode(src, dst, srcInfo.Mode())
}

// copyFileWithMode copies src to dst, creating dst with the given mode.
//
// The mode is applied when the file is created rather than by a follow up
// Chmod, which saves a round trip per file. Note that this means the mode is
// masked by the process umask, where an explicit Chmod would not be.
func copyFileWithMode(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		return err
	}

	// Closing a file on a network filesystem flushes the write, so the error
	// matters here
	return dstFile.Close()
}

// LimitTempDirs ensures there are no more than maxDirs temp directories
// by deleting the oldest directories if the limit is exceeded
func LimitTempDirs(maxDirs int) {
	tmpDir := LocalTempRoot

	logging.Logger.Debug("Checking temp directory count limit",
		zap.Int("maxDirs", maxDirs))

	// Read all entries in /tmp
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		logging.Logger.Error("Failed to read tmp directory", zap.Error(err))
		return
	}

	// Collect all git-repo directories with their modification times
	type dirInfo struct {
		name    string
		modTime time.Time
	}
	var gitRepoDirs []dirInfo

	for _, entry := range entries {
		// Check if it's a directory and starts with the prefix
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), gitRepoPrefix) {
			continue
		}

		// Get full path
		fullPath := filepath.Join(tmpDir, entry.Name())

		// Get directory info to check modification time
		info, err := os.Stat(fullPath)
		if err != nil {
			logging.Logger.Warn("Failed to stat temp directory",
				zap.String("path", fullPath),
				zap.Error(err))
			continue
		}

		gitRepoDirs = append(gitRepoDirs, dirInfo{
			name:    entry.Name(),
			modTime: info.ModTime(),
		})
	}

	// Check if we have more than maxDirs directories
	dirCount := len(gitRepoDirs)
	if dirCount <= maxDirs {
		logging.Logger.Debug("Temp directory count within limit",
			zap.Int("count", dirCount),
			zap.Int("limit", maxDirs))
		return
	}

	logging.Logger.Info("Temp directory count exceeds limit, cleaning up",
		zap.Int("count", dirCount),
		zap.Int("limit", maxDirs),
		zap.Int("toDelete", dirCount-maxDirs))

	// Sort directories by modification time (oldest first)
	// Using a simple bubble sort for clarity
	for i := 0; i < len(gitRepoDirs)-1; i++ {
		for j := 0; j < len(gitRepoDirs)-i-1; j++ {
			if gitRepoDirs[j].modTime.After(gitRepoDirs[j+1].modTime) {
				gitRepoDirs[j], gitRepoDirs[j+1] = gitRepoDirs[j+1], gitRepoDirs[j]
			}
		}
	}

	// Delete oldest directories until we're at the limit
	numToDelete := dirCount - maxDirs
	deletedCount := 0

	for i := 0; i < numToDelete; i++ {
		fullPath := filepath.Join(tmpDir, gitRepoDirs[i].name)
		err := os.RemoveAll(fullPath)
		if err != nil {
			logging.Logger.Error("Failed to remove old temp directory",
				zap.String("path", fullPath),
				zap.Error(err))
		} else {
			logging.Logger.Info("Removed old temp directory to enforce limit",
				zap.String("path", fullPath),
				zap.Time("modTime", gitRepoDirs[i].modTime))
			deletedCount++
		}
	}

	logging.Logger.Info("Temp directory limit enforcement completed",
		zap.Int("deletedCount", deletedCount),
		zap.Int("remaining", dirCount-deletedCount))
}

// backgroundCopies tracks the fixed destinations a call to CopyRepoToTempAsync is
// still writing to. The destination directory is created before it is populated, so
// its presence on disk is not enough to know that it holds a usable repository.
var (
	backgroundCopiesMu sync.Mutex
	backgroundCopies   = map[string]struct{}{}
)

// CopyRepoToTempAsync starts a copy of repoPath into the fixed copy for fixedPath
// beneath destRoot and returns straight away, leaving the copy to finish in the
// background. It is used for the copies onto the network file share, where the caller
// has somewhere faster to read the repository from and only needs the share to catch
// up eventually.
//
// Only one background copy per destination runs at a time, so a call made while an
// earlier copy is still running does nothing.
func CopyRepoToTempAsync(repoPath string, destRoot string, fixedPath string) {
	dest := TempRepoPath(destRoot, fixedPath)

	backgroundCopiesMu.Lock()
	if _, running := backgroundCopies[dest]; running {
		backgroundCopiesMu.Unlock()
		return
	}
	backgroundCopies[dest] = struct{}{}
	backgroundCopiesMu.Unlock()

	go func() {
		defer func() {
			backgroundCopiesMu.Lock()
			delete(backgroundCopies, dest)
			backgroundCopiesMu.Unlock()
		}()

		if _, _, err := CopyRepoToTemp(repoPath, destRoot, true, fixedPath); err != nil {
			logging.Logger.Error("Background repository copy failed",
				zap.String("repoPath", repoPath),
				zap.String("dest", dest),
				zap.Error(err))
		}
	}()
}

// TempRepoReady reports whether the fixed repository copy for fixedPath beneath
// destRoot can be served, meaning it exists and no background copy is still writing
// to it.
func TempRepoReady(destRoot string, fixedPath string) (bool, error) {
	dest := TempRepoPath(destRoot, fixedPath)

	backgroundCopiesMu.Lock()
	_, running := backgroundCopies[dest]
	backgroundCopiesMu.Unlock()

	if running {
		return false, nil
	}

	return TempRepoExists(destRoot, fixedPath)
}
