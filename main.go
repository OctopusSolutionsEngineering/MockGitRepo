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
func copyRepoToTemp(repoPath string, username string) (string, error) {
	// Create a temporary directory
	tempDir := "/tmp/git-repo-" + username

	// Check if the target directory already exists
	if _, err := os.Stat(tempDir); err == nil {
		logger.Info("Temp directory already exists, skipping copy",
			zap.String("tempDir", tempDir),
			zap.String("username", username))
		return tempDir, nil
	}

	logger.Info("Copying repository to temp directory",
		zap.String("repoPath", repoPath),
		zap.String("username", username))

	err := os.Mkdir(tempDir, 0700)
	if err != nil {
		logger.Error("Failed to create temp directory",
			zap.String("tempDir", tempDir),
			zap.Error(err))
		return "", err
	}

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

// gitHTTPBackend handles Git HTTP requests using git-http-backend CGI
func gitHTTPBackend(c *gin.Context) {
	logger.Info("Git HTTP request received",
		zap.String("method", c.Request.Method),
		zap.String("path", c.Param("path")),
		zap.String("clientIP", c.ClientIP()))

	// Check if Authorization header is present
	if c.GetHeader("Authorization") == "" {
		logger.Warn("No Authorization header provided",
			zap.String("clientIP", c.ClientIP()),
			zap.String("path", c.Param("path")))
		c.Header("WWW-Authenticate", `Basic realm="Git Repository"`)
		c.String(http.StatusUnauthorized, "Authorization required")
		return
	}

	// Extract username from Authorization header
	username := extractUsername(c)

	// Get the original repository path
	gitProjectRoot := getGitProjectRoot()

	// Construct the full path to the repository using only the first directory
	repoPath := filepath.Join(gitProjectRoot, "repotemplate")

	// Copy repository to temporary directory
	tempRepoPath, err := copyRepoToTemp("repotemplate", username)
	if err != nil {
		logger.Error("Failed to copy repository to temp",
			zap.String("repoPath", repoPath),
			zap.Error(err))
		c.String(http.StatusInternalServerError, "Failed to copy repository: %s", err)
		return
	}

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
	router.Any("/*path", gitHTTPBackend)

	logger.Info("Starting HTTP server", zap.String("port", "8080"))
	// Start the server on port 8080
	if err := router.Run(":8080"); err != nil {
		logger.Fatal("Failed to start server", zap.Error(err))
	}
}
