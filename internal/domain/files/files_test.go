package files

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newPackedRepo builds a git repository whose objects have been packed, so that
// there is a real pack index for an AppleDouble sidecar to sit beside.
func newPackedRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	run("init", "--quiet")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("contents"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run("add", "file.txt")
	run("commit", "--quiet", "-m", "initial")
	run("gc", "--prune=now", "--quiet")

	return repo
}

// findMacMetadata reports any macOS metadata under root. The check is written out
// literally rather than calling isMacMetadata, so it does not depend on the code
// under test.
func findMacMetadata(t *testing.T, root string) []string {
	t.Helper()

	var found []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if name := info.Name(); name == ".DS_Store" || strings.HasPrefix(name, "._") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

// TestCopyDirSkipsMacMetadata checks that AppleDouble sidecars in the source are
// not carried into the copy. A ._pack-*.idx sidecar is read by git as a truncated
// pack index, which breaks every fetch from the copied repository.
func TestCopyDirSkipsMacMetadata(t *testing.T) {
	src := newPackedRepo(t)

	packDir := filepath.Join(src, ".git", "objects", "pack")
	indexes, err := filepath.Glob(filepath.Join(packDir, "pack-*.idx"))
	if err != nil {
		t.Fatalf("glob pack indexes: %v", err)
	}
	if len(indexes) == 0 {
		t.Fatalf("no pack index created in %s", packDir)
	}

	// Pollute the source the way macOS does when a file carrying extended
	// attributes is written to a filesystem that cannot store them natively
	sidecar := filepath.Join(packDir, "._"+filepath.Base(indexes[0]))
	if err := os.WriteFile(sidecar, []byte("AppleDouble\x00\x05\x16\x07junk"), 0644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, ".DS_Store"), []byte("junk"), 0644); err != nil {
		t.Fatalf("write .DS_Store: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "copy")
	if err := CopyDir(src, dst); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}

	if found := findMacMetadata(t, dst); len(found) > 0 {
		t.Errorf("copy contains macOS metadata: %v", found)
	}

	// The real pack index must still have been copied
	copied, err := filepath.Glob(filepath.Join(dst, ".git", "objects", "pack", "pack-*.idx"))
	if err != nil {
		t.Fatalf("glob copied indexes: %v", err)
	}
	if len(copied) != len(indexes) {
		t.Errorf("pack indexes copied: want %d, got %d", len(indexes), len(copied))
	}

	// git must be able to use the copy without complaining
	out, err := exec.Command("git", "-C", dst, "fsck", "--no-progress").CombinedOutput()
	if err != nil {
		t.Errorf("fsck on copy failed: %v\n%s", err, out)
	}
	if len(out) > 0 {
		t.Errorf("fsck on copy reported: %s", out)
	}
}

// TestCopyDirPreservesTree checks that a copy is a faithful reproduction of the
// source, including empty directories, nested directories and file modes.
func TestCopyDirPreservesTree(t *testing.T) {
	src := t.TempDir()

	if err := os.MkdirAll(filepath.Join(src, "a/b/c/d/e"), 0755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.Mkdir(filepath.Join(src, "empty"), 0700); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "a/b/c/d/e/deep.txt"), []byte("deep"), 0644); err != nil {
		t.Fatalf("write deep file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "exec.sh"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("write exec file: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "copy")
	if err := CopyDir(src, dst); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}

	cases := []struct {
		relPath string
		mode    os.FileMode
		content string
	}{
		{"empty", os.ModeDir | 0700, ""},
		{"a/b/c/d/e", os.ModeDir | 0755, ""},
		{"a/b/c/d/e/deep.txt", 0644, "deep"},
		{"exec.sh", 0755, "#!/bin/sh\n"},
	}

	for _, tc := range cases {
		path := filepath.Join(dst, tc.relPath)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s: %v", tc.relPath, err)
			continue
		}
		if info.Mode() != tc.mode {
			t.Errorf("%s: mode want %v, got %v", tc.relPath, tc.mode, info.Mode())
		}
		if tc.content != "" {
			got, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("%s: %v", tc.relPath, err)
				continue
			}
			if string(got) != tc.content {
				t.Errorf("%s: content want %q, got %q", tc.relPath, tc.content, got)
			}
		}
	}
}
