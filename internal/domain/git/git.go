package git

import (
	"fmt"
	"time"

	"github.com/go-git/go-billy/v6/memfs"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/transport/http"
	"github.com/go-git/go-git/v6/storage/memory"
)

// FileMove represents a single old→new path rename.
type FileMove struct {
	OldPath string
	NewPath string
}

func MoveFileAndPush(repoURL, username, password string, moves []FileMove, commitMsg string) error {
	// Initialize in-memory storage and filesystem
	storer := memory.NewStorage()
	fs := memfs.New()

	// Clone the repository into memory
	repo, err := git.Clone(storer, fs, &git.CloneOptions{
		URL:        repoURL,
		Auth:       &http.BasicAuth{Username: username, Password: password},
		RemoteName: "origin",
	})
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}
	fmt.Println("Repository cloned into memory.")

	// Get the worktree
	worktree, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	// Move each file in the in-memory filesystem and stage the changes
	for _, m := range moves {
		_, err = worktree.Move(m.OldPath, m.NewPath)
		if err != nil {
			return fmt.Errorf("failed to move file %s: %w", m.OldPath, err)
		}
		fmt.Printf("File moved from %s to %s.\n", m.OldPath, m.NewPath)
	}

	// Commit the changes
	commit, err := worktree.Commit(commitMsg, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "MockGit Server",
			Email: "mockgit@example.org",
			When:  time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to commit changes: %w", err)
	}
	fmt.Printf("Changes committed with hash: %s\n", commit.String())

	// Push the changes to the remote repository
	err = repo.Push(&git.PushOptions{
		Auth:       &http.BasicAuth{Username: username, Password: password},
		Progress:   nil,
		RemoteName: "origin",
	})
	if err != nil {
		return fmt.Errorf("failed to push changes: %w", err)
	}
	fmt.Println("Changes pushed successfully to remote origin.")

	return nil
}
