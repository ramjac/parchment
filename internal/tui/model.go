package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
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
	note note.Note
	err  error
}

type noteDeletedMsg struct{ err error }
type historyChangedMsg struct {
	description string
	err         error
}

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
	singleMarkdownFile  bool
	singleDocumentFile  bool
	initialNoteID       string
}

// Option customizes the interactive model.
type Option func(*Model)

// WithDocuments adds the documents screen, reached from notes with Tab.
func WithDocuments(service *document.Service) Option {
	return func(m *Model) {
		m.documents = newDocumentsScreen(service, m.theme, m.newOperationContext)
	}
}

// WithSingleMarkdownFile restricts the notes screen to editing one ordinary
// Markdown file without adding Parchment metadata.
func WithSingleMarkdownFile() Option {
	return func(m *Model) {
		m.singleMarkdownFile = true
		m.showPreview = true
	}
}

// WithInitialNote selects and previews one note after the first list load.
func WithInitialNote(id string) Option {
	return func(m *Model) {
		m.initialNoteID = id
	}
}

// WithInitialDocument opens the documents screen on one document.
func WithInitialDocument(id string) Option {
	return func(m *Model) {
		m.documentsActive = true
		if m.documents != nil {
			m.documents.initialDocumentID = id
		}
	}
}

// WithSingleDocumentFile keeps navigation and creation within one open document.
func WithSingleDocumentFile() Option {
	return func(m *Model) {
		m.singleDocumentFile = true
		if m.documents != nil {
			m.documents.singleFile = true
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
	for _, option := range options {
		option(&m)
	}
	return m
}

// Init loads the initial note list.
func (m *Model) Init() tea.Cmd {
	if m.documentsActive {
		return m.documents.init()
	}
	return m.loadNotes()
}

// Update applies a terminal message to the notes screen.
func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.documents != nil {
		if handled, cmd := m.updateDocuments(message); handled {
			return m, cmd
		}
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeEditors()
		m.resizePreview()
	case tea.MouseMsg:
		if m.mode == editing && !m.pending && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			bodyY := 3
			if !m.singleMarkdownFile {
				bodyY += 2
				if msg.Y == 3 {
					m.titleInput.SetCursor(max(msg.X-len(m.titleInput.Prompt), 0))
					m.bodyInput.Blur()
					return m, m.titleInput.Focus()
				}
			}
			if placeTextareaCursor(&m.bodyInput, msg.X, msg.Y-bodyY) {
				m.titleInput.Blur()
				return m, m.bodyInput.Focus()
			}
		}
		return m, nil
	case notesLoadedMsg:
		m.finishOperation()
		m.pending = false
		if msg.err != nil {
			m.errMessage = msg.err.Error()
		} else {
			m.errMessage = ""
			m.notes = msg.notes
			m.selectInitialNote()
			m.clampSelection()
			m.resizePreview()
			m.refreshHistoryAvailability()
			if m.singleMarkdownFile {
				if n, ok := m.selectedNote(); ok {
					if !m.startEdit(n) {
						return m, nil
					}
					return m, m.bodyInput.Focus()
				}
			}
		}
		if msg.err != nil {
			m.refreshHistoryAvailability()
		}
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
			return m, nil
		}
		m.refreshHistoryAvailability()
		m.errMessage = ""
		m.status = "Saved “" + msg.note.Title + "”"
		if m.singleMarkdownFile {
			m.editingSnapshot = msg.note
			m.originalTitle, m.originalBody = msg.note.Title, msg.note.Body
			m.pending = false
			return m, nil
		}
		m.mode = browsing
		m.creating = false
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
	if key == "ctrl+c" {
		if m.mode == editing {
			if m.dirty() {
				m.status = "Unsaved changes: Ctrl+S saves, Esc discards"
				return m, nil
			}
			m.mode = browsing
			return m, nil
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
			if m.singleMarkdownFile {
				if m.dirty() {
					m.titleInput.SetValue(m.originalTitle)
					m.bodyInput.SetValue(m.originalBody)
					m.status = "Edit discarded"
					return m, nil
				}
				return m, tea.Quit
			}
			m.mode = browsing
			m.creating = false
			m.status = "Edit cancelled"
			return m, nil
		case "ctrl+s":
			m.pending = true
			return m, m.saveNote()
		case "tab":
			if m.singleMarkdownFile {
				return m, nil
			}
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
	if m.singleMarkdownFile && key != "e" && key != "enter" &&
		key != "up" && key != "k" && key != "down" && key != "j" &&
		key != "pgup" && key != "pgdown" && key != "home" && key != "end" &&
		key != "left" && key != "right" && key != "esc" &&
		key != "u" && key != "ctrl+z" && key != "ctrl+r" {
		return m, nil
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
		m.startCreate()
		return m, m.titleInput.Focus()
	case "e":
		if n, ok := m.selectedNote(); ok {
			if !m.startEdit(n) {
				return m, nil
			}
			return m, m.titleInput.Focus()
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
		sanitizeTextareaView(&bodyInput)
		content := header + "\n" + state + "  ·  "
		if m.singleMarkdownFile {
			content += "Ctrl+S saves  ·  Esc cancels\n\n" + bodyInput.View()
		} else {
			content += "Tab switches fields  ·  Ctrl+S saves  ·  Esc cancels\n\n" +
				titleInput.View() + "\n\n" + bodyInput.View()
		}
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
	if m.singleMarkdownFile {
		return header + "\n" + m.preview.View() +
			"\ne edit  ·  ↑/↓ scroll  ·  u undo  ·  Ctrl+R redo  ·  q quit" + m.statusLine()
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

func (m *Model) startCreate() {
	m.mode, m.creating = editing, true
	m.editingID = ""
	m.editingSnapshot = note.Note{}
	m.originalTitle, m.originalBody = "", ""
	m.titleInput.SetValue("")
	m.bodyInput.SetValue("")
	m.titleInput.Focus()
	m.bodyInput.Blur()
	m.status, m.errMessage = "", ""
}

func (m *Model) startEdit(n note.Note) bool {
	m.titleInput.SetValue(n.Title)
	m.bodyInput.SetValue(n.Body)
	if m.titleInput.Value() != n.Title || m.bodyInput.Value() != n.Body {
		m.errMessage = "This note exceeds editor limits or contains text the editor cannot preserve; edit it outside the TUI"
		m.status = ""
		return false
	}
	m.mode, m.creating = editing, false
	m.editingID = n.ID
	m.editingSnapshot = n
	m.originalTitle, m.originalBody = n.Title, n.Body
	if m.singleMarkdownFile {
		m.titleInput.Blur()
		m.bodyInput.Focus()
	} else {
		m.titleInput.Focus()
		m.bodyInput.Blur()
	}
	m.resizeEditors()
	m.status, m.errMessage = "", ""
	return true
}

func (m *Model) saveNote() tea.Cmd {
	title, body, id, create, expected := strings.TrimSpace(m.titleInput.Value()), m.bodyInput.Value(), m.editingID, m.creating, m.editingSnapshot
	ctx := m.startOperation()
	return func() tea.Msg {
		var n note.Note
		var err error
		if create {
			n, err = m.service.Create(ctx, title, body)
		} else {
			expected.ID = id
			n, err = m.service.UpdateExpected(ctx, expected, title, body)
		}
		return noteSavedMsg{note: n, err: err}
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
		if m.singleMarkdownFile {
			m.preview.SetContent(sanitizeTerminalText(artifactfile.StripPrivateBlocks(n.Body)))
		} else {
			m.preview.SetContent(preview(n))
		}
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

func (m *Model) selectInitialNote() {
	if m.initialNoteID == "" {
		return
	}
	for i, item := range m.notes {
		if item.ID == m.initialNoteID {
			m.selected = i
			m.showPreview = true
			break
		}
	}
	m.initialNoteID = ""
}

func (m Model) dirty() bool {
	return m.titleInput.Value() != m.originalTitle || m.bodyInput.Value() != m.originalBody
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

// sanitizeTextareaView replaces control characters in a view copy of editor.
// SetValue moves the cursor to the end of the text, which would draw it away
// from (or outside the scrolled view of) its real position, so it is skipped
// when nothing needs replacing.
func sanitizeTextareaView(editor *textarea.Model) {
	value := editor.Value()
	if sanitized := sanitizeTerminalText(value); sanitized != value {
		editor.SetValue(sanitized)
	}
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
	program := tea.NewProgram(&model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))
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
		if m.singleDocumentFile {
			return true, tea.Quit
		}
		m.documentsActive = false
		return true, m.loadNotes()
	}
	return true, cmd
}
