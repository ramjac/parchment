package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/pdfrender"
	"example.com/parchment/internal/recovery"
	"example.com/parchment/internal/tui/editor"
)

// newDocumentHarness opens the editor on a document file, creating it with
// draft first when draft has a body.
func newDocumentHarness(t *testing.T, draft document.Draft, undoLimits ...int) *harness {
	t.Helper()
	undoLimit := 10
	if len(undoLimits) > 0 {
		undoLimit = undoLimits[0]
	}
	h := newHarnessWithUndoLimit(t, "", undoLimit)
	if draft.Body != "" {
		if _, err := h.docs.Create(context.Background(), h.path, draft); err != nil {
			t.Fatal(err)
		}
	}
	h.open(artifact.DocumentKind)
	if h.m.stage != stageDocument {
		t.Fatalf("stage = %d, error = %q", h.m.stage, h.m.errMessage)
	}
	if draft.Body != "" {
		if !h.m.documents.reading {
			t.Fatal("an existing document did not open in the reader")
		}
		h.key("e")
	}
	if h.m.documents.reading {
		t.Fatalf("document did not enter the editor: %q", h.m.documents.errMessage)
	}
	return h
}

func TestDocumentEditModeSeamlessUndoRedo(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	s := h.m.documents
	h.typeText("IntroText")
	if !s.dirty() || s.body.Value() != "IntroText" {
		t.Fatalf("dirty=%t body=%q", s.dirty(), s.body.Value())
	}

	// Press Ctrl+Z with unsaved edits - should undo seamlessly
	h.key("ctrl+z")
	if s.errMessage != "" || s.status == "Save or discard your edits before undo or redo" {
		t.Fatalf("unexpected status %q or error %q", s.status, s.errMessage)
	}
	if s.body.Value() != "" {
		t.Fatalf("after undo: body = %q, want empty", s.body.Value())
	}

	// Press Ctrl+R with unsaved edits - should redo seamlessly
	h.key("ctrl+r")
	if s.body.Value() != "IntroText" {
		t.Fatalf("after redo: body = %q, want IntroText", s.body.Value())
	}
}

func TestDocumentEditorPastesClipboardText(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "before"})
	h.send(tea.KeyPressMsg{Code: tea.KeyEnd})
	h.send(tea.PasteMsg{Content: " after"})
	if got := h.m.documents.body.Value(); got != "before after" {
		t.Fatalf("pasted document body = %q", got)
	}
}

func TestDocumentEditorFindBarUsesSharedMatchHighlighting(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "alpha ALPHA alpha"})
	s := h.m.documents
	h.send(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	h.typeText("ALPHA")
	if !s.find.active || s.body.MatchCount() != 1 || s.body.Cursor.Col != 6 ||
		!strings.Contains(s.find.view(s.body.MatchCount(), s.body.ActiveMatch(), s.width), "1/1") {
		t.Fatalf("document editor search: active=%t matches=%d cursor=%+v",
			s.find.active, s.body.MatchCount(), s.body.Cursor)
	}
	h.send(tea.KeyPressMsg{Code: tea.KeyEsc})
	if s.find.active || s.body.MatchCount() != 0 || !s.body.Focused || s.body.Cursor.Col != 6 {
		t.Fatalf("closing document search did not preserve the current match: active=%t matches=%d focused=%t cursor=%+v",
			s.find.active, s.body.MatchCount(), s.body.Focused, s.body.Cursor)
	}
}

func TestDocumentEditorHasToolbarAndSavesLayout(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	view := h.m.View().Content
	for _, label := range []string{"Save", "PDF", "Tracking", "Changes", "Preview", "B", "H1", "Page break", "Section", "Cols: 1", "Margins: Normal", "Header", "Footer", "Image"} {
		if !strings.Contains(view, label) {
			t.Fatalf("editor toolbar is missing %q:\n%s", label, view)
		}
	}
	s := h.m.documents
	h.typeText("Intro")
	h.send(tea.KeyPressMsg{Code: []rune(string("b"))[0], Mod: tea.ModAlt})
	h.typeText("bold")
	s.act("pagebreak")
	s.act("columns")
	h.key("ctrl+s")
	got, err := h.docs.Get(context.Background(), h.path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "report" || got.Layout.Columns != 2 {
		t.Fatalf("saved document = %q columns %d", got.Title, got.Layout.Columns)
	}
	if !strings.Contains(got.Body, "Intro**bold") || !strings.Contains(got.Body, document.PageBreakMarkup) {
		t.Fatalf("saved body = %q", got.Body)
	}
	if h.m.stage != stageDocument || s.dirty() || h.quit {
		t.Fatalf("after save: stage=%d dirty=%t quit=%t", h.m.stage, s.dirty(), h.quit)
	}
}

func TestOpeningDocumentRefreshesPersistentHistoryAfterLoad(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "before"})
	current, err := h.docs.Get(context.Background(), h.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.docs.Save(context.Background(), current, document.Draft{Body: "after", Layout: current.Layout}); err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(t.TempDir(), "other-document.md")
	if _, err := h.docs.Create(context.Background(), otherPath, document.Draft{Body: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.docs.Get(context.Background(), otherPath); err != nil {
		t.Fatal(err)
	}

	h.open(artifact.DocumentKind)
	if !h.m.documents.canUndo {
		t.Fatal("opening the edited document did not refresh persistent undo availability")
	}
}

func TestDocumentSaveHandsOffUndoAndPreservesHistoryOnFailure(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "left\tright"})
	s := h.m.documents
	if got := s.body.Value(); got != "left\tright" {
		t.Fatalf("document body = %q, want literal tab preserved", got)
	}
	h.typeText("!")
	h.key("ctrl+s")
	if s.body.CanUndo() || s.body.CanRedo() {
		t.Fatal("successful document save retained keystroke history")
	}
	h.key("ctrl+z")
	if got := s.body.Value(); got != "left\tright" {
		t.Fatalf("Ctrl+Z after save = %q, want service-level undo to restore prior body", got)
	}

	failed := newDocumentHarness(t, document.Draft{Body: "body"})
	failed.typeText("!")
	unsaved := failed.m.documents.body.Value()
	failed.m.documents.body.Cursor = editor.Point{Line: 0, Col: 2}
	failed.m.documents.body.Selection = editor.Selection{Anchor: editor.Point{Line: 0, Col: 1}, Active: true}
	failed.m.documents.documentSaved(documentSavedMsg{err: errors.New("save failed")})
	if !failed.m.documents.body.CanUndo() || failed.m.documents.body.Value() != unsaved ||
		failed.m.documents.body.Cursor != (editor.Point{Line: 0, Col: 2}) ||
		failed.m.documents.body.Selection != (editor.Selection{Anchor: editor.Point{Line: 0, Col: 1}, Active: true}) {
		t.Fatal("failed document save changed the unsaved draft or editor state")
	}
}

func TestDocumentSavePreservesEditorViewState(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	s := h.m.documents
	s.body.SetValue(strings.Repeat("a line of content\n", 30))
	wantCursor := editor.Point{Line: 20, Col: 4}
	wantSelection := editor.Selection{
		Anchor: editor.Point{Line: 19, Col: 1},
		Active: true,
	}
	s.body.Cursor = wantCursor
	s.body.Selection = wantSelection
	s.body.TopLine = 12
	s.body.TopRowOffset = 0
	s.body.Focus()

	h.key("ctrl+s")

	if s.dirty() || s.body.Value() != strings.Repeat("a line of content\n", 30) {
		t.Fatal("successful save did not preserve the saved editor content")
	}
	if s.body.Cursor != wantCursor ||
		s.body.Selection != wantSelection ||
		s.body.TopLine != 12 ||
		s.body.TopRowOffset != 0 ||
		!s.body.Focused {
		t.Fatalf("save changed editor view state: cursor=%+v selection=%+v top=%d/%d focused=%t",
			s.body.Cursor, s.body.Selection, s.body.TopLine, s.body.TopRowOffset, s.body.Focused)
	}

	h.key("f5")
	h.key("f5")
	if s.body.Cursor != wantCursor ||
		s.body.Selection != wantSelection ||
		s.body.TopLine != 12 ||
		s.body.TopRowOffset != 0 ||
		!s.body.Focused {
		t.Fatalf("preview transition changed editor view state: cursor=%+v selection=%+v top=%d/%d focused=%t",
			s.body.Cursor, s.body.Selection, s.body.TopLine, s.body.TopRowOffset, s.body.Focused)
	}
}

func TestDocumentPDFExportFromEditorPreservesUnsavedDraft(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "Saved body"})
	s := h.m.documents
	h.typeText(" with unsaved changes")
	output := filepath.Join(t.TempDir(), "editor.pdf")
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if s.prompt != promptPDF {
		t.Fatal("Ctrl+P did not open the PDF path prompt")
	}
	s.promptInput.SetValue(output)
	h.key("enter")

	pdf, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("export does not have a PDF signature: %q", pdf[:min(len(pdf), 8)])
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export permissions = %v; want 0600", info.Mode().Perm())
	}
	var expected bytes.Buffer
	if err := pdfrender.Render(&expected, s.editorDocument()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pdf, expected.Bytes()) {
		t.Fatal("PDF export did not render the current unsaved editor state")
	}
	if !s.dirty() || !strings.Contains(s.body.Value(), "with unsaved changes") {
		t.Fatalf("PDF export changed the unsaved draft: dirty=%t body=%q", s.dirty(), s.body.Value())
	}
	saved, err := h.docs.Get(context.Background(), h.path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Body != "Saved body" {
		t.Fatalf("PDF export saved editor changes: %q", saved.Body)
	}

	h.key("f5")
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	previewOutput := filepath.Join(t.TempDir(), "preview.pdf")
	s.promptInput.SetValue(previewOutput)
	if !strings.Contains(h.m.View().Content, "PDF output path:") {
		t.Fatal("print preview did not display the PDF path prompt")
	}
	h.key("enter")
	previewPDF, err := os.ReadFile(previewOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(previewPDF, []byte("%PDF-")) {
		t.Fatalf("preview export does not have a PDF signature: %q", previewPDF[:min(len(previewPDF), 8)])
	}
	if !strings.Contains(h.m.View().Content, "PDF exported to "+previewOutput) {
		t.Fatal("print preview did not display the PDF export success status")
	}
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	s.promptInput.SetValue(previewOutput)
	h.key("enter")
	if s.prompt != promptPDFOverwrite || !strings.Contains(h.m.View().Content, "Overwrite?") {
		t.Fatal("print preview did not display the overwrite confirmation")
	}
	h.key("esc")
}

func TestDocumentPDFExportWarnsForUnsavedTrackedEdit(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "Saved body"})
	s := h.m.documents
	h.key("f3")
	if !s.snapshot.Tracking {
		t.Fatal("tracking was not enabled")
	}
	h.typeText(" with unsaved changes")
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	s.promptInput.SetValue(filepath.Join(t.TempDir(), "tracked.pdf"))
	h.key("enter")
	if !strings.Contains(s.status, "pending tracked changes not exported") {
		t.Fatalf("missing tracking warning: %q", s.status)
	}
}

func TestDocumentPDFExportFromReaderAndConfirmsOverwrite(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "# Reader PDF"})
	s := h.m.documents
	s.startReading(s.snapshot)
	output := filepath.Join(t.TempDir(), "reader.pdf")
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if s.prompt != promptPDF {
		t.Fatal("Ctrl+P did not open the PDF path prompt in the reader")
	}
	s.promptInput.SetValue(output)
	h.key("enter")
	pdf, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("export does not have a PDF signature: %q", pdf[:min(len(pdf), 8)])
	}

	h.open(artifact.DocumentKind)
	s = h.m.documents
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if got := s.promptInput.Value(); got != output {
		t.Fatalf("remembered PDF path = %q, want %q", got, output)
	}
	h.key("enter")
	if s.prompt != promptPDFOverwrite || !strings.Contains(h.m.View().Content, "Overwrite?") {
		t.Fatalf("missing overwrite confirmation: %s", h.m.View().Content)
	}
	h.key("enter")
	after, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, pdf) {
		t.Fatal("refused overwrite changed the existing PDF")
	}

	if err := os.WriteFile(output, []byte("old PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	h.key("enter")
	h.key("y")
	after, err = os.ReadFile(output)
	if err != nil || !bytes.HasPrefix(after, []byte("%PDF-")) {
		t.Fatalf("confirmed overwrite did not create PDF: %v", err)
	}
	if !strings.Contains(s.status, "PDF exported") {
		t.Fatalf("status after confirmed overwrite = %q", s.status)
	}
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	s.promptInput.SetValue(filepath.Join(t.TempDir(), "missing", "failed.pdf"))
	h.key("enter")
	if !strings.Contains(s.errMessage, "PDF export failed") {
		t.Fatalf("expected export failure, got %q", s.errMessage)
	}
	h.open(artifact.DocumentKind)
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if got := h.m.documents.promptInput.Value(); got != output {
		t.Fatalf("failed export changed remembered path to %q", got)
	}
}

func TestDocumentPDFExportCannotOverwriteSource(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "Source document"})
	s := h.m.documents
	before, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatal(err)
	}
	h.send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	s.promptInput.SetValue(h.path)
	h.key("enter")
	h.key("y")
	after, err := os.ReadFile(h.path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("source document was changed: %v", err)
	}
	if !strings.Contains(s.errMessage, "cannot overwrite the source file") {
		t.Fatalf("error = %q", s.errMessage)
	}
}

func TestDocumentEditorPaneAndMouseCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		width   int
		outline bool
	}{
		{"narrow", 60, false},
		{"wide", 100, false},
		{"outline", 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDocumentHarness(t, document.Draft{})
			h.send(tea.WindowSizeMsg{Width: tc.width, Height: 30})
			s := h.m.documents
			if tc.outline {
				h.key("ctrl+o")
			}
			left := 2
			if tc.outline {
				outlineWidth, _ := s.readerPaneWidths()
				left += outlineWidth
			}
			width := tc.width - 2 - left
			if s.body.Width != width {
				t.Fatalf("editor width = %d, want %d", s.body.Width, width)
			}
			s.body.SetValue(strings.Repeat("a", width) + "bcdef")
			y := toolbarTop + s.toolbarRows() + 1
			lines := strings.Split(ansi.Strip(h.m.View().Content), "\n")
			for row, line := range lines {
				if got := ansi.StringWidth(line); got > tc.width {
					t.Fatalf("row %d overflows terminal: width=%d line=%q", row, got, line)
				}
			}
			for row := y - 1; row <= y+2; row++ {
				if got := ansi.StringWidth(lines[row]); got != tc.width {
					t.Fatalf("row %d width = %d, want %d: %q", row, got, tc.width, lines[row])
				}
			}
			if !strings.HasPrefix(lines[y], "│") || !strings.HasSuffix(lines[y], "│") ||
				!strings.Contains(lines[y+1], "bcdef") {
				t.Fatalf("body not wrapped within the pane:\n%s\n%s", lines[y], lines[y+1])
			}
			s.focusToolbar()
			h.send(mouseEvent{X: left + 2, Y: y + 1, Action: mousePress, Button: mouseLeft})
			if s.body.Cursor != (editor.Point{Line: 0, Col: width + 2}) || s.focus != focusBody {
				t.Fatalf("wrapped click: cursor=%+v focus=%v", s.body.Cursor, s.focus)
			}
			h.send(mouseEvent{X: left + 4, Y: y + 1, Action: mouseMotion, Button: mouseLeft})
			h.send(mouseEvent{X: left + 4, Y: y + 1, Action: mouseRelease, Button: mouseLeft})
			if got := s.body.SelectedText(); got != "de" {
				t.Fatalf("drag selected %q, want %q", got, "de")
			}
			h.send(mouseEvent{X: tc.width - 1, Y: y, Action: mousePress, Button: mouseLeft})
			if s.body.Cursor != (editor.Point{Line: 0, Col: width + 4}) {
				t.Fatalf("border click moved cursor: %+v", s.body.Cursor)
			}
		})
	}
}

func TestDocumentEditorFrameAndClickAfterResize(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	s := h.m.documents
	body := strings.Repeat("x", 150) + "\n界 CLICKTARGET"

	checkFrame := func(width, height int) []string {
		t.Helper()
		lines := strings.Split(ansi.Strip(h.m.View().Content), "\n")
		if len(lines) != height {
			t.Fatalf("view has %d rows, want %d:\n%s", len(lines), height, h.m.View().Content)
		}
		top, bottom := -1, -1
		for i, line := range lines {
			if ansi.StringWidth(line) > width {
				t.Fatalf("row %d exceeds terminal width %d: %q", i, width, line)
			}
			if strings.HasPrefix(line, "┌") {
				top = i
			}
			if strings.HasPrefix(line, "└") {
				bottom = i
			}
		}
		if top < 0 || bottom <= top {
			t.Fatalf("missing pane borders:\n%s", h.m.View().Content)
		}
		for row := top; row <= bottom; row++ {
			line := lines[row]
			if ansi.StringWidth(line) != width {
				t.Fatalf("frame row %d has width %d, want %d: %q", row, ansi.StringWidth(line), width, line)
			}
			if row > top && row < bottom && (!strings.HasPrefix(line, "│") || !strings.HasSuffix(line, "│")) {
				t.Fatalf("frame row %d is missing a side border: %q", row, line)
			}
		}
		return lines
	}

	checkFrame(100, 30)
	s.body.SetValue("short")
	checkFrame(100, 30)
	s.body.SetValue(body)
	checkFrame(100, 30)
	h.key("ctrl+o")
	checkFrame(100, 30)
	h.send(tea.WindowSizeMsg{Width: 60, Height: 30})
	checkFrame(60, 30)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	lines := checkFrame(120, 40)
	if s.body.Value() != body {
		t.Fatal("resizing or toggling the outline changed the document")
	}

	for y, line := range lines {
		if i := strings.Index(line, "CLICKTARGET"); i >= 0 {
			x := ansi.StringWidth(line[:i]) + 3
			h.send(mouseEvent{X: x, Y: y, Action: mousePress, Button: mouseLeft})
			h.typeText("!")
			if got, want := s.body.Value(), strings.Replace(body, "CLICKTARGET", "CL!ICKTARGET", 1); got != want {
				t.Fatalf("click after resize inserted text at the wrong position: %q", got)
			}
			return
		}
	}
	t.Fatalf("click target not visible:\n%s", h.m.View().Content)
}

func TestDocumentTrackingReviewAndAccept(t *testing.T) {
	ctx := context.Background()
	h := newDocumentHarness(t, document.Draft{Body: "before", Layout: document.DefaultLayout()})
	s := h.m.documents
	h.key("f3")
	if !s.snapshot.Tracking {
		t.Fatalf("tracking was not enabled: %q", s.status)
	}
	s.body.SetValue("after")
	h.key("ctrl+s")
	if s.dirty() || s.body.Value() != "after" {
		t.Fatalf("editor did not save tracked content: body %q", s.body.Value())
	}
	live, err := h.docs.Get(ctx, h.path)
	if err != nil || live.Body != "after" || len(live.TrackedChanges) != 1 {
		t.Fatalf("tracked edit not saved: %+v, %v", live, err)
	}
	h.key("f4")
	h.key("enter")
	if !s.reviewing || !strings.Contains(h.m.View().Content, "Inserted") {
		t.Fatal("review did not display tracked content")
	}
	h.key("a")
	accepted, err := h.docs.Get(ctx, h.path)
	if err != nil || accepted.Body != "after" {
		t.Fatalf("accepted document = %+v, %v", accepted, err)
	}
	if len(s.changes) != 0 {
		t.Fatalf("accepted changes remain pending = %+v", s.changes)
	}
	if s.body.Value() != "after" {
		t.Fatalf("editor not refreshed after acceptance: %q", s.body.Value())
	}
	h.key("esc")
	if s.showChanges || h.quit {
		t.Fatalf("Esc from review: showChanges=%t quit=%t", s.showChanges, h.quit)
	}
}

func TestDocumentAcceptRefusedWithUnsavedEdits(t *testing.T) {
	ctx := context.Background()
	h := newDocumentHarness(t, document.Draft{Body: "before", Layout: document.DefaultLayout()})
	if _, err := h.docs.ModifyWithTracking(ctx, h.path, "Other", true, func(d *document.Document) error {
		d.Body = "proposed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := h.m.documents
	s.body.SetValue("local edit")
	h.key("f4")
	h.key("enter")
	h.key("a")
	if !strings.Contains(s.status, "Save or discard") {
		t.Fatalf("status = %q", s.status)
	}
	if d, _ := h.docs.Get(ctx, h.path); d.Body != "proposed" {
		t.Fatalf("review modified saved content over unsaved edits: %q", d.Body)
	}
	h.key("r")
	d, err := h.docs.Get(ctx, h.path)
	if err != nil || len(d.TrackedChanges) != 1 {
		t.Fatalf("rejected despite unsaved edits: %+v, %v", d.TrackedChanges, err)
	}
	if s.body.Value() != "local edit" {
		t.Fatalf("reject discarded unsaved edits: %q", s.body.Value())
	}
}

func TestDocumentChangeReviewScrollsAndBoundsChangeList(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: strings.Repeat("current line\n", 80) + "CURRENT-LAST"})
	change := document.TrackedChange{Deleted: strings.Repeat("current line\n", 80) + "CURRENT-LAST",
		Inserted: strings.Repeat("proposed line\n", 80) + "PROPOSED-LAST"}
	s := h.m.documents
	s.changes, s.selectedChange, s.showChanges, s.reviewing = []document.TrackedChange{change}, 0, true, true
	s.resize(100, 12)
	s.setChangeReviewContent()
	view := h.m.View().Content
	if strings.Contains(view, "CURRENT-LAST") || strings.Contains(view, "PROPOSED-LAST") {
		t.Fatalf("review showed content beyond the initial viewport:\n%s", view)
	}
	sawCurrent, sawProposed := false, false
	for i := 0; i < 100 && !(sawCurrent && sawProposed); i++ {
		h.key("pgdown")
		view = h.m.View().Content
		sawCurrent = sawCurrent || strings.Contains(view, "CURRENT-LAST")
		sawProposed = sawProposed || strings.Contains(view, "PROPOSED-LAST")
	}
	if !sawCurrent || !sawProposed {
		t.Fatal("scrolling did not reveal both document bodies")
	}
	if !strings.Contains(view, "a accept") || !strings.Contains(view, "Esc/q return") {
		t.Fatalf("review controls disappeared while scrolling:\n%s", view)
	}
	s.reviewing = false
	s.changes = make([]document.TrackedChange, 20)
	for i := range s.changes {
		s.changes[i].ID = fmt.Sprintf("Change %02d", i)
	}
	s.selectedChange = len(s.changes) - 1
	view = s.viewChanges("header")
	if !strings.Contains(view, "Change 19") || strings.Contains(view, "Change 00") {
		t.Fatalf("change list was not bounded around the selection:\n%s", view)
	}
}

func TestDocumentChangeReviewReflowsLongLinesOnResize(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: strings.Repeat("c", 90) + "CURRENT-END"})
	change := document.TrackedChange{Deleted: strings.Repeat("c", 90) + "CURRENT-END",
		Inserted: strings.Repeat("p", 90) + "PROPOSED-END"}
	s := h.m.documents
	s.changes, s.selectedChange, s.showChanges, s.reviewing = []document.TrackedChange{change}, 0, true, true
	s.resize(100, 12)
	wide := s.changeReview.TotalLineCount()
	s.resize(40, 12)
	if s.changeReview.TotalLineCount() <= wide {
		t.Fatalf("narrow review did not reflow: wide=%d narrow=%d", wide, s.changeReview.TotalLineCount())
	}
	sawCurrent, sawProposed := false, false
	for i := 0; i < s.changeReview.TotalLineCount() && !(sawCurrent && sawProposed); i++ {
		s.changeReview.SetYOffset(i)
		view := s.changeReview.View()
		sawCurrent = sawCurrent || strings.Contains(view, "CURRENT-END")
		sawProposed = sawProposed || strings.Contains(view, "PROPOSED-END")
	}
	if !sawCurrent || !sawProposed {
		t.Fatal("reflowed review did not make the ends of both long lines inspectable")
	}
	if review := s.viewChanges("header"); !strings.Contains(review, "a accept") || !strings.Contains(review, "Esc/q return") {
		t.Fatal("review controls disappeared after reflow")
	}
}

func TestToolbarMouseClickAndUnsavedEscape(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	s := h.m.documents
	placed, _ := s.buttonLayout(s.width)
	buttons := s.toolbarButtons()
	for _, p := range placed {
		if buttons[p.index].action == "columns" {
			h.send(mouseEvent{X: p.x + 1, Y: toolbarTop + p.row, Action: mousePress, Button: mouseLeft})
		}
	}
	if s.layout.Columns != 2 {
		t.Fatalf("clicking the columns button left %d columns", s.layout.Columns)
	}
	h.key("esc")
	if h.quit || !strings.Contains(s.status, "Unsaved changes") {
		t.Fatalf("Esc discarded unsaved layout changes without confirmation: %q", s.status)
	}
	h.key("esc")
	if h.quit || !s.reading {
		t.Fatal("second Esc did not return to the reader")
	}
	h.key("q")
	if !h.quit {
		t.Fatal("q did not quit the reader")
	}
	if d, _ := h.docs.Get(context.Background(), h.path); d.Layout.Columns != 1 {
		t.Fatalf("discarded layout was saved: %d columns", d.Layout.Columns)
	}
}

func TestAltFormattingFromToolbarFocusesBody(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	s := h.m.documents
	h.key("f2")
	if s.focus != focusToolbar || s.body.Focused {
		t.Fatal("F2 did not move focus from the body to the toolbar")
	}
	h.send(tea.KeyPressMsg{Code: []rune(string("b"))[0], Mod: tea.ModAlt})
	h.typeText("x")
	if !s.body.Focused {
		t.Fatal("formatting from the toolbar did not focus the body")
	}
	if s.body.Value() != "**x**" {
		t.Fatalf("body = %q", s.body.Value())
	}
}

func TestToolbarFormattingOnSelectedText(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	s := h.m.documents
	s.body.SetValue("Hello World")
	s.body.Selection = editor.Selection{Anchor: editor.Point{Line: 0, Col: 6}, Active: true}
	s.body.Cursor = editor.Point{Line: 0, Col: 11}

	// Alt+B triggers "bold" toolbar action
	h.send(tea.KeyPressMsg{Code: []rune(string("b"))[0], Mod: tea.ModAlt})
	if s.body.Value() != "Hello **World**" {
		t.Fatalf("body after bolding selection = %q, want %q", s.body.Value(), "Hello **World**")
	}
}

func TestSectionPromptRequiresWholeValue(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	s := h.m.documents
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

func TestSmallTerminalQDoesNotQuitDirtyDocument(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	h.typeText("Draft")
	h.send(tea.WindowSizeMsg{Width: 30, Height: 8})
	h.key("q")
	if h.quit {
		t.Fatal("q quit the editor with unsaved changes")
	}
	h.m.documents.body.SetValue("")
	h.key("q")
	if !h.quit {
		t.Fatal("q did not quit a clean editor")
	}
}

func TestCancelledImageLoadIsDiscarded(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "pixel.png")
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
	s := h.m.documents
	s.pending = true
	h.send(readImageCommand(ctx, path)())
	if len(s.images) != 0 || s.body.Value() != "" || s.pending {
		t.Fatal("cancelled image load changed the document")
	}
	if !strings.Contains(s.status, "cancelled") {
		t.Fatalf("status = %q", s.status)
	}
}

func TestEditorRefusesDocumentItCannotPreserve(t *testing.T) {
	body := "before\x1b[2Jafter"
	h := newHarness(t, "")
	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body}); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.DocumentKind)
	h.key("e")
	if s := h.m.documents; !s.reading || !strings.Contains(s.errMessage, "exceeds editor limits") {
		t.Fatalf("reading = %v, error = %q", s.reading, s.errMessage)
	}
	if stored, err := h.docs.Get(context.Background(), h.path); err != nil || stored.Body != body {
		t.Fatalf("stored body changed: %q, %v", stored.Body, err)
	}
}

func TestDocumentAutosaveIsOfferedOnReopen(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "# Before\n\nOriginal"})
	s := h.m.documents
	s.body.SetValue("# After\n\nRecovered")
	s.layout.Columns = 2
	h.run(s.saveDocumentRecovery(s.autosaveSession))
	if !h.hasDraft() {
		t.Fatal("autosave wrote no draft")
	}

	h.open(artifact.DocumentKind)
	if h.m.stage != stageRecovery {
		t.Fatalf("stage = %d", h.m.stage)
	}
	h.key("r")
	s = h.m.documents
	if h.m.stage != stageDocument || s.body.Value() != "# After\n\nRecovered" || s.layout.Columns != 2 {
		t.Fatalf("recovered editor: stage=%d body=%q columns=%d", h.m.stage, s.body.Value(), s.layout.Columns)
	}
	if s.snapshot.Body != "# Before\n\nOriginal" {
		t.Fatalf("recovered snapshot body = %q", s.snapshot.Body)
	}
	h.key("ctrl+s")
	if d, err := h.docs.Get(context.Background(), h.path); err != nil || d.Body != "# After\n\nRecovered" || d.Layout.Columns != 2 {
		t.Fatalf("saved recovered document = %+v, %v", d, err)
	}
	if h.hasDraft() {
		t.Fatal("saving left the draft behind")
	}
}

func TestRevertingDocumentEditsRemovesAutosavedDraft(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "Original"})
	s := h.m.documents
	s.body.SetValue("Unsaved")
	h.run(s.saveDocumentRecovery(s.autosaveSession))
	if !h.hasDraft() {
		t.Fatal("autosave wrote no draft")
	}
	s.body.SetValue("Original")
	h.run(s.saveDocumentRecovery(s.autosaveSession))
	if h.hasDraft() || s.draftStored {
		t.Fatalf("reverted editor kept its draft (draftStored=%t)", s.draftStored)
	}
}

func TestDocumentAutosaveStoresImagesOnceAndSkipsUnchangedState(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	h := newDocumentHarness(t, document.Draft{Body: "Intro"})
	if _, _, err := h.docs.AddImage(ctx, h.path, "pixel", buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.DocumentKind)
	s := h.m.documents
	if len(s.images) != 1 {
		t.Fatalf("images = %d", len(s.images))
	}
	s.body.SetValue(s.body.Value() + "\n\nEdited")
	h.run(s.saveDocumentRecovery(s.autosaveSession))
	draft, ok, err := h.repo.LoadRecovery(ctx, h.path)
	if err != nil || !ok {
		t.Fatalf("draft = %t, %v", ok, err)
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	if n := strings.Count(string(draft.Data), encoded); n != 1 {
		t.Fatalf("image payload stored %d times in the draft", n)
	}

	// An unchanged editor does not rewrite the draft.
	if err := h.repo.DeleteRecovery(ctx, h.path); err != nil {
		t.Fatal(err)
	}
	h.run(s.saveDocumentRecovery(s.autosaveSession))
	if h.hasDraft() {
		t.Fatal("unchanged state was autosaved again")
	}
	s.body.SetValue(s.body.Value() + " more")
	h.run(s.saveDocumentRecovery(s.autosaveSession))
	if !h.hasDraft() {
		t.Fatal("changed state was not autosaved")
	}

	h.open(artifact.DocumentKind)
	h.key("r")
	s = h.m.documents
	if h.m.stage != stageDocument || len(s.images) != 1 || len(s.snapshot.Images) != 1 ||
		!bytes.Equal(s.images[0].Data, buf.Bytes()) || !strings.HasSuffix(s.body.Value(), "Edited more") {
		t.Fatalf("recovered: stage=%d images=%d snapshot images=%d body=%q", h.m.stage, len(s.images), len(s.snapshot.Images), s.body.Value())
	}
	h.key("ctrl+s")
	if d, err := h.docs.Get(ctx, h.path); err != nil || len(d.Images) != 1 || !strings.HasSuffix(d.Body, "Edited more") {
		t.Fatalf("saved recovered document = %+v, %v", d, err)
	}
}

func TestDocumentPreviewFitsTerminalAndScrolls(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "# Hello\n\nSome text"})
	h.send(tea.WindowSizeMsg{Width: 90, Height: 24})
	h.key("f5")
	s := h.m.documents
	if !s.previewing || len(s.editPages) == 0 || len(s.editPages[0].Lines) <= 24 {
		t.Fatalf("preview: previewing=%t pages=%d", s.previewing, len(s.editPages))
	}
	view := h.m.View().Content
	if lines := strings.Count(view, "\n") + 1; lines > 24 {
		t.Fatalf("preview view has %d lines for a 24-line terminal", lines)
	}
	cleanView := ansi.Strip(view)
	if !strings.Contains(cleanView, "Hello") || !strings.Contains(cleanView, "Some text") || !strings.Contains(cleanView, "lines 1–21 of") {
		t.Fatalf("preview does not show the top of the page:\n%s", view)
	}
	h.send(tea.KeyPressMsg{Code: tea.KeyDown})
	if !strings.Contains(h.m.View().Content, "lines 2–22 of") {
		t.Fatalf("down did not scroll:\n%s", h.m.View().Content)
	}
	h.send(mouseEvent{Action: mousePress, Button: mouseWheelDown})
	if !strings.Contains(h.m.View().Content, "lines 5–25 of") {
		t.Fatalf("wheel did not scroll:\n%s", h.m.View().Content)
	}
	h.send(tea.KeyPressMsg{Code: tea.KeyEnd})
	total := len(s.editPages[0].Lines)
	if !strings.Contains(h.m.View().Content, fmt.Sprintf("lines %d–%d of %d", total-20, total, total)) {
		t.Fatalf("end did not reach the bottom:\n%s", h.m.View().Content)
	}
	h.key("esc")
	if s.previewing || h.m.stage != stageDocument {
		t.Fatal("esc did not return to the editor")
	}
}

func TestDocumentOpensInReaderWithOutline(t *testing.T) {
	h := newHarness(t, "")
	brk := "\n\n" + document.PageBreakMarkup + "\n\n"
	body := "# Intro\n\nHello." + brk + "# Middle\n\nMore." + brk + "## Detail\n\nEnd."
	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body}); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.DocumentKind)
	s := h.m.documents
	if h.m.stage != stageDocument || !s.reading {
		t.Fatalf("stage = %d, reading = %v", h.m.stage, s.reading)
	}
	view := h.m.View().Content
	for _, want := range []string{"Outline", "› Intro", "Middle", "Detail", "Page 1/", "Hello."} {
		if !strings.Contains(view, want) {
			t.Fatalf("reader is missing %q:\n%s", want, view)
		}
	}
	if lines := strings.Count(view, "\n") + 1; lines > h.m.height {
		t.Fatalf("reader view has %d lines for a %d-line terminal", lines, h.m.height)
	}
	if len(s.readerPages) < 3 {
		t.Fatalf("pages = %d", len(s.readerPages))
	}
	h.key("}")
	if s.readerPage != s.outline[1].Page-1 || !strings.Contains(h.m.View().Content, "› Middle") {
		t.Fatalf("} moved to page %d", s.readerPage+1)
	}
	h.key("{")
	if s.readerPage != 0 {
		t.Fatalf("{ moved to page %d", s.readerPage+1)
	}
	h.key("]")
	if s.readerPage != 1 {
		t.Fatalf("] moved to page %d", s.readerPage+1)
	}
	h.key("[")
	h.key("[")
	if s.readerPage != len(s.readerPages)-1 {
		t.Fatalf("[ did not wrap to the last page: %d", s.readerPage+1)
	}
	_, targets := s.outlineLines()
	for row, target := range targets {
		if target == 1 {
			h.send(mouseEvent{X: 3, Y: row + 2, Action: mousePress, Button: mouseLeft})
		}
	}
	if s.readerPage != s.outline[1].Page-1 {
		t.Fatalf("clicking the outline moved to page %d", s.readerPage+1)
	}

	h.key("e")
	if s.reading || !strings.Contains(h.m.View().Content, "Editing document") {
		t.Fatal("e did not open the editor")
	}
	h.key("esc")
	if !s.reading || h.quit {
		t.Fatal("Esc in a clean editor did not return to the reader")
	}
	h.key("q")
	if !h.quit {
		t.Fatal("q did not quit the reader")
	}
}

func TestDocumentReaderShowsSavedEditsAndDiscardsDraft(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "# Start\n\nText."})
	s := h.m.documents
	h.typeText("New ")
	h.key("ctrl+s")
	h.key("esc")
	if !s.reading || !strings.Contains(h.m.View().Content, "New") {
		t.Fatalf("reader does not show the saved edit:\n%s", h.m.View().Content)
	}
	h.key("e")
	h.typeText("Unsaved ")
	h.key("esc")
	h.key("esc")
	if !s.reading || strings.Contains(h.m.View().Content, "Unsaved") || !strings.Contains(s.status, "Discarded") {
		t.Fatalf("discarding did not return to the saved reader: %q\n%s", s.status, h.m.View().Content)
	}
	if h.hasDraft() {
		t.Fatal("leaving the editor kept the recovery draft")
	}
}

func TestDocumentEditConfigOpensExistingDocumentInEditor(t *testing.T) {
	h := newHarness(t, "")
	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: "# Created"}); err != nil {
		t.Fatal(err)
	}
	h.openWith(artifact.DocumentKind, true)
	s := h.m.documents
	if h.m.stage != stageDocument || s.reading || s.body.Value() != "# Created" {
		t.Fatalf("stage = %d, reading = %v, body = %q", h.m.stage, s.reading, s.body.Value())
	}
	h.key("esc")
	if !s.reading {
		t.Fatal("Esc did not return to the reader")
	}
}

type failingDeleteStore struct{ recovery.Store }

func (failingDeleteStore) DeleteRecovery(context.Context, string) error {
	return errors.New("disk is read-only")
}

func TestLeavingDocumentEditorReportsDraftCleanupFailure(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "# Start"})
	s := h.m.documents
	s.recoveryStore = failingDeleteStore{h.repo}
	s.draftStored = false
	h.key("esc")
	if !s.reading || !s.draftStored || !strings.Contains(s.errMessage, "disk is read-only") {
		t.Fatalf("reading = %v, draftStored = %v, error = %q", s.reading, s.draftStored, s.errMessage)
	}
}

func TestDocumentReaderFitsTerminalWidth(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "# Heading\n\n" + strings.Repeat("word ", 400)})
	h.key("esc")
	for _, width := range []int{80, 100, 137} {
		h.send(tea.WindowSizeMsg{Width: width, Height: 30})
		for _, line := range strings.Split(h.m.View().Content, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("reader line is %d columns in a %d-column terminal: %q", got, width, line)
			}
		}
	}
}
