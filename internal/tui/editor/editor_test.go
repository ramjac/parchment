package editor

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestEditorSetValueAndValue(t *testing.T) {
	ed := New()
	content := "Hello\nWorld\n"
	ed.SetValue(content)

	if ed.Value() != content {
		t.Fatalf("Value() = %q, want %q", ed.Value(), content)
	}
	if len(ed.Lines) != 3 {
		t.Fatalf("len(Lines) = %d, want 3", len(ed.Lines))
	}
}

func TestEditorInsertAndRuneBuffer(t *testing.T) {
	ed := New()
	ed.SetValue("Hello World")
	ed.Cursor = Point{Line: 0, Col: 5}

	ed.InsertRune('!')
	if ed.Value() != "Hello! World" {
		t.Fatalf("Value() = %q, want %q", ed.Value(), "Hello! World")
	}
	if ed.Cursor.Col != 6 {
		t.Fatalf("Cursor.Col = %d, want 6", ed.Cursor.Col)
	}
}

func TestEditorSelectionAndDestructiveEdit(t *testing.T) {
	ed := New()
	ed.SetValue("Hello World")

	ed.Selection = Selection{Anchor: Point{Line: 0, Col: 6}, Active: true}
	ed.Cursor = Point{Line: 0, Col: 11}

	if text := ed.SelectedText(); text != "World" {
		t.Fatalf("SelectedText() = %q, want %q", text, "World")
	}

	ed.InsertRune('P')
	if ed.Value() != "Hello P" {
		t.Fatalf("Value() after replace = %q, want %q", ed.Value(), "Hello P")
	}
}

func TestEditorMultiLineSelection(t *testing.T) {
	ed := New()
	ed.SetValue("Line 1\nLine 2\nLine 3")

	ed.Selection = Selection{Anchor: Point{Line: 0, Col: 4}, Active: true}
	ed.Cursor = Point{Line: 2, Col: 4}

	expectedSel := " 1\nLine 2\nLine"
	if sel := ed.SelectedText(); sel != expectedSel {
		t.Fatalf("SelectedText() = %q, want %q", sel, expectedSel)
	}

	ed.DeleteSelection()
	if ed.Value() != "Line 3" {
		t.Fatalf("Value() after DeleteSelection = %q, want %q", ed.Value(), "Line 3")
	}
}

func TestEditorWordNavigationAndDeletion(t *testing.T) {
	ed := New()
	ed.SetValue("foo bar baz")
	ed.Cursor = Point{Line: 0, Col: 11}

	ed.moveWordLeft()
	if ed.Cursor.Col != 8 {
		t.Fatalf("moveWordLeft Col = %d, want 8", ed.Cursor.Col)
	}

	ed.moveWordLeft()
	if ed.Cursor.Col != 4 {
		t.Fatalf("moveWordLeft Col = %d, want 4", ed.Cursor.Col)
	}

	ed.moveWordRight()
	if ed.Cursor.Col != 7 {
		t.Fatalf("moveWordRight Col = %d, want 7", ed.Cursor.Col)
	}
}

func TestEditorMouseClickAndSelection(t *testing.T) {
	ed := New()
	ed.SetValue("Hello World\nSecond Line")
	ed.SetWidth(40)
	ed.SetHeight(10)

	ed.handleMouse(tea.MouseMsg{X: 3, Y: 0, Action: tea.MouseActionPress})
	if ed.Cursor.Line != 0 || ed.Cursor.Col != 3 {
		t.Fatalf("Cursor after press = %+v, want Line 0 Col 3", ed.Cursor)
	}

	ed.handleMouse(tea.MouseMsg{X: 8, Y: 0, Action: tea.MouseActionMotion})
	if !ed.Selection.Active {
		t.Fatalf("Selection not active after drag")
	}
	if sel := ed.SelectedText(); sel != "lo Wo" {
		t.Fatalf("SelectedText() = %q, want %q", sel, "lo Wo")
	}
}

func TestEditorDoubleTripleClick(t *testing.T) {
	ed := New()
	ed.SetValue("Hello World\nSecond Line")

	now := time.Now()
	ed.lastClickTime = now
	ed.lastClickPos = Point{Line: 0, Col: 2}
	ed.clickCount = 1

	ed.handleMouse(tea.MouseMsg{X: 2, Y: 0, Action: tea.MouseActionPress})
	if ed.clickCount != 2 {
		t.Fatalf("clickCount = %d, want 2", ed.clickCount)
	}
	if sel := ed.SelectedText(); sel != "Hello" {
		t.Fatalf("SelectedText() on double click = %q, want %q", sel, "Hello")
	}

	ed.handleMouse(tea.MouseMsg{X: 2, Y: 0, Action: tea.MouseActionPress})
	if ed.clickCount != 3 {
		t.Fatalf("clickCount = %d, want 3", ed.clickCount)
	}
	if sel := ed.SelectedText(); sel != "Hello World" {
		t.Fatalf("SelectedText() on triple click = %q, want %q", sel, "Hello World")
	}
}

func TestEditorUndoAndRedo(t *testing.T) {
	ed := New()
	ed.SetValue("Hello")
	ed.Cursor = Point{Line: 0, Col: 5}

	if ed.CanUndo() {
		t.Fatal("CanUndo should be false on fresh SetValue")
	}

	ed.InsertString(" World")
	if ed.Value() != "Hello World" {
		t.Fatalf("Value = %q, want %q", ed.Value(), "Hello World")
	}
	if !ed.CanUndo() {
		t.Fatal("CanUndo should be true after InsertString")
	}

	if !ed.Undo() || ed.Value() != "Hello" {
		t.Fatalf("Undo failed: Value = %q, want %q", ed.Value(), "Hello")
	}
	if !ed.CanRedo() {
		t.Fatal("CanRedo should be true after Undo")
	}

	if !ed.Redo() || ed.Value() != "Hello World" {
		t.Fatalf("Redo failed: Value = %q, want %q", ed.Value(), "Hello World")
	}

	// Test key handling
	ed.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if ed.Value() != "Hello" {
		t.Fatalf("Ctrl+Z failed: Value = %q, want %q", ed.Value(), "Hello")
	}

	ed.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if ed.Value() != "Hello World" {
		t.Fatalf("Ctrl+R failed: Value = %q, want %q", ed.Value(), "Hello World")
	}
}
