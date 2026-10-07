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

	"github.com/charmbracelet/bubbles/cursor"
	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/filerepo"
	"example.com/parchment/internal/note"
)

// harness wires the editor to real services over files in temporary
// directories: one for the artifact and one standing in for ~/.parchment.
type harness struct {
	t     *testing.T
	m     *Model
	repo  *filerepo.Repository
	notes *note.Service
	docs  *document.Service
	path  string
	quit  bool
}

func newHarness(t *testing.T, kind artifact.Kind) *harness {
	t.Helper()
	repo, err := filerepo.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		t: t, repo: repo,
		notes: note.NewService(repo, 10), docs: document.NewService(repo, 10),
		path: filepath.Join(t.TempDir(), "report.md"),
	}
	h.open(kind)
	return h
}

// open starts a fresh editor session on the harness file, as running
// `parchment tui` again would.
func (h *harness) open(kind artifact.Kind) {
	h.t.Helper()
	model := NewModel(Config{Path: h.path, Kind: kind, Notes: h.notes, Documents: h.docs, Recovery: h.repo})
	h.m, h.quit = &model, false
	// Blinking cursors return tea.Tick commands, which the harness would run
	// synchronously.
	h.m.titleInput.Cursor.SetMode(cursor.CursorStatic)
	h.m.bodyInput.Cursor.SetMode(cursor.CursorStatic)
	if s := h.m.documents; s != nil {
		s.titleInput.Cursor.SetMode(cursor.CursorStatic)
		s.body.Cursor.SetMode(cursor.CursorStatic)
		s.promptInput.Cursor.SetMode(cursor.CursorStatic)
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
	_, cmd := h.m.Update(message)
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

func TestNoteSaveStaysInEditorAndCloseQuits(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.key("tab")
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
	h.key("tab")
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
	h.m.titleInput.SetValue("Edited\x1b[2J")
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
}

func TestEditorRefusesNotesItWouldNormalize(t *testing.T) {
	cases := []struct{ name, title, body string }{
		{"long title", strings.Repeat("x", 201), "body"},
		{"tab in body", "Title", "before\tafter"},
		{"control in body", "Title", "before\x1b[2Jafter"},
		{"oversized body", "Title", strings.Repeat("x", 1_000_001)},
		{"too many body lines", "Title", strings.Repeat("x\n", 10_000) + "last"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "")
			if _, err := h.notes.Create(context.Background(), h.path, tc.title, tc.body); err != nil {
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
	h.key("tab")
	h.typeText("Edited body")
	external := h.m.snapshot
	external.Title = "Changed in a text editor"
	if err := h.repo.Transition(ctx, h.path, &h.m.snapshot, &external); err != nil {
		t.Fatal(err)
	}
	h.key("ctrl+s")
	if h.m.stage != stageNote || !strings.Contains(h.m.errMessage, "changed since it was loaded") {
		t.Fatalf("stage = %d, error = %q", h.m.stage, h.m.errMessage)
	}
	current, err := h.notes.Get(ctx, h.path)
	if err != nil || current.Title != external.Title || current.Body != "" {
		t.Fatalf("stale editor overwrote external update: %+v, %v", current, err)
	}
}

func TestPreviewCanScroll(t *testing.T) {
	h := newHarness(t, "")
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("Line %02d", i+1)
	}
	if _, err := h.notes.Create(context.Background(), h.path, "Long", strings.Join(lines, "\n")); err != nil {
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
	created, err := h.notes.Create(ctx, h.path, "Original", "Before")
	if err != nil {
		t.Fatal(err)
	}
	withBlock := created
	withBlock.Blocks = map[string]json.RawMessage{"parchment-extra": json.RawMessage(`{"kept":true}`)}
	if err := h.repo.Transition(ctx, h.path, &created, &withBlock); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	h.m.titleInput.SetValue("Recovered title")
	h.m.titleInput.CursorEnd()
	h.m.bodyInput.SetValue("Recovered body")
	h.run(h.m.saveNoteRecovery(h.m.autosaveSession))
	if !h.hasDraft() {
		t.Fatal("autosave wrote no draft")
	}

	// Simulate a crash: reopen without closing.
	h.open(artifact.NoteKind)
	if h.m.stage != stageRecovery || !strings.Contains(h.m.View(), "Recovered title") {
		t.Fatalf("stage = %d, view:\n%s", h.m.stage, h.m.View())
	}
	h.key("r")
	if h.m.stage != stageNote || h.m.titleInput.Value() != "Recovered title" || h.m.bodyInput.Value() != "Recovered body" {
		t.Fatalf("recovered editor: stage=%d title=%q body=%q", h.m.stage, h.m.titleInput.Value(), h.m.bodyInput.Value())
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
	if _, err := h.notes.Update(ctx, h.path, "report", "changed elsewhere"); err != nil {
		t.Fatal(err)
	}
	h.open(artifact.NoteKind)
	h.key("r")
	if !strings.Contains(h.m.status, "saving will report a conflict") {
		t.Fatalf("status = %q", h.m.status)
	}
}

func TestClosingNormallyDeletesDraft(t *testing.T) {
	h := newHarness(t, artifact.NoteKind)
	h.key("tab")
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
