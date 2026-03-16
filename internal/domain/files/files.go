package files

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mcasperson/MockGitRepo/internal/domain/logging"
	"github.com/mcasperson/MockGitRepo/internal/domain/security"
	"go.uber.org/zap"
)

const (
	gitRepoPrefix = "git-repo-"
)

// CopyRepoToTemp copies the repository directory to a temporary directory
// repoPath is the path to the original repository
// fixedLocation indicates whether to use a fixed location for the temp directory
// fixedPath is the name of the fixed directory to use if fixedLocation is true
// Returns the path to the temporary directory
func CopyRepoToTemp(repoPath string, fixedLocation bool, fixedPath string) (string, error) {
	if fixedLocation && !security.IsValidUsernameOrPath(fixedPath) {
		return "", errors.New("invalid repository path: special characters are not allowed")
	}

	var tempDir string
	if fixedLocation {
		tempDir = filepath.Join(os.TempDir(), fixedPath)
	} else {
		// Create a temporary directory
		var err error
		tempDir, err = os.MkdirTemp("", gitRepoPrefix+"*")

		if err != nil {
			logging.Logger.Error("Failed to create temp directory", zap.Error(err))
			return "", err
		}
	}

	_, err := os.Stat(tempDir)
	if err != nil {
		// We expect an error when the directory doesn't exist,
		// but if it's a different error, we should return it
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		// Create the directory if it doesn't exist
		mkdirErr := os.MkdirAll(tempDir, 0755)
		if mkdirErr != nil {
			return "", mkdirErr
		}
	} else {
		// The directory already exists, so we can skip copying and return it directly
		return tempDir, nil
	}

	logging.Logger.Info("Copying repository to temp directory",
		zap.String("repoPath", repoPath))

	// Copy the repository to the temp directory
	err = CopyDir(repoPath, tempDir)
	if err != nil {
		logging.Logger.Error("Failed to copy repository",
			zap.String("src", repoPath),
			zap.String("dst", tempDir),
			zap.Error(err))
		os.RemoveAll(tempDir)
		return "", err
	}

	logging.Logger.Info("Repository copied successfully",
		zap.String("tempDir", tempDir))

	return tempDir, nil
}

// CopyDir recursively copies a directory from src to dst
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

	// Read source directory
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	// Copy each entry
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			// Recursively copy subdirectories
			err = CopyDir(srcPath, dstPath)
			if err != nil {
				return err
			}
		} else {
			// Copy file
			err = CopyFile(srcPath, dstPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// CopyFile copies a single file from src to dst
func CopyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	// Copy file permissions
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, srcInfo.Mode())
}

// LimitTempDirs ensures there are no more than maxDirs temp directories
// by deleting the oldest directories if the limit is exceeded
func LimitTempDirs(maxDirs int) {
	tmpDir := "/tmp"

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
