package tui

import (
	"context"
	"encoding/json"
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
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/recovery"
)

// documentMessage marks results of document operations, which are routed to
// the document screen.
type documentMessage interface{ isDocumentMessage() }

type documentSavedMsg struct {
	document   document.Document
	err        error
	cleanupErr error
}

// documentReloadedMsg reports an operation that changed the file, together
// with the file's current document and proposals.
type documentReloadedMsg struct {
	description string
	document    document.Document
	changes     []document.Change
	showChanges bool
	cleanupErr  error
	err         error
}
type documentChangesLoadedMsg struct {
	changes []document.Change
	err     error
}
type imageLoadedMsg struct {
	image document.Image
	alt   string
	err   error
}
type documentAutosaveTickMsg struct{ session uint64 }
type documentAutosaveFinishedMsg struct {
	session uint64
	err     error
}

// documentRecoveryData is the autosaved document editor state: the saved
// document the edits started from and the unsaved draft.
type documentRecoveryData struct {
	Snapshot       document.Document          `json:"snapshot"`
	SnapshotBlocks map[string]json.RawMessage `json:"snapshot_blocks,omitempty"`
	Draft          document.Draft             `json:"draft"`
}

func (documentSavedMsg) isDocumentMessage()            {}
func (documentReloadedMsg) isDocumentMessage()         {}
func (documentChangesLoadedMsg) isDocumentMessage()    {}
func (imageLoadedMsg) isDocumentMessage()              {}
func (documentAutosaveTickMsg) isDocumentMessage()     {}
func (documentAutosaveFinishedMsg) isDocumentMessage() {}

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

// documentsScreen edits one document file and reviews its proposed changes.
// It owns UI state only; document rules live in the document service.
type documentsScreen struct {
	service        *document.Service
	path           string
	theme          theme
	width, height  int
	showChanges    bool
	reviewing      bool
	changes        []document.Change
	selectedChange int
	changeReview   viewport.Model
	pending        bool
	canUndo        bool
	canRedo        bool
	status         string
	errMessage     string

	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc
	recoveryStore       recovery.Store
	autosaveSession     uint64
	autosaveCancel      context.CancelFunc
	autosaveScheduler   func(uint64) tea.Cmd

	// Editor state.
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

func newDocumentsScreen(service *document.Service, path string, t theme, newContext func() (context.Context, context.CancelFunc)) *documentsScreen {
	body := textarea.New()
	body.Prompt = ""
	body.ShowLineNumbers = false
	body.Placeholder = "Write Markdown…"
	body.CharLimit = 5_000_000
	body.MaxHeight = 0
	prompt := textinput.New()
	prompt.CharLimit = document.MaxRunningTextLength * 2
	return &documentsScreen{
		service: service, path: path, theme: t, body: body, promptInput: prompt,
		changeReview: viewport.New(0, 0), newOperationContext: newContext,
		canUndo: service.CanUndo(), canRedo: service.CanRedo(), layout: document.DefaultLayout(),
	}
}

func (s *documentsScreen) resize(width, height int) {
	s.width, s.height = width, height
	s.resizeChangeReview()
	s.layoutEditor()
}

func (s *documentsScreen) update(message tea.Msg) tea.Cmd {
	switch msg := message.(type) {
	case documentAutosaveTickMsg:
		if msg.session == s.autosaveSession {
			return s.saveDocumentRecovery(msg.session)
		}
	case documentAutosaveFinishedMsg:
		if msg.session == s.autosaveSession {
			if s.autosaveCancel != nil {
				s.autosaveCancel()
				s.autosaveCancel = nil
			}
			if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
				s.errMessage = "Autosave failed: " + msg.err.Error()
			}
			return s.scheduleDocumentAutosave(msg.session)
		}
	case documentSavedMsg:
		s.finishOperation()
		s.pending = false
		s.refreshHistory()
		if msg.err != nil {
			if isCancelled(msg.err) {
				s.errMessage, s.status = "", "Save cancelled; unsaved changes remain"
			} else {
				s.errMessage = msg.err.Error()
			}
			s.startDocumentAutosaveSession()
			return s.scheduleDocumentAutosave(s.autosaveSession)
		}
		s.loadDocument(msg.document)
		s.errMessage, s.status = "", "Saved “"+msg.document.Title+"”"
		if msg.cleanupErr != nil {
			s.status += " (could not remove recovery draft: " + msg.cleanupErr.Error() + ")"
		}
		s.startDocumentAutosaveSession()
		return s.scheduleDocumentAutosave(s.autosaveSession)
	case documentReloadedMsg:
		s.finishOperation()
		s.pending = false
		s.refreshHistory()
		if msg.err != nil {
			s.errMessage = msg.err.Error()
			s.startDocumentAutosaveSession()
			return s.scheduleDocumentAutosave(s.autosaveSession)
		}
		if !s.dirty() || msg.showChanges {
			// The proposal recorded the edits, so the editor returns to the
			// saved document.
			s.loadDocument(msg.document)
		} else {
			s.snapshot = msg.document
		}
		s.changes = msg.changes
		s.selectedChange = max(min(s.selectedChange, len(s.changes)-1), 0)
		if msg.showChanges {
			s.showChanges, s.reviewing = true, false
		}
		if s.reviewing {
			s.setChangeReviewContent()
		}
		s.errMessage, s.status = "", msg.description
		if msg.cleanupErr != nil {
			s.status += " (could not remove recovery draft: " + msg.cleanupErr.Error() + ")"
		}
		s.startDocumentAutosaveSession()
		return s.scheduleDocumentAutosave(s.autosaveSession)
	case documentChangesLoadedMsg:
		s.finishOperation()
		s.pending = false
		if msg.err != nil {
			s.errMessage = msg.err.Error()
			return nil
		}
		s.changes = msg.changes
		s.selectedChange = max(min(s.selectedChange, len(s.changes)-1), 0)
		s.showChanges, s.reviewing = true, false
		s.errMessage = ""
	case imageLoadedMsg:
		s.finishOperation()
		s.pending = false
		if errors.Is(msg.err, context.Canceled) {
			s.errMessage, s.status = "", "Image insert cancelled"
			return nil
		}
		if msg.err != nil {
			s.errMessage = msg.err.Error()
			return nil
		}
		s.errMessage = ""
		s.images = document.WithImage(document.Document{Images: s.images}, msg.image).Images
		s.insertBlock(document.ImageMarkdown(msg.alt, msg.image.Name))
		s.status = "Inserted image " + msg.image.Name
		cmd := s.focusBody()
		s.layoutEditor()
		return cmd
	case tea.KeyMsg:
		cmd := s.updateKey(msg)
		s.layoutEditor()
		return cmd
	case tea.MouseMsg:
		cmd := s.updateMouse(msg)
		s.layoutEditor()
		return cmd
	}
	return nil
}

func isCancelled(err error) bool {
	return err != nil && strings.Contains(err.Error(), context.Canceled.Error())
}

func (s *documentsScreen) updateKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	if s.width < 40 || s.height < 10 {
		if key == "q" && !s.pending && !s.dirty() {
			return s.close()
		}
		return nil
	}
	if s.pending {
		if key == "ctrl+c" {
			s.cancelPending()
			s.status = "Cancelling…"
			return nil
		}
		s.status = "Please wait for the current operation to finish"
		return nil
	}
	if s.showChanges {
		return s.updateChangesKey(msg)
	}
	return s.updateEditorKey(msg)
}

// loadChanges opens the list of proposals recorded in this file.
func (s *documentsScreen) loadChanges() tea.Cmd {
	s.pending = true
	ctx := s.startOperation()
	service, path := s.service, s.path
	return func() tea.Msg {
		changes, err := service.Changes(ctx, path)
		return documentChangesLoadedMsg{changes: changes, err: err}
	}
}

// reload runs an operation that changes the file, then reads the document and
// its proposals back.
func (s *documentsScreen) reload(showChanges bool, run func(context.Context) (string, error)) tea.Cmd {
	s.stopDocumentAutosave()
	s.pending = true
	s.errMessage = ""
	ctx := s.startOperation()
	service, path := s.service, s.path
	return func() tea.Msg {
		description, err := run(ctx)
		if err != nil {
			return documentReloadedMsg{err: err}
		}
		d, err := service.Get(ctx, path)
		if err != nil {
			return documentReloadedMsg{err: err}
		}
		changes, err := service.Changes(ctx, path)
		return documentReloadedMsg{description: description, document: d, changes: changes, showChanges: showChanges, err: err}
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
			if key == "a" && s.dirty() {
				s.status = "Save or discard your edits before accepting a proposal"
				return nil
			}
			service, path := s.service, s.path
			if key == "a" {
				return s.reload(false, func(ctx context.Context) (string, error) {
					_, err := service.Accept(ctx, path, change.ID)
					return "Proposal accepted", err
				})
			}
			return s.reload(false, func(ctx context.Context) (string, error) {
				return "Proposal rejected", service.Reject(ctx, path, change.ID)
			})
		}
		return nil
	}
	switch key {
	case "esc", "left", "f4":
		s.showChanges = false
		return s.focusBody()
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

func (s *documentsScreen) history(undo bool) tea.Cmd {
	service := s.service
	return s.reload(false, func(ctx context.Context) (string, error) {
		run, prefix := service.Redo, "Redid "
		if undo {
			run, prefix = service.Undo, "Undid "
		}
		description, err := run(ctx)
		return prefix + strings.ToLower(description), err
	})
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

func pageText(page document.Page, width int) string {
	lines := make([]string, len(page.Lines))
	for i, line := range page.Lines {
		lines[i] = runewidth.Truncate(sanitizeTerminalLine(line), max(width, 1), "")
	}
	return strings.Join(lines, "\n")
}

// view renders the document screen below the shared header.
func (s *documentsScreen) view(header string) string {
	if s.showChanges {
		return s.viewChanges(header)
	}
	return s.editorView(header)
}

func (s *documentsScreen) viewChanges(header string) string {
	var b strings.Builder
	b.WriteString(header + "\n\nProposed changes to this document\n")
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
		b.WriteString("\n↑/↓ select  Enter review  ·  Esc returns to the editor")
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

// visibleWindowStart returns the first index of a list window of the given
// size that keeps selected visible.
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
