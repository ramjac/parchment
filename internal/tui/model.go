package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/recovery"
	"example.com/parchment/internal/spreadsheet"
)

// Config selects the one artifact file the interactive editor opens.
type Config struct {
	// Path is the artifact file. A missing file is created.
	Path string
	// Kind is the artifact kind. When empty, the editor asks which kind to
	// create.
	Kind          artifact.Kind
	Notes         *note.Service
	Documents     *document.Service
	Spreadsheets  *spreadsheet.Service
	Presentations *presentation.Service
	Recovery      recovery.Store
}

type stage int

const (
	stageOpening stage = iota
	stageChooseKind
	stageRecovery
	stageNote
	stageDocument
	stageSpreadsheet
	stagePresentation
	stageFailed
)

type fileOpenedMsg struct {
	kind     artifact.Kind
	note     note.Note
	document document.Document
	book     spreadsheet.Spreadsheet
	deck     presentation.Presentation
	created  bool
	draft    recovery.Draft
	hasDraft bool
	err      error
}

type noteSavedMsg struct {
	note       note.Note
	err        error
	cleanupErr error
}

type historyChangedMsg struct {
	description string
	note        note.Note
	err         error
}

type recoveryDiscardedMsg struct{ err error }
type autosaveTickMsg struct{ session uint64 }
type autosaveFinishedMsg struct {
	session uint64
	err     error
	// cleared reports that the draft was deleted because the editor was clean.
	cleared bool
}

const autosaveInterval = 2 * time.Second

type theme struct {
	primary lipgloss.AdaptiveColor
	border  lipgloss.AdaptiveColor
}

// Model is the interactive editor for one artifact file. It owns UI state
// only; artifact rules live in the services.
type Model struct {
	path          string
	kind          artifact.Kind
	service       *note.Service
	spreadsheets  *spreadsheet.Service
	presentations *presentation.Service
	recoveryStore recovery.Store
	stage         stage
	width         int
	height        int
	theme         theme
	pending       bool
	status        string
	errMessage    string

	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc

	// Startup state.
	opened fileOpenedMsg

	// Note editor state.
	snapshot        note.Note
	originalBody    string
	bodyInput       textarea.Model
	previewing      bool
	preview         viewport.Model
	discardWarning  bool
	canUndo         bool
	canRedo         bool
	autosaveSession uint64
	autosaveCancel  context.CancelFunc
	// draftStored reports that a recovery draft for this file may exist.
	draftStored       bool
	autosaveScheduler func(uint64) tea.Cmd

	documents *documentsScreen
	sheet     *spreadsheetModel
	deck      *presentationModel
}

// NewModel creates the interactive editor for config.Path.
func NewModel(config Config) Model {
	body := textarea.New()
	body.Prompt = ""
	body.ShowLineNumbers = false
	body.Placeholder = "Write Markdown…"
	body.CharLimit = 1_000_000
	m := Model{
		path: config.Path, kind: config.Kind,
		service: config.Notes, spreadsheets: config.Spreadsheets, presentations: config.Presentations,
		recoveryStore: config.Recovery,
		bodyInput:     body, preview: viewport.New(0, 0),
		newOperationContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
		theme: theme{
			primary: lipgloss.AdaptiveColor{Light: "#4b3f72", Dark: "#c4b5fd"},
			border:  lipgloss.AdaptiveColor{Light: "#b8b4c7", Dark: "#55516a"},
		},
	}
	if m.service != nil {
		m.canUndo, m.canRedo = m.service.CanUndo(), m.service.CanRedo()
	}
	m.autosaveScheduler = func(session uint64) tea.Cmd {
		return tea.Tick(autosaveInterval, func(time.Time) tea.Msg {
			return autosaveTickMsg{session: session}
		})
	}
	if config.Documents != nil {
		m.documents = newDocumentsScreen(config.Documents, config.Path, m.theme, m.newOperationContext)
		m.documents.recoveryStore = config.Recovery
		m.documents.autosaveScheduler = func(session uint64) tea.Cmd {
			return tea.Tick(autosaveInterval, func(time.Time) tea.Msg {
				return documentAutosaveTickMsg{session: session}
			})
		}
	}
	if m.kind == "" {
		m.stage = stageChooseKind
	}
	return m
}

// Init opens the file unless the user must first choose a kind.
func (m *Model) Init() tea.Cmd {
	if m.stage == stageChooseKind {
		return nil
	}
	return m.openFile(m.kind)
}

// openFile loads the file, creating it when missing, and looks for an
// autosaved draft of note or document edits for its path.
func (m *Model) openFile(kind artifact.Kind) tea.Cmd {
	m.kind, m.stage, m.pending = kind, stageOpening, true
	ctx := m.startOperation()
	path, notes, store := m.path, m.service, m.recoveryStore
	sheets, decks := m.spreadsheets, m.presentations
	var documents *document.Service
	if m.documents != nil {
		documents = m.documents.service
	}
	return func() tea.Msg {
		msg := fileOpenedMsg{kind: kind}
		switch {
		case kind == artifact.NoteKind && notes != nil:
			msg.note, msg.err = notes.Get(ctx, path)
			if errors.Is(msg.err, note.ErrNotFound) {
				msg.note, msg.err = notes.Create(ctx, path, "")
				msg.created = true
			}
		case kind == artifact.DocumentKind && documents != nil:
			msg.document, msg.err = documents.Get(ctx, path)
			if errors.Is(msg.err, document.ErrNotFound) {
				msg.document, msg.err = documents.Create(ctx, path, document.Draft{Layout: document.DefaultLayout()})
				msg.created = true
			}
		case kind == artifact.SpreadsheetKind && sheets != nil:
			msg.book, msg.err = sheets.Get(ctx, path)
			if errors.Is(msg.err, spreadsheet.ErrNotFound) {
				msg.book, msg.err = sheets.Create(ctx, path, nil)
				msg.created = true
			}
			return msg
		case kind == artifact.PresentationKind && decks != nil:
			msg.deck, msg.err = decks.Get(ctx, path)
			if errors.Is(msg.err, presentation.ErrNotFound) {
				msg.deck, msg.err = decks.Create(ctx, path, "")
				msg.created = true
			}
			return msg
		default:
			msg.err = fmt.Errorf("the interactive editor cannot open %s artifacts", kind)
		}
		if msg.err == nil && store != nil {
			msg.draft, msg.hasDraft, msg.err = store.LoadRecovery(ctx, path)
		}
		return msg
	}
}

// Update applies a terminal message to the editor.
func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch {
	case m.stage == stageSpreadsheet && m.sheet != nil:
		_, cmd := m.sheet.Update(message)
		return m, cmd
	case m.stage == stagePresentation && m.deck != nil:
		_, cmd := m.deck.Update(message)
		return m, cmd
	}
	if size, ok := message.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		m.resizeEditors()
		if m.documents != nil {
			m.documents.resize(size.Width, size.Height)
		}
		return m, nil
	}
	if _, ok := message.(documentMessage); ok && m.documents != nil {
		return m, m.documents.update(message)
	}
	if m.stage == stageDocument && m.documents != nil {
		switch message.(type) {
		case tea.KeyMsg, tea.MouseMsg:
			return m, m.documents.update(message)
		}
	}
	switch msg := message.(type) {
	case fileOpenedMsg:
		m.finishOperation()
		m.pending = false
		if msg.err != nil {
			m.stage, m.errMessage = stageFailed, msg.err.Error()
			return m, nil
		}
		m.opened = msg
		if msg.created {
			m.status = "Created " + m.path
		}
		if msg.hasDraft {
			m.stage = stageRecovery
			return m, nil
		}
		return m, m.enterEditor()
	case recoveryDiscardedMsg:
		if msg.err != nil {
			m.errMessage = "Discard autosaved draft: " + msg.err.Error()
			return m, nil
		}
		m.status = "Autosaved draft discarded"
		return m, m.enterEditor()
	case autosaveTickMsg:
		if msg.session == m.autosaveSession && m.stage == stageNote {
			return m, m.saveNoteRecovery(msg.session)
		}
	case autosaveFinishedMsg:
		if msg.session == m.autosaveSession {
			if m.autosaveCancel != nil {
				m.autosaveCancel()
				m.autosaveCancel = nil
			}
			if msg.cleared && msg.err == nil {
				m.draftStored = false
			}
			if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
				m.errMessage = "Autosave failed: " + msg.err.Error()
			}
			if m.stage == stageNote {
				return m, m.scheduleAutosave(msg.session)
			}
		}
	case noteSavedMsg:
		m.finishOperation()
		m.pending = false
		m.refreshHistoryAvailability()
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				m.errMessage, m.status = "", "Save cancelled; unsaved changes remain"
			} else {
				m.errMessage = msg.err.Error()
			}
			m.startAutosaveSession()
			return m, m.scheduleAutosave(m.autosaveSession)
		}
		m.loadNote(msg.note)
		m.errMessage, m.status = "", "Saved “"+msg.note.Title+"”"
		if msg.cleanupErr != nil {
			m.status += " (could not remove recovery draft: " + msg.cleanupErr.Error() + ")"
		}
		m.startAutosaveSession()
		return m, m.scheduleAutosave(m.autosaveSession)
	case historyChangedMsg:
		m.finishOperation()
		m.pending = false
		m.refreshHistoryAvailability()
		if msg.err != nil {
			m.errMessage = msg.err.Error()
			return m, nil
		}
		m.loadNote(msg.note)
		m.errMessage, m.status = "", msg.description
	case tea.MouseMsg:
		if m.stage == stageNote && !m.pending && !m.previewing &&
			msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			// The body starts below the header, state, and blank lines.
			if placeTextareaCursor(&m.bodyInput, msg.X, msg.Y-3) {
				return m, m.bodyInput.Focus()
			}
		}
	case tea.KeyMsg:
		return m, m.updateKey(msg)
	}
	return m, nil
}

// enterEditor opens the loaded artifact in its editor.
func (m *Model) enterEditor() tea.Cmd {
	switch m.opened.kind {
	case artifact.NoteKind:
		if !m.startEdit(m.opened.note) {
			m.stage = stageFailed
			return nil
		}
		m.stage = stageNote
		return tea.Batch(m.bodyInput.Focus(), m.scheduleAutosave(m.autosaveSession))
	case artifact.SpreadsheetKind:
		m.sheet = newSpreadsheetModel(m.spreadsheets, m.path)
		m.sheet.newOperationContext, m.sheet.theme = m.newOperationContext, m.theme
		m.sheet.width, m.sheet.height = m.width, m.height
		m.sheet.setBook(m.opened.book)
		m.sheet.status = m.status
		m.stage = stageSpreadsheet
		_, cmd := m.sheet.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		return cmd
	case artifact.PresentationKind:
		m.deck = newPresentationModel(m.presentations, m.path)
		m.deck.newOperationContext, m.deck.theme = m.newOperationContext, m.theme
		m.deck.width, m.deck.height = m.width, m.height
		m.deck.setItem(m.opened.deck)
		m.deck.status = m.status
		m.stage = stagePresentation
		_, cmd := m.deck.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		return cmd
	case artifact.DocumentKind:
		// Existing documents open in the reader; new ones start in the editor.
		m.stage = stageDocument
		if !m.opened.created {
			m.documents.startReading(m.opened.document)
			m.documents.status = m.status
			return nil
		}
		cmd, ok := m.documents.startEdit(m.opened.document)
		if !ok {
			m.stage, m.errMessage = stageFailed, m.documents.errMessage
			return nil
		}
		m.documents.status = m.status
		return cmd
	}
	return nil
}

func (m *Model) updateKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	if m.pending {
		if key == "ctrl+c" || key == "q" && m.stage != stageNote {
			if m.cancelOperation != nil {
				m.cancelOperation()
			}
			if m.stage == stageNote {
				m.status = "Cancelling save…"
				return nil
			}
			return tea.Quit
		}
		m.status = "Please wait for the current operation to finish"
		return nil
	}
	switch m.stage {
	case stageOpening, stageFailed:
		if key == "q" || key == "ctrl+c" || key == "esc" {
			return tea.Quit
		}
	case stageChooseKind:
		switch key {
		case "n":
			return m.openFile(artifact.NoteKind)
		case "d":
			if m.documents != nil {
				return m.openFile(artifact.DocumentKind)
			}
		case "s":
			if m.spreadsheets != nil {
				return m.openFile(artifact.SpreadsheetKind)
			}
		case "p":
			if m.presentations != nil {
				return m.openFile(artifact.PresentationKind)
			}
		case "q", "esc", "ctrl+c":
			return tea.Quit
		}
	case stageRecovery:
		switch key {
		case "r":
			return m.recoverDraft()
		case "d":
			store, path := m.recoveryStore, m.path
			return func() tea.Msg {
				return recoveryDiscardedMsg{err: store.DeleteRecovery(context.Background(), path)}
			}
		case "q", "esc", "ctrl+c":
			return tea.Quit
		}
	case stageNote:
		return m.updateNoteKey(msg)
	}
	return nil
}

func (m *Model) updateNoteKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	if m.tooSmall() {
		if key == "q" && !m.dirty() {
			return m.close()
		}
		return nil
	}
	if key != "esc" && key != "ctrl+c" {
		m.discardWarning = false
	}
	if m.previewing {
		switch key {
		case "esc", "f5":
			m.previewing = false
		case "ctrl+c":
			return m.requestClose()
		default:
			m.preview, _ = m.preview.Update(msg)
		}
		return nil
	}
	switch key {
	case "esc", "ctrl+c":
		return m.requestClose()
	case "ctrl+s":
		m.stopAutosave()
		m.pending = true
		return m.saveNote()
	case "f5":
		m.previewing = true
		m.resizePreview()
		return nil
	case "ctrl+z", "ctrl+r":
		if m.dirty() {
			m.status = "Save or discard your edits before undo or redo"
			return nil
		}
		return m.history(key == "ctrl+z")
	}
	var command tea.Cmd
	m.bodyInput, command = m.bodyInput.Update(msg)
	return command
}

// requestClose quits, first asking for confirmation when edits are unsaved.
func (m *Model) requestClose() tea.Cmd {
	if m.dirty() && !m.discardWarning {
		m.discardWarning = true
		m.status = "Unsaved changes: Ctrl+S saves, Esc again discards and quits"
		return nil
	}
	return m.close()
}

// close discards the autosaved draft and quits. Only an abnormal exit leaves
// a draft to recover.
func (m *Model) close() tea.Cmd {
	m.stopAutosave()
	return closeEditor(m.recoveryStore, m.path)
}

func closeEditor(store recovery.Store, path string) tea.Cmd {
	if store == nil {
		return tea.Quit
	}
	return tea.Sequence(func() tea.Msg {
		_ = store.DeleteRecovery(context.Background(), path)
		return nil
	}, tea.Quit)
}

func (m *Model) history(undo bool) tea.Cmd {
	m.pending = true
	ctx := m.startOperation()
	service, path := m.service, m.path
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

// View renders the current model without performing application operations.
func (m Model) View() string {
	if m.tooSmall() {
		return "parchment — terminal too small (minimum 40×10)\nResize the terminal, or press q to quit."
	}
	header := lipgloss.NewStyle().Bold(true).Foreground(m.theme.primary).
		Render("parchment  ·  " + sanitizeTerminalLine(m.path))
	switch m.stage {
	case stageOpening:
		return header + "\n\nOpening…" + m.statusLine()
	case stageFailed:
		return header + "\n\nThis file cannot be opened in the editor.\n\nq quits" + m.statusLine()
	case stageChooseKind:
		options := "n  Note"
		if m.documents != nil {
			options += "\nd  Document"
		}
		if m.spreadsheets != nil {
			options += "\ns  Spreadsheet"
		}
		if m.presentations != nil {
			options += "\np  Presentation"
		}
		return header + "\n\nThis file does not exist yet. What kind of artifact should Parchment create?\n\n" +
			options + "\n\nq quits without creating the file" + m.statusLine()
	case stageRecovery:
		draft := m.opened.draft
		return header + "\n\nAn autosaved draft of unsaved edits to this file exists.\n\n" +
			"Autosaved: " + draft.UpdatedAt.Local().Format("2006-01-02 15:04") + "\n\n" +
			"r  Recover the draft into the editor\n" +
			"d  Discard the draft and open the saved file\n" +
			"q  Quit and keep the draft" + m.statusLine()
	case stageDocument:
		return m.documents.view(header)
	case stageSpreadsheet:
		return m.sheet.View()
	case stagePresentation:
		return m.deck.View()
	}
	state := "Editing note"
	if m.dirty() {
		state += " • unsaved"
	}
	if m.previewing {
		return header + "\nPreview  ·  ↑/↓ scroll  ·  Esc returns to the editor\n" + m.preview.View() + m.statusLine()
	}
	bodyInput := m.bodyInput
	sanitizeTextareaView(&bodyInput)
	return header + "\n" + state + "  ·  Ctrl+S saves  ·  F5 preview  ·  Ctrl+Z/Ctrl+R undo/redo  ·  Esc quits\n\n" +
		bodyInput.View() + m.statusLine()
}

func (m Model) tooSmall() bool { return m.width < 40 || m.height < 10 }

func (m Model) statusLine() string {
	undo, redo := "undo unavailable", "redo unavailable"
	if m.canUndo {
		undo = "undo available"
	}
	if m.canRedo {
		redo = "redo available"
	}
	message := m.status
	if m.errMessage != "" {
		message = "Error: " + m.errMessage
	}
	if message == "" {
		message = "Ready"
	}
	return "\n" + sanitizeTerminalLine(message) + "  ·  " + undo + "  ·  " + redo
}

func (m *Model) refreshHistoryAvailability() {
	m.canUndo = m.service.CanUndo()
	m.canRedo = m.service.CanRedo()
}

// startEdit loads n into the editor, refusing notes the editor widgets would
// truncate or alter.
func (m *Model) startEdit(n note.Note) bool {
	m.bodyInput.SetValue(n.Body)
	if m.bodyInput.Value() != n.Body {
		m.errMessage = "This note exceeds editor limits or contains text the editor cannot preserve; edit it with a text editor"
		m.status = ""
		return false
	}
	m.loadNote(n)
	m.resizeEditors()
	m.errMessage = ""
	m.startAutosaveSession()
	return true
}

// loadNote makes n the saved state of the editor.
func (m *Model) loadNote(n note.Note) {
	m.snapshot = n
	m.originalBody = n.Body
	m.bodyInput.SetValue(n.Body)
	m.discardWarning = false
}

func (m *Model) saveNote() tea.Cmd {
	body, expected := m.bodyInput.Value(), m.snapshot
	store, path, service := m.recoveryStore, m.path, m.service
	ctx := m.startOperation()
	return func() tea.Msg {
		n, err := service.UpdateExpected(ctx, expected, body)
		var cleanupErr error
		if err == nil && store != nil {
			cleanupErr = store.DeleteRecovery(context.Background(), path)
		}
		return noteSavedMsg{note: n, err: err, cleanupErr: cleanupErr}
	}
}

func (m *Model) startOperation() context.Context {
	ctx, cancel := m.newOperationContext()
	m.cancelOperation = cancel
	return ctx
}

func (m *Model) finishOperation() {
	if m.cancelOperation != nil {
		m.cancelOperation()
		m.cancelOperation = nil
	}
}

func (m *Model) resizePreview() {
	m.preview.Width, m.preview.Height = max(m.width-2, 1), max(m.height-4, 1)
	n := m.snapshot
	n.Body = m.bodyInput.Value()
	m.preview.SetContent(preview(n))
	m.preview.GotoTop()
}

func (m *Model) resizeEditors() {
	width := max(m.width-8, 20)
	m.bodyInput.SetWidth(width)
	m.bodyInput.SetHeight(max(m.height-5, 3))
	if m.previewing {
		m.resizePreview()
	}
}

func (m Model) dirty() bool {
	return m.stage == stageNote && m.bodyInput.Value() != m.originalBody
}

func preview(n note.Note) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", sanitizeTerminalLine(n.Title))
	fmt.Fprintf(&b, "\n%s", sanitizeTerminalText(artifactfile.StripPrivateBlocks(n.Body)))
	return b.String()
}

func sanitizeTerminalLine(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, value)
}

func sanitizeTerminalText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return '�'
		}
		return r
	}, value)
}

// Run starts the full-screen editor for one artifact file.
func Run(ctx context.Context, config Config) error {
	model := NewModel(config)
	model.newOperationContext = func() (context.Context, context.CancelFunc) {
		return context.WithCancel(ctx)
	}
	if model.documents != nil {
		model.documents.newOperationContext = model.newOperationContext
	}
	program := tea.NewProgram(&model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))
	_, err := program.Run()
	return err
}

// sanitizeTextareaView replaces control characters in a textarea copy that is
// only rendered, leaving the canonical editor value untouched.
func sanitizeTextareaView(editor *textarea.Model) {
	value := editor.Value()
	if sanitized := sanitizeTerminalText(value); sanitized != value {
		editor.SetValue(sanitized)
	}
}
