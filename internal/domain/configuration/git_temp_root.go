package configuration

import "os"

// GetGitTempRoot returns the root directory used for temporary git repositories.
// When not set, os.MkdirTemp will use the system default temporary directory.
func GetGitTempRoot() string {
	return os.Getenv("GIT_TEMP_ROOT")
}
