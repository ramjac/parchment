package note_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"example.com/parchment/internal/filerepo"
	"example.com/parchment/internal/note"
)

func newService(t *testing.T, undoLimit int) (*note.Service, *filerepo.Repository, string) {
	t.Helper()
	repo, err := filerepo.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return note.NewService(repo, undoLimit), repo, filepath.Join(t.TempDir(), "note.md")
}

// replace writes n over the stored note without recording history.
func replace(t *testing.T, repo *filerepo.Repository, n note.Note) {
	t.Helper()
	current, err := repo.Get(context.Background(), n.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Transition(context.Background(), n.Path, &current, &n); err != nil {
		t.Fatal(err)
	}
}

func TestNoteOperationsUndoAndRedo(t *testing.T) {
	ctx := context.Background()
	service, _, path := newService(t, 20)
	created, err := service.Create(ctx, path, "draft")
	if err != nil {
		t.Fatal(err)
	}
	if service.CanUndo() {
		t.Fatal("creating a file was recorded in undo history")
	}
	updated, err := service.Update(ctx, path, "final")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Body != "final" || updated.Path != created.Path {
		t.Fatalf("updated = %+v", updated)
	}
	if _, err := service.Update(ctx, path, "revised"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := service.Get(ctx, path)
	if err != nil || restored.Body != "final" {
		t.Fatalf("undo revised = %+v, %v", restored, err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err = service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Body != "draft" {
		t.Fatalf("undo body = %q", restored.Body)
	}
	if service.CanUndo() {
		t.Fatal("undo history extends past the file's creation")
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	redone, err := service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if redone.Body != "final" {
		t.Fatalf("redo body = %q", redone.Body)
	}
	if !service.CanUndo() || !service.CanRedo() {
		t.Fatalf("history availability undo=%t redo=%t", service.CanUndo(), service.CanRedo())
	}
}

func TestCreateRefusesExistingFile(t *testing.T) {
	ctx := context.Background()
	service, _, path := newService(t, 10)
	if _, err := service.Create(ctx, path, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, path, "two"); err == nil {
		t.Fatal("create overwrote an existing file")
	}
	n, err := service.Get(ctx, path)
	if err != nil || n.Body != "one" || n.Title != "note" {
		t.Fatalf("existing note = %+v, %v", n, err)
	}
}

func TestNoOpUpdateDoesNotCreateHistory(t *testing.T) {
	ctx := context.Background()
	service, _, path := newService(t, 10)
	n, err := service.Create(ctx, path, "Body")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, path, n.Body); err != nil {
		t.Fatal(err)
	}
	if service.CanUndo() {
		t.Fatal("no-op update was recorded in history")
	}
}

func TestUndoRefusesToOverwriteExternalEdits(t *testing.T) {
	ctx := context.Background()
	service, repo, path := newService(t, 10)
	if _, err := service.Create(ctx, path, "before"); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(ctx, path, "after")
	if err != nil {
		t.Fatal(err)
	}
	updated.Body = "edited in a text editor"
	replace(t, repo, updated)
	if _, err := service.Undo(ctx); err == nil {
		t.Fatal("undo overwrote an external edit")
	}
	current, err := service.Get(ctx, path)
	if err != nil || current.Body != "edited in a text editor" {
		t.Fatalf("current = %+v, %v", current, err)
	}
}

func TestHistorySnapshotsDoNotAliasReturnedNoteBlocks(t *testing.T) {
	ctx := context.Background()
	service, repo, path := newService(t, 10)
	created, err := service.Create(ctx, path, "before")
	if err != nil {
		t.Fatal(err)
	}
	created.Blocks = map[string]json.RawMessage{"parchment-extra": json.RawMessage(`{"a":1}`)}
	replace(t, repo, created)
	current, err := service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateExpected(ctx, current, "after")
	if err != nil {
		t.Fatal(err)
	}
	updated.Blocks["parchment-extra"][2] = 'z'
	if _, err := service.Undo(ctx); err != nil {
		t.Fatalf("undo failed after mutating returned blocks: %v", err)
	}
	restored, err := service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Body != "before" || string(restored.Blocks["parchment-extra"]) != `{"a":1}` {
		t.Fatalf("undo restored mutated history snapshot: %+v", restored)
	}
}
