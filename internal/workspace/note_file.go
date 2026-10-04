package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/note"
)

// NoteFile stores one existing Parchment note outside a workspace.
type NoteFile struct {
	path string
	mu   sync.Mutex
	item note.Note
	data []byte
}

// OpenNoteFile opens a standalone Parchment note without changing its envelope.
func OpenNoteFile(path string) (*NoteFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve note path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve note file: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect note file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("note file %s is not a regular file", resolved)
	}
	if info.Size() > artifactfile.MaxFileSize {
		return nil, fmt.Errorf("note file is larger than %d MiB", artifactfile.MaxFileSize>>20)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read note file: %w", err)
	}
	file, err := artifactfile.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("open note file: %w", err)
	}
	if file.Artifact.Kind != artifact.NoteKind {
		return nil, errors.New("file is not a Parchment note")
	}
	if len(file.Blocks) == 0 {
		file.Blocks = nil
	}
	a := file.Artifact
	a.ID = standaloneID(artifact.NoteKind, resolved)
	a.Title = standaloneTitle(resolved)
	a.Location = filepath.ToSlash(resolved)
	item := note.Note{Artifact: a, Body: file.Body, Blocks: file.Blocks}
	return &NoteFile{path: resolved, item: item, data: data}, nil
}

func (f *NoteFile) ArtifactLocation(id string) string {
	if id != f.item.ID {
		return ""
	}
	return f.item.Location
}

func (f *NoteFile) List(ctx context.Context) ([]note.Note, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return []note.Note{f.item}, nil
}

func (f *NoteFile) Get(ctx context.Context, id string) (note.Note, error) {
	if err := ctx.Err(); err != nil {
		return note.Note{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return note.Note{}, note.ErrNotFound
	}
	return f.item, nil
}

func (f *NoteFile) Save(ctx context.Context, item note.Note) error {
	return f.Transition(ctx, item.ID, nil, &item)
}

func (f *NoteFile) Delete(context.Context, string) error {
	return errors.New("cannot delete a standalone note")
}

func (f *NoteFile) Transition(ctx context.Context, id string, expected, target *note.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return note.ErrNotFound
	}
	if expected == nil || target == nil {
		return errors.New("cannot create or delete a standalone note")
	}
	if !note.Equal(f.item, *expected) {
		return errors.New("stale note file")
	}
	if target.ID != id || target.Kind != artifact.NoteKind || target.Location != f.item.Location {
		return errors.New("invalid standalone note update")
	}
	info, err := os.Stat(f.path)
	if err != nil {
		return fmt.Errorf("inspect note before save: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("note file is not a regular file")
	}
	current, err := os.ReadFile(f.path)
	if err != nil {
		return fmt.Errorf("read note before save: %w", err)
	}
	if !bytes.Equal(current, f.data) {
		return errors.New("note file changed outside Parchment")
	}
	blocks := make(map[string]any, len(target.Blocks))
	for name, payload := range target.Blocks {
		blocks[name] = payload
	}
	data, err := artifactfile.Encode(target.Artifact, target.Body, blocks)
	if err != nil {
		return err
	}
	if err := writeAtomic(f.path, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("save note file: %w", err)
	}
	f.item = *target
	f.data = data
	return nil
}
