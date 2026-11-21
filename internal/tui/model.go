package tui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"os"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/config"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/imageedit"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/notetext"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/recovery"
	"example.com/parchment/internal/spreadsheet"
	tuitheme "example.com/parchment/internal/tui/theme"
)

// Config selects the one artifact file the interactive editor opens.
type Config struct {
	// Path is the artifact file. A missing file is created.
	Path string
	// Kind is the artifact kind. When empty, the editor asks which kind to
	// create.
	Kind artifact.Kind
	// Edit opens an existing document in its editor instead of its reader.
	Edit          bool
	Notes         *note.Service
	Documents     *document.Service
	Spreadsheets  *spreadsheet.Service
	Presentations *presentation.Service
	Images        *imageedit.Service
	Recovery      recovery.Store
	Theme         config.Theme
	UndoLimit     int
	LastPDFPath   string
	PDFStore      PDFExportStore
	NoteEncoding  string
}

// PDFExportStore persists PDF output and the last destination path.
type PDFExportStore interface {
	WritePDFExport(context.Context, string, string, []byte, bool) error
	SavePDFPath(context.Context, string) error
}

// Theme is the shared semantic style set used across TUI components.
type Theme = tuitheme.Theme

func resolveTheme(c config.Theme) Theme { return tuitheme.Resolve(c) }

type stage int

const (
	stageOpening stage = iota
	stageChooseKind
	stageRecovery
	stageNote
	stageDocument
	stageSpreadsheet
	stagePresentation
	stageImage
	stageFailed
)

type fileOpenedMsg struct {
	kind        artifact.Kind
	note        note.Note
	text        *note.TextSnapshot
	document    document.Document
	book        spreadsheet.Spreadsheet
	deck        presentation.Presentation
	history     *historyAvailability
	image       *imageedit.Image
	created     bool
	draft       recovery.Draft
	hasDraft    bool
	noteOpenErr error
	err         error
}

type historyAvailability struct {
	canUndo bool
	canRedo bool
	err     error
}

type noteSavedMsg struct {
	note       note.Note
	text       *note.TextSnapshot
	history    *historyAvailability
	err        error
	cleanupErr error
}

type historyChangedMsg struct {
	description string
	note        note.Note
	text        *note.TextSnapshot
	changed     bool
	history     *historyAvailability
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

// Model is the interactive editor for one artifact file. It owns UI state
// only; artifact rules live in the services.
type Model struct {
	path          string
	kind          artifact.Kind
	edit          bool
	service       *note.Service
	spreadsheets  *spreadsheet.Service
	presentations *presentation.Service
	images        *imageedit.Service
	recoveryStore recovery.Store
	theme         Theme
	undoLimit     int
	noteEncoding  string
	stage         stage
	width         int
	height        int
	pending       bool
	status        string
	errMessage    string

	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc

	// Startup state.
	opened fileOpenedMsg

	// Note editor state.
	noteEditor

	documents *documentsScreen
	sheet     *spreadsheetModel
	deck      *presentationModel
	image     *imageModel
}

// NewModel creates the interactive editor for config.Path.
func NewModel(config Config) Model {
	if config.NoteEncoding == "" {
		config.NoteEncoding = notetext.Auto
	}
	undoLimit := config.UndoLimit
	if undoLimit < 1 {
		undoLimit = 100
	}
	resolvedTheme := resolveTheme(config.Theme)
	m := Model{
		path: config.Path, kind: config.Kind, edit: config.Edit,
		service: config.Notes, spreadsheets: config.Spreadsheets, presentations: config.Presentations,
		images:        config.Images,
		recoveryStore: config.Recovery,
		theme:         resolvedTheme,
		undoLimit:     undoLimit,
		noteEncoding:  config.NoteEncoding,
		noteEditor:    newNoteEditor(undoLimit, resolvedTheme),
		newOperationContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
	}
	m.noteEditor.pdfStore = config.PDFStore
	m.noteEditor.lastPDFPath = config.LastPDFPath
	if config.Documents != nil {
		m.documents = newDocumentsScreen(config.Documents, config.Path, m.newOperationContext, m.theme, undoLimit)
		m.documents.recoveryStore = config.Recovery
		m.documents.pdfStore = config.PDFStore
		m.documents.lastPDFPath = config.LastPDFPath
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
	path, notes, store, encoding := m.path, m.service, m.recoveryStore, m.noteEncoding
	sheets, decks := m.spreadsheets, m.presentations
	images := m.images
	var documents *document.Service
	if m.documents != nil {
		documents = m.documents.service
	}
	return func() tea.Msg {
		msg := fileOpenedMsg{kind: kind}
		if kind == artifact.NoteKind && store != nil {
			msg.draft, msg.hasDraft, msg.err = store.LoadRecovery(ctx, path)
			if msg.err != nil {
				return msg
			}
		}
		switch {
		case kind == artifact.ImageKind && images != nil:
			msg.image, msg.err = images.Load(ctx, path)
		case kind == artifact.NoteKind && notes != nil:
			snapshot, openErr, err := openNoteText(ctx, notes, path, encoding, msg.draft, msg.hasDraft)
			msg.err = err
			msg.noteOpenErr = openErr
			msg.note = snapshot.Note
			if snapshot.Plain {
				msg.text = &snapshot
			}
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
		case kind == artifact.PresentationKind && decks != nil:
			msg.deck, msg.err = decks.Get(ctx, path)
			if errors.Is(msg.err, presentation.ErrNotFound) {
				msg.deck, msg.err = decks.Create(ctx, path, "")
				msg.created = true
			}
		default:
			msg.err = fmt.Errorf("the interactive editor cannot open %s artifacts", kind)
		}
		if msg.err == nil {
			switch kind {
			case artifact.NoteKind:
				msg.history = queryHistoryAvailability(ctx, notes.HistoryAvailability)
			case artifact.DocumentKind:
				msg.history = queryHistoryAvailability(ctx, documents.HistoryAvailability)
			}
		}
		if msg.err == nil && store != nil && kind != artifact.ImageKind && kind != artifact.NoteKind {
			msg.draft, msg.hasDraft, msg.err = store.LoadRecovery(ctx, path)
		}
		return msg
	}
}

// Update applies a terminal message to the editor.
func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	message = normalizeTerminalKey(message)
	// Record the size before delegating: the file may open before the first
	// size arrives, and the shell's view checks the size too.
	size, resized := message.(tea.WindowSizeMsg)
	if resized {
		m.width, m.height = size.Width, size.Height
		m.resizeEditors()
		if m.documents != nil {
			m.documents.resize(size.Width, size.Height)
		}
	}
	switch {
	case m.stage == stageImage && m.image != nil:
		_, cmd := m.image.Update(message)
		return m, cmd
	case m.stage == stageSpreadsheet && m.sheet != nil:
		_, cmd := m.sheet.Update(message)
		return m, cmd
	case m.stage == stagePresentation && m.deck != nil:
		_, cmd := m.deck.Update(message)
		return m, cmd
	}
	if resized {
		return m, nil
	}
	if _, ok := message.(documentMessage); ok && m.documents != nil {
		return m, m.documents.update(message)
	}
	if m.stage == stageDocument && m.documents != nil {
		switch message.(type) {
		case tea.KeyPressMsg, mouseEvent:
			return m, m.documents.update(message)
		}
	}
	switch msg := message.(type) {
	case fileOpenedMsg:
		return m.handleFileOpened(msg)
	case recoveryDiscardedMsg:
		return m.handleRecoveryDiscarded(msg)
	case autosaveTickMsg:
		return m.handleAutosaveTick(msg)
	case autosaveFinishedMsg:
		return m.handleAutosaveFinished(msg)
	case noteSavedMsg:
		return m.handleNoteSaved(msg)
	case notePDFExportedMsg:
		return m, m.notePDFExported(msg)
	case historyChangedMsg:
		return m.handleHistoryChanged(msg)
	case mouseEvent:
		return m.handleMouse(msg)
	case tea.KeyPressMsg:
		return m, m.updateKey(msg)
	case tea.PasteMsg:
		if m.stage == stageNote && !m.pending && m.pdfPrompt == promptPDF {
			var cmd tea.Cmd
			m.pdfInput, cmd = m.pdfInput.Update(msg)
			return m, cmd
		}
		if m.stage == stageNote && !m.pending && !m.previewing && m.pdfPrompt == promptNone {
			if m.find.active {
				return m, m.updateFindKey(msg)
			}
			m.discardWarning = false
			return m, m.updateNoteInput(msg)
		}
	}
	if m.stage == stageDocument && m.documents != nil {
		return m, m.documents.update(message)
	}
	if m.stage == stageNote && m.find.active && m.pdfPrompt == promptNone {
		return m, m.updateFindKey(message)
	}
	return m, nil
}

func (m *Model) applyHistoryAvailability(availability *historyAvailability) {
	if availability == nil {
		return
	}
	if availability.err != nil {
		if !errors.Is(availability.err, context.Canceled) {
			m.status = "History unavailable: " + availability.err.Error()
		}
		return
	}
	m.canUndo, m.canRedo = availability.canUndo, availability.canRedo
}

func queryHistoryAvailability(ctx context.Context, query func(context.Context) (bool, bool, error)) *historyAvailability {
	canUndo, canRedo, err := query(ctx)
	return &historyAvailability{canUndo: canUndo, canRedo: canRedo, err: err}
}

func (m *Model) handleFileOpened(msg fileOpenedMsg) (tea.Model, tea.Cmd) {
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
	if msg.kind == artifact.NoteKind {
		m.applyHistoryAvailability(msg.history)
	} else if msg.kind == artifact.DocumentKind && m.documents != nil {
		m.documents.applyHistoryAvailability(msg.history)
	}
	if msg.hasDraft {
		m.stage = stageRecovery
		return m, nil
	}
	return m, m.enterEditor()
}

func (m *Model) handleRecoveryDiscarded(msg recoveryDiscardedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.errMessage = "Discard autosaved draft: " + msg.err.Error()
		return m, nil
	}
	m.status = "Autosaved draft discarded"
	if m.opened.noteOpenErr != nil {
		return m, m.openFile(artifact.NoteKind)
	}
	return m, m.enterEditor()
}

func (m *Model) handleAutosaveTick(msg autosaveTickMsg) (tea.Model, tea.Cmd) {
	if msg.session == m.autosaveSession && m.stage == stageNote {
		return m, m.saveNoteRecovery(msg.session)
	}
	return m, nil
}

func (m *Model) handleAutosaveFinished(msg autosaveFinishedMsg) (tea.Model, tea.Cmd) {
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
	return m, nil
}

func (m *Model) handleNoteSaved(msg noteSavedMsg) (tea.Model, tea.Cmd) {
	m.finishOperation()
	m.pending = false
	m.applyHistoryAvailability(msg.history)
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) {
			m.errMessage, m.status = "", "Save cancelled; unsaved changes remain"
		} else {
			m.errMessage = msg.err.Error()
		}
		m.startAutosaveSession()
		return m, m.scheduleAutosave(m.autosaveSession)
	}
	m.bodyInput.ResetHistory()
	if msg.text != nil {
		m.markTextSaved(*msg.text)
	} else {
		m.markSaved(msg.note)
	}
	m.errMessage, m.status = "", "Saved “"+msg.note.Title+"”"
	if msg.cleanupErr != nil {
		m.status += " (could not remove recovery draft: " + msg.cleanupErr.Error() + ")"
	}
	m.applyHistoryAvailability(msg.history)
	m.startAutosaveSession()
	return m, m.scheduleAutosave(m.autosaveSession)
}

func (m *Model) handleHistoryChanged(msg historyChangedMsg) (tea.Model, tea.Cmd) {
	m.finishOperation()
	m.pending = false
	m.applyHistoryAvailability(msg.history)
	if msg.err != nil {
		m.errMessage = msg.err.Error()
		if msg.changed {
			m.stage = stageFailed
		}
		return m, nil
	}
	if msg.text != nil {
		if !m.startTextEdit(*msg.text) {
			m.stage = stageFailed
			return m, nil
		}
	} else {
		m.loadNote(msg.note)
	}
	m.errMessage, m.status = "", msg.description
	m.applyHistoryAvailability(msg.history)
	if msg.text != nil {
		return m, m.scheduleAutosave(m.autosaveSession)
	}
	return m, nil
}

func (m *Model) handleMouse(msg mouseEvent) (tea.Model, tea.Cmd) {
	if m.stage == stageNote && !m.pending && !m.previewing && m.pdfPrompt == promptNone {
		mouseMsg := msg
		mouseMsg.Y = msg.Y - 3
		mouseMsg.X = msg.X - 2
		var cmd tea.Cmd
		m.bodyInput, cmd = m.bodyInput.Update(toTeaMouseMessage(mouseMsg))
		return m, cmd
	}
	return m, nil
}

// enterEditor opens the loaded artifact in its editor.
func (m *Model) enterEditor() tea.Cmd {
	switch m.opened.kind {
	case artifact.ImageKind:
		m.image = newImageModel(m.images, m.opened.image, m.theme, m.undoLimit)
		m.opened.image = nil
		m.image.newOperationContext = m.newOperationContext
		m.stage = stageImage
		_, cmd := m.image.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		return cmd
	case artifact.NoteKind:
		ok := false
		if m.opened.text != nil {
			ok = m.startTextEdit(*m.opened.text)
		} else {
			ok = m.startEdit(m.opened.note)
		}
		if !ok {
			m.stage = stageFailed
			return nil
		}
		m.stage = stageNote
		return tea.Batch(m.bodyInput.Focus(), m.scheduleAutosave(m.autosaveSession))
	case artifact.SpreadsheetKind:
		m.sheet = newSpreadsheetModel(m.spreadsheets, m.path, m.theme, m.undoLimit)
		m.sheet.recoveryStore = m.recoveryStore
		m.sheet.pdfStore = m.noteEditor.pdfStore
		m.sheet.lastPDFPath = m.noteEditor.lastPDFPath
		m.sheet.autosaveScheduler = func(session uint64) tea.Cmd {
			return tea.Tick(autosaveInterval, func(time.Time) tea.Msg {
				return spreadsheetAutosaveTickMsg{session: session}
			})
		}
		m.sheet.newOperationContext = m.newOperationContext
		m.sheet.width, m.sheet.height = m.width, m.height
		m.sheet.setBook(m.opened.book)
		m.sheet.status = m.status
		m.stage = stageSpreadsheet
		_, cmd := m.sheet.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		return cmd
	case artifact.PresentationKind:
		m.deck = newPresentationModel(m.presentations, m.path, m.theme, m.undoLimit)
		m.deck.recoveryStore = m.recoveryStore
		m.deck.pdfStore = m.noteEditor.pdfStore
		m.deck.lastPDFPath = m.noteEditor.lastPDFPath
		m.deck.autosaveScheduler = func(session uint64) tea.Cmd {
			return tea.Tick(autosaveInterval, func(time.Time) tea.Msg {
				return presentationAutosaveTickMsg{session: session}
			})
		}
		m.deck.newOperationContext = m.newOperationContext
		m.deck.width, m.deck.height = m.width, m.height
		m.deck.setItem(m.opened.deck)
		m.deck.status = m.status
		m.stage = stagePresentation
		_, cmd := m.deck.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		return cmd
	case artifact.DocumentKind:
		// Existing documents open in the reader unless editing was requested;
		// new ones start in the editor.
		m.stage = stageDocument
		if !m.opened.created && !m.edit {
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

func (m *Model) updateChooseKindKey(key string) tea.Cmd {
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
	return nil
}

func (m *Model) updateRecoveryKey(key string) tea.Cmd {
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
	return nil
}

func (m *Model) updatePendingKey(key string) tea.Cmd {
	if key == "ctrl+c" || key == "q" && m.stage != stageNote {
		if m.cancelOperation != nil {
			m.cancelOperation()
		}
		if m.stage == stageNote {
			m.status = "Cancelling…"
			return nil
		}
		return tea.Quit
	}
	m.status = "Please wait for the current operation to finish"
	return nil
}

func (m *Model) updateKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.pending {
		return m.updatePendingKey(key)
	}
	switch m.stage {
	case stageOpening, stageFailed:
		if key == "q" || key == "ctrl+c" || key == "esc" {
			return tea.Quit
		}
	case stageChooseKind:
		return m.updateChooseKindKey(key)
	case stageRecovery:
		return m.updateRecoveryKey(key)
	case stageNote:
		return m.updateNoteKey(msg)
	}
	return nil
}

func (m *Model) updateNoteKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.tooSmall() {
		if key == "q" && !m.dirty() {
			return m.close()
		}
		return nil
	}
	if m.pdfPrompt != promptNone {
		return m.updateNotePDFPrompt(msg)
	}
	if m.find.active {
		return m.updateFindKey(msg)
	}
	if key != "esc" && key != "ctrl+c" {
		m.discardWarning = false
	}
	if m.previewing {
		if m.find.visible {
			switch key {
			case "n":
				m.movePreviewMatch(1)
				return nil
			case "N":
				m.movePreviewMatch(-1)
				return nil
			case "esc":
				m.find.close()
				m.searchPreview("")
				return nil
			}
		}
		if key == "/" || key == "ctrl+f" {
			return m.openFind(m.width)
		}
		switch key {
		case "ctrl+p":
			return m.openNotePDFPrompt()
		case "esc", "f5", "q":
			m.previewing = false
		case "ctrl+c":
			return m.requestClose()
		default:
			m.preview, _ = m.preview.Update(msg)
		}
		return nil
	}
	switch key {
	case "ctrl+f":
		return m.openFind(m.width)
	case "ctrl+p":
		return m.openNotePDFPrompt()
	case "ctrl+c":
		if m.bodyInput.Selection.Active {
			var cmd tea.Cmd
			m.bodyInput, cmd = m.bodyInput.Update(msg)
			return cmd
		}
		return m.requestClose()
	case "esc":
		return m.requestClose()
	case "ctrl+s":
		m.stopAutosave()
		m.pending = true
		return m.saveNote()
	case "f5":
		m.previewing = true
		m.resizePreview()
		m.searchPreview(m.find.query())
		return nil
	case "ctrl+z", "ctrl+r", "ctrl+y", "ctrl+shift+z":
		if key == "ctrl+z" && m.bodyInput.CanUndo() {
			m.bodyInput.Undo()
			return nil
		}
		if (key == "ctrl+r" || key == "ctrl+y" || key == "ctrl+shift+z") && m.bodyInput.CanRedo() {
			m.bodyInput.Redo()
			return nil
		}
		return m.history(key == "ctrl+z")
	}
	return m.updateNoteInput(msg)
}

func (m *Model) updateNoteInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.bodyInput, cmd = m.bodyInput.Update(msg)
	if err := m.bodyInput.TakeInputError(); err != nil {
		m.errMessage = err.Error()
	}
	return cmd
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
	service, path, requested := m.service, m.path, m.noteEncoding
	fallback := ""
	if m.textSnapshot != nil {
		fallback = m.textSnapshot.Format.Encoding
	}
	return func() tea.Msg {
		run, prefix := service.Redo, "Redid "
		if undo {
			run, prefix = service.Undo, "Undid "
		}
		description, err := run(ctx)
		if err != nil {
			return historyChangedMsg{history: queryHistoryAvailability(ctx, service.HistoryAvailability), err: err}
		}
		encodings := []string{requested}
		if fallback != "" && fallback != notetext.Auto && fallback != requested {
			encodings = append(encodings, fallback)
		}
		var snapshot note.TextSnapshot
		for _, encoding := range encodings {
			snapshot, err = service.OpenText(ctx, path, encoding)
			if err == nil {
				break
			}
		}
		msg := historyChangedMsg{
			description: prefix + strings.ToLower(description), note: snapshot.Note, changed: true,
			history: queryHistoryAvailability(ctx, service.HistoryAvailability), err: err,
		}
		if snapshot.Plain {
			msg.text = &snapshot
		}
		return msg
	}
}

// View renders the current model without performing application operations.
func (m Model) View() tea.View {
	view := tea.NewView(m.viewContent())
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

type terminalModel struct {
	tea.Model
}

func (m terminalModel) Init() tea.Cmd { return m.Model.Init() }

func (m terminalModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.Model.Update(message)
	return terminalModel{Model: model}, cmd
}

func (m terminalModel) View() tea.View {
	view := m.Model.View()
	view.Content = protectITermGraphics(view.Content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m Model) viewContent() string {
	if m.stage == stageImage && m.image != nil {
		return m.image.View().Content
	}
	if m.tooSmall() {
		cleanup := ""
		if m.stage == stageDocument && m.documents != nil {
			cleanup = m.documents.clearDocumentImages()
		}
		return cleanup + "parchment — terminal too small (minimum 40×10)\nResize the terminal, or press q to quit."
	}
	header := m.theme.Chrome.
		Render("parchment  ·  " + sanitizeTerminalLine(m.path))
	switch m.stage {
	case stageOpening:
		return header + "\n\nOpening…\n" + m.statusLine()
	case stageFailed:
		return header + "\n\nThis file cannot be opened in the editor.\n\nq quits\n" + m.statusLine()
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
			options + "\n\nq quits without creating the file\n" + m.statusLine()
	case stageRecovery:
		draft := m.opened.draft
		return header + "\n\nAn autosaved draft of unsaved edits to this file exists.\n\n" +
			"Autosaved: " + draft.UpdatedAt.Local().Format("2006-01-02 15:04") + "\n\n" +
			"r  Recover the draft into the editor\n" +
			"d  Discard the draft and open the saved file\n" +
			"q  Quit and keep the draft\n" + m.statusLine()
	case stageDocument:
		return m.documents.view(header)
	case stageSpreadsheet:
		return m.sheet.View().Content
	case stagePresentation:
		return m.deck.View().Content
	}
	state := "Editing note"
	if m.dirty() {
		state += " • unsaved"
	}
	if m.previewing {
		shortcuts := ansi.Truncate("Preview  ·  Ctrl+P PDF  ·  ↑/↓ scroll  ·  Esc returns to the editor", m.width, "…")
		content := m.preview.View()
		if m.pdfPrompt != promptNone {
			content = m.pdfInput.View() + "\n" + content
		}
		status := m.statusLine()
		if m.find.visible {
			status = m.find.view(len(m.previewMatches), m.previewMatch, m.width)
		}
		return chromeLayout(m.width, m.height, header, shortcuts, content, status, m.theme.BorderColor)
	}
	shortcuts := ansi.Truncate(state+"  ·  Ctrl+F find  ·  Ctrl+S saves  ·  Ctrl+P PDF  ·  F5 preview  ·  Ctrl+Z/Ctrl+R undo/redo  ·  Esc quits", m.width, "…")
	content := m.bodyInput.View()
	if m.pdfPrompt != promptNone {
		content = m.pdfInput.View() + "\n" + content
	}
	status := m.statusLine()
	if m.find.active {
		status = m.findStatus(m.width)
	}
	return chromeLayout(m.width, m.height, header, shortcuts, content, status, m.theme.BorderColor)
}

func (m Model) tooSmall() bool { return m.width < 40 || m.height < 10 }

func chromeLayout(width, height int, header, shortcuts, content, status string, border color.Color) string {
	chromeRows := lipgloss.Height(header) + lipgloss.Height(status)
	if shortcuts != "" {
		chromeRows += lipgloss.Height(shortcuts)
	}

	paneHeight := height - chromeRows - 2
	if paneHeight < 1 {
		paneHeight = 1
	}
	pane := lipgloss.NewStyle().
		Width(width).
		Height(paneHeight+2).
		Border(lipgloss.NormalBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Render(content)

	if shortcuts == "" {
		return header + "\n" + pane + "\n" + status
	}
	return header + "\n" + shortcuts + "\n" + pane + "\n" + status
}

func (m Model) statusLine() string {
	undo, redo := "undo unavailable", "redo unavailable"
	if m.canUndo || m.bodyInput.CanUndo() {
		undo = "undo available"
	}
	if m.canRedo || m.bodyInput.CanRedo() {
		redo = "redo available"
	}
	message := m.status
	if m.errMessage != "" {
		message = "Error: " + m.errMessage
	}
	if message == "" {
		message = "Ready"
	}
	return sanitizeTerminalLine(message) + "  ·  " + undo + "  ·  " + redo
}

// startEdit loads n into the editor, refusing notes the editor widgets would
// truncate or alter.
func (m *Model) startEdit(n note.Note) bool {
	if isUnpreservableText(n.Body) {
		m.errMessage = "This note exceeds editor limits or contains text the editor cannot preserve; edit it with a text editor"
		m.status = ""
		return false
	}
	m.bodyInput.SetValue(n.Body)
	m.loadNote(n)
	m.resizeEditors()
	m.errMessage = ""
	m.startAutosaveSession()
	return true
}

func (m *Model) startTextEdit(snapshot note.TextSnapshot) bool {
	if err := m.bodyInput.SetDocument(snapshot.Document); err != nil {
		m.errMessage = err.Error()
		return false
	}
	m.markTextSaved(snapshot)
	m.resizeEditors()
	m.errMessage = ""
	m.startAutosaveSession()
	return true
}

func isUnpreservableText(text string) bool {
	if len(text) > 1_000_000 {
		return true
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 10_000 {
		return true
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return true
		}
	}
	return false
}

func (m *Model) saveNote() tea.Cmd {
	body, expected := m.bodyInput.Value(), m.snapshot
	var text *note.TextSnapshot
	doc := m.bodyInput.Document()
	if m.textSnapshot != nil {
		snapshot := m.textSnapshot.Clone()
		text = &snapshot
	}
	store, path, service := m.recoveryStore, m.path, m.service
	ctx := m.startOperation()
	return func() tea.Msg {
		var n note.Note
		var savedText *note.TextSnapshot
		var err error
		if text != nil {
			saved, saveErr := service.SaveTextExpected(ctx, *text, doc)
			n, err = saved.Note, saveErr
			if err == nil {
				savedText = &saved
			}
		} else {
			n, err = service.UpdateExpected(ctx, expected, body)
		}
		var cleanupErr error
		if err == nil && store != nil {
			cleanupErr = store.DeleteRecovery(context.Background(), path)
		}
		return noteSavedMsg{
			note: n, text: savedText, history: queryHistoryAvailability(ctx, service.HistoryAvailability),
			err: err, cleanupErr: cleanupErr,
		}
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
	m.noteEditor.resizePreview(m.width, m.height)
}

func (m *Model) resizeEditors() {
	m.noteEditor.resizeEditors(m.width, m.height)
}

func (m Model) dirty() bool {
	if m.stage == stageNote && m.textSnapshot != nil {
		return !m.bodyInput.Document().Equal(m.textSnapshot.Document)
	}
	return m.stage == stageNote && m.bodyInput.Value() != m.originalBody
}

func sanitizeTerminalLine(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '\uFFFD'
		}
		return r
	}, value)
}

func sanitizeTerminalText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return '\uFFFD'
		}
		return r
	}, value)
}

// Run starts the full-screen editor for one artifact file.
func Run(ctx context.Context, config Config, opts ...tea.ProgramOption) error {
	model := NewModel(config)
	model.newOperationContext = func() (context.Context, context.CancelFunc) {
		return context.WithCancel(ctx)
	}
	if model.documents != nil {
		model.documents.newOperationContext = model.newOperationContext
	}
	options := append([]tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithOutput(keyboardTerminalOutput{os.Stdout}),
	}, opts...)
	program := tea.NewProgram(terminalModel{Model: &model}, options...)
	_, err := program.Run()
	return err
}
