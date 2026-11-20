package history

import (
	"context"
	"errors"
	"time"
)

// Snapshot references the immutable chunks that reconstruct one exact file.
type Snapshot struct {
	FileDigest       string            `json:"file_digest"`
	ByteSize         int64             `json:"byte_size"`
	RawBlobHash      string            `json:"raw_blob_hash,omitempty"`
	MetadataBlobHash string            `json:"metadata_blob_hash,omitempty"`
	BodyBlobHash     string            `json:"body_blob_hash,omitempty"`
	BlockBlobHashes  map[string]string `json:"block_blob_hashes,omitempty"`
	ImageBlobHashes  map[string]string `json:"image_blob_hashes,omitempty"`
}

// Entry records one saved file state.
type Entry struct {
	ID          string    `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	Description string    `json:"description"`
	Snapshot    Snapshot  `json:"snapshot"`
}

// Manifest is the bounded undo/redo timeline for one canonical file path.
type Manifest struct {
	SchemaVersion int     `json:"schema_version"`
	ArtifactPath  string  `json:"artifact_path"`
	UndoLimit     int     `json:"undo_limit"`
	NextEntryID   uint64  `json:"next_entry_id"`
	HeadIndex     int     `json:"head_index"`
	Entries       []Entry `json:"entries"`
}

// Journal describes an interrupted persistent-history transition.
type Journal struct {
	TransitionID string `json:"transition_id"`
	FromDigest   string `json:"from_digest"`
	ToDigest     string `json:"to_digest"`
	TargetHead   int    `json:"target_head"`
}

// Store persists saved-file history. Paths identify artifacts; IDs are internal
// to an individual history manifest.
type Store interface {
	Undo(context.Context, string) (string, error)
	Redo(context.Context, string) (string, error)
	CanUndo(context.Context, string) (bool, error)
	CanRedo(context.Context, string) (bool, error)
	Log(context.Context, string) ([]Entry, int, error)
	Restore(context.Context, string, string) (string, error)
	Clean(context.Context, string) error
	CleanAll(context.Context) error
	GC(context.Context) error
}

type descriptionContextKey struct{}

// WithDescription associates a user-facing operation description with a
// repository write so the persistent history store can record it atomically.
func WithDescription(ctx context.Context, description string) context.Context {
	return context.WithValue(ctx, descriptionContextKey{}, description)
}

// DescriptionFromContext returns the repository-write history description.
func DescriptionFromContext(ctx context.Context) string {
	description, _ := ctx.Value(descriptionContextKey{}).(string)
	return description
}

// Operation describes an applied change that can be undone and redone.
type Operation interface {
	Apply(context.Context) error
	Undo(context.Context) error
	Description() string
}

// Stack keeps a bounded in-memory undo and redo history.
type Stack struct {
	limit int
	undo  []Operation
	redo  []Operation
}

// New returns a history stack with the requested maximum number of operations.
func New(limit int) *Stack {
	if limit < 1 {
		limit = 100
	}
	return &Stack{limit: limit}
}

// Execute applies an operation and records it only if it succeeds.
func (s *Stack) Execute(ctx context.Context, op Operation) error {
	if op == nil {
		return errors.New("operation is required")
	}
	if err := op.Apply(ctx); err != nil {
		return err
	}
	s.undo = append(s.undo, op)
	if len(s.undo) > s.limit {
		s.undo = append([]Operation(nil), s.undo[len(s.undo)-s.limit:]...)
	}
	s.redo = nil
	return nil
}

// Undo reverses the most recently applied operation.
func (s *Stack) Undo(ctx context.Context) (string, error) {
	if len(s.undo) == 0 {
		return "", errors.New("nothing to undo")
	}
	op := s.undo[len(s.undo)-1]
	if err := op.Undo(ctx); err != nil {
		return "", err
	}
	s.undo = s.undo[:len(s.undo)-1]
	s.redo = append(s.redo, op)
	return op.Description(), nil
}

// Redo reapplies the most recently undone operation.
func (s *Stack) Redo(ctx context.Context) (string, error) {
	if len(s.redo) == 0 {
		return "", errors.New("nothing to redo")
	}
	op := s.redo[len(s.redo)-1]
	if err := op.Apply(ctx); err != nil {
		return "", err
	}
	s.redo = s.redo[:len(s.redo)-1]
	s.undo = append(s.undo, op)
	return op.Description(), nil
}

// CanUndo reports whether an undo operation is available.
func (s *Stack) CanUndo() bool { return len(s.undo) > 0 }

// CanRedo reports whether a redo operation is available.
func (s *Stack) CanRedo() bool { return len(s.redo) > 0 }
