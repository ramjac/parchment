package filerepo

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewInternal(t *testing.T) {
	t.Run("empty state directory returns error", func(t *testing.T) {
		repo, err := New("")
		if err == nil {
			t.Fatal("expected error for empty stateDir, got nil")
		}
		if repo != nil {
			t.Fatalf("expected nil repository, got %v", repo)
		}
		if !strings.Contains(err.Error(), "state directory is required") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("absolute path preserves stateDir", func(t *testing.T) {
		tempDir := t.TempDir()
		repo, err := New(tempDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo == nil {
			t.Fatal("expected non-nil Repository")
		}
		if repo.stateDir != tempDir {
			t.Fatalf("expected stateDir=%q, got %q", tempDir, repo.stateDir)
		}
	})

	t.Run("relative path resolves to absolute path in stateDir", func(t *testing.T) {
		t.Chdir(t.TempDir())
		relPath := filepath.Join(".", "relative_state_dir")
		absPath, err := filepath.Abs(relPath)
		if err != nil {
			t.Fatal(err)
		}

		repo, err := New(relPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo == nil {
			t.Fatal("expected non-nil Repository")
		}
		if repo.stateDir != absPath {
			t.Fatalf("expected stateDir=%q, got %q", absPath, repo.stateDir)
		}
	})

	t.Run("non-existent directory path resolves without error", func(t *testing.T) {
		tempDir := t.TempDir()
		nonExistentPath := filepath.Join(tempDir, "sub", "dir")
		repo, err := New(nonExistentPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo == nil {
			t.Fatal("expected non-nil Repository")
		}
		expectedAbs, err := filepath.Abs(nonExistentPath)
		if err != nil {
			t.Fatal(err)
		}
		if repo.stateDir != expectedAbs {
			t.Fatalf("expected stateDir=%q, got %q", expectedAbs, repo.stateDir)
		}
	})
}
