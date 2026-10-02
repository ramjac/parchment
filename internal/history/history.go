package history

import (
	"context"
	"errors"
)

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
