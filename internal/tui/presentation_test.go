package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/presentation"
)

// drivePresentation sends a message and feeds presentation results back in.
// While editing, only a pending save command runs; textarea focus and cursor
// blink commands are skipped because executing them sleeps and repeats.
// Results other than presentation messages are dropped for the same reason.
func drivePresentation(t *testing.T, m *presentationModel, message tea.Msg) tea.Msg {
	t.Helper()
	_, cmd := m.Update(message)
	var last tea.Msg
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil || (m.editing && !m.pending) {
			return
		}
		switch result := c().(type) {
		case tea.BatchMsg:
			for _, inner := range result {
				run(inner)
			}
		case nil:
		case tea.QuitMsg:
			last = result
		case presentationLoadedMsg, presentationSavedMsg, presentationHistoryMsg:
			last = result
			_, next := m.Update(result)
			run(next)
		}
	}
	run(cmd)
	return last
}

func newPresentationTestModel(t *testing.T) (*presentationModel, *presentation.Service, string) {
	t.Helper()
	sample, err := os.ReadFile("../../examples/presentation.md")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "deck.md")
	if err := os.WriteFile(path, sample, 0o600); err != nil {
		t.Fatal(err)
	}
	service := presentation.NewService(newTestRepository(t), 10)
	m := newPresentationModel(service, path)
	drivePresentation(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	drivePresentation(t, m, m.Init()())
	if !m.loaded || m.pending || m.errMessage != "" {
		t.Fatalf("model not loaded: loaded %t pending %t err %q", m.loaded, m.pending, m.errMessage)
	}
	return m, service, path
}

func storedPresentation(t *testing.T, path string) presentation.Presentation {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	item, err := presentation.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestPresentationViewNavigatesSlidesAndNotes(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	view := m.View()
	for _, want := range []string{"Product Update", "deck.md", "Parchment team", "Title page"} {
		if !strings.Contains(view, want) {
			t.Fatalf("title page missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "parchment-meta") || strings.Contains(view, "parchment-body") {
		t.Fatalf("view leaked envelope:\n%s", view)
	}
	drivePresentation(t, m, sheetKey("right"))
	view = m.View()
	if !strings.Contains(view, "What shipped") || !strings.Contains(view, "Slide 1 of") {
		t.Fatalf("first slide not shown:\n%s", view)
	}
	withNotes := -1
	for index, slide := range m.deck.Slides {
		if slide.Notes != "" {
			withNotes = index
			break
		}
	}
	if withNotes < 0 {
		t.Fatal("example presentation has no speaker notes")
	}
	for m.page < withNotes+1 {
		drivePresentation(t, m, sheetKey("n"))
	}
	if strings.Contains(m.View(), m.deck.Slides[withNotes].Notes) {
		t.Fatal("speaker notes shown before toggling")
	}
	drivePresentation(t, m, sheetKey("s"))
	if view := m.View(); !strings.Contains(view, "Speaker notes") ||
		!strings.Contains(view, m.deck.Slides[withNotes].Notes) {
		t.Fatalf("notes not shown:\n%s", view)
	}
	drivePresentation(t, m, sheetKey("end"))
	if m.page != len(m.deck.Slides) {
		t.Fatalf("end page = %d", m.page)
	}
	drivePresentation(t, m, sheetKey("right"))
	if m.page != len(m.deck.Slides) {
		t.Fatalf("navigation passed last slide: %d", m.page)
	}
	drivePresentation(t, m, sheetKey("home"))
	drivePresentation(t, m, sheetKey("left"))
	if m.page != 0 {
		t.Fatalf("navigation passed title page: %d", m.page)
	}
	if _, ok := drivePresentation(t, m, sheetKey("esc")).(tea.QuitMsg); !ok {
		t.Fatal("esc did not quit")
	}
}

func TestPresentationEditModeSeamlessUndoRedo(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	drivePresentation(t, m, sheetKey("e"))
	if !m.editing {
		t.Fatal("e did not start editing")
	}
	drivePresentation(t, m, sheetKey("!"))
	if m.editor.Value() == m.item.Source {
		t.Fatal("typing ! did not edit source")
	}

	// Press Ctrl+Z with unsaved edits - should undo seamlessly
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlZ})
	if m.editor.Value() != m.item.Source {
		t.Fatalf("after undo: source = %q, want %q", m.editor.Value(), m.item.Source)
	}

	// Press Ctrl+R with unsaved edits - should redo seamlessly
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if !strings.Contains(m.editor.Value(), "!") {
		t.Fatalf("after redo: source = %q, want containing !", m.editor.Value())
	}
}

func TestPresentationEditSavesSourceAndUndoes(t *testing.T) {
	m, _, path := newPresentationTestModel(t)
	original := m.item.Source
	drivePresentation(t, m, sheetKey("right"))
	drivePresentation(t, m, sheetKey("e"))
	if !m.editing {
		t.Fatal("e did not start editing")
	}
	if line := m.editor.Cursor.Line; line != slideSourceLine(original, 1) || line == 0 {
		t.Fatalf("cursor line = %d", line)
	}
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	// Typing q while editing inserts text rather than quitting.
	if msg := drivePresentation(t, m, sheetKey(" q")); msg != nil {
		if _, quit := msg.(tea.QuitMsg); quit {
			t.Fatal("q quit while editing")
		}
	}
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.editing || m.pending || m.errMessage != "" || m.status != "Saved" {
		t.Fatalf("save state: editing %t pending %t status %q err %q", m.editing, m.pending, m.status, m.errMessage)
	}
	saved := storedPresentation(t, path)
	if !strings.Contains(saved.Source, "## What shipped q\n") || m.page != 1 ||
		!strings.Contains(m.View(), "What shipped q") {
		t.Fatalf("saved source = %q\nview:\n%s", saved.Source, m.View())
	}
	drivePresentation(t, m, sheetKey("u"))
	if m.errMessage != "" || !strings.HasPrefix(m.status, "Undid") {
		t.Fatalf("undo status %q err %q", m.status, m.errMessage)
	}
	if storedPresentation(t, path).Source != original || m.item.Source != original {
		t.Fatal("undo did not restore original source")
	}
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if storedPresentation(t, path).Source != saved.Source || m.item.Source != saved.Source {
		t.Fatal("redo did not reapply edit")
	}
}

func TestPresentationSaveReturnsToSlideBeingEdited(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	original := m.item.Source
	drivePresentation(t, m, sheetKey("e"))
	if m.page != 0 {
		t.Fatal("test must begin on the title page")
	}
	m.moveEditorToLine(slideSourceLine(original, 3))
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	drivePresentation(t, m, sheetKey("!"))
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.editing || m.page != 3 {
		t.Fatalf("after save: editing %t, page %d, want page 3", m.editing, m.page)
	}
	drivePresentation(t, m, sheetKey("e"))
	m.moveEditorToLine(slideSourceLine(original, 1))
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.editing || m.page != 1 {
		t.Fatalf("after cancel: editing %t, page %d, want page 1", m.editing, m.page)
	}
}

func TestPresentationEditRejectsInvalidSourceAndConfirmsDiscard(t *testing.T) {
	m, _, path := newPresentationTestModel(t)
	original := m.item.Source
	drivePresentation(t, m, sheetKey("e"))
	m.editor.SetValue("not a presentation")
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.editing || m.pending || !strings.Contains(m.errMessage, "Invalid presentation") {
		t.Fatalf("invalid save state: editing %t pending %t err %q", m.editing, m.pending, m.errMessage)
	}
	if storedPresentation(t, path).Source != original {
		t.Fatal("invalid source was saved")
	}
	drivePresentation(t, m, sheetKey("esc"))
	if !m.editing || !m.discardWarning {
		t.Fatal("dirty esc did not warn")
	}
	drivePresentation(t, m, sheetKey("esc"))
	if m.editing || m.item.Source != original {
		t.Fatal("second esc did not discard draft")
	}
	drivePresentation(t, m, sheetKey("e"))
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.editing || m.status != "No changes" {
		t.Fatalf("unchanged save state: editing %t status %q", m.editing, m.status)
	}
}

func TestPresentationSaveErrorKeepsDraft(t *testing.T) {
	m, _, path := newPresentationTestModel(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	trailer := strings.Index(content, "<!-- parchment-blocks -->")
	if trailer < 0 {
		t.Fatal("presentation trailing-block boundary is missing")
	}
	content = content[:trailer] + "External\n\n" + content[trailer:]
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	drivePresentation(t, m, sheetKey("e"))
	draft := m.item.Source + "\nLocal edit\n"
	m.editor.SetValue(draft)
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !m.editing || m.pending || !strings.Contains(m.errMessage, "changed since it was loaded") {
		t.Fatalf("save error state: editing %t pending %t err %q", m.editing, m.pending, m.errMessage)
	}
	if m.editor.Value() != draft {
		t.Fatal("save failure lost the draft")
	}
	if strings.Contains(storedPresentation(t, path).Source, "Local edit") {
		t.Fatal("external edit was overwritten")
	}
}

func TestPresentationLoadErrorsAndStaleResults(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	m.path = filepath.Join(t.TempDir(), "missing.md")
	drivePresentation(t, m, sheetKey("r"))
	if m.errMessage != "Load failed: presentation not found" {
		t.Fatalf("missing load error = %q", m.errMessage)
	}
	cmd := m.load()
	drivePresentation(t, m, sheetKey("esc"))
	if m.pending || m.status != "Cancelled" {
		t.Fatalf("cancel state: pending %t status %q", m.pending, m.status)
	}
	before := m.item
	drivePresentation(t, m, cmd())
	if !presentation.Equal(before, m.item) || m.status != "Cancelled" {
		t.Fatal("stale load result was applied")
	}
	drivePresentation(t, m, presentationSavedMsg{seq: m.seq, err: context.Canceled})
	if m.errMessage != "" {
		t.Fatalf("cancelled save error shown: %q", m.errMessage)
	}
	m.pending = true
	drivePresentation(t, m, presentationHistoryMsg{seq: m.seq, err: errors.New("boom")})
	if m.errMessage != "History failed: boom" {
		t.Fatalf("history error = %q", m.errMessage)
	}
}

func TestPageForSourceLineAndSlideSourceLine(t *testing.T) {
	source := "# Presentation Title\n\nIntro text\n\n```markdown\n## Code block slide header inside fence\n```\n\n## Slide 1\nSlide 1 content\n\n## Slide 2\n~~~\n## Fence slide 2\n~~~\nSlide 2 content\n"

	if got := slideSourceLine(source, 0); got != 0 {
		t.Errorf("slideSourceLine(page 0) = %d, want 0", got)
	}
	if got := slideSourceLine(source, 1); got != 8 {
		t.Errorf("slideSourceLine(page 1) = %d, want 8", got)
	}
	if got := slideSourceLine(source, 2); got != 11 {
		t.Errorf("slideSourceLine(page 2) = %d, want 11", got)
	}
	if got := slideSourceLine(source, 99); got != 0 {
		t.Errorf("slideSourceLine(page 99) = %d, want 0", got)
	}

	tests := []struct {
		line int
		want int
	}{
		{-1, 0},
		{0, 0},
		{5, 0},
		{7, 0},
		{8, 1},
		{10, 1},
		{11, 2},
		{14, 2},
		{100, 2},
	}

	for _, tt := range tests {
		if got := pageForSourceLine(source, tt.line); got != tt.want {
			t.Errorf("pageForSourceLine(line %d) = %d, want %d", tt.line, got, tt.want)
		}
	}

	if got := slideSourceLine("", 0); got != 0 {
		t.Errorf("slideSourceLine(\"\", 0) = %d, want 0", got)
	}
	if got := pageForSourceLine("", 0); got != 0 {
		t.Errorf("pageForSourceLine(\"\", 0) = %d, want 0", got)
	}
	if got := pageForSourceLine("", 5); got != 0 {
		t.Errorf("pageForSourceLine(\"\", 5) = %d, want 0", got)
	}
}

func TestPageForSourceLineAndSlideSourceLineTrailingNewlines(t *testing.T) {
	source := "line 1\n## Slide 1\n"
	if got := slideSourceLine(source, 1); got != 1 {
		t.Errorf("slideSourceLine = %d, want 1", got)
	}

	if got := pageForSourceLine(source, 0); got != 0 {
		t.Errorf("pageForSourceLine(line 0) = %d, want 0", got)
	}
	if got := pageForSourceLine(source, 1); got != 1 {
		t.Errorf("pageForSourceLine(line 1) = %d, want 1", got)
	}
	if got := pageForSourceLine(source, 2); got != 1 {
		t.Errorf("pageForSourceLine(line 2) = %d, want 1", got)
	}

	multiline := "line 1\nline 2\n## Slide 1\n"
	splitLines := strings.Split(multiline, "\n")
	for i := range splitLines {
		want := 0
		if i >= 2 {
			want = 1
		}
		if got := pageForSourceLine(multiline, i); got != want {
			t.Errorf("pageForSourceLine(multiline, line %d) = %d, want %d", i, got, want)
		}
	}
}

func TestPresentationViewSmallTerminal(t *testing.T) {
	service := presentation.NewService(newTestRepository(t), 10)
	m := newPresentationModel(service, "deck.md")

	m.width, m.height = 19, 30
	if got := m.View(); got != "Terminal too small. Press q to quit." {
		t.Fatalf("small width view = %q, want 'Terminal too small. Press q to quit.'", got)
	}

	m.width, m.height = 80, 4
	if got := m.View(); got != "Terminal too small. Press q to quit." {
		t.Fatalf("small height view = %q, want 'Terminal too small. Press q to quit.'", got)
	}
}

func TestPresentationViewUnloadedAndErrorStates(t *testing.T) {
	service := presentation.NewService(newTestRepository(t), 10)
	m := newPresentationModel(service, "deck.md")
	m.width, m.height = 80, 20

	view := m.View()
	if !strings.Contains(view, "Presentation") || !strings.Contains(view, "deck.md") || !strings.Contains(view, "r retry • esc/q quit") {
		t.Fatalf("unloaded view missing expected strings:\n%s", view)
	}

	m.errMessage = "Failed to load"
	view = m.View()
	if !strings.Contains(view, "Error: Failed to load") {
		t.Fatalf("unloaded view missing error message:\n%s", view)
	}

	// Model without path
	mNoPath := newPresentationModel(service, "")
	mNoPath.width, mNoPath.height = 80, 20
	if viewNoPath := mNoPath.View(); !strings.HasPrefix(viewNoPath, "Presentation\n") {
		t.Fatalf("unloaded model without path view wrong:\n%s", viewNoPath)
	}
}

func TestPresentationViewEditMode(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	drivePresentation(t, m, sheetKey("e"))
	if !m.editing {
		t.Fatal("expected model to be in editing mode")
	}

	view := m.View()
	if !strings.Contains(view, "— editing source") {
		t.Fatalf("edit mode header missing '— editing source':\n%s", view)
	}
	if !strings.Contains(view, "ctrl+s save • esc cancel • ctrl+c quit") {
		t.Fatalf("edit mode help text missing:\n%s", view)
	}
}

func TestPresentationViewBrowseModeAndScrolling(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	m.width, m.height = 80, 10 // small height so visibleLines is small

	// Title page (page 0)
	view := m.View()
	slideCount := len(m.deck.Slides)
	wantTitleCounter := fmt.Sprintf("0/%d", slideCount)
	wantTitleStatus := fmt.Sprintf("Title page • %d slides", slideCount)
	if !strings.Contains(view, wantTitleCounter) || !strings.Contains(view, wantTitleStatus) {
		t.Fatalf("title page status missing expected strings:\n%s", view)
	}

	// Go to Slide 1
	drivePresentation(t, m, sheetKey("right"))
	view = m.View()
	wantSlideCounter := fmt.Sprintf("1/%d", slideCount)
	wantSlideStatus := fmt.Sprintf("Slide 1 of %d", slideCount)
	if !strings.Contains(view, wantSlideCounter) || !strings.Contains(view, wantSlideStatus) {
		t.Fatalf("slide 1 status missing expected strings:\n%s", view)
	}

	// Scroll down
	drivePresentation(t, m, sheetKey("down"))
	viewScrolled := m.View()
	if viewScrolled == view {
		t.Fatalf("scrolling down did not alter View() output")
	}

	// Custom status overrides default slide status
	m.status = "Saved"
	if view := m.View(); !strings.Contains(view, "Saved") || strings.Contains(view, "Slide 1 of 2") {
		t.Fatalf("custom status 'Saved' not displayed properly:\n%s", view)
	}

	// Error message overrides status
	m.errMessage = "Something went wrong"
	if view := m.View(); !strings.Contains(view, "Error: Something went wrong") {
		t.Fatalf("error message not displayed properly in view:\n%s", view)
	}
}

func TestPresentationUpdateMouseAndGenericMessages(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)

	// In browse mode: MouseMsg and generic msg return (m, nil)
	mouseMsg := tea.MouseMsg{X: 10, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	updatedModel, cmd := m.Update(mouseMsg)
	if updatedModel != m || cmd != nil {
		t.Fatalf("browse mode MouseMsg returned model %v, cmd %v", updatedModel, cmd)
	}

	type dummyMsg struct{}
	updatedModel, cmd = m.Update(dummyMsg{})
	if updatedModel != m || cmd != nil {
		t.Fatalf("browse mode generic msg returned model %v, cmd %v", updatedModel, cmd)
	}

	// In edit mode: MouseMsg and generic msg are forwarded to m.editor
	drivePresentation(t, m, sheetKey("e"))
	if !m.editing {
		t.Fatal("e did not enter edit mode")
	}

	updatedModel, _ = m.Update(mouseMsg)
	if updatedModel != m {
		t.Fatal("edit mode MouseMsg did not return model")
	}

	updatedModel, _ = m.Update(dummyMsg{})
	if updatedModel != m {
		t.Fatal("edit mode generic msg did not return model")
	}
}

func TestPresentationUpdateStaleAndPendingOperations(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)

	// Stale presentationSavedMsg and presentationHistoryMsg (msg.seq != m.seq)
	m.seq = 5
	updatedModel, cmd := m.Update(presentationSavedMsg{seq: 1, err: nil})
	if updatedModel != m || cmd != nil {
		t.Fatalf("stale presentationSavedMsg returned model %v, cmd %v", updatedModel, cmd)
	}

	updatedModel, cmd = m.Update(presentationHistoryMsg{seq: 1, err: nil})
	if updatedModel != m || cmd != nil {
		t.Fatalf("stale presentationHistoryMsg returned model %v, cmd %v", updatedModel, cmd)
	}

	// Pending state in browse mode: key press sets "Working… (esc cancels)"
	m.pending = true
	m.Update(sheetKey("x"))
	if m.status != "Working… (esc cancels)" {
		t.Fatalf("status = %q, want 'Working… (esc cancels)'", m.status)
	}
	m.cancelPending()

	// Pending state in edit mode: esc cancels, other key shows "Saving… (esc cancels)"
	drivePresentation(t, m, sheetKey("e"))
	m.pending = true
	m.Update(sheetKey("x"))
	if m.status != "Saving… (esc cancels)" {
		t.Fatalf("status = %q, want 'Saving… (esc cancels)'", m.status)
	}

	m.Update(sheetKey("esc"))
	if m.pending || m.status != "Save cancelled" {
		t.Fatalf("after esc while pending in edit mode: pending %t, status %q", m.pending, m.status)
	}
}

func TestPresentationUpdateEditKeysAndSelection(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)
	drivePresentation(t, m, sheetKey("e"))

	// Ctrl+C with active selection forwards to editor instead of quitting
	m.editor.Selection.Anchor = m.editor.Cursor
	m.editor.Selection.Active = true
	msg := drivePresentation(t, m, sheetKey("ctrl+c"))
	if _, quit := msg.(tea.QuitMsg); quit {
		t.Fatal("ctrl+c with active selection quit the app")
	}

	// Ctrl+Y and Ctrl+Shift+Z when editor can redo
	m.editor.Selection.Active = false
	drivePresentation(t, m, sheetKey("a"))
	m.editor.Undo()
	drivePresentation(t, m, sheetKey("ctrl+y"))
	if !strings.Contains(m.editor.Value(), "a") {
		t.Fatalf("after ctrl+y redo: source = %q", m.editor.Value())
	}
	m.editor.Undo()
	drivePresentation(t, m, sheetKey("ctrl+shift+z"))
	if !strings.Contains(m.editor.Value(), "a") {
		t.Fatalf("after ctrl+shift+z redo: source = %q", m.editor.Value())
	}

	// Ctrl+Z / Ctrl+R when editor cannot undo/redo triggers backend service history
	m.editor.SetValue(m.item.Source) // Fresh editor state with empty undo/redo stacks
	m.Update(sheetKey("ctrl+z"))
	if !m.pending {
		t.Fatal("ctrl+z when editor cannot undo did not trigger service undo")
	}
	m.cancelPending()

	m.Update(sheetKey("ctrl+r"))
	if !m.pending {
		t.Fatal("ctrl+r when editor cannot redo did not trigger service redo")
	}
	m.cancelPending()
}

func TestPresentationUpdateBrowseKeysAndNavigation(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)

	// Test key aliases in browse mode: 'n' / 'p', 'j' / 'k', 'g' / 'G', pgup / pgdown
	drivePresentation(t, m, sheetKey("n"))
	if m.page != 1 {
		t.Fatalf("after 'n': page = %d, want 1", m.page)
	}
	drivePresentation(t, m, sheetKey("p"))
	if m.page != 0 {
		t.Fatalf("after 'p': page = %d, want 0", m.page)
	}

	drivePresentation(t, m, sheetKey("n")) // go to slide 1
	m.height = 6                           // reduce height so visibleLines() = 1 and scroll limit > 0
	drivePresentation(t, m, sheetKey("j"))
	if m.scroll == 0 {
		t.Fatalf("after 'j': scroll = %d, want > 0", m.scroll)
	}
	drivePresentation(t, m, sheetKey("k"))
	if m.scroll != 0 {
		t.Fatalf("after 'k': scroll = %d, want 0", m.scroll)
	}
	m.height = 30 // restore height

	drivePresentation(t, m, sheetKey("G"))
	if m.page != len(m.deck.Slides) {
		t.Fatalf("after 'G': page = %d, want %d", m.page, len(m.deck.Slides))
	}
	drivePresentation(t, m, sheetKey("g"))
	if m.page != 0 {
		t.Fatalf("after 'g': page = %d, want 0", m.page)
	}

	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.page != 1 {
		t.Fatalf("after pgdown: page = %d, want 1", m.page)
	}
	drivePresentation(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.page != 0 {
		t.Fatalf("after pgup: page = %d, want 0", m.page)
	}

	// Redo key aliases in browse mode: ctrl+y and ctrl+shift+z
	m.Update(sheetKey("ctrl+y"))
	if !m.pending {
		t.Fatal("ctrl+y in browse mode did not trigger redo")
	}
	m.cancelPending()

	m.Update(sheetKey("ctrl+shift+z"))
	if !m.pending {
		t.Fatal("ctrl+shift+z in browse mode did not trigger redo")
	}
	m.cancelPending()

	// Keys when !m.loaded (other than 'r', 'esc', 'q', 'ctrl+c') are ignored
	m.loaded = false
	drivePresentation(t, m, sheetKey("n"))
	if m.page != 0 {
		t.Fatalf("key press when !m.loaded modified state: page = %d", m.page)
	}
}

func TestPresentationFencedCodeBlocksAndEmptyNotes(t *testing.T) {
	m, _, _ := newPresentationTestModel(t)

	// Source with fenced code blocks containing headings and slide with empty speaker notes
	sourceWithFence := "# Fenced Title\n\n```\n# Code Heading inside backtick fence\n## Code Slide Heading inside backtick fence\n```\n\n~~~\n# Code Heading inside tilde fence\n## Code Slide Heading inside tilde fence\n~~~\n\n## Real Slide 1\n\nBody of slide 1\n\n<!-- parchment-notes\n-->\n"
	m.setItem(presentation.Presentation{Source: sourceWithFence})

	if m.pageCount() != 2 {
		t.Fatalf("pageCount = %d, want 2 (fenced headings should be ignored)", m.pageCount())
	}

	line := slideSourceLine(sourceWithFence, 1)
	if line <= 0 {
		t.Fatalf("slideSourceLine for slide 1 = %d, want > 0", line)
	}

	page := pageForSourceLine(sourceWithFence, line)
	if page != 1 {
		t.Fatalf("pageForSourceLine = %d, want 1", page)
	}

	// Toggle speaker notes on slide with empty notes
	drivePresentation(t, m, sheetKey("n"))
	drivePresentation(t, m, sheetKey("s"))
	view := m.View()
	if !strings.Contains(view, "(none)") {
		t.Fatalf("empty speaker notes did not render '(none)':\n%s", view)
	}
}
