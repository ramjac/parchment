package editor

import (
	"strings"
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

func TestEditorShiftSelectionAndCollapse(t *testing.T) {
	ed := New()
	ed.SetValue("Hello World")
	ed.Focus()

	// Shift+Right 5 times -> select "Hello"
	for range 5 {
		ed.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	}
	if !ed.Selection.Active {
		t.Fatalf("Selection should be active after Shift+Right")
	}
	if sel := ed.SelectedText(); sel != "Hello" {
		t.Fatalf("SelectedText() = %q, want %q", sel, "Hello")
	}

	// Unshifted Left moves to start (0) and collapses
	ed.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if ed.Selection.Active {
		t.Fatalf("Selection should collapse on unshifted Left")
	}
	if ed.Cursor.Col != 0 {
		t.Fatalf("Cursor.Col = %d, want 0", ed.Cursor.Col)
	}

	// Select again
	for range 5 {
		ed.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	}
	// Unshifted Right moves to end (5) and collapses
	ed.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ed.Selection.Active {
		t.Fatalf("Selection should collapse on unshifted Right")
	}
	if ed.Cursor.Col != 5 {
		t.Fatalf("Cursor.Col = %d, want 5", ed.Cursor.Col)
	}
}

func TestEditorDestructiveOperations(t *testing.T) {
	// Enter replaces selection
	ed := New()
	ed.SetValue("Hello World")
	ed.Selection = Selection{Anchor: Point{Line: 0, Col: 5}, Active: true}
	ed.Cursor = Point{Line: 0, Col: 11}
	ed.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if ed.Value() != "Hello\n" {
		t.Fatalf("Value() after Enter = %q, want %q", ed.Value(), "Hello\n")
	}

	// Tab replaces selection with two spaces
	ed = New()
	ed.SetValue("Hello World")
	ed.Selection = Selection{Anchor: Point{Line: 0, Col: 5}, Active: true}
	ed.Cursor = Point{Line: 0, Col: 11}
	ed.Update(tea.KeyMsg{Type: tea.KeyTab})
	if ed.Value() != "Hello  " {
		t.Fatalf("Value() after Tab = %q, want %q", ed.Value(), "Hello  ")
	}

	// Backspace deletes selection
	ed = New()
	ed.SetValue("Hello World")
	ed.Selection = Selection{Anchor: Point{Line: 0, Col: 0}, Active: true}
	ed.Cursor = Point{Line: 0, Col: 6}
	ed.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if ed.Value() != "World" {
		t.Fatalf("Value() after Backspace = %q, want %q", ed.Value(), "World")
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

func TestEditorMouseWheelAndPageUpDown(t *testing.T) {
	ed := New()
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "line"
	}
	ed.SetValue(strings.Join(lines, "\n"))
	ed.SetWidth(20)
	ed.SetHeight(10)
	ed.Focus()

	// Mouse wheel down
	ed.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	if ed.TopLine != 3 {
		t.Fatalf("TopLine after WheelDown = %d, want 3", ed.TopLine)
	}
	if ed.Selection.Active {
		t.Fatalf("WheelDown should not activate selection")
	}

	// Mouse wheel up
	ed.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	if ed.TopLine != 0 {
		t.Fatalf("TopLine after WheelUp = %d, want 0", ed.TopLine)
	}

	// PgDn
	ed.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if ed.Cursor.Line != 10 {
		t.Fatalf("Cursor.Line after PgDn = %d, want 10", ed.Cursor.Line)
	}

	// PgUp
	ed.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if ed.Cursor.Line != 0 {
		t.Fatalf("Cursor.Line after PgUp = %d, want 0", ed.Cursor.Line)
	}
}

func TestEditorViewRendering(t *testing.T) {
	ed := New()
	ed.SetValue("Hello")
	ed.SetWidth(10)
	ed.SetHeight(2)
	ed.Focus()

	view := ed.View()
	if !strings.Contains(view, "Hello") {
		t.Fatalf("View() missing text: %q", view)
	}

	// Select "ell"
	ed.Selection = Selection{Anchor: Point{Line: 0, Col: 1}, Active: true}
	ed.Cursor = Point{Line: 0, Col: 4}
	selectedView := ed.View()
	if selectedView == "" {
		t.Fatalf("View() with selection returned empty")
	}
}
