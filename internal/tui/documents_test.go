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

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

// newDocumentHarness opens the editor on a document file, creating it with
// draft first when draft has a body.
func newDocumentHarness(t *testing.T, draft document.Draft) *harness {
	t.Helper()
	h := newHarness(t, "")
	if draft.Body != "" {
		if _, err := h.docs.Create(context.Background(), h.path, draft); err != nil {
			t.Fatal(err)
		}
	}
	h.open(artifact.DocumentKind)
	if h.m.stage != stageDocument {
		t.Fatalf("stage = %d, error = %q", h.m.stage, h.m.errMessage)
	}
	return h
}

func TestDocumentEditorHasToolbarAndSavesLayout(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	view := h.m.View()
	for _, label := range []string{"Save", "Propose", "Changes", "Preview", "B", "H1", "Page break", "Section", "Cols: 1", "Margins: Normal", "Header", "Footer", "Image"} {
		if !strings.Contains(view, label) {
			t.Fatalf("editor toolbar is missing %q:\n%s", label, view)
		}
	}
	s := h.m.documents
	h.typeText("Intro")
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true})
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

func TestDocumentProposeReviewAndAccept(t *testing.T) {
	ctx := context.Background()
	h := newDocumentHarness(t, document.Draft{Body: "before", Layout: document.DefaultLayout()})
	s := h.m.documents
	h.key("f3")
	if !strings.Contains(s.status, "Edit the document first") || s.showChanges {
		t.Fatalf("propose without edits: status=%q", s.status)
	}
	s.body.SetValue("after")
	h.key("f3")
	if !s.showChanges || len(s.changes) != 1 || s.changes[0].Status != document.ChangePending {
		t.Fatalf("proposal was not recorded for review: %+v (error %q)", s.changes, s.errMessage)
	}
	if s.dirty() || s.body.Value() != "before" {
		t.Fatalf("editor did not return to the saved document: body %q", s.body.Value())
	}
	live, err := h.docs.Get(ctx, h.path)
	if err != nil || live.Body != "before" {
		t.Fatalf("proposal modified the live document: %+v, %v", live, err)
	}
	h.key("enter")
	if !s.reviewing || !strings.Contains(h.m.View(), "Proposed Markdown") {
		t.Fatal("proposal review did not display its before/after content")
	}
	h.key("a")
	accepted, err := h.docs.Get(ctx, h.path)
	if err != nil || accepted.Body != "after" {
		t.Fatalf("accepted document = %+v, %v", accepted, err)
	}
	if len(s.changes) != 1 || s.changes[0].Status != document.ChangeAccepted {
		t.Fatalf("accepted proposal state = %+v", s.changes)
	}
	if s.body.Value() != "after" {
		t.Fatalf("editor not refreshed after acceptance: %q", s.body.Value())
	}
	h.key("esc")
	h.key("esc")
	if s.showChanges || h.quit {
		t.Fatalf("Esc from review: showChanges=%t quit=%t", s.showChanges, h.quit)
	}
}

func TestDocumentAcceptRefusedWithUnsavedEdits(t *testing.T) {
	ctx := context.Background()
	h := newDocumentHarness(t, document.Draft{Body: "before", Layout: document.DefaultLayout()})
	before, err := h.docs.Get(ctx, h.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.docs.Propose(ctx, before, "Other", document.Draft{Body: "proposed", Layout: before.Layout}); err != nil {
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
	if d, _ := h.docs.Get(ctx, h.path); d.Body != "before" {
		t.Fatalf("proposal accepted over unsaved edits: %q", d.Body)
	}
	h.key("r")
	changes, err := h.docs.Changes(ctx, h.path)
	if err != nil || changes[0].Status != document.ChangeRejected {
		t.Fatalf("reject with unsaved edits = %+v, %v", changes, err)
	}
	if s.body.Value() != "local edit" {
		t.Fatalf("reject discarded unsaved edits: %q", s.body.Value())
	}
}

func TestDocumentChangeReviewScrollsAndBoundsChangeList(t *testing.T) {
	ctx := context.Background()
	h := newDocumentHarness(t, document.Draft{Body: strings.Repeat("current line\n", 80) + "CURRENT-LAST"})
	created, err := h.docs.Get(ctx, h.path)
	if err != nil {
		t.Fatal(err)
	}
	change, err := h.docs.Propose(ctx, created, "Long proposal", document.Draft{
		Body: strings.Repeat("proposed line\n", 80) + "PROPOSED-LAST", Layout: created.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := h.m.documents
	s.changes, s.selectedChange, s.showChanges, s.reviewing = []document.Change{change}, 0, true, true
	s.resize(100, 12)
	s.setChangeReviewContent()
	view := h.m.View()
	if strings.Contains(view, "CURRENT-LAST") || strings.Contains(view, "PROPOSED-LAST") {
		t.Fatalf("review showed content beyond the initial viewport:\n%s", view)
	}
	sawCurrent, sawProposed := false, false
	for i := 0; i < 100 && !(sawCurrent && sawProposed); i++ {
		h.key("pgdown")
		view = h.m.View()
		sawCurrent = sawCurrent || strings.Contains(view, "CURRENT-LAST")
		sawProposed = sawProposed || strings.Contains(view, "PROPOSED-LAST")
	}
	if !sawCurrent || !sawProposed {
		t.Fatal("scrolling did not reveal both document bodies")
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
	ctx := context.Background()
	h := newDocumentHarness(t, document.Draft{Body: strings.Repeat("c", 90) + "CURRENT-END"})
	created, err := h.docs.Get(ctx, h.path)
	if err != nil {
		t.Fatal(err)
	}
	change, err := h.docs.Propose(ctx, created, "Long proposal", document.Draft{
		Body: strings.Repeat("p", 90) + "PROPOSED-END", Layout: created.Layout,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := h.m.documents
	s.changes, s.selectedChange, s.showChanges, s.reviewing = []document.Change{change}, 0, true, true
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
	if review := s.viewChanges("header"); !strings.Contains(review, "a accept") || !strings.Contains(review, "Esc return") {
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
			h.send(tea.MouseMsg{X: p.x + 1, Y: toolbarTop + p.row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
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
	if !h.quit {
		t.Fatal("second Esc did not close the editor")
	}
	if d, _ := h.docs.Get(context.Background(), h.path); d.Layout.Columns != 1 {
		t.Fatalf("discarded layout was saved: %d columns", d.Layout.Columns)
	}
}

func TestAltFormattingFromToolbarFocusesBody(t *testing.T) {
	h := newDocumentHarness(t, document.Draft{})
	s := h.m.documents
	h.key("tab")
	if s.focus != focusToolbar || s.body.Focused() {
		t.Fatal("Tab did not move focus from the body to the toolbar")
	}
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b"), Alt: true})
	h.typeText("x")
	if !s.body.Focused() {
		t.Fatal("formatting from the toolbar did not focus the body")
	}
	if s.body.Value() != "**x**" {
		t.Fatalf("body = %q", s.body.Value())
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
	if h.m.stage != stageFailed || !strings.Contains(h.m.errMessage, "exceeds editor limits") {
		t.Fatalf("stage = %d, error = %q", h.m.stage, h.m.errMessage)
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
