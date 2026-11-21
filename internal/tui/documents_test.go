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
	"example.com/parchment/internal/spreadsheet"
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

func TestDocumentEmbeddedSpreadsheetWorkflow(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "# Budget"})
	s := h.m.documents
	h.send(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl | tea.ModShift})
	if s.embeddedEditor == nil {
		t.Fatalf("embedded editor did not open: %s", s.status)
	}
	if !s.embeddedEditor.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(book, spreadsheet.SetCellOptions{
			Sheet: "Sheet", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "Revenue"},
		})
	}) {
		t.Fatalf("edit embedded sheet: %s", s.embeddedEditor.errMessage)
	}
	draftBeforeCommit := s.currentDraft()
	if !strings.Contains(draftBeforeCommit.body, "parchment-sheet:") ||
		draftBeforeCommit.workbook.Sheets[0].Rows[0][0].Value != "Revenue" {
		t.Fatalf("autosave draft omitted in-progress sheet state: %+v", draftBeforeCommit)
	}
	h.key("ctrl+s")
	if s.embeddedEditor != nil || len(s.workbook.Sheets) != 1 {
		t.Fatalf("embedded commit did not return to document editor: editor=%t workbook=%+v", s.embeddedEditor != nil, s.workbook)
	}
	if !strings.Contains(s.body.Value(), "parchment-sheet:"+s.workbook.Sheets[0].ID) {
		t.Fatalf("sheet reference was not inserted into document: %q", s.body.Value())
	}
	h.key("ctrl+s")
	saved, err := h.docs.Get(context.Background(), h.path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Workbook.Sheets[0].Rows[0][0].Value != "Revenue" {
		t.Fatalf("saved embedded cell = %+v", saved.Workbook.Sheets[0].Rows[0][0])
	}
}

func TestDocumentEmbeddedSpreadsheetTracksAtomicChange(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{Body: "# Budget"})
	s := h.m.documents
	tracked, err := h.docs.SetTrackingExpected(context.Background(), s.snapshot, true)
	if err != nil {
		t.Fatal(err)
	}
	s.markSaved(tracked)
	h.send(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl | tea.ModShift})
	if s.embeddedEditor == nil {
		t.Fatalf("tracked embedded editor did not open: %s", s.status)
	}
	if !s.embeddedEditor.editWorkbook(func(book *spreadsheet.Spreadsheet) error {
		return spreadsheet.SetCellInBook(book, spreadsheet.SetCellOptions{
			Sheet: "Sheet", Row: 1, Column: 1, Cell: spreadsheet.Cell{Value: "4"},
		})
	}) {
		t.Fatalf("edit tracked embedded sheet: %s", s.embeddedEditor.errMessage)
	}
	h.key("ctrl+s")
	h.key("ctrl+s")
	saved, err := h.docs.Get(context.Background(), h.path)
	if err != nil {
		t.Fatal(err)
	}
	var workbookChange document.TrackedChange
	for _, change := range saved.TrackedChanges {
		if change.Kind == "workbook" {
			workbookChange = change
			break
		}
	}
	if workbookChange.ID == "" || workbookChange.Group != workbookChange.ID ||
		!strings.Contains(document.TrackingSummary(workbookChange), "Spreadsheet editing session") {
		t.Fatalf("workbook change was not tracked: %+v", saved.TrackedChanges)
	}
	for _, change := range saved.TrackedChanges {
		if change.ID != workbookChange.ID && change.Group != workbookChange.Group {
			t.Fatalf("spreadsheet reference was not grouped with workbook edit: %+v", saved.TrackedChanges)
		}
	}
	rejected, err := h.docs.ReviewTracked(context.Background(), saved, workbookChange.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rejected.Workbook.Sheets) != 0 || rejected.Body != "# Budget" || len(rejected.TrackedChanges) != 0 {
		t.Fatalf("rejecting spreadsheet session did not restore atomically: body=%q workbook=%+v changes=%+v",
			rejected.Body, rejected.Workbook, rejected.TrackedChanges)
	}
}

func TestDocumentEmbeddedChartReferenceFromGrid(t *testing.T) {
	sheet, err := spreadsheet.NewEmbeddedSheet("Data")
	if err != nil {
		t.Fatal(err)
	}
	book := spreadsheet.Workbook{Sheets: []spreadsheet.Sheet{sheet}}
	for row, pair := range [][2]string{{"North", "3"}, {"South", "5"}} {
		for column, value := range pair {
			if err := book.SetCell(spreadsheet.SetCellOptions{
				Sheet: "Data", Row: row + 1, Column: column + 1,
				Cell: spreadsheet.Cell{Value: value},
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	chart, err := book.AddChart(spreadsheet.Chart{
		ID: "chart-one", Type: "bar", Sheet: "Data", FirstRow: 1, LastRow: 2,
		FirstColumn: 1, LastColumn: 2, Labels: true,
		PlaceSheet: "Data", PlaceRow: 1, PlaceColumn: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	sheetRef, err := spreadsheet.FormatReference(spreadsheet.Reference{
		Kind: spreadsheet.SheetReferenceKind, ID: sheet.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := newDocumentHarness(t, document.Draft{Body: sheetRef, Workbook: book})
	h.send(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl | tea.ModShift})
	if h.m.documents.embeddedEditor == nil {
		t.Fatal("embedded sheet reference did not open")
	}
	h.m.documents.embeddedEditor.column = 3
	h.send(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl | tea.ModShift})
	if h.m.documents.embeddedEditor != nil {
		t.Fatal("chart reference command did not return to document editor")
	}
	chartRef, err := spreadsheet.FormatReference(spreadsheet.Reference{
		Kind: spreadsheet.ChartReferenceKind, ID: chart.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.m.documents.body.Value(), chartRef) {
		t.Fatalf("chart reference was not inserted: %q", h.m.documents.body.Value())
	}
	if len(h.m.documents.workbook.Charts) != 1 {
		t.Fatalf("committed workbook lost its chart: %+v", h.m.documents.workbook.Charts)
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

func TestDocumentReaderDoubleClickOpensEditorAtClickedWord(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	s := h.m.documents
	s.width = 70
	s.snapshot = document.Document{
		Body:   "# Heading\n\nThis paragraph contains targetword for editing.",
		Layout: document.DefaultLayout(),
	}
	s.startReading(s.snapshot)

	row, column := renderedWordPosition(s.readerLines, "targetword")
	x, y := column+2, row+3
	msg := mouseEvent{X: x, Y: y, Action: mousePress, Button: mouseLeft}
	s.updateReaderMouse(msg)
	if !s.reading {
		t.Fatal("single click unexpectedly opened the editor")
	}
	msg.X += 3
	s.updateReaderMouse(msg)
	if s.reading {
		t.Fatal("double click did not open the editor")
	}
	if got, want := s.body.Cursor, (editor.Point{Line: 2, Col: len("This paragraph contains ")}); got != want {
		t.Fatalf("editor cursor = %+v, want %+v", got, want)
	}
}

func TestDocumentReaderOpeningEditorPreservesScrolledLocation(t *testing.T) {
	for _, action := range []string{"double click", "keyboard"} {
		t.Run(action, func(t *testing.T) {
			h := newDocumentHarness(t, document.Draft{})
			s := h.m.documents
			s.resize(70, 18)
			var body strings.Builder
			body.WriteString("# Heading\n\n")
			for i := range 60 {
				fmt.Fprintf(&body, "unique%03d content\n\n", i)
			}
			s.startReading(document.Document{Body: body.String(), Layout: document.DefaultLayout()})
			topRow, _ := renderedWordPosition(s.readerLines, "unique040")
			clickRow, column := renderedWordPosition(s.readerLines, "unique045")
			s.reader.SetYOffset(topRow)
			if s.reader.YOffset() != topRow {
				t.Fatal("reader did not scroll to the test location")
			}
			if action == "keyboard" {
				s.updateReaderKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
				if !strings.Contains(strings.Split(ansi.Strip(s.body.View()), "\n")[0], "unique040") {
					t.Fatalf("editor did not preserve the reader's scrolled content: %q", s.body.View())
				}
			} else {
				msg := mouseEvent{X: column + 2, Y: 3 + clickRow - topRow, Action: mousePress, Button: mouseLeft}
				s.updateReaderMouse(msg)
				s.updateReaderMouse(msg)
				viewLines := strings.Split(ansi.Strip(s.body.View()), "\n")
				wantRow := min(clickRow-topRow, s.body.Height-1)
				if wantRow >= len(viewLines) || !strings.Contains(viewLines[wantRow], "unique045") {
					t.Fatalf("clicked text is not visible in editor: %q", s.body.View())
				}
				if got := string(s.body.Lines[s.body.Cursor.Line]); !strings.Contains(got, "unique045") {
					t.Fatalf("cursor landed on %q", got)
				}
			}
			if s.reading || s.body.TopLine == 0 {
				t.Fatalf("editor not scrolled: reading=%t top=%d", s.reading, s.body.TopLine)
			}
		})
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
	body := "# Intro\n\nHello.\n\n" + strings.Repeat("First paragraph.\n\n", 15) + brk +
		"# Middle\n\nMore.\n\n" + strings.Repeat("Middle paragraph.\n\n", 15) + brk + "## Detail\n\nEnd."
	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body}); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.DocumentKind)
	s := h.m.documents
	if h.m.stage != stageDocument || !s.reading {
		t.Fatalf("stage = %d, reading = %v", h.m.stage, s.reading)
	}
	view := h.m.View().Content
	for _, want := range []string{"Outline", "› Intro", "Middle", "Detail", "Hello."} {
		if !strings.Contains(view, want) {
			t.Fatalf("reader is missing %q:\n%s", want, view)
		}
	}
	if lines := strings.Count(view, "\n") + 1; lines > h.m.height {
		t.Fatalf("reader view has %d lines for a %d-line terminal", lines, h.m.height)
	}
	if strings.Contains(view, "Page 1/") || len(s.readerLines) < s.reader.Height()*2 {
		t.Fatal("reader still uses pages or did not retain the entire document")
	}
	h.key("}")
	if s.reader.YOffset() != s.readerRows[1] || !strings.Contains(h.m.View().Content, "› Middle") {
		t.Fatalf("} moved to row %d", s.reader.YOffset())
	}
	h.key("{")
	if s.reader.YOffset() != s.readerRows[0] {
		t.Fatalf("{ moved to row %d", s.reader.YOffset())
	}
	_, targets := s.outlineLines()
	for row, target := range targets {
		if target == 1 {
			h.send(mouseEvent{X: 3, Y: row + 3, Action: mousePress, Button: mouseLeft})
		}
	}
	if s.reader.YOffset() != s.readerRows[1] {
		t.Fatalf("clicking the outline moved to row %d", s.reader.YOffset())
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

func TestDocumentReaderContinuousRenderingAndNavigation(t *testing.T) {
	h := newHarness(t, "")
	layout := document.DefaultLayout()
	layout.Header, layout.Footer = "PRINT HEADER", "PRINT FOOTER"
	body := "# Repeated\n\nFirst.\n\n" + document.PageBreakMarkup + "\n\nSecond.\n\n" +
		document.SectionBreakMarkup(2, false) + "\n\nThird.\n\n" +
		"```text\n" + document.PageBreakMarkup + "\n```\n\n" +
		"```parchment-private\nhidden payload\n```\n\n" +
		strings.Repeat("Long paragraph for scrolling.\n\n", 25) +
		"<a id=\"destination\"></a>\n\n# Repeated\n\n[Back](#repeated)\n\n" +
		strings.Repeat("Trailing text.\n\n", 25)
	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body, Layout: layout}); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.DocumentKind)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 20})
	s := h.m.documents
	text := ansi.Strip(strings.Join(s.readerLines, "\n"))
	for _, want := range []string{"First.", "Second.", "Third.", "Trailing text."} {
		if !strings.Contains(text, want) {
			t.Fatalf("continuous reader omitted %q", want)
		}
	}
	if strings.Contains(text, "PRINT HEADER") || strings.Contains(text, "PRINT FOOTER") ||
		strings.Contains(text, "hidden payload") || strings.Count(text, "parchment:page-break") != 1 {
		t.Fatalf("reader included print layout or altered fenced code:\n%s", text)
	}
	if strings.Index(text, "Second.")-strings.Index(text, "First.") > 200 {
		t.Fatal("explicit page break inserted a page-sized gap")
	}
	var anchor, duplicate int = -1, -1
	for i, sec := range s.outline {
		switch sec.Slug {
		case "destination":
			anchor = i
		case "repeated-1":
			duplicate = i
		}
	}
	if anchor < 0 || duplicate < 0 || s.readerRows[anchor] < 0 || s.readerRows[duplicate] < 0 {
		t.Fatalf("missing anchor or duplicate heading positions: %+v rows=%v", s.outline, s.readerRows)
	}
	s.navigateToTarget("destination")
	if s.reader.YOffset() != s.readerRows[anchor] {
		t.Fatalf("anchor navigation landed on %d, want %d", s.reader.YOffset(), s.readerRows[anchor])
	}
	s.navigateToTarget("repeated-1")
	if s.reader.YOffset() != s.readerRows[duplicate] {
		t.Fatalf("duplicate heading navigation landed on %d, want %d", s.reader.YOffset(), s.readerRows[duplicate])
	}
	if len(s.readerLinks) != 1 {
		t.Fatalf("reader links = %+v", s.readerLinks)
	}
	box := s.readerLinks[0]
	outlineWidth, _ := s.readerPaneWidths()
	h.send(mouseEvent{X: outlineWidth + 3 + box.startCol, Y: box.row + 3 - s.reader.YOffset(), Action: mousePress, Button: mouseLeft})
	if s.reader.YOffset() != s.readerRows[0] {
		t.Fatalf("scrolled link did not navigate to first heading: %d", s.reader.YOffset())
	}
	stored, err := h.docs.Get(context.Background(), h.path)
	if err != nil || stored.Body != body {
		t.Fatalf("reader changed canonical Markdown: %v", err)
	}
}

func TestDocumentReaderStandaloneHTMLBlockAnchorNavigation(t *testing.T) {
	h := newHarness(t, "")
	body := "# Start\n\n" + strings.Repeat("Before.\n\n", 25) +
		"<a id=\"destination\"></a>\n\n" + strings.Repeat("After.\n\n", 25)
	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body}); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.DocumentKind)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 20})
	s := h.m.documents
	anchor := -1
	for index, section := range s.outline {
		if section.Anchor && section.Slug == "destination" {
			anchor = index
			break
		}
	}
	if anchor < 0 || s.readerRows[anchor] < 0 {
		t.Fatalf("standalone HTML block anchor missing from reader outline: %+v rows=%v", s.outline, s.readerRows)
	}
	s.navigateToTarget("destination")
	if s.reader.YOffset() != s.readerRows[anchor] {
		t.Fatalf("anchor navigation landed on %d, want %d", s.reader.YOffset(), s.readerRows[anchor])
	}
}

func TestDocumentReaderSectionNavigationInShortDocument(t *testing.T) {
	h := newHarness(t, "")
	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{
		Body: "# First\n\nText.\n\n# Second\n\nText.\n\n# Third\n",
	}); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.DocumentKind)
	s := h.m.documents
	h.key("}")
	h.key("}")
	if s.currentSection() != 2 {
		t.Fatalf("section navigation stopped at %d", s.currentSection())
	}
	h.key("{")
	if s.currentSection() != 1 {
		t.Fatalf("reverse section navigation stopped at %d", s.currentSection())
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
