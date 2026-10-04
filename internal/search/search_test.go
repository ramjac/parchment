package search_test

import (
	"context"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/search"
)

type noteLister struct {
	note.Repository
	notes []note.Note
}

func (l noteLister) List(context.Context) ([]note.Note, error) { return l.notes, nil }

type documentLister struct {
	document.Repository
	documents []document.Document
}

func (l documentLister) ListDocuments(context.Context) ([]document.Document, error) {
	return l.documents, nil
}

func newNote(id, title, body string, modified time.Time) note.Note {
	n := note.Note{Body: body}
	n.ID, n.Kind, n.Title, n.ModifiedAt = id, artifact.NoteKind, title, modified
	return n
}

func TestNotesOrdersResultsByModificationTime(t *testing.T) {
	ctx := context.Background()
	older := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	repository := noteLister{notes: []note.Note{
		newNote("n2", "Second", "shared query", older),
		newNote("n1", "First", "shared query", newer),
	}}

	results, err := search.Notes(ctx, repository, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].ID != "n1" || results[1].ID != "n2" {
		t.Fatalf("search result order = %+v, want most recently modified first", results)
	}
	all, err := search.Notes(ctx, repository, " \t ")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != "n1" || all[1].ID != "n2" {
		t.Fatalf("blank-query order = %+v, want most recently modified first", all)
	}
}

func TestDocumentsMatchTitleAndBody(t *testing.T) {
	ctx := context.Background()
	d := document.Document{Body: "Alpha PLAN"}
	d.ID, d.Kind, d.Title = "d1", artifact.DocumentKind, "Budget"
	repository := documentLister{documents: []document.Document{d}}
	for _, query := range []string{"plan", "budget"} {
		results, err := search.Documents(ctx, repository, query)
		if err != nil || len(results) != 1 {
			t.Fatalf("Documents(%q) = %v, %v", query, results, err)
		}
	}
	if results, _ := search.Documents(ctx, repository, "absent"); len(results) != 0 {
		t.Fatalf("unexpected results %v", results)
	}
}
