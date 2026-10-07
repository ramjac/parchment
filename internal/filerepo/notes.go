package filerepo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/note"
)

// Get reads the note stored at path.
func (r *Repository) Get(ctx context.Context, path string) (note.Note, error) {
	if err := ctx.Err(); err != nil {
		return note.Note{}, err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return note.Note{}, err
	}
	content, err := readContent(path, artifact.NoteKind, note.ErrNotFound)
	if err != nil {
		return note.Note{}, err
	}
	return decodeNote(path, content)
}

// Transition writes target to path only if the file still matches expected.
// A nil expected value creates a new file and fails if path exists.
func (r *Repository) Transition(ctx context.Context, path string, expected, target *note.Note) error {
	if target == nil {
		return errors.New("Parchment does not delete artifact files")
	}
	if err := validateTarget(target.Artifact, artifact.NoteKind); err != nil {
		return err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return r.update(ctx, path, expected == nil, note.ErrNotFound, func(content []byte) ([]byte, error) {
		if expected != nil {
			if err := checkKind(path, content, artifact.NoteKind); err != nil {
				return nil, err
			}
			current, err := decodeNote(path, content)
			if err != nil {
				return nil, err
			}
			want := *expected
			want.Path = path
			if !note.Equal(current, want) || target.ID != current.ID {
				return nil, changedError(path)
			}
		}
		data, err := artifactfile.Encode(target.Artifact, target.Body, encodeBlocks(target.Blocks))
		if err != nil {
			return nil, fmt.Errorf("encode note: %w", err)
		}
		return data, nil
	})
}

func decodeNote(path string, content []byte) (note.Note, error) {
	file, err := decodeArtifact(path, content)
	if err != nil {
		return note.Note{}, err
	}
	return note.Note{Artifact: file.Artifact, Body: file.Body, Blocks: copyPayloadBlocks(file.Blocks, "")}, nil
}
