package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/workspace"
)

// drive sends a message and feeds the results of returned commands back into
// the model, as the Bubble Tea runtime would.
func drive(t *testing.T, m *Model, message tea.Msg) {
	t.Helper()
	_, cmd := m.Update(message)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch result := c().(type) {
		case tea.BatchMsg:
			for _, inner := range result {
				run(inner)
			}
		case nil:
		default:
			_, next := m.Update(result)
			run(next)
		}
	}
	run(cmd)
}

func typeText(t *testing.T, m *Model, text string) {
	t.Helper()
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
}

func newDocumentsModel(t *testing.T) (*Model, *document.Service) {
	t.Helper()
	ws := openTestWorkspace(t)
	docs := document.NewService(ws, 10)
	model := NewModel(note.NewService(ws, 10), ws, "test", "/tmp/test", WithDocuments(docs))
	m := &model
	drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	drive(t, m, notesLoadedMsg{})
	return m, docs
}

func TestInitialDocumentOptionOpensMatchingDocument(t *testing.T) {
	ws := openTestWorkspace(t)
	service := document.NewService(ws, 10)
	created, err := service.Create(context.Background(), document.Draft{Title: "Target", Body: "Target body"})
	if err != nil {
		t.Fatal(err)
	}

	model := NewModel(note.NewService(ws, 10), ws, "test", "/tmp/test",
		WithDocuments(service), WithInitialDocument(created.ID))
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = *updated.(*Model)
	message := model.Init()()
	updated, _ = model.Update(message)
	model = *updated.(*Model)
	selected, ok := model.documents.selectedDocument()
	if !model.documentsActive || !ok || selected.ID != created.ID || !model.documents.showPreview {
		t.Fatalf("initial document state = active %t, selected %q, preview %t",
			model.documentsActive, selected.ID, model.documents.showPreview)
	}
}

func TestStandaloneDocumentScreenCannotCreateOrDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "document.md")
	sample, err := os.ReadFile("../../examples/document.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, sample, 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := workspace.OpenDocumentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	items, err := docs.ListDocuments(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("list document = %v, %v", items, err)
	}
	notes, err := workspace.OpenMarkdownFile(path)
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(note.NewService(notes, 10), notes, "document.md", path,
		WithDocuments(document.NewService(docs, 10)), WithInitialDocument(items[0].ID), WithSingleDocumentFile())
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model.Update(model.Init()())
	if !model.documents.showPreview {
		t.Fatal("document did not open in reader view")
	}
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if model.documents.mode != documentBrowsing || model.documents.creating {
		t.Fatal("standalone document entered create mode")
	}
	model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if model.documents.confirmDelete {
		t.Fatal("standalone document entered delete confirmation")
	}
	_, quit := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	if quit == nil {
		t.Fatal("Tab did not exit standalone document")
	}
	if msg := quit(); msg != (tea.QuitMsg{}) {
		t.Fatalf("Tab returned %T, want quit", msg)
	}
}

func TestStandaloneDocumentPreviewNavigatesExamplePages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "document.md")
	sample, err := os.ReadFile("../../examples/document.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, sample, 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := workspace.OpenDocumentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	items, err := docs.ListDocuments(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("list document = %v, %v", items, err)
	}
	notes, err := workspace.OpenMarkdownFile(path)
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(note.NewService(notes, 10), notes, "document.md", path,
		WithDocuments(document.NewService(docs, 10)), WithInitialDocument(items[0].ID), WithSingleDocumentFile())
	m := &model
	drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	drive(t, m, m.Init()())
	s := m.documents
	if !s.showPreview || len(s.previewPages) < 3 {
		t.Fatalf("example preview = focused %t, %d pages", s.showPreview, len(s.previewPages))
	}
	if s.preview.AtBottom() {
		t.Fatal("first page is not scrollable at test size")
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if s.previewPage != 0 || s.preview.YOffset == 0 {
		t.Fatalf("scrolling inside first page: page %d, offset %d", s.previewPage, s.preview.YOffset)
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if s.previewPage != 1 || !s.preview.AtTop() {
		t.Fatalf("right arrow: page %d, top %t", s.previewPage, s.preview.AtTop())
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if s.previewPage != 2 {
		t.Fatalf("right arrow: page %d, want 2", s.previewPage)
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if s.previewPage != 1 {
		t.Fatalf("left arrow: page %d, want 1", s.previewPage)
	}
	s.preview.GotoBottom()
	drive(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if s.previewPage != 2 || !s.preview.AtTop() {
		t.Fatalf("page down at bottom: page %d, top %t", s.previewPage, s.preview.AtTop())
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if s.previewPage != 1 {
		t.Fatalf("page up at top: page %d, want 1", s.previewPage)
	}
	s.preview.GotoTop()
	drive(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if s.previewPage != 0 || !s.preview.AtBottom() {
		t.Fatalf("up at top: page %d, bottom %t", s.previewPage, s.preview.AtBottom())
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	if s.previewPage != 1 {
		t.Fatalf("] shortcut: page %d, want 1", s.previewPage)
	}
	s.preview.GotoBottom()
	drive(t, m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	if s.previewPage != 2 {
		t.Fatalf("wheel down at bottom: page %d, want 2", s.previewPage)
	}
	drive(t, m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if s.previewPage != 1 || !s.preview.AtBottom() {
		t.Fatalf("wheel up at top: page %d, bottom %t", s.previewPage, s.preview.AtBottom())
	}
}

func TestStandaloneDocumentEditorSavesBodyWithoutTitle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	sample, err := os.ReadFile("../../examples/document.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, sample, 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := workspace.OpenDocumentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	items, err := docs.ListDocuments(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("list document = %v, %v", items, err)
	}
	notes, err := workspace.OpenMarkdownFile(path)
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(note.NewService(notes, 10), notes, "report.md", path,
		WithDocuments(document.NewService(docs, 10)), WithInitialDocument(items[0].ID), WithSingleDocumentFile())
	m := &model
	drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	drive(t, m, m.Init()())
	drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if m.documents.mode != documentEditing || !m.documents.body.Focused() || strings.Contains(m.View(), "Title:") {
		t.Fatal("standalone editor did not focus the body without a title field")
	}
	m.documents.body.SetValue("# Updated report\n")
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	got, err := docs.GetDocument(context.Background(), items[0].ID)
	if err != nil || got.Body != "# Updated report\n" {
		t.Fatalf("saved document body = %q, %v", got.Body, err)
	}
}

func TestDocumentEditorHasToolbarAndSavesLayout(t *testing.T) {
	m, docs := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.documentsActive {
		t.Fatal("Tab did not open the documents screen")
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	view := m.View()
	for _, label := range []string{"Save", "Preview", "Bold", "H1", "Page break", "Section", "Cols: 1", "Margins: Normal", "Header", "Footer", "Image"} {
		if label == "Bold" {
			label = "B"
		}
		if !strings.Contains(view, label) {
			t.Fatalf("editor toolbar is missing %q:\n%s", label, view)
		}
	}
	typeText(t, m, "Intro")
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true})
	typeText(t, m, "bold")
	m.documents.act("pagebreak")
	m.documents.act("columns")
	if _, err := docs.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	list, err := docs.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("documents = %v, err = %v", list, err)
	}
	got := list[0]
	if got.Layout.Columns != 2 {
		t.Fatalf("saved document columns = %d", got.Layout.Columns)
	}
	if !strings.Contains(got.Body, "Intro**bold") || !strings.Contains(got.Body, document.PageBreakMarkup) {
		t.Fatalf("saved body = %q", got.Body)
	}
	if m.documents.mode != documentBrowsing {
		t.Fatal("editor did not close after saving")
	}
}

func TestDocumentTUIRecordsReviewsAndAcceptsProposal(t *testing.T) {
	m, docs := newDocumentsModel(t)
	created, err := docs.Create(context.Background(), document.Draft{
		Title: "Original", Body: "before", Layout: document.DefaultLayout(),
	})
	if err != nil {
		t.Fatal(err)
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	s := m.documents
	if s.mode != documentEditing || !s.proposing {
		t.Fatal("c did not open the proposal editor")
	}
	s.body.SetValue("after")
	drive(t, m, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !s.showChanges || len(s.changes) != 1 || s.changes[0].Status != document.ChangePending {
		t.Fatalf("proposal was not recorded for review: %+v", s.changes)
	}
	live, err := docs.Get(context.Background(), created.ID)
	if err != nil || live.Body != "before" {
		t.Fatalf("proposal modified the live document: %+v, %v", live, err)
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !s.reviewing || !strings.Contains(m.View(), "Proposed Markdown") {
		t.Fatal("proposal review did not display its before/after content")
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	accepted, err := docs.Get(context.Background(), created.ID)
	if err != nil || accepted.Body != "after" {
		t.Fatalf("accepted document = %+v, %v", accepted, err)
	}
	if len(s.changes) != 1 || s.changes[0].Status != document.ChangeAccepted {
		t.Fatalf("accepted proposal state = %+v", s.changes)
	}
}

func TestDocumentChangeReviewScrollsAndBoundsChangeList(t *testing.T) {
	m, docs := newDocumentsModel(t)
	ctx := context.Background()
	created, err := docs.Create(ctx, document.Draft{
		Title: "Long", Body: strings.Repeat("current line\n", 80) + "CURRENT-LAST",
	})
	if err != nil {
		t.Fatal(err)
	}
	change, err := docs.Propose(ctx, created, "Long proposal", document.Draft{
		Title: "Long", Body: strings.Repeat("proposed line\n", 80) + "PROPOSED-LAST",
		Layout: created.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.documentsActive = true
	s := m.documents
	s.changes, s.selectedChange, s.showChanges, s.reviewing = []document.Change{change}, 0, true, true
	s.resize(100, 12)
	s.setChangeReviewContent()
	view := m.View()
	if strings.Contains(view, "CURRENT-LAST") || strings.Contains(view, "PROPOSED-LAST") {
		t.Fatalf("review unexpectedly showed content beyond the initial viewport:\n%s", view)
	}
	sawCurrent, sawProposed := false, false
	for i := 0; i < 100 && !(sawCurrent && sawProposed); i++ {
		drive(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
		view = m.View()
		sawCurrent = sawCurrent || strings.Contains(view, "CURRENT-LAST")
		sawProposed = sawProposed || strings.Contains(view, "PROPOSED-LAST")
	}
	if !sawCurrent || !sawProposed {
		t.Fatalf("scrolling did not reveal both document bodies")
	}
	if !strings.Contains(view, "a accept") || !strings.Contains(view, "Esc return") {
		t.Fatalf("review controls disappeared while scrolling:\n%s", view)
	}
	for _, label := range []string{"Header:", "Footer:", "Page numbers:"} {
		if !strings.Contains(changeLayoutDescription(change.Before.Layout), label) {
			t.Errorf("review omitted %q layout details", label)
		}
	}

	s.reviewing = false
	s.changes = make([]document.Change, 20)
	for i := range s.changes {
		s.changes[i].Description = fmt.Sprintf("Change %02d", i)
	}
	s.selectedChange = len(s.changes) - 1
	view = s.viewChanges("header")
	if !strings.Contains(view, "Change 19") || strings.Contains(view, "Change 00") {
		t.Fatalf("change list was not bounded around the selection:\n%s", view)
	}
}

func TestDocumentChangeReviewReflowsLongLinesOnResize(t *testing.T) {
	m, docs := newDocumentsModel(t)
	ctx := context.Background()
	created, err := docs.Create(ctx, document.Draft{
		Title: "Long", Body: strings.Repeat("c", 90) + "CURRENT-END",
	})
	if err != nil {
		t.Fatal(err)
	}
	change, err := docs.Propose(ctx, created, "Long proposal", document.Draft{
		Title: "Long", Body: strings.Repeat("p", 90) + "PROPOSED-END",
		Layout: created.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := m.documents
	s.changes, s.selectedChange, s.showChanges, s.reviewing = []document.Change{change}, 0, true, true
	s.resize(100, 12)
	wideLineCount := s.changeReview.TotalLineCount()
	s.resize(40, 12)
	if s.changeReview.TotalLineCount() <= wideLineCount {
		t.Fatalf("narrow review did not reflow long lines: wide=%d narrow=%d",
			wideLineCount, s.changeReview.TotalLineCount())
	}
	var view string
	sawCurrent, sawProposed := false, false
	for i := 0; i < s.changeReview.TotalLineCount() && !(sawCurrent && sawProposed); i++ {
		s.changeReview.SetYOffset(i)
		view = s.changeReview.View()
		sawCurrent = sawCurrent || strings.Contains(view, "CURRENT-END")
		sawProposed = sawProposed || strings.Contains(view, "PROPOSED-END")
	}
	if !sawCurrent || !sawProposed {
		t.Fatalf("reflowed review did not make the ends of both long lines inspectable: offset=%d lines=%d view=%q",
			s.changeReview.YOffset, s.changeReview.TotalLineCount(), view)
	}
	review := s.viewChanges("header")
	if !strings.Contains(review, "a accept") || !strings.Contains(review, "Esc return") {
		t.Fatal("review controls disappeared after reflow")
	}
}

func TestToolbarMouseClickAndUnsavedEscape(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	s := m.documents
	placed, _ := s.buttonLayout(s.width)
	buttons := s.toolbarButtons()
	for _, p := range placed {
		if buttons[p.index].action == "columns" {
			drive(t, m, tea.MouseMsg{X: p.x + 1, Y: toolbarTop + p.row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		}
	}
	if s.layout.Columns != 2 {
		t.Fatalf("clicking the columns button left %d columns", s.layout.Columns)
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if s.mode != documentEditing {
		t.Fatal("Esc discarded unsaved layout changes without confirmation")
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if s.mode != documentBrowsing {
		t.Fatal("second Esc did not close the editor")
	}
}

func TestDocumentsTabReturnsToNotes(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.documentsActive {
		t.Fatal("Tab did not return to notes")
	}
}

func TestDocumentEditorStartsInBodyAndFormatsText(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if !m.documents.body.Focused() {
		t.Fatal("document editor did not focus body on open")
	}
	if strings.Contains(m.View(), "Title:") {
		t.Fatal("document editor still displays a title field")
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.documents.focus != focusToolbar {
		t.Fatal("Tab did not focus the toolbar from the body")
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.documents.body.Focused() {
		t.Fatal("Tab did not return to the body from the toolbar")
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true})
	typeText(t, m, "x")
	s := m.documents
	if !s.body.Focused() {
		t.Fatal("formatting did not focus the body")
	}
	if s.body.Value() != "**x**" {
		t.Fatalf("body = %q", s.body.Value())
	}
}

func TestSectionPromptRequiresWholeValue(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	s := m.documents
	for _, value := range []string{"2junk", "2.5", "2cc", "5", "0"} {
		s.finishPrompt(promptSection, value)
		if s.body.Value() != "" || s.errMessage == "" {
			t.Fatalf("section value %q accepted: body %q", value, s.body.Value())
		}
	}
	s.finishPrompt(promptSection, "2c")
	if !strings.Contains(s.body.Value(), document.SectionBreakMarkup(2, true)) {
		t.Fatalf("valid section value rejected: %q", s.body.Value())
	}
}

func TestSmallTerminalQDoesNotQuitEditor(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	typeText(t, m, "Draft")
	_, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	cmd, _ := m.documents.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q quit the editor with unsaved changes")
		}
	}
	if m.documents.mode != documentEditing {
		t.Fatal("editor closed")
	}
}

func TestCancelledImageLoadIsDiscarded(t *testing.T) {
	m, _ := newDocumentsModel(t)
	drive(t, m, tea.KeyMsg{Type: tea.KeyTab})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := t.TempDir() + "/pixel.png"
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg := readImageCommand(context.Background(), path)().(imageLoadedMsg); msg.err != nil {
		t.Fatalf("valid image failed to load: %v", msg.err)
	}
	msg := readImageCommand(ctx, path)()
	m.documents.pending = true
	m.documents.update(msg)
	if len(m.documents.images) != 0 || m.documents.body.Value() != "" || m.documents.pending {
		t.Fatal("cancelled image load changed the document")
	}
	if !strings.Contains(m.documents.status, "cancelled") {
		t.Fatalf("status = %q", m.documents.status)
	}
}

func TestEditorRefusesDocumentItCannotPreserve(t *testing.T) {
	m, docs := newDocumentsModel(t)
	long := strings.Repeat("t", 5_000_001)
	created, err := docs.Create(context.Background(), document.Draft{Title: "Long document", Body: long})
	if err != nil {
		t.Fatal(err)
	}
	s := m.documents
	if command := s.startEdit(created); command != nil {
		t.Fatal("editor accepted oversized body")
	}
	if s.mode != documentBrowsing || !strings.Contains(s.errMessage, "exceeds editor limits") {
		t.Fatalf("mode = %d, error = %q", s.mode, s.errMessage)
	}
	stored, err := docs.Get(context.Background(), created.ID)
	if err != nil || stored.Body != long {
		t.Fatalf("stored body changed: length %d, %v", len(stored.Body), err)
	}
}

func TestDocumentOutlineNavigation(t *testing.T) {
	ws := openTestWorkspace(t)
	service := document.NewService(ws, 10)
	var body strings.Builder
	body.WriteString("# Intro\n\nhello\n\n")
	for i := 0; i < 90; i++ {
		body.WriteString("filler line\n\n")
	}
	body.WriteString("## Middle\n\nmore\n\n<!-- parchment:page-break -->\n\n## End\n\ndone\n")
	created, err := service.Create(context.Background(), document.Draft{Title: "Doc", Body: body.String()})
	if err != nil {
		t.Fatal(err)
	}
	model := NewModel(note.NewService(ws, 10), ws, "test", "/tmp/test",
		WithDocuments(service), WithInitialDocument(created.ID), WithSingleDocumentFile())
	m := &model
	drive(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	drive(t, m, m.Init()())
	s := m.documents
	if len(s.outline) != 3 {
		t.Fatalf("outline = %+v", s.outline)
	}
	if view := m.View(); !strings.Contains(view, "Outline") || !strings.Contains(view, "Middle") || strings.Contains(view, "Documents") {
		t.Fatalf("view lacks outline pane:\n%s", view)
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("}")})
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("}")})
	if s.currentSection() != 2 || s.previewPage != s.outline[2].Page-1 || s.previewPage == 0 {
		t.Fatalf("after }}: section %d page %d", s.currentSection(), s.previewPage)
	}
	drive(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("{")})
	if s.currentSection() != 1 {
		t.Fatalf("after {: section %d", s.currentSection())
	}
	_, targets := s.sidebar()
	for row, target := range targets {
		if target == 0 {
			drive(t, m, tea.MouseMsg{X: 3, Y: 2 + row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		}
	}
	if s.previewPage != 0 || s.currentSection() != 0 {
		t.Fatalf("click Intro: section %d page %d", s.currentSection(), s.previewPage)
	}
}
