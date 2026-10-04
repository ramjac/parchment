package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/note"
)

// standaloneID is stable for a resolved file path and is never persisted.
func standaloneID(kind artifact.Kind, path string) string {
	prefix := map[artifact.Kind]byte{
		artifact.NoteKind: 'n', artifact.DocumentKind: 'd', artifact.SpreadsheetKind: 's',
		artifact.PresentationKind: 'p', artifact.ImageKind: 'i',
	}[kind]
	digest := sha256.Sum256([]byte(path))
	return string(prefix) + new(big.Int).SetBytes(digest[:16]).Text(36)
}

func standaloneTitle(path string) string {
	title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if title == "" {
		return filepath.Base(path)
	}
	return title
}

func markdownTitle(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
		return line
	}
	return "Untitled"
}

// MarkdownFile is a repository for one ordinary Markdown file opened directly
// without adding Parchment metadata.
type MarkdownFile struct {
	path string
	mu   sync.Mutex
	item note.Note
}

// OpenMarkdownFile opens an existing regular Markdown file as one editable note.
func OpenMarkdownFile(path string) (*MarkdownFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve Markdown file path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve Markdown file: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect Markdown file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Markdown file %s is not a regular file", resolved)
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read Markdown file: %w", err)
	}
	id := standaloneID(artifact.NoteKind, resolved)
	title := standaloneTitle(resolved)
	now := time.Now().UTC()
	return &MarkdownFile{
		path: resolved,
		item: note.Note{Artifact: artifact.Artifact{
			ID: id, Kind: artifact.NoteKind, Title: title,
			CreatedAt: now, ModifiedAt: now, FormatVersion: artifact.FormatVersion,
			Location: filepath.ToSlash(resolved),
		}, Body: string(content)},
	}, nil
}

func (f *MarkdownFile) ArtifactLocation(id string) string {
	if id != f.item.ID {
		return ""
	}
	return filepath.ToSlash(f.path)
}

func (f *MarkdownFile) List(ctx context.Context) ([]note.Note, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return []note.Note{f.item}, nil
}

func (f *MarkdownFile) Get(ctx context.Context, id string) (note.Note, error) {
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

func (f *MarkdownFile) Save(ctx context.Context, item note.Note) error {
	return f.Transition(ctx, item.ID, nil, &item)
}

func (f *MarkdownFile) Delete(context.Context, string) error {
	return errors.New("cannot delete a Markdown file from the notes screen")
}

func (f *MarkdownFile) Transition(ctx context.Context, id string, expected, target *note.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return note.ErrNotFound
	}
	if expected != nil && !note.Equal(f.item, *expected) {
		return errors.New("stale Markdown file")
	}
	if target == nil {
		return errors.New("cannot delete a Markdown file from the notes screen")
	}
	if target.ID != f.item.ID || target.Kind != artifact.NoteKind || target.Location != f.item.Location {
		return errors.New("invalid Markdown file update")
	}
	current, err := os.ReadFile(f.path)
	if err != nil {
		return fmt.Errorf("read Markdown file before save: %w", err)
	}
	if string(current) != f.item.Body {
		return errors.New("Markdown file changed outside Parchment")
	}
	info, err := os.Stat(f.path)
	if err != nil {
		return fmt.Errorf("inspect Markdown file before save: %w", err)
	}
	if err := writeAtomic(f.path, []byte(target.Body), info.Mode().Perm()); err != nil {
		return fmt.Errorf("save Markdown file: %w", err)
	}
	f.item = *target
	return nil
}
