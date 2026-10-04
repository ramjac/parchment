package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/document"
)

// documentMessage marks results of document operations, which are routed to
// the documents screen even if the user has since switched screens.
type documentMessage interface{ isDocumentMessage() }

type documentsLoadedMsg struct {
	documents []document.Document
	err       error
}
type documentLoadedMsg struct {
	document document.Document
	err      error
}
type documentSavedMsg struct {
	document document.Document
	err      error
}
type documentDeletedMsg struct{ err error }
type documentHistoryMsg struct {
	description string
	err         error
}
type imageLoadedMsg struct {
	image document.Image
	alt   string
	err   error
}

func (documentsLoadedMsg) isDocumentMessage() {}
func (documentLoadedMsg) isDocumentMessage()  {}
func (documentSavedMsg) isDocumentMessage()   {}
func (documentDeletedMsg) isDocumentMessage() {}
func (documentHistoryMsg) isDocumentMessage() {}
func (imageLoadedMsg) isDocumentMessage()     {}

type documentMode int

const (
	documentBrowsing documentMode = iota
	documentEditing
)

type editorFocus int

const (
	focusTitle editorFocus = iota
	focusBody
	focusToolbar
)

type promptKind int

const (
	promptNone promptKind = iota
	promptLink
	promptImage
	promptHeader
	promptFooter
	promptSection
)

// documentsScreen is the interactive documents list, print preview, and
// editor. It owns UI state only; document rules live in the document service.
type documentsScreen struct {
	service       *document.Service
	theme         theme
	width, height int
	mode          documentMode
	documents     []document.Document
	selected      int
	showPreview   bool
	previewPages  []document.Page
	previewPage   int
	preview       viewport.Model
	confirmDelete bool
	help          bool
	pending       bool
	canUndo       bool
	canRedo       bool
	status        string
	errMessage    string

	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc

	// Editor state.
	creating       bool
	snapshot       document.Document
	titleInput     textinput.Model
	body           textarea.Model
	layout         document.Layout
	images         []document.Image
	original       editorDraft
	focus          editorFocus
	toolbarIndex   int
	prompt         promptKind
	promptInput    textinput.Model
	previewing     bool
	editPages      []document.Page
	editPage       int
	discardWarning bool
}

type editorDraft struct {
	title, body, images string
	layout              document.Layout
}

func newDocumentsScreen(service *document.Service, t theme, newContext func() (context.Context, context.CancelFunc)) *documentsScreen {
	title := textinput.New()
	title.Prompt = "Title: "
	title.CharLimit = 200
	body := textarea.New()
	body.Prompt = ""
	body.ShowLineNumbers = false
	body.Placeholder = "Write Markdown…"
	body.CharLimit = 5_000_000
	body.MaxHeight = 0
	prompt := textinput.New()
	prompt.CharLimit = document.MaxRunningTextLength * 2
	return &documentsScreen{
		service: service, theme: t, titleInput: title, body: body, promptInput: prompt,
		preview: viewport.New(0, 0), newOperationContext: newContext,
		canUndo: service.CanUndo(), canRedo: service.CanRedo(), layout: document.DefaultLayout(),
	}
}

func (s *documentsScreen) init() tea.Cmd { return s.loadDocuments() }

func (s *documentsScreen) resize(width, height int) {
	s.width, s.height = width, height
	s.resizePreview()
	s.layoutEditor()
}

// busy reports whether leaving the screen would lose work or interrupt an operation.
func (s *documentsScreen) busy() bool { return s.pending || s.mode == documentEditing }

func (s *documentsScreen) update(message tea.Msg) (tea.Cmd, bool) {
	switch msg := message.(type) {
	case documentsLoadedMsg:
		s.finishOperation()
		s.pending = false
		if msg.err != nil {
			s.errMessage = msg.err.Error()
		} else {
			s.errMessage = ""
			s.documents = msg.documents
			s.clampSelection()
			s.refreshPreview()
		}
		s.refreshHistory()
	case documentLoadedMsg:
		s.finishOperation()
		s.pending = false
		if msg.err != nil {
			s.errMessage = msg.err.Error()
			return nil, false
		}
		return s.startEdit(msg.document), false
	case documentSavedMsg:
		s.finishOperation()
		s.refreshHistory()
		if msg.err != nil {
			s.pending = false
			if isCancelled(msg.err) {
				s.errMessage, s.status = "", "Save cancelled; unsaved changes remain"
			} else {
				s.errMessage = msg.err.Error()
			}
			return nil, false
		}
		s.mode, s.creating = documentBrowsing, false
		s.errMessage, s.status = "", "Saved “"+msg.document.Title+"”"
		return tea.Batch(tea.DisableMouse, s.loadDocuments()), false
	case documentDeletedMsg:
		s.finishOperation()
		s.refreshHistory()
		s.confirmDelete = false
		if msg.err != nil {
			s.pending = false
			s.errMessage = msg.err.Error()
			return nil, false
		}
		s.errMessage, s.status = "", "Document deleted"
		return s.loadDocuments(), false
	case documentHistoryMsg:
		s.finishOperation()
		s.refreshHistory()
		if msg.err != nil {
			s.pending = false
			s.errMessage = msg.err.Error()
			return nil, false
		}
		s.errMessage, s.status = "", msg.description
		return s.loadDocuments(), false
	case imageLoadedMsg:
		s.finishOperation()
		s.pending = false
		if msg.err != nil {
			s.errMessage = msg.err.Error()
			return nil, false
		}
		s.errMessage = ""
		s.images = document.WithImage(document.Document{Images: s.images}, msg.image).Images
		alt := msg.alt
		s.insertBlock(document.ImageMarkdown(alt, msg.image.Name))
		s.status = "Inserted image " + msg.image.Name
		cmd := s.focusBody()
		s.layoutEditor()
		return cmd, false
	case tea.KeyMsg:
		cmd, leave := s.updateKey(msg)
		s.layoutEditor()
		return cmd, leave
	case tea.MouseMsg:
		cmd := s.updateMouse(msg)
		s.layoutEditor()
		return cmd, false
	}
	return nil, false
}

func isCancelled(err error) bool {
	return err != nil && strings.Contains(err.Error(), context.Canceled.Error())
}

func (s *documentsScreen) updateKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	key := msg.String()
	if (s.width < 40 || s.height < 10) && key == "q" {
		s.cancelPending()
		return tea.Quit, false
	}
	if s.pending {
		if key == "ctrl+c" || key == "q" {
			s.cancelPending()
			if s.mode == documentEditing {
				s.status = "Cancelling…"
				return nil, false
			}
			return tea.Quit, false
		}
		s.status = "Please wait for the current operation to finish"
		return nil, false
	}
	if key == "ctrl+c" {
		if s.mode == documentEditing {
			if s.dirty() {
				s.status = "Unsaved changes: Ctrl+S saves, Esc twice discards"
				return nil, false
			}
			return s.stopEditing("Edit closed"), false
		}
		return tea.Quit, false
	}
	if s.mode == documentEditing {
		return s.updateEditorKey(msg), false
	}
	return s.updateBrowseKey(msg)
}

func (s *documentsScreen) updateBrowseKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	key := msg.String()
	if s.confirmDelete {
		switch key {
		case "y":
			if d, ok := s.selectedDocument(); ok {
				s.pending = true
				ctx := s.startOperation()
				service := s.service
				return func() tea.Msg { return documentDeletedMsg{err: service.Delete(ctx, d.ID)} }, false
			}
			s.confirmDelete = false
		case "n", "esc":
			s.confirmDelete = false
			s.status = "Deletion cancelled"
		}
		return nil, false
	}
	if s.help {
		if key == "?" || key == "esc" {
			s.help = false
		}
		return nil, false
	}
	if s.showPreview {
		switch key {
		case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
			s.preview, _ = s.preview.Update(msg)
			return nil, false
		}
	}
	switch key {
	case "q":
		return tea.Quit, false
	case "tab":
		return nil, true
	case "?":
		s.help = true
	case "esc", "left":
		s.showPreview = false
	case "right", "enter":
		s.showPreview = true
		s.resizePreview()
	case "[", "]":
		if len(s.previewPages) > 0 {
			step := 1
			if key == "[" {
				step = -1
			}
			s.previewPage = (s.previewPage + step + len(s.previewPages)) % len(s.previewPages)
			s.showPreviewPage()
		}
	case "n":
		return s.startCreate(), false
	case "e":
		if d, ok := s.selectedDocument(); ok {
			s.pending = true
			ctx := s.startOperation()
			service := s.service
			return func() tea.Msg {
				full, err := service.Get(ctx, d.ID)
				return documentLoadedMsg{document: full, err: err}
			}, false
		}
	case "d":
		if _, ok := s.selectedDocument(); ok {
			s.confirmDelete = true
		}
	case "u", "ctrl+z":
		return s.history("Undid ", s.service.Undo), false
	case "ctrl+r":
		return s.history("Redid ", s.service.Redo), false
	case "up", "k":
		if !s.showPreview && s.selected > 0 {
			s.selected--
			s.refreshPreview()
		}
	case "down", "j":
		if !s.showPreview && s.selected+1 < len(s.documents) {
			s.selected++
			s.refreshPreview()
		}
	}
	return nil, false
}

func (s *documentsScreen) history(prefix string, run func(context.Context) (string, error)) tea.Cmd {
	s.pending = true
	ctx := s.startOperation()
	return func() tea.Msg {
		description, err := run(ctx)
		return documentHistoryMsg{description: prefix + strings.ToLower(description), err: err}
	}
}

func (s *documentsScreen) loadDocuments() tea.Cmd {
	s.pending = true
	ctx := s.startOperation()
	service := s.service
	return func() tea.Msg {
		documents, err := service.List(ctx)
		return documentsLoadedMsg{documents: documents, err: err}
	}
}

func (s *documentsScreen) startOperation() context.Context {
	ctx, cancel := s.newOperationContext()
	s.cancelOperation = cancel
	return ctx
}

func (s *documentsScreen) finishOperation() {
	if s.cancelOperation != nil {
		s.cancelOperation()
		s.cancelOperation = nil
	}
}

func (s *documentsScreen) cancelPending() {
	if s.cancelOperation != nil {
		s.cancelOperation()
	}
}

func (s *documentsScreen) refreshHistory() {
	s.canUndo, s.canRedo = s.service.CanUndo(), s.service.CanRedo()
}

func (s *documentsScreen) selectedDocument() (document.Document, bool) {
	if s.selected < 0 || s.selected >= len(s.documents) {
		return document.Document{}, false
	}
	return s.documents[s.selected], true
}

func (s *documentsScreen) clampSelection() {
	s.selected = max(min(s.selected, len(s.documents)-1), 0)
}

// refreshPreview paginates the selected document for the print preview.
func (s *documentsScreen) refreshPreview() {
	s.previewPages, s.previewPage = nil, 0
	if d, ok := s.selectedDocument(); ok {
		pages, err := document.Paginate(d)
		if err == nil {
			s.previewPages = pages
		}
	}
	s.resizePreview()
}

func (s *documentsScreen) resizePreview() {
	width, height := s.width-4, s.height-8
	if s.width >= 80 {
		width, height = s.width-s.width/3-8, s.height-10
	}
	s.preview.Width, s.preview.Height = max(width, 1), max(height, 1)
	s.showPreviewPage()
}

func (s *documentsScreen) showPreviewPage() {
	if s.previewPage >= len(s.previewPages) {
		s.preview.SetContent("")
		return
	}
	s.preview.SetContent(pageText(s.previewPages[s.previewPage], s.preview.Width))
	s.preview.GotoTop()
}

func pageText(page document.Page, width int) string {
	lines := make([]string, len(page.Lines))
	for i, line := range page.Lines {
		lines[i] = runewidth.Truncate(sanitizeTerminalLine(line), max(width, 1), "")
	}
	return strings.Join(lines, "\n")
}

// view renders the documents screen below the shared header.
func (s *documentsScreen) view(header string) string {
	if s.mode == documentEditing {
		return s.editorView(header)
	}
	if s.help {
		return header + "\n\n" +
			"Documents\n\n" +
			"↑/↓ or j/k   Select document\n" +
			"Enter        Focus print preview (↑/↓ scroll)\n" +
			"[ / ]        Previous / next page in the preview\n" +
			"n            New document\n" +
			"e            Edit document\n" +
			"d            Delete document (confirmation required)\n" +
			"u / Ctrl+Z   Undo    Ctrl+R  Redo\n" +
			"Tab          Switch to notes\n" +
			"?            Close help    q  Quit" + s.statusLine()
	}
	if s.width < 80 {
		return s.viewNarrow(header)
	}
	return s.viewWide(header)
}

func (s *documentsScreen) viewNarrow(header string) string {
	if s.showPreview {
		if _, ok := s.selectedDocument(); ok {
			return header + "\n" + s.preview.View() + "\n\n↑/↓ scroll  ·  [ ] page " + s.pageLabel() + "  ·  Esc returns" + s.statusLine()
		}
	}
	var b strings.Builder
	visible := max(s.height-8, 1)
	start := visibleWindowStart(len(s.documents), s.selected, visible)
	end := min(start+visible, len(s.documents))
	b.WriteString(header + "\n\nDocuments\n")
	if len(s.documents) == 0 {
		b.WriteString("  No documents yet. Press n to create one.\n")
	}
	for i := start; i < end; i++ {
		marker := "  "
		if i == s.selected {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s%s\n", marker, sanitizeTerminalLine(s.documents[i].Title))
	}
	b.WriteString("\nEnter preview  ·  n new  ·  Tab notes  ·  ? help")
	return b.String() + s.statusLine()
}

func (s *documentsScreen) viewWide(header string) string {
	listWidth := max(s.width/3, 24)
	previewWidth := max(s.width-listWidth-4, 30)
	visible := max((s.height-9)/2, 1)
	start := visibleWindowStart(len(s.documents), s.selected, visible)
	end := min(start+visible, len(s.documents))
	var list strings.Builder
	list.WriteString("Documents\n")
	if len(s.documents) == 0 {
		list.WriteString("\nNo documents yet.\nPress n to create one.")
	}
	for i := start; i < end; i++ {
		marker := "  "
		if i == s.selected {
			marker = "› "
		}
		fmt.Fprintf(&list, "%s%s\n", marker, sanitizeTerminalLine(s.documents[i].Title))
	}
	pane := func(width int, content string) string {
		return lipgloss.NewStyle().Width(width).Height(s.height-5).Border(lipgloss.NormalBorder()).
			BorderForeground(s.theme.border).Padding(0, 1).Render(content)
	}
	content := "Select a document to preview its printed pages."
	if _, ok := s.selectedDocument(); ok {
		content = "Page " + s.pageLabel() + "\n" + s.preview.View()
	}
	footer := "↑/↓ select  Enter focus preview  [ ] page  n new  e edit  d delete  Tab notes  ? help  q quit"
	if s.showPreview {
		footer = "↑/↓ scroll  [ ] page  Esc return to list  n new  e edit  d delete  Tab notes  ? help  q quit"
	}
	return header + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, pane(listWidth, list.String()), pane(previewWidth, content)) +
		"\n" + footer + s.statusLine()
}

func (s *documentsScreen) pageLabel() string {
	if len(s.previewPages) == 0 {
		return "–"
	}
	return fmt.Sprintf("%d/%d", s.previewPage+1, len(s.previewPages))
}

func (s *documentsScreen) statusLine() string {
	undo, redo := "undo unavailable", "redo unavailable"
	if s.canUndo {
		undo = "undo available"
	}
	if s.canRedo {
		redo = "redo available"
	}
	message := s.status
	if s.errMessage != "" {
		message = "Error: " + s.errMessage
	}
	if s.confirmDelete {
		message = "Delete this document permanently? y confirms, n or Esc cancels"
	}
	if message == "" {
		message = "Ready"
	}
	return "\n" + sanitizeTerminalLine(message) + "  ·  " + undo + "  ·  " + redo
}

func (s *documentsScreen) dirty() bool { return s.currentDraft() != s.original }

func (s *documentsScreen) currentDraft() editorDraft {
	return editorDraft{
		title: s.titleInput.Value(), body: s.body.Value(), layout: s.layout, images: imageKey(s.images),
	}
}

func imageKey(images []document.Image) string {
	names := make([]string, len(images))
	for i, img := range images {
		names[i] = img.Name
	}
	return strings.Join(names, ",")
}

// readImageCommand loads an image file off the UI goroutine.
func readImageCommand(path string) tea.Cmd {
	return func() tea.Msg {
		data, err := os.ReadFile(path)
		if err != nil {
			return imageLoadedMsg{err: err}
		}
		img, err := document.NewImage(data)
		alt := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		return imageLoadedMsg{image: img, alt: alt, err: err}
	}
}

func hasControl(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' })
}
