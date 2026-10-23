package editor

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestEditorNew(t *testing.T) {
	ed := New()

	if len(ed.Lines) != 1 || len(ed.Lines[0]) != 0 {
		t.Fatalf("New().Lines = %v, want [][]rune{{}}", ed.Lines)
	}
	if ed.Width != 80 {
		t.Fatalf("New().Width = %d, want 80", ed.Width)
	}
	if ed.Height != 20 {
		t.Fatalf("New().Height = %d, want 20", ed.Height)
	}
	if !ed.Focused {
		t.Fatalf("New().Focused = false, want true")
	}
	if ed.Cursor.Line != 0 || ed.Cursor.Col != 0 {
		t.Fatalf("New().Cursor = %+v, want Point{Line: 0, Col: 0}", ed.Cursor)
	}
	if ed.Selection.Active {
		t.Fatalf("New().Selection.Active = true, want false")
	}
	if ed.Selection.Anchor != (Point{Line: 0, Col: 0}) {
		t.Fatalf("New().Selection.Anchor = %+v, want Point{Line: 0, Col: 0}", ed.Selection.Anchor)
	}
	if ed.TopLine != 0 {
		t.Fatalf("New().TopLine = %d, want 0", ed.TopLine)
	}
	if ed.LeftCol != 0 {
		t.Fatalf("New().LeftCol = %d, want 0", ed.LeftCol)
	}
	if ed.StickyCol != 0 {
		t.Fatalf("New().StickyCol = %d, want 0", ed.StickyCol)
	}
	if ed.Value() != "" {
		t.Fatalf("New().Value() = %q, want %q", ed.Value(), "")
	}
	if ed.LineCount() != 1 {
		t.Fatalf("New().LineCount() = %d, want 1", ed.LineCount())
	}
	if ed.CanUndo() {
		t.Fatalf("New().CanUndo() = true, want false")
	}
	if ed.CanRedo() {
		t.Fatalf("New().CanRedo() = true, want false")
	}
	expectedStyle := lipgloss.NewStyle().Reverse(true)
	if ed.SelectStyle.Render("test") != expectedStyle.Render("test") {
		t.Fatalf("New().SelectStyle mismatch")
	}
}

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

func TestEditorValue(t *testing.T) {
	t.Run("zero value or empty buffer model", func(t *testing.T) {
		ed := New()
		if got := ed.Value(); got != "" {
			t.Errorf("New().Value() = %q, want %q", got, "")
		}

		// Explicit nil Lines
		nilEd := Model{Lines: nil}
		if got := nilEd.Value(); got != "" {
			t.Errorf("Model{Lines: nil}.Value() = %q, want %q", got, "")
		}

		// Explicit empty Lines slice
		emptySliceEd := Model{Lines: [][]rune{}}
		if got := emptySliceEd.Value(); got != "" {
			t.Errorf("Model{Lines: [][]rune{}}.Value() = %q, want %q", got, "")
		}
	})

	t.Run("various string values via SetValue", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			expected string
		}{
			{
				name:     "empty string",
				input:    "",
				expected: "",
			},
			{
				name:     "single line no trailing newline",
				input:    "Hello World",
				expected: "Hello World",
			},
			{
				name:     "single line with trailing newline",
				input:    "Hello World\n",
				expected: "Hello World\n",
			},
			{
				name:     "multiple lines no trailing newline",
				input:    "Line 1\nLine 2\nLine 3",
				expected: "Line 1\nLine 2\nLine 3",
			},
			{
				name:     "multiple lines with trailing newline",
				input:    "Line 1\nLine 2\nLine 3\n",
				expected: "Line 1\nLine 2\nLine 3\n",
			},
			{
				name:     "consecutive newlines",
				input:    "Line 1\n\n\nLine 4",
				expected: "Line 1\n\n\nLine 4",
			},
			{
				name:     "unicode text",
				input:    "Hello 🌍\n日本語 text\n✨",
				expected: "Hello 🌍\n日本語 text\n✨",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				ed := New()
				ed.SetValue(tc.input)
				if got := ed.Value(); got != tc.expected {
					t.Errorf("Value() = %q, want %q", got, tc.expected)
				}
			})
		}
	})

	t.Run("value after modifications", func(t *testing.T) {
		ed := New()
		ed.SetValue("Hello")

		ed.Cursor = Point{Line: 0, Col: 5}
		ed.InsertString("\nWorld")
		if got, want := ed.Value(), "Hello\nWorld"; got != want {
			t.Errorf("Value() after InsertString = %q, want %q", got, want)
		}

		ed.Undo()
		if got, want := ed.Value(), "Hello"; got != want {
			t.Errorf("Value() after Undo = %q, want %q", got, want)
		}

		ed.Redo()
		if got, want := ed.Value(), "Hello\nWorld"; got != want {
			t.Errorf("Value() after Redo = %q, want %q", got, want)
		}

		// Test InsertRune, Backspace, and DeleteSelection
		ed.InsertRune('!')
		if got, want := ed.Value(), "Hello\nWorld!"; got != want {
			t.Errorf("Value() after InsertRune = %q, want %q", got, want)
		}

		// Delete '!' using backspace
		ed.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		if got, want := ed.Value(), "Hello\nWorld"; got != want {
			t.Errorf("Value() after Backspace = %q, want %q", got, want)
		}

		// Delete selection across lines ("o\nWor")
		ed.Selection = Selection{Anchor: Point{Line: 0, Col: 4}, Active: true}
		ed.Cursor = Point{Line: 1, Col: 3}
		ed.DeleteSelection()
		if got, want := ed.Value(), "Hellld"; got != want {
			t.Errorf("Value() after DeleteSelection = %q, want %q", got, want)
		}
	})
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

func TestEditorCanRedo(t *testing.T) {
	t.Run("zero value model and New", func(t *testing.T) {
		var zero Model
		if zero.CanRedo() {
			t.Errorf("zero value Model.CanRedo() = true, want false")
		}

		newModel := New()
		if newModel.CanRedo() {
			t.Errorf("New().CanRedo() = true, want false")
		}
	})

	t.Run("initial state and edits without undo", func(t *testing.T) {
		ed := New()
		ed.SetValue("Initial content")
		if ed.CanRedo() {
			t.Errorf("CanRedo() after SetValue = true, want false")
		}

		ed.InsertString(" edit")
		if ed.CanRedo() {
			t.Errorf("CanRedo() after edit without undo = true, want false")
		}

		ed.InsertRune('!')
		if ed.CanRedo() {
			t.Errorf("CanRedo() after InsertRune without undo = true, want false")
		}
	})

	t.Run("undo and redo state transitions", func(t *testing.T) {
		ed := New()
		ed.SetValue("Initial")
		ed.InsertString(" First Edit")

		if ed.CanRedo() {
			t.Fatalf("CanRedo() before undo = true, want false")
		}

		ed.Undo()
		if !ed.CanRedo() {
			t.Fatalf("CanRedo() after Undo = false, want true")
		}

		ed.Redo()
		if ed.CanRedo() {
			t.Fatalf("CanRedo() after Redo = true, want false")
		}
	})

	t.Run("multi-step undo and redo transitions", func(t *testing.T) {
		ed := New()
		ed.SetValue("Base")
		ed.InsertString(" 1")
		ed.InsertString(" 2")

		ed.Undo() // Undoes " 2"
		if !ed.CanRedo() {
			t.Errorf("CanRedo() after 1st Undo = false, want true")
		}

		ed.Undo() // Undoes " 1"
		if !ed.CanRedo() {
			t.Errorf("CanRedo() after 2nd Undo = false, want true")
		}

		ed.Redo() // Redoes " 1"
		if !ed.CanRedo() {
			t.Errorf("CanRedo() after 1st Redo (1 redo remaining) = false, want true")
		}

		ed.Redo() // Redoes " 2"
		if ed.CanRedo() {
			t.Errorf("CanRedo() after all Redos = true, want false")
		}
	})

	t.Run("new edit clears redo stack", func(t *testing.T) {
		ed := New()
		ed.SetValue("Base")
		ed.InsertString(" First")

		ed.Undo()
		if !ed.CanRedo() {
			t.Fatalf("CanRedo() after Undo = false, want true")
		}

		ed.InsertRune('X')
		if ed.CanRedo() {
			t.Errorf("CanRedo() after new InsertRune = true, want false")
		}

		ed.Undo() // Undoes 'X'
		if !ed.CanRedo() {
			t.Fatalf("CanRedo() after Undo 'X' = false, want true")
		}

		ed.Selection = Selection{Anchor: Point{Line: 0, Col: 0}, Active: true}
		ed.Cursor = Point{Line: 0, Col: 1}
		ed.DeleteSelection()
		if ed.CanRedo() {
			t.Errorf("CanRedo() after DeleteSelection = true, want false")
		}
	})

	t.Run("SetValue clears redo stack", func(t *testing.T) {
		ed := New()
		ed.SetValue("Base")
		ed.InsertString(" First")
		ed.Undo()
		if !ed.CanRedo() {
			t.Fatalf("CanRedo() after Undo = false, want true")
		}

		ed.SetValue("Brand New")
		if ed.CanRedo() {
			t.Errorf("CanRedo() after SetValue = true, want false")
		}
	})

	t.Run("value receiver immutability", func(t *testing.T) {
		ed := New()
		ed.SetValue("Base")
		ed.InsertString(" First")
		ed.Undo()

		beforeVal := ed.Value()
		beforeUndoLen := len(ed.undoStack)
		beforeRedoLen := len(ed.redoStack)

		res1 := ed.CanRedo()
		res2 := ed.CanRedo()

		if res1 != res2 || !res1 {
			t.Errorf("CanRedo() calls returned %v then %v, want true", res1, res2)
		}
		if ed.Value() != beforeVal || len(ed.undoStack) != beforeUndoLen || len(ed.redoStack) != beforeRedoLen {
			t.Errorf("CanRedo() mutated model state")
		}
	})

	t.Run("key bindings update redo state", func(t *testing.T) {
		ed := New()
		ed.SetValue("Base")
		ed.Focus()
		ed.InsertString(" Added")

		ed.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
		if !ed.CanRedo() {
			t.Errorf("CanRedo() after Ctrl+Z = false, want true")
		}

		ed.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
		if ed.CanRedo() {
			t.Errorf("CanRedo() after Ctrl+R = true, want false")
		}

		ed.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
		if !ed.CanRedo() {
			t.Errorf("CanRedo() after Ctrl+Z = false, want true")
		}

		ed.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
		if ed.CanRedo() {
			t.Errorf("CanRedo() after Ctrl+Y = true, want false")
		}
	})
}

func TestEditorCanUndo(t *testing.T) {
	t.Run("initial state and SetValue", func(t *testing.T) {
		ed := New()
		if ed.CanUndo() {
			t.Errorf("CanUndo() = true, want false for newly created editor")
		}

		ed.SetValue("Initial Content")
		if ed.CanUndo() {
			t.Errorf("CanUndo() = true, want false after SetValue()")
		}
	})

	t.Run("various editing mutations", func(t *testing.T) {
		ed := New()
		ed.SetValue("Hello")

		if ed.CanUndo() {
			t.Errorf("CanUndo() = true, want false before edits")
		}

		ed.InsertString(" World")
		if !ed.CanUndo() {
			t.Errorf("CanUndo() = false, want true after InsertString()")
		}

		ed.SetValue("Fresh")
		ed.InsertRune('X')
		if !ed.CanUndo() {
			t.Errorf("CanUndo() = false, want true after InsertRune()")
		}

		ed.SetValue("Select Me")
		ed.Selection = Selection{Anchor: Point{Line: 0, Col: 0}, Active: true}
		ed.Cursor = Point{Line: 0, Col: 6}
		ed.DeleteSelection()
		if !ed.CanUndo() {
			t.Errorf("CanUndo() = false, want true after DeleteSelection()")
		}

		ed.SetValue("Line1")
		ed.Cursor = Point{Line: 0, Col: 5}
		ed.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if !ed.CanUndo() {
			t.Errorf("CanUndo() = false, want true after Enter key")
		}

		ed.SetValue("Line1\nLine2")
		ed.Cursor = Point{Line: 1, Col: 0}
		ed.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		if !ed.CanUndo() {
			t.Errorf("CanUndo() = false, want true after Backspace key")
		}

		ed.SetValue("Line1\nLine2")
		ed.Cursor = Point{Line: 0, Col: 5}
		ed.Update(tea.KeyMsg{Type: tea.KeyDelete})
		if !ed.CanUndo() {
			t.Errorf("CanUndo() = false, want true after Delete key")
		}
	})

	t.Run("undoing until history exhausted", func(t *testing.T) {
		ed := New()
		ed.SetValue("Base")

		ed.InsertString(" 1")
		ed.InsertString(" 2")
		ed.InsertString(" 3")

		if !ed.CanUndo() {
			t.Fatalf("CanUndo() = false, want true with 3 undo states")
		}

		// First undo
		if !ed.Undo() || !ed.CanUndo() {
			t.Errorf("CanUndo() should be true after 1st undo out of 3")
		}

		// Second undo
		if !ed.Undo() || !ed.CanUndo() {
			t.Errorf("CanUndo() should be true after 2nd undo out of 3")
		}

		// Third undo
		if !ed.Undo() {
			t.Fatalf("3rd Undo failed")
		}
		if ed.CanUndo() {
			t.Errorf("CanUndo() = true, want false after all undo history is exhausted")
		}
	})

	t.Run("redo restores undo history", func(t *testing.T) {
		ed := New()
		ed.SetValue("Start")
		ed.InsertString(" Edit")

		ed.Undo()
		if ed.CanUndo() {
			t.Fatalf("CanUndo() = true, want false after undoing single edit")
		}

		ed.Redo()
		if !ed.CanUndo() {
			t.Errorf("CanUndo() = false, want true after Redo()")
		}
	})

	t.Run("stack limit truncation retains undo capability", func(t *testing.T) {
		ed := New()
		ed.SetValue("Root")

		for range 250 {
			ed.InsertString("a")
		}

		if !ed.CanUndo() {
			t.Fatalf("CanUndo() = false, want true when history exceeds max capacity")
		}

		undoCount := 0
		for ed.CanUndo() {
			if !ed.Undo() {
				t.Fatalf("Undo() returned false while CanUndo() reported true at count %d", undoCount)
			}
			undoCount++
		}

		if undoCount != 200 {
			t.Errorf("exhausted undo count = %d, want 200 due to max stack size limit", undoCount)
		}
		if ed.CanUndo() {
			t.Errorf("CanUndo() = true, want false after undoing maximum history length")
		}
	})
}

func TestEditorRedoDirectAndEdgeCases(t *testing.T) {
	// 1. Redo on empty stack returns false
	ed := New()
	ed.SetValue("Initial")
	if ed.Redo() {
		t.Fatal("Redo() should return false when redo stack is empty")
	}

	// 2. Multi-step Undo/Redo & State Verification
	ed.Cursor = Point{Line: 0, Col: len("Initial")}
	ed.InsertString(" Second")
	ed.InsertString(" Third")
	if ed.Value() != "Initial Second Third" {
		t.Fatalf("Value = %q, want %q", ed.Value(), "Initial Second Third")
	}

	// Undo " Third"
	if !ed.Undo() || ed.Value() != "Initial Second" {
		t.Fatalf("First Undo failed, Value = %q", ed.Value())
	}

	// Undo " Second"
	if !ed.Undo() || ed.Value() != "Initial" {
		t.Fatalf("Second Undo failed, Value = %q", ed.Value())
	}

	// Redo " Second"
	if !ed.Redo() || ed.Value() != "Initial Second" {
		t.Fatalf("First Redo failed, Value = %q", ed.Value())
	}
	if ed.StickyCol != len("Initial Second") {
		t.Fatalf("StickyCol after Redo = %d, want %d", ed.StickyCol, len("Initial Second"))
	}
	if ed.Selection.Active {
		t.Fatal("Selection.Active should be false after Redo")
	}

	// Redo " Third"
	if !ed.Redo() || ed.Value() != "Initial Second Third" {
		t.Fatalf("Second Redo failed, Value = %q", ed.Value())
	}

	// No more redos available
	if ed.CanRedo() || ed.Redo() {
		t.Fatal("CanRedo should be false when redo stack is exhausted")
	}

	// 3. New edit after Undo clears redo stack
	ed.Undo() // back to "Initial Second"
	if !ed.CanRedo() {
		t.Fatal("CanRedo should be true after Undo")
	}
	ed.InsertString(" Branch") // new edit
	if ed.CanRedo() || ed.Redo() {
		t.Fatal("New edit after Undo should clear redo stack")
	}

	// 4. Test Key Bindings for Redo (ctrl+y, ctrl+shift+z)
	ed = New()
	ed.SetValue("Start")
	ed.Cursor = Point{Line: 0, Col: len("Start")}
	ed.Focus()
	ed.InsertString(" End")
	ed.Undo()
	if ed.Value() != "Start" {
		t.Fatalf("Value after Undo = %q, want %q", ed.Value(), "Start")
	}

	// Test ctrl+y key
	ed.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	if ed.Value() != "Start End" {
		t.Fatalf("Ctrl+Y Redo failed: Value = %q, want %q", ed.Value(), "Start End")
	}

	ed.Undo()
	// Test ctrl+shift+z key
	ed.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ctrl+shift+z")})
	if ed.Value() != "Start End" {
		t.Fatalf("Ctrl+Shift+Z Redo failed: Value = %q, want %q", ed.Value(), "Start End")
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

func TestEditorLineCount(t *testing.T) {
	t.Run("default initialized editor", func(t *testing.T) {
		ed := New()
		if got := ed.LineCount(); got != 1 {
			t.Errorf("New().LineCount() = %d, want 1", got)
		}
	})

	t.Run("line count after SetValue", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			expected int
		}{
			{
				name:     "empty string",
				input:    "",
				expected: 1,
			},
			{
				name:     "single line without trailing newline",
				input:    "Hello World",
				expected: 1,
			},
			{
				name:     "single line with trailing newline",
				input:    "Hello World\n",
				expected: 2,
			},
			{
				name:     "multiple lines",
				input:    "Line 1\nLine 2\nLine 3",
				expected: 3,
			},
			{
				name:     "multiple lines with trailing newline",
				input:    "Line 1\nLine 2\nLine 3\n",
				expected: 4,
			},
			{
				name:     "consecutive newlines",
				input:    "Line 1\n\n\nLine 4",
				expected: 4,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				ed := New()
				ed.SetValue(tc.input)
				if got := ed.LineCount(); got != tc.expected {
					t.Errorf("LineCount() = %d, want %d for input %q", got, tc.expected, tc.input)
				}
			})
		}
	})

	t.Run("line count after buffer modifications", func(t *testing.T) {
		ed := New()
		ed.SetValue("Line 1\nLine 2")
		if got := ed.LineCount(); got != 2 {
			t.Fatalf("LineCount() initial = %d, want 2", got)
		}

		// Insert newline
		ed.Cursor = Point{Line: 1, Col: 6}
		ed.InsertString("\nLine 3")
		if got := ed.LineCount(); got != 3 {
			t.Fatalf("LineCount() after InsertString = %d, want 3", got)
		}

		// Multiline selection delete
		ed.Selection = Selection{Anchor: Point{Line: 0, Col: 0}, Active: true}
		ed.Cursor = Point{Line: 2, Col: 0}
		ed.DeleteSelection()
		if got := ed.LineCount(); got != 1 {
			t.Fatalf("LineCount() after DeleteSelection = %d, want 1", got)
		}

		// Undo selection delete
		ed.Undo()
		if got := ed.LineCount(); got != 3 {
			t.Fatalf("LineCount() after Undo = %d, want 3", got)
		}

		// Redo selection delete
		ed.Redo()
		if got := ed.LineCount(); got != 1 {
			t.Fatalf("LineCount() after Redo = %d, want 1", got)
		}
	})
}

func TestEditorLine(t *testing.T) {
	t.Run("initial state", func(t *testing.T) {
		ed := New()
		if got := ed.Line(); got != 0 {
			t.Errorf("Line() = %d, want 0", got)
		}
	})

	t.Run("after SetValue resets cursor to line 0", func(t *testing.T) {
		ed := New()
		ed.SetValue("First\nSecond\nThird")
		ed.CursorDown()
		ed.CursorDown()
		if got := ed.Line(); got != 2 {
			t.Fatalf("Line() before SetValue = %d, want 2", got)
		}

		ed.SetValue("New First\nNew Second")
		if got := ed.Line(); got != 0 {
			t.Errorf("Line() after SetValue = %d, want 0", got)
		}
	})

	t.Run("vertical navigation CursorUp CursorDown", func(t *testing.T) {
		ed := New()
		ed.SetValue("Line 0\nLine 1\nLine 2")

		if got := ed.Line(); got != 0 {
			t.Errorf("Line() initial = %d, want 0", got)
		}

		ed.CursorDown()
		if got := ed.Line(); got != 1 {
			t.Errorf("Line() after CursorDown = %d, want 1", got)
		}

		ed.CursorDown()
		if got := ed.Line(); got != 2 {
			t.Errorf("Line() after second CursorDown = %d, want 2", got)
		}

		// CursorDown at last line should stay at last line
		ed.CursorDown()
		if got := ed.Line(); got != 2 {
			t.Errorf("Line() after CursorDown at bottom = %d, want 2", got)
		}

		ed.CursorUp()
		if got := ed.Line(); got != 1 {
			t.Errorf("Line() after CursorUp = %d, want 1", got)
		}

		ed.CursorUp()
		if got := ed.Line(); got != 0 {
			t.Errorf("Line() after second CursorUp = %d, want 0", got)
		}

		// CursorUp at top line should stay at 0
		ed.CursorUp()
		if got := ed.Line(); got != 0 {
			t.Errorf("Line() after CursorUp at top = %d, want 0", got)
		}
	})

	t.Run("insertion and deletion of lines", func(t *testing.T) {
		ed := New()
		ed.SetValue("Alpha")
		ed.Cursor = Point{Line: 0, Col: 5}

		// Inserting newline via Enter key moves cursor to line 1
		ed.Focus()
		ed.Update(tea.KeyMsg{Type: tea.KeyEnter})
		if got := ed.Line(); got != 1 {
			t.Errorf("Line() after Enter = %d, want 1", got)
		}

		ed.InsertString("Beta\nGamma")
		if got := ed.Line(); got != 2 {
			t.Errorf("Line() after InsertString multiline = %d, want 2", got)
		}

		// Backspace at Col 0 of line 2 merges line 2 into line 1
		ed.Cursor = Point{Line: 2, Col: 0}
		ed.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		if got := ed.Line(); got != 1 {
			t.Errorf("Line() after Backspace line merge = %d, want 1", got)
		}
	})

	t.Run("page navigation pgup pgdown", func(t *testing.T) {
		ed := New()
		var sb strings.Builder
		for i := 0; i < 50; i++ {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString("line")
		}
		ed.SetValue(sb.String())
		ed.SetHeight(10)
		ed.Focus()

		ed.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		if got := ed.Line(); got != 10 {
			t.Errorf("Line() after PgDown = %d, want 10", got)
		}

		ed.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		if got := ed.Line(); got != 20 {
			t.Errorf("Line() after second PgDown = %d, want 20", got)
		}

		ed.Update(tea.KeyMsg{Type: tea.KeyPgUp})
		if got := ed.Line(); got != 10 {
			t.Errorf("Line() after PgUp = %d, want 10", got)
		}
	})

	t.Run("direct cursor manipulation and clamping", func(t *testing.T) {
		ed := New()
		ed.SetValue("Line 0\nLine 1")

		ed.Cursor.Line = 1
		if got := ed.Line(); got != 1 {
			t.Errorf("Line() after direct set = %d, want 1", got)
		}

		// Clamp when clampCursor is called
		ed.Cursor.Line = 99
		ed.clampCursor()
		if got := ed.Line(); got != 1 {
			t.Errorf("Line() after clamping max = %d, want 1", got)
		}

		ed.Cursor.Line = -10
		ed.clampCursor()
		if got := ed.Line(); got != 0 {
			t.Errorf("Line() after clamping min = %d, want 0", got)
		}
	})

	t.Run("undo and redo line position restoration", func(t *testing.T) {
		ed := New()
		ed.SetValue("Line 0")
		ed.Cursor = Point{Line: 0, Col: 6}

		ed.InsertString("\nLine 1")
		if got := ed.Line(); got != 1 {
			t.Fatalf("Line() = %d, want 1", got)
		}

		ed.Undo()
		if got := ed.Line(); got != 0 {
			t.Errorf("Line() after Undo = %d, want 0", got)
		}

		ed.Redo()
		if got := ed.Line(); got != 1 {
			t.Errorf("Line() after Redo = %d, want 1", got)
		}
	})
}

func TestEditorUndoDetailed(t *testing.T) {
	t.Run("empty undo stack", func(t *testing.T) {
		ed := New()
		ed.SetValue("Initial")
		if ed.Undo() {
			t.Fatal("Undo() should return false when undo stack is empty")
		}
		if ed.Value() != "Initial" {
			t.Fatalf("Value = %q, want %q", ed.Value(), "Initial")
		}
	})

	t.Run("single undo restores cursor, stickyCol, selection and lines", func(t *testing.T) {
		ed := New()
		ed.SetValue("Hello")
		ed.Cursor = Point{Line: 0, Col: 5}

		// Save state parameters before insert
		origCursor := ed.Cursor

		ed.InsertString(" World") // creates snapshot

		if ed.Value() != "Hello World" {
			t.Fatalf("Value before Undo = %q, want %q", ed.Value(), "Hello World")
		}

		ok := ed.Undo()
		if !ok {
			t.Fatal("Undo() returned false, expected true")
		}

		if ed.Value() != "Hello" {
			t.Fatalf("Value after Undo = %q, want %q", ed.Value(), "Hello")
		}
		if ed.Cursor != origCursor {
			t.Fatalf("Cursor after Undo = %+v, want %+v", ed.Cursor, origCursor)
		}
		if ed.StickyCol != origCursor.Col {
			t.Fatalf("StickyCol after Undo = %d, want %d", ed.StickyCol, origCursor.Col)
		}
		if ed.Selection.Active {
			t.Fatal("Selection.Active should be false after Undo")
		}
		if ed.Selection.Anchor != ed.Cursor {
			t.Fatalf("Selection.Anchor = %+v, want equal to Cursor %+v", ed.Selection.Anchor, ed.Cursor)
		}
	})

	t.Run("multi-step LIFO undo", func(t *testing.T) {
		ed := New()
		ed.SetValue("Step 0")
		ed.Cursor = Point{Line: 0, Col: len("Step 0")}

		ed.InsertString("\nStep 1")
		ed.InsertString("\nStep 2")

		if ed.Value() != "Step 0\nStep 1\nStep 2" {
			t.Fatalf("Value = %q, want %q", ed.Value(), "Step 0\nStep 1\nStep 2")
		}

		if !ed.Undo() || ed.Value() != "Step 0\nStep 1" {
			t.Fatalf("First Undo failed: Value = %q, want %q", ed.Value(), "Step 0\nStep 1")
		}

		if !ed.Undo() || ed.Value() != "Step 0" {
			t.Fatalf("Second Undo failed: Value = %q, want %q", ed.Value(), "Step 0")
		}

		if ed.Undo() {
			t.Fatal("Third Undo should return false")
		}
	})

	t.Run("undo selection deletion", func(t *testing.T) {
		ed := New()
		ed.SetValue("Keep Delete Keep")
		// "Keep " is indices 0..5, "Delete" is 5..11
		ed.Selection = Selection{Anchor: Point{Line: 0, Col: 5}, Active: true}
		ed.Cursor = Point{Line: 0, Col: 11}

		ed.DeleteSelection()
		if ed.Value() != "Keep  Keep" {
			t.Fatalf("Value after DeleteSelection = %q, want %q", ed.Value(), "Keep  Keep")
		}

		if !ed.Undo() || ed.Value() != "Keep Delete Keep" {
			t.Fatalf("Undo DeleteSelection failed: Value = %q, want %q", ed.Value(), "Keep Delete Keep")
		}
	})

	t.Run("undo history capacity limit", func(t *testing.T) {
		ed := New()
		ed.SetValue("Start")

		// Push 210 edits
		for range 210 {
			ed.InsertString("a")
		}

		// Can undo up to 200 times
		undoCount := 0
		for ed.Undo() {
			undoCount++
		}

		if undoCount != 200 {
			t.Fatalf("Undo count = %d, want 200", undoCount)
		}
	})
}

func TestSetValueResetState(t *testing.T) {
	ed := New()
	ed.SetValue("Initial Line 1\nInitial Line 2\nInitial Line 3")

	// Modify editor state: cursor, sticky col, selection, viewport, edit history
	ed.Cursor = Point{Line: 2, Col: 5}
	ed.StickyCol = 5
	ed.Selection = Selection{Anchor: Point{Line: 0, Col: 0}, Active: true}
	ed.TopLine = 1
	ed.LeftCol = 2

	ed.InsertString(" extra")
	if !ed.CanUndo() {
		t.Fatal("Expected CanUndo to be true after InsertString")
	}
	ed.Undo()
	if !ed.CanRedo() {
		t.Fatal("Expected CanRedo to be true after Undo")
	}

	// Call SetValue to load new content
	ed.SetValue("Replacement Line 1\nReplacement Line 2")

	if got := ed.Value(); got != "Replacement Line 1\nReplacement Line 2" {
		t.Errorf("Value() = %q, want %q", got, "Replacement Line 1\nReplacement Line 2")
	}
	if ed.Cursor != (Point{Line: 0, Col: 0}) {
		t.Errorf("Cursor = %+v, want Point{0,0}", ed.Cursor)
	}
	if ed.StickyCol != 0 {
		t.Errorf("StickyCol = %d, want 0", ed.StickyCol)
	}
	if ed.Selection != (Selection{}) {
		t.Errorf("Selection = %+v, want Selection{}", ed.Selection)
	}
	if ed.TopLine != 0 {
		t.Errorf("TopLine = %d, want 0", ed.TopLine)
	}
	if ed.LeftCol != 0 {
		t.Errorf("LeftCol = %d, want 0", ed.LeftCol)
	}
	if ed.CanUndo() {
		t.Errorf("CanUndo() = true, want false after SetValue")
	}
	if ed.CanRedo() {
		t.Errorf("CanRedo() = true, want false after SetValue")
	}
	if ed.lastEditType != editNone {
		t.Errorf("lastEditType = %v, want editNone", ed.lastEditType)
	}
}

func TestUndo(t *testing.T) {
	t.Run("returns false when undo stack is empty", func(t *testing.T) {
		ed := New()
		ed.SetValue("Sample text")
		if ed.Undo() {
			t.Errorf("Undo() = true, want false on empty stack")
		}
		if ed.Value() != "Sample text" {
			t.Errorf("Value() changed to %q, want %q", ed.Value(), "Sample text")
		}
	})

	t.Run("restores buffer, cursor, sticky column, and resets selection", func(t *testing.T) {
		ed := New()
		ed.SetValue("Hello World")
		ed.Cursor = Point{Line: 0, Col: 11}
		ed.StickyCol = 11

		// Insert string creates a snapshot of ("Hello World", Cursor{0, 11})
		ed.InsertString("!!!")
		if ed.Value() != "Hello World!!!" {
			t.Fatalf("Value after InsertString = %q, want %q", ed.Value(), "Hello World!!!")
		}

		// Set an active selection
		ed.Selection = Selection{
			Anchor: Point{Line: 0, Col: 0},
			Active: true,
		}

		ok := ed.Undo()
		if !ok {
			t.Fatalf("Undo() returned false, want true")
		}

		if ed.Value() != "Hello World" {
			t.Errorf("Value() after Undo = %q, want %q", ed.Value(), "Hello World")
		}
		if ed.Cursor != (Point{Line: 0, Col: 11}) {
			t.Errorf("Cursor after Undo = %+v, want Point{0, 11}", ed.Cursor)
		}
		if ed.StickyCol != 11 {
			t.Errorf("StickyCol after Undo = %d, want 11", ed.StickyCol)
		}
		if ed.Selection.Active {
			t.Errorf("Selection.Active = true, want false")
		}
		if ed.Selection.Anchor != ed.Cursor {
			t.Errorf("Selection.Anchor = %+v, want %+v", ed.Selection.Anchor, ed.Cursor)
		}
	})

	t.Run("pushes state to redo stack and pops undo stack", func(t *testing.T) {
		ed := New()
		ed.SetValue("Alpha")
		ed.Cursor = Point{Line: 0, Col: 5}

		if ed.CanRedo() {
			t.Fatalf("CanRedo() = true initially, want false")
		}

		ed.InsertString(" Beta")
		if len(ed.undoStack) != 1 {
			t.Fatalf("len(undoStack) = %d, want 1", len(ed.undoStack))
		}

		ed.Undo()

		if len(ed.undoStack) != 0 {
			t.Errorf("len(undoStack) after Undo = %d, want 0", len(ed.undoStack))
		}
		if !ed.CanRedo() {
			t.Errorf("CanRedo() after Undo = false, want true")
		}
		if len(ed.redoStack) != 1 {
			t.Fatalf("len(redoStack) after Undo = %d, want 1", len(ed.redoStack))
		}

		// Verify redo stack stored the state prior to undo
		redoSnap := ed.redoStack[0]
		if string(redoSnap.Lines[0]) != "Alpha Beta" {
			t.Errorf("redoStack snapshot lines = %q, want %q", string(redoSnap.Lines[0]), "Alpha Beta")
		}
	})

	t.Run("resets lastEditType to editNone", func(t *testing.T) {
		ed := New()
		ed.SetValue("Hello")
		ed.Cursor = Point{Line: 0, Col: 5}

		// Typing runes consecutively batches without extra snapshots while lastEditType == editTyping
		ed.InsertRune('a') // snapshot saved, lastEditType becomes editTyping
		ed.InsertRune('b') // no new snapshot saved because lastEditType == editTyping
		if len(ed.undoStack) != 1 {
			t.Fatalf("len(undoStack) = %d, want 1", len(ed.undoStack))
		}

		ed.Undo() // resets lastEditType to editNone
		if ed.lastEditType != editNone {
			t.Errorf("lastEditType after Undo = %v, want editNone", ed.lastEditType)
		}

		// Subsequent rune insertion must save a new snapshot because lastEditType was editNone
		ed.InsertRune('c')
		if len(ed.undoStack) != 1 {
			t.Fatalf("len(undoStack) after InsertRune = %d, want 1", len(ed.undoStack))
		}
	})

	t.Run("adjusts viewport when restored cursor is out of view", func(t *testing.T) {
		ed := New()
		ed.SetHeight(5)
		ed.SetWidth(10)

		// Create multiline document
		var lines []string
		for i := 0; i < 20; i++ {
			lines = append(lines, "line")
		}
		ed.SetValue(strings.Join(lines, "\n"))

		// Move cursor to top (Line 0, Col 0)
		ed.Cursor = Point{Line: 0, Col: 0}

		// Perform operation that moves cursor far down and creates snapshot
		ed.InsertString("\n") // snapshot saved with Cursor{Line: 0, Col: 0}
		ed.Cursor = Point{Line: 18, Col: 0}
		ed.TopLine = 15 // Scrolled down

		// Undo should restore cursor to Line 0, Col 0 and adjust TopLine
		ed.Undo()
		if ed.Cursor.Line != 0 {
			t.Errorf("Cursor.Line after Undo = %d, want 0", ed.Cursor.Line)
		}
		if ed.TopLine != 0 {
			t.Errorf("TopLine after Undo = %d, want 0", ed.TopLine)
		}
	})

	t.Run("clones lines to prevent mutation of snapshot history", func(t *testing.T) {
		ed := New()
		ed.SetValue("Original")
		ed.Cursor = Point{Line: 0, Col: 8}

		ed.InsertString(" Step1") // snapshot 0: "Original"
		ed.InsertString(" Step2") // snapshot 1: "Original Step1"

		// Perform Undo to restore to "Original Step1"
		ed.Undo()

		// Mutate active buffer line directly
		ed.Lines[0][0] = 'X'

		// Verify that remaining snapshot in undoStack ("Original") is unaffected
		if string(ed.undoStack[0].Lines[0]) != "Original" {
			t.Errorf("undoStack snapshot line = %q, want %q", string(ed.undoStack[0].Lines[0]), "Original")
		}
	})

	t.Run("ctrl+z triggers Undo via Update", func(t *testing.T) {
		ed := New()
		ed.SetValue("Text")
		ed.Cursor = Point{Line: 0, Col: 4}
		ed.Focus()

		ed.InsertString(" Extra")
		if ed.Value() != "Text Extra" {
			t.Fatalf("Value = %q, want %q", ed.Value(), "Text Extra")
		}

		ed.Update(tea.KeyMsg{Type: tea.KeyCtrlZ})
		if ed.Value() != "Text" {
			t.Errorf("Value() after ctrl+z = %q, want %q", ed.Value(), "Text")
		}
	})
}

func TestRedo(t *testing.T) {
	t.Run("returns false when redo stack is empty", func(t *testing.T) {
		ed := New()
		ed.SetValue("Sample text")
		if ed.Redo() {
			t.Errorf("Redo() = true, want false on empty stack")
		}
		if ed.Value() != "Sample text" {
			t.Errorf("Value() changed to %q, want %q", ed.Value(), "Sample text")
		}
	})

	t.Run("restores buffer, cursor, sticky column, and resets selection", func(t *testing.T) {
		ed := New()
		ed.SetValue("Hello")
		ed.Cursor = Point{Line: 0, Col: 5}

		ed.InsertString(" World")
		targetCursor := ed.Cursor
		targetVal := ed.Value()

		ed.Undo()
		if ed.Value() != "Hello" {
			t.Fatalf("Value after Undo = %q, want %q", ed.Value(), "Hello")
		}

		// Set an active selection prior to Redo
		ed.Selection = Selection{
			Anchor: Point{Line: 0, Col: 0},
			Active: true,
		}

		ok := ed.Redo()
		if !ok {
			t.Fatalf("Redo() returned false, want true")
		}

		if ed.Value() != targetVal {
			t.Errorf("Value() after Redo = %q, want %q", ed.Value(), targetVal)
		}
		if ed.Cursor != targetCursor {
			t.Errorf("Cursor after Redo = %+v, want %+v", ed.Cursor, targetCursor)
		}
		if ed.StickyCol != targetCursor.Col {
			t.Errorf("StickyCol after Redo = %d, want %d", ed.StickyCol, targetCursor.Col)
		}
		if ed.Selection.Active {
			t.Errorf("Selection.Active = true, want false")
		}
		if ed.Selection.Anchor != ed.Cursor {
			t.Errorf("Selection.Anchor = %+v, want %+v", ed.Selection.Anchor, ed.Cursor)
		}
	})

	t.Run("pushes state to undo stack and pops redo stack", func(t *testing.T) {
		ed := New()
		ed.SetValue("Alpha")
		ed.Cursor = Point{Line: 0, Col: 5}

		ed.InsertString(" Beta")
		ed.Undo()

		if len(ed.redoStack) != 1 {
			t.Fatalf("len(redoStack) before Redo = %d, want 1", len(ed.redoStack))
		}
		if len(ed.undoStack) != 0 {
			t.Fatalf("len(undoStack) before Redo = %d, want 0", len(ed.undoStack))
		}

		ed.Redo()

		if len(ed.redoStack) != 0 {
			t.Errorf("len(redoStack) after Redo = %d, want 0", len(ed.redoStack))
		}
		if len(ed.undoStack) != 1 {
			t.Errorf("len(undoStack) after Redo = %d, want 1", len(ed.undoStack))
		}

		// Verify undo stack stored the state prior to redo ("Alpha")
		undoSnap := ed.undoStack[0]
		if string(undoSnap.Lines[0]) != "Alpha" {
			t.Errorf("undoStack snapshot lines = %q, want %q", string(undoSnap.Lines[0]), "Alpha")
		}
	})

	t.Run("resets lastEditType to editNone", func(t *testing.T) {
		ed := New()
		ed.SetValue("Hello")
		ed.Cursor = Point{Line: 0, Col: 5}

		ed.InsertString(" World")
		ed.Undo()

		// Manually set lastEditType to editTyping
		ed.lastEditType = editTyping

		ed.Redo()
		if ed.lastEditType != editNone {
			t.Errorf("lastEditType after Redo = %v, want editNone", ed.lastEditType)
		}
	})

	t.Run("adjusts viewport when restored cursor is out of view", func(t *testing.T) {
		ed := New()
		ed.SetHeight(5)
		ed.SetWidth(10)

		var lines []string
		for i := 0; i < 20; i++ {
			lines = append(lines, "line")
		}
		ed.SetValue(strings.Join(lines, "\n"))

		ed.Cursor = Point{Line: 0, Col: 0}
		ed.InsertString("\nExtra Line") // cursor moves to Line 1
		ed.Undo()                       // cursor back to Line 0, TopLine is 0

		// Manually scroll viewport far away from Line 1
		ed.TopLine = 10
		ed.Cursor = Point{Line: 10, Col: 0}

		ed.Redo() // restores cursor to Line 1
		if ed.Cursor.Line != 1 {
			t.Errorf("Cursor.Line after Redo = %d, want 1", ed.Cursor.Line)
		}
		// ensureCursorVisible clamps TopLine to Line 1 (since Line 1 < TopLine 10)
		if ed.TopLine != 1 {
			t.Errorf("TopLine after Redo = %d, want 1", ed.TopLine)
		}
	})

	t.Run("clones lines to prevent mutation of snapshot history", func(t *testing.T) {
		ed := New()
		ed.SetValue("Original")
		ed.Cursor = Point{Line: 0, Col: 8}

		ed.InsertString(" Step1")
		ed.Undo()

		// Redo restores "Original Step1"
		ed.Redo()

		// Mutate active buffer line directly
		ed.Lines[0][0] = 'X'

		// Verify snapshot in undoStack ("Original") is unaffected by active buffer mutation
		if string(ed.undoStack[0].Lines[0]) != "Original" {
			t.Errorf("undoStack snapshot line = %q, want %q", string(ed.undoStack[0].Lines[0]), "Original")
		}
	})

	t.Run("key bindings trigger Redo via Update", func(t *testing.T) {
		testKeys := []struct {
			name string
			msg  tea.Msg
		}{
			{
				name: "ctrl+r",
				msg:  tea.KeyMsg{Type: tea.KeyCtrlR},
			},
			{
				name: "ctrl+y",
				msg:  tea.KeyMsg{Type: tea.KeyCtrlY},
			},
			{
				name: "ctrl+shift+z",
				msg:  tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ctrl+shift+z")},
			},
		}

		for _, tk := range testKeys {
			t.Run(tk.name, func(t *testing.T) {
				ed := New()
				ed.SetValue("Text")
				ed.Cursor = Point{Line: 0, Col: 4}
				ed.Focus()

				ed.InsertString(" Extra")
				ed.Undo()
				if ed.Value() != "Text" {
					t.Fatalf("Value after Undo = %q, want %q", ed.Value(), "Text")
				}

				ed.Update(tk.msg)
				if ed.Value() != "Text Extra" {
					t.Errorf("Value() after key %s = %q, want %q", tk.name, ed.Value(), "Text Extra")
				}
			})
		}
	})
}

func TestSetValue(t *testing.T) {
	t.Run("basic string loading and rune conversion", func(t *testing.T) {
		ed := New()
		input := "Hello\nWorld"
		ed.SetValue(input)

		if ed.Value() != input {
			t.Errorf("Value() = %q, want %q", ed.Value(), input)
		}
		if len(ed.Lines) != 2 {
			t.Fatalf("len(Lines) = %d, want 2", len(ed.Lines))
		}
		if string(ed.Lines[0]) != "Hello" {
			t.Errorf("Lines[0] = %q, want %q", string(ed.Lines[0]), "Hello")
		}
		if string(ed.Lines[1]) != "World" {
			t.Errorf("Lines[1] = %q, want %q", string(ed.Lines[1]), "World")
		}
	})

	t.Run("full state reset on populated model", func(t *testing.T) {
		ed := New()
		ed.SetValue("Initial line 1\nInitial line 2")

		// Modify various internal state properties
		ed.Cursor = Point{Line: 1, Col: 5}
		ed.StickyCol = 5
		ed.Selection = Selection{Anchor: Point{Line: 0, Col: 2}, Active: true}
		ed.TopLine = 1
		ed.LeftCol = 3

		// Push undo and redo stack entries
		ed.InsertString(" edit")
		if !ed.CanUndo() {
			t.Fatalf("CanUndo() = false, want true after edit")
		}
		ed.Undo()
		if !ed.CanRedo() {
			t.Fatalf("CanRedo() = false, want true after undo")
		}

		// Execute SetValue
		newText := "New content line 1\nNew content line 2"
		ed.SetValue(newText)

		// Assert that all state properties are reset
		if got := ed.Value(); got != newText {
			t.Errorf("Value() = %q, want %q", got, newText)
		}
		if ed.Cursor != (Point{Line: 0, Col: 0}) {
			t.Errorf("Cursor = %+v, want Point{0,0}", ed.Cursor)
		}
		if ed.StickyCol != 0 {
			t.Errorf("StickyCol = %d, want 0", ed.StickyCol)
		}
		if ed.Selection != (Selection{}) {
			t.Errorf("Selection = %+v, want Selection{}", ed.Selection)
		}
		if ed.TopLine != 0 {
			t.Errorf("TopLine = %d, want 0", ed.TopLine)
		}
		if ed.LeftCol != 0 {
			t.Errorf("LeftCol = %d, want 0", ed.LeftCol)
		}
		if ed.CanUndo() {
			t.Errorf("CanUndo() = true, want false")
		}
		if ed.CanRedo() {
			t.Errorf("CanRedo() = true, want false")
		}
		if len(ed.undoStack) != 0 {
			t.Errorf("len(undoStack) = %d, want 0", len(ed.undoStack))
		}
		if len(ed.redoStack) != 0 {
			t.Errorf("len(redoStack) = %d, want 0", len(ed.redoStack))
		}
		if ed.lastEditType != editNone {
			t.Errorf("lastEditType = %v, want editNone", ed.lastEditType)
		}
	})

	t.Run("table-driven edge cases", func(t *testing.T) {
		tests := []struct {
			name          string
			input         string
			expectedLines int
			expectedValue string
		}{
			{
				name:          "empty string",
				input:         "",
				expectedLines: 1,
				expectedValue: "",
			},
			{
				name:          "single line without trailing newline",
				input:         "Hello world",
				expectedLines: 1,
				expectedValue: "Hello world",
			},
			{
				name:          "single line with trailing newline",
				input:         "Hello world\n",
				expectedLines: 2,
				expectedValue: "Hello world\n",
			},
			{
				name:          "multiline with consecutive empty lines",
				input:         "line1\n\n\nline4\n",
				expectedLines: 5,
				expectedValue: "line1\n\n\nline4\n",
			},
			{
				name:          "unicode and emoji content",
				input:         "⌘ Hello 世界 🎉\n🚀 Go Parchment",
				expectedLines: 2,
				expectedValue: "⌘ Hello 世界 🎉\n🚀 Go Parchment",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				ed := New()
				ed.SetValue(tc.input)

				if ed.LineCount() != tc.expectedLines {
					t.Errorf("LineCount() = %d, want %d", ed.LineCount(), tc.expectedLines)
				}
				if got := ed.Value(); got != tc.expectedValue {
					t.Errorf("Value() = %q, want %q", got, tc.expectedValue)
				}
				if ed.Cursor != (Point{Line: 0, Col: 0}) {
					t.Errorf("Cursor = %+v, want Point{0,0}", ed.Cursor)
				}
			})
		}
	})
}
