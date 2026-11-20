package note

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/history"
)

// Note is a Markdown artifact with the shared artifact metadata envelope.
type Note struct {
	artifact.Artifact
	Body   string                     `json:"-"`
	Blocks map[string]json.RawMessage `json:"-"`
}

// Repository is the persistence boundary required by note operations.
type Repository interface {
	// Get reads the note stored at a file path.
	Get(context.Context, string) (Note, error)
	// Transition replaces the note at a path only if it still matches
	// expected. A nil expected value creates a new file.
	Transition(context.Context, string, *Note, *Note) error
}

// Service applies note operations and records successful changes in history.
type Service struct {
	repository Repository
	history    *history.Stack
	store      history.Store
	activePath string
	now        func() time.Time
}

// NewService returns a note service backed by the given repository.
func NewService(repository Repository, undoLimit int) *Service {
	service := &Service{repository: repository, history: history.New(undoLimit), now: func() time.Time { return time.Now().UTC() }}
	service.store, _ = repository.(history.Store)
	return service
}

// Get loads the note stored at path.
func (s *Service) Get(ctx context.Context, path string) (Note, error) {
	s.activePath = path
	return s.repository.Get(ctx, path)
}

// Create writes a new note file at path. Creating a file is not recorded in
// undo history; Parchment never deletes the user's files. The note's title is
// derived from its file name.
func (s *Service) Create(ctx context.Context, path, body string) (Note, error) {
	id, err := artifact.NewID(artifact.NoteKind)
	if err != nil {
		return Note{}, err
	}
	now := s.now().UTC()
	n := Note{Artifact: artifact.Artifact{
		ID: id, Kind: artifact.NoteKind, CreatedAt: now, ModifiedAt: now,
		FormatVersion: artifact.FormatVersion, Path: path,
	}, Body: body}
	if err := s.repository.Transition(ctx, path, nil, &n); err != nil {
		return Note{}, err
	}
	return s.repository.Get(ctx, path)
}

// Update replaces a note's Markdown body as one undoable change.
func (s *Service) Update(ctx context.Context, path, body string) (Note, error) {
	before, err := s.repository.Get(ctx, path)
	if err != nil {
		return Note{}, err
	}
	return s.UpdateExpected(ctx, before, body)
}

// UpdateExpected applies an edit only if the note still matches its snapshot.
func (s *Service) UpdateExpected(ctx context.Context, expected Note, body string) (Note, error) {
	after := expected
	after.Body = body
	if after.Body == expected.Body {
		return expected, nil
	}
	after.ModifiedAt = s.now().UTC()
	if err := s.change(ctx, &expected, &after, "Edit note"); err != nil {
		return Note{}, err
	}
	return after, nil
}

// Undo reverses the most recent note change.
func (s *Service) Undo(ctx context.Context) (string, error) {
	if s.store != nil {
		return s.store.Undo(ctx, s.activePath)
	}
	return s.history.Undo(ctx)
}

// Redo reapplies the most recently undone note change.
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

// HistoryAvailability reports whether saved changes to the active note can be
// undone or redone, and can be canceled while waiting for persistent storage.
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

func (s *Service) change(ctx context.Context, before, after *Note, description string) error {
	s.activePath = before.Path
	if s.store != nil {
		return s.repository.Transition(history.WithDescription(ctx, description), before.Path, before, after)
	}
	return s.history.Execute(ctx, noteOperation{
		repository: s.repository, before: cloneNote(before), after: cloneNote(after), description: description,
	})
}

func cloneNote(n *Note) *Note {
	if n == nil {
		return nil
	}
	clone := *n
	clone.Blocks = cloneBlocks(n.Blocks)
	return &clone
}

func cloneBlocks(blocks map[string]json.RawMessage) map[string]json.RawMessage {
	if len(blocks) == 0 {
		return nil
	}
	clone := make(map[string]json.RawMessage, len(blocks))
	for name, payload := range blocks {
		clone[name] = append(json.RawMessage(nil), payload...)
	}
	return clone
}

type noteOperation struct {
	repository  Repository
	before      *Note
	after       *Note
	description string
}

func (o noteOperation) Apply(ctx context.Context) error {
	return o.transition(ctx, o.before, o.after)
}

func (o noteOperation) Undo(ctx context.Context) error {
	return o.transition(ctx, o.after, o.before)
}

func (o noteOperation) Description() string { return o.description }

func (o noteOperation) transition(ctx context.Context, expected, target *Note) error {
	if o.before == nil || o.after == nil {
		return errors.New("note operation must have a before and after value")
	}
	return o.repository.Transition(ctx, o.before.Path, expected, target)
}

// Equal reports whether two notes have the same persisted value. Titles and
// IDs are derived from the file path and are not compared.
func Equal(left, right Note) bool {
	left.Title, right.Title = "", ""
	left.ID, right.ID = "", ""
	return reflect.DeepEqual(left, right)
}

// ErrNotFound indicates that the requested note does not exist.
var ErrNotFound = errors.New("note not found")
