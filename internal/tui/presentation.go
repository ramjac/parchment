package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/recovery"
	"example.com/parchment/internal/tui/editor"
	"example.com/parchment/internal/tui/termimg"
)

// presentationChromeLines counts the header, status, and help lines.
const presentationChromeLines = 3

const presentationTransitionFPS = 60

type presentationAutosaveTickMsg struct{ session uint64 }

type presentationTransitionTickMsg struct{ generation uint64 }

type presentationAutosaveFinishedMsg struct {
	session uint64
	err     error
	cleared bool
}

type presentationLoadedMsg struct {
	seq  int
	item presentation.Presentation
	err  error
}

type presentationSavedMsg struct {
	seq  int
	item presentation.Presentation
	err  error
}

type presentationHistoryMsg struct {
	seq         int
	description string
	err         error
}

type presentationOpState struct {
	pending             bool
	seq                 int
	status              string
	errMessage          string
	loadedStatus        string
	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc
}

type presentationViewState struct {
	page          int
	scroll        int
	showNotes     bool
	width, height int
	searchMatches []renderedMatch
	searchMatch   int
}

type presentationEditorState struct {
	editing        bool
	editor         editor.Model
	discardWarning bool
}

type renderedPresentationImage struct {
	output string
	pixels *image.NRGBA
	cols   int
	rows   int
}

type presentationImageRow struct {
	image renderedPresentationImage
	row   int
}

type presentationImageSlot struct {
	name   string
	height string
	width  string
}

type presentationImageViewState struct {
	page, scroll, width, height int
	showNotes                   bool
	pdfPrompt                   promptKind
	revision                    uint64
}

type presentationTransitionState struct {
	from       string
	spring     harmonica.Spring
	position   float64
	velocity   float64
	generation uint64
	direction  int
	active     bool
}

// presentationModel is a single-deck slide viewer and Markdown source editor.
// It owns only UI state; it reads and edits the deck file through the
// presentation service.
type presentationModel struct {
	service *presentation.Service
	theme   Theme
	path    string
	item    presentation.Presentation
	deck    presentation.Deck
	loaded  bool

	recoveryStore       recovery.Store
	autosaveScheduler   func(session uint64) tea.Cmd
	transitionScheduler func(generation uint64) tea.Cmd
	autosaveSession     uint64
	autosaveCancel      context.CancelFunc
	draftStored         bool
	pdfStore            PDFExportStore
	lastPDFPath         string
	pdfPrompt           promptKind
	pdfInput            textinput.Model
	pdfOverwritePath    string
	pdfPending          bool
	pdfTwoUp            bool
	find                findBar
	imageRenderCache    map[string]renderedPresentationImage
	imageRowMarkers     map[rune]presentationImageRow
	nextImageMarker     rune
	imageGraphicsUsed   bool
	imageFrame          uint64
	imageRevision       uint64

	presentationOpState
	presentationViewState
	presentationEditorState
	presentationTransitionState
}

func newPresentationModel(service *presentation.Service, path string, theme Theme, undoLimits ...int) *presentationModel {
	undoLimit := 100
	if len(undoLimits) > 0 {
		undoLimit = undoLimits[0]
	}
	body := editor.NewWithUndoLimit(undoLimit)
	body.SetTheme(theme)
	pdfInput := textinput.New()
	pdfInput.CharLimit = 64 << 10
	return &presentationModel{
		service:  service,
		path:     path,
		theme:    theme,
		pdfInput: pdfInput,
		find:     newFindBar(theme),
		transitionScheduler: func(generation uint64) tea.Cmd {
			return tea.Tick(time.Second/presentationTransitionFPS, func(time.Time) tea.Msg {
				return presentationTransitionTickMsg{generation: generation}
			})
		},
		imageRenderCache: make(map[string]renderedPresentationImage),
		presentationEditorState: presentationEditorState{
			editor: body,
		},
		presentationOpState: presentationOpState{
			newOperationContext: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
		},
		presentationViewState: presentationViewState{searchMatch: -1},
	}
}

func (m *presentationModel) Init() tea.Cmd { return m.load() }

func (m *presentationModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	previous := m.imageViewState()
	model, cmd := m.updatePresentation(message)
	if previous != m.imageViewState() {
		m.imageFrame++
	}
	return model, cmd
}

func (m *presentationModel) updatePresentation(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.cancelPageTransition()
		m.width, m.height = msg.Width, msg.Height
		m.editor.SetWidth(max(1, m.width-4))
		m.resizePDFPrompt()
		if !m.editing && m.find.visible && m.find.query() != "" {
			current := m.searchMatch
			m.rebuildSlideMatches(m.find.query())
			m.searchMatch = min(max(current, 0), len(m.searchMatches)-1)
		}
		m.clampScroll()
		return m, nil
	case presentationTransitionTickMsg:
		return m, m.updatePageTransition(msg)
	case presentationPDFExportedMsg:
		return m, m.presentationPDFExported(msg)
	case presentationLoadedMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.finishOperation()
		if msg.err != nil {
			m.setError("Load failed", msg.err)
			return m, nil
		}
		m.setItem(msg.item)
		m.status, m.loadedStatus = m.loadedStatus, ""
		return m, nil
	case presentationSavedMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.finishOperation()
		if msg.err != nil {
			// Keep the editor open so the draft is not lost.
			m.setError("Save failed", msg.err)
			m.startAutosaveSession()
			return m, m.scheduleAutosave(m.autosaveSession)
		}
		if m.editing {
			m.editor.ResetHistory()
			m.leaveEditor()
		}
		m.setItem(msg.item)
		m.status = "Saved"
		if m.recoveryStore != nil {
			if cleanupErr := m.recoveryStore.DeleteRecovery(context.Background(), m.path); cleanupErr != nil {
				m.status += " (could not remove recovery draft: " + cleanupErr.Error() + ")"
			}
			m.draftStored = false
		}
		return m, nil
	case presentationAutosaveTickMsg:
		if msg.session == m.autosaveSession && m.editing {
			return m, m.saveRecovery(msg.session)
		}
		return m, nil
	case presentationAutosaveFinishedMsg:
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
			if m.editing {
				return m, m.scheduleAutosave(msg.session)
			}
		}
		return m, nil
	case presentationHistoryMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.finishOperation()
		if msg.err != nil {
			m.setError("History failed", msg.err)
			return m, nil
		}
		cmd := m.load()
		m.loadedStatus = msg.description
		return m, cmd
	case tea.KeyPressMsg:
		var cmd tea.Cmd
		if m.pdfPrompt != promptNone {
			cmd = m.updatePDFPrompt(msg)
		} else if m.editing {
			cmd = m.updateEditKey(msg)
		} else if m.find.active {
			cmd = m.updateFindKey(msg)
		} else {
			cmd = m.updateBrowseKey(msg)
		}
		return m, cmd
	case mouseEvent:
		if m.editing && !m.pending && m.pdfPrompt == promptNone {
			mouseMsg := msg
			mouseMsg.Y = msg.Y - 2
			mouseMsg.X = msg.X - 2
			var cmd tea.Cmd
			m.editor, cmd = m.editor.Update(toTeaMouseMessage(mouseMsg))
			return m, cmd
		}
		return m, nil
	}
	if m.find.active && m.pdfPrompt == promptNone {
		return m, m.updateFindKey(message)
	}
	if m.editing {
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(message)
		return m, cmd
	}
	return m, nil
}

func (m *presentationModel) updateEditKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.find.active {
		return m.updateFindKey(msg)
	}
	if key == "ctrl+c" {
		if m.editor.Selection.Active {
			var cmd tea.Cmd
			m.editor, cmd = m.editor.Update(msg)
			return cmd
		}
		m.cancelPending()
		return tea.Quit
	}
	if m.pending {
		if key == "esc" {
			pdfPending := m.pdfPending
			m.cancelPending()
			m.status, m.errMessage = "Save cancelled", ""
			if pdfPending {
				m.status = "PDF export cancelled"
			}
		} else {
			m.status = "Saving… (esc cancels)"
			if m.pdfPending {
				m.status = "Exporting PDF… (esc cancels)"
			}
		}
		return nil
	}
	if key == "ctrl+f" {
		m.editor.Blur()
		m.editor.SetSearch(m.find.query())
		return m.find.open(m.width)
	}
	switch key {
	case "ctrl+p":
		return m.openPDFPrompt()
	case "ctrl+s":
		return m.save()
	case "ctrl+z", "ctrl+r", "ctrl+y", "ctrl+shift+z":
		if key == "ctrl+z" && m.editor.CanUndo() {
			m.editor.Undo()
			return nil
		}
		if (key == "ctrl+r" || key == "ctrl+y" || key == "ctrl+shift+z") && m.editor.CanRedo() {
			m.editor.Redo()
			return nil
		}
		if key == "ctrl+z" {
			return m.history("Undid", m.service.Undo)
		}
		return m.history("Redid", m.service.Redo)
	case "esc":
		if m.editor.Value() != m.item.Source && !m.discardWarning {
			m.discardWarning = true
			m.status, m.errMessage = "Unsaved changes: esc again discards, ctrl+s saves", ""
			return nil
		}
		m.leaveEditor()
		m.status, m.errMessage = "Edit cancelled", ""
		return nil
	}
	m.discardWarning = false
	var cmd tea.Cmd
	m.editor, cmd = m.editor.Update(msg)
	return cmd
}

func (m *presentationModel) updateFindKey(msg tea.Msg) tea.Cmd {
	action, cmd := m.find.update(msg)
	if !m.editing {
		var transitionCmd tea.Cmd
		switch action {
		case findChanged:
			transitionCmd = m.searchSlides(m.find.query())
		case findNext:
			transitionCmd = m.moveSearchMatch(1)
			m.find.blur()
		case findPrevious:
			transitionCmd = m.moveSearchMatch(-1)
			m.find.blur()
		case findClosed:
			transitionCmd = m.searchSlides("")
		}
		return tea.Batch(cmd, transitionCmd)
	}
	switch action {
	case findChanged:
		m.editor.SetSearch(m.find.query())
	case findNext:
		m.editor.MoveToMatch(1)
	case findPrevious:
		m.editor.MoveToMatch(-1)
	case findClosed:
		m.editor.ClearSearch()
		return tea.Batch(cmd, m.editor.Focus())
	}
	return cmd
}

func (m *presentationModel) updateBrowseKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	if m.active && !isPresentationPageNavigationKey(key, m.find.visible) {
		m.cancelPageTransition()
	}
	if m.find.visible && !m.pending {
		switch key {
		case "n":
			return m.moveSearchMatch(1)
		case "N":
			return m.moveSearchMatch(-1)
		case "esc":
			m.find.close()
			m.searchSlides("")
			return nil
		}
	}
	switch key {
	case "ctrl+c":
		m.cancelPending()
		return tea.Quit
	case "q":
		m.cancelPending()
		return m.close()
	case "esc":
		if m.pending {
			pdfPending := m.pdfPending
			m.cancelPending()
			m.status, m.errMessage = "Cancelled", ""
			if pdfPending {
				m.status = "PDF export cancelled"
			}
			return nil
		}
		m.cancelPending()
		return m.close()
	}
	if m.pending {
		m.status = "Working… (esc cancels)"
		return nil
	}
	if key == "r" {
		return m.load()
	}
	if !m.loaded {
		return nil
	}
	if key == "/" || key == "ctrl+f" {
		transitionCmd := m.searchSlides(m.find.query())
		return tea.Batch(transitionCmd, m.find.open(m.width))
	}
	switch key {
	case "ctrl+p":
		return m.openPDFPrompt()
	case "right", "l", "n", "space", "pgdown":
		return m.navigateToPage(m.page + 1)
	case "left", "h", "p", "pgup":
		return m.navigateToPage(m.page - 1)
	case "home", "g":
		return m.navigateToPage(0)
	case "end", "G":
		return m.navigateToPage(m.pageCount() - 1)
	case "down", "j":
		m.scroll++
		m.clampScroll()
	case "up", "k":
		m.scroll--
		m.clampScroll()
	case "s":
		m.showNotes = !m.showNotes
		m.clampScroll()
	case "f":
		fonts := []string{"courier", "helvetica", "times"}
		current := m.item.Font
		if current == "" {
			current = "courier"
		}
		next := fonts[0]
		for i, f := range fonts {
			if f == current {
				next = fonts[(i+1)%len(fonts)]
			}
		}
		ctx, seq := m.startOperation()
		m.status, m.errMessage = "Saving…", ""
		service, path := m.service, m.path
		return func() tea.Msg {
			item, err := service.SetFont(ctx, path, next)
			return presentationSavedMsg{seq: seq, item: item, err: err}
		}
	case "enter", "e":
		return m.beginEdit()
	case "u", "ctrl+z":
		return m.history("Undid", m.service.Undo)
	case "ctrl+r", "ctrl+y", "ctrl+shift+z":
		return m.history("Redid", m.service.Redo)
	}
	return nil
}

// pageCount includes the title page followed by one page per slide.
func (m *presentationModel) pageCount() int { return len(m.deck.Slides) + 1 }

func (m *presentationModel) goToPage(page int) {
	m.cancelPageTransition()
	page = min(max(page, 0), m.pageCount()-1)
	if page != m.page {
		m.page, m.scroll = page, 0
	}
}

func isPresentationPageNavigationKey(key string, searching bool) bool {
	switch key {
	case "right", "l", "n", "space", "pgdown", "left", "h", "p", "pgup", "home", "g", "end", "G":
		return true
	case "N":
		return searching
	default:
		return false
	}
}

func (m *presentationModel) navigateToPage(page int) tea.Cmd {
	page = min(max(page, 0), m.pageCount()-1)
	if page == m.page {
		return nil
	}

	fromPage := m.page
	outgoing := ""
	// Terminal image placements cannot be composited safely as text frames.
	animate := !m.editing && m.loaded && m.width >= 40 && m.height >= 10 &&
		!m.pageHasImageDirectiveAt(fromPage) && !m.pageHasImageDirectiveAt(page)
	if animate {
		outgoing = m.renderSlideContent()
	}

	m.goToPage(page)
	if !animate {
		return nil
	}
	m.startPageTransition(outgoing, page-fromPage)
	return m.schedulePageTransition(m.generation)
}

func (m *presentationModel) startPageTransition(outgoing string, direction int) {
	m.generation++
	m.from = outgoing
	m.spring = harmonica.NewSpring(harmonica.FPS(presentationTransitionFPS), 12, 1)
	m.position, m.velocity = 0, 0
	m.direction = 1
	if direction < 0 {
		m.direction = -1
	}
	m.active = true
}

func (m *presentationModel) cancelPageTransition() {
	if !m.active {
		return
	}
	m.active = false
	m.from = ""
	m.generation++
}

func (m *presentationModel) schedulePageTransition(generation uint64) tea.Cmd {
	if m.transitionScheduler != nil {
		return m.transitionScheduler(generation)
	}
	return nil
}

func (m *presentationModel) updatePageTransition(msg presentationTransitionTickMsg) tea.Cmd {
	if !m.active || msg.generation != m.generation {
		return nil
	}
	m.position, m.velocity = m.spring.Update(m.position, m.velocity, 1)
	if 1-m.position < 0.01 && math.Abs(m.velocity) < 0.05 {
		m.position, m.velocity = 1, 0
		m.active = false
		m.from = ""
		m.generation++
		return nil
	}
	return m.schedulePageTransition(msg.generation)
}

func (m *presentationModel) searchSlides(query string) tea.Cmd {
	m.rebuildSlideMatches(query)
	if len(m.searchMatches) > 0 {
		m.searchMatch = 0
		return m.gotoSearchMatch()
	} else {
		m.clampScroll()
		return nil
	}
}

func (m *presentationModel) rebuildSlideMatches(query string) {
	m.searchMatches = nil
	m.searchMatch = -1
	if !m.loaded || query == "" {
		m.clampScroll()
		return
	}
	page, showNotes, scroll := m.page, m.showNotes, m.scroll
	for currentPage := 0; currentPage < m.pageCount(); currentPage++ {
		m.page, m.showNotes = currentPage, true
		lines := m.pageLines()
		notesRow := -1
		for row, line := range lines {
			if strings.Contains(ansi.Strip(line), "Speaker notes") {
				notesRow = row
				break
			}
		}
		for _, match := range renderedMatches(lines, query, currentPage) {
			match.inNotes = notesRow >= 0 && match.row > notesRow
			m.searchMatches = append(m.searchMatches, match)
		}
	}
	m.page, m.showNotes, m.scroll = page, showNotes, scroll
}

func (m *presentationModel) gotoSearchMatch() tea.Cmd {
	if m.searchMatch < 0 || m.searchMatch >= len(m.searchMatches) {
		return nil
	}
	match := m.searchMatches[m.searchMatch]
	if match.page < 0 || match.page >= m.pageCount() {
		m.searchMatches, m.searchMatch = nil, -1
		return nil
	}
	transitionCmd := m.navigateToPage(match.page)
	if match.inNotes {
		m.showNotes = true
	}
	m.scroll = max(match.row-m.visibleLines()/2, 0)
	m.clampScroll()
	return transitionCmd
}

func (m *presentationModel) moveSearchMatch(step int) tea.Cmd {
	if len(m.searchMatches) == 0 {
		return nil
	}
	m.searchMatch = (m.searchMatch + step) % len(m.searchMatches)
	if m.searchMatch < 0 {
		m.searchMatch += len(m.searchMatches)
	}
	return m.gotoSearchMatch()
}

func (m *presentationModel) beginEdit() tea.Cmd {
	m.editing, m.discardWarning = true, false
	m.status, m.errMessage = "", ""
	m.editor.SetValue(m.item.Source)
	m.moveEditorToLine(slideSourceLine(m.item.Source, m.page))
	m.startAutosaveSession()
	return tea.Batch(m.editor.Focus(), m.scheduleAutosave(m.autosaveSession))
}

func (m *presentationModel) restoreDraft(source string) tea.Cmd {
	m.editing, m.discardWarning = true, false
	m.errMessage = ""
	m.draftStored = true
	m.editor.SetValue(source)
	m.moveEditorToLine(slideSourceLine(source, m.page))
	m.startAutosaveSession()
	return tea.Batch(m.editor.Focus(), m.scheduleAutosave(m.autosaveSession))
}

func (m *presentationModel) dirty() bool {
	return m.editing && m.editor.Value() != m.item.Source
}

func (m *presentationModel) startAutosaveSession() {
	m.autosaveSession++
	if m.autosaveCancel != nil {
		m.autosaveCancel()
		m.autosaveCancel = nil
	}
}

func (m *presentationModel) stopAutosave() {
	m.startAutosaveSession()
}

func (m *presentationModel) scheduleAutosave(session uint64) tea.Cmd {
	if m.recoveryStore == nil || m.autosaveScheduler == nil {
		return nil
	}
	return m.autosaveScheduler(session)
}

func (m *presentationModel) saveRecovery(session uint64) tea.Cmd {
	if m.recoveryStore == nil {
		return nil
	}
	if !m.dirty() {
		if !m.draftStored {
			return m.scheduleAutosave(session)
		}
		store, path := m.recoveryStore, m.path
		return func() tea.Msg {
			return presentationAutosaveFinishedMsg{
				session: session,
				err:     store.DeleteRecovery(context.Background(), path),
				cleared: true,
			}
		}
	}
	m.draftStored = true
	data := presentationRecoveryData{
		Baseline: m.item,
		Source:   m.editor.Value(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.autosaveCancel = cancel
	store := m.recoveryStore
	draft := recovery.Draft{
		Path:      m.path,
		Kind:      string(artifact.PresentationKind),
		UpdatedAt: time.Now().UTC(),
	}
	return func() tea.Msg {
		encoded, err := json.Marshal(data)
		if err == nil {
			draft.Data = encoded
			err = store.SaveRecovery(ctx, draft)
		}
		return presentationAutosaveFinishedMsg{session: session, err: err}
	}
}

func (m *presentationModel) close() tea.Cmd {
	m.cancelPageTransition()
	m.stopAutosave()
	return closeEditor(m.recoveryStore, m.path)
}

func (m *presentationModel) moveEditorToLine(line int) {
	if len(m.editor.Lines) > 0 {
		m.editor.Cursor.Line = min(max(line, 0), len(m.editor.Lines)-1)
	} else {
		m.editor.Cursor.Line = 0
	}
	m.editor.Cursor.Col = 0
	m.editor.StickyCol = 0
}

func trimLeftSpaces(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	return s
}

// slideSourceLine finds the source line of a page's heading for cursor
// placement. Page 0 is the title page; it skips headings inside fences.
func slideSourceLine(source string, page int) int {
	slide, fence := 0, ""
	index := 0
	for {
		var line string
		idx := strings.IndexByte(source, '\n')
		if idx >= 0 {
			line = source[:idx]
			source = source[idx+1:]
		} else {
			line = source
			source = ""
		}

		trimmed := trimLeftSpaces(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
		} else if len(trimmed) > 0 {
			c := trimmed[0]
			if c == '`' || c == '~' {
				if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
					fence = trimmed[:3]
				}
			} else if c == '#' {
				if page == 0 && strings.HasPrefix(trimmed, "# ") {
					return index
				} else if strings.HasPrefix(trimmed, "## ") {
					slide++
					if slide == page {
						return index
					}
				}
			}
		}

		if idx < 0 {
			break
		}
		index++
	}
	return 0
}

// pageForSourceLine is the inverse of slideSourceLine: it returns the page
// containing a source line. Lines before the first slide heading belong to
// the title page.
func pageForSourceLine(source string, line int) int {
	slide, fence := 0, ""
	index := 0
	for index <= line {
		var text string
		idx := strings.IndexByte(source, '\n')
		if idx >= 0 {
			text = source[:idx]
			source = source[idx+1:]
		} else {
			text = source
			source = ""
		}

		trimmed := trimLeftSpaces(text)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
		} else if len(trimmed) > 0 {
			c := trimmed[0]
			if c == '`' || c == '~' {
				if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
					fence = trimmed[:3]
				}
			} else if c == '#' {
				if strings.HasPrefix(trimmed, "## ") {
					slide++
				}
			}
		}

		if idx < 0 {
			break
		}
		index++
	}
	return slide
}

// leaveEditor returns to the viewer on the slide containing the cursor.
func (m *presentationModel) leaveEditor() {
	m.goToPage(pageForSourceLine(m.editor.Value(), m.editor.Cursor.Line))
	m.editing, m.discardWarning = false, false
	m.editor.Blur()
	m.stopAutosave()
	if m.draftStored && m.recoveryStore != nil {
		store, path := m.recoveryStore, m.path
		m.draftStored = false
		go func() {
			_ = store.DeleteRecovery(context.Background(), path)
		}()
	}
}

func (m *presentationModel) save() tea.Cmd {
	draft := m.editor.Value()
	if draft == m.item.Source {
		m.leaveEditor()
		m.status, m.errMessage = "No changes", ""
		return nil
	}
	if _, err := presentation.Parse(draft); err != nil {
		m.status, m.errMessage = "", "Invalid presentation: "+err.Error()
		return nil
	}
	ctx, seq := m.startOperation()
	m.status, m.errMessage = "Saving…", ""
	service, expected := m.service, m.item
	return func() tea.Msg {
		item, err := service.Update(ctx, expected, draft)
		return presentationSavedMsg{seq: seq, item: item, err: err}
	}
}

func (m *presentationModel) load() tea.Cmd {
	ctx, seq := m.startOperation()
	m.status, m.errMessage = "Loading…", ""
	service, path := m.service, m.path
	return func() tea.Msg {
		item, err := service.Get(ctx, path)
		return presentationLoadedMsg{seq: seq, item: item, err: err}
	}
}

func (m *presentationModel) history(prefix string, run func(context.Context) (string, error)) tea.Cmd {
	ctx, seq := m.startOperation()
	m.status, m.errMessage = "Working…", ""
	return func() tea.Msg {
		description, err := run(ctx)
		if err == nil {
			description = prefix + " " + strings.ToLower(description)
		}
		return presentationHistoryMsg{seq: seq, description: description, err: err}
	}
}

func (m *presentationModel) startOperation() (context.Context, int) {
	m.cancelPending()
	ctx, cancel := m.newOperationContext()
	m.cancelOperation = cancel
	m.pending = true
	m.seq++
	return ctx, m.seq
}

func (m *presentationModel) finishOperation() {
	if m.cancelOperation != nil {
		m.cancelOperation()
		m.cancelOperation = nil
	}
	m.pending = false
}

// cancelPending abandons the current operation; its late result is ignored.
func (m *presentationModel) cancelPending() {
	if m.pending {
		m.seq++
	}
	m.finishOperation()
	m.pdfPending = false
}

func (m *presentationModel) setError(prefix string, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		m.status, m.errMessage = "Cancelled", ""
	case errors.Is(err, presentation.ErrNotFound):
		m.status, m.errMessage = "", prefix+": presentation not found"
	default:
		m.status, m.errMessage = "", prefix+": "+err.Error()
	}
}

func (m *presentationModel) setItem(item presentation.Presentation) {
	m.cancelPageTransition()
	deck, err := presentation.Parse(item.Source)
	if err != nil {
		m.status, m.errMessage = "", "Invalid presentation: "+err.Error()
		return
	}
	m.item, m.deck, m.loaded = item, deck, true
	m.imageGraphicsUsed = m.imageGraphicsUsed || len(item.Images) > 0
	m.imageRevision++
	clear(m.imageRenderCache)
	m.find.close()
	m.editor.ClearSearch()
	m.searchMatches, m.searchMatch = nil, -1
	m.status, m.errMessage = "", ""
	m.page = min(m.page, m.pageCount()-1)
	m.clampScroll()
}

// pageLines renders the current page as wrapped, sanitized terminal lines.
func (m *presentationModel) pageLines() []string {
	if !m.loaded {
		return nil
	}
	m.imageRowMarkers = make(map[rune]presentationImageRow)
	m.nextImageMarker = 0xf0000
	width := max(1, m.width-4)
	var text strings.Builder
	var title string
	imageSlots := make(map[string]presentationImageSlot)
	if m.page == 0 {
		title = m.deck.Title
		text.WriteString(m.deck.Header)
	} else {
		slide := m.deck.Slides[m.page-1]
		title = slide.Title
		nextSlot := 0
		expanded, err := presentation.ExpandDirectivesContextWithImageRenderer(
			context.Background(), slide.Body, filepath.Dir(m.path), m.item.Images,
			func(name, height, imageWidth string) string {
				slot := fmt.Sprintf("PIMG%d__", nextSlot)
				nextSlot++
				imageSlots[slot] = presentationImageSlot{name: name, height: height, width: imageWidth}
				return slot
			},
		)
		body := slide.Body
		if err == nil {
			body = expanded
		}
		text.WriteString(body)
		if m.showNotes {
			text.WriteString("\n\n── Speaker notes ──\n")
			if slide.Notes == "" {
				text.WriteString("(none)")
			} else {
				text.WriteString(strings.TrimSpace(slide.Notes))
			}
		}
	}
	accent := m.theme.Heading
	lines := []string{
		accent.Render(runewidth.Truncate(sanitizeTerminalLine(title), width, "…")),
		strings.Repeat("─", min(width, max(1, runewidth.StringWidth(sanitizeTerminalLine(title))))),
		"",
	}
	body := strings.ReplaceAll(sanitizeTerminalText(strings.Trim(text.String(), "\n")), "\t", "    ")
	if body != "" {
		rendered := renderMarkdown(body, width, m.theme)
		for _, line := range strings.Split(rendered, "\n") {
			plainLine := ansi.Strip(line)
			matchedSlot := false
			for slot, directive := range imageSlots {
				if strings.Contains(plainLine, slot) {
					remainingRows := max(m.visibleLines()-len(lines), 1)
					slotAt := strings.Index(line, slot)
					prefixWidth := ansi.StringWidth(line[:slotAt])
					availableCols := max(1, width-prefixWidth)
					image := m.renderPresentationImage(
						directive.name, directive.height, directive.width, remainingRows, availableCols,
					)
					if termimg.IsGraphicsOutput(termimg.DetectProtocol(), image.output) && image.pixels != nil {
						marker := m.nextPresentationImageMarker(body, image, 0)
						lines = append(lines, strings.Replace(line, slot, string(marker), 1))
						for row := 1; row < max(image.rows, 1); row++ {
							marker := m.nextPresentationImageMarker(body, image, row)
							lines = append(lines, strings.Repeat(" ", prefixWidth)+string(marker))
						}
					} else {
						outputLines := strings.Split(image.output, "\n")
						lines = append(lines, strings.Replace(line, slot, outputLines[0], 1))
						for row := 1; row < max(image.rows, 1); row++ {
							if row < len(outputLines) {
								lines = append(lines, strings.Repeat(" ", prefixWidth)+outputLines[row])
							} else {
								lines = append(lines, strings.Repeat(" ", prefixWidth))
							}
						}
					}
					matchedSlot = true
					break
				}
			}
			if !matchedSlot {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func (m *presentationModel) nextPresentationImageMarker(
	source string,
	rendered renderedPresentationImage,
	row int,
) rune {
	if m.nextImageMarker < 0xf0000 {
		m.nextImageMarker = 0xf0000
	}
	for {
		marker := m.nextImageMarker
		m.nextImageMarker++
		if !strings.ContainsRune(source, marker) {
			m.imageRowMarkers[marker] = presentationImageRow{image: rendered, row: row}
			return marker
		}
	}
}

func (m *presentationModel) renderPresentationImage(
	name, heightText, widthText string,
	availableRows, availableCols int,
) renderedPresentationImage {
	var data []byte
	for _, img := range m.item.Images {
		if img.Name == name {
			data = img.Data
			break
		}
	}
	if data == nil {
		return renderedPresentationImage{
			output: m.theme.Image.Render("[missing presentation image " + name + "]"),
			rows:   1,
		}
	}

	maxCols := min(max(1, m.width-4), max(availableCols, 1))
	maxRows := max(availableRows, 1)
	if heightText != "" && heightText != "_" {
		height, err := strconv.Atoi(heightText)
		if err != nil || height < 1 {
			return renderedPresentationImage{
				output: m.theme.Image.Render("[invalid image height " + heightText + "]"),
				rows:   1,
			}
		}
		maxRows = min(maxRows, height)
	}
	if widthText != "" && widthText != "_" {
		width, err := strconv.Atoi(widthText)
		if err != nil || width < 1 {
			return renderedPresentationImage{
				output: m.theme.Image.Render("[invalid image width " + widthText + "]"),
				rows:   1,
			}
		}
		maxCols = min(maxCols, width)
	}
	maxRows = max(maxRows, 1)

	cacheKey := fmt.Sprintf("%s:%d:%d:%d", name, maxCols, maxRows, termimg.DetectProtocol())
	if rendered, ok := m.imageRenderCache[cacheKey]; ok {
		return rendered
	}
	img, err := termimg.DecodeImage(data)
	if err != nil {
		rendered := renderedPresentationImage{
			output: m.theme.Image.Render("[could not render image " + name + ": " + err.Error() + "]"),
			rows:   1,
		}
		m.imageRenderCache[cacheKey] = rendered
		return rendered
	}
	cols, rows := termimg.CellSize(img, maxCols, maxRows)
	output, rows := termimg.RenderPixels(img, maxCols, maxRows, termimg.DetectProtocol())
	rendered := renderedPresentationImage{output: output, cols: cols, rows: max(rows, 1)}
	if termimg.IsGraphicsOutput(termimg.DetectProtocol(), output) {
		scaled := image.NewNRGBA(image.Rect(0, 0,
			cols*documentImagePixelScale, rows*2*documentImagePixelScale))
		bounds := img.Bounds()
		for y := 0; y < scaled.Bounds().Dy(); y++ {
			for x := 0; x < scaled.Bounds().Dx(); x++ {
				sourceX := bounds.Min.X + x*bounds.Dx()/scaled.Bounds().Dx()
				sourceY := bounds.Min.Y + y*bounds.Dy()/scaled.Bounds().Dy()
				scaled.Set(x, y, img.At(sourceX, sourceY))
			}
		}
		rendered.pixels = scaled
	}
	m.imageRenderCache[cacheKey] = rendered
	return rendered
}

func (m *presentationModel) visibleLines() int {
	promptRows := 0
	if m.pdfPrompt != promptNone {
		promptRows = 1
	}
	return max(1, m.height-5-promptRows)
}

func (m *presentationModel) clampScroll() {
	limit := max(0, len(m.pageLines())-m.visibleLines())
	m.scroll = min(max(m.scroll, 0), limit)
}

func (m *presentationModel) View() tea.View {
	return tea.NewView(m.viewContent())
}

func (m *presentationModel) viewContent() string {
	view := m.presentationView()
	if m.imageGraphicsUsed && termimg.DetectProtocol() != termimg.ProtocolNone {
		view = m.paintPresentationImages(view)
		view = repaintImageFrame(view, m.imageFrame)
	}
	return view
}

func (m *presentationModel) paintPresentationImages(view string) string {
	type placement struct {
		top, last, x, width, sourceRow int
		image                          renderedPresentationImage
	}
	var images []placement
	lines := strings.Split(view, "\n")
	for row, line := range lines {
		var painted strings.Builder
		for offset := 0; offset < len(line); {
			marker, size := utf8.DecodeRuneInString(line[offset:])
			rowInfo, found := m.imageRowMarkers[marker]
			if !found {
				painted.WriteString(line[offset : offset+size])
				offset += size
				continue
			}
			x := ansi.StringWidth(line[:offset])
			width := min(rowInfo.image.cols, max(m.width-x-1, 0))
			painted.WriteByte(' ')
			if width == 0 {
				offset += size
				continue
			}
			merged := false
			for p := range images {
				previous := &images[p]
				if previous.image.pixels == rowInfo.image.pixels && previous.x == x && previous.width == width &&
					previous.sourceRow+previous.last-previous.top+1 == rowInfo.row &&
					previous.last+1 == row {
					previous.last = row
					merged = true
					break
				}
			}
			if !merged {
				images = append(images, placement{
					top: row, last: row, x: x, width: width,
					sourceRow: rowInfo.row, image: rowInfo.image,
				})
			}
			offset += size
		}
		lines[row] = painted.String()
	}
	for _, img := range images {
		scale := documentImagePixelScale
		top := img.sourceRow * 2 * scale
		height := (img.last - img.top + 1) * 2 * scale
		cropped := img.image.pixels.SubImage(image.Rect(0, top,
			img.width*scale, top+height))
		var output string
		switch termimg.DetectProtocol() {
		case termimg.ProtocolKitty:
			output = termimg.RenderKitty(cropped, img.width, img.last-img.top+1)
		case termimg.ProtocolITerm2:
			output = termimg.RenderITerm2(cropped, img.width, img.last-img.top+1)
		case termimg.ProtocolSixel:
			output = termimg.RenderSixel(cropped, img.width, img.last-img.top+1)
		}
		if output == "" {
			continue
		}
		// Text repaints erase Sixel and iTerm2 pixels. Paint only after the
		// entire image rectangle, including its blank rows, has been written.
		position := "\r"
		if img.last > img.top {
			position += fmt.Sprintf("\x1b[%dA", img.last-img.top)
		}
		if img.x > 0 {
			position += fmt.Sprintf("\x1b[%dC", img.x)
		}
		lines[img.last] += "\x1b[s" + position + output + "\x1b[u"
	}
	return strings.Join(lines, "\n")
}

func (m *presentationModel) presentationView() string {
	clearImages := ""
	if m.imageGraphicsUsed && termimg.DetectProtocol() == termimg.ProtocolKitty {
		clearImages = termimg.ClearKittyPlacements()
	}
	if m.width < 20 || m.height < presentationChromeLines+2 {
		return clearImages + "Terminal too small. Press q to quit."
	}
	muted := m.theme.Muted
	var content string
	if m.editing {
		content = m.editor.View()
	} else {
		content = m.renderSlideContent()
	}
	if m.pdfPrompt != promptNone {
		content = m.pdfInput.View() + "\n" + content
	}
	status := m.renderStatus()
	if m.editing && m.find.active {
		status = m.find.view(m.editor.MatchCount(), m.editor.ActiveMatch(), m.width)
	} else if m.find.visible && !m.editing {
		status = m.find.view(len(m.searchMatches), m.searchMatch, m.width)
	}
	return clearImages + chromeLayout(m.width, m.height, m.renderHeader(m.theme.Chrome), m.renderHelp(muted), content, status, m.theme.BorderColor)
}

func (m *presentationModel) pageHasImageDirective() bool {
	return m.pageHasImageDirectiveAt(m.page)
}

func (m *presentationModel) pageHasImageDirectiveAt(page int) bool {
	if !m.loaded || page <= 0 || page > len(m.deck.Slides) {
		return false
	}
	for _, line := range strings.Split(m.deck.Slides[page-1].Body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ".image ") {
			return true
		}
	}
	return false
}

func (m *presentationModel) imageViewState() presentationImageViewState {
	if m.editing || !m.imageGraphicsUsed || !m.pageHasImageDirective() ||
		m.width < 20 || m.height < presentationChromeLines+2 ||
		termimg.DetectProtocol() == termimg.ProtocolNone {
		return presentationImageViewState{}
	}
	return presentationImageViewState{
		page: m.page, scroll: m.scroll, width: m.width, height: m.height,
		showNotes: m.showNotes, pdfPrompt: m.pdfPrompt, revision: m.imageRevision,
	}
}

func (m *presentationModel) renderHeader(chrome lipgloss.Style) string {
	header := "Presentation"
	if m.loaded {
		header = sanitizeTerminalLine(m.deck.Title)
		if m.editing {
			header += " — editing source"
		} else {
			header += fmt.Sprintf(" — %d/%d", m.page, len(m.deck.Slides))
		}
	}
	if m.path != "" {
		header += "  " + sanitizeTerminalLine(m.path)
	}
	return chrome.Render(runewidth.Truncate(header, m.width, "…"))
}

func (m *presentationModel) renderSlideContent() string {
	lines := m.pageLines()
	if m.find.visible && m.find.query() != "" {
		lines = highlightLines(lines, m.find.query(), m.theme)
	}
	visibleLines := m.visibleLines()
	if m.active {
		current := make([]string, visibleLines)
		for i := range current {
			if index := m.scroll + i; index < len(lines) {
				current[i] = lines[index]
			}
		}
		lines = presentationTransitionLines(
			strings.Split(m.from, "\n"), current, max(m.width-4, 1),
			m.position, m.direction,
		)
		return strings.Join(lines, "\n")
	}
	var slideBuf strings.Builder
	for i := 0; i < visibleLines; i++ {
		if i > 0 {
			slideBuf.WriteByte('\n')
		}
		if index := m.scroll + i; index < len(lines) {
			slideBuf.WriteString(lines[index])
		}
	}
	return slideBuf.String()
}

func presentationTransitionLines(from, to []string, width int, position float64, direction int) []string {
	shift := min(max(int(float64(width)*position+0.5), 0), width)
	lines := make([]string, max(len(from), len(to)))
	for row := range lines {
		var oldLine, newLine string
		if row < len(from) {
			oldLine = from[row]
		}
		if row < len(to) {
			newLine = to[row]
		}
		if direction >= 0 {
			leftWidth := width - shift
			lines[row] = fitPresentationTransitionPart(ansi.Cut(oldLine, shift, width), leftWidth) +
				fitPresentationTransitionPart(ansi.Cut(newLine, 0, shift), shift)
		} else {
			lines[row] = fitPresentationTransitionPart(ansi.Cut(newLine, width-shift, width), shift) +
				fitPresentationTransitionPart(ansi.Cut(oldLine, 0, width-shift), width-shift)
		}
	}
	return lines
}

func fitPresentationTransitionPart(part string, width int) string {
	return part + strings.Repeat(" ", max(width-ansi.StringWidth(part), 0))
}

func (m *presentationModel) renderStatus() string {
	status := m.status
	if m.errMessage != "" {
		status = "Error: " + m.errMessage
	} else if status == "" && m.loaded && !m.editing {
		if m.page == 0 {
			status = fmt.Sprintf("Title page • %d slides", len(m.deck.Slides))
		} else {
			status = fmt.Sprintf("Slide %d of %d", m.page, len(m.deck.Slides))
		}
	}
	return runewidth.Truncate(sanitizeTerminalLine(status), m.width, "…")
}

func (m *presentationModel) renderHelp(muted lipgloss.Style) string {
	help := "ctrl+p PDF • ←/→ slide • ↑/↓ scroll • e edit • s notes • f font • u undo • ctrl+r redo • r reload • esc/q quit"
	if m.editing {
		help = "ctrl+f find • ctrl+p PDF • ctrl+s save • esc cancel • ctrl+c quit"
	} else if !m.loaded {
		help = "r retry • esc/q quit"
	}
	return muted.Render(runewidth.Truncate(help, m.width, "…"))
}
