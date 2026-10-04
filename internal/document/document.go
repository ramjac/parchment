package document

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/history"
)

// ErrNotFound indicates that the requested document does not exist.
var ErrNotFound = errors.New("document not found")

// Document is a multi-page artifact. Its canonical Markdown file also stores
// page layout and embedded images in a hidden payload block.
type Document struct {
	artifact.Artifact
	Body   string
	Layout Layout
	Images []Image
}

// Draft is the editable part of a document.
type Draft struct {
	Title  string
	Body   string
	Layout Layout
	Images []Image
}

// ChangeSnapshot captures the editable document fields without duplicating
// embedded image data in the change log.
type ChangeSnapshot struct {
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Layout     Layout   `json:"layout"`
	ImageNames []string `json:"images,omitempty"`
}

type ChangeStatus string

const (
	ChangePending  ChangeStatus = "pending"
	ChangeAccepted ChangeStatus = "accepted"
	ChangeRejected ChangeStatus = "rejected"
)

// Change is a proposed document edit. The canonical document is unchanged
// until the proposal is accepted.
type Change struct {
	ID          string         `json:"id"`
	DocumentID  string         `json:"document_id"`
	Description string         `json:"description"`
	CreatedAt   time.Time      `json:"created_at"`
	ResolvedAt  *time.Time     `json:"resolved_at,omitempty"`
	Status      ChangeStatus   `json:"status"`
	Before      ChangeSnapshot `json:"before"`
	After       ChangeSnapshot `json:"after"`
}

// Repository is the persistence boundary required by document operations.
type Repository interface {
	ListDocuments(context.Context) ([]Document, error)
	GetDocument(context.Context, string) (Document, error)
	TransitionDocument(context.Context, string, *Document, *Document) error
	ListDocumentChanges(context.Context, string) ([]Change, error)
	ProposeDocumentChange(context.Context, string, Document, Change) error
	TransitionDocumentChange(context.Context, string, string, Change, Change, *Document, *Document) error
}

// Service applies document operations and records successful changes in
// history.
type Service struct {
	repository Repository
	history    *history.Stack
	now        func() time.Time
}

// NewService returns a document service backed by the given repository.
func NewService(repository Repository, undoLimit int) *Service {
	return &Service{repository: repository, history: history.New(undoLimit), now: func() time.Time { return time.Now().UTC() }}
}

// List returns documents ordered by most recently modified first. Embedded
// image data is not necessarily loaded; use Get for a complete document.
func (s *Service) List(ctx context.Context) ([]Document, error) {
	documents, err := s.repository.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(documents, func(i, j int) bool {
		return documents[i].ModifiedAt.After(documents[j].ModifiedAt)
	})
	return documents, nil
}

// Get loads one document, including embedded images, by its artifact ID.
func (s *Service) Get(ctx context.Context, id string) (Document, error) {
	return s.repository.GetDocument(ctx, id)
}

// Propose records a title, Markdown, or layout edit for later review without
// changing the live document. Proposals may retain or remove embedded images,
// but cannot add or replace their data.
func (s *Service) Propose(ctx context.Context, id, description string, draft Draft) (Change, error) {
	before, err := s.repository.GetDocument(ctx, id)
	if err != nil {
		return Change{}, err
	}
	for _, proposedImage := range draft.Images {
		found := false
		for _, existingImage := range before.Images {
			if proposedImage.Name == existingImage.Name && bytes.Equal(proposedImage.Data, existingImage.Data) {
				found = true
				break
			}
		}
		if !found {
			return Change{}, errors.New("proposals cannot add or replace embedded images")
		}
	}
	after := cloneDocument(&before)
	applyDraft(after, draft)
	after.Images = before.Images
	if err := normalize(after); err != nil {
		return Change{}, err
	}
	if sameSnapshot(snapshot(before), snapshot(*after)) {
		return Change{}, errors.New("proposed change has no document edits")
	}
	description = strings.TrimSpace(description)
	if description == "" {
		description = "Edit document"
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Change{}, fmt.Errorf("generate document change ID: %w", err)
	}
	change := Change{
		ID: hex.EncodeToString(idBytes), DocumentID: id, Description: description,
		CreatedAt: s.now().UTC(), Status: ChangePending,
		Before: snapshot(before), After: snapshot(*after),
	}
	if err := change.Validate(); err != nil {
		return Change{}, err
	}
	if err := s.repository.ProposeDocumentChange(ctx, id, before, change); err != nil {
		return Change{}, err
	}
	return change, nil
}

// Changes returns the recorded proposals for a document, newest first.
func (s *Service) Changes(ctx context.Context, id string) ([]Change, error) {
	changes, err := s.repository.ListDocumentChanges(ctx, id)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(changes, func(i, j int) bool {
		return changes[i].CreatedAt.After(changes[j].CreatedAt)
	})
	return changes, nil
}

// GetChange returns one recorded proposal by ID.
func (s *Service) GetChange(ctx context.Context, id, changeID string) (Change, error) {
	return s.getChange(ctx, id, changeID)
}

// Accept applies a pending proposal if its original document state is still live.
func (s *Service) Accept(ctx context.Context, id, changeID string) (Document, error) {
	change, err := s.getChange(ctx, id, changeID)
	if err != nil {
		return Document{}, err
	}
	if change.Status != ChangePending {
		return Document{}, errors.New("document change is not pending")
	}
	before, err := s.repository.GetDocument(ctx, id)
	if err != nil {
		return Document{}, err
	}
	if !sameSnapshot(snapshot(before), change.Before) {
		return Document{}, errors.New("document changed since this proposal was recorded")
	}
	after := cloneDocument(&before)
	applySnapshot(after, change.After)
	after.ModifiedAt = s.now().UTC()
	if err := normalize(after); err != nil {
		return Document{}, err
	}
	resolved := change
	resolved.Status = ChangeAccepted
	now := s.now().UTC()
	resolved.ResolvedAt = &now
	if err := s.history.Execute(ctx, &documentChangeAcceptance{
		repository: s.repository, changeID: changeID, beforeChange: change, afterChange: resolved,
		before: *cloneDocument(&before), after: *cloneDocument(after),
	}); err != nil {
		return Document{}, err
	}
	return *cloneDocument(after), nil
}

// Reject marks a pending proposal as rejected without changing the document.
func (s *Service) Reject(ctx context.Context, id, changeID string) error {
	change, err := s.getChange(ctx, id, changeID)
	if err != nil {
		return err
	}
	if change.Status != ChangePending {
		return errors.New("document change is not pending")
	}
	rejected := change
	rejected.Status = ChangeRejected
	now := s.now().UTC()
	rejected.ResolvedAt = &now
	return s.repository.TransitionDocumentChange(ctx, id, changeID, change, rejected, nil, nil)
}

func (s *Service) getChange(ctx context.Context, id, changeID string) (Change, error) {
	changes, err := s.repository.ListDocumentChanges(ctx, id)
	if err != nil {
		return Change{}, err
	}
	for _, change := range changes {
		if change.ID == changeID {
			return change, nil
		}
	}
	return Change{}, ErrChangeNotFound
}

// ErrChangeNotFound indicates that the requested document proposal does not exist.
var ErrChangeNotFound = errors.New("document change not found")

// Validate checks the persisted shape and consistency of a proposed change.
func (c Change) Validate() error {
	if !validArtifactID(c.ID) || !validArtifactID(c.DocumentID) {
		return errors.New("document change IDs must be 32 lowercase hexadecimal characters")
	}
	if strings.TrimSpace(c.Description) == "" || c.CreatedAt.IsZero() {
		return errors.New("document change description and creation time are required")
	}
	switch c.Status {
	case ChangePending:
		if c.ResolvedAt != nil {
			return errors.New("pending document change cannot have a resolution time")
		}
	case ChangeAccepted, ChangeRejected:
		if c.ResolvedAt == nil || c.ResolvedAt.IsZero() {
			return errors.New("resolved document change requires a resolution time")
		}
	default:
		return fmt.Errorf("unsupported document change status %q", c.Status)
	}
	if err := validateSnapshot(c.Before); err != nil {
		return fmt.Errorf("invalid original snapshot: %w", err)
	}
	if err := validateSnapshot(c.After); err != nil {
		return fmt.Errorf("invalid proposed snapshot: %w", err)
	}
	if sameSnapshot(c.Before, c.After) {
		return errors.New("document change has no edits")
	}
	return nil
}

func validArtifactID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}

func validateSnapshot(s ChangeSnapshot) error {
	if strings.TrimSpace(s.Title) == "" {
		return errors.New("title is required")
	}
	if err := s.Layout.Validate(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, name := range s.ImageNames {
		if !IsImageName(name) || seen[name] {
			return fmt.Errorf("invalid or duplicate image name %q", name)
		}
		seen[name] = true
	}
	return nil
}

func snapshot(d Document) ChangeSnapshot {
	result := ChangeSnapshot{Title: d.Title, Body: d.Body, Layout: d.Layout}
	for _, img := range d.Images {
		result.ImageNames = append(result.ImageNames, img.Name)
	}
	return result
}

// ChangeSnapshotOf returns the editable state captured by a proposal.
func ChangeSnapshotOf(d Document) ChangeSnapshot { return snapshot(d) }

// ChangeSnapshotEqual reports whether two captured document states are equal.
func ChangeSnapshotEqual(left, right ChangeSnapshot) bool { return sameSnapshot(left, right) }

func sameSnapshot(left, right ChangeSnapshot) bool {
	if len(left.ImageNames) == 0 {
		left.ImageNames = nil
	}
	if len(right.ImageNames) == 0 {
		right.ImageNames = nil
	}
	return reflect.DeepEqual(left, right)
}

func applySnapshot(d *Document, s ChangeSnapshot) {
	d.Title, d.Body, d.Layout = s.Title, s.Body, s.Layout
	wanted := make(map[string]bool, len(s.ImageNames))
	for _, name := range s.ImageNames {
		wanted[name] = true
	}
	images := d.Images[:0]
	for _, img := range d.Images {
		if wanted[img.Name] {
			images = append(images, img)
		}
	}
	d.Images = images
}

// Create adds a document. A zero Layout selects DefaultLayout.
func (s *Service) Create(ctx context.Context, draft Draft) (Document, error) {
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Document{}, fmt.Errorf("generate document ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	now := s.now().UTC()
	if draft.Layout == (Layout{}) {
		draft.Layout = DefaultLayout()
	}
	d := Document{Artifact: artifact.Artifact{
		ID: id, Kind: artifact.DocumentKind, CreatedAt: now, ModifiedAt: now,
		FormatVersion: artifact.FormatVersion, Location: ".parchment/artifacts/" + id + "/content.md",
	}}
	applyDraft(&d, draft)
	if err := normalize(&d); err != nil {
		return Document{}, err
	}
	if err := s.change(ctx, nil, &d, "Create document"); err != nil {
		return Document{}, err
	}
	return d, nil
}

// Save applies an edit only if the document still matches expected, so an
// editor cannot overwrite changes made since it loaded the document.
func (s *Service) Save(ctx context.Context, expected Document, draft Draft) (Document, error) {
	return s.apply(ctx, expected, "Edit document", func(d *Document) error {
		applyDraft(d, draft)
		return nil
	})
}

// Modify changes the latest stored version of a document as one undoable change.
func (s *Service) Modify(ctx context.Context, id, description string, edit func(*Document) error) (Document, error) {
	before, err := s.repository.GetDocument(ctx, id)
	if err != nil {
		return Document{}, err
	}
	return s.apply(ctx, before, description, edit)
}

// Rename changes a document's title without changing its ID.
func (s *Service) Rename(ctx context.Context, id, title string) (Document, error) {
	return s.Modify(ctx, id, "Rename document", func(d *Document) error {
		d.Title = title
		return nil
	})
}

// SetLayout replaces the page layout.
func (s *Service) SetLayout(ctx context.Context, id string, layout Layout) (Document, error) {
	return s.Modify(ctx, id, "Change page layout", func(d *Document) error {
		d.Layout = layout
		return nil
	})
}

// AddImage embeds an image and appends it to the end of the body. It returns
// the updated document and the embedded image's stable name.
func (s *Service) AddImage(ctx context.Context, id, alt string, data []byte) (Document, string, error) {
	img, err := NewImage(data)
	if err != nil {
		return Document{}, "", err
	}
	updated, err := s.Modify(ctx, id, "Insert image", func(d *Document) error {
		*d = WithImage(*d, img)
		d.Body = strings.TrimRight(d.Body, "\n") + "\n\n" + ImageMarkdown(alt, img.Name) + "\n"
		return nil
	})
	return updated, img.Name, err
}

// AddTag adds a unique tag to a document.
func (s *Service) AddTag(ctx context.Context, id, tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return errors.New("tag is required")
	}
	_, err := s.Modify(ctx, id, "Add tag", func(d *Document) error {
		for _, existing := range d.Tags {
			if existing == tag {
				return nil
			}
		}
		d.Tags = append(d.Tags, tag)
		return nil
	})
	return err
}

// RemoveTag removes a tag from a document.
func (s *Service) RemoveTag(ctx context.Context, id, tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return errors.New("tag is required")
	}
	_, err := s.Modify(ctx, id, "Remove tag", func(d *Document) error {
		kept := d.Tags[:0:0]
		for _, existing := range d.Tags {
			if existing != tag {
				kept = append(kept, existing)
			}
		}
		d.Tags = kept
		return nil
	})
	return err
}

// Delete removes a document and records enough information to restore it.
func (s *Service) Delete(ctx context.Context, id string) error {
	before, err := s.repository.GetDocument(ctx, id)
	if err != nil {
		return err
	}
	return s.change(ctx, &before, nil, "Delete document")
}

// Undo reverses the most recent document change.
func (s *Service) Undo(ctx context.Context) (string, error) { return s.history.Undo(ctx) }

// Redo reapplies the most recently undone document change.
func (s *Service) Redo(ctx context.Context) (string, error) { return s.history.Redo(ctx) }

// CanUndo reports whether an undo operation is available.
func (s *Service) CanUndo() bool { return s.history.CanUndo() }

// CanRedo reports whether a redo operation is available.
func (s *Service) CanRedo() bool { return s.history.CanRedo() }

// WithImage returns the document with img added, replacing any image of the
// same name.
func WithImage(d Document, img Image) Document {
	images := make([]Image, 0, len(d.Images)+1)
	for _, existing := range d.Images {
		if existing.Name != img.Name {
			images = append(images, existing)
		}
	}
	d.Images = append(images, img)
	return d
}

func applyDraft(d *Document, draft Draft) {
	d.Title = draft.Title
	d.Body = draft.Body
	d.Layout = draft.Layout
	d.Images = draft.Images
}

// apply edits a clone of expected and records the change if anything differs.
func (s *Service) apply(ctx context.Context, expected Document, description string, edit func(*Document) error) (Document, error) {
	after := cloneDocument(&expected)
	if err := edit(after); err != nil {
		return Document{}, err
	}
	if err := normalize(after); err != nil {
		return Document{}, err
	}
	if Equal(expected, *after) {
		return expected, nil
	}
	after.ModifiedAt = s.now().UTC()
	if err := s.change(ctx, &expected, after, description); err != nil {
		return Document{}, err
	}
	return *after, nil
}

// normalize validates a document and prunes images that the body no longer
// references, so the artifact directory holds only images in use.
func normalize(d *Document) error {
	d.Title = strings.TrimSpace(d.Title)
	if d.Title == "" {
		return errors.New("document title is required")
	}
	if err := d.Layout.Validate(); err != nil {
		return err
	}
	referenced := ReferencedImages(d.Body)
	seen := map[string]bool{}
	var images []Image
	for _, img := range d.Images {
		if !referenced[img.Name] || seen[img.Name] {
			continue
		}
		if err := img.Validate(); err != nil {
			return err
		}
		seen[img.Name] = true
		images = append(images, img)
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Name < images[j].Name })
	d.Images = images
	return d.Artifact.Validate()
}

func (s *Service) change(ctx context.Context, before, after *Document, description string) error {
	return s.history.Execute(ctx, documentOperation{
		repository: s.repository, before: cloneDocument(before), after: cloneDocument(after), description: description,
	})
}

func cloneDocument(d *Document) *Document {
	if d == nil {
		return nil
	}
	clone := *d
	clone.Tags = append([]string(nil), d.Tags...)
	clone.Links = append([]string(nil), d.Links...)
	clone.Images = make([]Image, len(d.Images))
	for i, img := range d.Images {
		clone.Images[i] = Image{Name: img.Name, Data: append([]byte(nil), img.Data...)}
	}
	return &clone
}

type documentOperation struct {
	repository  Repository
	before      *Document
	after       *Document
	description string
}

type documentChangeAcceptance struct {
	repository   Repository
	changeID     string
	beforeChange Change
	afterChange  Change
	before       Document
	after        Document
	resolved     bool
}

func (o *documentChangeAcceptance) Apply(ctx context.Context) error {
	if o.resolved {
		return o.repository.TransitionDocument(ctx, o.before.ID, &o.before, &o.after)
	}
	if err := o.repository.TransitionDocumentChange(ctx, o.before.ID, o.changeID,
		o.beforeChange, o.afterChange, &o.before, &o.after); err != nil {
		return err
	}
	o.resolved = true
	return nil
}

func (o *documentChangeAcceptance) Undo(ctx context.Context) error {
	return o.repository.TransitionDocument(ctx, o.before.ID, &o.after, &o.before)
}

func (o *documentChangeAcceptance) Description() string { return "Accept document change" }

func (o documentOperation) Apply(ctx context.Context) error {
	return o.transition(ctx, o.before, o.after)
}

func (o documentOperation) Undo(ctx context.Context) error {
	return o.transition(ctx, o.after, o.before)
}

func (o documentOperation) Description() string { return o.description }

func (o documentOperation) transition(ctx context.Context, expected, target *Document) error {
	switch {
	case o.before != nil:
		return o.repository.TransitionDocument(ctx, o.before.ID, expected, target)
	case o.after != nil:
		return o.repository.TransitionDocument(ctx, o.after.ID, expected, target)
	}
	return errors.New("document operation has no artifact")
}

// Equal reports whether two documents have the same persisted value.
func Equal(left, right Document) bool {
	if len(left.Tags) == 0 {
		left.Tags = nil
	}
	if len(right.Tags) == 0 {
		right.Tags = nil
	}
	if len(left.Links) == 0 {
		left.Links = nil
	}
	if len(right.Links) == 0 {
		right.Links = nil
	}
	if len(left.Images) != len(right.Images) {
		return false
	}
	for i := range left.Images {
		if left.Images[i].Name != right.Images[i].Name || !bytes.Equal(left.Images[i].Data, right.Images[i].Data) {
			return false
		}
	}
	left.Images, right.Images = nil, nil
	return reflect.DeepEqual(left, right)
}
