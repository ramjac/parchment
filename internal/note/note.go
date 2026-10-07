package note

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
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
	now        func() time.Time
}

// NewService returns a note service backed by the given repository.
func NewService(repository Repository, undoLimit int) *Service {
	return &Service{repository: repository, history: history.New(undoLimit), now: func() time.Time { return time.Now().UTC() }}
}

// Get loads the note stored at path.
func (s *Service) Get(ctx context.Context, path string) (Note, error) {
	return s.repository.Get(ctx, path)
}

// Create writes a new note file at path. Creating a file is not recorded in
// undo history; Parchment never deletes the user's files.
func (s *Service) Create(ctx context.Context, path, title, body string) (Note, error) {
	if strings.TrimSpace(title) == "" {
		return Note{}, errors.New("note title is required")
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Note{}, fmt.Errorf("generate note ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	now := s.now().UTC()
	n := Note{Artifact: artifact.Artifact{
		ID: id, Kind: artifact.NoteKind, Title: strings.TrimSpace(title),
		CreatedAt: now, ModifiedAt: now, FormatVersion: artifact.FormatVersion,
		Path: path,
	}, Body: body}
	if err := s.repository.Transition(ctx, path, nil, &n); err != nil {
		return Note{}, err
	}
	return n, nil
}

// Update saves a note's title and Markdown body as one undoable change.
func (s *Service) Update(ctx context.Context, path, title, body string) (Note, error) {
	return s.UpdateFields(ctx, path, &title, &body)
}

// UpdateExpected applies an edit only if the note still matches its snapshot.
func (s *Service) UpdateExpected(ctx context.Context, expected Note, title, body string) (Note, error) {
	if strings.TrimSpace(title) == "" {
		return Note{}, errors.New("note title is required")
	}
	after := expected
	after.Title = strings.TrimSpace(title)
	after.Body = body
	if after.Title == expected.Title && after.Body == expected.Body {
		return expected, nil
	}
	after.ModifiedAt = s.now().UTC()
	if err := s.change(ctx, &expected, &after, "Edit note"); err != nil {
		return Note{}, err
	}
	return after, nil
}

// UpdateFields changes only the supplied title and body fields from one
// repository snapshot, so omitted fields cannot overwrite concurrent updates.
func (s *Service) UpdateFields(ctx context.Context, path string, title, body *string) (Note, error) {
	before, err := s.repository.Get(ctx, path)
	if err != nil {
		return Note{}, err
	}
	nextTitle, nextBody := before.Title, before.Body
	if title != nil {
		nextTitle = *title
	}
	if body != nil {
		nextBody = *body
	}
	return s.UpdateExpected(ctx, before, nextTitle, nextBody)
}

// Rename changes a note's title without changing its file or ID.
func (s *Service) Rename(ctx context.Context, path, title string) (Note, error) {
	before, err := s.repository.Get(ctx, path)
	if err != nil {
		return Note{}, err
	}
	if strings.TrimSpace(title) == "" {
		return Note{}, errors.New("note title is required")
	}
	after := before
	after.Title = strings.TrimSpace(title)
	after.ModifiedAt = s.now().UTC()
	if before.Title == after.Title {
		return before, nil
	}
	if err := s.change(ctx, &before, &after, "Rename note"); err != nil {
		return Note{}, err
	}
	return after, nil
}

// AddTag adds a unique tag to a note.
func (s *Service) AddTag(ctx context.Context, path, tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return errors.New("tag is required")
	}
	before, err := s.repository.Get(ctx, path)
	if err != nil {
		return err
	}
	for _, existing := range before.Tags {
		if existing == tag {
			return nil
		}
	}
	after := before
	after.Tags = append(append([]string(nil), before.Tags...), tag)
	after.ModifiedAt = s.now().UTC()
	return s.change(ctx, &before, &after, "Add tag")
}

// RemoveTag removes a tag from a note.
func (s *Service) RemoveTag(ctx context.Context, path, tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return errors.New("tag is required")
	}
	before, err := s.repository.Get(ctx, path)
	if err != nil {
		return err
	}
	after := before
	after.Tags = nil
	for _, existing := range before.Tags {
		if existing != tag {
			after.Tags = append(after.Tags, existing)
		}
	}
	if len(after.Tags) == len(before.Tags) {
		return nil
	}
	after.ModifiedAt = s.now().UTC()
	return s.change(ctx, &before, &after, "Remove tag")
}

// Undo reverses the most recent note change.
func (s *Service) Undo(ctx context.Context) (string, error) { return s.history.Undo(ctx) }

// Redo reapplies the most recently undone note change.
func (s *Service) Redo(ctx context.Context) (string, error) { return s.history.Redo(ctx) }

// CanUndo reports whether an undo operation is available.
func (s *Service) CanUndo() bool { return s.history.CanUndo() }

// CanRedo reports whether a redo operation is available.
func (s *Service) CanRedo() bool { return s.history.CanRedo() }

func (s *Service) change(ctx context.Context, before, after *Note, description string) error {
	return s.history.Execute(ctx, noteOperation{
		repository: s.repository, before: cloneNote(before), after: cloneNote(after), description: description,
	})
}

func cloneNote(n *Note) *Note {
	if n == nil {
		return nil
	}
	clone := *n
	clone.Tags = append([]string(nil), n.Tags...)
	clone.Links = append([]string(nil), n.Links...)
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

// Equal reports whether two notes have the same persisted value.
func Equal(left, right Note) bool {
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
	return reflect.DeepEqual(left, right)
}

// ErrNotFound indicates that the requested note does not exist.
var ErrNotFound = errors.New("note not found")
