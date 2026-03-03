package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	gitHTTPBackendPath = "/usr/libexec/git-core/git-http-backend"
	maxRequestSize     = 128 * 1024 // 128KB in bytes
	gitRepoPrefix      = "git-repo-"
)

var logger *zap.Logger

// extractUsername extracts the username from the Basic Auth Authorization header
func extractUsername(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		logger.Debug("No Authorization header found")
		return ""
	}

	// Basic auth format: "Basic base64(username:password)"
	const prefix = "Basic "
	if !strings.HasPrefix(authHeader, prefix) {
		logger.Warn("Authorization header does not use Basic auth")
		return ""
	}

	// Decode base64
	encoded := authHeader[len(prefix):]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		logger.Error("Failed to decode base64 credentials", zap.Error(err))
		return ""
	}

	// Extract username (before the colon)
	credentials := string(decoded)
	parts := strings.SplitN(credentials, ":", 2)
	if len(parts) == 0 {
		logger.Warn("Invalid credentials format")
		return ""
	}

	username := parts[0]
	logger.Info("User authenticated", zap.String("username", username))
	return username
}

// copyRepoToTemp copies the repository directory to a temporary directory
// Returns the path to the temporary directory
func copyRepoToTemp(repoPath string) (string, error) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", gitRepoPrefix+"*")

	if err != nil {
		logger.Error("Failed to create temp directory", zap.Error(err))
		return "", err
	}

	logger.Info("Copying repository to temp directory",
		zap.String("repoPath", repoPath))

	// Copy the repository to the temp directory
	err = copyDir(repoPath, tempDir)
	if err != nil {
		logger.Error("Failed to copy repository",
			zap.String("src", repoPath),
			zap.String("dst", tempDir),
			zap.Error(err))
		os.RemoveAll(tempDir)
		return "", err
	}

	logger.Info("Repository copied successfully",
		zap.String("tempDir", tempDir))
	return tempDir, nil
}

// copyDir recursively copies a directory from src to dst
func copyDir(src, dst string) error {
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
			err = copyDir(srcPath, dstPath)
			if err != nil {
				return err
			}
		} else {
			// Copy file
			err = copyFile(srcPath, dstPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFile copies a single file from src to dst
func copyFile(src, dst string) error {
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

// setupCGIEnvironment configures the CGI environment variables for git-http-backend
func setupCGIEnvironment(c *gin.Context, tempRepoPath string) []string {
	env := []string{}
	env = append(env, "REQUEST_METHOD="+c.Request.Method)
	env = append(env, "QUERY_STRING="+c.Request.URL.RawQuery)
	env = append(env, "CONTENT_TYPE="+c.GetHeader("Content-Type"))

	// Use the repository name from the path parameter
	env = append(env, "PATH_INFO="+c.Param("path"))

	env = append(env, "REMOTE_USER="+c.GetHeader("Remote-User"))
	env = append(env, "REMOTE_ADDR="+c.ClientIP())
	env = append(env, "CONTENT_LENGTH="+c.GetHeader("Content-Length"))
	env = append(env, "SERVER_SOFTWARE=Gin-Git-Server")
	env = append(env, "SERVER_PROTOCOL="+c.Request.Proto)
	env = append(env, "HTTP_USER_AGENT="+c.GetHeader("User-Agent"))
	env = append(env, "HTTP_ACCEPT="+c.GetHeader("Accept"))
	env = append(env, "HTTP_ACCEPT_ENCODING="+c.GetHeader("Accept-Encoding"))
	env = append(env, "HTTP_ACCEPT_LANGUAGE="+c.GetHeader("Accept-Language"))

	// Use the temp directory as the Git project root
	env = append(env, "GIT_PROJECT_ROOT="+tempRepoPath)
	env = append(env, "GIT_HTTP_EXPORT_ALL=1") // Allow all repos to be exported

	return env
}

// parseCGIHeaders parses CGI response headers and sets them on the Gin context
func parseCGIHeaders(c *gin.Context, headerLines []string) {
	for _, line := range headerLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		headerParts := strings.SplitN(line, ":", 2)
		if len(headerParts) == 2 {
			key := strings.TrimSpace(headerParts[0])
			value := strings.TrimSpace(headerParts[1])
			c.Header(key, value)
		}
	}
}

// parseCGIResponse splits CGI output into headers and body
func parseCGIResponse(output string) ([]string, []byte) {
	parts := strings.SplitN(output, "\r\n\r\n", 2)
	if len(parts) < 2 {
		parts = strings.SplitN(output, "\n\n", 2)
	}

	headerLines := strings.Split(parts[0], "\n")

	var body []byte
	if len(parts) == 2 {
		body = []byte(parts[1])
	} else {
		body = []byte{}
	}

	return headerLines, body
}

// handlePOSTRequestBody reads the request body for POST requests and sets it as stdin for the command
func handlePOSTRequestBody(c *gin.Context, cmd *exec.Cmd) error {
	if c.Request.Method == "POST" {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return err
		}
		cmd.Stdin = bytes.NewReader(body)
	}
	return nil
}

// parseStatusCode extracts the HTTP status code from the CGI Status header
func parseStatusCode(c *gin.Context) int {
	statusCode := http.StatusOK
	if status := c.Writer.Header().Get("Status"); status != "" {
		c.Writer.Header().Del("Status")
		// Parse status code from "200 OK" format
		if len(status) >= 3 {
			if code, err := strconv.Atoi(status[:3]); err == nil {
				statusCode = code
			}
		}
	}
	return statusCode
}

// getGitProjectRoot returns the git project root from environment variable or default
func getGitProjectRoot() string {
	gitProjectRoot := os.Getenv("GIT_PROJECT_ROOT")
	if gitProjectRoot == "" {
		gitProjectRoot = "/data/repos"
	}
	return gitProjectRoot
}

// limitTempDirs ensures there are no more than maxDirs temp directories
// by deleting the oldest directories if the limit is exceeded
func limitTempDirs(maxDirs int) {
	tmpDir := "/tmp"

	logger.Debug("Checking temp directory count limit",
		zap.Int("maxDirs", maxDirs))

	// Read all entries in /tmp
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		logger.Error("Failed to read tmp directory", zap.Error(err))
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
			logger.Warn("Failed to stat temp directory",
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
		logger.Debug("Temp directory count within limit",
			zap.Int("count", dirCount),
			zap.Int("limit", maxDirs))
		return
	}

	logger.Info("Temp directory count exceeds limit, cleaning up",
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
			logger.Error("Failed to remove old temp directory",
				zap.String("path", fullPath),
				zap.Error(err))
		} else {
			logger.Info("Removed old temp directory to enforce limit",
				zap.String("path", fullPath),
				zap.Time("modTime", gitRepoDirs[i].modTime))
			deletedCount++
		}
	}

	logger.Info("Temp directory limit enforcement completed",
		zap.Int("deletedCount", deletedCount),
		zap.Int("remaining", dirCount-deletedCount))
}

// gitHTTPBackend handles Git HTTP requests using git-http-backend CGI
func gitHTTPBackend(c *gin.Context) {
	logger.Info("Git HTTP request received",
		zap.String("method", c.Request.Method),
		zap.String("path", c.Param("path")),
		zap.String("clientIP", c.ClientIP()))

	// Limit the number of temp directories to 20
	limitTempDirs(20)

	// Check if Authorization header is present
	if c.GetHeader("Authorization") == "" {
		logger.Warn("No Authorization header provided",
			zap.String("clientIP", c.ClientIP()),
			zap.String("path", c.Param("path")))
		c.Header("WWW-Authenticate", `Basic realm="Git Repository"`)
		c.String(http.StatusUnauthorized, "Authorization required")
		return
	}

	// Check request size limit (128KB)
	if c.Request.ContentLength > maxRequestSize {
		logger.Warn("Request size exceeds limit",
			zap.Int64("contentLength", c.Request.ContentLength),
			zap.Int64("maxSize", maxRequestSize),
			zap.String("clientIP", c.ClientIP()))
		c.String(http.StatusBadRequest, "Request size exceeds maximum allowed size of 128KB")
		return
	}

	// Get the original repository path
	gitProjectRoot := getGitProjectRoot()

	// Construct the full path to the repository using only the first directory
	repoPath := filepath.Join(gitProjectRoot, "repotemplate")

	// Copy repository to temporary directory
	tempRepoPath, err := copyRepoToTemp(repoPath)
	if err != nil {
		logger.Error("Failed to copy repository to temp",
			zap.String("repoPath", repoPath),
			zap.Error(err))
		c.String(http.StatusInternalServerError, "Failed to copy repository: %s", err)
		return
	}

	// Defer deletion of the temporary directory
	defer func() {
		err := os.RemoveAll(tempRepoPath)
		if err != nil {
			logger.Error("Failed to delete temp directory",
				zap.String("tempRepoPath", tempRepoPath),
				zap.Error(err))
		} else {
			logger.Info("Deleted temp directory",
				zap.String("tempRepoPath", tempRepoPath))
		}
	}()

	logger.Debug("Executing git-http-backend",
		zap.String("tempRepoPath", tempRepoPath))

	// Create the command
	cmd := exec.Command(gitHTTPBackendPath)

	// Set up CGI environment variables with temp repo path
	cmd.Env = setupCGIEnvironment(c, tempRepoPath)

	// Capture stdin for POST requests
	if err := handlePOSTRequestBody(c, cmd); err != nil {
		logger.Error("Failed to read request body", zap.Error(err))
		c.String(http.StatusInternalServerError, "Failed to read request body")
		return
	}

	// Capture stdout and stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute the CGI script
	err = cmd.Run()
	if err != nil {
		logger.Error("CGI execution failed",
			zap.Error(err),
			zap.String("stderr", stderr.String()))
		c.String(http.StatusInternalServerError, "CGI execution failed: %s\nStderr: %s", err, stderr.String())
		return
	}

	logger.Debug("CGI execution successful",
		zap.Int("outputSize", stdout.Len()))

	// Parse CGI response
	output := stdout.String()
	headerLines, body := parseCGIResponse(output)

	// Parse headers
	parseCGIHeaders(c, headerLines)

	// Determine status code from Status header or default to 200
	statusCode := parseStatusCode(c)

	logger.Info("Git HTTP request completed",
		zap.Int("statusCode", statusCode),
		zap.Int("bodySize", len(body)))

	c.Data(statusCode, c.Writer.Header().Get("Content-Type"), body)
}

// cleanupOldTempDirs removes temporary git directories older than the specified duration
func cleanupOldTempDirs(maxAge time.Duration) {
	tmpDir := "/tmp"
	prefix := "git-repo-"

	logger.Debug("Starting cleanup of old temp directories",
		zap.String("tmpDir", tmpDir),
		zap.Duration("maxAge", maxAge))

	// Read all entries in /tmp
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		logger.Error("Failed to read tmp directory", zap.Error(err))
		return
	}

	now := time.Now()
	cleanedCount := 0

	for _, entry := range entries {
		// Check if it's a directory and starts with the prefix
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}

		// Get full path
		fullPath := filepath.Join(tmpDir, entry.Name())

		// Get directory info to check modification time
		info, err := os.Stat(fullPath)
		if err != nil {
			logger.Warn("Failed to stat temp directory",
				zap.String("path", fullPath),
				zap.Error(err))
			continue
		}

		// Check if directory is older than maxAge
		age := now.Sub(info.ModTime())
		if age > maxAge {
			// Remove the directory
			err := os.RemoveAll(fullPath)
			if err != nil {
				logger.Error("Failed to remove old temp directory",
					zap.String("path", fullPath),
					zap.Duration("age", age),
					zap.Error(err))
			} else {
				logger.Info("Removed old temp directory",
					zap.String("path", fullPath),
					zap.Duration("age", age))
				cleanedCount++
			}
		}
	}

	logger.Info("Cleanup completed",
		zap.Int("cleanedCount", cleanedCount))
}

// startCleanupWorker starts a background goroutine that periodically cleans up old temp directories
func startCleanupWorker() {
	go func() {
		// Run cleanup every 10 minutes
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		// Run cleanup immediately on start
		cleanupOldTempDirs(90 * time.Minute)

		for range ticker.C {
			cleanupOldTempDirs(90 * time.Minute)
		}
	}()
}

func main() {
	// Initialize logger with plain text output and no stack traces
	var err error
	config := zap.NewDevelopmentConfig()
	config.DisableStacktrace = true
	logger, err = config.Build()
	if err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}
	defer logger.Sync()

	logger.Info("Starting Gin Git Server")

	// Start background cleanup worker
	startCleanupWorker()
	logger.Info("Background cleanup worker started")

	// Create a new Gin router with default middleware
	router := gin.Default()

	// Git HTTP backend routes
	// Match all Git HTTP protocol routes
	router.Any("/repo/*path", gitHTTPBackend)

	// This allows us to insert a random id into the URL, which bypasses
	// the constraint in Octopus where a git repo can only be used one.
	router.Any("/uniquerepo/:id/*path", gitHTTPBackend)

	// Get port from environment variable or default to 8080
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	logger.Info("Starting HTTP server", zap.String("port", port))
	// Start the server
	if err := router.Run(":" + port); err != nil {
		logger.Fatal("Failed to start server", zap.Error(err))
	}
}
