package history

import (
	"context"
	"errors"
	"testing"
)

type testOperation struct {
	value       *int
	before      int
	after       int
	fail        bool
	description string
}

func (o testOperation) Apply(context.Context) error {
	if o.fail {
		return errors.New("failed")
	}
	*o.value = o.after
	return nil
}

func (o testOperation) Undo(context.Context) error {
	*o.value = o.before
	return nil
}

func (o testOperation) Description() string { return o.description }

func TestStackUndoRedoBoundAndBranch(t *testing.T) {
	ctx := context.Background()
	value := 0
	stack := New(2)
	operations := []testOperation{
		{value: &value, before: 0, after: 1, description: "one"},
		{value: &value, before: 1, after: 2, description: "two"},
		{value: &value, before: 2, after: 3, description: "three"},
	}
	for _, operation := range operations {
		if err := stack.Execute(ctx, operation); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stack.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if value != 2 {
		t.Fatalf("undo value = %d, want 2", value)
	}
	if _, err := stack.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if value != 1 {
		t.Fatalf("bounded history undo value = %d, want 1", value)
	}
	if _, err := stack.Undo(ctx); err == nil {
		t.Fatal("expected bounded history to discard its oldest operation")
	}
	if _, err := stack.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	if value != 2 || !stack.CanUndo() {
		t.Fatalf("redo state = %d, can undo %t", value, stack.CanUndo())
	}
	if err := stack.Execute(ctx, testOperation{value: &value, before: 2, after: 4}); err != nil {
		t.Fatal(err)
	}
	if stack.CanRedo() {
		t.Fatal("new operation did not clear redo history")
	}
}

func TestFailedOperationDoesNotEnterHistory(t *testing.T) {
	stack := New(5)
	if err := stack.Execute(context.Background(), testOperation{fail: true}); err == nil {
		t.Fatal("expected operation failure")
	}
	if stack.CanUndo() {
		t.Fatal("failed operation entered history")
	}
}
