package note_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ramjac/parchment/internal/filerepo"
	"github.com/ramjac/parchment/internal/note"
	"github.com/ramjac/parchment/internal/notetext"
)

type transientTextRepository struct {
	note.Repository
	note.TextRepository
}

func TestTextSessionInMemoryHistoryAndFailedSavePreserveRedo(t *testing.T) {
	ctx := context.Background()
	repo, _ := filerepo.New(t.TempDir(), filerepo.Options{})
	service := note.NewService(transientTextRepository{repo, repo}, 10)
	path := filepath.Join(t.TempDir(), "note.txt")
	raw := []byte("caf\xe9\r\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.OpenText(ctx, path, notetext.Windows1252)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.SaveTextExpected(ctx, snapshot, notetext.Split("edited café\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	saved.Raw[0] = 'X'
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveTextExpected(ctx, snapshot, notetext.Split("😀")); err == nil {
		t.Fatal("lossy operation succeeded")
	}
	if !service.CanRedo() {
		t.Fatal("failed save cleared redo")
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, []byte("edited caf\xe9\r\n")) {
		t.Fatalf("redo restored aliased data: %x", got)
	}
}
