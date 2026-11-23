package filerepo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/parchment/internal/artifactfile"
)

func TestPlainNoteSaveRejectsOversizedFile(t *testing.T) {
	repo, err := New(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := repo.Get(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	edited := original
	edited.Body = strings.Repeat("x", artifactfile.MaxFileSize+1)
	err = repo.Transition(context.Background(), path, &original, &edited)
	if err == nil || !strings.Contains(err.Error(), "larger than 16 MiB") {
		t.Fatalf("oversized save error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("source after rejected save = %q, error %v", data, err)
	}
}

func TestNewInternal_EmptyStateDir(t *testing.T) {
	repo, err := New("", Options{})
	if err == nil {
		t.Fatal("expected error for empty stateDir, got nil")
	}
	if repo != nil {
		t.Fatalf("expected nil repository, got %v", repo)
	}
	if !strings.Contains(err.Error(), "state directory is required") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestNewInternal_AbsolutePath(t *testing.T) {
	tempDir := t.TempDir()
	repo, err := New(tempDir, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo == nil {
		t.Fatal("expected non-nil Repository")
	}
	if repo.stateDir != tempDir {
		t.Fatalf("expected stateDir=%q, got %q", tempDir, repo.stateDir)
	}
}

func TestNewInternal_RelativePath(t *testing.T) {
	t.Chdir(t.TempDir())
	relPath := filepath.Join(".", "relative_state_dir")
	absPath, err := filepath.Abs(relPath)
	if err != nil {
		t.Fatal(err)
	}

	repo, err := New(relPath, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo == nil {
		t.Fatal("expected non-nil Repository")
	}
	if repo.stateDir != absPath {
		t.Fatalf("expected stateDir=%q, got %q", absPath, repo.stateDir)
	}
}

func TestNewInternal_NonExistentDir(t *testing.T) {
	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "sub", "dir")
	repo, err := New(nonExistentPath, Options{})
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
}

func TestNewInternal_FilepathAbsError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	_ = os.RemoveAll(dir)

	repo, err := New("relative_path", Options{})
	if err == nil {
		t.Fatal("expected error when working directory is deleted, got nil")
	}
	if repo != nil {
		t.Fatalf("expected nil repo, got %v", repo)
	}
	if !strings.Contains(err.Error(), "resolve state directory:") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
