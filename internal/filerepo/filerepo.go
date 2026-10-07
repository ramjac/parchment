// Package filerepo stores each Parchment artifact as one ordinary,
// user-named Markdown file. It never manages a directory of artifacts: callers
// address artifacts by file path, and files may live anywhere alongside the
// user's other files. Application state such as recovery drafts and the write
// lock lives in a separate state directory.
package filerepo

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
)

// ErrExists reports that a create operation targeted an existing path.
var ErrExists = errors.New("file already exists")

// ErrNotArtifact reports that a file has no Parchment metadata envelope.
var ErrNotArtifact = errors.New("file is not a Parchment artifact")

// Repository reads and writes artifact files by path.
type Repository struct {
	stateDir string
}

// New returns a repository that keeps its write lock and recovery drafts in
// stateDir, which is created on demand with owner-only permissions.
func New(stateDir string) (*Repository, error) {
	if stateDir == "" {
		return nil, errors.New("state directory is required")
	}
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		return nil, fmt.Errorf("resolve state directory: %w", err)
	}
	return &Repository{stateDir: abs}, nil
}

// KindError reports that a file holds a different kind of artifact.
type KindError struct {
	Path string
	Kind artifact.Kind
	Want artifact.Kind
}

func (e *KindError) Error() string {
	return fmt.Sprintf("%s is a %s, not a %s", e.Path, e.Kind, e.Want)
}

// Kind returns the artifact kind recorded in a file's metadata.
func Kind(path string) (artifact.Kind, error) {
	resolved, err := resolve(path)
	if err != nil {
		return "", err
	}
	file, err := openRegularFile(resolved)
	if err != nil {
		return "", err
	}
	defer file.Close()
	metadata, err := artifactfile.ReadMetadataFrom(file)
	if errors.Is(err, artifactfile.ErrMetadataMissing) {
		return "", fmt.Errorf("%s: %w", path, ErrNotArtifact)
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return metadata.Kind, nil
}

// readContent reads an artifact file and checks its kind. A missing file
// returns notFound wrapped with the path.
func readContent(path string, want artifact.Kind, notFound error) ([]byte, error) {
	resolved, err := resolve(path)
	if err != nil {
		return nil, err
	}
	content, err := readRegularFile(resolved, artifactfile.MaxFileSize)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, notFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := checkKind(path, content, want); err != nil {
		return nil, err
	}
	return content, nil
}

func checkKind(path string, content []byte, want artifact.Kind) error {
	metadata, err := artifactfile.ReadMetadata(content)
	if errors.Is(err, artifactfile.ErrMetadataMissing) {
		return fmt.Errorf("%s: %w", path, ErrNotArtifact)
	}
	if err != nil {
		return fmt.Errorf("read metadata %s: %w", path, err)
	}
	if metadata.Kind != want {
		return &KindError{Path: path, Kind: metadata.Kind, Want: want}
	}
	return nil
}

// decodeArtifact decodes content already checked by checkKind.
func decodeArtifact(path string, content []byte) (artifactfile.File, error) {
	file, err := artifactfile.Decode(content)
	if err != nil {
		return artifactfile.File{}, fmt.Errorf("decode %s: %w", path, err)
	}
	file.Artifact.CreatedAt = file.Artifact.CreatedAt.UTC()
	file.Artifact.ModifiedAt = file.Artifact.ModifiedAt.UTC()
	setRuntimeIdentity(&file.Artifact, path)
	return file, nil
}

// setRuntimeIdentity derives the artifact's runtime ID and title from its
// path. Neither is persisted; the file name is how users identify a file.
func setRuntimeIdentity(item *artifact.Artifact, path string) {
	item.Path = path
	item.ID = pathID(item.Kind, path)
	item.Title = pathTitle(path)
}

// pathID returns a stable, valid artifact ID for a path.
func pathID(kind artifact.Kind, path string) string {
	prefix := map[artifact.Kind]byte{
		artifact.NoteKind: 'n', artifact.DocumentKind: 'd', artifact.SpreadsheetKind: 's',
		artifact.PresentationKind: 'p', artifact.ImageKind: 'i',
	}[kind]
	digest := sha256.Sum256([]byte(path))
	return string(prefix) + new(big.Int).SetBytes(digest[:16]).Text(36)
}

// pathTitle returns the file name without its extension.
func pathTitle(path string) string {
	base := filepath.Base(path)
	if title := strings.TrimSuffix(base, filepath.Ext(base)); title != "" {
		return title
	}
	return base
}

// update writes one artifact file under the repository's write lock. When
// create is true the path must not exist and write receives nil; otherwise the
// file must exist and write receives its current content.
func (r *Repository) update(ctx context.Context, path string, create bool, notFound error, write func([]byte) ([]byte, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.withLock(ctx, func() error {
		resolved, err := resolve(path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		var current []byte
		info, statErr := os.Lstat(resolved)
		switch {
		case create && statErr == nil:
			return fmt.Errorf("%s: %w", path, ErrExists)
		case create && !errors.Is(statErr, fs.ErrNotExist):
			return fmt.Errorf("inspect %s: %w", path, statErr)
		case !create && errors.Is(statErr, fs.ErrNotExist):
			return fmt.Errorf("%s: %w", path, notFound)
		case !create && statErr != nil:
			return fmt.Errorf("inspect %s: %w", path, statErr)
		case !create:
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%s is not a regular file", path)
			}
			mode = info.Mode().Perm()
			current, err = readRegularFile(resolved, artifactfile.MaxFileSize)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
		}
		data, err := write(current)
		if err != nil {
			return err
		}
		if create {
			err = createAtomic(ctx, resolved, data, mode)
		} else {
			err = writeAtomic(ctx, resolved, data, mode)
		}
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s: %w", path, ErrExists)
		}
		if err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	})
}

func (r *Repository) withLock(ctx context.Context, operation func() error) error {
	if err := ensureDirectory(r.stateDir); err != nil {
		return fmt.Errorf("open state directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(r.stateDir, "write.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open write lock: %w", err)
	}
	unlock, err := lockFile(ctx, file)
	if err != nil {
		return errors.Join(fmt.Errorf("acquire write lock: %w", err), file.Close())
	}
	operationErr := operation()
	return errors.Join(operationErr, unlock(), file.Close())
}

// resolve returns an absolute path with symbolic links in existing components
// evaluated, so atomic replacement updates the link target instead of
// replacing the link.
func resolve(path string) (string, error) {
	if path == "" {
		return "", errors.New("file path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("resolve directory of %s: %w", path, err)
	}
	return filepath.Join(dir, filepath.Base(abs)), nil
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

func encodeBlocks(blocks map[string]json.RawMessage) map[string]any {
	encoded := make(map[string]any, len(blocks)+1)
	for name, payload := range blocks {
		encoded[name] = payload
	}
	return encoded
}

func validateTarget(item artifact.Artifact, kind artifact.Kind) error {
	if item.Kind != kind {
		return fmt.Errorf("invalid %s artifact", kind)
	}
	return item.Validate()
}

// changedError reports an optimistic-concurrency conflict, typically because
// the file was edited outside this process.
func changedError(path string) error {
	return fmt.Errorf("%s changed since it was loaded; reload it and try again", path)
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	file, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return data, nil
}

func openRegularFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	return os.Open(path)
}

// writeAtomic replaces path with data using a temporary file in the same
// directory followed by sync and rename. It does not create directories.
// It honors cancellation up to the rename.
func writeAtomic(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	temp, err := writeTemp(path, data, mode)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// createAtomic writes data to a new file at path without ever replacing an
// existing file, even one created concurrently by another program. It links a
// fully written temporary file into place, which fails if path exists. On
// filesystems without hard links it falls back to an exclusive create.
func createAtomic(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	temp, err := writeTemp(path, data, mode)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	switch err := os.Link(temp, path); {
	case err == nil:
		return syncDirectory(filepath.Dir(path))
	case errors.Is(err, fs.ErrExist):
		return err
	}
	return createExclusive(path, data, mode)
}

func createExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// writeTemp writes data to a synced temporary file beside path and returns
// its name.
func writeTemp(path string, data []byte, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".parchment-*")
	if err != nil {
		return "", err
	}
	temp := file.Name()
	written := false
	defer func() {
		if !written {
			_ = os.Remove(temp)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	written = true
	return temp, nil
}

func ensureDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	// MkdirAll leaves an existing directory's mode unchanged; keep
	// application state owner-only even if the directory was pre-created.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("restrict permissions of %s: %w", path, err)
		}
	}
	return nil
}
