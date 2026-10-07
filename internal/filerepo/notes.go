package filerepo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/note"
)

// Get reads the note stored at path. An ordinary Markdown file without
// Parchment metadata is returned as a note whose body is the whole file.
func (r *Repository) Get(ctx context.Context, path string) (note.Note, error) {
	if err := ctx.Err(); err != nil {
		return note.Note{}, err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return note.Note{}, err
	}
	content, err := readContent(path, artifact.NoteKind, note.ErrNotFound)
	if errors.Is(err, ErrNotArtifact) {
		return readPlainNote(path)
	}
	if err != nil {
		return note.Note{}, err
	}
	return decodeNote(path, content)
}

// Transition writes target to path only if the file still matches expected.
// A nil expected value creates a new file and fails if path exists. An
// ordinary Markdown file stays plain Markdown when saved without structured
// blocks.
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
			if isPlainMarkdown(content) {
				if expected.Body != string(content) || len(expected.Blocks) != 0 {
					return nil, changedError(path)
				}
				if len(target.Blocks) == 0 {
					return []byte(target.Body), nil
				}
			} else {
				if err := checkKind(path, content, artifact.NoteKind); err != nil {
					return nil, err
				}
				current, err := decodeNote(path, content)
				if err != nil {
					return nil, err
				}
				want := *expected
				want.Path = path
				if !note.Equal(current, want) {
					return nil, changedError(path)
				}
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

func isPlainMarkdown(content []byte) bool {
	_, err := artifactfile.ReadMetadata(content)
	return errors.Is(err, artifactfile.ErrMetadataMissing)
}

// readPlainNote opens an ordinary Markdown file. Its timestamps come from the
// filesystem because the file carries no Parchment metadata.
func readPlainNote(path string) (note.Note, error) {
	resolved, err := resolve(path)
	if err != nil {
		return note.Note{}, err
	}
	content, err := readRegularFile(resolved, artifactfile.MaxFileSize)
	if errors.Is(err, fs.ErrNotExist) {
		return note.Note{}, fmt.Errorf("%s: %w", path, note.ErrNotFound)
	}
	if err != nil {
		return note.Note{}, fmt.Errorf("read %s: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return note.Note{}, fmt.Errorf("inspect %s: %w", path, err)
	}
	modified := info.ModTime().UTC()
	item := artifact.Artifact{
		Kind: artifact.NoteKind, CreatedAt: modified, ModifiedAt: modified,
		FormatVersion: artifact.FormatVersion,
	}
	setRuntimeIdentity(&item, path)
	return note.Note{Artifact: item, Body: string(content)}, nil
}
