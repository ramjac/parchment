package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/recovery"
	"example.com/parchment/internal/search"
)

type mode int

const (
	browsing mode = iota
	editing
	searching
)

type notesLoadedMsg struct {
	notes []note.Note
	err   error
}

type searchCompletedMsg struct {
	notes []note.Note
	err   error
}

type noteSavedMsg struct {
	note       note.Note
	err        error
	cleanupErr error
}

type noteDeletedMsg struct{ err error }
type historyChangedMsg struct {
	description string
	err         error
}

type recoveriesLoadedMsg struct {
	drafts []recovery.Draft
	err    error
}
type recoveryDeletedMsg struct {
	id         string
	err        error
	showStatus bool
}
type recoveryOpenedMsg struct {
	draft recovery.Draft
	data  noteRecoveryData
	err   error
}
type autosaveTickMsg struct{ session uint64 }
type autosaveFinishedMsg struct {
	session uint64
	err     error
}

type noteRecoveryData struct {
	Snapshot       note.Note                  `json:"snapshot"`
	SnapshotBody   string                     `json:"snapshot_body"`
	SnapshotBlocks map[string]json.RawMessage `json:"snapshot_blocks,omitempty"`
	Title          string                     `json:"title"`
	Body           string                     `json:"body"`
}

const autosaveInterval = 2 * time.Second

type theme struct {
	primary lipgloss.AdaptiveColor
	border  lipgloss.AdaptiveColor
}

// Model coordinates the interactive notes screen and owns only UI state.
type Model struct {
	service             *note.Service
	repository          note.Repository
	workspaceName       string
	workspacePath       string
	notes               []note.Note
	selected            int
	width               int
	height              int
	mode                mode
	showPreview         bool
	confirmDelete       bool
	editingID           string
	editingSnapshot     note.Note
	creating            bool
	originalTitle       string
	originalBody        string
	titleInput          textinput.Model
	bodyInput           textarea.Model
	searchInput         textinput.Model
	preview             viewport.Model
	searchQuery         string
	searchActive        bool
	theme               theme
	pending             bool
	canUndo             bool
	canRedo             bool
	status              string
	errMessage          string
	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc
	documents           *documentsScreen
	documentsActive     bool
	recoveryStore       recovery.Store
	recoveries          []recovery.Draft
	recoverySelected    int
	recoveryLoading     bool
	recoveryDismissed   bool
	autosaveSession     uint64
	autosaveID          string
	autosaveCancel      context.CancelFunc
	autosaveScheduler   func(uint64) tea.Cmd
}

// Option customizes the interactive model.
type Option func(*Model)

// WithDocuments adds the documents screen, reached from notes with Tab.
func WithDocuments(service *document.Service) Option {
	return func(m *Model) {
		m.documents = newDocumentsScreen(service, m.theme, m.newOperationContext)
		m.documents.recoveryStore = m.recoveryStore
		m.documents.autosaveScheduler = func(session uint64) tea.Cmd {
			return tea.Tick(autosaveInterval, func(time.Time) tea.Msg {
				return documentAutosaveTickMsg{session: session}
			})
		}
	}
}

// NewModel creates the interactive notes model for a workspace.
func NewModel(service *note.Service, repository note.Repository, workspaceName, workspacePath string, options ...Option) Model {
	title := textinput.New()
	title.Prompt = "Title: "
	title.CharLimit = 200
	body := textarea.New()
	body.Prompt = ""
	body.ShowLineNumbers = false
	body.Placeholder = "Write Markdown…"
	body.CharLimit = 1_000_000
	searchField := textinput.New()
	searchField.Prompt = "/ "
	searchField.Placeholder = "Search notes"
	searchField.CharLimit = 200
	preview := viewport.New(0, 0)
	m := Model{
		service: service, repository: repository, workspaceName: workspaceName, workspacePath: workspacePath,
		titleInput: title, bodyInput: body, searchInput: searchField, preview: preview,
		pending: true, canUndo: service.CanUndo(), canRedo: service.CanRedo(),
		newOperationContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
		theme: theme{
			primary: lipgloss.AdaptiveColor{Light: "#4b3f72", Dark: "#c4b5fd"},
			border:  lipgloss.AdaptiveColor{Light: "#b8b4c7", Dark: "#55516a"},
		},
	}
	if store, ok := repository.(recovery.Store); ok {
		m.recoveryStore = store
	}
	m.autosaveScheduler = func(session uint64) tea.Cmd {
		return tea.Tick(autosaveInterval, func(time.Time) tea.Msg {
			return autosaveTickMsg{session: session}
		})
	}
	for _, option := range options {
		option(&m)
	}
	return m
}

// Init loads the initial note list.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadNotes()}
	if m.recoveryStore != nil {
		m.recoveryLoading = true
		cmds = append(cmds, m.loadRecoveries())
	}
	return tea.Batch(cmds...)
}

// Update applies a terminal message to the notes screen.
func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if recovered, ok := message.(documentRecoveryReadyMsg); ok {
		m.recoveryLoading = false
		if recovered.err != nil {
			m.errMessage = recovered.err.Error()
			return m, nil
		}
		if m.documents == nil {
			m.errMessage = "Document editor is unavailable for this recovery draft"
			return m, nil
		}
		m.removeRecovery(recovered.draft.ID)
		m.recoveryDismissed = true
		m.documentsActive = true
		cmd := m.documents.restoreRecovery(recovered.draft, recovered.data)
		return m, cmd
	}
	if m.documents != nil {
		if handled, cmd := m.updateDocuments(message); handled {
			return m, cmd
		}
	}
	switch msg := message.(type) {
	case recoveriesLoadedMsg:
		m.recoveryLoading = false
		if msg.err != nil {
			m.status = "Autosaved draft check failed: " + msg.err.Error()
		} else {
			m.recoveries = msg.drafts
			m.recoverySelected = 0
			m.recoveryDismissed = false
		}
	case recoveryOpenedMsg:
		m.recoveryLoading = false
		if msg.err != nil {
			m.errMessage = msg.err.Error()
			return m, nil
		}
		m.removeRecovery(msg.draft.ID)
		m.recoveryDismissed = true
		m.mode, m.creating = editing, msg.draft.Created
		m.editingID, m.autosaveID = msg.draft.Artifact, msg.draft.ID
		m.editingSnapshot = msg.data.Snapshot
		m.editingSnapshot.Body = msg.data.SnapshotBody
		m.editingSnapshot.Blocks = cloneRawMessages(msg.data.SnapshotBlocks)
		m.titleInput.SetValue(msg.data.Title)
		m.titleInput.CursorEnd()
		m.bodyInput.SetValue(msg.data.Body)
		m.originalTitle, m.originalBody = msg.data.Snapshot.Title, msg.data.Snapshot.Body
		m.titleInput.Focus()
		m.bodyInput.Blur()
		m.errMessage, m.status = "", "Recovered unsaved note draft"
		m.startAutosaveSession()
		return m, m.scheduleAutosave(m.autosaveSession)
	case recoveryDeletedMsg:
		if msg.err != nil {
			m.errMessage = msg.err.Error()
		} else {
			m.removeRecovery(msg.id)
			m.errMessage = ""
			if msg.showStatus {
				m.status = "Autosaved draft discarded"
			}
		}
	case autosaveTickMsg:
		if msg.session == m.autosaveSession && m.mode == editing && !m.documentsActive {
			return m, m.saveNoteRecovery(msg.session)
		}
	case autosaveFinishedMsg:
		if msg.session == m.autosaveSession {
			if m.autosaveCancel != nil {
				m.autosaveCancel()
				m.autosaveCancel = nil
			}
			if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
				m.errMessage = "Autosave failed: " + msg.err.Error()
			}
			if m.mode == editing && !m.documentsActive {
				return m, m.scheduleAutosave(msg.session)
			}
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeEditors()
		m.resizePreview()
	case notesLoadedMsg:
		m.finishOperation()
		m.pending = false
		if msg.err != nil {
			m.errMessage = msg.err.Error()
		} else {
			m.errMessage = ""
			m.notes = msg.notes
			m.clampSelection()
			m.resizePreview()
		}
		m.refreshHistoryAvailability()
	case searchCompletedMsg:
		m.finishOperation()
		m.pending = false
		if msg.err != nil {
			m.errMessage = msg.err.Error()
		} else {
			m.errMessage = ""
			m.notes = msg.notes
			m.selected = 0
			m.clampSelection()
			m.resizePreview()
			m.status = fmt.Sprintf("Search: %s  ·  Esc clears (%d results)", m.searchQuery, len(m.notes))
		}
		m.refreshHistoryAvailability()
	case noteSavedMsg:
		m.finishOperation()
		if msg.err != nil {
			m.pending = false
			if errors.Is(msg.err, context.Canceled) {
				m.errMessage = ""
				m.status = "Save cancelled; unsaved changes remain"
			} else {
				m.errMessage = msg.err.Error()
			}
			m.refreshHistoryAvailability()
			m.startAutosaveSession()
			return m, m.scheduleAutosave(m.autosaveSession)
		}
		m.refreshHistoryAvailability()
		m.mode = browsing
		m.creating = false
		m.recoveryDismissed = false
		m.errMessage = ""
		m.status = "Saved “" + msg.note.Title + "”"
		if msg.cleanupErr != nil {
			m.status += " (could not remove recovery draft: " + msg.cleanupErr.Error() + ")"
		}
		return m, m.loadNotes()
	case noteDeletedMsg:
		m.finishOperation()
		if msg.err != nil {
			m.pending = false
			m.confirmDelete = false
			m.errMessage = msg.err.Error()
			m.refreshHistoryAvailability()
			return m, nil
		}
		m.refreshHistoryAvailability()
		m.confirmDelete = false
		m.errMessage = ""
		m.status = "Note deleted"
		return m, m.loadNotes()
	case historyChangedMsg:
		m.finishOperation()
		if msg.err != nil {
			m.pending = false
			m.errMessage = msg.err.Error()
			m.refreshHistoryAvailability()
			return m, nil
		}
		m.refreshHistoryAvailability()
		m.errMessage = ""
		m.status = msg.description
		return m, m.loadNotes()
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if (m.width < 40 || m.height < 10) && key == "q" {
		if m.cancelOperation != nil {
			m.cancelOperation()
		}
		return m, tea.Quit
	}
	if m.pending {
		if key == "ctrl+c" || key == "q" {
			if m.cancelOperation != nil {
				m.cancelOperation()
			}
			if m.mode == editing {
				m.status = "Cancelling save…"
				return m, nil
			}
			return m, tea.Quit
		}
		m.status = "Please wait for the current operation to finish"
		return m, nil
	}
	if m.recoveryLoading {
		m.status = "Checking for autosaved drafts…"
		return m, nil
	}
	if len(m.recoveries) > 0 && !m.recoveryDismissed && m.mode != editing {
		switch key {
		case "up", "k":
			m.recoverySelected = max(0, m.recoverySelected-1)
		case "down", "j":
			m.recoverySelected = min(len(m.recoveries)-1, m.recoverySelected+1)
		case "r":
			return m, m.openRecovery()
		case "d":
			return m, m.deleteRecovery()
		case "esc":
			m.recoveryDismissed = true
			m.status = "Autosaved drafts postponed; press F6 to review"
		}
		return m, nil
	}
	if key == "f6" && len(m.recoveries) > 0 {
		m.recoveryDismissed = false
		return m, nil
	}
	if key == "ctrl+c" {
		if m.mode == editing {
			if m.dirty() {
				m.status = "Unsaved changes: Ctrl+S saves, Esc discards"
				return m, nil
			}
			m.mode = browsing
			m.stopAutosave()
			m.creating = false
			m.recoveryDismissed = false
			return m, m.deleteCurrentRecovery()
		}
		return m, tea.Quit
	}
	if m.confirmDelete {
		switch key {
		case "y":
			if n, ok := m.selectedNote(); ok {
				m.pending = true
				return m, m.deleteNote(n.ID)
			}
			m.confirmDelete = false
		case "n", "esc":
			m.confirmDelete = false
			m.status = "Deletion cancelled"
		}
		return m, nil
	}
	if m.status == "help" && m.mode == browsing {
		if key == "?" || key == "esc" {
			m.status = ""
		}
		return m, nil
	}
	if m.mode == editing {
		switch key {
		case "esc":
			m.stopAutosave()
			m.mode = browsing
			m.creating = false
			m.recoveryDismissed = false
			m.status = "Edit cancelled"
			return m, m.deleteCurrentRecovery()
		case "ctrl+s":
			m.stopAutosave()
			m.pending = true
			return m, m.saveNote()
		case "tab":
			if m.titleInput.Focused() {
				m.titleInput.Blur()
				return m, m.bodyInput.Focus()
			}
			m.bodyInput.Blur()
			return m, m.titleInput.Focus()
		}
		var command tea.Cmd
		if m.titleInput.Focused() {
			m.titleInput, command = m.titleInput.Update(msg)
		} else {
			m.bodyInput, command = m.bodyInput.Update(msg)
		}
		return m, command
	}
	if key == "tab" && m.mode == browsing && m.documents != nil {
		m.documentsActive = true
		m.documents.resize(m.width, m.height)
		return m, m.documents.init()
	}
	if m.mode == searching {
		switch key {
		case "esc":
			m.searchInput.Blur()
			m.searchInput.Reset()
			m.mode = browsing
			m.searchActive = false
			m.searchQuery = ""
			return m, m.loadNotes()
		case "enter":
			m.searchInput.Blur()
			m.mode = browsing
			m.searchActive = true
			m.searchQuery = m.searchInput.Value()
			m.pending = true
			m.status = "Searching: " + m.searchQuery
			return m, m.searchNotes(m.searchQuery)
		}
		m.searchInput, _ = m.searchInput.Update(msg)
		return m, nil
	}
	if m.showPreview {
		switch key {
		case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
			m.preview, _ = m.preview.Update(msg)
			return m, nil
		}
	}
	if key == "q" {
		return m, tea.Quit
	}
	switch key {
	case "?":
		m.status = "help"
	case "esc":
		if m.searchActive {
			m.searchActive = false
			m.searchQuery = ""
			m.status = ""
			m.pending = true
			return m, m.loadNotes()
		} else if m.showPreview {
			m.showPreview = false
		}
	case "/":
		m.mode = searching
		m.searchInput.SetValue("")
		return m, m.searchInput.Focus()
	case "n":
		if m.startCreate() {
			return m, tea.Batch(m.titleInput.Focus(), m.scheduleAutosave(m.autosaveSession))
		}
		return m, nil
	case "e":
		if n, ok := m.selectedNote(); ok {
			if !m.startEdit(n) {
				return m, nil
			}
			return m, tea.Batch(m.titleInput.Focus(), m.scheduleAutosave(m.autosaveSession))
		}
	case "d":
		if _, ok := m.selectedNote(); ok {
			m.confirmDelete = true
		}
	case "u", "ctrl+z":
		m.pending = true
		ctx := m.startOperation()
		return m, func() tea.Msg {
			description, err := m.service.Undo(ctx)
			return historyChangedMsg{description: "Undid " + strings.ToLower(description), err: err}
		}
	case "ctrl+r":
		m.pending = true
		ctx := m.startOperation()
		return m, func() tea.Msg {
			description, err := m.service.Redo(ctx)
			return historyChangedMsg{description: "Redid " + strings.ToLower(description), err: err}
		}
	case "up", "k":
		if !m.showPreview && m.selected > 0 {
			m.selected--
			m.resizePreview()
		}
	case "down", "j":
		if !m.showPreview && m.selected+1 < len(m.notes) {
			m.selected++
			m.resizePreview()
		}
	case "enter":
		m.showPreview = true
		m.resizePreview()
	case "left":
		m.showPreview = false
	case "right":
		m.showPreview = true
		m.resizePreview()
	}
	return m, nil
}

// View renders the current model without performing application operations.
func (m Model) View() string {
	if m.width < 40 || m.height < 10 {
		return "parchment — terminal too small (minimum 40×10)\nResize the terminal, or press q to quit."
	}
	header := lipgloss.NewStyle().Bold(true).Foreground(m.theme.primary).
		Render("parchment  ·  " + sanitizeTerminalLine(m.workspaceName) + "  ·  " + sanitizeTerminalLine(m.workspacePath))
	if m.documentsActive {
		if !m.recoveryLoading && (len(m.recoveries) == 0 || m.recoveryDismissed) {
			return m.documents.view(header)
		}
	}
	if m.recoveryLoading || len(m.recoveries) > 0 && !m.recoveryDismissed {
		return m.recoveryView(header)
	}
	if m.documentsActive {
		return m.documents.view(header)
	}
	if m.mode == editing {
		state := "Editing"
		if m.dirty() {
			state += " • unsaved"
		}
		titleInput := m.titleInput
		titleInput.SetValue(sanitizeTerminalLine(titleInput.Value()))
		bodyInput := m.bodyInput
		bodyInput.SetValue(sanitizeTerminalText(bodyInput.Value()))
		content := header + "\n" + state + "  ·  Tab switches fields  ·  Ctrl+S saves  ·  Esc cancels\n\n" +
			titleInput.View() + "\n\n" + bodyInput.View()
		return content + m.statusLine()
	}
	if m.mode == searching {
		searchInput := m.searchInput
		searchInput.SetValue(sanitizeTerminalLine(searchInput.Value()))
		header += "\n" + searchInput.View() + "  (Esc clears search)"
	}
	if m.status == "help" {
		return header + "\n\n" +
			"Notes\n\n" +
			"↑/↓ or j/k  Select note\n" +
			"Enter        Open/focus preview\n" +
			"↑/↓          Scroll preview when focused\n" +
			"n            New note\n" +
			"e            Edit note\n" +
			"d            Delete note (confirmation required)\n" +
			"/            Search titles, content, and tags\n" +
			"u / Ctrl+Z   Undo    Ctrl+R  Redo\n" +
			"?            Close help    q  Quit" + m.statusLine()
	}
	if m.width < 80 {
		return m.viewNarrow(header)
	}
	return m.viewWide(header)
}

func (m Model) viewNarrow(header string) string {
	if m.showPreview {
		if _, ok := m.selectedNote(); ok {
			return header + "\n" + m.preview.View() + "\n\n↑/↓ scroll  ·  Esc returns to notes" + m.statusLine()
		}
	}
	var b strings.Builder
	visible := m.height - 8
	if visible < 1 {
		visible = 1
	}
	start := visibleWindowStart(len(m.notes), m.selected, visible)
	end := min(start+visible, len(m.notes))
	if len(m.notes) == 0 {
		b.WriteString(header + "\n\nNotes\n")
	} else {
		b.WriteString(fmt.Sprintf("%s\n\nNotes (%d-%d of %d)\n", header, start+1, end, len(m.notes)))
	}
	if len(m.notes) == 0 {
		b.WriteString("  No notes yet. Press n to create one.\n")
	}
	for i, n := range m.notes[start:end] {
		i += start
		marker := "  "
		if i == m.selected {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s%s\n", marker, sanitizeTerminalLine(n.Title))
	}
	b.WriteString("\nEnter preview  ·  n new  ·  / search  ·  ? help")
	return b.String() + m.statusLine()
}

func (m Model) viewWide(header string) string {
	listWidth := m.width / 3
	if listWidth < 24 {
		listWidth = 24
	}
	previewWidth := m.width - listWidth - 4
	if previewWidth < 30 {
		previewWidth = 30
	}
	visible := (m.height - 9) / 2
	if visible < 1 {
		visible = 1
	}
	start := visibleWindowStart(len(m.notes), m.selected, visible)
	end := min(start+visible, len(m.notes))
	var list strings.Builder
	list.WriteString("Notes\n")
	if len(m.notes) == 0 {
		list.WriteString("\nNo notes yet.\nPress n to create one.")
	}
	for i, n := range m.notes[start:end] {
		i += start
		marker := "  "
		if i == m.selected {
			marker = "› "
		}
		fmt.Fprintf(&list, "%s%s\n", marker, sanitizeTerminalLine(n.Title))
		if i == m.selected && len(n.Tags) > 0 {
			tags := make([]string, len(n.Tags))
			for j, tag := range n.Tags {
				tags[j] = sanitizeTerminalLine(tag)
			}
			fmt.Fprintf(&list, "  #%s\n", strings.Join(tags, " #"))
		}
	}
	listPane := lipgloss.NewStyle().Width(listWidth).Height(m.height-5).Border(lipgloss.NormalBorder()).
		BorderForeground(m.theme.border).Padding(0, 1).Render(list.String())
	content := "Select a note to preview its Markdown."
	if _, ok := m.selectedNote(); ok {
		content = m.preview.View()
	}
	previewPane := lipgloss.NewStyle().Width(previewWidth).Height(m.height-5).Border(lipgloss.NormalBorder()).
		BorderForeground(m.theme.border).Padding(0, 1).Render(content)
	footer := "↑/↓ select  Enter focus preview  n new  e edit  d delete  / search  ? help  q quit"
	if m.showPreview {
		footer = "↑/↓ scroll preview  Esc return to list  n new  e edit  d delete  / search  ? help  q quit"
	}
	return header + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, listPane, previewPane) + "\n" + footer + m.statusLine()
}

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
	if m.confirmDelete {
		message = "Delete this note permanently? y confirms, n or Esc cancels"
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

func (m *Model) startCreate() bool {
	id, err := newRecoveryID()
	if err != nil {
		m.errMessage = "Start autosave: " + err.Error()
		return false
	}
	m.mode, m.creating = editing, true
	m.editingID = ""
	m.autosaveID = id
	m.editingSnapshot = note.Note{}
	m.originalTitle, m.originalBody = "", ""
	m.titleInput.SetValue("")
	m.bodyInput.SetValue("")
	m.titleInput.Focus()
	m.bodyInput.Blur()
	m.status, m.errMessage = "", ""
	m.startAutosaveSession()
	return true
}

func (m *Model) startEdit(n note.Note) bool {
	id, err := newRecoveryID()
	if err != nil {
		m.errMessage = "Start autosave: " + err.Error()
		return false
	}
	m.titleInput.SetValue(n.Title)
	m.bodyInput.SetValue(n.Body)
	if m.titleInput.Value() != n.Title || m.bodyInput.Value() != n.Body {
		m.errMessage = "This note exceeds editor limits or contains text the editor cannot preserve; edit it outside the TUI"
		m.status = ""
		return false
	}
	m.mode, m.creating = editing, false
	m.editingID = n.ID
	m.autosaveID = id
	m.editingSnapshot = n
	m.originalTitle, m.originalBody = n.Title, n.Body
	m.titleInput.Focus()
	m.bodyInput.Blur()
	m.resizeEditors()
	m.status, m.errMessage = "", ""
	m.startAutosaveSession()
	return true
}

func (m *Model) saveNote() tea.Cmd {
	title, body, id, create, expected := strings.TrimSpace(m.titleInput.Value()), m.bodyInput.Value(), m.editingID, m.creating, m.editingSnapshot
	store, recoveryID := m.recoveryStore, m.autosaveID
	service := m.service
	ctx := m.startOperation()
	return func() tea.Msg {
		var n note.Note
		var err error
		if create {
			n, err = service.Create(ctx, title, body)
		} else {
			expected.ID = id
			n, err = service.UpdateExpected(ctx, expected, title, body)
		}
		var cleanupErr error
		if err == nil && store != nil {
			cleanupErr = store.DeleteRecovery(context.Background(), recoveryID)
		}
		return noteSavedMsg{note: n, err: err, cleanupErr: cleanupErr}
	}
}

func (m *Model) deleteNote(id string) tea.Cmd {
	ctx := m.startOperation()
	return func() tea.Msg {
		return noteDeletedMsg{err: m.service.Delete(ctx, id)}
	}
}

func (m *Model) loadNotes() tea.Cmd {
	m.pending = true
	if m.searchActive {
		return m.searchNotes(m.searchQuery)
	}
	ctx := m.startOperation()
	return func() tea.Msg {
		notes, err := m.service.List(ctx)
		return notesLoadedMsg{notes: notes, err: err}
	}
}

func (m *Model) searchNotes(query string) tea.Cmd {
	m.pending = true
	ctx := m.startOperation()
	return func() tea.Msg {
		notes, err := search.Notes(ctx, m.repository, query)
		return searchCompletedMsg{notes: notes, err: err}
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
	width, height := m.width-4, m.height-7
	if m.width >= 80 {
		width = m.width - m.width/3 - 8
		height = m.height - 9
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	m.preview.Width, m.preview.Height = width, height
	if n, ok := m.selectedNote(); ok {
		m.preview.SetContent(preview(n))
	} else {
		m.preview.SetContent("")
	}
	m.preview.GotoTop()
}

func visibleWindowStart(total, selected, visible int) int {
	if total <= visible {
		return 0
	}
	start := selected - visible/2
	if start < 0 {
		return 0
	}
	if end := start + visible; end > total {
		return total - visible
	}
	return start
}

func (m *Model) resizeEditors() {
	width := m.width - 8
	if width < 20 {
		width = 20
	}
	m.titleInput.Width = width
	m.bodyInput.SetWidth(width)
	height := m.height - 10
	if height < 3 {
		height = 3
	}
	m.bodyInput.SetHeight(height)
}

func (m Model) selectedNote() (note.Note, bool) {
	if m.selected < 0 || m.selected >= len(m.notes) {
		return note.Note{}, false
	}
	return m.notes[m.selected], true
}

func (m *Model) clampSelection() {
	if m.selected >= len(m.notes) {
		m.selected = len(m.notes) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
}

func (m Model) dirty() bool {
	return m.titleInput.Value() != m.originalTitle || m.bodyInput.Value() != m.originalBody
}

func preview(n note.Note) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", sanitizeTerminalLine(n.Title))
	if len(n.Tags) > 0 {
		tags := make([]string, len(n.Tags))
		for i, tag := range n.Tags {
			tags[i] = sanitizeTerminalLine(tag)
		}
		fmt.Fprintf(&b, "\nTags: #%s\n", strings.Join(tags, " #"))
	}
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

// Run starts the full-screen terminal application.
func Run(ctx context.Context, service *note.Service, repository note.Repository, workspaceName, workspacePath string, options ...Option) error {
	model := NewModel(service, repository, workspaceName, workspacePath, options...)
	model.newOperationContext = func() (context.Context, context.CancelFunc) {
		return context.WithCancel(ctx)
	}
	if model.documents != nil {
		model.documents.newOperationContext = model.newOperationContext
	}
	program := tea.NewProgram(&model, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := program.Run()
	return err
}

// updateDocuments routes messages to the documents screen when it is active or
// when a document operation result arrives.
func (m *Model) updateDocuments(message tea.Msg) (bool, tea.Cmd) {
	if size, ok := message.(tea.WindowSizeMsg); ok {
		m.documents.resize(size.Width, size.Height)
		return false, nil
	}
	_, isResult := message.(documentMessage)
	if !isResult && !m.documentsActive {
		return false, nil
	}
	cmd, leave := m.documents.update(message)
	if leave && !m.documents.busy() {
		m.documentsActive = false
		m.recoveryDismissed = false
		return true, m.loadNotes()
	}
	return true, cmd
}
