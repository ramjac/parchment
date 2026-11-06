package filerepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/document"
)

// Convert converts a plain Markdown file into a typed Parchment artifact in place.
func (r *Repository) Convert(ctx context.Context, path string, kind artifact.Kind) error {
	if kind != artifact.DocumentKind && kind != artifact.NoteKind && kind != artifact.PresentationKind {
		return fmt.Errorf("unsupported conversion kind %q", kind)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return r.update(ctx, path, false, os.ErrNotExist, func(content []byte) ([]byte, error) {
		if !isPlainMarkdown(content) {
			return nil, errors.New("file already has a parchment-meta block")
		}

		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat: %w", err)
		}
		modTime := info.ModTime().UTC()
		now := time.Now().UTC()

		id, err := artifact.NewID(kind)
		if err != nil {
			return nil, err
		}

		item := artifact.Artifact{
			ID:            id,
			Kind:          kind,
			CreatedAt:     modTime,
			ModifiedAt:    now,
			FormatVersion: artifact.FormatVersion,
			Path:          path,
		}

		body := string(content)
		var blocks map[string]any

		switch kind {
		case artifact.DocumentKind:
			layout := document.DefaultLayout()
			if err := layout.Validate(); err != nil {
				return nil, err
			}
			blocks = map[string]any{
				"parchment-document": map[string]any{
					"layout": layout,
				},
			}
		case artifact.PresentationKind:
			blocks = map[string]any{
				"parchment-presentation": map[string]any{
					"version": 1,
				},
			}
		}

		data, err := artifactfile.Encode(item, body, blocks)
		if err != nil {
			return nil, fmt.Errorf("encode: %w", err)
		}

		if _, err := artifactfile.Decode(data); err != nil {
			return nil, fmt.Errorf("failed round-trip decoding: %w", err)
		}

		return data, nil
	})
}
