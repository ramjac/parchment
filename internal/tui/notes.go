package tui

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/recovery"
)

// notesScreen manages the note editor UI state, previewing, and autosaving.
type notesScreen struct {
	service       *note.Service
	path          string
	recoveryStore recovery.Store
	width, height int
	theme         theme

	snapshot          note.Note
	originalBody      string
	bodyInput         textarea.Model
	previewing        bool
	preview           viewport.Model
	discardWarning    bool
	canUndo           bool
	canRedo           bool
	autosaveSession   uint64
	autosaveCancel    context.CancelFunc
	draftStored       bool
	autosaveScheduler func(uint64) tea.Cmd

	newOperationContext func() (context.Context, context.CancelFunc)
}

func newNotesScreen(service *note.Service, path string, store recovery.Store, t theme, newContext func() (context.Context, context.CancelFunc)) *notesScreen {
	body := textarea.New()
	body.Prompt = ""
	body.ShowLineNumbers = false
	body.Placeholder = "Write Markdown…"
	body.CharLimit = 1_000_000

	s := &notesScreen{
		service:             service,
		path:                path,
		recoveryStore:       store,
		theme:               t,
		bodyInput:           body,
		preview:             viewport.New(0, 0),
		newOperationContext: newContext,
	}
	if service != nil {
		s.canUndo, s.canRedo = service.CanUndo(), service.CanRedo()
	}
	s.autosaveScheduler = func(session uint64) tea.Cmd {
		return tea.Tick(autosaveInterval, func(time.Time) tea.Msg {
			return autosaveTickMsg{session: session}
		})
	}
	return s
}

func (s *notesScreen) startAutosaveSession() {
	s.autosaveSession++
	if s.autosaveCancel != nil {
		s.autosaveCancel()
		s.autosaveCancel = nil
	}
}

func (s *notesScreen) scheduleAutosave(session uint64) tea.Cmd {
	if s.recoveryStore == nil || s.autosaveScheduler == nil {
		return nil
	}
	return s.autosaveScheduler(session)
}

func (s *notesScreen) stopAutosave() { s.startAutosaveSession() }

func (s *notesScreen) saveNoteRecovery(session uint64) tea.Cmd {
	if s.recoveryStore == nil {
		return nil
	}
	if !s.dirty() {
		if !s.draftStored {
			return s.scheduleAutosave(session)
		}
		// The edits were reverted after a draft was autosaved; remove it so a
		// crash does not offer changes the user already undid.
		store, path := s.recoveryStore, s.path
		return func() tea.Msg {
			return autosaveFinishedMsg{session: session, err: store.DeleteRecovery(context.Background(), path), cleared: true}
		}
	}
	s.draftStored = true
	data := noteRecoveryData{
		Snapshot: s.snapshot, SnapshotBody: s.snapshot.Body,
		SnapshotBlocks: cloneRawMessages(s.snapshot.Blocks),
		Body:           s.bodyInput.Value(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.autosaveCancel = cancel
	store := s.recoveryStore
	draft := recovery.Draft{
		Path: s.path, Kind: string(artifact.NoteKind), UpdatedAt: time.Now().UTC(),
	}
	return func() tea.Msg {
		encoded, err := json.Marshal(data)
		if err == nil {
			draft.Data = encoded
			err = store.SaveRecovery(ctx, draft)
		}
		return autosaveFinishedMsg{session: session, err: err}
	}
}

func (s *notesScreen) refreshHistoryAvailability() {
	if s.service != nil {
		s.canUndo = s.service.CanUndo()
		s.canRedo = s.service.CanRedo()
	}
}

func (s *notesScreen) startEdit(n note.Note) (string, bool) {
	s.bodyInput.SetValue(n.Body)
	if s.bodyInput.Value() != n.Body {
		return "This note exceeds editor limits or contains text the editor cannot preserve; edit it with a text editor", false
	}
	s.loadNote(n)
	s.resizeEditors()
	s.startAutosaveSession()
	return "", true
}

func (s *notesScreen) loadNote(n note.Note) {
	s.snapshot = n
	s.originalBody = n.Body
	s.bodyInput.SetValue(n.Body)
	s.discardWarning = false
}

func (s *notesScreen) saveNote(startOp func() context.Context) tea.Cmd {
	body, expected := s.bodyInput.Value(), s.snapshot
	store, path := s.recoveryStore, s.path
	service := s.service
	ctx := startOp()
	return func() tea.Msg {
		n, err := service.UpdateExpected(ctx, expected, body)
		var cleanupErr error
		if err == nil && store != nil {
			cleanupErr = store.DeleteRecovery(context.Background(), path)
		}
		return noteSavedMsg{note: n, err: err, cleanupErr: cleanupErr}
	}
}

func (s *notesScreen) history(undo bool, startOp func() context.Context) tea.Cmd {
	ctx := startOp()
	service, path := s.service, s.path
	return func() tea.Msg {
		run, prefix := service.Redo, "Redid "
		if undo {
			run, prefix = service.Undo, "Undid "
		}
		description, err := run(ctx)
		if err != nil {
			return historyChangedMsg{err: err}
		}
		n, err := service.Get(ctx, path)
		return historyChangedMsg{description: prefix + strings.ToLower(description), note: n, err: err}
	}
}

func (s *notesScreen) resizePreview() {
	s.preview.Width, s.preview.Height = max(s.width-2, 1), max(s.height-4, 1)
	n := s.snapshot
	n.Body = s.bodyInput.Value()
	s.preview.SetContent(preview(n))
	s.preview.GotoTop()
}

func (s *notesScreen) resizeEditors() {
	width := max(s.width-8, 20)
	s.bodyInput.SetWidth(width)
	s.bodyInput.SetHeight(max(s.height-5, 3))
	if s.previewing {
		s.resizePreview()
	}
}

func (s *notesScreen) resize(width, height int) {
	s.width, s.height = width, height
	s.resizeEditors()
}

func (s *notesScreen) dirty() bool {
	return s.bodyInput.Value() != s.originalBody
}
