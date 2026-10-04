package tui

import (
	"context"
	"errors"
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
	proposal bool
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
type documentChangesLoadedMsg struct {
	changes []document.Change
	err     error
}
type documentProposedMsg struct {
	change document.Change
	err    error
}
type documentChangeUpdatedMsg struct {
	description string
	err         error
}
type imageLoadedMsg struct {
	image document.Image
	alt   string
	err   error
}

func (documentsLoadedMsg) isDocumentMessage()       {}
func (documentLoadedMsg) isDocumentMessage()        {}
func (documentSavedMsg) isDocumentMessage()         {}
func (documentDeletedMsg) isDocumentMessage()       {}
func (documentHistoryMsg) isDocumentMessage()       {}
func (documentChangesLoadedMsg) isDocumentMessage() {}
func (documentProposedMsg) isDocumentMessage()      {}
func (documentChangeUpdatedMsg) isDocumentMessage() {}
func (imageLoadedMsg) isDocumentMessage()           {}

type documentMode int

const (
	documentBrowsing documentMode = iota
	documentEditing
)

type editorFocus int

const (
	focusBody editorFocus = iota
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
	service           *document.Service
	singleFile        bool
	theme             theme
	width, height     int
	mode              documentMode
	documents         []document.Document
	selected          int
	initialDocumentID string
	showPreview       bool
	showChanges       bool
	changesDocumentID string
	reviewing         bool
	changes           []document.Change
	selectedChange    int
	outline           []document.Section
	previewPages      []document.Page
	previewPage       int
	preview           viewport.Model
	changeReview      viewport.Model
	confirmDelete     bool
	help              bool
	pending           bool
	canUndo           bool
	canRedo           bool
	status            string
	errMessage        string

	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc

	// Editor state.
	creating       bool
	proposing      bool
	snapshot       document.Document
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
	body, images string
	layout       document.Layout
}

func newDocumentsScreen(service *document.Service, t theme, newContext func() (context.Context, context.CancelFunc)) *documentsScreen {
	body := textarea.New()
	body.Prompt = ""
	body.ShowLineNumbers = false
	body.Placeholder = "Write Markdown…"
	body.CharLimit = 5_000_000
	body.MaxHeight = 0
	prompt := textinput.New()
	prompt.CharLimit = document.MaxRunningTextLength * 2
	return &documentsScreen{
		service: service, theme: t, body: body, promptInput: prompt,
		preview: viewport.New(0, 0), changeReview: viewport.New(0, 0), newOperationContext: newContext,
		canUndo: service.CanUndo(), canRedo: service.CanRedo(), layout: document.DefaultLayout(),
	}
}

func (s *documentsScreen) init() tea.Cmd { return s.loadDocuments() }

func (s *documentsScreen) resize(width, height int) {
	s.width, s.height = width, height
	s.resizePreview()
	s.resizeChangeReview()
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
			s.selectInitialDocument()
			s.clampSelection()
			s.refreshPreview()
		}
		s.refreshHistory()
		if msg.err == nil && s.showChanges && s.changesDocumentID != "" {
			return s.loadChanges(s.changesDocumentID), false
		}
	case documentLoadedMsg:
		s.finishOperation()
		s.pending = false
		if msg.err != nil {
			s.errMessage = msg.err.Error()
			return nil, false
		}
		cmd := s.startEdit(msg.document)
		if s.mode == documentEditing {
			s.proposing = msg.proposal
		}
		return cmd, false
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
		return s.loadDocuments(), false
	case documentProposedMsg:
		s.finishOperation()
		s.pending = false
		if msg.err != nil {
			s.errMessage = msg.err.Error()
			return nil, false
		}
		s.mode, s.proposing = documentBrowsing, false
		s.errMessage, s.status = "", "Proposal recorded: "+msg.change.ID
		return s.loadChanges(msg.change.DocumentID), false
	case documentChangesLoadedMsg:
		s.finishOperation()
		s.pending = false
		if msg.err != nil {
			s.errMessage = msg.err.Error()
			return nil, false
		}
		s.changes = msg.changes
		s.selectedChange = max(min(s.selectedChange, len(s.changes)-1), 0)
		s.showChanges, s.reviewing = true, false
		s.errMessage = ""
	case documentChangeUpdatedMsg:
		s.finishOperation()
		s.pending = false
		s.refreshHistory()
		if msg.err != nil {
			s.errMessage = msg.err.Error()
			return nil, false
		}
		s.status, s.errMessage = msg.description, ""
		if s.changesDocumentID != "" {
			return s.loadDocuments(), false
		}
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
		if errors.Is(msg.err, context.Canceled) {
			s.errMessage, s.status = "", "Image insert cancelled"
			return nil, false
		}
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
		if s.mode == documentBrowsing && !s.showChanges && !s.pending && !s.help && !s.confirmDelete &&
			msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && s.width >= 80 {
			s.clickOutline(msg.X, msg.Y)
			return nil, false
		}
		if s.mode == documentBrowsing && s.showPreview && !s.showChanges && !s.pending && msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelDown:
				if s.preview.AtBottom() && s.changePreviewPage(1, false) {
					return nil, false
				}
				s.preview, _ = s.preview.Update(msg)
			case tea.MouseButtonWheelUp:
				if s.preview.AtTop() && s.changePreviewPage(-1, false) {
					s.preview.GotoBottom()
					return nil, false
				}
				s.preview, _ = s.preview.Update(msg)
			}
			return nil, false
		}
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
	if (s.width < 40 || s.height < 10) && key == "q" && s.mode == documentBrowsing {
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
	if s.showChanges {
		return s.updateChangesKey(msg), false
	}
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
	switch key {
	case "{", "}":
		step := 1
		if key == "{" {
			step = -1
		}
		s.stepSection(step)
		return nil, false
	}
	if s.showPreview {
		switch key {
		case "right":
			s.changePreviewPage(1, false)
			return nil, false
		case "left":
			s.changePreviewPage(-1, false)
			return nil, false
		case "up", "k", "pgup":
			if s.preview.AtTop() && s.changePreviewPage(-1, false) {
				s.preview.GotoBottom()
				return nil, false
			}
			s.preview, _ = s.preview.Update(msg)
			return nil, false
		case "down", "j", "pgdown":
			if s.preview.AtBottom() && s.changePreviewPage(1, false) {
				return nil, false
			}
			s.preview, _ = s.preview.Update(msg)
			return nil, false
		case "home", "end":
			s.preview, _ = s.preview.Update(msg)
			return nil, false
		}
	}
	switch key {
	case "q":
		return tea.Quit, false
	case "tab":
		return nil, true
	case "c":
		if d, ok := s.selectedDocument(); ok {
			s.pending = true
			ctx := s.startOperation()
			service := s.service
			return func() tea.Msg {
				full, err := service.Get(ctx, d.ID)
				return documentLoadedMsg{document: full, proposal: true, err: err}
			}, false
		}
	case "v":
		if d, ok := s.selectedDocument(); ok {
			return s.loadChanges(d.ID), false
		}
	case "?":
		s.help = true
	case "esc", "left":
		s.showPreview = false
	case "right", "enter":
		s.showPreview = true
		s.resizePreview()
	case "[", "]":
		step := 1
		if key == "[" {
			step = -1
		}
		s.changePreviewPage(step, true)
	case "n":
		if !s.singleFile {
			return s.startCreate(), false
		}
	case "e":
		if d, ok := s.selectedDocument(); ok {
			s.pending = true
			ctx := s.startOperation()
			service := s.service
			return func() tea.Msg {
				full, err := service.Get(ctx, d.ID)
				return documentLoadedMsg{document: full, proposal: false, err: err}
			}, false
		}
	case "d":
		if !s.singleFile {
			if _, ok := s.selectedDocument(); ok {
				s.confirmDelete = true
			}
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

func (s *documentsScreen) changePreviewPage(step int, wrap bool) bool {
	if len(s.previewPages) == 0 {
		return false
	}
	next := s.previewPage + step
	if wrap {
		next = (next + len(s.previewPages)) % len(s.previewPages)
	}
	if next < 0 || next >= len(s.previewPages) || next == s.previewPage {
		return false
	}
	s.previewPage = next
	s.showPreviewPage()
	return true
}

func (s *documentsScreen) loadChanges(id string) tea.Cmd {
	s.pending = true
	s.changesDocumentID = id
	ctx := s.startOperation()
	service := s.service
	return func() tea.Msg {
		changes, err := service.Changes(ctx, id)
		return documentChangesLoadedMsg{changes: changes, err: err}
	}
}

func (s *documentsScreen) updateChangesKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	if s.reviewing {
		switch key {
		case "esc", "left":
			s.reviewing = false
		case "up", "down", "j", "k", "pgup", "pgdown", "home", "end":
			s.changeReview, _ = s.changeReview.Update(msg)
		case "a", "r":
			change, ok := s.selectedChangeValue()
			if !ok || change.Status != document.ChangePending {
				break
			}
			s.pending = true
			ctx := s.startOperation()
			service := s.service
			if key == "a" {
				return func() tea.Msg {
					_, err := service.Accept(ctx, change.DocumentID, change.ID)
					return documentChangeUpdatedMsg{description: "Proposal accepted", err: err}
				}
			}
			return func() tea.Msg {
				err := service.Reject(ctx, change.DocumentID, change.ID)
				return documentChangeUpdatedMsg{description: "Proposal rejected", err: err}
			}
		}
		return nil
	}
	switch key {
	case "esc", "left":
		s.showChanges = false
	case "up", "k":
		if s.selectedChange > 0 {
			s.selectedChange--
		}
	case "down", "j":
		if s.selectedChange+1 < len(s.changes) {
			s.selectedChange++
		}
	case "enter":
		if _, ok := s.selectedChangeValue(); ok {
			s.reviewing = true
			s.setChangeReviewContent()
		}
	}
	return nil
}

func (s *documentsScreen) resizeChangeReview() {
	offset := s.changeReview.YOffset
	s.changeReview.Width = max(s.width-4, 1)
	s.changeReview.Height = max(s.height-8, 1)
	if s.reviewing {
		s.setChangeReviewContent()
		s.changeReview.SetYOffset(offset)
	}
}

func (s *documentsScreen) setChangeReviewContent() {
	change, ok := s.selectedChangeValue()
	if !ok {
		s.changeReview.SetContent("")
		return
	}
	content := fmt.Sprintf(
		"%s  ·  %s  ·  %s\n\nCurrent page setup:\n%s\n\nProposed page setup:\n%s\n\n--- Current Markdown ---\n%s\n\n+++ Proposed Markdown +++\n%s",
		change.ID, change.Status, sanitizeTerminalLine(change.Description),
		changeLayoutDescription(change.Before.Layout), changeLayoutDescription(change.After.Layout),
		sanitizeTerminalText(change.Before.Body), sanitizeTerminalText(change.After.Body),
	)
	s.changeReview.SetContent(wrapReviewText(content, max(s.changeReview.Width, 1)))
	s.changeReview.GotoTop()
}

func wrapReviewText(content string, width int) string {
	var wrapped strings.Builder
	for lineIndex, line := range strings.Split(content, "\n") {
		if lineIndex > 0 {
			wrapped.WriteByte('\n')
		}
		lineWidth := 0
		for _, r := range line {
			runeWidth := max(runewidth.RuneWidth(r), 0)
			if lineWidth > 0 && lineWidth+runeWidth > width {
				wrapped.WriteByte('\n')
				lineWidth = 0
			}
			wrapped.WriteRune(r)
			lineWidth += runeWidth
		}
	}
	return wrapped.String()
}

func (s *documentsScreen) selectedChangeValue() (document.Change, bool) {
	if s.selectedChange < 0 || s.selectedChange >= len(s.changes) {
		return document.Change{}, false
	}
	return s.changes[s.selectedChange], true
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

func (s *documentsScreen) selectInitialDocument() {
	if s.initialDocumentID == "" {
		return
	}
	for i, item := range s.documents {
		if item.ID == s.initialDocumentID {
			s.selected = i
			s.showPreview = true
			break
		}
	}
	s.initialDocumentID = ""
}

// refreshPreview paginates the selected document for the print preview.
func (s *documentsScreen) refreshPreview() {
	s.previewPages, s.previewPage, s.outline = nil, 0, nil
	if d, ok := s.selectedDocument(); ok {
		pages, err := document.Paginate(d)
		if err == nil {
			s.previewPages = pages
			s.outline, _ = document.Outline(d)
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
	if s.showChanges {
		return s.viewChanges(header)
	}
	if s.help {
		return header + "\n\n" +
			"Documents\n\n" +
			"↑/↓ or j/k   Select document\n" +
			"Enter        Focus print preview (↑/↓ scroll across pages)\n" +
			"←/→ or [/]   Previous / next page in the preview\n" +
			"{ / }        Previous / next section (or click it in the outline)\n" +
			"n            New document\n" +
			"e            Edit document\n" +
			"c            Propose an edit    v  Review proposals\n" +
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
			return header + "\n" + s.preview.View() + "\n\n↑/↓ scroll across pages  ·  ←/→ or [ ] page " + s.pageLabel() + "  ·  { } section  ·  Esc returns" + s.statusLine()
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
	b.WriteString("\nEnter preview  ·  n new  ·  c propose  ·  v changes  ·  Tab notes  ·  ? help")
	return b.String() + s.statusLine()
}

func (s *documentsScreen) viewWide(header string) string {
	listWidth, previewWidth := s.paneWidths()
	lines, _ := s.sidebar()
	for i, line := range lines {
		lines[i] = runewidth.Truncate(line, max(listWidth-2, 1), "…")
	}
	pane := func(width int, content string) string {
		return lipgloss.NewStyle().Width(width).Height(s.height-5).Border(lipgloss.NormalBorder()).
			BorderForeground(s.theme.border).Padding(0, 1).Render(content)
	}
	content := "Select a document to preview its printed pages."
	if _, ok := s.selectedDocument(); ok {
		content = "Page " + s.pageLabel() + "\n" + s.preview.View()
	}
	footer := "↑/↓ select  Enter focus preview  [ ] page  { } section  e edit  c propose  v changes  Tab notes  ? help  q quit"
	if s.singleFile {
		footer = "Enter focus preview  [ ] page  { } section  click outline to jump  e edit  c propose  v changes  Tab notes  ? help  q quit"
	}
	if s.showPreview {
		footer = "↑/↓ scroll across pages  ←/→ or [ ] page  { } section  Esc return  e edit  ? help  q quit"
	}
	return header + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, pane(listWidth, strings.Join(lines, "\n")), pane(previewWidth, content)) +
		"\n" + footer + s.statusLine()
}

func (s *documentsScreen) paneWidths() (list, preview int) {
	list = max(s.width/3, 24)
	return list, max(s.width-list-4, 30)
}

// currentSection is the last outline entry that starts on or before the page
// being previewed, or -1 before the first heading.
func (s *documentsScreen) currentSection() int {
	current := -1
	for i, section := range s.outline {
		if section.Page <= s.previewPage+1 {
			current = i
		}
	}
	return current
}

// sidebar renders the left pane: the document list when several documents
// can be opened, then the outline of the selected document. targets maps each
// line to an outline index, or -1 when the line is not a section.
func (s *documentsScreen) sidebar() (lines []string, targets []int) {
	add := func(line string, target int) {
		lines = append(lines, line)
		targets = append(targets, target)
	}
	if !s.singleFile {
		add("Documents", -1)
		if len(s.documents) == 0 {
			add("No documents yet.", -1)
			add("Press n to create one.", -1)
		}
		visible := max((s.height-5)/3, 1)
		start := visibleWindowStart(len(s.documents), s.selected, visible)
		for i := start; i < min(start+visible, len(s.documents)); i++ {
			marker := "  "
			if i == s.selected {
				marker = "› "
			}
			add(marker+sanitizeTerminalLine(s.documents[i].Title), -1)
		}
		add("", -1)
	}
	add("Outline", -1)
	if len(s.outline) == 0 {
		if _, ok := s.selectedDocument(); ok {
			add("  No headings", -1)
		}
		return lines, targets
	}
	room := max(s.height-5-len(lines), 1)
	current := s.currentSection()
	start := visibleWindowStart(len(s.outline), max(current, 0), room)
	for i := start; i < min(start+room, len(s.outline)); i++ {
		section := s.outline[i]
		marker := "  "
		if i == current {
			marker = "› "
		}
		indent := strings.Repeat("  ", min(section.Level-1, 4))
		add(fmt.Sprintf("%s%s%s", marker, indent, sanitizeTerminalLine(section.Title)), i)
	}
	return lines, targets
}

func (s *documentsScreen) stepSection(step int) {
	if len(s.outline) == 0 {
		return
	}
	current := s.currentSection()
	next := current + step
	if next < 0 || next >= len(s.outline) {
		return
	}
	s.gotoSection(next)
}

func (s *documentsScreen) gotoSection(index int) {
	s.previewPage = min(max(s.outline[index].Page-1, 0), max(len(s.previewPages)-1, 0))
	s.showPreview = true
	s.resizePreview()
}

// clickOutline jumps to the section under a left click in the sidebar. Rows
// start below the header line and the pane's top border.
func (s *documentsScreen) clickOutline(x, y int) {
	listWidth, _ := s.paneWidths()
	if x < 1 || x > listWidth+1 {
		return
	}
	_, targets := s.sidebar()
	row := y - 2
	if row >= 0 && row < len(targets) && targets[row] >= 0 {
		s.gotoSection(targets[row])
	}
}

func (s *documentsScreen) viewChanges(header string) string {
	var b strings.Builder
	b.WriteString(header + "\n\nDocument changes\n")
	if s.reviewing {
		change, ok := s.selectedChangeValue()
		if ok {
			b.WriteString(s.changeReview.View())
			b.WriteString("\n\n")
			if change.Status == document.ChangePending {
				b.WriteString("a accept  ·  r reject  ·  ↑/↓ scroll  ·  PgUp/PgDn  ·  Esc return")
			} else {
				b.WriteString("↑/↓ scroll  ·  PgUp/PgDn  ·  Esc return")
			}
		}
	} else {
		if len(s.changes) == 0 {
			b.WriteString("\nNo proposed or resolved changes.\n")
		}
		visible := max(s.height-8, 1)
		start := visibleWindowStart(len(s.changes), s.selectedChange, visible)
		end := min(start+visible, len(s.changes))
		for i := start; i < end; i++ {
			change := s.changes[i]
			marker := "  "
			if i == s.selectedChange {
				marker = "> "
			}
			fmt.Fprintf(&b, "%s%s  %s  %s\n", marker, change.Status, change.ID, sanitizeTerminalLine(change.Description))
		}
		b.WriteString("\n↑/↓ select  Enter review  ·  Esc return")
	}
	return b.String() + s.statusLine()
}

func changeLayoutDescription(layout document.Layout) string {
	return fmt.Sprintf("%s %s, %d columns, margins %d/%d/%d/%d mm\nHeader: %s\nFooter: %s\nPage numbers: %s",
		layout.PageSize, layout.Orientation, layout.Columns,
		layout.Margins.Top, layout.Margins.Right, layout.Margins.Bottom, layout.Margins.Left,
		sanitizeTerminalText(layout.Header), sanitizeTerminalText(layout.Footer),
		sanitizeTerminalLine(layout.PageNumbers))
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
		body: s.body.Value(), layout: s.layout, images: imageKey(s.images),
	}
}

func imageKey(images []document.Image) string {
	names := make([]string, len(images))
	for i, img := range images {
		names[i] = img.Name
	}
	return strings.Join(names, ",")
}

// readImageCommand loads an image file off the UI goroutine. A result that
// arrives after the operation is cancelled is discarded.
func readImageCommand(ctx context.Context, path string) tea.Cmd {
	return func() tea.Msg {
		data, err := os.ReadFile(path)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return imageLoadedMsg{err: ctxErr}
		}
		if err != nil {
			return imageLoadedMsg{err: err}
		}
		img, err := document.NewImage(data)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return imageLoadedMsg{err: ctxErr}
		}
		alt := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		return imageLoadedMsg{image: img, alt: alt, err: err}
	}
}

func hasControl(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' })
}
