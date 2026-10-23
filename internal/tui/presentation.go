package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/tui/editor"
)

// presentationChromeLines counts the header, status, and help lines.
const presentationChromeLines = 3

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
}

type presentationEditorState struct {
	editing        bool
	editor         editor.Model
	discardWarning bool
}

// presentationModel is a single-deck slide viewer and Markdown source editor.
// It owns only UI state; it reads and edits the deck file through the
// presentation service.
type presentationModel struct {
	service *presentation.Service
	path    string
	item    presentation.Presentation
	deck    presentation.Deck
	loaded  bool

	presentationOpState
	presentationViewState
	presentationEditorState
}

func newPresentationModel(service *presentation.Service, path string) *presentationModel {
	body := editor.New()
	return &presentationModel{
		service: service,
		path:    path,
		presentationEditorState: presentationEditorState{
			editor: body,
		},
		presentationOpState: presentationOpState{
			newOperationContext: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
		},
	}
}

func (m *presentationModel) Init() tea.Cmd { return m.load() }

func (m *presentationModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.editor.SetWidth(max(1, m.width-4))
		m.editor.SetHeight(max(1, m.height-5))
		m.clampScroll()
		return m, nil
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
			return m, nil
		}
		m.leaveEditor()
		m.setItem(msg.item)
		m.status = "Saved"
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
	case tea.KeyMsg:
		if m.editing {
			return m, m.updateEditKey(msg)
		}
		return m, m.updateBrowseKey(msg)
	case tea.MouseMsg:
		if m.editing && !m.pending {
			mouseMsg := msg
			mouseMsg.Y = msg.Y - 2
			mouseMsg.X = msg.X - 2
			var cmd tea.Cmd
			m.editor, cmd = m.editor.Update(mouseMsg)
			return m, cmd
		}
		return m, nil
	}
	if m.editing {
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(message)
		return m, cmd
	}
	return m, nil
}

func (m *presentationModel) updateEditKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
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
			m.cancelPending()
			m.status, m.errMessage = "Save cancelled", ""
		} else {
			m.status = "Saving… (esc cancels)"
		}
		return nil
	}
	switch key {
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

func (m *presentationModel) updateBrowseKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "ctrl+c", "q":
		m.cancelPending()
		return tea.Quit
	case "esc":
		if m.pending {
			m.cancelPending()
			m.status, m.errMessage = "Cancelled", ""
			return nil
		}
		m.cancelPending()
		return tea.Quit
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
	switch key {
	case "right", "l", "n", " ", "pgdown":
		m.goToPage(m.page + 1)
	case "left", "h", "p", "pgup":
		m.goToPage(m.page - 1)
	case "home", "g":
		m.goToPage(0)
	case "end", "G":
		m.goToPage(m.pageCount() - 1)
	case "down", "j":
		m.scroll++
		m.clampScroll()
	case "up", "k":
		m.scroll--
		m.clampScroll()
	case "s":
		m.showNotes = !m.showNotes
		m.clampScroll()
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
	page = min(max(page, 0), m.pageCount()-1)
	if page != m.page {
		m.page, m.scroll = page, 0
	}
}

func (m *presentationModel) beginEdit() tea.Cmd {
	m.editing, m.discardWarning = true, false
	m.status, m.errMessage = "", ""
	m.editor.SetValue(m.item.Source)
	m.moveEditorToLine(slideSourceLine(m.item.Source, m.page))
	cmd := m.editor.Focus()
	return cmd
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

func trimLeadingSpace(s string) string {
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

		trimmed := trimLeadingSpace(line)
		if fence != "" {
			if len(trimmed) >= 3 && trimmed[:3] == fence {
				fence = ""
			}
		} else if len(trimmed) >= 3 {
			prefix := trimmed[:3]
			if prefix == "```" || prefix == "~~~" {
				fence = prefix
			} else if page == 0 && prefix[:2] == "# " {
				return index
			} else if prefix == "## " {
				slide++
				if slide == page {
					return index
				}
			}
		} else if page == 0 && strings.HasPrefix(trimmed, "# ") {
			return index
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

		trimmed := trimLeadingSpace(text)
		if fence != "" {
			if len(trimmed) >= 3 && trimmed[:3] == fence {
				fence = ""
			}
		} else if len(trimmed) >= 3 {
			prefix := trimmed[:3]
			if prefix == "```" || prefix == "~~~" {
				fence = prefix
			} else if prefix == "## " {
				slide++
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
}

func (m *presentationModel) setError(prefix string, err error) {
	switch {
	case isCancelled(err) || errors.Is(err, context.Canceled):
		m.status, m.errMessage = "Cancelled", ""
	case errors.Is(err, presentation.ErrNotFound):
		m.status, m.errMessage = "", prefix+": presentation not found"
	default:
		m.status, m.errMessage = "", prefix+": "+err.Error()
	}
}

func (m *presentationModel) setItem(item presentation.Presentation) {
	deck, err := presentation.Parse(item.Source)
	if err != nil {
		m.status, m.errMessage = "", "Invalid presentation: "+err.Error()
		return
	}
	m.item, m.deck, m.loaded = item, deck, true
	m.status, m.errMessage = "", ""
	m.page = min(m.page, m.pageCount()-1)
	m.clampScroll()
}

// pageLines renders the current page as wrapped, sanitized terminal lines.
func (m *presentationModel) pageLines() []string {
	if !m.loaded {
		return nil
	}
	width := max(1, m.width-4)
	var text strings.Builder
	var title string
	if m.page == 0 {
		title = m.deck.Title
		text.WriteString(m.deck.Header)
	} else {
		slide := m.deck.Slides[m.page-1]
		title = slide.Title
		text.WriteString(slide.Body)
		if m.showNotes {
			text.WriteString("\n\n── Speaker notes ──\n")
			if slide.Notes == "" {
				text.WriteString("(none)")
			} else {
				text.WriteString(slide.Notes)
			}
		}
	}
	accent := lipgloss.NewStyle().Foreground(primaryColor).Bold(true)
	lines := []string{
		accent.Render(runewidth.Truncate(sanitizeTerminalLine(title), width, "…")),
		strings.Repeat("─", min(width, max(1, runewidth.StringWidth(sanitizeTerminalLine(title))))),
		"",
	}
	body := strings.ReplaceAll(sanitizeTerminalText(strings.Trim(text.String(), "\n")), "\t", "    ")
	if body != "" {
		wrapped := lipgloss.NewStyle().Width(width).Render(body)
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}
	return lines
}

func (m *presentationModel) visibleLines() int {
	return max(1, m.height-5)
}

func (m *presentationModel) clampScroll() {
	limit := max(0, len(m.pageLines())-m.visibleLines())
	m.scroll = min(max(m.scroll, 0), limit)
}

func (m *presentationModel) View() string {
	if m.width < 20 || m.height < presentationChromeLines+2 {
		return "Terminal too small. Press q to quit."
	}
	accent := lipgloss.NewStyle().Foreground(primaryColor).Bold(true)
	muted := lipgloss.NewStyle().Foreground(borderColor)
	var b strings.Builder

	b.WriteString(m.renderHeader(accent) + "\n")
	b.WriteString(m.renderContentPane() + "\n")
	b.WriteString(m.renderStatus() + "\n")
	b.WriteString(m.renderHelp(muted))
	return b.String()
}

func (m *presentationModel) renderHeader(accent lipgloss.Style) string {
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
	return accent.Render(runewidth.Truncate(header, m.width, "…"))
}

func (m *presentationModel) renderContentPane() string {
	var content string
	if m.editing {
		content = m.editor.View()
	} else {
		content = m.renderSlideContent()
	}

	paneHeight := max(1, m.height-3)
	return lipgloss.NewStyle().Width(max(1, m.width)).Height(paneHeight).
		Border(lipgloss.NormalBorder()).BorderForeground(borderColor).Padding(0, 1).Render(content)
}

func (m *presentationModel) renderSlideContent() string {
	lines := m.pageLines()
	var slideBuf strings.Builder
	for i := 0; i < m.visibleLines(); i++ {
		if i > 0 {
			slideBuf.WriteByte('\n')
		}
		if index := m.scroll + i; index < len(lines) {
			slideBuf.WriteString(lines[index])
		}
	}
	return slideBuf.String()
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
	help := "←/→ slide • ↑/↓ scroll • e edit • s notes • u undo • ctrl+r redo • r reload • esc/q quit"
	if m.editing {
		help = "ctrl+s save • esc cancel • ctrl+c quit"
	} else if !m.loaded {
		help = "r retry • esc/q quit"
	}
	return muted.Render(runewidth.Truncate(help, m.width, "…"))
}
