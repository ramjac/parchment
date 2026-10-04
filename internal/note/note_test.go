package note_test

import (
	"context"
	"errors"
	"testing"

	"example.com/parchment/internal/note"
)

func TestNoteOperationsUndoAndRedo(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service := note.NewService(repository, 20)
	created, err := service.Create(ctx, "First", "draft")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateExpected(ctx, created, "First", "final")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Body != "final" {
		t.Fatalf("body = %q", updated.Body)
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
	if restored.Body != "final" {
		t.Fatalf("undo of deletion restored body = %q, want final", restored.Body)
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
	repository := newMemoryRepository()
	service := note.NewService(repository, 10)
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
	if _, err := service.UpdateExpected(ctx, n, n.Title, n.Body); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, n.ID); err == nil {
		t.Fatal("no-op update unexpectedly replaced creation history")
	}
}

func TestEqualIgnoresTransientTitle(t *testing.T) {
	left := note.Note{}
	right := left
	left.Title = "filename one"
	right.Title = "filename two"

	if !note.Equal(left, right) {
		t.Fatal("transient title differences should not affect note equality")
	}
}

func TestCreateDoesNotRequireTitle(t *testing.T) {
	service := note.NewService(newMemoryRepository(), 1)

	created, err := service.Create(context.Background(), "  ", "body")
	if err != nil {
		t.Fatalf("created note without a title: %v", err)
	}
	if created.Body != "body" {
		t.Fatalf("body = %q, want body", created.Body)
	}
}

type memoryRepository struct {
	notes map[string]note.Note
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{notes: make(map[string]note.Note)}
}

func (*memoryRepository) ArtifactLocation(id string) string {
	return "parchment/artifacts/" + id + "/content.md"
}

func (r *memoryRepository) List(ctx context.Context) ([]note.Note, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	notes := make([]note.Note, 0, len(r.notes))
	for _, item := range r.notes {
		notes = append(notes, cloneNote(item))
	}
	return notes, nil
}

func (r *memoryRepository) Get(ctx context.Context, id string) (note.Note, error) {
	if err := ctx.Err(); err != nil {
		return note.Note{}, err
	}
	item, ok := r.notes[id]
	if !ok {
		return note.Note{}, note.ErrNotFound
	}
	return cloneNote(item), nil
}

func (r *memoryRepository) Save(ctx context.Context, item note.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.notes[item.ID] = cloneNote(item)
	return nil
}

func (r *memoryRepository) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := r.notes[id]; !ok {
		return note.ErrNotFound
	}
	delete(r.notes, id)
	return nil
}

func (r *memoryRepository) Transition(ctx context.Context, id string, expected, target *note.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, exists := r.notes[id]
	if expected == nil {
		if exists {
			return errors.New("note already exists")
		}
	} else if !exists || !note.Equal(current, *expected) {
		return errors.New("stale note")
	}
	if target == nil {
		if !exists {
			return note.ErrNotFound
		}
		delete(r.notes, id)
		return nil
	}
	r.notes[id] = cloneNote(*target)
	return nil
}

func cloneNote(item note.Note) note.Note {
	return item
}
