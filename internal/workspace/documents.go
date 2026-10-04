package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

const layoutName = "layout.json"

var errNotDocument = errors.New("artifact is not a document")

// validArtifactFileName lists the files an artifact transaction may touch.
func validArtifactFileName(name string) bool {
	return name == "content.md" || name == metadataName || name == layoutName || document.IsImageName(name)
}

// ListDocuments implements the document repository interface. Embedded image
// data is omitted; GetDocument returns complete documents.
func (w *Workspace) ListDocuments(ctx context.Context) ([]document.Document, error) {
	root := filepath.Join(w.root, ".parchment", "artifacts")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	var documents []document.Document
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !validID.MatchString(entry.Name()) {
			continue
		}
		var d document.Document
		err := withArtifactLock(ctx, root, entry.Name(), func() error {
			var readErr error
			d, readErr = w.readDocumentUnlocked(entry.Name(), false)
			return readErr
		})
		if errors.Is(err, errNotDocument) || errors.Is(err, errNoMetadata) {
			continue
		}
		if err != nil {
			return nil, err
		}
		documents = append(documents, d)
	}
	return documents, nil
}

// GetDocument returns the document, with its images, for a stable ID.
func (w *Workspace) GetDocument(ctx context.Context, id string) (document.Document, error) {
	if err := ctx.Err(); err != nil {
		return document.Document{}, err
	}
	if !validID.MatchString(id) {
		return document.Document{}, document.ErrNotFound
	}
	var d document.Document
	err := withArtifactLock(ctx, filepath.Join(w.root, ".parchment", "artifacts"), id, func() error {
		var readErr error
		d, readErr = w.readDocumentUnlocked(id, true)
		return readErr
	})
	if errors.Is(err, errNotDocument) || errors.Is(err, errNoMetadata) {
		return document.Document{}, document.ErrNotFound
	}
	return d, err
}

// TransitionDocument applies a document update only if its current value
// matches expected. A nil expected value means the document must not exist; a
// nil target deletes it.
func (w *Workspace) TransitionDocument(ctx context.Context, id string, expected, target *document.Document) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(id) {
		return document.ErrNotFound
	}
	if expected != nil && expected.ID != id {
		return errors.New("expected document ID does not match transition ID")
	}
	if target != nil && target.ID != id {
		return errors.New("target document ID does not match transition ID")
	}
	artifactsDir := filepath.Join(w.root, ".parchment", "artifacts")
	return withArtifactLock(ctx, artifactsDir, id, func() error {
		_, dirErr := os.Lstat(filepath.Join(artifactsDir, id))
		if errors.Is(dirErr, os.ErrNotExist) {
			if expected == nil && target != nil {
				return w.saveDocumentLocked(ctx, *target)
			}
			return document.ErrNotFound
		}
		if dirErr != nil {
			return fmt.Errorf("inspect artifact storage: %w", dirErr)
		}
		current, err := w.readDocumentUnlocked(id, true)
		if errors.Is(err, errNotDocument) || errors.Is(err, errNoMetadata) {
			return fmt.Errorf("artifact ID %s is occupied by a non-document artifact", id)
		}
		if err != nil {
			return err
		}
		if expected == nil {
			return fmt.Errorf("document %s already exists", id)
		}
		if !document.Equal(current, *expected) {
			return fmt.Errorf("document %s changed since this operation was recorded", id)
		}
		if target == nil {
			return w.deleteArtifactLocked(ctx, artifactsDir, id, func() error { return nil })
		}
		return w.saveDocumentLocked(ctx, *target)
	})
}

func (w *Workspace) saveDocumentLocked(ctx context.Context, d document.Document) error {
	if !validID.MatchString(d.ID) || d.Kind != artifact.DocumentKind {
		return errors.New("invalid document artifact")
	}
	if err := d.Artifact.Validate(); err != nil {
		return err
	}
	if d.Location != filepath.ToSlash(filepath.Join(".parchment", "artifacts", d.ID, "content.md")) {
		return errors.New("invalid document content location")
	}
	if err := d.Layout.Validate(); err != nil {
		return err
	}
	metadata, err := json.MarshalIndent(d.Artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode document metadata: %w", err)
	}
	layout, err := json.MarshalIndent(d.Layout, "", "  ")
	if err != nil {
		return fmt.Errorf("encode document layout: %w", err)
	}
	files := []stagedArtifactFile{
		{name: "content.md", data: []byte(d.Body)},
		{name: layoutName, data: append(layout, '\n')},
		{name: metadataName, data: append(metadata, '\n')},
	}
	keep := map[string]bool{}
	for _, img := range d.Images {
		if !document.IsImageName(img.Name) {
			return fmt.Errorf("invalid document image name %q", img.Name)
		}
		keep[img.Name] = true
		files = append(files, stagedArtifactFile{name: img.Name, data: img.Data})
	}

	dir := filepath.Join(w.root, ".parchment", "artifacts", d.ID)
	created := false
	if err := os.Mkdir(dir, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create document storage: %w", err)
		}
		info, statErr := os.Lstat(dir)
		if statErr != nil {
			return fmt.Errorf("inspect document storage: %w", statErr)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("document storage path is not a directory")
		}
	} else {
		created = true
		if err := syncDirectory(filepath.Dir(dir)); err != nil {
			_ = os.Remove(dir)
			return fmt.Errorf("sync document storage parent: %w", err)
		}
	}
	if err := recoverArtifactFiles(dir); err != nil {
		if created {
			_ = os.Remove(dir)
		}
		return fmt.Errorf("recover document files: %w", err)
	}
	if !created {
		if _, err := w.readDocumentUnlocked(d.ID, false); err != nil {
			if errors.Is(err, errNotDocument) || errors.Is(err, errNoMetadata) {
				return fmt.Errorf("artifact ID %s is occupied by a non-document artifact", d.ID)
			}
			return fmt.Errorf("inspect existing document before save: %w", err)
		}
	}
	if err := replaceArtifactFiles(ctx, dir, files); err != nil {
		if created {
			if cleanupErr := os.Remove(dir); cleanupErr != nil {
				return fmt.Errorf("save document files: %w (also failed to remove new artifact directory: %v)", err, cleanupErr)
			}
		}
		return fmt.Errorf("save document files: %w", err)
	}
	removeStaleImages(dir, keep)
	return nil
}

// removeStaleImages deletes managed image files that the saved document no
// longer embeds. The save has already committed, so a failure here only
// leaves an unreferenced file behind.
func removeStaleImages(dir string, keep map[string]bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	removed := false
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !document.IsImageName(entry.Name()) || keep[entry.Name()] {
			continue
		}
		if os.Remove(filepath.Join(dir, entry.Name())) == nil {
			removed = true
		}
	}
	if removed {
		_ = syncDirectory(dir)
	}
}

func (w *Workspace) readDocumentUnlocked(id string, withImages bool) (document.Document, error) {
	dir := filepath.Join(w.root, ".parchment", "artifacts", id)
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return document.Document{}, fmt.Errorf("%w: %s", errNoMetadata, id)
		}
		return document.Document{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return document.Document{}, errors.New("document storage path is not a directory")
	}
	metadata, err := readRegularFile(filepath.Join(dir, metadataName), 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return document.Document{}, fmt.Errorf("%w: %s", errNoMetadata, id)
	}
	if err != nil {
		return document.Document{}, fmt.Errorf("read document metadata %s: %w", id, err)
	}
	var a artifact.Artifact
	if err := json.Unmarshal(metadata, &a); err != nil {
		return document.Document{}, fmt.Errorf("decode artifact metadata %s: %w", id, err)
	}
	if err := a.Validate(); err != nil {
		return document.Document{}, fmt.Errorf("validate artifact metadata %s: %w", id, err)
	}
	if a.ID != id {
		return document.Document{}, fmt.Errorf("invalid document metadata for %s", id)
	}
	if a.Kind != artifact.DocumentKind {
		return document.Document{}, errNotDocument
	}
	if a.Location != filepath.ToSlash(filepath.Join(".parchment", "artifacts", id, "content.md")) {
		return document.Document{}, fmt.Errorf("invalid document metadata for %s", id)
	}
	content, err := readRegularFile(filepath.Join(dir, "content.md"), -1)
	if err != nil {
		return document.Document{}, fmt.Errorf("read document content %s: %w", id, err)
	}
	layout := document.DefaultLayout()
	layoutData, err := readRegularFile(filepath.Join(dir, layoutName), 1<<20)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return document.Document{}, fmt.Errorf("read document layout %s: %w", id, err)
	default:
		if err := json.Unmarshal(layoutData, &layout); err != nil {
			return document.Document{}, fmt.Errorf("decode document layout %s: %w", id, err)
		}
		if err := layout.Validate(); err != nil {
			return document.Document{}, fmt.Errorf("validate document layout %s: %w", id, err)
		}
	}
	a.CreatedAt = a.CreatedAt.UTC()
	a.ModifiedAt = a.ModifiedAt.UTC()
	d := document.Document{Artifact: a, Body: string(content), Layout: layout}
	if withImages {
		if d.Images, err = readDocumentImages(dir); err != nil {
			return document.Document{}, fmt.Errorf("read document images %s: %w", id, err)
		}
	}
	return d, nil
}

// readDocumentImages loads managed image files in name order.
func readDocumentImages(dir string) ([]document.Image, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var images []document.Image
	for _, entry := range entries {
		if !document.IsImageName(entry.Name()) {
			continue
		}
		data, err := readRegularFile(filepath.Join(dir, entry.Name()), document.MaxImageBytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		images = append(images, document.Image{Name: entry.Name(), Data: data})
	}
	return images, nil
}

// readRegularFile reads a regular file without following links. A negative
// limit means no size limit; otherwise larger files are an error.
func readRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if limit < 0 {
		return io.ReadAll(file)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return data, nil
}
