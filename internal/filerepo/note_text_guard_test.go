package filerepo

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"example.com/parchment/internal/note"
	"example.com/parchment/internal/notetext"
)

func TestTextSessionConflictsIncludeFormatOnlyChangesAndNoOpSaves(t *testing.T) {
	for _, external := range [][]byte{
		[]byte("one\n"),
		[]byte("\xef\xbb\xbfone\r\n"),
		{0xff, 0xfe, 'o', 0, 'n', 0, 'e', 0, '\r', 0, '\n', 0},
	} {
		t.Run(string(external), func(t *testing.T) {
			repo, _ := New(t.TempDir(), Options{})
			service := note.NewService(repo, 10)
			path := filepath.Join(t.TempDir(), "note.txt")
			if err := os.WriteFile(path, []byte("one\r\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			snapshot, err := service.OpenText(context.Background(), path, notetext.Auto)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, external, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := service.SaveTextExpected(context.Background(), snapshot, snapshot.Document); err == nil {
				t.Fatal("no-op save failed to detect a format-only external change")
			}
			if _, err := service.SaveTextExpected(context.Background(), snapshot, notetext.Split("changed\r\n")); err == nil {
				t.Fatal("edited save overwrote a format-only external change")
			}
			got, _ := os.ReadFile(path)
			if !bytes.Equal(got, external) || service.CanUndo() {
				t.Fatal("conflict changed file or history")
			}
		})
	}
}

func TestTextSessionPreservesSymlinkPermissionsAndFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	repo, _ := New(t.TempDir(), Options{})
	service := note.NewService(repo, 10)
	folder := t.TempDir()
	path, link := filepath.Join(folder, "note.txt"), filepath.Join(folder, "link.txt")
	if err := os.WriteFile(path, []byte("one\r"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.OpenText(context.Background(), link, notetext.Auto)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.SaveTextExpected(ctx, snapshot, notetext.Split("cancelled\r")); err == nil {
		t.Fatal("cancelled save succeeded")
	}
	if _, err := service.SaveTextExpected(context.Background(), snapshot, notetext.Split("changed\r")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("permissions changed: %v, %v", info, err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced")
	}
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) != 2 {
		t.Fatalf("unexpected files beside note: %v, %v", entries, err)
	}
}
