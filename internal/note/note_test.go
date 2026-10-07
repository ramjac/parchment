package note_test

import (
	"context"
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
	created, err := service.Create(ctx, path, "First", "draft")
	if err != nil {
		t.Fatal(err)
	}
	if service.CanUndo() {
		t.Fatal("creating a file was recorded in undo history")
	}
	updated, err := service.Update(ctx, path, "First", "final")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Body != "final" || updated.Path != created.Path {
		t.Fatalf("updated = %+v", updated)
	}
	if err := service.AddTag(ctx, path, "work"); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveTag(ctx, path, " work "); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveTag(ctx, path, "   "); err == nil {
		t.Fatal("empty tag removal succeeded")
	}
	if _, err := service.Rename(ctx, path, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Title != "First" {
		t.Fatalf("undo rename title = %q", restored.Title)
	}
	for range 2 {
		if _, err := service.Undo(ctx); err != nil {
			t.Fatal(err)
		}
	}
	restored, err = service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Tags) != 0 {
		t.Fatalf("undo tag changes left tags: %v", restored.Tags)
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
	if _, err := service.Create(ctx, path, "First", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, path, "Second", "two"); err == nil {
		t.Fatal("create overwrote an existing file")
	}
	n, err := service.Get(ctx, path)
	if err != nil || n.Title != "First" {
		t.Fatalf("existing note = %+v, %v", n, err)
	}
}

func TestNoOpUpdateDoesNotCreateHistory(t *testing.T) {
	ctx := context.Background()
	service, _, path := newService(t, 10)
	n, err := service.Create(ctx, path, "Title", "Body")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, path, n.Title, n.Body); err != nil {
		t.Fatal(err)
	}
	if service.CanUndo() {
		t.Fatal("no-op update was recorded in history")
	}
}

func TestUndoRedoTreatsEmptySlicesAsEquivalentToNil(t *testing.T) {
	ctx := context.Background()
	service, repo, path := newService(t, 10)
	created, err := service.Create(ctx, path, "Title", "before")
	if err != nil {
		t.Fatal(err)
	}
	created.Tags = []string{}
	created.Links = []string{}
	replace(t, repo, created)
	if _, err := service.Update(ctx, path, "Title", "after"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatalf("undo failed after persistence normalized empty slices: %v", err)
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatalf("redo failed after persistence normalized empty slices: %v", err)
	}
}

func TestUndoRefusesToOverwriteExternalEdits(t *testing.T) {
	ctx := context.Background()
	service, repo, path := newService(t, 10)
	if _, err := service.Create(ctx, path, "Title", "before"); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(ctx, path, "Title", "after")
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

func TestHistorySnapshotsDoNotAliasReturnedNoteSlices(t *testing.T) {
	ctx := context.Background()
	service, repo, path := newService(t, 10)
	created, err := service.Create(ctx, path, "Before", "body")
	if err != nil {
		t.Fatal(err)
	}
	created.Tags = []string{"work"}
	created.Links = []string{"related"}
	replace(t, repo, created)

	renamed, err := service.Rename(ctx, path, "After")
	if err != nil {
		t.Fatal(err)
	}
	renamed.Tags[0] = "mutated"
	renamed.Links[0] = "mutated"

	if _, err := service.Undo(ctx); err != nil {
		t.Fatalf("undo failed after mutating returned slices: %v", err)
	}
	restored, err := service.Get(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Title != "Before" || restored.Tags[0] != "work" || restored.Links[0] != "related" {
		t.Fatalf("undo restored mutated history snapshot: %+v", restored)
	}
}
