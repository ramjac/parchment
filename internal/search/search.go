package search

import (
	"context"
	"strings"

	"example.com/parchment/internal/note"
)

// Notes scans workspace notes directly without maintaining a background index.
func Notes(ctx context.Context, repository note.Repository, query string) ([]note.Note, error) {
	notes, err := repository.List(ctx)
	if err != nil {
		return nil, err
	}
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
		if !matches {
			for _, tag := range n.Tags {
				if strings.Contains(strings.ToLower(tag), query) {
					matches = true
					break
				}
			}
		}
		if matches {
			results = append(results, n)
		}
	}
	return results, nil
}
