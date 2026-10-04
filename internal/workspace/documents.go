package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/document"
)

const documentDataBlock = "parchment-document"

var errNotDocument = errors.New("artifact is not a document")

// validArtifactFileName accepts current and legacy targets in recovery journals.
func validArtifactFileName(name string) bool {
	switch name {
	case "content.md", "metadata.json", "layout.json", "changes.json",
		"spreadsheet.json", "presentation.md":
		return true
	default:
		return document.IsImageName(name)
	}
}

type embeddedDocumentImage struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

type documentFileData struct {
	Layout  document.Layout         `json:"layout"`
	Changes []document.Change       `json:"changes,omitempty"`
	Images  []embeddedDocumentImage `json:"images,omitempty"`
}

type documentFileDataWithoutImages struct {
	Layout  document.Layout   `json:"layout"`
	Changes []document.Change `json:"changes,omitempty"`
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
	return w.transitionDocument(ctx, id, expected, target, nil)
}

// TransitionDocumentWithChanges restores a document and its proposals as one
// atomic artifact write.
func (w *Workspace) TransitionDocumentWithChanges(
	ctx context.Context, id string, expected, target *document.Document, changes []document.Change,
) error {
	return w.transitionDocument(ctx, id, expected, target, &changes)
}

func (w *Workspace) transitionDocument(
	ctx context.Context, id string, expected, target *document.Document, changes *[]document.Change,
) error {
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
				if changes != nil {
					return w.saveDocumentLockedWithChanges(ctx, *target, *changes)
				}
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
		if changes != nil {
			return w.saveDocumentLockedWithChanges(ctx, *target, *changes)
		}
		return w.saveDocumentLocked(ctx, *target)
	})
}

// DeleteDocument atomically captures proposal history and deletes the document.
func (w *Workspace) DeleteDocument(ctx context.Context, id string, expected document.Document) ([]document.Change, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validID.MatchString(id) {
		return nil, document.ErrNotFound
	}
	if expected.ID != id {
		return nil, errors.New("expected document ID does not match deletion ID")
	}
	artifactsDir := filepath.Join(w.root, ".parchment", "artifacts")
	var changes []document.Change
	err := withArtifactLock(ctx, artifactsDir, id, func() error {
		current, err := w.readDocumentUnlocked(id, true)
		if errors.Is(err, errNotDocument) || errors.Is(err, errNoMetadata) {
			return document.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !document.Equal(current, expected) {
			return fmt.Errorf("document %s changed since this operation was recorded", id)
		}
		changes, err = w.readDocumentChangesUnlocked(id)
		if err != nil {
			return err
		}
		return w.deleteArtifactLocked(ctx, artifactsDir, id, func() error {
			deleted, err := w.readDocumentUnlocked(id, true)
			if err != nil {
				return err
			}
			if !document.Equal(deleted, expected) {
				return fmt.Errorf("document %s changed before deletion", id)
			}
			currentChanges, err := w.readDocumentChangesUnlocked(id)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(currentChanges, changes) {
				return errors.New("document proposals changed before deletion")
			}
			return nil
		})
	})
	return changes, err
}

// ListDocumentChanges returns the persisted proposals for a document.
func (w *Workspace) ListDocumentChanges(ctx context.Context, id string) ([]document.Change, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validID.MatchString(id) {
		return nil, document.ErrNotFound
	}
	var changes []document.Change
	err := withArtifactLock(ctx, filepath.Join(w.root, ".parchment", "artifacts"), id, func() error {
		if _, err := w.readDocumentUnlocked(id, false); err != nil {
			return err
		}
		var readErr error
		changes, readErr = w.readDocumentChangesUnlocked(id)
		return readErr
	})
	if errors.Is(err, errNotDocument) || errors.Is(err, errNoMetadata) {
		return nil, document.ErrNotFound
	}
	return changes, err
}

// ProposeDocumentChange persists a proposal only if the document still matches
// the snapshot from which the proposal was created.
func (w *Workspace) ProposeDocumentChange(ctx context.Context, id string, expected document.Document, change document.Change) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(id) {
		return document.ErrNotFound
	}
	if expected.ID != id || change.DocumentID != id {
		return errors.New("document ID does not match proposal")
	}
	if err := change.Validate(); err != nil {
		return err
	}
	if change.Status != document.ChangePending {
		return errors.New("new document change must be pending")
	}
	artifactsDir := filepath.Join(w.root, ".parchment", "artifacts")
	return withArtifactLock(ctx, artifactsDir, id, func() error {
		current, err := w.readDocumentUnlocked(id, true)
		if err != nil {
			return err
		}
		if !document.Equal(current, expected) {
			return fmt.Errorf("document %s changed since it was loaded", id)
		}
		if !document.ChangeSnapshotEqual(change.Before, document.ChangeSnapshotOf(current)) {
			return errors.New("proposal original snapshot does not match the document")
		}
		changes, err := w.readDocumentChangesUnlocked(id)
		if err != nil {
			return err
		}
		for _, existing := range changes {
			if existing.ID == change.ID {
				return errors.New("document change ID already exists")
			}
			if existing.Status == document.ChangePending {
				return errors.New("resolve the existing document change before proposing another")
			}
		}
		changes = append(changes, change)
		return w.writeDocumentChangesLocked(ctx, id, changes)
	})
}

// TransitionDocumentChange resolves a proposal, optionally updating the live
// document in the same filesystem transaction.
func (w *Workspace) TransitionDocumentChange(
	ctx context.Context, id, changeID string, expected, target document.Change,
	expectedDocument, targetDocument *document.Document,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(id) || !validID.MatchString(changeID) {
		return document.ErrChangeNotFound
	}
	if expected.DocumentID != id || target.DocumentID != id || expected.ID != changeID || target.ID != changeID {
		return errors.New("document change ID does not match transition")
	}
	if err := target.Validate(); err != nil {
		return err
	}
	artifactsDir := filepath.Join(w.root, ".parchment", "artifacts")
	return withArtifactLock(ctx, artifactsDir, id, func() error {
		current, err := w.readDocumentUnlocked(id, true)
		if err != nil {
			return err
		}
		changes, err := w.readDocumentChangesUnlocked(id)
		if err != nil {
			return err
		}
		index := -1
		for i := range changes {
			if changes[i].ID == changeID {
				index = i
				break
			}
		}
		if index < 0 {
			return document.ErrChangeNotFound
		}
		if !reflect.DeepEqual(changes[index], expected) {
			return errors.New("document change is no longer in the expected state")
		}
		if expected.Status != document.ChangePending ||
			(target.Status != document.ChangeAccepted && target.Status != document.ChangeRejected) {
			return errors.New("invalid document change transition")
		}
		if (expectedDocument == nil) != (targetDocument == nil) {
			return errors.New("document transition must include both expected and target documents")
		}
		changes[index] = target
		if expectedDocument == nil {
			return w.writeDocumentChangesLocked(ctx, id, changes)
		}
		if expectedDocument.ID != id || targetDocument.ID != id || !document.Equal(current, *expectedDocument) {
			return errors.New("document changed since the proposal was reviewed")
		}
		if !document.ChangeSnapshotEqual(target.Before, document.ChangeSnapshotOf(*expectedDocument)) ||
			!document.ChangeSnapshotEqual(target.After, document.ChangeSnapshotOf(*targetDocument)) {
			return errors.New("accepted document does not match the proposal")
		}
		return w.saveDocumentLockedWithChanges(ctx, *targetDocument, changes)
	})
}

func (w *Workspace) saveDocumentLocked(ctx context.Context, d document.Document) error {
	return w.saveDocumentLockedWithChanges(ctx, d, nil)
}

func (w *Workspace) saveDocumentLockedWithChanges(ctx context.Context, d document.Document, changes []document.Change) error {
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
	payload := documentFileData{Layout: d.Layout}
	seenImages := map[string]bool{}
	for _, img := range d.Images {
		if err := img.Validate(); err != nil {
			return err
		}
		if seenImages[img.Name] {
			return fmt.Errorf("duplicate document image %s", img.Name)
		}
		seenImages[img.Name] = true
		payload.Images = append(payload.Images, embeddedDocumentImage{Name: img.Name, Data: img.Data})
	}
	if changes != nil {
		if err := validateDocumentChanges(d.ID, changes); err != nil {
			return err
		}
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
		if changes == nil {
			var err error
			changes, err = w.readDocumentChangesUnlocked(d.ID)
			if err != nil {
				return err
			}
		}
	}
	payload.Changes = changes
	blocks := make(map[string]any, len(d.Blocks)+1)
	for name, block := range d.Blocks {
		blocks[name] = block
	}
	blocks[documentDataBlock] = payload
	data, err := artifactfile.Encode(d.Artifact, d.Body, blocks)
	if err != nil {
		if created {
			if cleanupErr := os.Remove(dir); cleanupErr != nil {
				return fmt.Errorf("encode document artifact: %w (also failed to remove new artifact directory: %v)", err, cleanupErr)
			}
		}
		return fmt.Errorf("encode document artifact: %w", err)
	}
	if err := replaceArtifactFiles(ctx, dir, []stagedArtifactFile{{name: "content.md", data: data}}); err != nil {
		if created {
			if cleanupErr := os.Remove(dir); cleanupErr != nil {
				return fmt.Errorf("save document files: %w (also failed to remove new artifact directory: %v)", err, cleanupErr)
			}
		}
		return fmt.Errorf("save document files: %w", err)
	}
	return nil
}

func validateDocumentChanges(id string, changes []document.Change) error {
	seen := map[string]bool{}
	for _, change := range changes {
		if err := change.Validate(); err != nil {
			return err
		}
		if change.DocumentID != id || seen[change.ID] {
			return errors.New("invalid document change list")
		}
		seen[change.ID] = true
	}
	return nil
}

func (w *Workspace) readDocumentChangesUnlocked(id string) ([]document.Change, error) {
	_, changes, err := w.readDocumentFileUnlocked(id, false)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, change := range changes {
		if err := change.Validate(); err != nil {
			return nil, fmt.Errorf("validate document change %s: %w", change.ID, err)
		}
		if change.DocumentID != id || seen[change.ID] {
			return nil, fmt.Errorf("invalid document change list for %s", id)
		}
		seen[change.ID] = true
	}
	return changes, nil
}

func (w *Workspace) writeDocumentChangesLocked(ctx context.Context, id string, changes []document.Change) error {
	current, err := w.readDocumentUnlocked(id, true)
	if err != nil {
		return err
	}
	if err := w.saveDocumentLockedWithChanges(ctx, current, changes); err != nil {
		return fmt.Errorf("save document changes: %w", err)
	}
	return nil
}

func (w *Workspace) readDocumentUnlocked(id string, withImages bool) (document.Document, error) {
	item, _, err := w.readDocumentFileUnlocked(id, withImages)
	return item, err
}

func (w *Workspace) readDocumentFileUnlocked(id string, withImages bool) (document.Document, []document.Change, error) {
	dir := filepath.Join(w.root, ".parchment", "artifacts", id)
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return document.Document{}, nil, fmt.Errorf("%w: %s", errNoMetadata, id)
		}
		return document.Document{}, nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return document.Document{}, nil, errors.New("document storage path is not a directory")
	}
	content, err := readRegularFile(filepath.Join(dir, "content.md"), 64<<20)
	if errors.Is(err, os.ErrNotExist) {
		a, legacyErr := readLegacyArtifactMetadata(dir, id)
		if legacyErr == nil {
			if a.Kind != artifact.DocumentKind {
				return document.Document{}, nil, errNotDocument
			}
			return document.Document{}, nil, fmt.Errorf("read document artifact %s: %w", id, err)
		}
		if !errors.Is(legacyErr, os.ErrNotExist) {
			return document.Document{}, nil, fmt.Errorf("read legacy document metadata %s: %w", id, legacyErr)
		}
		return document.Document{}, nil, fmt.Errorf("%w: %s", errNoMetadata, id)
	}
	if err != nil {
		return document.Document{}, nil, fmt.Errorf("read document artifact %s: %w", id, err)
	}
	metadata, err := artifactfile.ReadMetadata(content)
	if err != nil {
		if !errors.Is(err, artifactfile.ErrMetadataMissing) {
			return document.Document{}, nil, fmt.Errorf("read document metadata %s: %w", id, err)
		}
		return w.readLegacyDocumentFileUnlocked(id, dir, content, withImages, err)
	}
	if metadata.ID != id {
		return document.Document{}, nil, fmt.Errorf("invalid document metadata for %s", id)
	}
	if metadata.Kind != artifact.DocumentKind {
		return document.Document{}, nil, errNotDocument
	}
	file, err := artifactfile.Decode(content)
	if err != nil {
		return document.Document{}, nil, fmt.Errorf("decode document artifact %s: %w", id, err)
	}
	a := file.Artifact
	if a.ID != id {
		return document.Document{}, nil, fmt.Errorf("invalid document metadata for %s", id)
	}
	if a.Kind != artifact.DocumentKind {
		return document.Document{}, nil, errNotDocument
	}
	if a.Location != filepath.ToSlash(filepath.Join(".parchment", "artifacts", id, "content.md")) {
		return document.Document{}, nil, fmt.Errorf("invalid document metadata for %s", id)
	}
	payloadJSON, ok := file.Blocks[documentDataBlock]
	if !ok {
		return document.Document{}, nil, errors.New("document data block is missing")
	}
	var payload documentFileData
	if withImages {
		if err := json.Unmarshal(payloadJSON, &payload); err != nil {
			return document.Document{}, nil, fmt.Errorf("decode document data %s: %w", id, err)
		}
	} else {
		var lightweight documentFileDataWithoutImages
		if err := json.Unmarshal(payloadJSON, &lightweight); err != nil {
			return document.Document{}, nil, fmt.Errorf("decode document data %s: %w", id, err)
		}
		payload.Layout = lightweight.Layout
		payload.Changes = lightweight.Changes
	}
	if payload.Layout == (document.Layout{}) {
		payload.Layout = document.DefaultLayout()
	}
	if err := payload.Layout.Validate(); err != nil {
		return document.Document{}, nil, fmt.Errorf("validate document layout %s: %w", id, err)
	}
	if err := validateDocumentChanges(id, payload.Changes); err != nil {
		return document.Document{}, nil, fmt.Errorf("validate document changes %s: %w", id, err)
	}
	a.CreatedAt = a.CreatedAt.UTC()
	a.ModifiedAt = a.ModifiedAt.UTC()
	d := document.Document{
		Artifact: a, Body: file.Body, Layout: payload.Layout,
		Blocks: copyPayloadBlocks(file.Blocks, documentDataBlock),
	}
	seenImages := map[string]bool{}
	for _, imageData := range payload.Images {
		img := document.Image{Name: imageData.Name, Data: imageData.Data}
		if err := img.Validate(); err != nil {
			return document.Document{}, nil, fmt.Errorf("validate document image %s: %w", img.Name, err)
		}
		if seenImages[img.Name] {
			return document.Document{}, nil, fmt.Errorf("duplicate document image %s", img.Name)
		}
		seenImages[img.Name] = true
		d.Images = append(d.Images, img)
	}
	return d, payload.Changes, nil
}

func (w *Workspace) readLegacyDocumentFileUnlocked(
	id, dir string, content []byte, withImages bool, metadataErr error,
) (document.Document, []document.Change, error) {
	a, err := readLegacyArtifactMetadata(dir, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return document.Document{}, nil, fmt.Errorf("read document metadata %s: %w", id, metadataErr)
		}
		return document.Document{}, nil, fmt.Errorf("read legacy document metadata %s: %w", id, err)
	}
	if a.Kind != artifact.DocumentKind {
		return document.Document{}, nil, errNotDocument
	}
	layout := document.DefaultLayout()
	layoutData, err := readRegularFile(filepath.Join(dir, "layout.json"), 1<<20)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return document.Document{}, nil, fmt.Errorf("read document layout %s: %w", id, err)
	}
	if err == nil {
		if err := json.Unmarshal(layoutData, &layout); err != nil {
			return document.Document{}, nil, fmt.Errorf("decode document layout %s: %w", id, err)
		}
	}
	if err := layout.Validate(); err != nil {
		return document.Document{}, nil, fmt.Errorf("validate document layout %s: %w", id, err)
	}
	var changes []document.Change
	changeData, err := readRegularFile(filepath.Join(dir, "changes.json"), artifactfile.MaxFileSize)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return document.Document{}, nil, fmt.Errorf("read document changes %s: %w", id, err)
	}
	if err == nil {
		if err := json.Unmarshal(changeData, &changes); err != nil {
			return document.Document{}, nil, fmt.Errorf("decode document changes %s: %w", id, err)
		}
		if err := validateDocumentChanges(id, changes); err != nil {
			return document.Document{}, nil, fmt.Errorf("validate document changes %s: %w", id, err)
		}
	}
	a.CreatedAt = a.CreatedAt.UTC()
	a.ModifiedAt = a.ModifiedAt.UTC()
	d := document.Document{Artifact: a, Body: string(content), Layout: layout}
	if withImages {
		if d.Images, err = readDocumentImages(dir); err != nil {
			return document.Document{}, nil, fmt.Errorf("read document images %s: %w", id, err)
		}
	}
	return d, changes, nil
}

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
		img := document.Image{Name: entry.Name(), Data: data}
		if err := img.Validate(); err != nil {
			return nil, err
		}
		images = append(images, img)
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
