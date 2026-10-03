package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/note"
)

const (
	metadataName          = "metadata.json"
	transactionName       = ".parchment-transaction.json"
	deletedArtifactPrefix = ".parchment-deleted-"
)

var validID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var errNotNote = errors.New("artifact is not a note")
var errNoMetadata = errors.New("artifact metadata not found")

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
	configPath := filepath.Join(abs, "parchment.toml")
	if info, err := os.Lstat(configPath); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("workspace config path %s is not a regular file", configPath)
		}
		if err := ensureWorkspaceDirectories(abs); err != nil {
			return fmt.Errorf("create workspace: %w", err)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check workspace config: %w", err)
	}
	if err := ensureWorkspaceDirectories(abs); err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}
	return writeAtomic(configPath, []byte("version = 1\n\n[workspace]\ndiscovery = \"parents\"\n"), 0o600)
}

// Open returns an initialized workspace without creating workspace directories.
func Open(path string) (*Workspace, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	configPath := filepath.Join(abs, "parchment.toml")
	configInfo, err := os.Lstat(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("open workspace: %s is not initialized (run `parchment init`)", abs)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect workspace config: %w", err)
	}
	if !configInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("open workspace: %s is not a regular file", configPath)
	}
	private := filepath.Join(abs, ".parchment")
	if err := ensureExistingDirectory(private); err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}
	artifacts := filepath.Join(private, "artifacts")
	if err := ensureExistingDirectory(artifacts); err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}
	entries, err := os.ReadDir(artifacts)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	for _, entry := range entries {
		if id := strings.TrimPrefix(entry.Name(), deletedArtifactPrefix); id != entry.Name() && validID.MatchString(id) {
			if err := withArtifactLock(context.Background(), artifacts, id, func() error {
				return cleanupDeletedArtifact(artifacts, id)
			}); err != nil {
				return nil, fmt.Errorf("finish deletion of artifact %s: %w", id, err)
			}
			continue
		}
		if !entry.IsDir() || !validID.MatchString(entry.Name()) {
			continue
		}
		if err := withArtifactLock(context.Background(), artifacts, entry.Name(), func() error {
			return recoverArtifactFiles(filepath.Join(artifacts, entry.Name()))
		}); err != nil {
			return nil, fmt.Errorf("recover artifact %s: %w", entry.Name(), err)
		}
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
		n, err := w.readNote(ctx, entry.Name())
		if errors.Is(err, errNotNote) {
			continue
		}
		if errors.Is(err, errNoMetadata) {
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
	n, err := w.readNote(ctx, id)
	if errors.Is(err, errNotNote) {
		return note.Note{}, note.ErrNotFound
	}
	if errors.Is(err, errNoMetadata) {
		return note.Note{}, note.ErrNotFound
	}
	return n, err
}

// Transition applies a note update only if its current value matches expected.
// A nil expected value means the note must not exist; a nil target deletes it.
func (w *Workspace) Transition(ctx context.Context, id string, expected, target *note.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(id) {
		return note.ErrNotFound
	}
	if expected != nil && expected.ID != id {
		return errors.New("expected note ID does not match transition ID")
	}
	if target != nil && target.ID != id {
		return errors.New("target note ID does not match transition ID")
	}
	artifactsDir := filepath.Join(w.root, ".parchment", "artifacts")
	return withArtifactLock(ctx, artifactsDir, id, func() error {
		current, err := w.readNoteUnlocked(id)
		if errors.Is(err, errNotNote) || errors.Is(err, errNoMetadata) {
			err = note.ErrNotFound
		}
		if expected == nil {
			if err == nil {
				return fmt.Errorf("note %s already exists", id)
			}
			if !errors.Is(err, note.ErrNotFound) {
				return err
			}
		} else {
			if err != nil {
				return err
			}
			if !note.Equal(current, *expected) {
				return fmt.Errorf("note %s changed since this operation was recorded", id)
			}
		}
		if target == nil {
			return w.deleteLocked(artifactsDir, id)
		}
		return w.saveLocked(ctx, *target)
	})
}

// Save persists note Markdown and its common artifact metadata.
func (w *Workspace) Save(ctx context.Context, n note.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(n.ID) {
		return errors.New("invalid note artifact")
	}
	return withArtifactLock(ctx, filepath.Join(w.root, ".parchment", "artifacts"), n.ID, func() error {
		return w.saveLocked(ctx, n)
	})
}

func (w *Workspace) saveLocked(ctx context.Context, n note.Note) error {
	if !validID.MatchString(n.ID) || n.Kind != artifact.NoteKind {
		return errors.New("invalid note artifact")
	}
	if err := n.Artifact.Validate(); err != nil {
		return err
	}
	if n.Location != filepath.ToSlash(filepath.Join(".parchment", "artifacts", n.ID, "content.md")) {
		return errors.New("invalid note content location")
	}
	data, err := json.MarshalIndent(n.Artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode note metadata: %w", err)
	}
	metadata := append(data, '\n')

	dir := filepath.Join(w.root, ".parchment", "artifacts", n.ID)
	created := false
	if err := os.Mkdir(dir, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create note storage: %w", err)
		}
		info, statErr := os.Lstat(dir)
		if statErr != nil {
			return fmt.Errorf("inspect note storage: %w", statErr)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("note storage path is not a directory")
		}
	} else {
		created = true
		if err := syncDirectory(filepath.Dir(dir)); err != nil {
			_ = os.Remove(dir)
			return fmt.Errorf("sync note storage parent: %w", err)
		}
	}
	if err := recoverArtifactFiles(dir); err != nil {
		if created {
			_ = os.Remove(dir)
		}
		return fmt.Errorf("recover note files: %w", err)
	}
	if err := replaceArtifactFiles(ctx, dir, []stagedArtifactFile{
		{name: "content.md", data: []byte(n.Body)},
		{name: metadataName, data: metadata},
	}); err != nil {
		if created {
			if cleanupErr := os.Remove(dir); cleanupErr != nil {
				return fmt.Errorf("save note files: %w (also failed to remove new artifact directory: %v)", err, cleanupErr)
			}
		}
		return fmt.Errorf("save note files: %w", err)
	}
	return nil
}

type stagedArtifactFile struct {
	name string
	data []byte
	temp string
}

type transactionFile struct {
	Name   string `json:"name"`
	Backup string `json:"backup,omitempty"`
	HadOld bool   `json:"had_old"`
}

func replaceArtifactFiles(ctx context.Context, dir string, files []stagedArtifactFile) error {
	transaction := make([]transactionFile, len(files))
	journalCreated := false
	defer func() {
		if !journalCreated {
			cleanupTransactionBackups(dir, transaction)
		}
	}()
	for i := range files {
		if err := ctx.Err(); err != nil {
			cleanupStagedFiles(files)
			return err
		}
		temp, err := stageFile(dir, files[i].data)
		if err != nil {
			cleanupStagedFiles(files)
			return err
		}
		files[i].temp = temp
		target := filepath.Join(dir, files[i].name)
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			transaction[i] = transactionFile{Name: files[i].name}
			continue
		}
		if err != nil {
			cleanupStagedFiles(files)
			return err
		}
		if !info.Mode().IsRegular() {
			cleanupStagedFiles(files)
			return fmt.Errorf("%s is not a regular file", files[i].name)
		}
		old, err := os.ReadFile(target)
		if err != nil {
			cleanupStagedFiles(files)
			return err
		}
		backup, err := stageFile(dir, old)
		if err != nil {
			cleanupStagedFiles(files)
			return err
		}
		transaction[i] = transactionFile{Name: files[i].name, Backup: filepath.Base(backup), HadOld: true}
	}

	journal, err := json.Marshal(transaction)
	if err != nil {
		cleanupStagedFiles(files)
		return fmt.Errorf("encode note transaction: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, transactionName), append(journal, '\n'), 0o600); err != nil {
		cleanupStagedFiles(files)
		if _, statErr := os.Lstat(filepath.Join(dir, transactionName)); statErr == nil {
			journalCreated = true
			return errors.Join(fmt.Errorf("write note transaction: %w", err), recoverArtifactFiles(dir))
		}
		return fmt.Errorf("write note transaction: %w", err)
	}
	journalCreated = true
	rollback := func(cause error) error {
		recoveryErr := recoverArtifactFiles(dir)
		cleanupStagedFiles(files)
		return errors.Join(cause, recoveryErr)
	}
	for i := range files {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if err := os.Rename(files[i].temp, filepath.Join(dir, files[i].name)); err != nil {
			return rollback(err)
		}
		files[i].temp = ""
	}
	if err := syncDirectory(dir); err != nil {
		return rollback(err)
	}
	if err := os.Remove(filepath.Join(dir, transactionName)); err != nil {
		return rollback(err)
	}
	if err := syncDirectory(dir); err != nil {
		if journalErr := writeAtomic(filepath.Join(dir, transactionName), append(journal, '\n'), 0o600); journalErr != nil {
			return fmt.Errorf("sync note transaction removal: %w (also failed to restore transaction journal: %v)", err, journalErr)
		}
		return rollback(err)
	}
	cleanupTransactionBackups(dir, transaction)
	cleanupStagedFiles(files)
	return nil
}

func recoverArtifactFiles(dir string) error {
	path := filepath.Join(dir, transactionName)
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("note transaction journal is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var transaction []transactionFile
	if err := json.Unmarshal(data, &transaction); err != nil {
		return fmt.Errorf("decode note transaction: %w", err)
	}
	for _, file := range transaction {
		if file.Name != "content.md" && file.Name != metadataName {
			return fmt.Errorf("invalid note transaction target %q", file.Name)
		}
		target := filepath.Join(dir, file.Name)
		if file.HadOld {
			if filepath.Base(file.Backup) != file.Backup || !strings.HasPrefix(file.Backup, ".parchment-stage-") {
				return fmt.Errorf("invalid note transaction backup %q", file.Backup)
			}
			backupPath := filepath.Join(dir, file.Backup)
			backupInfo, err := os.Lstat(backupPath)
			if err != nil {
				return fmt.Errorf("inspect backup for %s: %w", file.Name, err)
			}
			if !backupInfo.Mode().IsRegular() {
				return fmt.Errorf("backup for %s is not a regular file", file.Name)
			}
			old, err := os.ReadFile(backupPath)
			if err != nil {
				return fmt.Errorf("read backup for %s: %w", file.Name, err)
			}
			restore, err := stageFile(dir, old)
			if err != nil {
				return fmt.Errorf("stage restore for %s: %w", file.Name, err)
			}
			if err := os.Rename(restore, target); err != nil {
				_ = os.Remove(restore)
				return fmt.Errorf("restore %s: %w", file.Name, err)
			}
		} else if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove uncommitted %s: %w", file.Name, err)
		}
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	cleanupTransactionBackups(dir, transaction)
	return nil
}

func stageFile(dir string, data []byte) (string, error) {
	file, err := os.CreateTemp(dir, ".parchment-stage-*")
	if err != nil {
		return "", err
	}
	temp := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return "", err
	}
	return temp, nil
}

func cleanupStagedFiles(files []stagedArtifactFile) {
	for _, file := range files {
		if file.temp != "" {
			_ = os.Remove(file.temp)
		}
	}
}

func cleanupTransactionBackups(dir string, transaction []transactionFile) {
	for _, file := range transaction {
		if file.Backup != "" {
			_ = os.Remove(filepath.Join(dir, file.Backup))
		}
	}
}

// Delete permanently removes the artifact directory; callers may retain a snapshot for undo.
func (w *Workspace) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(id) {
		return note.ErrNotFound
	}
	artifactsDir := filepath.Join(w.root, ".parchment", "artifacts")
	return withArtifactLock(ctx, artifactsDir, id, func() error {
		return w.deleteLocked(artifactsDir, id)
	})
}

func (w *Workspace) deleteLocked(artifactsDir, id string) error {
	dir := filepath.Join(artifactsDir, id)
	if err := cleanupDeletedArtifact(artifactsDir, id); err != nil {
		return fmt.Errorf("clean up prior deletion: %w", err)
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return note.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("inspect note storage: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("note storage path is not a directory")
	}
	tombstone := filepath.Join(artifactsDir, deletedArtifactPrefix+id)
	if err := os.Rename(dir, tombstone); err != nil {
		return fmt.Errorf("stage note deletion: %w", err)
	}
	if err := syncDirectory(artifactsDir); err != nil {
		restoreErr := os.Rename(tombstone, dir)
		if restoreErr == nil {
			restoreErr = syncDirectory(artifactsDir)
		}
		return errors.Join(fmt.Errorf("sync deleted note directory: %w", err), restoreErr)
	}
	if err := cleanupDeletedArtifact(artifactsDir, id); err != nil {
		slog.Warn("note deletion committed but tombstone cleanup failed", "artifact_id", id, "error", err)
	}
	return nil
}

func cleanupDeletedArtifact(artifactsDir, id string) error {
	tombstone := filepath.Join(artifactsDir, deletedArtifactPrefix+id)
	info, err := os.Lstat(tombstone)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("deleted artifact tombstone is not a directory")
	}
	if err := os.RemoveAll(tombstone); err != nil {
		return fmt.Errorf("remove deleted artifact tombstone: %w", err)
	}
	if err := syncDirectory(artifactsDir); err != nil {
		return fmt.Errorf("sync removed artifact tombstone: %w", err)
	}
	return nil
}

func (w *Workspace) readNote(ctx context.Context, id string) (result note.Note, resultErr error) {
	artifactsDir := filepath.Join(w.root, ".parchment", "artifacts")
	resultErr = withArtifactLock(ctx, artifactsDir, id, func() error {
		var err error
		result, err = w.readNoteUnlocked(id)
		return err
	})
	return result, resultErr
}

func (w *Workspace) readNoteUnlocked(id string) (note.Note, error) {
	dir := filepath.Join(w.root, ".parchment", "artifacts", id)
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return note.Note{}, fmt.Errorf("%w: %s", errNoMetadata, id)
		}
		return note.Note{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return note.Note{}, errors.New("note storage path is not a directory")
	}
	metadataPath := filepath.Join(dir, metadataName)
	metadataInfo, err := os.Lstat(metadataPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return note.Note{}, fmt.Errorf("%w: %s", errNoMetadata, id)
		}
		return note.Note{}, err
	}
	if !metadataInfo.Mode().IsRegular() {
		return note.Note{}, fmt.Errorf("note metadata %s is not a regular file", id)
	}
	metadata, err := os.Open(metadataPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return note.Note{}, fmt.Errorf("%w: %s", errNoMetadata, id)
		}
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
	contentPath := filepath.Join(dir, "content.md")
	contentInfo, err := os.Lstat(contentPath)
	if err != nil {
		return note.Note{}, fmt.Errorf("inspect note content %s: %w", id, err)
	}
	if !contentInfo.Mode().IsRegular() {
		return note.Note{}, fmt.Errorf("note content %s is not a regular file", id)
	}
	content, err := os.ReadFile(contentPath)
	if err != nil {
		return note.Note{}, fmt.Errorf("read note content %s: %w", id, err)
	}
	a.CreatedAt = a.CreatedAt.UTC()
	a.ModifiedAt = a.ModifiedAt.UTC()
	return note.Note{Artifact: a, Body: string(content)}, nil
}

func withArtifactLock(ctx context.Context, artifactsDir, id string, operation func() error) error {
	unlock, err := lockArtifact(ctx, artifactsDir, id)
	if err != nil {
		return err
	}
	operationErr := operation()
	return errors.Join(operationErr, unlock())
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
	return syncDirectory(dir)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func ensureWorkspaceDirectories(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	private := filepath.Join(root, ".parchment")
	if err := ensureDirectory(private); err != nil {
		return err
	}
	return ensureDirectory(filepath.Join(private, "artifacts"))
}

func ensureDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

func ensureExistingDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}
