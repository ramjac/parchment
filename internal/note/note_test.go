package note_test

import (
	"context"
	"testing"

	"example.com/parchment/internal/note"
	"example.com/parchment/internal/workspace"
)

func TestNoteOperationsUndoAndRedo(t *testing.T) {
	ctx := context.Background()
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := note.NewService(ws, 20)
	created, err := service.Create(ctx, "First", "draft")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(ctx, created.ID, "First", "final")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Body != "final" {
		t.Fatalf("body = %q", updated.Body)
	}
	if err := service.AddTag(ctx, created.ID, "work"); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveTag(ctx, created.ID, "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Rename(ctx, created.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, created.ID); err == nil {
		t.Fatal("deleted note still exists")
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Title != "Renamed" {
		t.Fatalf("restored title = %q", restored.Title)
	}
	for range 3 {
		if _, err := service.Undo(ctx); err != nil {
			t.Fatal(err)
		}
	}
	restored, err = service.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Tags) != 0 {
		t.Fatalf("undo tag changes left tags: %v", restored.Tags)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	restored, err = service.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Body != "draft" {
		t.Fatalf("undo body = %q", restored.Body)
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	redone, err := service.Get(ctx, created.ID)
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

func TestNoOpUpdateDoesNotCreateHistory(t *testing.T) {
	ctx := context.Background()
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := note.NewService(ws, 10)
	n, err := service.Create(ctx, "Title", "Body")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, n.ID, n.Title, n.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, n.ID); err == nil {
		t.Fatal("no-op update unexpectedly replaced creation history")
	}
}
