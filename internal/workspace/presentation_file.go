package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/presentation"
)

// PresentationFile stores one existing presentation directly, without a
// workspace. It implements presentation.Repository for that single artifact.
type PresentationFile struct {
	path string
	mu   sync.Mutex
	item presentation.Presentation
	data []byte
}

// OpenPresentationFile opens an existing standalone presentation artifact.
func OpenPresentationFile(path string) (*PresentationFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve presentation path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve presentation file: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect presentation file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("presentation file %s is not a regular file", resolved)
	}
	if info.Size() > artifactfile.MaxFileSize {
		return nil, fmt.Errorf("presentation file is larger than %d MiB", artifactfile.MaxFileSize>>20)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read presentation file: %w", err)
	}
	item, err := presentation.Decode(data)
	if errors.Is(err, presentation.ErrNotFound) {
		return nil, fmt.Errorf("open presentation file: %s is not a presentation artifact", resolved)
	}
	if err != nil {
		return nil, fmt.Errorf("open presentation file: %w", err)
	}
	item.ID = standaloneID(item.Kind, resolved)
	item.Title = standaloneTitle(resolved)
	item.Location = filepath.ToSlash(resolved)
	return &PresentationFile{path: resolved, item: item, data: data}, nil
}

// Path returns the resolved filesystem path of the presentation file.
func (f *PresentationFile) Path() string { return f.path }

func (f *PresentationFile) ArtifactLocation(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return ""
	}
	return f.item.Location
}

func (f *PresentationFile) ListPresentations(ctx context.Context) ([]presentation.Presentation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return []presentation.Presentation{clonePresentationFileItem(f.item)}, nil
}

func (f *PresentationFile) GetPresentation(ctx context.Context, id string) (presentation.Presentation, error) {
	if err := ctx.Err(); err != nil {
		return presentation.Presentation{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return presentation.Presentation{}, presentation.ErrNotFound
	}
	return clonePresentationFileItem(f.item), nil
}

// TransitionPresentation saves target over the file if both the in-memory
// expected state and the on-disk bytes are unchanged since the last load/save.
func (f *PresentationFile) TransitionPresentation(
	ctx context.Context, id string, expected, target *presentation.Presentation,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return presentation.ErrNotFound
	}
	if expected == nil || target == nil {
		return errors.New("cannot create or delete a standalone presentation")
	}
	if !presentation.Equal(f.item, *expected) {
		return errors.New("stale presentation file")
	}
	if target.ID != id || target.Kind != f.item.Kind || target.Location != f.item.Location {
		return errors.New("invalid standalone presentation update")
	}
	info, err := os.Stat(f.path)
	if err != nil {
		return fmt.Errorf("inspect presentation before save: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("presentation file is not a regular file")
	}
	current, err := os.ReadFile(f.path)
	if err != nil {
		return fmt.Errorf("read presentation before save: %w", err)
	}
	if !bytes.Equal(current, f.data) {
		return errors.New("presentation file changed outside Parchment")
	}
	data, err := presentation.Encode(*target)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeAtomic(ctx, f.path, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("save presentation file: %w", err)
	}
	f.item = clonePresentationFileItem(*target)
	f.item.Title = standaloneTitle(f.path)
	f.data = data
	return nil
}

func clonePresentationFileItem(item presentation.Presentation) presentation.Presentation {
	if item.Blocks != nil {
		clone := make(map[string]json.RawMessage, len(item.Blocks))
		for name, payload := range item.Blocks {
			clone[name] = append(json.RawMessage(nil), payload...)
		}
		item.Blocks = clone
	}
	return item
}
