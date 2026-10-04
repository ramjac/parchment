package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/spreadsheet"
)

// memoryRepository is an in-memory note, document, and spreadsheet store for
// TUI tests that need several artifacts without touching the filesystem.
type memoryRepository struct {
	mu           sync.Mutex
	notes        map[string]note.Note
	documents    map[string]document.Document
	changes      map[string][]document.Change
	spreadsheets map[string]spreadsheet.Spreadsheet
}

func openTestWorkspace(t *testing.T) *memoryRepository {
	t.Helper()
	return &memoryRepository{
		notes:        map[string]note.Note{},
		documents:    map[string]document.Document{},
		changes:      map[string][]document.Change{},
		spreadsheets: map[string]spreadsheet.Spreadsheet{},
	}
}

func cloneValue[T any](value T) T {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return out
}

func cloneBlocks(blocks map[string]json.RawMessage) map[string]json.RawMessage {
	if blocks == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(blocks))
	for name, payload := range blocks {
		out[name] = append(json.RawMessage(nil), payload...)
	}
	return out
}

// Body and Blocks are excluded from some JSON encodings, so they are copied
// explicitly.
func cloneNote(n note.Note) note.Note {
	out := cloneValue(n)
	out.Body, out.Blocks = n.Body, cloneBlocks(n.Blocks)
	return out
}

// DocumentID is runtime-only and excluded from JSON, so it is restored.
func cloneChanges(changes []document.Change) []document.Change {
	out := cloneValue(changes)
	for i := range out {
		out[i].DocumentID = changes[i].DocumentID
	}
	return out
}

func cloneDocument(d document.Document) document.Document {
	out := cloneValue(d)
	out.Blocks = cloneBlocks(d.Blocks)
	return out
}

func cloneSpreadsheet(book spreadsheet.Spreadsheet) spreadsheet.Spreadsheet {
	out := cloneValue(book)
	out.Body, out.Blocks = book.Body, cloneBlocks(book.Blocks)
	return out
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (r *memoryRepository) ArtifactLocation(id string) string { return id + ".md" }

func (r *memoryRepository) List(ctx context.Context) ([]note.Note, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []note.Note
	for _, id := range sortedKeys(r.notes) {
		out = append(out, cloneNote(r.notes[id]))
	}
	return out, ctx.Err()
}

func (r *memoryRepository) Get(ctx context.Context, id string) (note.Note, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.notes[id]
	if !ok {
		return note.Note{}, note.ErrNotFound
	}
	return cloneNote(n), ctx.Err()
}

func (r *memoryRepository) Save(ctx context.Context, n note.Note) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes[n.ID] = cloneNote(n)
	return ctx.Err()
}

func (r *memoryRepository) Delete(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.notes[id]; !ok {
		return note.ErrNotFound
	}
	delete(r.notes, id)
	return ctx.Err()
}

func (r *memoryRepository) Transition(ctx context.Context, id string, expected, target *note.Note) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, exists := r.notes[id]
	switch {
	case expected == nil && exists:
		return fmt.Errorf("note %s already exists", id)
	case expected != nil && !exists:
		return note.ErrNotFound
	case expected != nil && !note.Equal(current, *expected):
		return fmt.Errorf("note %s changed since this operation was recorded", id)
	}
	if target == nil {
		delete(r.notes, id)
		return nil
	}
	r.notes[id] = cloneNote(*target)
	return nil
}

func (r *memoryRepository) ListDocuments(ctx context.Context) ([]document.Document, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []document.Document
	for _, id := range sortedKeys(r.documents) {
		d := cloneDocument(r.documents[id])
		d.Images = nil
		out = append(out, d)
	}
	return out, ctx.Err()
}

func (r *memoryRepository) GetDocument(ctx context.Context, id string) (document.Document, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.documents[id]
	if !ok {
		return document.Document{}, document.ErrNotFound
	}
	return cloneDocument(d), ctx.Err()
}

func (r *memoryRepository) TransitionDocument(ctx context.Context, id string, expected, target *document.Document) error {
	return r.transitionDocument(ctx, id, expected, target, nil)
}

func (r *memoryRepository) TransitionDocumentWithChanges(
	ctx context.Context, id string, expected, target *document.Document, changes []document.Change,
) error {
	return r.transitionDocument(ctx, id, expected, target, &changes)
}

func (r *memoryRepository) transitionDocument(
	ctx context.Context, id string, expected, target *document.Document, changes *[]document.Change,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, exists := r.documents[id]
	switch {
	case expected == nil && exists:
		return fmt.Errorf("document %s already exists", id)
	case expected == nil && target == nil, expected != nil && !exists:
		return document.ErrNotFound
	case expected != nil && !document.Equal(current, *expected):
		return fmt.Errorf("document %s changed since this operation was recorded", id)
	}
	if target == nil {
		delete(r.documents, id)
		delete(r.changes, id)
		return nil
	}
	r.documents[id] = cloneDocument(*target)
	if changes != nil {
		r.changes[id] = cloneChanges(*changes)
	}
	return nil
}

func (r *memoryRepository) DeleteDocument(ctx context.Context, id string, expected document.Document) ([]document.Change, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.documents[id]
	if !ok {
		return nil, document.ErrNotFound
	}
	if !document.Equal(current, expected) {
		return nil, fmt.Errorf("document %s changed since this operation was recorded", id)
	}
	changes := cloneChanges(r.changes[id])
	delete(r.documents, id)
	delete(r.changes, id)
	return changes, ctx.Err()
}

func (r *memoryRepository) ListDocumentChanges(ctx context.Context, id string) ([]document.Change, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.documents[id]; !ok {
		return nil, document.ErrNotFound
	}
	return cloneChanges(r.changes[id]), ctx.Err()
}

func (r *memoryRepository) ProposeDocumentChange(ctx context.Context, id string, expected document.Document, change document.Change) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.documents[id]
	if !ok {
		return document.ErrNotFound
	}
	if err := change.Validate(); err != nil {
		return err
	}
	if change.Status != document.ChangePending {
		return errors.New("new document change must be pending")
	}
	if !document.Equal(current, expected) {
		return fmt.Errorf("document %s changed since it was loaded", id)
	}
	for _, existing := range r.changes[id] {
		if existing.ID == change.ID {
			return errors.New("document change ID already exists")
		}
		if existing.Status == document.ChangePending {
			return errors.New("resolve the existing document change before proposing another")
		}
	}
	r.changes[id] = append(r.changes[id], cloneChanges([]document.Change{change})[0])
	return ctx.Err()
}

func (r *memoryRepository) TransitionDocumentChange(
	ctx context.Context, id, changeID string, expected, target document.Change,
	expectedDocument, targetDocument *document.Document,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.documents[id]
	if !ok {
		return document.ErrNotFound
	}
	changes := r.changes[id]
	index := -1
	for i := range changes {
		if changes[i].ID == changeID {
			index = i
		}
	}
	if index < 0 {
		return document.ErrChangeNotFound
	}
	if err := target.Validate(); err != nil {
		return err
	}
	if !reflect.DeepEqual(changes[index], expected) {
		return errors.New("document change is no longer in the expected state")
	}
	if (expectedDocument == nil) != (targetDocument == nil) {
		return errors.New("document transition must include both expected and target documents")
	}
	if expectedDocument != nil {
		if !document.Equal(current, *expectedDocument) {
			return errors.New("document changed since the proposal was reviewed")
		}
		r.documents[id] = cloneDocument(*targetDocument)
	}
	r.changes[id][index] = cloneChanges([]document.Change{target})[0]
	return ctx.Err()
}

func (r *memoryRepository) ListSpreadsheets(ctx context.Context) ([]spreadsheet.Spreadsheet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []spreadsheet.Spreadsheet
	for _, id := range sortedKeys(r.spreadsheets) {
		out = append(out, cloneSpreadsheet(r.spreadsheets[id]))
	}
	return out, ctx.Err()
}

func (r *memoryRepository) GetSpreadsheet(ctx context.Context, id string) (spreadsheet.Spreadsheet, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	book, ok := r.spreadsheets[id]
	if !ok {
		return spreadsheet.Spreadsheet{}, spreadsheet.ErrNotFound
	}
	return cloneSpreadsheet(book), ctx.Err()
}

func (r *memoryRepository) TransitionSpreadsheet(ctx context.Context, id string, expected, target *spreadsheet.Spreadsheet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, exists := r.spreadsheets[id]
	switch {
	case expected == nil && exists:
		return fmt.Errorf("spreadsheet %s already exists", id)
	case expected != nil && !exists:
		return spreadsheet.ErrNotFound
	case expected != nil && !spreadsheet.Equal(current, *expected):
		return fmt.Errorf("spreadsheet %s changed since this operation was recorded", id)
	}
	if target == nil {
		delete(r.spreadsheets, id)
		return nil
	}
	r.spreadsheets[id] = cloneSpreadsheet(*target)
	return nil
}
