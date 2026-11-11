package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/filerepo"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/recovery"
	"example.com/parchment/internal/spreadsheet"
	"example.com/parchment/internal/tui/editor"
)

// harness wires the editor to real services over files in temporary
// directories: one for the artifact and one standing in for ~/.parchment.
type harness struct {
	t      *testing.T
	m      *Model
	repo   *filerepo.Repository
	notes  *note.Service
	docs   *document.Service
	sheets *spreadsheet.Service
	decks  *presentation.Service
	path   string
	quit   bool
}

func newHarness(t *testing.T, kind artifact.Kind) *harness {
	t.Helper()
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		t: t, repo: repo,
		notes: note.NewService(repo, 10), docs: document.NewService(repo, 10),
		sheets: spreadsheet.NewService(repo, 10), decks: presentation.NewService(repo, 10),
		path: filepath.Join(t.TempDir(), "report.md"),
	}
	h.open(kind)
	return h
}

// open starts a fresh editor session on the harness file, as running
// `parchment tui` again would.
func (h *harness) open(kind artifact.Kind) {
	h.t.Helper()
	h.openWith(kind, false)
}

// openWith starts a session; edit opens an existing document in its editor.
func (h *harness) openWith(kind artifact.Kind, edit bool) {
	h.t.Helper()
	model := NewModel(Config{
		Path: h.path, Kind: kind, Edit: edit, Notes: h.notes, Documents: h.docs,
		Spreadsheets: h.sheets, Presentations: h.decks, Recovery: h.repo,
	})
	h.m, h.quit = &model, false
	// Blinking cursors return tea.Tick commands, which the harness would run
	// synchronously.
	if s := h.m.documents; s != nil {
		s.promptInput.Cursor.SetMode(cursor.CursorStatic)
	}
	if s := h.m.sheet; s != nil {
		s.input.Cursor.SetMode(cursor.CursorStatic)
	}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.run(h.m.Init())
}

// send delivers a message and feeds the results of returned commands back
// into the model, as the Bubble Tea runtime would.
func (h *harness) send(message tea.Msg) {
	h.t.Helper()
	h.m.autosaveScheduler = nil
	if h.m.documents != nil {
		h.m.documents.autosaveScheduler = nil
	}
	if h.m.deck != nil {
		h.m.deck.autosaveScheduler = nil
	}
	if h.m.sheet != nil {
		h.m.sheet.autosaveScheduler = nil
		h.m.sheet.input.Cursor.SetMode(cursor.CursorStatic)
	}
	_, cmd := h.m.Update(message)
	if h.m.sheet != nil {
		h.m.sheet.autosaveScheduler = nil
		h.m.sheet.input.Cursor.SetMode(cursor.CursorStatic)
	}
	h.run(cmd)
}

var cmdType = reflect.TypeOf(tea.Cmd(nil))

func (h *harness) run(c tea.Cmd) {
	h.t.Helper()
	if c == nil {
		return
	}
	result := c()
	if v := reflect.ValueOf(result); v.Kind() == reflect.Slice && v.Type().Elem() == cmdType {
		// tea.Batch and tea.Sequence results.
		for i := range v.Len() {
			h.run(v.Index(i).Interface().(tea.Cmd))
		}
		return
	}
	switch result.(type) {
	case nil:
	case tea.QuitMsg:
		h.quit = true
	default:
		h.send(result)
	}
}

func (h *harness) key(key string) {
	h.t.Helper()
	switch key {
	case "esc":
		h.send(tea.KeyMsg{Type: tea.KeyEsc})
	case "tab":
		h.send(tea.KeyMsg{Type: tea.KeyTab})
	case "enter":
		h.send(tea.KeyMsg{Type: tea.KeyEnter})
	case "ctrl+s":
		h.send(tea.KeyMsg{Type: tea.KeyCtrlS})
	case "ctrl+c":
		h.send(tea.KeyMsg{Type: tea.KeyCtrlC})
	case "ctrl+z":
		h.send(tea.KeyMsg{Type: tea.KeyCtrlZ})
	case "f3":
		h.send(tea.KeyMsg{Type: tea.KeyF3})
	case "f4":
		h.send(tea.KeyMsg{Type: tea.KeyF4})
	case "f5":
		h.send(tea.KeyMsg{Type: tea.KeyF5})
	case "pgdown":
		h.send(tea.KeyMsg{Type: tea.KeyPgDown})
	default:
		h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	}
}

func (h *harness) typeText(text string) {
	h.t.Helper()
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
}

func (h *harness) hasDraft() bool {
	h.t.Helper()
	_, ok, err := h.repo.LoadRecovery(context.Background(), h.path)
	if err != nil {
		h.t.Fatal(err)
	}
	return ok
}

func TestMissingFileAsksForKindAndCreatesIt(t *testing.T) {
	h := newHarness(t, "")
	if h.m.stage != stageChooseKind || !strings.Contains(h.m.View(), "does not exist yet") {
		t.Fatalf("stage = %d, view:\n%s", h.m.stage, h.m.View())
	}
	if _, err := os.Stat(h.path); !os.IsNotExist(err) {
		t.Fatalf("file created before a kind was chosen: %v", err)
	}
	h.key("d")
	if h.m.stage != stageDocument {
		t.Fatalf("stage = %d, error = %q", h.m.stage, h.m.errMessage)
	}
	d, err := h.docs.Get(context.Background(), h.path)
	if err != nil || d.Title != "report" {
		t.Fatalf("created document = %+v, %v", d, err)
	}
	entries, err := os.ReadDir(filepath.Dir(h.path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("files next to the artifact = %v, %v", entries, err)
	}
}

func TestQuitAtKindPromptCreatesNothing(t *testing.T) {
	h := newHarness(t, "")
	h.key("q")
	if !h.quit {
		t.Fatal("q did not quit")
	}
	if _, err := os.Stat(h.path); !os.IsNotExist(err) {
		t.Fatalf("quitting created the file: %v", err)
	}
}

func TestMissingFileWithKindIsCreatedDirectly(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	if h.m.stage != stageNote || !strings.Contains(h.m.status, "Created") {
		t.Fatalf("stage = %d, status = %q", h.m.stage, h.m.status)
	}
	if n, err := h.notes.Get(context.Background(), h.path); err != nil || n.Title != "report" {
		t.Fatalf("created note = %+v, %v", n, err)
	}
}

func TestEditorOpensFileOfWrongKindAsFailure(t *testing.T) {
	h := newHarness(t, artifact.DocumentKind)
	h.open(artifact.NoteKind)
	if h.m.stage != stageFailed || h.m.errMessage == "" {
		t.Fatalf("stage = %d, error = %q", h.m.stage, h.m.errMessage)
	}
	h.key("q")
	if !h.quit {
		t.Fatal("q did not quit from the failure screen")
	}
}

func TestNoteEditModeSeamlessUndoRedo(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.typeText("Hello")
	if !h.m.dirty() || h.m.bodyInput.Value() != "Hello" {
		t.Fatalf("dirty=%t body=%q", h.m.dirty(), h.m.bodyInput.Value())
	}

	// Press Ctrl+Z with unsaved edits - should undo "Hello" seamlessly without error
	h.key("ctrl+z")
	if h.m.errMessage != "" || h.m.status == "Save or discard your edits before undo or redo" {
		t.Fatalf("unexpected status %q or error %q", h.m.status, h.m.errMessage)
	}
	if h.m.bodyInput.Value() != "" {
		t.Fatalf("after undo: body = %q, want empty", h.m.bodyInput.Value())
	}

	// Press Ctrl+R with unsaved edits - should redo "Hello" seamlessly
	h.key("ctrl+r")
	if h.m.bodyInput.Value() != "Hello" {
		t.Fatalf("after redo: body = %q, want Hello", h.m.bodyInput.Value())
	}
}

func TestNoteSaveStaysInEditorAndCloseQuits(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.typeText("Hello")
	if !h.m.dirty() {
		t.Fatal("typing did not dirty the editor")
	}
	h.key("ctrl+s")
	if h.m.stage != stageNote || h.m.dirty() || h.quit {
		t.Fatalf("after save: stage=%d dirty=%t quit=%t error=%q", h.m.stage, h.m.dirty(), h.quit, h.m.errMessage)
	}
	if n, err := h.notes.Get(context.Background(), h.path); err != nil || n.Body != "Hello" {
		t.Fatalf("saved note = %+v, %v", n, err)
	}
	h.key("ctrl+z")
	if n, _ := h.notes.Get(context.Background(), h.path); n.Body != "" || h.m.bodyInput.Value() != "" {
		t.Fatalf("undo: file body %q, editor body %q", n.Body, h.m.bodyInput.Value())
	}
	h.key("esc")
	if !h.quit {
		t.Fatal("Esc on a clean editor did not quit")
	}
}

func TestNoteEscapeConfirmsBeforeDiscarding(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.typeText("unsaved")
	h.key("esc")
	if h.quit || !strings.Contains(h.m.status, "Unsaved changes") {
		t.Fatalf("first Esc: quit=%t status=%q", h.quit, h.m.status)
	}
	h.key("esc")
	if !h.quit {
		t.Fatal("second Esc did not quit")
	}
	if n, _ := h.notes.Get(context.Background(), h.path); n.Body != "" {
		t.Fatalf("discarded edit was saved: %q", n.Body)
	}
}

func TestPendingCtrlCCancelsOperationAndQuits(t *testing.T) {
	h := newHarness(t, "")
	opCtx, cancel := context.WithCancel(context.Background())
	h.m.pending, h.m.cancelOperation, h.m.stage = true, cancel, stageOpening
	_, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Ctrl+C while opening did not request quit")
	}
	if !errors.Is(opCtx.Err(), context.Canceled) {
		t.Fatal("Ctrl+C did not cancel the pending operation")
	}
}

func TestPendingSaveCancellationKeepsEditorOpen(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	opCtx, cancel := context.WithCancel(context.Background())
	h.m.pending, h.m.cancelOperation = true, cancel
	_, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || h.m.stage != stageNote || !h.m.pending {
		t.Fatalf("cancelled save state: command=%v stage=%d pending=%t", cmd, h.m.stage, h.m.pending)
	}
	if opCtx.Err() == nil {
		t.Fatal("Ctrl+C did not cancel the pending save")
	}
	h.send(noteSavedMsg{err: context.Canceled})
	if h.m.pending || h.m.stage != stageNote || !strings.Contains(h.m.status, "unsaved changes remain") {
		t.Fatalf("completed cancelled save: pending=%t stage=%d status=%q", h.m.pending, h.m.stage, h.m.status)
	}
}

func TestSmallTerminalQClosesOnlyCleanNote(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.send(tea.WindowSizeMsg{Width: 30, Height: 8})
	if view := h.m.View(); !strings.Contains(view, "too small") {
		t.Fatalf("small terminal view = %q", view)
	}
	h.m.bodyInput.SetValue("unsaved")
	h.key("q")
	if h.quit {
		t.Fatal("q quit with unsaved changes")
	}
	h.m.bodyInput.SetValue("")
	h.key("q")
	if !h.quit {
		t.Fatal("q did not quit a clean editor")
	}
}

func TestViewSanitizesTerminalControlSequences(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.m.path = "/tmp/parch\x1b]52;c;payload\a.md"
	h.m.bodyInput.SetValue("Body\x1b]52;c;payload\a\nnext")
	view := h.m.View()
	for _, unsafe := range []string{"\x1b[2J", "\x1b]52;", "\x07"} {
		if strings.Contains(view, unsafe) {
			t.Errorf("view contains terminal control sequence %q", unsafe)
		}
	}
	if !strings.Contains(view, "�]52;") {
		t.Fatalf("view did not visibly sanitize the path: %q", view)
	}
	h.key("f5")
	if view := h.m.View(); strings.Contains(view, "\x1b]52;") || !strings.Contains(view, "Preview") {
		t.Fatalf("preview = %q", view)
	}
	h.key("q")
	if h.m.previewing {
		t.Fatal("q did not exit preview mode")
	}
}

func TestEditorRefusesNotesItWouldNormalize(t *testing.T) {
	cases := []struct{ name, body string }{
		{"tab in body", "before\tafter"},
		{"control in body", "before\x1b[2Jafter"},
		{"oversized body", strings.Repeat("x", 1_000_001)},
		{"too many body lines", strings.Repeat("x\n", 10_000) + "last"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "")
			if _, err := h.notes.Create(context.Background(), h.path, tc.body); err != nil {
				t.Fatal(err)
			}
			h.open(artifact.NoteKind)
			if h.m.stage != stageFailed || !strings.Contains(h.m.errMessage, "editor limits") {
				t.Fatalf("stage = %d, error = %q", h.m.stage, h.m.errMessage)
			}
		})
	}
}

func TestNoteSaveRejectsConcurrentExternalChange(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, artifact.NoteKind)
	h.typeText("Edited body")
	external := h.m.snapshot
	external.Body = "Changed in a text editor"
	if err := h.repo.Transition(ctx, h.path, &h.m.snapshot, &external); err != nil {
		t.Fatal(err)
	}
	h.key("ctrl+s")
	if h.m.stage != stageNote || !strings.Contains(h.m.errMessage, "changed since it was loaded") {
		t.Fatalf("stage = %d, error = %q", h.m.stage, h.m.errMessage)
	}
	current, err := h.notes.Get(ctx, h.path)
	if err != nil || current.Body != external.Body {
		t.Fatalf("stale editor overwrote external update: %+v, %v", current, err)
	}
}

func TestPreviewCanScroll(t *testing.T) {
	h := newHarness(t, "")
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("Line %02d", i+1)
	}
	if _, err := h.notes.Create(context.Background(), h.path, strings.Join(lines, "\n")); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 16})
	h.key("f5")
	if view := h.m.View(); !strings.Contains(view, "Line 01") || strings.Contains(view, "Line 60") {
		t.Fatalf("initial preview = %q", view)
	}
	for range 3 {
		h.key("pgdown")
	}
	if view := h.m.View(); !strings.Contains(view, "Line 3") {
		t.Fatalf("scrolled preview = %q", view)
	}
	h.key("esc")
	if h.m.previewing || h.quit {
		t.Fatal("Esc did not return from preview to the editor")
	}
}

func TestNoteAutosaveIsOfferedOnReopen(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, "")
	created, err := h.notes.Create(ctx, h.path, "Before")
	if err != nil {
		t.Fatal(err)
	}
	withBlock := created
	withBlock.Blocks = map[string]json.RawMessage{"parchment-extra": json.RawMessage(`{"kept":true}`)}
	if err := h.repo.Transition(ctx, h.path, &created, &withBlock); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	h.m.bodyInput.SetValue("Recovered body")
	h.run(h.m.saveNoteRecovery(h.m.autosaveSession))
	if !h.hasDraft() {
		t.Fatal("autosave wrote no draft")
	}

	// Simulate a crash: reopen without closing.
	h.open(artifact.NoteKind)
	if h.m.stage != stageRecovery || !strings.Contains(h.m.View(), "autosaved draft") {
		t.Fatalf("stage = %d, view:\n%s", h.m.stage, h.m.View())
	}
	h.key("r")
	if h.m.stage != stageNote || h.m.bodyInput.Value() != "Recovered body" {
		t.Fatalf("recovered editor: stage=%d body=%q", h.m.stage, h.m.bodyInput.Value())
	}
	if string(h.m.snapshot.Blocks["parchment-extra"]) != `{"kept":true}` || h.m.snapshot.Body != "Before" {
		t.Fatalf("recovered snapshot = %+v", h.m.snapshot)
	}
	h.key("ctrl+s")
	saved, err := h.notes.Get(ctx, h.path)
	if err != nil || saved.Body != "Recovered body" || string(saved.Blocks["parchment-extra"]) != `{"kept":true}` {
		t.Fatalf("saved recovered note = %+v, %v", saved, err)
	}
	if h.hasDraft() {
		t.Fatal("saving left the draft behind")
	}
}

func TestRecoveryOfferDiscardAndQuit(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.m.bodyInput.SetValue("draft")
	h.run(h.m.saveNoteRecovery(h.m.autosaveSession))

	h.open(artifact.NoteKind)
	h.key("q")
	if !h.quit || !h.hasDraft() {
		t.Fatalf("q at the offer: quit=%t draft kept=%t", h.quit, h.hasDraft())
	}
	h.open(artifact.NoteKind)
	h.key("d")
	if h.m.stage != stageNote || h.m.bodyInput.Value() != "" || h.hasDraft() {
		t.Fatalf("discard: stage=%d body=%q draft=%t", h.m.stage, h.m.bodyInput.Value(), h.hasDraft())
	}
}

func TestRecoveryWarnsWhenFileChangedAfterDraft(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, artifact.NoteKind)
	h.m.bodyInput.SetValue("draft")
	h.run(h.m.saveNoteRecovery(h.m.autosaveSession))
	if _, err := h.notes.Update(ctx, h.path, "changed elsewhere"); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	h.key("r")
	if !strings.Contains(h.m.status, "saving will report a conflict") {
		t.Fatalf("status = %q", h.m.status)
	}
}

func TestPresentationRecoveryOfferRecoverDiscardQuit(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, artifact.PresentationKind)
	h.key("e")
	if !h.m.deck.editing {
		t.Fatal("not in edit mode")
	}
	h.m.deck.editor.SetValue(h.m.deck.item.Source + "\n---\n\n## Recovered Slide\n")
	h.run(h.m.deck.saveRecovery(h.m.deck.autosaveSession))
	if !h.hasDraft() {
		t.Fatal("failed to store recovery draft")
	}

	// 1. Test 'q' keeps draft
	h.open(artifact.PresentationKind)
	if h.m.stage != stageRecovery {
		t.Fatalf("stage = %d, want stageRecovery", h.m.stage)
	}
	h.key("q")
	if !h.quit || !h.hasDraft() {
		t.Fatalf("q at offer: quit=%t, hasDraft=%t", h.quit, h.hasDraft())
	}

	// 2. Test 'd' discards draft and opens saved file
	h.open(artifact.PresentationKind)
	if h.m.stage != stageRecovery {
		t.Fatalf("stage = %d, want stageRecovery", h.m.stage)
	}
	h.key("d")
	if h.m.stage != stagePresentation || h.hasDraft() {
		t.Fatalf("d at offer: stage=%d, hasDraft=%t", h.m.stage, h.hasDraft())
	}

	// Recreate draft
	h.key("e")
	h.m.deck.editor.SetValue(h.m.deck.item.Source + "\n---\n\n## Recovered Slide\n")
	h.run(h.m.deck.saveRecovery(h.m.deck.autosaveSession))
	if !h.hasDraft() {
		t.Fatal("failed to store recovery draft again")
	}

	// 3. Test 'r' recovers directly into editor
	h.open(artifact.PresentationKind)
	if h.m.stage != stageRecovery {
		t.Fatalf("stage = %d, want stageRecovery", h.m.stage)
	}
	h.key("r")
	if h.m.stage != stagePresentation || !h.m.deck.editing {
		t.Fatalf("r at offer: stage=%d, editing=%t", h.m.stage, h.m.deck.editing)
	}
	if !strings.Contains(h.m.deck.editor.Value(), "Recovered Slide") {
		t.Fatalf("editor value = %q, want 'Recovered Slide'", h.m.deck.editor.Value())
	}

	// Save recovered presentation via ctrl+s
	h.key("ctrl+s")
	if h.m.deck.editing {
		t.Fatalf("editor did not close after successful save (errMessage = %q, status = %q)", h.m.deck.errMessage, h.m.deck.status)
	}
	saved, err := h.decks.Get(ctx, h.path)
	if err != nil || !strings.Contains(saved.Source, "Recovered Slide") {
		t.Fatalf("saved deck source = %q, err=%v", saved.Source, err)
	}
	if h.hasDraft() {
		t.Fatal("draft was not deleted after successful save")
	}
}

func TestPresentationRecoveryWarnsWhenFileChangedAfterDraft(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, artifact.PresentationKind)
	h.key("e")
	h.m.deck.editor.SetValue(h.m.deck.item.Source + "\n---\n\n## Draft Content\n")
	h.run(h.m.deck.saveRecovery(h.m.deck.autosaveSession))
	if !h.hasDraft() {
		t.Fatal("draft not stored")
	}

	// Modify file externally
	deck, err := h.decks.Get(ctx, h.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.decks.Update(ctx, deck, deck.Source+"\n---\n\n## External Edit\n"); err != nil {
		t.Fatal(err)
	}

	// Reopen and recover
	h.open(artifact.PresentationKind)
	if h.m.stage != stageRecovery {
		t.Fatalf("stage = %d, want stageRecovery", h.m.stage)
	}
	h.key("r")
	if !strings.Contains(h.m.deck.status, "saving will report a conflict") {
		t.Fatalf("status = %q, want conflict warning", h.m.deck.status)
	}

	// Attempting save should fail with conflict and keep editor open
	h.key("ctrl+s")
	if !h.m.deck.editing {
		t.Fatal("editor closed on save failure")
	}
	if !strings.Contains(h.m.deck.errMessage, "Save failed:") {
		t.Fatalf("errMessage = %q, want 'Save failed:'", h.m.deck.errMessage)
	}
}

func TestSpreadsheetAutosaveIsOfferedOnReopen(t *testing.T) {
	ctx := context.Background()

	hSheet := newHarness(t, "")
	_, err := hSheet.sheets.Create(ctx, hSheet.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	hSheet.open(artifact.SpreadsheetKind)

	err = hSheet.repo.SaveRecovery(ctx, recovery.Draft{
		Path:      hSheet.path,
		Kind:      string(artifact.SpreadsheetKind),
		UpdatedAt: time.Now().UTC(),
		Data:      json.RawMessage(`{"baseline":{"version":1,"artifact":{"kind":"spreadsheet"},"created_at":"2020-01-01T00:00:00Z","modified_at":"2020-01-01T00:00:00Z","sheets":[{"name":"Sheet1","rows":[[{"value":"original"}]]}]},"dirty":{"version":1,"artifact":{"kind":"spreadsheet"},"created_at":"2020-01-01T00:00:00Z","modified_at":"2020-01-01T00:00:00Z","sheets":[{"name":"Sheet1","rows":[[{"value":"recovered"}]]}]},"active_sheet":0,"cursor_row":0,"cursor_col":0}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	hSheet.open(artifact.SpreadsheetKind)
	if hSheet.m.stage != stageRecovery || !strings.Contains(hSheet.m.View(), "autosaved draft") {
		t.Fatalf("spreadsheet stage = %d, view:\n%s", hSheet.m.stage, hSheet.m.View())
	}
	hSheet.key("r")
	if hSheet.m.stage != stageSpreadsheet {
		t.Fatalf("recovered spreadsheet stage = %d", hSheet.m.stage)
	}
	if cell := hSheet.m.sheet.book.Sheets[0].Rows[0][0]; cell.Value != "recovered" {
		t.Fatalf("recovered cell value = %q, want 'recovered'", cell.Value)
	}
}

func TestSpreadsheetRecoveryOfferRecoverDiscardQuit(t *testing.T) {
	ctx := context.Background()
	hSheet := newHarness(t, "")
	_, err := hSheet.sheets.Create(ctx, hSheet.path, nil)
	if err != nil {
		t.Fatal(err)
	}

	saveDraft := func(data string) {
		err := hSheet.repo.SaveRecovery(ctx, recovery.Draft{
			Path:      hSheet.path,
			Kind:      string(artifact.SpreadsheetKind),
			UpdatedAt: time.Now().UTC(),
			Data:      json.RawMessage(data),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	validDraft := `{"baseline":{"version":1,"artifact":{"kind":"spreadsheet"},"created_at":"2020-01-01T00:00:00Z","modified_at":"2020-01-01T00:00:00Z","sheets":[{"name":"Sheet1","rows":[[{"value":"1"}]]}]},"dirty":{"version":1,"artifact":{"kind":"spreadsheet"},"created_at":"2020-01-01T00:00:00Z","modified_at":"2020-01-01T00:00:00Z","sheets":[{"name":"Sheet1","rows":[[{"value":"42"}]]}]},"active_sheet":0,"cursor_row":0,"cursor_col":0,"pending_cell_input":"=SUM(A1:A2)"}`

	// 1. 'q' keeps draft
	saveDraft(validDraft)
	hSheet.open(artifact.SpreadsheetKind)
	if hSheet.m.stage != stageRecovery {
		t.Fatalf("stage = %d, want stageRecovery", hSheet.m.stage)
	}
	hSheet.key("q")
	if !hSheet.quit || !hSheet.hasDraft() {
		t.Fatalf("q at offer: quit=%t, hasDraft=%t", hSheet.quit, hSheet.hasDraft())
	}

	// 2. 'd' discards draft and opens saved workbook
	hSheet.open(artifact.SpreadsheetKind)
	if hSheet.m.stage != stageRecovery {
		t.Fatalf("stage = %d, want stageRecovery", hSheet.m.stage)
	}
	hSheet.key("d")
	if hSheet.m.stage != stageSpreadsheet || hSheet.hasDraft() {
		t.Fatalf("d at offer: stage=%d, hasDraft=%t", hSheet.m.stage, hSheet.hasDraft())
	}

	// 3. 'r' recovers draft into spreadsheet
	saveDraft(validDraft)
	hSheet.open(artifact.SpreadsheetKind)
	hSheet.key("r")
	if hSheet.m.stage != stageSpreadsheet {
		t.Fatalf("r at offer: stage=%d", hSheet.m.stage)
	}
	if !hSheet.m.sheet.editing {
		t.Fatal("expected editing = true due to pending_cell_input")
	}
	if hSheet.m.sheet.input.Value() != "=SUM(A1:A2)" {
		t.Fatalf("input value = %q, want =SUM(A1:A2)", hSheet.m.sheet.input.Value())
	}
	if cell := hSheet.m.sheet.book.Sheets[0].Rows[0][0]; cell.Value != "42" {
		t.Fatalf("dirty cell value = %q, want 42", cell.Value)
	}

	// 4. Corrupt draft shows error and allows discard
	saveDraft(`{"baseline": "invalid-type"}`)
	hSheet.open(artifact.SpreadsheetKind)
	hSheet.key("r")
	if !strings.Contains(hSheet.m.errMessage, "Decode autosaved spreadsheet") {
		t.Fatalf("expected decode error, got %q", hSheet.m.errMessage)
	}
	hSheet.key("d")
	if hSheet.m.stage != stageSpreadsheet || hSheet.hasDraft() {
		t.Fatalf("discard after corrupt draft: stage=%d, hasDraft=%t", hSheet.m.stage, hSheet.hasDraft())
	}
}

func TestSpreadsheetRecoveryWarnsWhenFileChangedAfterDraft(t *testing.T) {
	ctx := context.Background()
	hSheet := newHarness(t, "")
	book, err := hSheet.sheets.Create(ctx, hSheet.path, nil)
	if err != nil {
		t.Fatal(err)
	}

	draft := fmt.Sprintf(`{"baseline":{"version":1,"artifact":{"kind":"spreadsheet"},"created_at":"2020-01-01T00:00:00Z","modified_at":"2020-01-01T00:00:00Z","sheets":[{"name":"Sheet1","rows":[[{"value":"old"}]]}]},"dirty":{"version":1,"artifact":{"kind":"spreadsheet"},"created_at":"2020-01-01T00:00:00Z","modified_at":"2020-01-01T00:00:00Z","sheets":[{"name":"Sheet1","rows":[[{"value":"draft"}]]}]},"active_sheet":0,"cursor_row":0,"cursor_col":0}`)
	err = hSheet.repo.SaveRecovery(ctx, recovery.Draft{
		Path:      hSheet.path,
		Kind:      string(artifact.SpreadsheetKind),
		UpdatedAt: time.Now().UTC(),
		Data:      json.RawMessage(draft),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Modify file externally
	if _, err := hSheet.sheets.SetCell(ctx, hSheet.path, 1, 1, spreadsheet.Cell{Value: "externally changed"}); err != nil {
		t.Fatal(err)
	}
	_ = book

	// Reopen and recover
	hSheet.open(artifact.SpreadsheetKind)
	if hSheet.m.stage != stageRecovery {
		t.Fatalf("stage = %d, want stageRecovery", hSheet.m.stage)
	}
	hSheet.key("r")
	if !strings.Contains(hSheet.m.sheet.status, "saving will report a conflict") {
		t.Fatalf("status = %q, want conflict warning", hSheet.m.sheet.status)
	}
}

func TestClosingNormallyDeletesDraft(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.typeText("unsaved")
	h.run(h.m.saveNoteRecovery(h.m.autosaveSession))
	if !h.hasDraft() {
		t.Fatal("autosave wrote no draft")
	}
	h.key("esc")
	h.key("esc")
	if !h.quit || h.hasDraft() {
		t.Fatalf("close: quit=%t draft remains=%t", h.quit, h.hasDraft())
	}
}

func TestKindPromptCreatesSpreadsheetAndPresentation(t *testing.T) {
	h := newHarness(t, "")
	if view := h.m.View(); !strings.Contains(view, "s  Spreadsheet") || !strings.Contains(view, "p  Presentation") {
		t.Fatalf("kind prompt = %q", view)
	}
	h.key("s")
	if h.m.stage != stageSpreadsheet || !strings.Contains(h.m.View(), "A1") {
		t.Fatalf("stage = %d, error = %q, view:\n%s", h.m.stage, h.m.errMessage, h.m.View())
	}
	if book, err := h.sheets.Get(context.Background(), h.path); err != nil || len(book.Sheets) != 1 {
		t.Fatalf("created spreadsheet = %+v, %v", book, err)
	}
	h.key("q")
	if !h.quit {
		t.Fatal("q did not quit the spreadsheet")
	}

	h = newHarness(t, "")
	h.key("p")
	if h.m.stage != stagePresentation || !strings.Contains(h.m.View(), "report") {
		t.Fatalf("stage = %d, error = %q, view:\n%s", h.m.stage, h.m.errMessage, h.m.View())
	}
	deck, err := h.decks.Get(context.Background(), h.path)
	if err != nil || !strings.HasPrefix(deck.Source, "# report") {
		t.Fatalf("created presentation = %+v, %v", deck, err)
	}
}

func TestPlainMarkdownOpensAsNoteAndStaysPlain(t *testing.T) {
	h := newHarness(t, "")
	if err := os.WriteFile(h.path, []byte("# Plain\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	if h.m.stage != stageNote || h.m.bodyInput.Value() != "# Plain\n" {
		t.Fatalf("stage = %d, body = %q, error = %q", h.m.stage, h.m.bodyInput.Value(), h.m.errMessage)
	}
	h.typeText("more")
	h.key("ctrl+s")
	data, err := os.ReadFile(h.path)
	if err != nil || strings.Contains(string(data), "parchment-meta") || !strings.Contains(string(data), "more") {
		t.Fatalf("saved plain Markdown = %q, %v", data, err)
	}
}

func TestNoteClickPositionsBodyCursor(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.m.bodyInput.SetValue("alpha\nbravo")
	h.send(tea.MouseMsg{X: 4, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	h.m.bodyInput.InsertString("!")
	if h.m.bodyInput.Value() != "alpha\nbr!avo" {
		t.Fatalf("note click positioned cursor at %q", h.m.bodyInput.Value())
	}
	h.send(tea.MouseMsg{X: 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	h.m.bodyInput.InsertString("?")
	if h.m.bodyInput.Value() != "alpha\nbr!?avo" {
		t.Fatalf("header click moved note cursor: %q", h.m.bodyInput.Value())
	}
}

func TestDocumentClickPositionsBodyCursor(t *testing.T) {
	h := newHarness(t, artifact.DocumentKind)
	s := h.m.documents
	s.body.SetValue("alpha\nbravo")
	y := toolbarTop + s.toolbarRows() + 1
	h.send(tea.MouseMsg{X: 4, Y: y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	s.body.InsertString("!")
	if s.body.Value() != "alpha\nbr!avo" {
		t.Fatalf("document click positioned cursor at %q", s.body.Value())
	}
	h.send(tea.MouseMsg{X: 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	s.body.InsertString("?")
	if s.body.Value() != "alpha\nbr!?avo" {
		t.Fatalf("header click moved document cursor: %q", s.body.Value())
	}
}

func TestRevertingNoteEditsRemovesAutosavedDraft(t *testing.T) {
	h := newHarness(t, "")
	if _, err := h.notes.Create(context.Background(), h.path, "Saved"); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	h.m.bodyInput.SetValue("Unsaved")
	h.run(h.m.saveNoteRecovery(h.m.autosaveSession))
	if !h.hasDraft() {
		t.Fatal("autosave wrote no draft")
	}
	h.m.bodyInput.SetValue("Saved")
	h.run(h.m.saveNoteRecovery(h.m.autosaveSession))
	if h.hasDraft() || h.m.draftStored {
		t.Fatalf("reverted editor kept its draft (draftStored=%t)", h.m.draftStored)
	}
}

func TestSanitizeTerminalText(t *testing.T) {
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
			name:     "normal text",
			input:    "Hello, world!",
			expected: "Hello, world!",
		},
		{
			name:     "text with newlines preserved",
			input:    "line 1\nline 2\n",
			expected: "line 1\nline 2\n",
		},
		{
			name:     "text with control characters replaced",
			input:    "hello\x00\x07world\x1b[31m",
			expected: "hello\uFFFD\uFFFDworld\uFFFD[31m",
		},
		{
			name:     "text with tabs replaced as control character",
			input:    "tab\tseparated",
			expected: "tab\uFFFDseparated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeTerminalText(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeTerminalText(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRun(t *testing.T) {
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "note.md")

	headlessOpts := []tea.ProgramOption{
		tea.WithInput(strings.NewReader("")),
		tea.WithoutRenderer(),
	}

	testCases := []struct {
		name   string
		cfg    Config
		errMsg string
	}{
		{"canceled context with note config", Config{Path: path, Kind: artifact.NoteKind, Notes: note.NewService(repo, 10), Recovery: repo}, "unexpected error running program"},
		{"canceled context with documents config", Config{Path: filepath.Join(t.TempDir(), "doc.md"), Kind: artifact.DocumentKind, Documents: document.NewService(repo, 10), Recovery: repo}, "unexpected error running program with documents config"},
		{"canceled context with spreadsheet config", Config{Path: filepath.Join(t.TempDir(), "sheet.csv"), Kind: artifact.SpreadsheetKind, Spreadsheets: spreadsheet.NewService(repo, 10), Recovery: repo}, "unexpected error running program with spreadsheet config"},
		{"canceled context with presentation config", Config{Path: filepath.Join(t.TempDir(), "deck.md"), Kind: artifact.PresentationKind, Presentations: presentation.NewService(repo, 10), Recovery: repo}, "unexpected error running program with presentation config"},
		{"canceled context with choose kind config", Config{Path: filepath.Join(t.TempDir(), "unknown.md"), Kind: "", Recovery: repo}, "unexpected error running program with choose kind config"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // pre-canceled context so program shuts down immediately
			err := Run(ctx, tc.cfg, headlessOpts...)
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("%s: %v", tc.errMsg, err)
			}
		})
	}
}

func TestModelUpdateDocumentMessageWhenDocumentsNil(t *testing.T) {
	m := NewModel(Config{
		Path: "test.md",
		Kind: artifact.DocumentKind,
	})
	if m.documents != nil {
		t.Fatal("expected nil m.documents")
	}

	var msg documentMessage = documentAutosaveFinishedMsg{session: 1, err: nil}
	model, cmd := m.Update(msg)
	if model != &m || cmd != nil {
		t.Fatalf("expected m and nil cmd when documents is nil, got model=%v cmd=%v", model, cmd)
	}
}

func TestModelUpdateFileOpenedMsgUnpreservableNote(t *testing.T) {
	m := NewModel(Config{
		Path: "test.md",
		Kind: artifact.NoteKind,
	})

	msg := fileOpenedMsg{
		kind: artifact.NoteKind,
		note: note.Note{
			Body: "invalid \x00 body",
		},
	}

	m.Update(msg)
	if m.stage != stageFailed {
		t.Errorf("expected stageFailed, got stage %v", m.stage)
	}
	if !strings.Contains(m.errMessage, "editor limits") {
		t.Errorf("expected errMessage to mention 'editor limits', got %q", m.errMessage)
	}
}

func TestModelUpdateFileOpenedMsg(t *testing.T) {
	t.Run("error setting stageFailed", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.pending = true
		testErr := errors.New("open file error")
		h.m.Update(fileOpenedMsg{err: testErr})

		if h.m.pending {
			t.Error("expected pending=false after fileOpenedMsg")
		}
		if h.m.stage != stageFailed {
			t.Errorf("stage = %v, want stageFailed", h.m.stage)
		}
		if h.m.errMessage != "open file error" {
			t.Errorf("errMessage = %q, want 'open file error'", h.m.errMessage)
		}
	})

	t.Run("created file status", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.pending = true
		h.m.Update(fileOpenedMsg{
			kind:    artifact.NoteKind,
			created: true,
			note:    note.Note{Artifact: artifact.Artifact{Title: "report"}, Body: "hello"},
		})

		if h.m.pending {
			t.Error("expected pending=false")
		}
		if !strings.Contains(h.m.status, "Created "+h.m.path) {
			t.Errorf("status = %q, want containing 'Created %s'", h.m.status, h.m.path)
		}
		if h.m.stage != stageNote {
			t.Errorf("stage = %v, want stageNote", h.m.stage)
		}
	})

	t.Run("hasDraft transitions to stageRecovery", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.pending = true
		h.m.Update(fileOpenedMsg{
			kind:     artifact.NoteKind,
			hasDraft: true,
		})

		if h.m.pending {
			t.Error("expected pending=false")
		}
		if h.m.stage != stageRecovery {
			t.Errorf("stage = %v, want stageRecovery", h.m.stage)
		}
	})
}

func TestModelUpdateNoteSavedMsg(t *testing.T) {
	t.Run("successful save with cleanup error", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.pending = true
		cleanupErr := errors.New("cannot delete draft")

		h.m.Update(noteSavedMsg{
			note:       note.Note{Artifact: artifact.Artifact{Title: "My Note"}, Body: "Content"},
			err:        nil,
			cleanupErr: cleanupErr,
		})

		if h.m.pending {
			t.Error("expected pending=false")
		}
		if h.m.errMessage != "" {
			t.Errorf("expected empty errMessage, got %q", h.m.errMessage)
		}
		if !strings.Contains(h.m.status, "Saved “My Note”") || !strings.Contains(h.m.status, "could not remove recovery draft: cannot delete draft") {
			t.Errorf("unexpected status: %q", h.m.status)
		}
	})

	t.Run("save with general error", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.pending = true
		saveErr := errors.New("permission denied")

		h.m.Update(noteSavedMsg{
			err: saveErr,
		})

		if h.m.pending {
			t.Error("expected pending=false")
		}
		if h.m.errMessage != "permission denied" {
			t.Errorf("errMessage = %q, want 'permission denied'", h.m.errMessage)
		}
	})
}

func TestModelUpdateHistoryChangedMsg(t *testing.T) {
	t.Run("history error", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.pending = true
		histErr := errors.New("history error")

		h.m.Update(historyChangedMsg{
			err: histErr,
		})

		if h.m.pending {
			t.Error("expected pending=false")
		}
		if h.m.errMessage != "history error" {
			t.Errorf("errMessage = %q, want 'history error'", h.m.errMessage)
		}
	})

	t.Run("history success", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.pending = true

		h.m.Update(historyChangedMsg{
			description: "Undid edit",
			note:        note.Note{Artifact: artifact.Artifact{Title: "Title"}, Body: "Restored"},
			err:         nil,
		})

		if h.m.pending {
			t.Error("expected pending=false")
		}
		if h.m.errMessage != "" {
			t.Errorf("errMessage = %q, want empty", h.m.errMessage)
		}
		if h.m.status != "Undid edit" {
			t.Errorf("status = %q, want 'Undid edit'", h.m.status)
		}
		if h.m.bodyInput.Value() != "Restored" {
			t.Errorf("bodyInput = %q, want 'Restored'", h.m.bodyInput.Value())
		}
	})
}

func TestModelUpdateKeyHandlingByStage(t *testing.T) {
	t.Run("stageChooseKind key options", func(t *testing.T) {
		for _, key := range []string{"n", "d", "s", "p"} {
			h := newHarness(t, "")
			if h.m.stage != stageChooseKind {
				t.Fatalf("stage = %v, want stageChooseKind", h.m.stage)
			}
			_, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			if cmd == nil {
				t.Errorf("expected non-nil cmd for key %q in stageChooseKind", key)
			}
		}

		h := newHarness(t, "")
		_, cmdQ := h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
		if cmdQ == nil || cmdQ() != tea.Quit() {
			t.Errorf("expected tea.Quit for 'q', got %v", cmdQ)
		}
	})

	t.Run("stageOpening / stageFailed quit keys", func(t *testing.T) {
		for _, key := range []string{"q", "esc", "ctrl+c"} {
			h := newHarness(t, "")
			h.m.stage = stageFailed
			var kMsg tea.KeyMsg
			switch key {
			case "q":
				kMsg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
			case "esc":
				kMsg = tea.KeyMsg{Type: tea.KeyEsc}
			case "ctrl+c":
				kMsg = tea.KeyMsg{Type: tea.KeyCtrlC}
			}
			_, cmd := h.m.Update(kMsg)
			if cmd == nil || cmd() != tea.Quit() {
				t.Errorf("key %s in stageFailed did not return tea.Quit", key)
			}
		}
	})

	t.Run("stageRecovery key options", func(t *testing.T) {
		// 'r' recovers draft
		hR := newHarness(t, artifact.NoteKind)
		draftData, _ := json.Marshal(noteRecoveryData{Body: "Draft body"})
		hR.m.opened = fileOpenedMsg{
			kind:  artifact.NoteKind,
			draft: recovery.Draft{Kind: string(artifact.NoteKind), Data: draftData},
		}
		hR.m.autosaveScheduler = func(session uint64) tea.Cmd {
			return func() tea.Msg { return nil }
		}
		hR.m.stage = stageRecovery
		_, cmdR := hR.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
		if cmdR == nil {
			t.Error("expected cmd for 'r'")
		}

		// 'd' discards draft
		hD := newHarness(t, artifact.NoteKind)
		hD.m.stage = stageRecovery
		_, cmdD := hD.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
		if cmdD == nil {
			t.Error("expected cmd for 'd'")
		}

		// 'esc' quits
		hEsc := newHarness(t, artifact.NoteKind)
		hEsc.m.stage = stageRecovery
		_, cmdEsc := hEsc.m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if cmdEsc == nil || cmdEsc() != tea.Quit() {
			t.Error("expected tea.Quit for 'esc' in stageRecovery")
		}
	})

	t.Run("pending state non-cancel key shows message", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.pending = true
		h.m.status = ""

		h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
		if h.m.status != "Please wait for the current operation to finish" {
			t.Errorf("status = %q, want 'Please wait for the current operation to finish'", h.m.status)
		}
	})
}

func TestModelUpdateNoteKeyHandling(t *testing.T) {
	t.Run("preview mode toggle and navigation", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.bodyInput.SetValue("Sample Note Text")

		// F5 enters preview
		h.m.Update(tea.KeyMsg{Type: tea.KeyF5})
		if !h.m.previewing {
			t.Fatal("F5 did not set previewing=true")
		}

		// Non-closing key in preview delegates to preview model
		h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		if !h.m.previewing {
			t.Error("previewing set to false on non-exit key")
		}

		// F5 exits preview mode
		h.m.Update(tea.KeyMsg{Type: tea.KeyF5})
		if h.m.previewing {
			t.Fatal("F5 in preview did not set previewing=false")
		}

		// Re-enter preview, Ctrl+C requests close
		h.m.Update(tea.KeyMsg{Type: tea.KeyF5})
		h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if h.quit {
			t.Error("Ctrl+C in preview on clean note quit immediately without closing")
		}
	})

	t.Run("Ctrl+C with active selection delegates to bodyInput", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.bodyInput.SetValue("Selected Text")
		h.m.bodyInput.Selection = editor.Selection{Active: true}

		// Ctrl+C with selection active should NOT quit or trigger requestClose
		h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if h.m.discardWarning {
			t.Error("Ctrl+C with active selection triggered discardWarning")
		}
	})

	t.Run("Ctrl+S triggers saveNote", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.m.bodyInput.SetValue("New content to save")

		_, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
		if cmd == nil {
			t.Fatal("Ctrl+S did not return save cmd")
		}
		if !h.m.pending {
			t.Error("Ctrl+S did not set pending=true")
		}
	})

	t.Run("Redo shortcut ctrl+y / ctrl+shift+z", func(t *testing.T) {
		h := newHarness(t, artifact.NoteKind)
		h.typeText("Hello")
		h.m.bodyInput.Undo()
		if h.m.bodyInput.Value() != "" {
			t.Fatalf("expected empty value after Undo, got %q", h.m.bodyInput.Value())
		}

		// Ctrl+Y redoes input directly
		h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
		if h.m.bodyInput.Value() != "Hello" {
			t.Errorf("Ctrl+Y did not redo input, value=%q", h.m.bodyInput.Value())
		}
	})
}

func TestModelInit_returns_nil_when_stage_is_stageChooseKind(t *testing.T) {
	model := NewModel(Config{
		Path: "test.md",
		Kind: "",
	})
	cmd := model.Init()
	if cmd != nil {
		t.Fatalf("expected nil cmd when stage is stageChooseKind, got %v", cmd)
	}
}

func TestModelInit_returns_openFile_cmd_for_kinds(t *testing.T) {
	kinds := []struct {
		name string
		kind artifact.Kind
	}{
		{"note", artifact.NoteKind},
		{"document", artifact.DocumentKind},
		{"spreadsheet", artifact.SpreadsheetKind},
		{"presentation", artifact.PresentationKind},
	}

	for _, tc := range kinds {
		t.Run("returns openFile cmd for "+tc.name, func(t *testing.T) {
			repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "testfile")
			cfg := Config{
				Path:          path,
				Kind:          tc.kind,
				Notes:         note.NewService(repo, 10),
				Documents:     document.NewService(repo, 10),
				Spreadsheets:  spreadsheet.NewService(repo, 10),
				Presentations: presentation.NewService(repo, 10),
				Recovery:      repo,
			}
			model := NewModel(cfg)

			cmd := model.Init()
			if cmd == nil {
				t.Fatalf("expected non-nil cmd for kind %s, got nil", tc.kind)
			}
			if model.stage != stageOpening || !model.pending {
				t.Fatalf("expected stageOpening and pending=true, got stage=%d pending=%t", model.stage, model.pending)
			}

			// Execute the command returned by Init()
			msg := cmd()
			openedMsg, ok := msg.(fileOpenedMsg)
			if !ok {
				t.Fatalf("expected fileOpenedMsg, got %T", msg)
			}
			if openedMsg.kind != tc.kind {
				t.Fatalf("expected kind %s, got %s", tc.kind, openedMsg.kind)
			}
			if openedMsg.err != nil {
				t.Fatalf("unexpected error opening %s file: %v", tc.kind, openedMsg.err)
			}
			if !openedMsg.created {
				t.Fatalf("expected new file to be created for %s", tc.kind)
			}
		})
	}
}

func TestModelInit_returns_error_msg_when_opening_unsupported_kind(t *testing.T) {
	unsupportedKind := artifact.Kind("unknown")
	model := NewModel(Config{
		Path: "test.unknown",
		Kind: unsupportedKind,
	})
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("expected non-nil cmd when kind is unsupported, got nil")
	}
	msg := cmd()
	openedMsg, ok := msg.(fileOpenedMsg)
	if !ok {
		t.Fatalf("expected fileOpenedMsg, got %T", msg)
	}
	if openedMsg.err == nil || !strings.Contains(openedMsg.err.Error(), "cannot open") {
		t.Fatalf("expected error containing 'cannot open', got %v", openedMsg.err)
	}
}

func TestModelInit_loads_existing_files_without_creating(t *testing.T) {
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	notes := note.NewService(repo, 10)
	docs := document.NewService(repo, 10)
	sheets := spreadsheet.NewService(repo, 10)
	decks := presentation.NewService(repo, 10)

	ctx := context.Background()

	// Pre-create files
	notePath := filepath.Join(t.TempDir(), "note.md")
	if _, err := notes.Create(ctx, notePath, "existing note body"); err != nil {
		t.Fatal(err)
	}

	docPath := filepath.Join(t.TempDir(), "doc.md")
	if _, err := docs.Create(ctx, docPath, document.Draft{Layout: document.DefaultLayout()}); err != nil {
		t.Fatal(err)
	}

	sheetPath := filepath.Join(t.TempDir(), "sheet.csv")
	if _, err := sheets.Create(ctx, sheetPath, nil); err != nil {
		t.Fatal(err)
	}

	deckPath := filepath.Join(t.TempDir(), "deck.md")
	if _, err := decks.Create(ctx, deckPath, "# Title\n\n## Slide 1\nContent"); err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name string
		kind artifact.Kind
		path string
	}{
		{"note", artifact.NoteKind, notePath},
		{"document", artifact.DocumentKind, docPath},
		{"spreadsheet", artifact.SpreadsheetKind, sheetPath},
		{"presentation", artifact.PresentationKind, deckPath},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(Config{
				Path:          tc.path,
				Kind:          tc.kind,
				Notes:         notes,
				Documents:     docs,
				Spreadsheets:  sheets,
				Presentations: decks,
				Recovery:      repo,
			})
			cmd := m.Init()
			if cmd == nil {
				t.Fatalf("expected non-nil cmd for %s", tc.name)
			}
			msg := cmd()
			openedMsg, ok := msg.(fileOpenedMsg)
			if !ok {
				t.Fatalf("expected fileOpenedMsg, got %T", msg)
			}
			if openedMsg.err != nil {
				t.Fatalf("unexpected error loading existing %s: %v", tc.name, openedMsg.err)
			}
			if openedMsg.created {
				t.Fatalf("expected existing %s file not to be marked as created", tc.name)
			}
		})
	}
}

func TestModelInit_detects_recovery_draft_for_note_and_document(t *testing.T) {
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	notes := note.NewService(repo, 10)
	docs := document.NewService(repo, 10)
	ctx := context.Background()

	notePath := filepath.Join(t.TempDir(), "note_with_draft.md")
	if _, err := notes.Create(ctx, notePath, "original body"); err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(t.TempDir(), "doc_with_draft.md")
	if _, err := docs.Create(ctx, docPath, document.Draft{Layout: document.DefaultLayout()}); err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name string
		path string
		kind artifact.Kind
		data string
		cfg  Config
	}{
		{"note", notePath, artifact.NoteKind, `{"body":"unsaved body draft"}`, Config{Path: notePath, Kind: artifact.NoteKind, Notes: notes, Recovery: repo}},
		{"document", docPath, artifact.DocumentKind, `{"body":"unsaved doc draft"}`, Config{Path: docPath, Kind: artifact.DocumentKind, Documents: docs, Recovery: repo}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			draft := recovery.Draft{
				Path:      tc.path,
				Kind:      string(tc.kind),
				Data:      json.RawMessage(tc.data),
				UpdatedAt: time.Now(),
			}
			if err := repo.SaveRecovery(ctx, draft); err != nil {
				t.Fatal(err)
			}

			m := NewModel(tc.cfg)
			cmd := m.Init()
			if cmd == nil {
				t.Fatalf("expected non-nil cmd from Init for %s", tc.name)
			}
			msg := cmd()
			openedMsg, ok := msg.(fileOpenedMsg)
			if !ok {
				t.Fatalf("expected fileOpenedMsg, got %T", msg)
			}
			if openedMsg.err != nil {
				t.Fatalf("unexpected error: %v", openedMsg.err)
			}
			if !openedMsg.hasDraft {
				t.Fatalf("expected hasDraft = true when %s recovery draft exists", tc.name)
			}
			if openedMsg.draft.Path != tc.path {
				t.Fatalf("expected draft path %q, got %q", tc.path, openedMsg.draft.Path)
			}
		})
	}
}

func TestModelInit_returns_error_when_service_is_nil_for_requested_kind(t *testing.T) {
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "note.md")

	// Configured with NoteKind but Notes service is nil
	m := NewModel(Config{
		Path:     path,
		Kind:     artifact.NoteKind,
		Recovery: repo,
	})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	openedMsg, ok := msg.(fileOpenedMsg)
	if !ok {
		t.Fatalf("expected fileOpenedMsg, got %T", msg)
	}
	if openedMsg.err == nil || !strings.Contains(openedMsg.err.Error(), "cannot open") {
		t.Fatalf("expected 'cannot open' error when service is nil, got %v", openedMsg.err)
	}
}

func TestModelInit_returns_error_when_file_load_fails_with_filesystem_error(t *testing.T) {
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	notes := note.NewService(repo, 10)

	// Pointing path to an existing directory causes create/open to fail
	dirPath := t.TempDir()

	m := NewModel(Config{
		Path:     dirPath,
		Kind:     artifact.NoteKind,
		Notes:    notes,
		Recovery: repo,
	})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	openedMsg, ok := msg.(fileOpenedMsg)
	if !ok {
		t.Fatalf("expected fileOpenedMsg, got %T", msg)
	}
	if openedMsg.err == nil {
		t.Fatal("expected error when opening directory as note file, got nil")
	}
}

func TestModelUpdateWindowSizeMsgAndDelegation(t *testing.T) {
	// Note stage
	hNote := newHarness(t, artifact.NoteKind)
	hNote.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	if hNote.m.width != 120 || hNote.m.height != 40 {
		t.Fatalf("note width/height = %d/%d, want 120/40", hNote.m.width, hNote.m.height)
	}

	// Document stage
	hDoc := newHarness(t, artifact.DocumentKind)
	hDoc.send(tea.WindowSizeMsg{Width: 110, Height: 35})
	if hDoc.m.width != 110 || hDoc.m.height != 35 {
		t.Fatalf("doc width/height = %d/%d, want 110/35", hDoc.m.width, hDoc.m.height)
	}
	if hDoc.m.documents == nil || hDoc.m.documents.width != 110 || hDoc.m.documents.height != 35 {
		t.Fatalf("documents screen width/height = %d/%d, want 110/35", hDoc.m.documents.width, hDoc.m.documents.height)
	}

	// Spreadsheet stage
	hSheet := newHarness(t, artifact.SpreadsheetKind)
	hSheet.send(tea.WindowSizeMsg{Width: 90, Height: 25})
	if hSheet.m.width != 90 || hSheet.m.height != 25 {
		t.Fatalf("spreadsheet width/height = %d/%d, want 90/25", hSheet.m.width, hSheet.m.height)
	}
	if hSheet.m.sheet == nil || hSheet.m.sheet.width != 90 || hSheet.m.sheet.height != 25 {
		t.Fatalf("sheet model width/height = %d/%d, want 90/25", hSheet.m.sheet.width, hSheet.m.sheet.height)
	}

	// Presentation stage
	hDeck := newHarness(t, artifact.PresentationKind)
	hDeck.send(tea.WindowSizeMsg{Width: 95, Height: 28})
	if hDeck.m.width != 95 || hDeck.m.height != 28 {
		t.Fatalf("presentation width/height = %d/%d, want 95/28", hDeck.m.width, hDeck.m.height)
	}
	if hDeck.m.deck == nil || hDeck.m.deck.width != 95 || hDeck.m.deck.height != 28 {
		t.Fatalf("deck model width/height = %d/%d, want 95/28", hDeck.m.deck.width, hDeck.m.deck.height)
	}
}

func TestModelUpdateStageDelegation(t *testing.T) {
	// 1. Spreadsheet stage delegation
	hSheet := newHarness(t, artifact.SpreadsheetKind)
	if hSheet.m.stage != stageSpreadsheet {
		t.Fatalf("stage = %d, want stageSpreadsheet", hSheet.m.stage)
	}
	// Down arrow in spreadsheet navigation mode
	hSheet.key("down")
	if hSheet.m.sheet.row != 1 {
		t.Fatalf("sheet row after down key = %d, want 1", hSheet.m.sheet.row)
	}

	// 2. Presentation stage delegation
	hDeck := newHarness(t, artifact.PresentationKind)
	if hDeck.m.stage != stagePresentation {
		t.Fatalf("stage = %d, want stagePresentation", hDeck.m.stage)
	}
	// 'e' toggles edit mode in presentation
	hDeck.key("e")
	if !hDeck.m.deck.editing {
		t.Fatal("deck editing = false after pressing 'e', want true")
	}

	// 3. Document stage key/mouse input delegation
	hDoc := newHarness(t, artifact.DocumentKind)
	if hDoc.m.stage != stageDocument {
		t.Fatalf("stage = %d, want stageDocument", hDoc.m.stage)
	}
	hDoc.typeText("Typed in document")
	if hDoc.m.documents.body.Value() != "Typed in document" {
		t.Fatalf("document body = %q, want 'Typed in document'", hDoc.m.documents.body.Value())
	}

	// 4. documentMessage delegation
	var dummyDocMsg documentMessage = documentAutosaveFinishedMsg{session: 999, err: nil}
	_, cmd := hDoc.m.Update(dummyDocMsg)
	// Sending documentMessage shouldn't panic and returns a command or nil
	_ = cmd
}

func TestModelUpdateAutosaveAndRecoveryMessages(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)

	// 1. autosaveTickMsg
	// Stage note & matching session triggering saveNoteRecovery
	h.typeText("dirty note")
	h.m.autosaveSession = 42
	h.m.stage = stageNote
	_, cmd := h.m.Update(autosaveTickMsg{session: 42})
	if cmd == nil {
		t.Fatal("autosaveTickMsg on matching session did not return a command")
	}

	// Mismatched session should do nothing
	_, cmdMismatch := h.m.Update(autosaveTickMsg{session: 99})
	if cmdMismatch != nil {
		t.Fatal("autosaveTickMsg on mismatched session returned a command")
	}

	// 2. autosaveFinishedMsg
	// Matching session with error and cleared
	cancelCalled := false
	h.m.autosaveCancel = func() { cancelCalled = true }
	h.m.draftStored = true

	testErr := errors.New("write error")
	h.m.Update(autosaveFinishedMsg{
		session: 42,
		err:     testErr,
		cleared: false,
	})

	if !cancelCalled {
		t.Fatal("autosaveFinishedMsg did not invoke autosaveCancel")
	}
	if !strings.Contains(h.m.errMessage, "Autosave failed: write error") {
		t.Fatalf("errMessage = %q, want containing 'Autosave failed: write error'", h.m.errMessage)
	}

	// Cleared with no error
	h.m.draftStored = true
	h.m.Update(autosaveFinishedMsg{
		session: 42,
		err:     nil,
		cleared: true,
	})
	if h.m.draftStored {
		t.Fatal("autosaveFinishedMsg with cleared=true did not set draftStored to false")
	}

	// Mismatched session for autosaveFinishedMsg
	h.m.errMessage = ""
	h.m.Update(autosaveFinishedMsg{
		session: 100,
		err:     testErr,
	})
	if h.m.errMessage != "" {
		t.Fatal("mismatched autosaveFinishedMsg session updated errMessage")
	}

	// 3. recoveryDiscardedMsg with error
	discardErr := errors.New("disk permission error")
	h.m.Update(recoveryDiscardedMsg{err: discardErr})
	if !strings.Contains(h.m.errMessage, "Discard autosaved draft: disk permission error") {
		t.Fatalf("errMessage = %q, want containing discard error", h.m.errMessage)
	}

	// 4. Unhandled message type
	type customUnknownMsg struct{}
	h.m.errMessage = ""
	model, cmdUnknown := h.m.Update(customUnknownMsg{})
	if model != h.m || cmdUnknown != nil {
		t.Fatalf("unhandled msg returned cmd %v", cmdUnknown)
	}
}

func TestNewModel_ZeroConfigDefaults(t *testing.T) {
	m := NewModel(Config{})
	if m.path != "" || m.kind != "" || m.edit != false {
		t.Errorf("expected empty path/kind/edit, got path=%q kind=%q edit=%t", m.path, m.kind, m.edit)
	}
	if m.service != nil || m.spreadsheets != nil || m.presentations != nil || m.recoveryStore != nil {
		t.Errorf("expected nil services/store for zero config")
	}
	if m.documents != nil {
		t.Errorf("expected nil documents screen")
	}
	if m.stage != stageChooseKind {
		t.Errorf("stage = %v, want stageChooseKind", m.stage)
	}
	if m.canUndo || m.canRedo {
		t.Errorf("canUndo/canRedo should be false when notes service is nil")
	}
}

func TestNewModel_EmptyKindSetsChooseKindStage(t *testing.T) {
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	notes := note.NewService(repo, 10)
	docs := document.NewService(repo, 10)
	sheets := spreadsheet.NewService(repo, 10)
	decks := presentation.NewService(repo, 10)
	_ = docs

	cfg := Config{
		Path:          "/path/to/file.md",
		Kind:          "",
		Edit:          true,
		Notes:         notes,
		Spreadsheets:  sheets,
		Presentations: decks,
		Recovery:      repo,
	}
	m := NewModel(cfg)
	if m.path != cfg.Path || m.kind != cfg.Kind || m.edit != cfg.Edit {
		t.Errorf("fields not matched: path=%q kind=%q edit=%t", m.path, m.kind, m.edit)
	}
	if m.service != cfg.Notes || m.spreadsheets != cfg.Spreadsheets || m.presentations != cfg.Presentations || m.recoveryStore != cfg.Recovery {
		t.Errorf("services/store not assigned correctly")
	}
	if m.stage != stageChooseKind {
		t.Errorf("stage = %v, want stageChooseKind (%v)", m.stage, stageChooseKind)
	}
	if m.documents != nil {
		t.Errorf("documents screen should be nil when config.Documents is nil")
	}
}

func TestNewModel_SpecifiedKindKeepsDefaultStage(t *testing.T) {
	cfg := Config{
		Path: "/path/to/file.md",
		Kind: artifact.NoteKind,
	}
	m := NewModel(cfg)
	if m.stage != stageOpening {
		t.Errorf("stage = %v, want stageOpening (%v)", m.stage, stageOpening)
	}
}

func TestNewModel_NotesServiceUndoRedoState(t *testing.T) {
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	notes := note.NewService(repo, 10)
	cfg := Config{
		Notes: notes,
	}
	m := NewModel(cfg)
	if m.canUndo != notes.CanUndo() || m.canRedo != notes.CanRedo() {
		t.Errorf("canUndo=%t canRedo=%t, want notes service state (%t, %t)", m.canUndo, m.canRedo, notes.CanUndo(), notes.CanRedo())
	}
}

func TestNewModel_DocumentsServiceInitializationAndAutosaveScheduler(t *testing.T) {
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	docs := document.NewService(repo, 10)
	cfg := Config{
		Path:      "/path/to/file.md",
		Documents: docs,
		Recovery:  repo,
	}
	m := NewModel(cfg)
	if m.documents == nil {
		t.Fatal("documents screen is nil")
	}
	if m.documents.service != docs {
		t.Errorf("documents service = %v, want %v", m.documents.service, docs)
	}
	if m.documents.recoveryStore != repo {
		t.Errorf("documents recoveryStore = %v, want %v", m.documents.recoveryStore, repo)
	}
	if m.documents.autosaveScheduler == nil {
		t.Fatal("documents autosaveScheduler is nil")
	}

	// Exercise the documents autosaveScheduler callback and its returned command
	cmd := m.documents.autosaveScheduler(42)
	if cmd == nil {
		t.Fatal("autosaveScheduler returned nil tea.Cmd")
	}
}

func TestNewModel_OperationContextFunction(t *testing.T) {
	cfg := Config{Path: "/path/to/file.md"}
	m := NewModel(cfg)
	if m.newOperationContext == nil {
		t.Fatal("newOperationContext is nil")
	}
	ctx, cancel := m.newOperationContext()
	if ctx == nil || cancel == nil {
		t.Fatal("newOperationContext returned nil context or cancel func")
	}
	cancel()
	if ctx.Err() == nil {
		t.Errorf("expected cancelled context, got nil error")
	}
}
