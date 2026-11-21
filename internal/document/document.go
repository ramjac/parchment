package document

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/history"
	"example.com/parchment/internal/spreadsheet"
)

// ErrNotFound indicates that the requested document does not exist.
var ErrNotFound = errors.New("document not found")

// Document is a multi-page artifact. Its canonical Markdown file also stores
// page layout and embedded images in a hidden payload block.
type Document struct {
	artifact.Artifact
	Body           string
	Layout         Layout
	Images         []artifact.Image
	Workbook       spreadsheet.Workbook
	Tracking       bool
	TrackedChanges []TrackedChange
	Blocks         map[string]json.RawMessage `json:"-"`
}

// Draft is the editable part of a document.
type Draft struct {
	Body     string
	Layout   Layout
	Images   []artifact.Image
	Workbook spreadsheet.Workbook
}

// Repository is the persistence boundary required by document operations.
type Repository interface {
	// GetDocument reads the document stored at a file path.
	GetDocument(context.Context, string) (Document, error)
	// TransitionDocument replaces the document at a path only if it still
	// matches expected. A nil expected value creates a new file.
	TransitionDocument(context.Context, string, *Document, *Document) error
}

// Service applies document operations and records successful changes in
// history.
type Service struct {
	repository Repository
	history    *history.Stack
	store      history.Store
	activePath string
	now        func() time.Time
}

// NewService returns a document service backed by the given repository.
func NewService(repository Repository, undoLimit int) *Service {
	service := &Service{repository: repository, history: history.New(undoLimit), now: func() time.Time { return time.Now().UTC() }}
	service.store, _ = repository.(history.Store)
	return service
}

// Get loads the document stored at path, including embedded images.
func (s *Service) Get(ctx context.Context, path string) (Document, error) {
	s.activePath = path
	return s.repository.GetDocument(ctx, path)
}

// ErrChangeNotFound indicates that the requested tracked change does not exist.
var ErrChangeNotFound = errors.New("document change not found")

func validChangeID(id string) bool {
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

// Create writes a new document file at path. A zero Layout selects
// DefaultLayout. Creation is not undoable; Parchment never deletes files.
func (s *Service) Create(ctx context.Context, path string, draft Draft) (Document, error) {
	id, err := artifact.NewID(artifact.DocumentKind)
	if err != nil {
		return Document{}, err
	}
	now := s.now().UTC()
	if draft.Layout == (Layout{}) {
		draft.Layout = DefaultLayout()
	}
	d := Document{Artifact: artifact.Artifact{
		ID: id, Kind: artifact.DocumentKind, CreatedAt: now, ModifiedAt: now,
		FormatVersion: artifact.FormatVersion, Path: path,
	}}
	applyDraft(&d, draft)
	if err := normalize(&d); err != nil {
		return Document{}, err
	}
	if err := s.repository.TransitionDocument(ctx, path, nil, &d); err != nil {
		return Document{}, err
	}
	return s.repository.GetDocument(ctx, path)
}

// Save applies an edit only if the document still matches expected, so an
// editor cannot overwrite changes made since it loaded the document.
func (s *Service) Save(ctx context.Context, expected Document, draft Draft) (Document, error) {
	return s.apply(ctx, expected, "Edit document", func(d *Document) error {
		applyDraft(d, draft)
		return nil
	})
}

// SetTracking enables or disables tracking for future body edits.
func (s *Service) SetTracking(ctx context.Context, path string, enabled bool) (Document, error) {
	return s.Modify(ctx, path, "Change tracking", func(d *Document) error {
		d.Tracking = enabled
		return nil
	})
}

// SetTrackingExpected changes tracking only if the editor's snapshot is current.
func (s *Service) SetTrackingExpected(ctx context.Context, expected Document, enabled bool) (Document, error) {
	return s.apply(ctx, expected, "Change tracking", func(d *Document) error {
		d.Tracking = enabled
		return nil
	})
}

// ReviewTracked resolves one pending change, or all changes when id is empty.
func (s *Service) ReviewTracked(ctx context.Context, expected Document, id string, accept bool) (Document, error) {
	after := cloneDocument(&expected)
	if err := ResolveTrackedChange(after, id, accept); err != nil {
		return Document{}, err
	}
	if err := normalize(after); err != nil {
		return Document{}, err
	}
	if Equal(expected, *after) {
		return expected, nil
	}
	after.ModifiedAt = s.now().UTC()
	description := "Reject tracked change"
	if accept {
		description = "Accept tracked change"
	}
	if err := s.change(ctx, &expected, after, description); err != nil {
		return Document{}, err
	}
	return *after, nil
}

// Modify changes the latest stored version of a document as one undoable change.
func (s *Service) Modify(ctx context.Context, path, description string, edit func(*Document) error) (Document, error) {
	before, err := s.repository.GetDocument(ctx, path)
	if err != nil {
		return Document{}, err
	}
	return s.apply(ctx, before, description, edit)
}

// ModifyWithTracking applies an explicit tracking choice for this edit only.
func (s *Service) ModifyWithTracking(ctx context.Context, path, description string, track bool, edit func(*Document) error) (Document, error) {
	return s.ModifyWithTrackingChoice(ctx, path, description, &track, edit)
}

// ModifyWithTrackingChoice requires an explicit choice when tracking is enabled.
func (s *Service) ModifyWithTrackingChoice(ctx context.Context, path, description string, track *bool, edit func(*Document) error) (Document, error) {
	before, err := s.repository.GetDocument(ctx, path)
	if err != nil {
		return Document{}, err
	}
	if track == nil && before.Tracking {
		return Document{}, errors.New("choose --track or --track=false for this edit")
	}
	return s.applyWithTracking(ctx, before, description, edit, track != nil && *track)
}

// SetLayout replaces the page layout.
func (s *Service) SetLayout(ctx context.Context, path string, layout Layout) (Document, error) {
	return s.Modify(ctx, path, "Change page layout", func(d *Document) error {
		d.Layout = layout
		return nil
	})
}

// AddImage embeds an image and appends it to the end of the body. It returns
// the updated document and the embedded image's stable name.
func (s *Service) AddImage(ctx context.Context, path, alt string, data []byte) (Document, string, error) {
	return s.addImage(ctx, path, alt, data, nil, false)
}

func (s *Service) AddImageWithTracking(ctx context.Context, path, alt string, data []byte, track bool) (Document, string, error) {
	return s.addImage(ctx, path, alt, data, &track, true)
}

func (s *Service) AddImageWithTrackingChoice(ctx context.Context, path, alt string, data []byte, track *bool) (Document, string, error) {
	return s.addImage(ctx, path, alt, data, track, true)
}

func (s *Service) addImage(ctx context.Context, path, alt string, data []byte, tracking *bool, explicitChoice bool) (Document, string, error) {
	img, err := artifact.NewImage(data)
	if err != nil {
		return Document{}, "", err
	}
	edit := func(d *Document) error {
		*d = WithImage(*d, img)
		d.Body = strings.TrimRight(d.Body, "\n") + "\n\n" + artifact.ImageMarkdown(alt, img.Name) + "\n"
		return nil
	}
	var updated Document
	if !explicitChoice {
		updated, err = s.Modify(ctx, path, "Insert image", edit)
	} else {
		updated, err = s.ModifyWithTrackingChoice(ctx, path, "Insert image", tracking, edit)
	}
	return updated, img.Name, err
}

// RemoveImage removes an image from a document. If it is referenced in the body,
// it is only removed if stripReferences is true. Pending changes retain the
// image data until they are resolved.
func (s *Service) RemoveImage(ctx context.Context, path, imageName string, stripReferences bool) (Document, error) {
	return s.removeImage(ctx, path, imageName, stripReferences, nil, false)
}

func (s *Service) RemoveImageWithTracking(ctx context.Context, path, imageName string, stripReferences, track bool) (Document, error) {
	return s.removeImage(ctx, path, imageName, stripReferences, &track, true)
}

func (s *Service) RemoveImageWithTrackingChoice(ctx context.Context, path, imageName string, stripReferences bool, track *bool) (Document, error) {
	return s.removeImage(ctx, path, imageName, stripReferences, track, true)
}

func (s *Service) removeImage(ctx context.Context, path, imageName string, stripReferences bool, tracking *bool, explicitChoice bool) (Document, error) {
	edit := func(d *Document) error {
		hasImage := false
		for _, img := range d.Images {
			if img.Name == imageName {
				hasImage = true
				break
			}
		}
		if !hasImage {
			return fmt.Errorf("image %q not found in document", imageName)
		}

		if artifact.ReferencedImages(d.Body)[imageName] {
			if !stripReferences {
				return fmt.Errorf("cannot remove image %q because it is referenced in the document body", imageName)
			}
			re := regexp.MustCompile(`!\[[^\]]*\]\(` + regexp.QuoteMeta(imageName) + `\)`)
			d.Body = re.ReplaceAllString(d.Body, "")
		}
		images := make([]artifact.Image, 0, len(d.Images)-1)
		for _, img := range d.Images {
			if img.Name != imageName {
				images = append(images, img)
			}
		}
		d.Images = images
		return nil
	}
	if !explicitChoice {
		return s.Modify(ctx, path, "Remove image", edit)
	}
	return s.ModifyWithTrackingChoice(ctx, path, "Remove image", tracking, edit)
}

// Undo reverses the most recent document change.
func (s *Service) Undo(ctx context.Context) (string, error) {
	if s.store != nil {
		return s.store.Undo(ctx, s.activePath)
	}
	return s.history.Undo(ctx)
}

// Redo reapplies the most recently undone document change.
func (s *Service) Redo(ctx context.Context) (string, error) {
	if s.store != nil {
		return s.store.Redo(ctx, s.activePath)
	}
	return s.history.Redo(ctx)
}

// CanUndo reports whether an undo operation is available.
func (s *Service) CanUndo() bool {
	if s.store != nil && s.activePath != "" {
		canUndo, err := s.store.CanUndo(context.Background(), s.activePath)
		return err == nil && canUndo
	}
	return s.history.CanUndo()
}

// HistoryAvailability reports whether saved changes to the active document
// can be undone or redone, and can be canceled while waiting for storage.
func (s *Service) HistoryAvailability(ctx context.Context) (bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if s.store == nil || s.activePath == "" {
		return s.history.CanUndo(), s.history.CanRedo(), nil
	}
	canUndo, err := s.store.CanUndo(ctx, s.activePath)
	if err != nil {
		return false, false, err
	}
	canRedo, err := s.store.CanRedo(ctx, s.activePath)
	if err != nil {
		return false, false, err
	}
	return canUndo, canRedo, nil
}

// CanRedo reports whether a redo operation is available.
func (s *Service) CanRedo() bool {
	if s.store != nil && s.activePath != "" {
		canRedo, err := s.store.CanRedo(context.Background(), s.activePath)
		return err == nil && canRedo
	}
	return s.history.CanRedo()
}

// WithImage returns the document with img added, replacing any image of the
// same name.
func WithImage(d Document, img artifact.Image) Document {
	for i, existing := range d.Images {
		if existing.Name == img.Name {
			d.Images[i] = img
			return d
		}
	}
	d.Images = append(d.Images, img)
	return d
}

func applyDraft(d *Document, draft Draft) {
	d.Body = draft.Body
	d.Layout = draft.Layout
	d.Images = draft.Images
	d.Workbook = draft.Workbook.Clone()
}

// apply edits a clone of expected and records the change if anything differs.
func (s *Service) apply(ctx context.Context, expected Document, description string, edit func(*Document) error) (Document, error) {
	return s.applyWithTracking(ctx, expected, description, edit, expected.Tracking)
}

func (s *Service) applyWithTracking(ctx context.Context, expected Document, description string, edit func(*Document) error, track bool) (Document, error) {
	after := cloneDocument(&expected)
	if err := edit(after); err != nil {
		return Document{}, err
	}
	workbookChanged := !expected.Workbook.Equal(after.Workbook)
	if workbookChanged {
		for _, change := range expected.TrackedChanges {
			if change.Kind == trackedWorkbookKind {
				return Document{}, errors.New("review pending spreadsheet changes before editing the embedded workbook again")
			}
		}
	}
	body := after.Body
	after.Body = expected.Body
	if err := EditTrackedBody(after, body, track, s.now()); err != nil {
		return Document{}, err
	}
	if workbookChanged && track {
		id, err := newTrackedID()
		if err != nil {
			return Document{}, fmt.Errorf("generate tracked spreadsheet change ID: %w", err)
		}
		existingByID := make(map[string]TrackedChange, len(expected.TrackedChanges))
		for _, change := range expected.TrackedChanges {
			existingByID[change.ID] = change
		}
		members := make(map[string]bool)
		groups := make(map[string]bool)
		for i := range after.TrackedChanges {
			change := after.TrackedChanges[i]
			if change.Kind == trackedWorkbookKind {
				continue
			}
			existing, ok := existingByID[change.ID]
			if !ok || existing.Deleted != change.Deleted || existing.Inserted != change.Inserted {
				members[change.ID] = true
				if existing.Group != "" {
					groups[existing.Group] = true
				}
				if change.Group != "" {
					groups[change.Group] = true
				}
			}
		}
		for i := range after.TrackedChanges {
			change := after.TrackedChanges[i]
			if change.Kind == trackedWorkbookKind {
				continue
			}
			if groups[change.Group] {
				members[change.ID] = true
			}
		}
		for i := range after.TrackedChanges {
			if members[after.TrackedChanges[i].ID] {
				after.TrackedChanges[i].Group = id
			}
		}
		after.TrackedChanges = append(after.TrackedChanges, TrackedChange{
			ID: id, Kind: trackedWorkbookKind, Group: id,
			Workbook: &TrackedWorkbookChange{
				Before: expected.Workbook.Clone(), After: after.Workbook.Clone(),
			},
			CreatedAt: s.now().UTC(),
		})
	}
	if len(after.TrackedChanges) > 0 {
		present := make(map[string]bool, len(after.Images))
		for _, image := range after.Images {
			present[image.Name] = true
		}
		for _, image := range expected.Images {
			if !present[image.Name] {
				after.Images = append(after.Images, image)
			}
		}
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
// references, so the file holds only images in use.
func normalize(d *Document) error {
	if !utf8.ValidString(d.Body) {
		return errors.New("document body must be valid UTF-8")
	}
	if err := d.Layout.Validate(); err != nil {
		return err
	}
	if err := ValidateTracking(*d); err != nil {
		return err
	}
	if err := spreadsheet.NormalizeEmbeddedWorkbook(&d.Workbook); err != nil {
		return fmt.Errorf("validate embedded workbook: %w", err)
	}
	referenced := artifact.ReferencedImages(d.Body)
	seen := map[string]bool{}
	var images []artifact.Image
	for _, img := range d.Images {
		if (!referenced[img.Name] && len(d.TrackedChanges) == 0) || seen[img.Name] {
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
	s.activePath = before.Path
	if s.store != nil {
		return s.repository.TransitionDocument(history.WithDescription(ctx, description), before.Path, before, after)
	}
	return s.history.Execute(ctx, &documentOperation{
		repository: s.repository, before: cloneDocument(before), after: cloneDocument(after), description: description,
	})
}

func cloneDocument(d *Document) *Document {
	if d == nil {
		return nil
	}
	clone := *d
	clone.TrackedChanges = append([]TrackedChange(nil), d.TrackedChanges...)
	for i := range clone.TrackedChanges {
		if d.TrackedChanges[i].Workbook != nil {
			clone.TrackedChanges[i].Workbook = &TrackedWorkbookChange{
				Before: d.TrackedChanges[i].Workbook.Before.Clone(),
				After:  d.TrackedChanges[i].Workbook.After.Clone(),
			}
		}
	}
	clone.Images = make([]artifact.Image, len(d.Images))
	for i, img := range d.Images {
		clone.Images[i] = artifact.Image{Name: img.Name, Data: append([]byte(nil), img.Data...)}
	}
	clone.Workbook = d.Workbook.Clone()
	if d.Blocks != nil {
		clone.Blocks = make(map[string]json.RawMessage, len(d.Blocks))
		for name, payload := range d.Blocks {
			clone.Blocks[name] = append(json.RawMessage(nil), payload...)
		}
	}
	return &clone
}

type documentOperation struct {
	repository  Repository
	before      *Document
	after       *Document
	description string
}

func (o *documentOperation) Apply(ctx context.Context) error {
	return o.transition(ctx, o.before, o.after)
}

func (o *documentOperation) Undo(ctx context.Context) error {
	return o.transition(ctx, o.after, o.before)
}

func (o *documentOperation) Description() string { return o.description }

func (o documentOperation) transition(ctx context.Context, expected, target *Document) error {
	if o.before == nil || o.after == nil {
		return errors.New("document operation must have a before and after value")
	}
	return o.repository.TransitionDocument(ctx, o.before.Path, expected, target)
}

// Equal reports whether two documents have the same persisted value.
func Equal(left, right Document) bool {
	left.Title, right.Title = "", ""
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
