package handlers

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/mcasperson/MockGitRepo/internal/domain/configuration"
	"github.com/mcasperson/MockGitRepo/internal/domain/files"
	"github.com/mcasperson/MockGitRepo/internal/domain/logging"
	"github.com/mcasperson/MockGitRepo/internal/domain/security"
	"github.com/mcasperson/MockGitRepo/internal/infrastructure"
	"go.uber.org/zap"
)

var gitHTTPBackendMu sync.Mutex

const (
	maxRequestSize = 128 * 1024 // 128KB in bytes
)

// GitHTTPBackend handles Git HTTP requests using git-http-backend CGI
func GitHTTPBackend(c *gin.Context) {
	if !gitHTTPBackendMu.TryLock() {
		c.String(http.StatusServiceUnavailable, "Server busy, please retry")
		return
	}
	defer gitHTTPBackendMu.Unlock()

	logging.Logger.Info("Git HTTP request received",
		zap.String("method", c.Request.Method),
		zap.String("path", c.Param("path")),
		zap.String("clientIP", c.ClientIP()))

	// Limit the number of temp directories to 20
	files.LimitTempDirs(20)

	// Check if Authorization header is present
	if c.GetHeader("Authorization") == "" {
		logging.Logger.Warn("No Authorization header provided",
			zap.String("clientIP", c.ClientIP()),
			zap.String("path", c.Param("path")))
		c.Header("WWW-Authenticate", `Basic realm="Git Repository"`)
		c.String(http.StatusUnauthorized, "Authorization required")
		return
	}

	// Check request size limit (128KB)
	if c.Request.ContentLength > maxRequestSize {
		logging.Logger.Warn("Request size exceeds limit",
			zap.Int64("contentLength", c.Request.ContentLength),
			zap.Int64("maxSize", maxRequestSize),
			zap.String("clientIP", c.ClientIP()))
		c.String(http.StatusBadRequest, "Request size exceeds maximum allowed size of 128KB")
		return
	}

	username, password, err := extractUsernamePassword(c.GetHeader("Authorization"))

	if err != nil {
		logging.Logger.Error("Failed to extract username and password")
		c.String(http.StatusInternalServerError, "Failed to extract username and password: %s", err)
		return
	}

	if !security.IsValidUsernameOrPath(username) {
		logging.Logger.Error("Usernames must be alphanumeric chars and dashes")
		c.String(http.StatusBadRequest, "Usernames must be alphanumeric chars and dashes")
		return
	}

	// Get the original repository path
	gitProjectRoot := configuration.GetGitProjectRoot()

	// Construct the full path to the repository using only the first directory
	repoPath := filepath.Join(gitProjectRoot, "repotemplate")

	userExists, err := infrastructure.TestCredentials(username, password)

	if err != nil {
		logging.Logger.Error("Failed to test for user in database", zap.Error(err))
		c.String(http.StatusInternalServerError, "Failed to test for user in database")
		return
	}

	// Copy repository to temporary directory
	tempRepoPath, created, err := files.CopyRepoToTemp(repoPath, userExists, username)
	if err != nil {
		logging.Logger.Error("Failed to copy repository to temp",
			zap.String("repoPath", repoPath),
			zap.Error(err))
		c.String(http.StatusInternalServerError, "Failed to copy repository: %s", err)
		return
	}

	if created {
		logging.Logger.Info("Created new directory for " + username + ".")
	} else {
		logging.Logger.Info("Directory for " + username + " already exists.")
	}

	if userExists {
		logging.Logger.Info("Found user " + username + ". Delaying repo cleanup.")
	} else {
		defer func() {
			logging.Logger.Info("Anonymous user " + username + " will be cleaned up immediately after request.")

			err := os.RemoveAll(tempRepoPath)
			if err != nil {
				logging.Logger.Error("Failed to delete temp directory",
					zap.String("tempRepoPath", tempRepoPath),
					zap.Error(err))
			} else {
				logging.Logger.Info("Deleted temp directory",
					zap.String("tempRepoPath", tempRepoPath))
			}
		}()
	}

	logging.Logger.Debug("Executing git-http-backend",
		zap.String("tempRepoPath", tempRepoPath))

	// Create the command
	cmd := exec.Command(configuration.GetGitHttpBackend())

	// Set up CGI environment variables with temp repo path
	cmd.Env = setupCGIEnvironment(c, tempRepoPath)

	// Capture stdin for POST requests
	if err := handlePOSTRequestBody(c, cmd); err != nil {
		logging.Logger.Error("Failed to read request body", zap.Error(err))
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
		logging.Logger.Error("CGI execution failed",
			zap.Error(err),
			zap.String("stderr", stderr.String()))
		c.String(http.StatusInternalServerError, "CGI execution failed: %s\nStderr: %s", err, stderr.String())
		return
	}

	logging.Logger.Debug("CGI execution successful",
		zap.Int("outputSize", stdout.Len()))

	// Parse CGI response
	output := stdout.String()
	headerLines, body := parseCGIResponse(output)

	// Parse headers
	parseCGIHeaders(c, headerLines)

	// Determine status code from Status header or default to 200
	statusCode := parseStatusCode(c)

	logging.Logger.Info("Git HTTP request completed",
		zap.Int("statusCode", statusCode),
		zap.Int("bodySize", len(body)))

	c.Data(statusCode, c.Writer.Header().Get("Content-Type"), body)
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

func extractUsernamePassword(authorization string) (string, string, error) {
	const prefix = "Basic "
	if !strings.HasPrefix(authorization, prefix) {
		return "", "", errors.New("invalid authorization header")
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(authorization, prefix))
	if err != nil {
		return "", "", err
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", errors.New("invalid authorization header")
	}

	return parts[0], parts[1], nil
}
