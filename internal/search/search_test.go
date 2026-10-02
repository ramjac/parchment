package search_test

import (
	"context"
	"testing"
	"time"

	"example.com/parchment/internal/note"
	"example.com/parchment/internal/search"
	"example.com/parchment/internal/workspace"
)

func TestNotesOrdersResultsByModificationTime(t *testing.T) {
	ctx := context.Background()
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := note.NewService(ws, 10)
	first, err := service.Create(ctx, "First", "shared query")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, "Second", "shared query")
	if err != nil {
		t.Fatal(err)
	}
	first.ModifiedAt = time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := ws.Save(ctx, first); err != nil {
		t.Fatal(err)
	}

	results, err := search.Notes(ctx, ws, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].ID != first.ID || results[1].ID != second.ID {
		t.Fatalf("search result order = %+v, want most recently modified first", results)
	}
}
