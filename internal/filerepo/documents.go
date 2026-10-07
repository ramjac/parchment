package filerepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/document"
)

const documentDataBlock = "parchment-document"

type embeddedDocumentImage struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

type documentFileData struct {
	Layout  document.Layout         `json:"layout"`
	Changes []document.Change       `json:"changes,omitempty"`
	Images  []embeddedDocumentImage `json:"images,omitempty"`
}

// GetDocument reads the document stored at path, including embedded images.
func (r *Repository) GetDocument(ctx context.Context, path string) (document.Document, error) {
	d, _, err := r.readDocument(ctx, path)
	return d, err
}

// ListDocumentChanges returns the proposals recorded in the document at path.
func (r *Repository) ListDocumentChanges(ctx context.Context, path string) ([]document.Change, error) {
	_, changes, err := r.readDocument(ctx, path)
	return changes, err
}

func (r *Repository) readDocument(ctx context.Context, path string) (document.Document, []document.Change, error) {
	if err := ctx.Err(); err != nil {
		return document.Document{}, nil, err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return document.Document{}, nil, err
	}
	content, err := readContent(path, artifact.DocumentKind, document.ErrNotFound)
	if err != nil {
		return document.Document{}, nil, err
	}
	return decodeDocument(path, content)
}

// TransitionDocument writes target to path only if the file still matches
// expected, preserving recorded proposals. A nil expected value creates a new
// file.
func (r *Repository) TransitionDocument(ctx context.Context, path string, expected, target *document.Document) error {
	if target == nil {
		return errors.New("Parchment does not delete artifact files")
	}
	return r.updateDocument(ctx, path, expected == nil, func(current document.Document, changes []document.Change) (document.Document, []document.Change, error) {
		if expected != nil && (!sameDocument(current, *expected) || target.ID != current.ID) {
			return document.Document{}, nil, changedError(current.Path)
		}
		return *target, changes, nil
	})
}

// ProposeDocumentChange records a pending proposal only if the document still
// matches the version from which the proposal was created.
func (r *Repository) ProposeDocumentChange(ctx context.Context, path string, expected document.Document, change document.Change) error {
	if err := change.Validate(); err != nil {
		return err
	}
	if change.Status != document.ChangePending {
		return errors.New("new document change must be pending")
	}
	return r.updateDocument(ctx, path, false, func(current document.Document, changes []document.Change) (document.Document, []document.Change, error) {
		if !sameDocument(current, expected) || change.DocumentID != current.ID {
			return document.Document{}, nil, changedError(current.Path)
		}
		if !document.ChangeSnapshotEqual(change.Before, document.ChangeSnapshotOf(current)) {
			return document.Document{}, nil, errors.New("proposal original snapshot does not match the document")
		}
		for _, existing := range changes {
			if existing.ID == change.ID {
				return document.Document{}, nil, errors.New("document change ID already exists")
			}
			if existing.Status == document.ChangePending {
				return document.Document{}, nil, errors.New("resolve the existing document change before proposing another")
			}
		}
		return current, append(changes, change), nil
	})
}

// TransitionDocumentChange resolves a proposal, optionally updating the
// document in the same file write.
func (r *Repository) TransitionDocumentChange(
	ctx context.Context, path, changeID string, expected, target document.Change,
	expectedDocument, targetDocument *document.Document,
) error {
	if expected.ID != changeID || target.ID != changeID || expected.DocumentID != target.DocumentID {
		return errors.New("document change ID does not match transition")
	}
	if err := target.Validate(); err != nil {
		return err
	}
	if expected.Status != document.ChangePending ||
		(target.Status != document.ChangeAccepted && target.Status != document.ChangeRejected) {
		return errors.New("invalid document change transition")
	}
	if (expectedDocument == nil) != (targetDocument == nil) {
		return errors.New("document transition must include both expected and target documents")
	}
	return r.updateDocument(ctx, path, false, func(current document.Document, changes []document.Change) (document.Document, []document.Change, error) {
		index := -1
		for i := range changes {
			if changes[i].ID == changeID {
				index = i
				break
			}
		}
		if index < 0 {
			return document.Document{}, nil, document.ErrChangeNotFound
		}
		if !reflect.DeepEqual(changes[index], expected) {
			return document.Document{}, nil, errors.New("document change is no longer in the expected state")
		}
		changes[index] = target
		if expectedDocument == nil {
			return current, changes, nil
		}
		if !sameDocument(current, *expectedDocument) || targetDocument.ID != current.ID {
			return document.Document{}, nil, errors.New("document changed since the proposal was reviewed")
		}
		if !document.ChangeSnapshotEqual(target.Before, document.ChangeSnapshotOf(*expectedDocument)) ||
			!document.ChangeSnapshotEqual(target.After, document.ChangeSnapshotOf(*targetDocument)) {
			return document.Document{}, nil, errors.New("accepted document does not match the proposal")
		}
		return *targetDocument, changes, nil
	})
}

type documentEdit func(current document.Document, changes []document.Change) (document.Document, []document.Change, error)

// updateDocument decodes the current file (unless creating), applies edit,
// and writes the resulting document and proposal list.
func (r *Repository) updateDocument(ctx context.Context, path string, create bool, edit documentEdit) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return r.update(ctx, path, create, document.ErrNotFound, func(content []byte) ([]byte, error) {
		current := document.Document{Artifact: artifact.Artifact{Path: path}}
		var changes []document.Change
		if !create {
			if err := checkKind(path, content, artifact.DocumentKind); err != nil {
				return nil, err
			}
			current, changes, err = decodeDocument(path, content)
			if err != nil {
				return nil, err
			}
		}
		target, changes, err := edit(current, changes)
		if err != nil {
			return nil, err
		}
		return encodeDocument(target, changes)
	})
}

func sameDocument(current, expected document.Document) bool {
	expected.Path = current.Path
	return document.Equal(current, expected)
}

func encodeDocument(d document.Document, changes []document.Change) ([]byte, error) {
	if err := validateTarget(d.Artifact, artifact.DocumentKind); err != nil {
		return nil, err
	}
	if err := d.Layout.Validate(); err != nil {
		return nil, err
	}
	if err := validateDocumentChanges(d.ID, changes); err != nil {
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
	blocks := encodeBlocks(d.Blocks)
	blocks[documentDataBlock] = payload
	data, err := artifactfile.Encode(d.Artifact, d.Body, blocks)
	if err != nil {
		return nil, fmt.Errorf("encode document: %w", err)
	}
	return data, nil
}

func decodeDocument(path string, content []byte) (document.Document, []document.Change, error) {
	file, err := decodeArtifact(path, content)
	if err != nil {
		return document.Document{}, nil, err
	}
	payloadJSON, ok := file.Blocks[documentDataBlock]
	if !ok {
		return document.Document{}, nil, fmt.Errorf("%s: document data block is missing", path)
	}
	var payload documentFileData
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return document.Document{}, nil, fmt.Errorf("decode document data %s: %w", path, err)
	}
	if payload.Layout == (document.Layout{}) {
		payload.Layout = document.DefaultLayout()
	}
	if err := payload.Layout.Validate(); err != nil {
		return document.Document{}, nil, fmt.Errorf("validate document layout %s: %w", path, err)
	}
	for i := range payload.Changes {
		payload.Changes[i].DocumentID = file.Artifact.ID
	}
	if err := validateDocumentChanges(file.Artifact.ID, payload.Changes); err != nil {
		return document.Document{}, nil, fmt.Errorf("validate document changes %s: %w", path, err)
	}
	d := document.Document{
		Artifact: file.Artifact, Body: file.Body, Layout: payload.Layout,
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
