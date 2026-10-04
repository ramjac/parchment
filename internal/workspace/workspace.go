package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/note"
)

const (
	transactionName       = ".parchment-transaction.json"
	deleteIntentPrefix    = ".parchment-delete-intent-"
	pendingArtifactPrefix = ".parchment-pending-delete-"
	deletedArtifactPrefix = ".parchment-deleted-"
)

type artifactDeletionIntent struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

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

// ValidateMarker checks the workspace config marker without following links.
func ValidateMarker(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	configPath := filepath.Join(abs, "parchment.toml")
	info, err := os.Lstat(configPath)
	if err != nil {
		return fmt.Errorf("inspect workspace config: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("workspace config path %s is not a regular file", configPath)
	}
	return nil
}

// Open returns an initialized workspace without creating workspace directories.
func Open(path string) (*Workspace, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	if err := ValidateMarker(abs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("open workspace: %s is not initialized (run `parchment init`)", abs)
		}
		return nil, fmt.Errorf("open workspace: %w", err)
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
		if id := strings.TrimPrefix(entry.Name(), deleteIntentPrefix); id != entry.Name() && validID.MatchString(id) {
			if err := withArtifactLock(context.Background(), artifacts, id, func() error {
				return recoverArtifactDeletion(artifacts, id)
			}); err != nil {
				return nil, fmt.Errorf("recover deletion of artifact %s: %w", id, err)
			}
			continue
		}
		if id := strings.TrimPrefix(entry.Name(), pendingArtifactPrefix); id != entry.Name() && validID.MatchString(id) {
			if err := withArtifactLock(context.Background(), artifacts, id, func() error {
				return restorePendingArtifact(artifacts, id)
			}); err != nil {
				return nil, fmt.Errorf("restore interrupted deletion of artifact %s: %w", id, err)
			}
			continue
		}
		if id := strings.TrimPrefix(entry.Name(), deletedArtifactPrefix); id != entry.Name() && validID.MatchString(id) {
			if err := withArtifactLock(context.Background(), artifacts, id, func() error {
				return restoreArtifactTombstone(artifacts, deletedArtifactPrefix, id)
			}); err != nil {
				return nil, fmt.Errorf("restore ambiguous legacy deletion of artifact %s: %w", id, err)
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
		if err := ValidateMarker(current); err == nil {
			return current, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
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
		var n note.Note
		err := withArtifactLock(ctx, root, entry.Name(), func() error {
			metadata, err := w.readArtifactMetadataUnlocked(entry.Name())
			if errors.Is(err, errNoMetadata) {
				return err
			}
			if err != nil {
				return err
			}
			if metadata.Kind != artifact.NoteKind {
				return errNotNote
			}
			n, err = w.readNoteUnlocked(entry.Name())
			return err
		})
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

func (w *Workspace) readArtifactMetadataUnlocked(id string) (artifact.Artifact, error) {
	dir := filepath.Join(w.root, ".parchment", "artifacts", id)
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return artifact.Artifact{}, errNoMetadata
		}
		return artifact.Artifact{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return artifact.Artifact{}, errors.New("artifact storage path is not a directory")
	}

	metadata, marked, err := readArtifactMetadata(filepath.Join(dir, "content.md"))
	if !marked {
		legacy, legacyErr := readLegacyArtifactMetadata(dir, id)
		if legacyErr == nil {
			return legacy, nil
		}
		if !errors.Is(legacyErr, os.ErrNotExist) {
			return artifact.Artifact{}, fmt.Errorf("read legacy artifact metadata %s: %w", id, legacyErr)
		}
	}
	if err == nil {
		if metadata.ID != id {
			return artifact.Artifact{}, fmt.Errorf("invalid artifact metadata for %s", id)
		}
		return metadata, nil
	}
	if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, artifactfile.ErrMetadataMissing) {
		return artifact.Artifact{}, err
	}
	if errors.Is(err, os.ErrNotExist) {
		return artifact.Artifact{}, errNoMetadata
	}
	return artifact.Artifact{}, fmt.Errorf("read artifact metadata %s: %w", id, err)
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
		dir := filepath.Join(artifactsDir, id)
		_, dirErr := os.Lstat(dir)
		if errors.Is(dirErr, os.ErrNotExist) {
			if expected == nil && target != nil {
				return w.saveLocked(ctx, *target)
			}
			return note.ErrNotFound
		}
		if dirErr != nil {
			return fmt.Errorf("inspect artifact storage: %w", dirErr)
		}
		current, err := w.readNoteUnlocked(id)
		if errors.Is(err, errNotNote) || errors.Is(err, errNoMetadata) {
			return fmt.Errorf("artifact ID %s is occupied by a non-note artifact", id)
		}
		if err != nil {
			return err
		}
		if expected == nil {
			return fmt.Errorf("note %s already exists", id)
		} else {
			if !note.Equal(current, *expected) {
				return fmt.Errorf("note %s changed since this operation was recorded", id)
			}
		}
		if target == nil {
			return w.deleteLocked(ctx, artifactsDir, id)
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
	blocks := make(map[string]any, len(n.Blocks))
	for name, payload := range n.Blocks {
		blocks[name] = payload
	}
	data, err := artifactfile.Encode(n.Artifact, n.Body, blocks)
	if err != nil {
		return fmt.Errorf("encode note artifact: %w", err)
	}

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
	if !created {
		if _, err := w.readNoteUnlocked(n.ID); err != nil {
			if errors.Is(err, errNotNote) || errors.Is(err, errNoMetadata) {
				return fmt.Errorf("artifact ID %s is occupied by a non-note artifact", n.ID)
			}
			return fmt.Errorf("inspect existing note before save: %w", err)
		}
	}
	if err := replaceArtifactFiles(ctx, dir, []stagedArtifactFile{{name: "content.md", data: data}}); err != nil {
		if created {
			if cleanupErr := os.Remove(dir); cleanupErr != nil {
				return fmt.Errorf("save note files: %w (also failed to remove new artifact directory: %v)", err, cleanupErr)
			}
		}
		return fmt.Errorf("save note files: %w", err)
	}
	if err := cleanupLegacyNoteMetadata(dir); err != nil {
		slog.Warn("note save committed but legacy metadata cleanup failed", "artifact_id", n.ID, "error", err)
	}
	return nil
}

func cleanupLegacyNoteMetadata(dir string) error {
	path := filepath.Join(dir, "metadata.json")
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return syncDirectory(dir)
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
		if !validArtifactFileName(file.Name) {
			return fmt.Errorf("invalid note transaction target %q", file.Name)
		}
		if file.HadOld {
			if filepath.Base(file.Backup) != file.Backup || !strings.HasPrefix(file.Backup, ".parchment-stage-") {
				return fmt.Errorf("invalid note transaction backup %q", file.Backup)
			}
		} else if file.Backup != "" {
			return fmt.Errorf("unexpected backup for new %s", file.Name)
		}
	}
	for _, file := range transaction {
		target := filepath.Join(dir, file.Name)
		if file.HadOld {
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
		return w.deleteLocked(ctx, artifactsDir, id)
	})
}

func (w *Workspace) deleteLocked(ctx context.Context, artifactsDir, id string) error {
	return w.deleteArtifactLocked(ctx, artifactsDir, id, func() error {
		if _, err := w.readNoteUnlocked(id); err != nil {
			if errors.Is(err, errNotNote) || errors.Is(err, errNoMetadata) {
				return fmt.Errorf("artifact ID %s is occupied by a non-note artifact", id)
			}
			return fmt.Errorf("validate note before deletion: %w", err)
		}
		return nil
	})
}

// deleteArtifactLocked removes an artifact directory through the recoverable
// tombstone sequence once validate confirms it is the expected kind.
func (w *Workspace) deleteArtifactLocked(ctx context.Context, artifactsDir, id string, validate func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(artifactsDir, id)
	intentPath := filepath.Join(artifactsDir, deleteIntentPrefix+id)
	if _, err := os.Lstat(intentPath); err == nil {
		if err := recoverArtifactDeletion(artifactsDir, id); err != nil {
			return fmt.Errorf("recover prior deletion: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect prior deletion intent: %w", err)
	}
	if err := restorePendingArtifact(artifactsDir, id); err != nil {
		return fmt.Errorf("restore prior deletion: %w", err)
	}
	if err := removeArtifactTombstone(artifactsDir, deletedArtifactPrefix, id); err != nil {
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
	if err := validate(); err != nil {
		return err
	}
	tombstone := filepath.Join(artifactsDir, pendingArtifactPrefix+id)
	if err := writeArtifactDeletionIntent(artifactsDir, artifactDeletionIntent{ID: id, State: "pending"}); err != nil {
		return fmt.Errorf("record deletion intent: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, removeArtifactDeletionIntent(artifactsDir, id))
	}
	if err := os.Rename(dir, tombstone); err != nil {
		return errors.Join(fmt.Errorf("stage note deletion: %w", err), removeArtifactDeletionIntent(artifactsDir, id))
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, rollbackPendingArtifactDeletion(artifactsDir, id))
	}
	if err := syncDirectory(artifactsDir); err != nil {
		return errors.Join(fmt.Errorf("sync deleted note directory: %w", err), rollbackPendingArtifactDeletion(artifactsDir, id))
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, rollbackPendingArtifactDeletion(artifactsDir, id))
	}
	if err := writeArtifactDeletionIntent(artifactsDir, artifactDeletionIntent{ID: id, State: "committed"}); err != nil {
		intent, readErr := readArtifactDeletionIntent(artifactsDir, id)
		if readErr == nil && intent.State == "committed" {
			slog.Warn("deletion intent committed despite directory sync error", "artifact_id", id, "error", err)
		} else {
			return errors.Join(fmt.Errorf("commit note deletion: %w", err), readErr, rollbackPendingArtifactDeletion(artifactsDir, id))
		}
	}
	if err := removeArtifactTombstone(artifactsDir, pendingArtifactPrefix, id); err != nil {
		slog.Warn("note deletion committed but tombstone cleanup failed", "artifact_id", id, "error", err)
		return nil
	}
	if err := removeArtifactDeletionIntent(artifactsDir, id); err != nil {
		slog.Warn("note deletion committed but intent cleanup failed", "artifact_id", id, "error", err)
	}
	return nil
}

func rollbackPendingArtifactDeletion(artifactsDir, id string) error {
	tombstone := filepath.Join(artifactsDir, pendingArtifactPrefix+id)
	dir := filepath.Join(artifactsDir, id)
	restoreErr := os.Rename(tombstone, dir)
	if restoreErr == nil {
		restoreErr = syncDirectory(artifactsDir)
	}
	if restoreErr != nil {
		return errors.Join(restoreErr, fmt.Errorf("pending note deletion remains recoverable at %s", tombstone))
	}
	if intentErr := removeArtifactDeletionIntent(artifactsDir, id); intentErr != nil {
		return fmt.Errorf("remove restored deletion intent: %w", intentErr)
	}
	return nil
}

func writeArtifactDeletionIntent(artifactsDir string, intent artifactDeletionIntent) error {
	data, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	path := filepath.Join(artifactsDir, deleteIntentPrefix+intent.ID)
	return writeAtomic(path, append(data, '\n'), 0o600)
}

func readArtifactDeletionIntent(artifactsDir, id string) (artifactDeletionIntent, error) {
	path := filepath.Join(artifactsDir, deleteIntentPrefix+id)
	info, err := os.Lstat(path)
	if err != nil {
		return artifactDeletionIntent{}, err
	}
	if !info.Mode().IsRegular() {
		return artifactDeletionIntent{}, errors.New("artifact deletion intent is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return artifactDeletionIntent{}, err
	}
	var intent artifactDeletionIntent
	if err := json.Unmarshal(data, &intent); err != nil {
		return artifactDeletionIntent{}, fmt.Errorf("decode artifact deletion intent: %w", err)
	}
	if intent.ID != id || (intent.State != "pending" && intent.State != "committed") {
		return artifactDeletionIntent{}, errors.New("invalid artifact deletion intent")
	}
	return intent, nil
}

func removeArtifactDeletionIntent(artifactsDir, id string) error {
	path := filepath.Join(artifactsDir, deleteIntentPrefix+id)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(artifactsDir)
}

func recoverArtifactDeletion(artifactsDir, id string) error {
	intent, err := readArtifactDeletionIntent(artifactsDir, id)
	if err != nil {
		return err
	}
	if intent.State == "pending" {
		if err := restorePendingArtifact(artifactsDir, id); err != nil {
			return err
		}
		return removeArtifactDeletionIntent(artifactsDir, id)
	}
	if err := removeArtifactTombstone(artifactsDir, pendingArtifactPrefix, id); err != nil {
		return err
	}
	return removeArtifactDeletionIntent(artifactsDir, id)
}

func restorePendingArtifact(artifactsDir, id string) error {
	return restoreArtifactTombstone(artifactsDir, pendingArtifactPrefix, id)
}

func restoreArtifactTombstone(artifactsDir, prefix, id string) error {
	tombstone := filepath.Join(artifactsDir, prefix+id)
	info, err := os.Lstat(tombstone)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("artifact tombstone is not a directory")
	}
	dir := filepath.Join(artifactsDir, id)
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("cannot restore artifact %s because its original path is occupied", id)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tombstone, dir); err != nil {
		return fmt.Errorf("restore artifact: %w", err)
	}
	return syncDirectory(artifactsDir)
}

func removeArtifactTombstone(artifactsDir, prefix, id string) error {
	tombstone := filepath.Join(artifactsDir, prefix+id)
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
	content, err := readRegularFile(filepath.Join(dir, "content.md"), artifactfile.MaxFileSize)
	if errors.Is(err, os.ErrNotExist) {
		a, legacyErr := readLegacyArtifactMetadata(dir, id)
		if legacyErr == nil {
			if a.Kind != artifact.NoteKind {
				return note.Note{}, errNotNote
			}
			return note.Note{}, fmt.Errorf("read note artifact %s: %w", id, err)
		}
		if !errors.Is(legacyErr, os.ErrNotExist) {
			return note.Note{}, fmt.Errorf("read legacy note metadata %s: %w", id, legacyErr)
		}
		return note.Note{}, fmt.Errorf("%w: %s", errNoMetadata, id)
	}
	if err != nil {
		return note.Note{}, fmt.Errorf("read note artifact %s: %w", id, err)
	}
	if !artifactfile.HasFormatMarker(content) {
		a, legacyErr := readLegacyArtifactMetadata(dir, id)
		if legacyErr == nil {
			if a.Kind != artifact.NoteKind {
				return note.Note{}, errNotNote
			}
			a.CreatedAt = a.CreatedAt.UTC()
			a.ModifiedAt = a.ModifiedAt.UTC()
			return note.Note{Artifact: a, Body: string(content)}, nil
		}
		if !errors.Is(legacyErr, os.ErrNotExist) {
			return note.Note{}, fmt.Errorf("read legacy note metadata %s: %w", id, legacyErr)
		}
	}
	metadata, err := artifactfile.ReadMetadataEnvelope(content)
	if err != nil {
		return note.Note{}, fmt.Errorf("read note metadata %s: %w", id, err)
	}
	if metadata.ID != id {
		return note.Note{}, fmt.Errorf("invalid note metadata for %s", id)
	}
	if metadata.Kind != artifact.NoteKind {
		return note.Note{}, errNotNote
	}
	file, err := artifactfile.Decode(content)
	if err != nil {
		return note.Note{}, fmt.Errorf("decode note artifact %s: %w", id, err)
	}
	a := file.Artifact
	if a.ID != id {
		return note.Note{}, fmt.Errorf("invalid note metadata for %s", id)
	}
	if a.Kind != artifact.NoteKind {
		return note.Note{}, errNotNote
	}
	if a.Location != filepath.ToSlash(filepath.Join(".parchment", "artifacts", id, "content.md")) {
		return note.Note{}, fmt.Errorf("invalid note metadata for %s", id)
	}
	a.CreatedAt = a.CreatedAt.UTC()
	a.ModifiedAt = a.ModifiedAt.UTC()
	return note.Note{Artifact: a, Body: file.Body, Blocks: copyPayloadBlocks(file.Blocks, "")}, nil
}

func readLegacyArtifactMetadata(dir, id string) (artifact.Artifact, error) {
	data, err := readRegularFile(filepath.Join(dir, "metadata.json"), 1<<20)
	if err != nil {
		return artifact.Artifact{}, err
	}
	var item artifact.Artifact
	if err := json.Unmarshal(data, &item); err != nil {
		return artifact.Artifact{}, fmt.Errorf("decode artifact metadata: %w", err)
	}
	if err := item.Validate(); err != nil {
		return artifact.Artifact{}, fmt.Errorf("validate artifact metadata: %w", err)
	}
	if item.ID != id || item.Location != filepath.ToSlash(filepath.Join(".parchment", "artifacts", id, "content.md")) {
		return artifact.Artifact{}, fmt.Errorf("invalid artifact metadata for %s", id)
	}
	return item, nil
}

func copyPayloadBlocks(blocks map[string]json.RawMessage, excluded string) map[string]json.RawMessage {
	var copied map[string]json.RawMessage
	for name, payload := range blocks {
		if name == excluded {
			continue
		}
		if copied == nil {
			copied = make(map[string]json.RawMessage, len(blocks))
		}
		copied[name] = append(json.RawMessage(nil), payload...)
	}
	return copied
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
