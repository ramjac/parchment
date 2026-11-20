package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/tui/editor"
	"example.com/parchment/internal/tui/termimg"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/recovery"
)

// documentMessage marks results of document operations, which are routed to
// the document screen.
type documentMessage interface{ isDocumentMessage() }

type documentSavedMsg struct {
	document   document.Document
	history    *historyAvailability
	err        error
	cleanupErr error
}

// documentReloadedMsg reports an operation that changed the file, together
// with the file's current document and tracked changes.
type documentReloadedMsg struct {
	description string
	document    document.Document
	changes     []document.TrackedChange
	history     *historyAvailability
	showChanges bool
	cleanupErr  error
	err         error
}
type documentChangesLoadedMsg struct {
	document document.Document
	err      error
}
type documentPDFExportedMsg struct {
	pdfExportResult
	tracked bool
}
type imageLoadedMsg struct {
	image artifact.Image
	alt   string
	err   error
}
type documentAutosaveTickMsg struct{ session uint64 }

// documentDraftDiscardedMsg reports deleting the recovery draft after leaving
// the editor.
type documentDraftDiscardedMsg struct{ err error }
type documentAutosaveFinishedMsg struct {
	session uint64
	err     error
	// cleared reports that the draft was deleted because the editor was clean.
	cleared bool
	state   documentAutosaveState
}

// documentRecoveryData is the autosaved document editor state: the saved
// document the edits started from and the unsaved draft. Embedded images are
// stored once in Images; the snapshot and draft refer to them by index.
type documentRecoveryData struct {
	Snapshot       document.Document          `json:"snapshot"`
	SnapshotBlocks map[string]json.RawMessage `json:"snapshot_blocks,omitempty"`
	Draft          document.Draft             `json:"draft"`
	Images         []artifact.Image           `json:"images,omitempty"`
	SnapshotImages []int                      `json:"snapshot_images,omitempty"`
	DraftImages    []int                      `json:"draft_images,omitempty"`
}

// documentAutosaveState is the editor state written by an autosave, used to
// skip rewriting an unchanged draft.
type documentAutosaveState struct {
	snapshot document.Document
	draft    document.Draft
}

func (a documentAutosaveState) equal(b documentAutosaveState) bool {
	return document.Equal(a.snapshot, b.snapshot) && a.draft.Body == b.draft.Body &&
		a.draft.Layout == b.draft.Layout && sameImages(a.draft.Images, b.draft.Images)
}

func sameImages(left, right []artifact.Image) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Name != right[i].Name || !bytes.Equal(left[i].Data, right[i].Data) {
			return false
		}
	}
	return true
}

func poolDocumentImage(images []artifact.Image, image artifact.Image) ([]artifact.Image, int) {
	for i, pooled := range images {
		if pooled.Name == image.Name && bytes.Equal(pooled.Data, image.Data) {
			return images, i
		}
	}
	images = append(images, image)
	return images, len(images) - 1
}

func poolDocumentImages(images, newImages []artifact.Image) ([]artifact.Image, []int) {
	var refs []int
	var ref int
	for _, image := range newImages {
		images, ref = poolDocumentImage(images, image)
		refs = append(refs, ref)
	}
	return images, refs
}

func newDocumentRecoveryData(state documentAutosaveState) documentRecoveryData {
	data := documentRecoveryData{
		Snapshot: state.snapshot, SnapshotBlocks: cloneRawMessages(state.snapshot.Blocks),
		Draft: state.draft,
	}
	data.Snapshot.Images, data.Draft.Images = nil, nil
	data.Images, data.SnapshotImages = poolDocumentImages(data.Images, state.snapshot.Images)
	data.Images, data.DraftImages = poolDocumentImages(data.Images, state.draft.Images)
	return data
}

func expandDocumentImages(images []artifact.Image, refs []int) ([]artifact.Image, error) {
	var out []artifact.Image
	for _, ref := range refs {
		if ref < 0 || ref >= len(images) {
			return nil, fmt.Errorf("invalid image reference %d", ref)
		}
		out = append(out, images[ref])
	}
	return out, nil
}

// expand returns the snapshot and draft with their images restored.
func (d documentRecoveryData) expand() (document.Document, document.Draft, error) {
	snapshot, draft := d.Snapshot, d.Draft
	var err error
	if snapshot.Images, err = expandDocumentImages(d.Images, d.SnapshotImages); err != nil {
		return document.Document{}, document.Draft{}, err
	}
	if draft.Images, err = expandDocumentImages(d.Images, d.DraftImages); err != nil {
		return document.Document{}, document.Draft{}, err
	}
	snapshot.Blocks = cloneRawMessages(d.SnapshotBlocks)
	return snapshot, draft, nil
}

func (documentSavedMsg) isDocumentMessage()            {}
func (documentReloadedMsg) isDocumentMessage()         {}
func (documentChangesLoadedMsg) isDocumentMessage()    {}
func (documentPDFExportedMsg) isDocumentMessage()      {}
func (imageLoadedMsg) isDocumentMessage()              {}
func (documentAutosaveTickMsg) isDocumentMessage()     {}
func (documentAutosaveFinishedMsg) isDocumentMessage() {}
func (documentDraftDiscardedMsg) isDocumentMessage()   {}

type editorFocus int

const (
	focusBody editorFocus = iota
	focusToolbar
	focusOutline
)

type promptKind int

const (
	promptNone promptKind = iota
	promptLink
	promptImage
	promptHeader
	promptFooter
	promptSection
	promptPDF
	promptPDFOverwrite
)

// documentChangesState holds state for reviewing tracked document changes.
type documentChangesState struct {
	showChanges    bool
	reviewing      bool
	changes        []document.TrackedChange
	reviewSnapshot document.Document
	selectedChange int
	changeReview   viewport.Model
}

// documentEditorState holds state for editing a document.
type documentEditorState struct {
	snapshot       document.Document
	body           editor.Model
	layout         document.Layout
	images         []artifact.Image
	original       editorDraft
	focus          editorFocus
	toolbarIndex   int
	prompt         promptKind
	promptInput    textinput.Model
	previewing     bool
	editPages      []document.Page
	editLinks      []linkHitBox
	editPage       int
	previewMatches []renderedMatch
	previewMatch   int
	// editPageLine is the first page line shown by the print preview.
	editPageLine   int
	discardWarning bool
}

// documentReaderState holds state for reading/viewing a document.
type documentReaderState struct {
	// reading reports that the reader, not the editor, is shown.
	reading       bool
	reader        viewport.Model
	readerPages   []document.Page
	readerLinks   []linkHitBox
	readerPage    int
	readerMatches []renderedMatch
	readerMatch   int
	outline       []document.Section
}

// documentsScreen edits one document file and reviews its tracked changes.
// It owns UI state only; document rules live in the document service.
type documentsScreen struct {
	imageState     documentImageState
	service        *document.Service
	path           string
	width, height  int
	pending        bool
	outlineVisible bool
	canUndo        bool
	canRedo        bool
	status         string
	errMessage     string

	theme               Theme
	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc
	recoveryStore       recovery.Store
	pdfStore            PDFExportStore
	lastPDFPath         string
	pdfOverwritePath    string
	find                findBar
	autosaveSession     uint64
	autosaveCancel      context.CancelFunc
	// draftStored reports that a recovery draft for this file may exist.
	draftStored bool
	// lastAutosave is the state most recently written as a recovery draft.
	lastAutosave      *documentAutosaveState
	autosaveScheduler func(uint64) tea.Cmd

	documentChangesState
	documentEditorState
	documentReaderState
}

type editorDraft struct {
	body, images string
	layout       document.Layout
}

func newDocumentEditorState(undoLimit int, theme Theme) documentEditorState {
	body := editor.NewWithUndoLimit(undoLimit)
	body.SetTheme(theme)
	body.Placeholder = "Write Markdown…"
	prompt := textinput.New()
	prompt.CharLimit = document.MaxRunningTextLength * 2
	return documentEditorState{
		body:        body,
		promptInput: prompt,
		layout:      document.DefaultLayout(),
	}
}

func newDocumentChangesState() documentChangesState {
	return documentChangesState{
		changeReview: viewport.New(0, 0),
	}
}

func newDocumentReaderState() documentReaderState {
	return documentReaderState{
		reader: viewport.New(0, 0),
	}
}

func newDocumentsScreen(service *document.Service, path string, newContext func() (context.Context, context.CancelFunc), theme Theme, undoLimit int) *documentsScreen {
	return &documentsScreen{
		theme:                theme,
		service:              service,
		path:                 path,
		find:                 newFindBar(theme),
		documentEditorState:  newDocumentEditorState(undoLimit, theme),
		documentChangesState: newDocumentChangesState(),
		documentReaderState:  newDocumentReaderState(),
		newOperationContext:  newContext,
	}
}

func (s *documentsScreen) resize(width, height int) {
	if s.width != width || s.height != height {
		s.imageState.frame++
	}
	s.width, s.height = width, height
	s.resizeChangeReview()
	s.layoutEditor()
	if s.reading {
		s.resizeReader()
	}
}

// resumeAutosave restarts autosave when the editor is open.
func (s *documentsScreen) resumeAutosave() tea.Cmd {
	if s.reading {
		return nil
	}
	s.startDocumentAutosaveSession()
	return s.scheduleDocumentAutosave(s.autosaveSession)
}

func (s *documentsScreen) update(message tea.Msg) tea.Cmd {
	previous := s.documentImageFrame()
	cmd := s.updateDocument(message)
	s.invalidateDocumentImages(previous)
	return cmd
}

func (s *documentsScreen) updateDocument(message tea.Msg) tea.Cmd {
	switch msg := message.(type) {
	case documentDraftDiscardedMsg:
		s.draftDiscarded(msg)
	case documentAutosaveTickMsg:
		if msg.session == s.autosaveSession {
			return s.saveDocumentRecovery(msg.session)
		}
	case documentAutosaveFinishedMsg:
		return s.autosaveFinished(msg)
	case documentSavedMsg:
		return s.documentSaved(msg)
	case documentReloadedMsg:
		return s.documentReloaded(msg)
	case documentChangesLoadedMsg:
		s.documentChangesLoaded(msg)
		return nil
	case documentPDFExportedMsg:
		return s.documentPDFExported(msg)
	case imageLoadedMsg:
		return s.imageLoaded(msg)
	case tea.KeyMsg:
		cmd := s.updateKey(msg)
		s.layoutEditor()
		return cmd
	case tea.MouseMsg:
		if s.reading && !s.pending && !s.showChanges {
			s.updateReaderMouse(msg)
			return nil
		}
		cmd := s.updateMouse(msg)
		s.layoutEditor()
		return cmd
	}
	if s.find.active && s.prompt == promptNone {
		switch {
		case s.reading:
			return s.updateReaderFindKey(message)
		case s.previewing:
			return s.updatePreviewFindKey(message)
		default:
			return s.updateFindKey(message)
		}
	}
	return nil
}

func (s *documentsScreen) autosaveFinished(msg documentAutosaveFinishedMsg) tea.Cmd {
	if msg.session != s.autosaveSession {
		return nil
	}
	if s.autosaveCancel != nil {
		s.autosaveCancel()
		s.autosaveCancel = nil
	}
	switch {
	case msg.err != nil:
	case msg.cleared:
		s.draftStored, s.lastAutosave = false, nil
	default:
		state := msg.state
		s.lastAutosave = &state
	}
	if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
		s.errMessage = "Autosave failed: " + msg.err.Error()
	}
	return s.scheduleDocumentAutosave(msg.session)
}

func (s *documentsScreen) documentSaved(msg documentSavedMsg) tea.Cmd {
	s.finishOperation()
	s.pending = false
	s.applyHistoryAvailability(msg.history)
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) {
			s.errMessage, s.status = "", "Save cancelled; unsaved changes remain"
		} else {
			s.errMessage = msg.err.Error()
		}
		return s.resumeAutosave()
	}
	s.body.ResetHistory()
	s.markSaved(msg.document)
	s.errMessage, s.status = "", "Saved “"+msg.document.Title+"”"
	if msg.cleanupErr != nil {
		s.status += " (could not remove recovery draft: " + msg.cleanupErr.Error() + ")"
	}
	s.applyHistoryAvailability(msg.history)
	return s.resumeAutosave()
}

func (s *documentsScreen) documentReloaded(msg documentReloadedMsg) tea.Cmd {
	s.finishOperation()
	s.pending = false
	s.applyHistoryAvailability(msg.history)
	if msg.err != nil {
		s.errMessage = msg.err.Error()
		return s.resumeAutosave()
	}
	if !s.dirty() || msg.showChanges {
		s.loadDocument(msg.document)
	} else {
		s.snapshot = msg.document
	}
	if s.reading {
		s.refreshReader()
	}
	s.changes = msg.changes
	s.reviewSnapshot = msg.document
	s.selectedChange = max(min(s.selectedChange, len(s.changes)-1), 0)
	if msg.showChanges {
		s.showChanges, s.reviewing = true, false
	}
	if len(s.changes) == 0 {
		s.reviewing = false
	} else if s.reviewing {
		s.setChangeReviewContent()
	}
	s.errMessage, s.status = "", msg.description
	if msg.cleanupErr != nil {
		s.status += " (could not remove recovery draft: " + msg.cleanupErr.Error() + ")"
	}
	s.applyHistoryAvailability(msg.history)
	return s.resumeAutosave()
}

func (s *documentsScreen) documentChangesLoaded(msg documentChangesLoadedMsg) {
	s.finishOperation()
	s.pending = false
	if msg.err != nil {
		s.errMessage = msg.err.Error()
		return
	}
	s.changes = msg.document.TrackedChanges
	s.reviewSnapshot = msg.document
	s.selectedChange = max(min(s.selectedChange, len(s.changes)-1), 0)
	s.showChanges, s.reviewing = true, false
	s.errMessage = ""
}

func (s *documentsScreen) imageLoaded(msg imageLoadedMsg) tea.Cmd {
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
	s.insertBlock(artifact.ImageMarkdown(msg.alt, msg.image.Name))
	s.status = "Inserted image " + msg.image.Name
	cmd := s.focusBody()
	s.layoutEditor()
	return cmd
}

func (s *documentsScreen) exportPDF(path string, overwrite bool) tea.Cmd {
	path = strings.TrimSpace(path)
	if path == "" {
		s.errMessage = "PDF output path is required"
		return nil
	}
	path, err := filepath.Abs(path)
	if err != nil {
		s.errMessage = "PDF output path: " + err.Error()
		return nil
	}
	if s.pdfStore == nil {
		s.errMessage = "PDF export storage is unavailable"
		return nil
	}
	d := s.snapshot
	tracked := len(s.snapshot.TrackedChanges) > 0 ||
		(!s.reading && s.snapshot.Tracking && s.body.Value() != s.snapshot.Body)
	if !s.reading {
		d = s.editorDocument()
	}
	ctx := s.startOperation()
	s.pending = true
	s.status, s.errMessage = "Exporting PDF…", ""
	store, source := s.pdfStore, s.path
	return func() tea.Msg {
		return documentPDFExportedMsg{pdfExportResult: writePDF(ctx, store, path, source, d, overwrite), tracked: tracked}
	}
}

func (s *documentsScreen) documentPDFExported(msg documentPDFExportedMsg) tea.Cmd {
	s.finishOperation()
	s.pending = false
	switch {
	case errors.Is(msg.err, context.Canceled):
		s.errMessage, s.status = "", "PDF export cancelled"
	case errors.Is(msg.err, fs.ErrExist):
		s.errMessage, s.status = "", ""
		s.pdfOverwritePath = msg.path
		return s.openPrompt(promptPDFOverwrite, "PDF exists. Overwrite? [y/N] ", "")
	case msg.err != nil:
		s.errMessage, s.status = "PDF export failed: "+msg.err.Error(), ""
	default:
		s.lastPDFPath = msg.path
		s.errMessage, s.status = "", "PDF exported to "+msg.path
		if msg.tracked {
			s.status += " (pending tracked changes not exported)"
		}
		if msg.warning != nil {
			s.status += " (could not remember PDF path: " + msg.warning.Error() + ")"
		}
	}
	return s.resumeAutosave()
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
	if s.reading {
		return s.updateReaderKey(msg)
	}
	return s.updateEditorKey(msg)
}

// loadChanges opens the list of pending tracked changes.
func (s *documentsScreen) loadChanges() tea.Cmd {
	s.pending = true
	ctx := s.startOperation()
	service, path := s.service, s.path
	return func() tea.Msg {
		d, err := service.Get(ctx, path)
		return documentChangesLoadedMsg{document: d, err: err}
	}
}

// reload runs an operation that changes the file, then reads the document and
// its tracked changes back.
func (s *documentsScreen) reload(showChanges bool, run func(context.Context) (string, error)) tea.Cmd {
	s.stopDocumentAutosave()
	s.pending = true
	s.errMessage = ""
	ctx := s.startOperation()
	service, path := s.service, s.path
	return func() tea.Msg {
		description, err := run(ctx)
		if err != nil {
			return documentReloadedMsg{
				history: queryHistoryAvailability(ctx, service.HistoryAvailability),
				err:     err,
			}
		}
		d, err := service.Get(ctx, path)
		if err != nil {
			return documentReloadedMsg{
				history: queryHistoryAvailability(ctx, service.HistoryAvailability),
				err:     err,
			}
		}
		return documentReloadedMsg{
			description: description, document: d, changes: d.TrackedChanges,
			history:     queryHistoryAvailability(ctx, service.HistoryAvailability),
			showChanges: showChanges, err: err,
		}
	}
}

func (s *documentsScreen) updateChangesKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	if s.reviewing {
		switch key {
		case "esc", "left", "q":
			s.reviewing = false
		case "up", "down", "j", "k", "pgup", "pgdown", "home", "end":
			s.changeReview, _ = s.changeReview.Update(msg)
		case "a", "r":
			change, ok := s.selectedChangeValue()
			if !ok {
				break
			}
			if s.dirty() {
				s.status = "Save or discard your edits before reviewing changes"
				return nil
			}
			return s.resolveTracked(change.ID, key == "a")
		}
		return nil
	}
	switch key {
	case "esc", "left", "f4", "v", "q":
		s.showChanges = false
		if s.reading {
			return nil
		}
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
	case "A", "R":
		if s.dirty() {
			s.status = "Save or discard your edits before reviewing changes"
			return nil
		}
		return s.resolveTracked("", key == "A")
	}

	return nil
}

func (s *documentsScreen) resolveTracked(id string, accept bool) tea.Cmd {
	service, snapshot := s.service, s.reviewSnapshot
	return s.reload(false, func(ctx context.Context) (string, error) {
		_, err := service.ReviewTracked(ctx, snapshot, id, accept)
		if accept {
			return "Tracked change accepted", err
		}
		return "Tracked change rejected", err
	})
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
	content := fmt.Sprintf("%s  ·  pending  ·  byte %d\n\n%s",
		change.ID, change.Offset, sanitizeTerminalText(document.TrackingSummary(change)))
	if change.Group != "" {
		content += "\n\nLinked image move (reviewed together):"
		for _, linked := range s.changes {
			if linked.ID != change.ID && linked.Group == change.Group {
				content += "\n\n" + sanitizeTerminalText(document.TrackingSummary(linked))
			}
		}
	}
	s.changeReview.SetContent(wrapReviewText(content, max(s.changeReview.Width, 1)))
	s.changeReview.GotoTop()
}

func wrapReviewText(content string, width int) string {
	var wrapped strings.Builder
	wrapped.Grow(len(content) + len(content)/16)
	remaining := content
	lineIndex := 0
	for {
		var line string
		idx := strings.IndexByte(remaining, '\n')
		if idx >= 0 {
			line = remaining[:idx]
			remaining = remaining[idx+1:]
		} else {
			line = remaining
		}

		if lineIndex > 0 {
			wrapped.WriteByte('\n')
		}
		lineIndex++

		lineWidth := 0
		for i := 0; i < len(line); {
			b := line[i]
			if b >= ' ' && b <= '~' {
				if lineWidth > 0 && lineWidth+1 > width {
					wrapped.WriteByte('\n')
					lineWidth = 0
				}
				wrapped.WriteByte(b)
				lineWidth++
				i++
				continue
			}

			r, size := utf8.DecodeRuneInString(line[i:])
			runeWidth := max(runewidth.RuneWidth(r), 0)
			if lineWidth > 0 && lineWidth+runeWidth > width {
				wrapped.WriteByte('\n')
				lineWidth = 0
			}
			wrapped.WriteRune(r)
			lineWidth += runeWidth
			i += size
		}

		if idx < 0 {
			break
		}
	}
	return wrapped.String()
}

func (s *documentsScreen) selectedChangeValue() (document.TrackedChange, bool) {
	if s.selectedChange < 0 || s.selectedChange >= len(s.changes) {
		return document.TrackedChange{}, false
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

func (s *documentsScreen) applyHistoryAvailability(availability *historyAvailability) {
	if availability == nil {
		return
	}
	if availability.err != nil {
		if !errors.Is(availability.err, context.Canceled) {
			s.status = "History unavailable: " + availability.err.Error()
		}
		return
	}
	s.canUndo, s.canRedo = availability.canUndo, availability.canRedo
}

func pageText(page document.Page, width int) string {
	lines := make([]string, len(page.Lines))
	for i, line := range page.Lines {
		lines[i] = ansi.Truncate(line, max(width, 1), "")
	}
	return strings.Join(lines, "\n")
}

// view renders the document screen below the shared header.
func (s *documentsScreen) view(header string) string {
	view := s.renderDocumentImages(s.documentView(header), max(s.width, 1))
	if s.imageState.graphicsUsed && termimg.DetectProtocol() != termimg.ProtocolNone {
		view = repaintImageFrame(view, s.imageState.frame)
	}
	return s.clearDocumentImages() + view
}

func (s *documentsScreen) documentView(header string) string {
	if s.showChanges {
		return s.viewChanges(header)
	}
	if s.reading {
		return s.readerView(header)
	}
	return s.editorView(header)
}

func (s *documentsScreen) viewChanges(header string) string {
	var b strings.Builder
	b.WriteString(header + "\n\nTracked changes to this document\n")
	if s.reviewing {
		change, ok := s.selectedChangeValue()
		if ok {
			b.WriteString(s.changeReview.View())
			b.WriteString("\n\n")
			_ = change
			b.WriteString("a accept  ·  r reject  ·  ↑/↓ scroll  ·  PgUp/PgDn  ·  Esc/q return")
		}
	} else {
		if len(s.changes) == 0 {
			b.WriteString("\nNo pending changes.\n")
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
			fmt.Fprintf(&b, "%spending  %s  byte %d\n", marker, change.ID, change.Offset)
		}
		back := "the editor"
		if s.reading {
			back = "the reader"
		}
		b.WriteString("\n↑/↓ select  Enter review  A accept all  R reject all  ·  Esc/q returns to " + back)
	}
	return b.String() + "\n" + s.statusLine()
}

func (s *documentsScreen) statusLine() string {
	undo, redo := "undo unavailable", "redo unavailable"
	if s.canUndo || s.body.CanUndo() {
		undo = "undo available"
	}
	if s.canRedo || s.body.CanRedo() {
		redo = "redo available"
	}
	message := s.status
	if s.errMessage != "" {
		message = "Error: " + s.errMessage
	}
	if message == "" {
		message = "Ready"
	}
	return sanitizeTerminalLine(message) + "  ·  " + undo + "  ·  " + redo
}

func (s *documentsScreen) dirty() bool { return s.currentDraft() != s.original }

func (s *documentsScreen) currentDraft() editorDraft {
	return editorDraft{
		body: s.body.Value(), layout: s.layout, images: imageKey(s.images),
	}
}

func imageKey(images []artifact.Image) string {
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
		img, err := artifact.NewImage(data)
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
