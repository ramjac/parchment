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
	failUndo    bool
	description string
}

func (o testOperation) Apply(context.Context) error {
	if o.fail {
		return errors.New("failed")
	}
	if o.value != nil {
		*o.value = o.after
	}
	return nil
}

func (o testOperation) Undo(context.Context) error {
	if o.failUndo {
		return errors.New("undo failed")
	}
	if o.value != nil {
		*o.value = o.before
	}
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

func TestExecuteNilOperation(t *testing.T) {
	stack := New(5)
	err := stack.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil operation, got nil")
	}
	if err.Error() != "operation is required" {
		t.Fatalf("got error %q, want %q", err.Error(), "operation is required")
	}
	if stack.CanUndo() {
		t.Fatal("nil operation entered undo history")
	}
}

func TestFailedOperationDoesNotEnterHistory(t *testing.T) {
	ctx := context.Background()
	stack := New(5)
	val := 0
	op := testOperation{value: &val, before: 0, after: 1, description: "op1"}

	if err := stack.Execute(ctx, op); err != nil {
		t.Fatal(err)
	}
	if _, err := stack.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	if !stack.CanRedo() {
		t.Fatal("expected can redo after undo")
	}

	failErr := stack.Execute(ctx, testOperation{fail: true})
	if failErr == nil {
		t.Fatal("expected operation failure")
	}
	if failErr.Error() != "failed" {
		t.Fatalf("got error %q, want %q", failErr.Error(), "failed")
	}
	if stack.CanUndo() {
		t.Fatal("failed operation entered history")
	}
	if !stack.CanRedo() {
		t.Fatal("failed operation cleared redo history")
	}
}

func TestNewDefaultLimit(t *testing.T) {
	stack := New(0)
	if stack.limit != 100 {
		t.Fatalf("expected limit to default to 100, got %d", stack.limit)
	}
	stackNegative := New(-5)
	if stackNegative.limit != 100 {
		t.Fatalf("expected limit to default to 100, got %d", stackNegative.limit)
	}
}

func TestUndoError(t *testing.T) {
	ctx := context.Background()
	stack := New(5)
	op := testOperation{failUndo: true, description: "fail undo op"}
	if err := stack.Execute(ctx, op); err != nil {
		t.Fatal(err)
	}
	_, err := stack.Undo(ctx)
	if err == nil {
		t.Fatal("expected error on Undo, got nil")
	}
	if err.Error() != "undo failed" {
		t.Fatalf("got error %q, want %q", err.Error(), "undo failed")
	}
}

func TestRedoEmpty(t *testing.T) {
	ctx := context.Background()
	stack := New(5)
	_, err := stack.Redo(ctx)
	if err == nil {
		t.Fatal("expected error on Redo on empty stack, got nil")
	}
	if err.Error() != "nothing to redo" {
		t.Fatalf("got error %q, want %q", err.Error(), "nothing to redo")
	}
}

func TestRedoError(t *testing.T) {
	ctx := context.Background()
	stack := New(5)
	op := testOperation{description: "op"}
	if err := stack.Execute(ctx, op); err != nil {
		t.Fatal(err)
	}
	if _, err := stack.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	// Modify operation to fail on Apply for Redo
	opFail := testOperation{fail: true, description: "op fail"}
	stack.redo[0] = opFail

	_, err := stack.Redo(ctx)
	if err == nil {
		t.Fatal("expected error on Redo, got nil")
	}
	if err.Error() != "failed" {
		t.Fatalf("got error %q, want %q", err.Error(), "failed")
	}
}
