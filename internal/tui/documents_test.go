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
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/config"
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

func TestDocumentReaderDoubleClickTargetsRepeatedWordOccurrence(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	s := h.m.documents
	s.width = 70
	s.snapshot = document.Document{
		Body:   "# Heading\n\nalpha beta alpha",
		Layout: document.DefaultLayout(),
	}
	s.startReading(s.snapshot)

	row, _ := renderedWordPosition(s.readerLines, "alpha")
	column := strings.LastIndex(ansi.Strip(s.readerLines[row]), "alpha")
	msg := mouseEvent{X: column + 2, Y: row + 3, Action: mousePress, Button: mouseLeft}
	s.updateReaderMouse(msg)
	msg.X += 3
	s.updateReaderMouse(msg)

	if s.reading {
		t.Fatal("double click did not open the editor")
	}
	if got, want := s.body.Cursor, (editor.Point{Line: 2, Col: len("alpha beta ")}); got != want {
		t.Fatalf("editor cursor = %+v, want clicked occurrence at %+v", got, want)
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

func TestDocumentReaderRefusesOversizedEditorAtScrolledLocation(t *testing.T) {
	body := strings.Repeat("Uneditable paragraph.\n\n", 50000)
	for _, click := range []bool{false, true} {
		t.Run(fmt.Sprintf("click=%t", click), func(t *testing.T) {
			s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
			s.reading = true
			s.snapshot = document.Document{Body: body}
			s.readerLines = []string{"first", "Uneditable paragraph.", "last"}
			s.reader.SetContentLines(s.readerLines)
			s.reader.SetHeight(1)
			s.reader.SetYOffset(1)
			if click {
				s.editAtReaderLocation(1, 2)
			} else {
				s.updateReaderKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
			}
			if !s.reading || s.reader.YOffset() != 1 || s.snapshot.Body != body ||
				!strings.Contains(s.errMessage, "exceeds editor limits") {
				t.Fatal("editor refusal changed reader position or saved document")
			}
		})
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
	h.key("e")
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
	if s.body.Value() != "" || s.dirty() {
		t.Fatal("reader unnecessarily initialized an editor draft")
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
	if s.reading || s.body.Value() != body || !strings.Contains(h.m.View().Content, "Editing document") {
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

func TestDocumentReaderOutlineMouseWheel(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&body, "# Section %02d\n\n%s\n\n", i, strings.Repeat("Paragraph.\n\n", 3))
	}
	h := newHarness(t, "")
	if _, err := h.docs.Create(context.Background(), h.path, document.Draft{Body: body.String()}); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.DocumentKind)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 24})
	s := h.m.documents
	wheel := func(x, y int, button tea.MouseButton) {
		h.send(tea.MouseWheelMsg(tea.Mouse{X: x, Y: y, Button: button}))
	}
	before := s.reader.YOffset()
	wheel(3, 5, tea.MouseWheelDown)
	_, targets := s.outlineLines()
	if targets[1] != 3 || s.reader.YOffset() != before {
		t.Fatalf("outline scroll: targets=%v reader offset=%d", targets, s.reader.YOffset())
	}
	for i := 0; i < 3; i++ {
		wheel(3, 5, tea.MouseWheelUp)
	}
	_, targets = s.outlineLines()
	if targets[1] != 0 || s.reader.YOffset() != before {
		t.Fatalf("outline did not stop at top: %v", targets)
	}
	for i := 0; i < 30; i++ {
		wheel(3, 5, tea.MouseWheelDown)
	}
	_, targets = s.outlineLines()
	if targets[len(targets)-1] != len(s.outline)-1 {
		t.Fatalf("outline did not reach last heading: %v", targets)
	}
	wheel(3, 5, tea.MouseWheelUp)
	_, targets = s.outlineLines()
	target := targets[1]
	h.send(tea.MouseClickMsg(tea.Mouse{X: 3, Y: 4, Button: tea.MouseLeft}))
	if s.currentSection() != target || s.reader.YOffset() != s.readerRows[target] {
		t.Fatalf("scrolled outline click selected %d, want %d", s.currentSection(), target)
	}
	outlineWidth, _ := s.readerPaneWidths()
	before = s.reader.YOffset()
	wheel(outlineWidth+3, 5, tea.MouseWheelDown)
	if s.reader.YOffset() <= before || s.readerOutlineManual {
		t.Fatal("wheel over document did not scroll the document and resume outline tracking")
	}
	wheel(3, 5, tea.MouseWheelDown)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	_, targets = s.outlineLines()
	if targets[len(targets)-1] >= len(s.outline) {
		t.Fatalf("invalid outline after resize: %v", targets)
	}
	h.send(tea.WindowSizeMsg{Width: 60, Height: 24})
	before = s.reader.YOffset()
	wheel(3, 5, tea.MouseWheelDown)
	if s.reader.YOffset() <= before {
		t.Fatal("wheel in narrow reader did not scroll the document")
	}
}

func TestDocumentEditorOutlineMouseWheel(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&body, "# Section %02d\n\nParagraph.\n\n", i)
	}
	h := newDocumentHarness(t, document.Draft{Body: body.String()})
	h.send(tea.WindowSizeMsg{Width: 100, Height: 24})
	h.key("ctrl+o")
	s := h.m.documents
	y := toolbarTop + s.toolbarRows() + 1
	wheel := func(x int, button tea.MouseButton) {
		h.send(tea.MouseWheelMsg(tea.Mouse{X: x, Y: y + 1, Button: button}))
	}
	before := s.body.TopLine
	wheel(3, tea.MouseWheelDown)
	_, targets := s.editorOutlineLines()
	if targets[1] != 3 || s.body.TopLine != before {
		t.Fatalf("outline scroll: targets=%v editor top=%d", targets, s.body.TopLine)
	}
	wheel(3, tea.MouseWheelUp)
	wheel(3, tea.MouseWheelUp)
	_, targets = s.editorOutlineLines()
	if targets[1] != 0 {
		t.Fatalf("outline did not stop at top: %v", targets)
	}
	for i := 0; i < 30; i++ {
		wheel(3, tea.MouseWheelDown)
	}
	_, targets = s.editorOutlineLines()
	if targets[len(targets)-1] != 59 {
		t.Fatalf("outline did not reach last heading: %v", targets)
	}
	wheel(3, tea.MouseWheelUp)
	_, targets = s.editorOutlineLines()
	target := targets[1]
	h.send(tea.MouseClickMsg(tea.Mouse{X: 3, Y: y + 1, Button: tea.MouseLeft}))
	if s.body.Cursor.Line != target*4 {
		t.Fatalf("scrolled outline click moved to line %d, want %d", s.body.Cursor.Line, target*4)
	}
	h.key("tab")
	outlineWidth, _ := s.readerPaneWidths()
	before = s.body.TopLine
	wheel(outlineWidth+4, tea.MouseWheelDown)
	if s.body.TopLine <= before {
		t.Fatal("wheel over editor did not scroll the editor")
	}
	if s.body.Value() != body.String() {
		t.Fatal("scrolling changed the document")
	}
}

func TestDocumentEditorOutlineCacheRefreshesOnContentRevision(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&body, "# Section %02d\n\nParagraph.\n\n", i)
	}
	h := newDocumentHarness(t, document.Draft{Body: body.String()})
	s := h.m.documents
	outline := s.editorOutline()
	if len(outline) != 60 || outline[0].Page != 0 {
		t.Fatalf("got %d outline entries, first page %d; want 60 source-only entries", len(outline), outline[0].Page)
	}

	revision := s.editorOutlineRev
	s.scrollEditorOutline(3)
	s.editorOutlineLines()
	if s.editorOutlineRev != revision {
		t.Fatal("scrolling rebuilt the outline without a content change")
	}

	s.body.SetCursorAtRow(editor.Point{Line: s.body.LineCount() - 1}, 0)
	s.body.InsertString("# Added\n")
	if got := len(s.editorOutline()); got != 61 {
		t.Fatalf("outline after edit has %d entries, want 61", got)
	}
	if s.editorOutlineRev != s.body.ContentRevision() {
		t.Fatal("outline cache did not advance to the edited content revision")
	}

	s.body.Undo()
	if got := len(s.editorOutline()); got != 60 {
		t.Fatalf("outline after undo has %d entries, want 60", got)
	}
	s.body.Redo()
	if got := len(s.editorOutline()); got != 61 {
		t.Fatalf("outline after redo has %d entries, want 61", got)
	}
}

func TestDocumentEditorKeyboardNavigationKeepsOutlineSelectionVisible(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&body, "# Section %02d\n\nParagraph.\n\n", i)
	}
	h := newDocumentHarness(t, document.Draft{Body: body.String()})
	s := h.m.documents
	if !s.outlineVisible {
		h.key("ctrl+o")
	}
	y := toolbarTop + s.toolbarRows() + 1
	for i := 0; i < 30; i++ {
		h.send(tea.MouseWheelMsg(tea.Mouse{X: 3, Y: y + 1, Button: tea.MouseWheelDown}))
	}
	s.readerSection = 30
	h.key("f6")
	h.key("down")

	if s.body.Cursor.Line != 31*4 {
		t.Fatalf("keyboard navigation moved cursor to line %d, want section 31", s.body.Cursor.Line)
	}
	_, targets := s.editorOutlineLines()
	for _, target := range targets {
		if target == 31 {
			return
		}
	}
	t.Fatalf("selected section 31 is outside the displayed outline: %v", targets)
}

func TestDocumentEditorKeyboardNavigationTracksSelection(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&body, "# Section %d\n\nParagraph.\n\n", i)
	}
	h := newDocumentHarness(t, document.Draft{Body: body.String()})
	s := h.m.documents
	if !s.outlineVisible {
		h.key("ctrl+o")
	}
	h.key("f6")

	for _, step := range []struct {
		key      string
		wantLine int
	}{
		{key: "down", wantLine: 4},
		{key: "down", wantLine: 8},
		{key: "up", wantLine: 4},
	} {
		h.key(step.key)
		if s.body.Cursor.Line != step.wantLine {
			t.Fatalf("after %s, editor cursor is on line %d, want %d", step.key, s.body.Cursor.Line, step.wantLine)
		}
	}

	s.body.SetCursorAtRow(editor.Point{Line: 0}, 0)
	s.body.InsertString("# Added\n\n")
	outline := s.editorOutline()
	wantLine, ok := findSourceLineForSection(s.body.Value(), outline, 3)
	if !ok {
		t.Fatal("could not locate Section 2 after inserting a heading")
	}
	h.key("down")
	if s.body.Cursor.Line != wantLine {
		t.Fatalf("after editing and pressing down, editor cursor is on line %d, want Section 2 on line %d", s.body.Cursor.Line, wantLine)
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

func TestDocumentReaderRepeatedNavigationMarkers(t *testing.T) {
	body := "# Same\n\n## Detail\n\nfirst <a id=\"inline\"></a> marker\n\n" +
		"# Same\n\nsecond <a id=\"inline\"></a> marker\n\n" +
		"<a id=\"block\"></a>\n\nbetween\n\n<a id=\"block\"></a>\n\n## Detail\n"
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.resize(80, 24)
	s.startReading(document.Document{Body: body, Layout: document.DefaultLayout()})
	if s.errMessage != "" {
		t.Fatal(s.errMessage)
	}
	var headings, inline, block []int
	for i, section := range s.outline {
		if s.readerRows[i] < 0 {
			t.Fatalf("unmapped section %+v", section)
		}
		switch {
		case section.Anchor && section.Slug == "inline":
			inline = append(inline, s.readerRows[i])
		case section.Anchor && section.Slug == "block":
			block = append(block, s.readerRows[i])
		case !section.Anchor:
			headings = append(headings, s.readerRows[i])
		}
	}
	for name, rows := range map[string][]int{"headings": headings, "inline": inline, "block": block} {
		want := 2
		if name == "headings" {
			want = 4
		}
		if len(rows) != want {
			t.Fatalf("%s rows = %v, want %d", name, rows, want)
		}
		for i := 1; i < len(rows); i++ {
			if rows[i] <= rows[i-1] {
				t.Fatalf("%s out of order: %v", name, rows)
			}
		}
	}
	if s.snapshot.Body != body {
		t.Fatal("reader changed the source")
	}
}

func TestReaderPreambleMatchesFullRendering(t *testing.T) {
	profile := terminalColorProfile
	terminalColorProfile = colorprofile.TrueColor
	t.Cleanup(func() { terminalColorProfile = profile })
	bodies := []string{
		"First plain paragraph.\n\n# Later\n\nMore text.",
		"# First heading with words.\n\n## Later\n\nMore text.",
		"### A smaller heading.\n\n# Later\n",
		"# First\n\nFirst paragraph.\n\nSecond paragraph.\n\n## Later\n\nLast paragraph.\n\nA [link][later]\n\n[later]: https://example.com",
		"First paragraph.\n\nSecond paragraph.\n\nA term\n: Definition\n\nEnd.",
		"First paragraph.\n\nSecond paragraph.\n\n<div>HTML content</div>",
		"First plain paragraph with several words that need wrapping at a narrow width.\n" +
			"Another line with punctuation, and numbers 123.\n\n" +
			"A [link][later]\n\n[later]: https://example.com",
	}
	for _, first := range []string{"First.", "# Heading", "Long first line.\nContinuation."} {
		for _, second := range []string{"Second.", "## Heading", "Another line.\nContinued."} {
			bodies = append(bodies, first+"\n\n"+second+"\n\nThird.\n\n[ref]: https://example.com")
		}
	}
	for _, terminalWidth := range []int{24, 44, 120} {
		for _, body := range bodies {
			for _, theme := range []Theme{{}, resolveTheme(config.Theme{Mode: "dark"})} {
				prefix := readerPreamble(body)
				if prefix == "" {
					t.Fatalf("plain paragraph was not accepted: %q", body)
				}
				doc := document.Document{Body: body, Layout: document.DefaultLayout()}
				screen := newDocumentsScreen(nil, "", nil, theme, 10)
				screen.snapshot = doc
				screen.width, screen.height = terminalWidth, 24
				screen.showReaderPreamble()
				width := screen.reader.Width()
				full, _, _, _, err := screen.renderReaderDocument(doc, width)
				if err != nil {
					t.Fatal(err)
				}
				partial := screen.readerLines
				if len(partial) == 0 || len(partial) > len(full) || !slices.Equal(partial, full[:len(partial)]) {
					t.Fatalf("width %d: partial %q differs from full prefix %q", width, partial, full)
				}
			}
		}
	}
	if got := readerPreamble("First.\n\nSecond.\n\nThird.\n\n[ref]: target"); got != "First.\n\nSecond." {
		t.Fatalf("preamble did not stop before the ambiguous block: %q", got)
	}
	if got := readerPreamble(strings.Repeat("Plain paragraph.\n\n", 1000) + "End."); len(got) > 8192 ||
		strings.Count(got, "Plain paragraph.") > maxReaderPreambleBlocks {
		t.Fatalf("preamble exceeded its limits: %d bytes, %d blocks", len(got), strings.Count(got, "Plain paragraph."))
	}
	for _, body := range []string{
		"# Heading *emphasis*\n\ntext", "A [reference][later]\n\n[later]: target",
		"A heading\n---\n\ntext", "    indented\n\ntext",
		"# Heading\nnext paragraph line\n\ntext", strings.Repeat("x", 8193) + "\n\n",
	} {
		if got := readerPreamble(body); got != "" {
			t.Fatalf("unsafe paragraph accepted: %q", body)
		}
	}
}

func TestDocumentReaderCoalescesRendersAndQueuesNavigation(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height = 80, 24
	s.reading = true
	s.snapshot = document.Document{Body: "First paragraph.\n\n# Heading\n\n" + strings.Repeat("text\n\n", 30), Layout: document.DefaultLayout()}
	s.showReaderPreamble()
	first := s.requestReaderRender()
	firstSession := s.readerRenderSession
	if first == nil || !s.readerRendering || !strings.Contains(s.readerContentView(), "First paragraph") {
		t.Fatal("reader did not show a preamble while rendering")
	}
	s.updateReaderKey(tea.KeyPressMsg{Code: 'G', Text: "G"})
	s.width, s.height = 100, 30
	if cmd := s.requestReaderRender(); cmd != nil {
		t.Fatal("resize started a second renderer")
	}
	stale := first().(documentReaderRenderMsg)
	if stale.session != firstSession {
		t.Fatal("first render lost its generation")
	}
	next := s.readerRenderFinished(stale)
	if next == nil || !s.readerRendering {
		t.Fatal("stale render did not schedule the latest size")
	}
	msg := next().(documentReaderRenderMsg)
	s.readerRenderFinished(msg)
	if s.readerRendering || len(s.readerQueuedInput) != 0 || !s.reader.AtBottom() {
		t.Fatalf("queued end navigation not applied: rendering=%t offset=%d", s.readerRendering, s.reader.YOffset())
	}
}

func TestDocumentReaderShowsOutlineBeforeRendering(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.backgroundReader = 80, 24, true, true
	s.snapshot = document.Document{
		Body:   "<a id=\"same\"></a>\n\nFirst.\n\n<a id=\"same\"></a>\n\n" + strings.Repeat("More.\n\n", 30),
		Layout: document.DefaultLayout(),
	}
	index := s.requestReaderRender()().(documentReaderIndexMsg)
	render := s.readerIndexFinished(index)
	if render == nil || !s.readerRendering || len(s.outline) != 2 ||
		s.readerRows[0] != -1 || s.readerRows[1] != -1 {
		t.Fatal("outline was not published before rendering")
	}
	s.updateReaderMouse(mouseEvent{X: 3, Y: 5, Action: mousePress, Button: mouseLeft})
	if len(s.readerQueuedInput) != 1 {
		t.Fatal("outline target was not queued while rendering")
	}
	s.readerRenderFinished(render().(documentReaderRenderMsg))
	if s.currentSection() != 1 || s.reader.YOffset() != s.readerRows[1] {
		t.Fatal("early outline click did not preserve the duplicate anchor")
	}
}

func TestDocumentReaderShowsStreamedPreviewBeforeRenderCompletes(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.backgroundReader = 80, 14, true, true
	s.snapshot = document.Document{
		Body:   "```go\nvar answer = 42\n```\n\n" + strings.Repeat("Paragraph with enough words to fill a row.\n\n", 4000),
		Layout: document.DefaultLayout(),
	}
	firstResult := s.requestReaderRender()()
	if early, ok := firstResult.(documentReaderEarlyMsg); ok {
		next := s.readerEarlyFinished(early)
		firstResult = next()
	}
	index := firstResult.(documentReaderIndexMsg)
	render := s.readerIndexFinished(index)
	first, ok := render().(documentReaderPreviewMsg)
	if !ok {
		t.Fatal("expected a streamed preview before the complete render")
	}
	next := s.readerPreviewFinished(first)
	if next == nil || !s.readerRendering || !s.readerPreambleShown || len(s.readerLines) <= s.reader.Height() {
		t.Fatal("streamed preview did not provide scrollable content")
	}
	s.reader.GotoBottom()
	if !strings.Contains(s.readerContentView(), "Loading…") {
		t.Fatal("preview has no loading indicator at its end")
	}
	s.reader.GotoTop()
	s.updateReaderKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if s.reader.YOffset() != 1 || s.readerPreviewInputs != 1 {
		t.Fatal("streamed preview did not handle scrolling while rendering")
	}
	var full documentReaderRenderMsg
	previews := 1
	for {
		switch msg := next().(type) {
		case documentReaderPreviewMsg:
			previous := len(s.readerLines)
			next = s.readerPreviewFinished(msg)
			if len(s.readerLines) <= previous {
				t.Fatal("subsequent preview did not grow")
			}
			previews++
		case documentReaderRenderMsg:
			full = msg
			goto complete
		default:
			t.Fatalf("unexpected render result %T", msg)
		}
	}
complete:
	if previews < 2 || len(full.lines) < len(s.readerLines) {
		t.Fatalf("got %d previews with %d of %d lines", previews, len(s.readerLines), len(full.lines))
	}
	for i, line := range s.readerLines {
		if line != full.lines[i] {
			t.Fatalf("preview row %d differs from completed rendering", i)
		}
	}
	s.readerRenderFinished(full)
	if s.readerRendering || s.reader.YOffset() != 1 || len(s.readerQueuedInput) != 0 {
		t.Fatal("completed render did not preserve streamed preview navigation")
	}
	s.reader.GotoBottom()
	if strings.Contains(s.readerContentView(), "Loading…") {
		t.Fatal("loading indicator remained after rendering")
	}
}

func TestDocumentReaderNavigatesSectionsAsBatchesArrive(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	defer s.cancelReaderRender()
	s.width, s.height, s.reading, s.backgroundReader = 80, 14, true, true
	paragraphs := strings.Repeat("Paragraph with enough words to fill a row.\n\n", 2000)
	s.snapshot = document.Document{
		Body:   "<a id=\"same\"></a>\n\n" + paragraphs + "<a id=\"same\"></a>\n\n" + paragraphs,
		Layout: document.DefaultLayout(),
	}
	result := s.requestReaderRender()()
	if early, ok := result.(documentReaderEarlyMsg); ok {
		result = s.readerEarlyFinished(early)()
	}
	next := s.readerIndexFinished(result.(documentReaderIndexMsg))
	first := next().(documentReaderPreviewMsg)
	next = s.readerPreviewFinished(first)
	if len(s.readerRows) != 2 || s.readerRows[0] < 0 || s.readerRows[1] >= 0 {
		t.Fatalf("unexpected first-batch section rows: %v", s.readerRows)
	}
	s.updateReaderMouse(mouseEvent{X: 3, Y: 4, Action: mousePress, Button: mouseLeft})
	if s.readerPreviewInputs != 1 || s.currentSection() != 0 {
		t.Fatal("available outline target was not applied")
	}
	s.updateReaderMouse(mouseEvent{X: 3, Y: 5, Action: mousePress, Button: mouseLeft})
	if s.readerPreviewInputs != 1 || len(s.readerQueuedInput) != 2 {
		t.Fatal("unrendered outline target was not queued")
	}
	jumped := false
	for {
		switch msg := next().(type) {
		case documentReaderPreviewMsg:
			next = s.readerPreviewFinished(msg)
			if s.readerPreviewInputs == 2 {
				if s.currentSection() != 1 || s.readerOffset() != s.readerRows[1] {
					t.Fatal("queued outline target did not jump when it became available")
				}
				jumped = true
			}
		case documentReaderRenderMsg:
			if !jumped {
				t.Fatal("outline target was not applied before rendering finished")
			}
			if !slices.Equal(s.readerRows, msg.rows) || !slices.Equal(s.readerRowIndex, msg.rowIndex) {
				for i := range msg.rows {
					if s.readerRows[i] != msg.rows[i] {
						t.Fatalf("section %d: progressive row %d, completed row %d", i, s.readerRows[i], msg.rows[i])
					}
				}
				for i := range min(len(s.readerRowIndex), len(msg.rowIndex)) {
					if s.readerRowIndex[i] != msg.rowIndex[i] {
						t.Fatalf("index %d: progressive %+v, completed %+v", i, s.readerRowIndex[i], msg.rowIndex[i])
					}
				}
				t.Fatalf("index lengths: progressive %d, completed %d", len(s.readerRowIndex), len(msg.rowIndex))
			}
			s.readerRenderFinished(msg)
			if s.currentSection() != 1 || s.readerOffset() != s.readerRows[1] {
				t.Fatal("completed rendering did not preserve the outline jump")
			}
			return
		default:
			t.Fatalf("unexpected render result %T", msg)
		}
	}
}

func TestDocumentReaderQueuesSectionKeyUntilAvailable(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.readerRendering = 60, 15, true, true
	s.readerRenderSession = 1
	s.readerPreambleShown, s.readerStreamPreview = true, true
	s.reader.SetWidth(50)
	s.reader.SetHeight(10)
	s.outline = []document.Section{{Title: "First"}, {Title: "Second"}}
	s.readerRows = []int{0, -1}
	s.readerRowIndex = []readerSectionRow{{row: 0, section: 0, highestSection: 0}}
	s.readerLines = make([]string, 100)
	s.readerSection = 0
	s.showReaderContent()
	s.updateReaderKey(tea.KeyPressMsg{Code: '}', Text: "}"})
	if len(s.readerQueuedInput) != 1 || s.readerPreviewInputs != 0 {
		t.Fatal("unrendered section key was not queued")
	}
	s.readerPreviewFinished(documentReaderPreviewMsg{
		session: 1, lines: make([]string, 100),
		sections: []readerSectionRow{{row: 110, section: 1}},
	})
	if s.readerPreviewInputs != 1 || s.readerOffset() != 110 || s.currentSection() != 1 {
		t.Fatal("section key did not apply when the target arrived")
	}
	s.readerRenderFinished(documentReaderRenderMsg{
		session: 1, lines: make([]string, 200), outline: s.outline, rows: []int{0, 110},
		rowIndex: []readerSectionRow{{row: 0, section: 0, highestSection: 0}, {row: 110, section: 1, highestSection: 1}},
	})
	if s.readerOffset() != 110 || s.currentSection() != 1 {
		t.Fatal("completed rendering did not preserve the section key")
	}
}

func TestDocumentReaderCheckpointWindowNavigationAndSearch(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	defer s.cancelReaderRender()
	s.width, s.height, s.reading, s.backgroundReader = 80, 14, true, true
	s.snapshot = document.Document{
		Body: "# First\n\n[Jump to last](#last)\n\n" + strings.Repeat("Paragraph with enough words to fill a row.\n\n", 4000) +
			"# Last\n\nUniqueNeedle appears here.\n\n" + strings.Repeat("The end.\n\n", 50),
		Layout: document.DefaultLayout(),
	}
	next := s.requestReaderRender()
	var full []string
	var preview []string
	for next != nil {
		result := next()
		if finished, ok := result.(documentReaderRenderMsg); ok {
			if finished.err != nil {
				t.Fatal(finished.err)
			}
			full = finished.lines
			preview = s.readerLines
			if finished.sparse != nil {
				t.Fatal("progressive load allocated a separate sparse row index")
			}
		}
		next = s.update(result)
	}
	if s.readerCache == nil || len(full) <= readerPreviewWindowLines ||
		s.readerCacheEnd-s.readerCacheBase > readerPreviewWindowLines {
		t.Fatal("completed reader did not retain a bounded checkpoint window")
	}
	if len(preview) != len(s.readerLines) || &preview[0] != &s.readerLines[0] {
		t.Fatal("completed reader did not reuse the progressive row index")
	}
	if len(s.readerLinks) == 0 || s.readerLinks[0].target != "last" {
		t.Fatal("cached reader did not retain the internal link")
	}
	for i := s.readerCacheEnd; i < len(s.readerLines); i++ {
		if s.readerLines[i] != "" {
			t.Fatalf("row %d retained outside the cached window", i)
		}
	}
	stale := s.update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if stale == nil || !s.readerCachePending {
		t.Fatal("distant navigation did not dispatch an asynchronous window")
	}
	s.update(tea.KeyPressMsg{Code: tea.KeyHome})
	s.update(stale())
	if s.readerCachePending || s.readerOffset() != 0 {
		t.Fatal("stale window changed the reader position")
	}
	window := s.update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if window == nil {
		t.Fatal("end navigation did not dispatch a window")
	}
	s.update(window())
	offset := s.readerOffset()
	if offset != len(full)-s.reader.Height() ||
		!slices.Equal(s.readerLines[offset:offset+s.reader.Height()], full[offset:offset+s.reader.Height()]) {
		t.Fatal("end window differs from completed rendering")
	}
	window = s.update(tea.KeyPressMsg{Code: tea.KeyHome})
	if window == nil {
		t.Fatal("home navigation did not dispatch a window")
	}
	s.update(window())
	s.find.visible = true
	s.find.input.SetValue("UniqueNeedle")
	search := s.searchReader(s.find.query())
	if search == nil {
		t.Fatal("cached reader search was not asynchronous")
	}
	result := search().(documentReaderSearchMsg)
	want, err := renderedReaderMatchesContext(context.Background(), full, s.find.query(), maxReaderSearchMatches)
	if err != nil || !slices.Equal(result.matches, want) {
		t.Fatalf("cached search differs from completed render: %v", err)
	}
	window = s.update(result)
	if window == nil {
		t.Fatal("search match did not dispatch its distant window")
	}
	s.update(window())
	if len(want) != 1 || s.readerOffset() != want[0].row {
		t.Fatal("cached search did not navigate to the match")
	}
	oldCache := s.readerCache
	s.width = 100
	if s.requestReaderRender() == nil {
		t.Fatal("resize did not start a reflow")
	}
	s.readerRenderFinished(documentReaderRenderMsg{session: s.readerRenderSession, err: errors.New("reflow failed")})
	if s.readerCache != oldCache {
		t.Fatal("failed reflow discarded the previous checkpoint source")
	}
	window = s.update(tea.KeyPressMsg{Code: tea.KeyHome})
	if window == nil {
		t.Fatal("cached navigation stopped working after failed reflow")
	}
	s.update(window())
	if s.readerOffset() != 0 || !slices.Equal(s.readerLines[:s.reader.Height()], full[:s.reader.Height()]) {
		t.Fatal("failed reflow did not preserve the old reader")
	}
	stale = s.update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if stale == nil || !s.readerCachePending {
		t.Fatal("end did not request a distant window")
	}
	s.width = 110
	if s.requestReaderRender() == nil {
		t.Fatal("resize did not start a reflow with a pending window")
	}
	window = s.update(documentReaderRenderMsg{session: s.readerRenderSession, err: errors.New("reflow failed")})
	if window == nil || !s.readerCachePending {
		t.Fatal("failed reflow did not restore pending navigation")
	}
	s.update(stale())
	s.update(window())
	if s.readerOffset() != len(full)-s.reader.Height() {
		t.Fatal("failed reflow lost the requested position")
	}
	window = s.update(tea.KeyPressMsg{Code: tea.KeyHome})
	s.update(window())
	stale = s.update(tea.KeyPressMsg{Code: tea.KeyEnd})
	s.width = 120
	if s.requestReaderRender() == nil {
		t.Fatal("resize did not start a second reflow")
	}
	rows := append([]int(nil), s.readerRows...)
	rowIndex := append([]readerSectionRow(nil), s.readerRowIndex...)
	oldLines := s.readerLines
	oldContents := slices.Clone(oldLines)
	sparse := make([]string, len(full))
	s.update(documentReaderRenderMsg{
		session: s.readerRenderSession, lines: full, outline: s.outline, rows: rows,
		rowIndex: rowIndex, source: oldCache, sparse: sparse,
	})
	s.update(stale())
	if s.readerOffset() != len(full)-s.reader.Height() {
		t.Fatal("successful reflow lost the requested position")
	}
	if &s.readerLines[0] != &sparse[0] || !slices.Equal(oldLines, oldContents) {
		t.Fatal("reflow reused or modified the previous reader's rows")
	}
}

func TestDocumentReaderCheckpointWindowDisplaysDistantImage(t *testing.T) {
	documentImageEnv(t, "none")
	img := documentTestImage(t)
	body := strings.Repeat("Paragraph with enough words to fill a row.\n\n", 3500) +
		"\n![Distant](" + img.Name + ")\n\n" + strings.Repeat("After image.\n\n", 100)
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	defer s.cancelReaderRender()
	s.width, s.height, s.reading, s.backgroundReader = 80, 14, true, true
	s.snapshot = document.Document{Body: body, Images: []artifact.Image{img}, Layout: document.DefaultLayout()}
	cmd := s.requestReaderRender()
	var full []string
	for cmd != nil {
		result := cmd()
		if completed, ok := result.(documentReaderRenderMsg); ok {
			full = completed.lines
		}
		cmd = s.update(result)
	}
	if s.errMessage != "" || s.readerCache == nil {
		t.Fatalf("media reader failed: %s", s.errMessage)
	}
	imageRow := -1
	for row, line := range full {
		for marker := range s.imageState.rows {
			if strings.ContainsRune(line, marker) {
				imageRow = row
				break
			}
		}
		if imageRow >= 0 {
			break
		}
	}
	if imageRow < 0 {
		t.Fatal("completed render did not contain the image")
	}
	s.setReaderOffset(imageRow)
	cmd = s.dispatchReaderWindow()
	if cmd == nil {
		t.Fatal("distant image did not request a window")
	}
	s.update(cmd())
	if !strings.Contains(ansi.Strip(s.view("")), "▀") {
		t.Fatal("cached window did not display the image")
	}
}

func TestDocumentReaderProgressiveSectionIndexMatchesCompletion(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	defer s.cancelReaderRender()
	s.width, s.height, s.reading, s.backgroundReader = 80, 14, true, true
	s.snapshot = document.Document{
		Body: strings.Repeat("# Repeated\n\nText <a id=\"inline\"></a> and <a id=\"other\"></a>.\n\n"+
			"<a id=\"block\"></a>\n\nParagraph with more words.\n\n", 1800),
		Layout: document.DefaultLayout(),
	}
	result := s.requestReaderRender()()
	if early, ok := result.(documentReaderEarlyMsg); ok {
		result = s.readerEarlyFinished(early)()
	}
	next := s.readerIndexFinished(result.(documentReaderIndexMsg))
	for {
		switch msg := next().(type) {
		case documentReaderPreviewMsg:
			next = s.readerPreviewFinished(msg)
		case documentReaderRenderMsg:
			if msg.err != nil {
				t.Fatal(msg.err)
			}
			if len(s.readerLines) != len(msg.lines) {
				t.Fatalf("last batch has %d lines; completed rendering has %d", len(s.readerLines), len(msg.lines))
			}
			for i, row := range msg.rows {
				want := row
				if row >= len(s.readerLines) {
					want = -1
				}
				if s.readerRows[i] != want {
					t.Fatalf("section %d: progressive row %d, want %d", i, s.readerRows[i], want)
				}
			}
			end := 0
			for end < len(msg.rowIndex) && msg.rowIndex[end].row < len(s.readerLines) {
				end++
			}
			if !slices.Equal(s.readerRowIndex, msg.rowIndex[:end]) {
				t.Fatal("progressive section index differs from completed rendering")
			}
			s.readerRenderFinished(msg)
			return
		default:
			t.Fatalf("unexpected render result %T", msg)
		}
	}
}

func TestDocumentReaderShowsFullEarlyViewportBeforeIndex(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.backgroundReader = 80, 24, true
	body := "# **Heading**\n\n[Visible][later]\n\n![Chart](parchment-chart:missing)\n\n" +
		strings.Repeat("Paragraph with enough words to fill a row.\n\n", 4000) +
		"\n[later]: https://example.com\n"
	doc := document.Document{Body: body, Layout: document.DefaultLayout()}
	cmd := s.startReading(doc)
	if cmd == nil {
		t.Fatal("reader did not start background loading")
	}
	early, ok := cmd().(documentReaderEarlyMsg)
	if !ok || len(early.lines) < s.reader.Height() ||
		!strings.Contains(ansi.Strip(strings.Join(early.lines, "\n")), "[image: Chart]") {
		t.Fatalf("early preview has %d lines, want at least %d", len(early.lines), s.reader.Height())
	}
	next := s.readerEarlyFinished(early)
	if next == nil || !s.readerProvisional || !strings.Contains(s.readerStatus(), "preview may change") {
		t.Fatal("full early viewport was not marked provisional")
	}
	offset := s.reader.YOffset()
	s.updateReaderKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if s.reader.YOffset() != offset || len(s.readerQueuedInput) != 1 {
		t.Fatal("provisional navigation was not queued")
	}
	index := next().(documentReaderIndexMsg)
	next = s.readerIndexFinished(index)
	sawVerified := false
	for {
		switch msg := next().(type) {
		case documentReaderPreviewMsg:
			next = s.readerPreviewFinished(msg)
			if s.readerProvisional || !s.readerStreamPreview || s.reader.YOffset() != 1 {
				t.Fatal("verified preview did not replay queued navigation")
			}
			sawVerified = true
		case documentReaderRenderMsg:
			s.readerRenderFinished(msg)
			if !sawVerified || s.readerRendering || s.snapshot.Body != body {
				t.Fatal("reader did not complete without changing the source")
			}
			return
		default:
			t.Fatalf("unexpected render result %T", msg)
		}
	}
}

func TestDocumentReaderStreamsLinksAndImages(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.backgroundReader = 80, 14, true, true
	img := documentTestImage(t)
	body := "# Heading\n\n[Jump to destination](#destination)\n\n![Picture](" + img.Name + ")\n\n" +
		strings.Repeat("Paragraph with enough words to fill a row.\n\n", 4000) +
		"# Destination\n\nDone."
	s.snapshot = document.Document{Body: body, Images: []artifact.Image{img}, Layout: document.DefaultLayout()}
	first := s.requestReaderRender()()
	if early, ok := first.(documentReaderEarlyMsg); ok {
		if len(early.lines) < s.reader.Height() || !strings.Contains(ansi.Strip(strings.Join(early.lines, "\n")), "[image: Picture]") {
			t.Fatal("early viewport did not use an image placeholder")
		}
		next := s.readerEarlyFinished(early)
		first = next()
	} else {
		t.Fatal("expected an early viewport")
	}
	next := s.readerIndexFinished(first.(documentReaderIndexMsg))
	var full documentReaderRenderMsg
	previews := 0
	for {
		switch msg := next().(type) {
		case documentReaderPreviewMsg:
			if !msg.approximate {
				t.Fatal("preview omitted pending link styling")
			}
			next = s.readerPreviewFinished(msg)
			if !strings.Contains(s.readerStatus(), "link styling pending") || len(s.imageState.rows) == 0 {
				t.Fatal("preview lost pending link styling or image data")
			}
			previews++
		case documentReaderRenderMsg:
			full = msg
			goto complete
		default:
			t.Fatalf("unexpected render result %T", msg)
		}
	}
complete:
	if previews < 2 || len(s.readerLines) > len(full.lines) || len(full.links) == 0 {
		t.Fatal("reader did not progressively render linked media")
	}
	if full.source == nil {
		t.Fatal("linked media did not use checkpoint windows")
	}
	for _, start := range []int{0, len(full.lines) / 2, len(full.lines) - 25} {
		end := min(start+25, len(full.lines))
		got, err := full.source.lines(context.Background(), start, end)
		if err != nil || !slices.Equal(got, full.lines[start:end]) {
			t.Fatalf("linked media window %d:%d differs: %v", start, end, err)
		}
	}
	for i, line := range s.readerLines {
		if ansi.Strip(line) != ansi.Strip(full.lines[i]) {
			t.Fatalf("preview row %d differs from completed content", i)
		}
	}
	s.readerRenderFinished(full)
	if s.readerRendering || s.readerApproximate || s.snapshot.Body != body || s.readerCache == nil {
		t.Fatal("linked media render did not complete without changing the source")
	}
}

func TestDocumentReaderStreamsLaterImagesWithTheirBatches(t *testing.T) {
	documentImageEnv(t, "none")
	first := documentTestImage(t)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 24, 12))); err != nil {
		t.Fatal(err)
	}
	second, err := artifact.NewImage(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	paragraphs := strings.Repeat("Paragraph with enough words to fill a row.\n\n", 1200)
	body := paragraphs + "![First](" + first.Name + ")\n\n" + paragraphs +
		"![Second](" + second.Name + ")\n\n" + paragraphs
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.backgroundReader = 80, 14, true, true
	s.snapshot = document.Document{Body: body, Images: []artifact.Image{first, second}, Layout: document.DefaultLayout()}
	result := s.requestReaderRender()()
	if early, ok := result.(documentReaderEarlyMsg); ok {
		result = s.readerEarlyFinished(early)()
	}
	next := s.readerIndexFinished(result.(documentReaderIndexMsg))
	for {
		switch msg := next().(type) {
		case documentReaderPreviewMsg:
			next = s.readerPreviewFinished(msg)
			for _, line := range msg.lines {
				for _, marker := range line {
					if marker >= 0xf0000 {
						if _, ok := s.imageState.rows[marker]; !ok {
							t.Fatalf("image marker %U arrived without its pixels", marker)
						}
					}
				}
			}
		case documentReaderRenderMsg:
			if msg.err != nil || len(s.imageState.rows) < 2 {
				t.Fatalf("streamed image rows=%d, error=%v", len(s.imageState.rows), msg.err)
			}
			s.readerRenderFinished(msg)
			return
		default:
			t.Fatalf("unexpected render result %T", msg)
		}
	}
}

func TestDocumentReaderProgressWindowScrollsAcrossBatches(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.readerRendering = 60, 15, true, true
	s.readerRenderSession = 1
	s.readerPreambleShown, s.readerStreamPreview = true, true
	s.reader.SetWidth(50)
	s.reader.SetHeight(10)
	for i := 0; i < 2000; i++ {
		s.readerLines = append(s.readerLines, fmt.Sprintf("line %04d", i))
	}
	s.showReaderContent()
	for i := 0; i < 130; i++ {
		s.updateReaderKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	offset := s.readerPreviewBase + s.reader.YOffset()
	if offset != 1300 || s.readerPreviewBase == 0 || s.reader.TotalLineCount() > readerPreviewWindowLines+1 {
		t.Fatalf("offset=%d base=%d window=%d", offset, s.readerPreviewBase, s.reader.TotalLineCount())
	}
	more := make([]string, 500)
	for i := range more {
		more[i] = fmt.Sprintf("line %04d", 2000+i)
	}
	next := make(chan tea.Msg, 1)
	s.readerPreviewFinished(documentReaderPreviewMsg{session: 1, lines: more, next: next})
	if s.readerPreviewBase+s.reader.YOffset() != offset || !strings.Contains(s.readerContentView(), "line 1300") {
		t.Fatal("appending a batch changed the visible position")
	}
	full := append([]string(nil), s.readerLines...)
	s.readerRenderFinished(documentReaderRenderMsg{session: 1, lines: full})
	if s.reader.YOffset() != offset || s.readerRendering {
		t.Fatalf("completed reader offset=%d, want %d", s.reader.YOffset(), offset)
	}
}

func TestDocumentReaderCancelsBlockedProgressPublisher(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.backgroundReader = 80, 14, true, true
	s.snapshot = document.Document{
		Body:   strings.Repeat("Paragraph with enough words to fill a row.\n\n", 4000),
		Layout: document.DefaultLayout(),
	}
	first := s.requestReaderRender()()
	if early, ok := first.(documentReaderEarlyMsg); ok {
		first = s.readerEarlyFinished(early)()
	}
	render := s.readerIndexFinished(first.(documentReaderIndexMsg))
	preview, ok := render().(documentReaderPreviewMsg)
	if !ok {
		t.Fatal("expected a progress message")
	}
	s.cancelReaderRender()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case result := <-preview.next:
			if final, ok := result.(documentReaderRenderMsg); ok {
				if !errors.Is(final.err, context.Canceled) {
					t.Fatalf("final error = %v", final.err)
				}
				return
			}
		case <-deadline:
			t.Fatal("canceled publisher did not finish")
		}
	}
}

func TestEarlyReaderPrefixIsBounded(t *testing.T) {
	body := strings.Repeat("line\n", 600)
	if prefix := earlyReaderPrefix(body); strings.Count(prefix, "\n") != 500 {
		t.Fatalf("prefix has %d lines", strings.Count(prefix, "\n"))
	}
	body = strings.Repeat("界", maxEarlyReaderBytes)
	if prefix := earlyReaderPrefix(body); len(prefix) > maxEarlyReaderBytes || !utf8.ValidString(prefix) {
		t.Fatalf("prefix is %d bytes or invalid UTF-8", len(prefix))
	}
}

func TestDocumentReaderRenderFailureDiscardsPartialPreview(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading = 80, 24, true
	s.snapshot = document.Document{Body: "First paragraph.\n\nSecond paragraph.", Layout: document.DefaultLayout()}
	s.showReaderPreamble()
	if !s.readerPreambleShown {
		t.Fatal("expected a preview")
	}
	s.readerRendering = true
	s.readerRenderSession = 1
	s.readerRows = []int{0}
	s.readerRowIndex = []readerSectionRow{{row: 0, section: 0, highestSection: 0}}
	s.readerSection = 0
	s.readerQueuedInput = []tea.Msg{tea.KeyPressMsg{Code: 'G', Text: "G"}}
	s.readerRenderFinished(documentReaderRenderMsg{session: 1, err: errReaderRenderLimit})
	if len(s.readerLines) != 0 || s.readerRendering || len(s.readerQueuedInput) != 0 ||
		len(s.readerRows) != 0 || len(s.readerRowIndex) != 0 || s.readerSection != -1 {
		t.Fatal("failed render retained a partial preview or queued navigation")
	}
	if !strings.Contains(s.readerStatus(), errReaderRenderLimit.Error()) ||
		s.snapshot.Body != "First paragraph.\n\nSecond paragraph." {
		t.Fatal("render failure was not displayed or changed the source")
	}
}

func TestDocumentReaderDiscardsStaleOutline(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.backgroundReader = 80, 24, true, true
	s.snapshot = document.Document{Body: "# Old\n\nText.", Layout: document.DefaultLayout()}
	first := s.requestReaderRender()
	s.snapshot.Body = "# New\n\nText."
	if cmd := s.requestReaderRender(); cmd != nil {
		t.Fatal("reload launched a concurrent index")
	}
	next := s.readerIndexFinished(first().(documentReaderIndexMsg))
	if next == nil || len(s.outline) != 0 {
		t.Fatal("stale index was accepted or not replaced")
	}
	render := s.readerIndexFinished(next().(documentReaderIndexMsg))
	if render == nil || len(s.outline) != 1 || s.outline[0].Title != "New" {
		t.Fatalf("latest outline was not published: %+v", s.outline)
	}
	s.readerRenderFinished(render().(documentReaderRenderMsg))
	if s.readerRendering || len(s.outline) != 1 || s.outline[0].Title != "New" {
		t.Fatal("latest render was not accepted")
	}
}

func TestDocumentReaderCoalescesResizeAfterIndex(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.backgroundReader = 80, 24, true, true
	s.snapshot = document.Document{Body: "# Heading\n\nText.", Layout: document.DefaultLayout()}
	index := s.requestReaderRender()().(documentReaderIndexMsg)
	render := s.readerIndexFinished(index)
	s.width = 100
	if cmd := s.requestReaderRender(); cmd != nil {
		t.Fatal("resize launched a concurrent render")
	}
	stale := render().(documentReaderRenderMsg)
	if !errors.Is(stale.err, context.Canceled) {
		t.Fatalf("stale render returned %v", stale.err)
	}
	next := s.readerRenderFinished(stale)
	if next == nil {
		t.Fatal("stale render did not schedule the latest index")
	}
	latest := s.readerIndexFinished(next().(documentReaderIndexMsg))
	s.readerRenderFinished(latest().(documentReaderRenderMsg))
	if s.readerRendering || s.reader.Width() <= index.width {
		t.Fatal("latest render did not use the resized viewport")
	}
}

func TestDocumentReaderScrollsEarlyOutline(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.backgroundReader = 80, 24, true, true
	var body strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&body, "# Heading %d\n\nText.\n\n", i)
	}
	s.snapshot = document.Document{Body: body.String(), Layout: document.DefaultLayout()}
	index := s.requestReaderRender()().(documentReaderIndexMsg)
	render := s.readerIndexFinished(index)
	s.updateReaderMouse(mouseEvent{X: 3, Y: 5, Action: mousePress, Button: mouseWheelDown})
	if !s.readerOutlineManual || s.readerOutlineStart() != 3 || len(s.readerQueuedInput) != 0 {
		t.Fatal("outline wheel input waited for the document render")
	}
	s.updateReaderMouse(mouseEvent{X: 3, Y: 4, Action: mousePress, Button: mouseLeft})
	s.readerRenderFinished(render().(documentReaderRenderMsg))
	if s.currentSection() != 3 || s.reader.YOffset() != s.readerRows[3] {
		t.Fatalf("early scrolled-outline click landed at section %d", s.currentSection())
	}
}

func TestDocumentReaderPreviewScrollReplaysAfterResize(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading = 60, 10, true
	s.snapshot = document.Document{
		Body:   strings.Repeat("Plain paragraph with several words.\n\n", 18) + "Final text.",
		Layout: document.DefaultLayout(),
	}
	s.showReaderPreamble()
	first := s.requestReaderRender()
	s.updateReaderKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	s.updateReaderMouse(mouseEvent{X: 4, Y: 4, Action: mousePress, Button: mouseWheelDown})
	if s.reader.YOffset() != 4 || s.readerPreviewInputs != 2 {
		t.Fatalf("preview scroll offset=%d inputs=%d, want 4 and 2", s.reader.YOffset(), s.readerPreviewInputs)
	}
	s.width = 44
	if cmd := s.requestReaderRender(); cmd != nil {
		t.Fatal("resize started a concurrent render")
	}
	if s.reader.YOffset() != 4 || s.readerPreviewStale {
		t.Fatalf("preview position after resize: %d, stale=%t", s.reader.YOffset(), s.readerPreviewStale)
	}
	next := s.readerRenderFinished(first().(documentReaderRenderMsg))
	if next == nil {
		t.Fatal("resize did not start a replacement render")
	}
	s.readerRenderFinished(next().(documentReaderRenderMsg))
	if s.reader.YOffset() != 4 || len(s.readerQueuedInput) != 0 || s.readerPreviewInputs != 0 {
		t.Fatalf("completed offset=%d queued=%d preview=%d", s.reader.YOffset(), len(s.readerQueuedInput), s.readerPreviewInputs)
	}
}

func TestDocumentReaderPreviewQueuesScrollBeyondKnownContent(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading = 60, 10, true
	s.snapshot = document.Document{
		Body:   strings.Repeat("Plain paragraph.\n\n", 20) + "Final text.",
		Layout: document.DefaultLayout(),
	}
	s.showReaderPreamble()
	cmd := s.requestReaderRender()
	s.updateReaderKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	s.updateReaderKey(tea.KeyPressMsg{Code: 'G', Text: "G"})
	s.updateReaderKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if s.reader.YOffset() != 1 || s.readerPreviewInputs != 1 || len(s.readerQueuedInput) != 3 {
		t.Fatalf("preview applied queued input out of order: offset=%d applied=%d queued=%d",
			s.reader.YOffset(), s.readerPreviewInputs, len(s.readerQueuedInput))
	}
	s.readerRenderFinished(cmd().(documentReaderRenderMsg))
	if s.reader.YOffset() != s.reader.TotalLineCount()-s.reader.Height()-1 {
		t.Fatalf("queued end then up landed at %d", s.reader.YOffset())
	}
}

func TestDocumentReaderPreviewQueuesAfterHeightResize(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading = 60, 10, true
	s.snapshot = document.Document{
		Body:   strings.Repeat("Plain paragraph.\n\n", 20) + "Final text.",
		Layout: document.DefaultLayout(),
	}
	s.showReaderPreamble()
	cmd := s.requestReaderRender()
	s.updateReaderKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if s.reader.YOffset() != 5 {
		t.Fatalf("preview page down landed at %d", s.reader.YOffset())
	}
	s.height = 40
	if resize := s.resizeReader(); resize != nil || !s.readerPreviewStale {
		t.Fatal("height resize did not defer the no-longer-visible preview movement")
	}
	s.updateReaderKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if s.readerPreviewInputs != 1 || len(s.readerQueuedInput) != 2 {
		t.Fatal("input after a stale preview was applied out of order")
	}
	s.readerRenderFinished(cmd().(documentReaderRenderMsg))
	if s.reader.YOffset() != s.reader.TotalLineCount()-s.reader.Height()-1 {
		t.Fatalf("resized queued navigation landed at %d", s.reader.YOffset())
	}
}

func TestDocumentReaderPreviewScrollDoesNotDelayClosing(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading = 60, 10, true
	s.snapshot = document.Document{
		Body:   strings.Repeat("Plain paragraph.\n\n", 20) + "Final text.",
		Layout: document.DefaultLayout(),
	}
	s.showReaderPreamble()
	s.requestReaderRender()
	s.updateReaderKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if cmd := s.updateReaderKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); cmd == nil || s.readerRendering {
		t.Fatal("closing after preview scroll waited for the render")
	}
}

func TestDocumentReaderPendingNavigationDoesNotDelayClosing(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading = 60, 10, true
	s.snapshot = document.Document{Body: strings.Repeat("Paragraph.\n\n", 20), Layout: document.DefaultLayout()}
	s.showReaderPreamble()
	s.requestReaderRender()
	s.updateReaderKey(tea.KeyPressMsg{Code: 'G', Text: "G"})
	if cmd := s.updateReaderKey(tea.KeyPressMsg{Code: 'q', Text: "q"}); cmd == nil || s.readerRendering {
		t.Fatal("closing after pending navigation waited for the render")
	}
}

func TestDocumentReaderPendingFindKeepsEscapeInOrder(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.reading, s.readerRendering = true, true
	s.updateReaderKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	s.updateReaderKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !s.readerRendering || len(s.readerQueuedInput) != 2 {
		t.Fatal("escape bypassed pending find input")
	}
}

func TestDocumentReaderCompactsAppliedPreviewScrolls(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.readerRendering, s.readerPreambleShown = 60, 10, true, true, true
	s.reader.SetWidth(56)
	s.reader.SetHeight(5)
	lines := make([]string, 2000)
	s.readerLines = lines
	s.showReaderContent()
	for range 700 {
		s.queueReaderInput(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	for range 400 {
		s.queueReaderInput(tea.KeyPressMsg{Code: 'k', Text: "k"})
	}
	if offset := s.readerPreviewBase + s.reader.YOffset(); offset != 300 || s.errMessage != "" {
		t.Fatalf("preview offset=%d error=%q", offset, s.errMessage)
	}
	s.reader.SetHeight(8)
	s.replayReaderPreview()
	if offset := s.readerPreviewBase + s.reader.YOffset(); offset != 300 || s.readerPreviewStale {
		t.Fatalf("replayed preview offset=%d stale=%t", offset, s.readerPreviewStale)
	}
	s.readerRenderFinished(documentReaderRenderMsg{session: s.readerRenderSession, lines: lines})
	if s.reader.YOffset() != 300 || len(s.readerQueuedInput) != 0 {
		t.Fatalf("completed offset=%d queued=%d", s.reader.YOffset(), len(s.readerQueuedInput))
	}
}

func TestLargeDocumentReaderUsesBoundedViewport(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.readerRendering, s.backgroundReader = 60, 11, true, true, true
	s.reader.SetWidth(56)
	s.reader.SetHeight(6)
	s.snapshot = document.Document{Body: strings.Repeat("x", asyncReaderSearchBytes)}
	lines := make([]string, 5000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %04d", i)
	}
	lines[3000] = "unique search term"
	const marker = '\U000f0000'
	lines[3200] = strings.Repeat(string(marker), 2)
	lines[4000] = "return link"
	outline := []document.Section{{Title: "First", Slug: "first"}, {Title: "Far", Slug: "far"}}
	rows := []int{0, 4500}
	rowIndex, err := indexReaderSectionRows(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	s.readerRenderFinished(documentReaderRenderMsg{
		session: s.readerRenderSession, lines: lines, outline: outline, rows: rows, rowIndex: rowIndex,
		links: []linkHitBox{{row: 4000, startCol: 0, endCol: 6, target: "first"}},
		images: documentImageState{rows: map[rune]documentImageRow{
			marker: {pixels: image.NewNRGBA(image.Rect(0, 0, 16, 16)), width: 2},
		}},
	})
	if !s.readerWindowed || s.reader.TotalLineCount() > readerPreviewWindowLines {
		t.Fatalf("completed viewport is not bounded: %d lines", s.reader.TotalLineCount())
	}
	for range 1800 {
		s.updateReaderKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	if s.readerOffset() != 1800 || !strings.Contains(s.readerContentView(), "line 1800") {
		t.Fatalf("scrolled to %d", s.readerOffset())
	}
	s.gotoSection(1)
	if s.readerOffset() != 4500 || s.currentSection() != 1 || !strings.Contains(s.readerContentView(), "line 4500") {
		t.Fatalf("section jump landed at %d, section %d", s.readerOffset(), s.currentSection())
	}
	s.find.visible = true
	s.find.input.SetValue("unique")
	search := s.searchReader("unique")
	s.readerSearchFinished(search().(documentReaderSearchMsg))
	if s.readerOffset() != 3000 || !strings.Contains(ansi.Strip(s.readerContentView()), "unique search term") {
		t.Fatalf("search landed at %d", s.readerOffset())
	}
	s.find.visible = false
	s.setReaderOffset(3200)
	if _, placements := s.documentImagePlacements(s.readerContentView(), s.reader.Width()); len(placements) != 1 || placements[0].y != 0 {
		t.Fatalf("windowed image placements = %+v", placements)
	}
	s.setReaderOffset(4000)
	s.updateReaderMouse(mouseEvent{X: 2, Y: 3, Action: mousePress, Button: mouseLeft})
	if s.readerOffset() != 0 {
		t.Fatalf("link landed at %d", s.readerOffset())
	}
	s.updateReaderKey(tea.KeyPressMsg{Code: tea.KeyEnd})
	if s.readerOffset() != len(lines)-s.reader.Height() {
		t.Fatalf("end landed at %d", s.readerOffset())
	}
	s.updateReaderMouse(mouseEvent{X: 5, Y: 4, Action: mousePress, Button: mouseWheelUp})
	if s.readerOffset() != len(lines)-s.reader.Height()-s.reader.MouseWheelDelta {
		t.Fatalf("wheel landed at %d", s.readerOffset())
	}
	s.updateReaderKey(tea.KeyPressMsg{Code: tea.KeyHome})
	if s.readerOffset() != 0 || s.reader.TotalLineCount() > readerPreviewWindowLines {
		t.Fatalf("home landed at %d with %d viewport lines", s.readerOffset(), s.reader.TotalLineCount())
	}
}

func TestLargeDocumentReaderReplaysPreviewScrollIntoWindow(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.readerRendering, s.backgroundReader = 60, 10, true, true, true
	s.readerPreambleShown = true
	s.reader.SetWidth(56)
	s.reader.SetHeight(5)
	s.readerLines = make([]string, 2000)
	s.showReaderContent()
	for range 1300 {
		s.queueReaderInput(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	s.readerRenderFinished(documentReaderRenderMsg{session: s.readerRenderSession, lines: make([]string, 5000)})
	if got := s.readerOffset(); got != 1300 || s.reader.TotalLineCount() > readerPreviewWindowLines {
		t.Fatalf("completed offset=%d, viewport lines=%d", got, s.reader.TotalLineCount())
	}
}

func TestLargeDocumentReaderPreservesEditorLocation(t *testing.T) {
	for _, action := range []string{"keyboard", "click"} {
		t.Run(action, func(t *testing.T) {
			s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
			s.width, s.height, s.reading, s.backgroundReader, s.readerWindowed = 60, 24, true, true, true
			s.reader.SetWidth(56)
			s.reader.SetHeight(19)
			lines := make([]string, 2000)
			for i := range lines {
				lines[i] = fmt.Sprintf("unique%04d content", i)
			}
			s.snapshot = document.Document{Body: strings.Join(lines, "\n"), Layout: document.DefaultLayout()}
			s.readerLines = lines
			s.showReaderContent()
			s.setReaderOffset(1300)
			if action == "keyboard" {
				s.updateReaderKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
				if got := s.body.Cursor.Line; got != 1300 {
					t.Fatalf("editor cursor line=%d, want 1300", got)
				}
			} else {
				s.editAtReaderLocation(1302, 0)
				if got := s.body.Cursor.Line; got != 1302 || s.body.TopLine != 1300 {
					t.Fatalf("editor cursor line=%d, top=%d, want 1302 and 1300", got, s.body.TopLine)
				}
			}
		})
	}
}

func TestLargeDocumentReaderPreservesWindowAcrossReflowAndFailure(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.backgroundReader, s.readerWindowed = 60, 10, true, true, true
	s.reader.SetWidth(56)
	s.reader.SetHeight(5)
	s.readerLines = make([]string, 5000)
	s.outline = []document.Section{{Title: "First", Slug: "first"}}
	s.readerRows = []int{0}
	s.readerRowIndex, _ = indexReaderSectionRows(context.Background(), s.readerRows)
	s.showReaderContent()
	s.setReaderOffset(1300)
	s.readerRendering = true
	s.readerRenderFinished(documentReaderRenderMsg{session: s.readerRenderSession, err: errors.New("render failed")})
	if s.readerOffset() != 1300 || !s.readerWindowed {
		t.Fatal("failed reflow discarded the previous window position")
	}
	s.readerRendering = true
	rows := []int{50}
	rowIndex, _ := indexReaderSectionRows(context.Background(), rows)
	s.readerRenderFinished(documentReaderRenderMsg{
		session: s.readerRenderSession, lines: make([]string, 5000), outline: s.outline, rows: rows, rowIndex: rowIndex,
	})
	if got := s.readerOffset(); got != 1350 {
		t.Fatalf("reflow offset=%d, want 1350", got)
	}
}

func TestDocumentReaderCompactionPreservesPendingInput(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.readerRendering, s.readerPreambleShown = 60, 10, true, true, true
	s.reader.SetWidth(56)
	s.reader.SetHeight(5)
	lines := make([]string, 2000)
	s.readerLines = lines
	s.showReaderContent()
	for range 200 {
		s.queueReaderInput(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	s.queueReaderInput(tea.KeyPressMsg{Code: 'G', Text: "G"})
	for range 55 {
		s.queueReaderInput(tea.KeyPressMsg{Code: 'k', Text: "k"})
	}
	s.queueReaderInput(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if s.errMessage != "" {
		t.Fatalf("input was lost: %s", s.errMessage)
	}
	s.readerRenderFinished(documentReaderRenderMsg{session: s.readerRenderSession, lines: lines})
	if got, want := s.reader.YOffset(), len(lines)-s.reader.Height()-56; got != want {
		t.Fatalf("completed offset=%d, want %d", got, want)
	}
}

func TestDocumentReaderCompactedPageAndWheelReplay(t *testing.T) {
	for _, tc := range []struct {
		name        string
		input       tea.Msg
		wantBefore  int
		wantResized int
	}{
		{"page", tea.KeyPressMsg{Code: tea.KeyPgDown}, 1350, 1620},
		{"wheel", readerQueuedWheel{event: mouseEvent{Button: mouseWheelDown}}, 810, 810},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
			s.width, s.height, s.reading, s.readerRendering, s.readerPreambleShown = 60, 10, true, true, true
			s.reader.SetWidth(56)
			s.reader.SetHeight(5)
			lines := make([]string, 2000)
			s.readerLines = lines
			s.showReaderContent()
			for range 270 {
				s.queueReaderInput(tc.input)
			}
			if got := s.readerPreviewBase + s.reader.YOffset(); got != tc.wantBefore {
				t.Fatalf("preview offset=%d, want %d", got, tc.wantBefore)
			}
			s.reader.SetHeight(6)
			s.replayReaderPreview()
			if got := s.readerPreviewBase + s.reader.YOffset(); got != tc.wantResized || s.readerPreviewStale {
				t.Fatalf("resized preview offset=%d stale=%t, want %d", got, s.readerPreviewStale, tc.wantResized)
			}
			s.readerRenderFinished(documentReaderRenderMsg{session: s.readerRenderSession, lines: lines})
			if got := s.reader.YOffset(); got != tc.wantResized {
				t.Fatalf("completed offset=%d, want %d", got, tc.wantResized)
			}
		})
	}
}

func TestDocumentReaderCompactsPendingScrollAndAdvancesWithBatches(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading, s.readerRendering, s.readerPreambleShown = 60, 10, true, true, true
	s.readerProvisional = true
	s.reader.SetWidth(56)
	s.reader.SetHeight(5)
	for range 700 {
		s.queueReaderInput(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	if s.errMessage != "" || len(s.readerQueuedInput) >= maxQueuedReaderInput {
		t.Fatalf("pending scrolls were not compacted: %q, %d entries", s.errMessage, len(s.readerQueuedInput))
	}
	s.readerPreviewFinished(documentReaderPreviewMsg{session: s.readerRenderSession, lines: make([]string, 200)})
	if got, want := s.readerPreviewBase+s.reader.YOffset(), 196; got != want {
		t.Fatalf("first batch offset=%d, want %d", got, want)
	}
	s.readerPreviewFinished(documentReaderPreviewMsg{session: s.readerRenderSession, lines: make([]string, 400)})
	if got, want := s.readerPreviewBase+s.reader.YOffset(), 596; got != want {
		t.Fatalf("second batch offset=%d, want %d", got, want)
	}
	s.readerRenderFinished(documentReaderRenderMsg{session: s.readerRenderSession, lines: make([]string, 2000)})
	if got := s.reader.YOffset(); got != 700 {
		t.Fatalf("completed offset=%d, want 700", got)
	}
}

func TestDocumentReaderDoesNotCompactPendingFindInput(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.readerRendering = true
	s.queueReaderInput(tea.KeyPressMsg{Code: '/', Text: "/"})
	for range maxQueuedReaderInput - 1 {
		s.queueReaderInput(tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	s.queueReaderInput(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if len(s.readerQueuedInput) != maxQueuedReaderInput || s.errMessage == "" {
		t.Fatal("find text was incorrectly compacted as navigation")
	}
}

func TestDocumentReaderReflowRestoresSearchAndQueuedNext(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading = 80, 10, true
	s.snapshot = document.Document{Body: strings.Repeat("x", asyncReaderSearchBytes), Layout: document.DefaultLayout()}
	s.reader.SetWidth(30)
	s.reader.SetHeight(5)
	lines := strings.Split("beta first\nfiller\nfiller\nfiller\nfiller\nfiller\nbeta second\nfiller\nfiller\nfiller\nfiller\nfiller\nbeta third\nfiller\nfiller", "\n")
	s.readerLines = lines
	s.showReaderContent()
	s.find.visible = true
	s.find.input.SetValue("beta")
	s.rebuildReaderMatches("beta")
	s.readerMatch = 1
	s.gotoReaderMatch()
	s.requestReaderRender()
	s.updateReaderKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	cmd := s.readerRenderFinished(documentReaderRenderMsg{
		session: s.readerRenderSession,
		lines:   lines,
	})
	if cmd == nil {
		t.Fatal("expected asynchronous search")
	}
	s.update(cmd())
	if s.readerSearching || s.readerMatch != 2 || s.reader.YOffset() != 10 {
		t.Fatalf("search navigation was not restored: searching=%t match=%d offset=%d",
			s.readerSearching, s.readerMatch, s.reader.YOffset())
	}
}

func TestDocumentReaderDiscardsRenderAfterClose(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading = 80, 24, true
	s.snapshot = document.Document{Body: "First paragraph.\n\n# Later", Layout: document.DefaultLayout()}
	cmd := s.requestReaderRender()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	s.close()
	msg := (<-done).(documentReaderRenderMsg)
	if next := s.readerRenderFinished(msg); next != nil || s.readerRendering || len(s.readerLines) != 0 {
		t.Fatal("closed reader accepted or restarted a stale render")
	}
}

func TestReaderSectionRowIndexPreservesSelection(t *testing.T) {
	rows := []int{10, 5, 10, -1, 6}
	index, err := indexReaderSectionRows(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.readerRows, s.readerRowIndex = rows, index
	s.readerSection = -1
	s.reader.SetHeight(1)
	s.reader.SetContentLines(make([]string, 20))
	for _, tt := range []struct {
		offset, current, atRow int
	}{
		{0, -1, -1},
		{4, 1, -1},
		{5, 4, 1},
		{9, 4, 4},
		{10, 4, 2},
	} {
		s.reader.SetYOffset(tt.offset)
		if got := s.currentSection(); got != tt.current {
			t.Errorf("currentSection at %d = %d, want %d", tt.offset, got, tt.current)
		}
		if got := s.sectionAtReaderRow(tt.offset); got != tt.atRow {
			t.Errorf("sectionAtReaderRow(%d) = %d, want %d", tt.offset, got, tt.atRow)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := indexReaderSectionRows(ctx, rows); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled index error = %v", err)
	}
}

func TestDocumentReaderRenderHonorsCancellation(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading = 80, 24, true
	s.snapshot = document.Document{Body: "# Heading\n\ntext", Layout: document.DefaultLayout()}
	cmd := s.requestReaderRender()
	s.cancelReaderRender()
	msg := cmd().(documentReaderRenderMsg)
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("cancelled render returned %v", msg.err)
	}
}

func TestDocumentReaderCancelsRunningRender(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading = 80, 24, true
	s.snapshot = document.Document{Body: strings.Repeat("# Heading\n\n", 100000), Layout: document.DefaultLayout()}
	cmd := s.requestReaderRender()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	s.cancelReaderRender()
	select {
	case result := <-done:
		if err := result.(documentReaderRenderMsg).err; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled render returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled render did not stop at a checkpoint")
	}
}

func TestDocumentEditorImageListDoesNotMutateReaderSnapshot(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	doc := document.Document{
		Body: "Document.", Layout: document.DefaultLayout(),
		Images: []artifact.Image{{Name: "original.png", Data: []byte("unchanged")}},
	}
	s.startReading(doc)
	if _, ok := s.startEdit(doc); !ok {
		t.Fatal(s.errMessage)
	}
	s.images[0] = artifact.Image{Name: "replacement.png"}
	if doc.Images[0].Name != "original.png" || s.snapshot.Images[0].Name != "original.png" {
		t.Fatal("editing the image list mutated the reader snapshot")
	}
}

func TestDocumentReaderQueuesFindInputDuringRender(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.width, s.height, s.reading = 80, 24, true
	s.snapshot = document.Document{Body: "First paragraph.\n\nSecond paragraph.", Layout: document.DefaultLayout()}
	cmd := s.requestReaderRender()
	for _, key := range []tea.KeyPressMsg{
		{Code: '/', Text: "/"}, {Code: 'q', Text: "q"}, {Code: tea.KeyEsc},
	} {
		s.updateReaderKey(key)
	}
	if len(s.readerQueuedInput) != 3 {
		t.Fatal("find keys were not queued in order")
	}
	s.readerRenderFinished(cmd().(documentReaderRenderMsg))
	if !s.reading || s.find.visible || s.find.query() != "" {
		t.Fatal("queued find input did not close the find bar")
	}
}

func TestDocumentReaderQueuesLinkTargetDuringReflow(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.resize(80, 24)
	doc := document.Document{
		Body:   "# Start\n\n[Jump](#target)\n\n" + strings.Repeat("Paragraph.\n\n", 40) + "# Target\n\n" + strings.Repeat("Last paragraph.\n\n", 20),
		Layout: document.DefaultLayout(),
	}
	s.startReading(doc)
	if len(s.readerLinks) != 1 {
		t.Fatal("expected a reader link")
	}
	box := s.readerLinks[0]
	outlineWidth, _ := s.readerPaneWidths()
	cmd := s.requestReaderRender()
	s.updateReaderMouse(mouseEvent{X: outlineWidth + 1 + box.startCol, Y: 3 + box.row, Action: mousePress, Button: mouseLeft})
	s.readerRenderFinished(cmd().(documentReaderRenderMsg))
	for i, section := range s.outline {
		if section.Slug == "target" {
			if s.reader.YOffset() != s.readerRows[i] {
				t.Fatalf("queued link landed at %d, want %d", s.reader.YOffset(), s.readerRows[i])
			}
			return
		}
	}
	t.Fatal("target heading missing")
}

func TestDocumentReaderQueuesDuplicateOutlineAnchor(t *testing.T) {
	s := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	s.resize(80, 24)
	doc := document.Document{
		Body:   "<a id=\"same\"></a>\n\nFirst.\n\n<a id=\"same\"></a>\n\n" + strings.Repeat("More.\n\n", 30),
		Layout: document.DefaultLayout(),
	}
	s.startReading(doc)
	if len(s.outline) != 2 || s.outline[0].Slug != "same" || s.outline[1].Slug != "same" {
		t.Fatalf("unexpected outline: %+v", s.outline)
	}
	cmd := s.requestReaderRender()
	s.updateReaderMouse(mouseEvent{X: 3, Y: 5, Action: mousePress, Button: mouseLeft})
	s.readerRenderFinished(cmd().(documentReaderRenderMsg))
	if s.currentSection() != 1 || s.reader.YOffset() != s.readerRows[1] {
		t.Fatal("queued outline click did not preserve the duplicate anchor")
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
