package search

import (
	"context"
	"sort"
	"strings"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
)

// Notes scans workspace notes directly without maintaining a background index.
func Notes(ctx context.Context, repository note.Repository, query string) ([]note.Note, error) {
	notes, err := repository.List(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(notes, func(i, j int) bool {
		return notes[i].ModifiedAt.After(notes[j].ModifiedAt)
	})
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return notes, nil
	}
	results := make([]note.Note, 0, len(notes))
	for _, n := range notes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matches := strings.Contains(strings.ToLower(n.Title), query) ||
			strings.Contains(strings.ToLower(n.Body), query)
		if matches {
			results = append(results, n)
		}
	}
	return results, nil
}

// Documents scans workspace documents directly, matching titles, Markdown
// bodies, and tags case-insensitively, newest-modified first.
func Documents(ctx context.Context, repository document.Repository, query string) ([]document.Document, error) {
	documents, err := repository.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(documents, func(i, j int) bool {
		return documents[i].ModifiedAt.After(documents[j].ModifiedAt)
	})
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return documents, nil
	}
	results := make([]document.Document, 0, len(documents))
	for _, d := range documents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matches := strings.Contains(strings.ToLower(d.Title), query) ||
			strings.Contains(strings.ToLower(d.Body), query)
		if matches {
			results = append(results, d)
		}
	}
	return results, nil
}
