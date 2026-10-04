package search_test

import (
	"context"
	"testing"
	"time"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/search"
	"example.com/parchment/internal/workspace"
)

func TestNotesOrdersResultsByModificationTime(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(root)
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
	all, err := search.Notes(ctx, ws, " \t ")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != first.ID || all[1].ID != second.ID {
		t.Fatalf("blank-query order = %+v, want most recently modified first", all)
	}
}

func TestDocumentsMatchTitleBodyAndTags(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := workspace.Init(root); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	service := document.NewService(ws, 10)
	d, err := service.Create(ctx, document.Draft{Title: "Budget", Body: "Alpha PLAN"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AddTag(ctx, d.ID, "finance"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"budget", "plan", "FINANCE"} {
		results, err := search.Documents(ctx, ws, query)
		if err != nil || len(results) != 1 {
			t.Fatalf("Documents(%q) = %v, %v", query, results, err)
		}
	}
	if results, _ := search.Documents(ctx, ws, "absent"); len(results) != 0 {
		t.Fatalf("unexpected results %v", results)
	}
}
