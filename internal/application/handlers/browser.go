package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mcasperson/MockGitRepo/internal/domain/configuration"
	"github.com/mcasperson/MockGitRepo/internal/domain/files"
	"github.com/mcasperson/MockGitRepo/internal/domain/logging"
	"github.com/mcasperson/MockGitRepo/internal/domain/security"
	"go.uber.org/zap"
)

// gitCommand runs a git command in the repo directory and returns stdout
func gitCommand(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		logging.Logger.Error("git command failed",
			zap.Strings("args", args),
			zap.String("stderr", stderr.String()),
			zap.Error(err))
		return "", err
	}
	return stdout.String(), nil
}

// listBranches returns all branches in the repository that contain the given path.
// If filePath is empty, all branches are returned.
func listBranches(repoPath string, filePath string) ([]string, error) {
	output, err := gitCommand(repoPath, "branch", "--list", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if filePath != "" {
			// Verify the path exists in this branch
			_, verifyErr := gitCommand(repoPath, "cat-file", "-t", line+":"+filePath)
			if verifyErr != nil {
				continue
			}
		}
		branches = append(branches, line)
	}
	return branches, nil
}

// getDefaultBranch returns the default branch (HEAD reference)
func getDefaultBranch(repoPath string) string {
	output, err := gitCommand(repoPath, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "main"
	}
	return strings.TrimSpace(output)
}

// listTree lists files/directories at a given path in a branch
func listTree(repoPath, branch, treePath string) ([]TreeEntry, error) {
	ref := branch + ":" + treePath
	if treePath == "" || treePath == "/" {
		ref = branch
	}

	output, err := gitCommand(repoPath, "ls-tree", ref)
	if err != nil {
		return nil, err
	}

	var entries []TreeEntry
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		// format: <mode> <type> <hash>\t<name>
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		meta := strings.Fields(parts[0])
		if len(meta) < 3 {
			continue
		}
		entries = append(entries, TreeEntry{
			Mode: meta[0],
			Type: meta[1],
			Name: parts[1],
		})
	}
	return entries, nil
}

// getFileContent returns the content of a file at a given path in a branch.
// Returns an error if the file exceeds 128KB.
func getFileContent(repoPath, branch, filePath string) (string, error) {
	ref := branch + ":" + filePath

	// Check file size using cat-file -s
	sizeOutput, err := gitCommand(repoPath, "cat-file", "-s", ref)
	if err != nil {
		return "", err
	}

	sizeStr := strings.TrimSpace(sizeOutput)
	var size int64
	for _, ch := range sizeStr {
		if ch < '0' || ch > '9' {
			break
		}
		size = size*10 + int64(ch-'0')
	}

	const maxFileSize = 128 * 1024 // 128KB
	if size > maxFileSize {
		return "", fmt.Errorf("file is too large to display (%d bytes, maximum is %d bytes)", size, maxFileSize)
	}

	output, err := gitCommand(repoPath, "show", ref)
	if err != nil {
		return "", err
	}
	return output, nil
}

// TreeEntry represents a file or directory in the git tree
type TreeEntry struct {
	Mode string
	Type string // "blob" or "tree"
	Name string
}

// BrowserData holds the data passed to the browser template
type BrowserData struct {
	Branches      []string
	CurrentBranch string
	CurrentPath   string
	ParentPath    string
	RepoName      string
	Entries       []TreeEntry
	FileContent   string
	FileName      string
	IsFile        bool
	IsRepoList    bool
	Username      string
	Error         string
}

// GitBrowser serves the HTML file browser
func GitBrowser(c *gin.Context) {
	username := c.Param("username")
	fullPath := strings.TrimPrefix(c.Param("filepath"), "/")
	branch := c.DefaultQuery("branch", "")

	// Username is required
	if username == "" {
		c.HTML(http.StatusBadRequest, "browser.html", BrowserData{
			Error: "A username is required in the URL path: /browse/{username}",
		})
		return
	}

	if !security.IsValidUsernameOrPath(username) {
		c.HTML(http.StatusBadRequest, "browser.html", BrowserData{
			Error: "Usernames must be alphanumeric chars and dashes.",
		})
		return
	}

	// Get the original repository path (same as git_http.go)
	gitProjectRoot := configuration.GetGitProjectRoot()
	repoPath := filepath.Join(gitProjectRoot, "repotemplate")

	// Copy repository to temporary directory if it doesn't exist
	tempRepoPath, _, err := files.CopyRepoToTemp(repoPath, true, username)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "browser.html", BrowserData{
			Error:    "Failed to prepare repository: " + err.Error(),
			Username: username,
		})
		return
	}

	// Split fullPath into repo name and path within the repo
	var repoName, filePath string
	if fullPath != "" {
		parts := strings.SplitN(fullPath, "/", 2)
		repoName = parts[0]
		if len(parts) > 1 {
			filePath = parts[1]
		}
	}

	// If no repo selected, list available repos (subdirectories)
	if repoName == "" {
		entries, err := listRepos(tempRepoPath)
		if err != nil {
			c.HTML(http.StatusInternalServerError, "browser.html", BrowserData{
				Error:    "Failed to list repositories: " + err.Error(),
				Username: username,
			})
			return
		}
		c.HTML(http.StatusOK, "browser.html", BrowserData{
			Username:   username,
			IsRepoList: true,
			Entries:    entries,
		})
		return
	}

	// Resolve the specific repo path
	individualRepoPath := filepath.Join(tempRepoPath, repoName)

	branches, err := listBranches(individualRepoPath, filePath)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "browser.html", BrowserData{
			Error:    "Failed to list branches: " + err.Error(),
			Username: username,
			RepoName: repoName,
		})
		return
	}

	if branch == "" {
		branch = getDefaultBranch(individualRepoPath)
	}

	data := BrowserData{
		Branches:      branches,
		CurrentBranch: branch,
		CurrentPath:   filePath,
		RepoName:      repoName,
		Username:      username,
	}

	// Compute parent path (within the repo)
	if filePath != "" {
		parts := strings.Split(filePath, "/")
		if len(parts) > 1 {
			data.ParentPath = strings.Join(parts[:len(parts)-1], "/")
		}
	}

	// Try to list as a tree first
	entries, err := listTree(individualRepoPath, branch, filePath)
	if err == nil && len(entries) > 0 {
		data.Entries = entries
		c.HTML(http.StatusOK, "browser.html", data)
		return
	}

	// If listing failed, try to show as a file
	content, err := getFileContent(individualRepoPath, branch, filePath)
	if err != nil {
		data.Error = "Failed to load path: " + filePath
		c.HTML(http.StatusNotFound, "browser.html", data)
		return
	}

	data.IsFile = true
	data.FileContent = content
	data.FileName = filepath.Base(filePath)
	c.HTML(http.StatusOK, "browser.html", data)
}

// listRepos lists subdirectories under the given path as available repositories
func listRepos(basePath string) ([]TreeEntry, error) {
	dirEntries, err := os.ReadDir(basePath)
	if err != nil {
		return nil, err
	}
	var entries []TreeEntry
	for _, entry := range dirEntries {
		if entry.IsDir() {
			entries = append(entries, TreeEntry{
				Type: "tree",
				Name: entry.Name(),
			})
		}
	}
	return entries, nil
}
