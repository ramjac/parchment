package filerepo

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateAtomicNeverReplacesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "racy.md")
	// Simulates another program creating the file after the existence check.
	if err := os.WriteFile(path, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := createAtomic(context.Background(), path, []byte("ours"), 0o600); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("createAtomic over an existing file = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "theirs" {
		t.Fatalf("existing file replaced: %q", data)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}

	fresh := filepath.Join(dir, "fresh.md")
	if err := createAtomic(context.Background(), fresh, []byte("ours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(fresh); string(data) != "ours" {
		t.Fatalf("created file = %q", data)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}
