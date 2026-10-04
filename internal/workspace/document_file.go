package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/document"
)

// DocumentFile stores one existing Parchment document directly, without a
// workspace. It cannot create or delete documents.
type DocumentFile struct {
	path    string
	mu      sync.Mutex
	item    document.Document
	changes []document.Change
	data    []byte
}

var _ document.Repository = (*DocumentFile)(nil)

// OpenDocumentFile opens an existing standalone Parchment document.
func OpenDocumentFile(path string) (*DocumentFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve document path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve document file: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect document file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("document file %s is not a regular file", resolved)
	}
	if info.Size() > artifactfile.MaxFileSize {
		return nil, fmt.Errorf("document file is larger than %d MiB", artifactfile.MaxFileSize>>20)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read document file: %w", err)
	}
	item, changes, err := decodeDocumentContent(data, true, standaloneID(artifact.DocumentKind, resolved))
	if errors.Is(err, errNotDocument) {
		return nil, errors.New("file is not a Parchment document")
	}
	if err != nil {
		return nil, fmt.Errorf("open document file: %w", err)
	}
	item.ID = standaloneID(artifact.DocumentKind, resolved)
	item.Title = standaloneTitle(resolved)
	item.Location = filepath.ToSlash(resolved)
	return &DocumentFile{path: resolved, item: item, changes: changes, data: data}, nil
}

// Path returns the resolved path of the standalone document.
func (f *DocumentFile) Path() string { return f.path }

func (f *DocumentFile) ArtifactLocation(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return ""
	}
	return f.item.Location
}

func (f *DocumentFile) ListDocuments(ctx context.Context) ([]document.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return []document.Document{cloneStandaloneDocument(f.item)}, nil
}

func (f *DocumentFile) GetDocument(ctx context.Context, id string) (document.Document, error) {
	if err := ctx.Err(); err != nil {
		return document.Document{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return document.Document{}, document.ErrNotFound
	}
	return cloneStandaloneDocument(f.item), nil
}

func (f *DocumentFile) TransitionDocument(ctx context.Context, id string, expected, target *document.Document) error {
	return f.transition(ctx, id, expected, target, nil)
}

func (f *DocumentFile) TransitionDocumentWithChanges(
	ctx context.Context, id string, expected, target *document.Document, changes []document.Change,
) error {
	return f.transition(ctx, id, expected, target, &changes)
}

func (f *DocumentFile) transition(
	ctx context.Context, id string, expected, target *document.Document, changes *[]document.Change,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return document.ErrNotFound
	}
	if expected == nil || target == nil {
		return errors.New("cannot create or delete a standalone document")
	}
	if !document.Equal(f.item, *expected) {
		return errors.New("stale document file")
	}
	next := f.changes
	if changes != nil {
		next = *changes
	}
	return f.saveLocked(ctx, *target, next)
}

// DeleteDocument always fails: a standalone document file is never removed.
func (f *DocumentFile) DeleteDocument(context.Context, string, document.Document) ([]document.Change, error) {
	return nil, errors.New("cannot delete a standalone document")
}

func (f *DocumentFile) ListDocumentChanges(ctx context.Context, id string) ([]document.Change, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return nil, document.ErrNotFound
	}
	return cloneChanges(f.changes), nil
}

func (f *DocumentFile) ProposeDocumentChange(ctx context.Context, id string, expected document.Document, change document.Change) error {
	if err := ctx.Err(); err != nil {
		return err
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return document.ErrNotFound
	}
	if !document.Equal(f.item, expected) {
		return fmt.Errorf("document %s changed since it was loaded", id)
	}
	if !document.ChangeSnapshotEqual(change.Before, document.ChangeSnapshotOf(f.item)) {
		return errors.New("proposal original snapshot does not match the document")
	}
	for _, existing := range f.changes {
		if existing.ID == change.ID {
			return errors.New("document change ID already exists")
		}
		if existing.Status == document.ChangePending {
			return errors.New("resolve the existing document change before proposing another")
		}
	}
	changes := append(cloneChanges(f.changes), cloneChange(change))
	return f.saveLocked(ctx, f.item, changes)
}

func (f *DocumentFile) TransitionDocumentChange(
	ctx context.Context, id, changeID string, expected, target document.Change,
	expectedDocument, targetDocument *document.Document,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected.DocumentID != id || target.DocumentID != id || expected.ID != changeID || target.ID != changeID {
		return errors.New("document change ID does not match transition")
	}
	if err := target.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.item.ID {
		return document.ErrNotFound
	}
	index := -1
	for i := range f.changes {
		if f.changes[i].ID == changeID {
			index = i
			break
		}
	}
	if index < 0 {
		return document.ErrChangeNotFound
	}
	if !reflect.DeepEqual(f.changes[index], expected) {
		return errors.New("document change is no longer in the expected state")
	}
	if expected.Status != document.ChangePending ||
		(target.Status != document.ChangeAccepted && target.Status != document.ChangeRejected) {
		return errors.New("invalid document change transition")
	}
	if (expectedDocument == nil) != (targetDocument == nil) {
		return errors.New("document transition must include both expected and target documents")
	}
	changes := cloneChanges(f.changes)
	changes[index] = cloneChange(target)
	if expectedDocument == nil {
		return f.saveLocked(ctx, f.item, changes)
	}
	if expectedDocument.ID != id || targetDocument.ID != id || !document.Equal(f.item, *expectedDocument) {
		return errors.New("document changed since the proposal was reviewed")
	}
	if !document.ChangeSnapshotEqual(target.Before, document.ChangeSnapshotOf(*expectedDocument)) ||
		!document.ChangeSnapshotEqual(target.After, document.ChangeSnapshotOf(*targetDocument)) {
		return errors.New("accepted document does not match the proposal")
	}
	return f.saveLocked(ctx, *targetDocument, changes)
}

// saveLocked writes target and changes only if the file still holds the bytes
// last read or written by this repository.
func (f *DocumentFile) saveLocked(ctx context.Context, target document.Document, changes []document.Change) error {
	if target.ID != f.item.ID || target.Kind != artifact.DocumentKind || target.Location != f.item.Location {
		return errors.New("invalid standalone document update")
	}
	data, err := encodeDocumentContent(target, changes)
	if err != nil {
		return err
	}
	info, err := os.Lstat(f.path)
	if err != nil {
		return fmt.Errorf("inspect document before save: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("document file is not a regular file")
	}
	current, err := readRegularFile(f.path, artifactfile.MaxFileSize)
	if err != nil {
		return fmt.Errorf("read document before save: %w", err)
	}
	if !bytes.Equal(current, f.data) {
		return errors.New("document file changed outside Parchment")
	}
	if err := writeAtomic(ctx, f.path, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("save document file: %w", err)
	}
	f.item = cloneStandaloneDocument(target)
	f.item.Title = standaloneTitle(f.path)
	f.changes = cloneChanges(changes)
	f.data = data
	return nil
}

func cloneStandaloneDocument(d document.Document) document.Document {
	images := make([]document.Image, len(d.Images))
	for i, img := range d.Images {
		images[i] = document.Image{Name: img.Name, Data: append([]byte(nil), img.Data...)}
	}
	d.Images = images
	if d.Blocks != nil {
		blocks := make(map[string]json.RawMessage, len(d.Blocks))
		for name, payload := range d.Blocks {
			blocks[name] = append(json.RawMessage(nil), payload...)
		}
		d.Blocks = blocks
	}
	return d
}

// encodeDocumentContent validates d and changes and renders the single-file
// document envelope. Unknown payload blocks in d.Blocks are preserved.
func encodeDocumentContent(d document.Document, changes []document.Change) ([]byte, error) {
	if d.Kind != artifact.DocumentKind {
		return nil, errors.New("invalid document artifact")
	}
	if err := d.Artifact.Validate(); err != nil {
		return nil, err
	}
	if err := d.Layout.Validate(); err != nil {
		return nil, err
	}
	payload := documentFileData{Layout: d.Layout, Changes: changes}
	seenImages := map[string]bool{}
	for _, img := range d.Images {
		if err := img.Validate(); err != nil {
			return nil, err
		}
		if seenImages[img.Name] {
			return nil, fmt.Errorf("duplicate document image %s", img.Name)
		}
		seenImages[img.Name] = true
		payload.Images = append(payload.Images, embeddedDocumentImage{Name: img.Name, Data: img.Data})
	}
	if err := validateDocumentChanges(d.ID, changes); err != nil {
		return nil, err
	}
	blocks := make(map[string]any, len(d.Blocks)+1)
	for name, block := range d.Blocks {
		blocks[name] = block
	}
	blocks[documentDataBlock] = payload
	return artifactfile.Encode(d.Artifact, d.Body, blocks)
}

// decodeDocumentContent parses a document envelope. It returns errNotDocument
// for other artifact kinds. Callers validate the ID and location they expect.
func decodeDocumentContent(content []byte, withImages bool, id string) (document.Document, []document.Change, error) {
	file, err := artifactfile.Decode(content)
	if err != nil {
		return document.Document{}, nil, fmt.Errorf("decode document artifact: %w", err)
	}
	a := file.Artifact
	if a.Kind != artifact.DocumentKind {
		return document.Document{}, nil, errNotDocument
	}
	a.ID = id
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
	for i := range payload.Changes {
		payload.Changes[i].DocumentID = id
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
