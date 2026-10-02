package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/note"
)

const metadataName = "metadata.json"

var validID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var errNotNote = errors.New("artifact is not a note")

// Workspace is the local filesystem-backed artifact store for one workspace.
type Workspace struct {
	root string
}

// Init creates a workspace and its versioned TOML configuration.
func Init(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(abs, ".parchment", "artifacts"), 0o700); err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	configPath := filepath.Join(abs, "parchment.toml")
	if _, err := os.Stat(configPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check workspace config: %w", err)
	}
	return writeAtomic(configPath, []byte("version = 1\n\n[workspace]\ndiscovery = \"parents\"\n"), 0o600)
}

// Open returns a workspace, creating its private artifact storage directory.
func Open(path string) (*Workspace, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(abs, ".parchment", "artifacts"), 0o700); err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}
	return &Workspace{root: abs}, nil
}

// Root returns the absolute filesystem path to the workspace.
func (w *Workspace) Root() string { return w.root }

// Name returns the final workspace path component.
func (w *Workspace) Name() string { return filepath.Base(w.root) }

// Find searches the current directory and its parents for a workspace.
func Find(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "parchment.toml")); err == nil {
			return current, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(current, ".parchment", "artifacts")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("no workspace found; run `parchment init` or pass --workspace")
		}
		current = parent
	}
}

// List implements the note repository interface.
func (w *Workspace) List(ctx context.Context) ([]note.Note, error) {
	root := filepath.Join(w.root, ".parchment", "artifacts")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	var notes []note.Note
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !validID.MatchString(entry.Name()) {
			continue
		}
		n, err := w.readNote(entry.Name())
		if errors.Is(err, errNotNote) {
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, nil
}

// Get returns the note with the supplied stable ID.
func (w *Workspace) Get(ctx context.Context, id string) (note.Note, error) {
	if err := ctx.Err(); err != nil {
		return note.Note{}, err
	}
	if !validID.MatchString(id) {
		return note.Note{}, note.ErrNotFound
	}
	n, err := w.readNote(id)
	if errors.Is(err, errNotNote) {
		return note.Note{}, note.ErrNotFound
	}
	if errors.Is(err, os.ErrNotExist) {
		return note.Note{}, note.ErrNotFound
	}
	return n, err
}

// Save persists note Markdown and its common artifact metadata.
func (w *Workspace) Save(ctx context.Context, n note.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(n.ID) || n.Kind != artifact.NoteKind {
		return errors.New("invalid note artifact")
	}
	if err := n.Artifact.Validate(); err != nil {
		return err
	}
	dir := filepath.Join(w.root, ".parchment", "artifacts", n.ID)
	_, statErr := os.Stat(dir)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect note storage: %w", statErr)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create note storage: %w", err)
	}
	if n.Location != filepath.ToSlash(filepath.Join(".parchment", "artifacts", n.ID, "content.md")) {
		if created {
			_ = os.RemoveAll(dir)
		}
		return errors.New("invalid note content location")
	}
	if err := writeAtomic(filepath.Join(dir, "content.md"), []byte(n.Body), 0o600); err != nil {
		if created {
			_ = os.RemoveAll(dir)
		}
		return fmt.Errorf("write note content: %w", err)
	}
	data, err := json.MarshalIndent(n.Artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode note metadata: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, metadataName), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write note metadata: %w", err)
	}
	return nil
}

// Delete permanently removes the artifact directory; callers may retain a snapshot for undo.
func (w *Workspace) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(id) {
		return note.ErrNotFound
	}
	dir := filepath.Join(w.root, ".parchment", "artifacts", id)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return note.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("inspect note storage: %w", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete note: %w", err)
	}
	return nil
}

func (w *Workspace) readNote(id string) (note.Note, error) {
	dir := filepath.Join(w.root, ".parchment", "artifacts", id)
	metadata, err := os.Open(filepath.Join(dir, metadataName))
	if err != nil {
		return note.Note{}, err
	}
	defer metadata.Close()
	var a artifact.Artifact
	if err := json.NewDecoder(io.LimitReader(metadata, 1<<20)).Decode(&a); err != nil {
		return note.Note{}, fmt.Errorf("decode artifact metadata %s: %w", id, err)
	}
	if err := a.Validate(); err != nil {
		return note.Note{}, fmt.Errorf("validate artifact metadata %s: %w", id, err)
	}
	if a.ID != id {
		return note.Note{}, fmt.Errorf("invalid note metadata for %s", id)
	}
	if a.Kind != artifact.NoteKind {
		return note.Note{}, errNotNote
	}
	if a.Location != filepath.ToSlash(filepath.Join(".parchment", "artifacts", id, "content.md")) {
		return note.Note{}, fmt.Errorf("invalid note metadata for %s", id)
	}
	content, err := os.ReadFile(filepath.Join(dir, "content.md"))
	if err != nil {
		return note.Note{}, fmt.Errorf("read note content %s: %w", id, err)
	}
	a.CreatedAt = a.CreatedAt.UTC()
	a.ModifiedAt = a.ModifiedAt.UTC()
	return note.Note{Artifact: a, Body: string(content)}, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".parchment-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer func() {
		_ = os.Remove(temp)
	}()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return nil
}
