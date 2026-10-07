package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/presentation"
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

// presentationModel is a single-deck slide viewer and Markdown source editor.
// It owns only UI state; it reads and edits the deck file through the
// presentation service.
type presentationModel struct {
	service             *presentation.Service
	path                string
	item                presentation.Presentation
	deck                presentation.Deck
	loaded              bool
	page                int
	scroll              int
	showNotes           bool
	width, height       int
	editing             bool
	editor              textarea.Model
	discardWarning      bool
	pending             bool
	seq                 int
	status              string
	errMessage          string
	loadedStatus        string
	theme               theme
	newOperationContext func() (context.Context, context.CancelFunc)
	cancelOperation     context.CancelFunc
}

func newPresentationModel(service *presentation.Service, path string) *presentationModel {
	editor := textarea.New()
	editor.Prompt = ""
	editor.ShowLineNumbers = true
	editor.CharLimit = 5_000_000
	editor.MaxHeight = 0
	return &presentationModel{
		service: service, path: path, editor: editor,
		newOperationContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
		theme: theme{
			primary: lipgloss.AdaptiveColor{Light: "#4b3f72", Dark: "#c4b5fd"},
			border:  lipgloss.AdaptiveColor{Light: "#b8b4c7", Dark: "#55516a"},
		},
	}
}

func (m *presentationModel) Init() tea.Cmd { return m.load() }

func (m *presentationModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.editor.SetWidth(max(1, m.width))
		m.editor.SetHeight(max(1, m.height-presentationChromeLines))
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
		if m.editing && !m.pending && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			placeTextareaCursor(&m.editor, msg.X, msg.Y-1)
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
		}
		return nil
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
	case "u":
		return m.history("Undid", m.service.Undo)
	case "ctrl+r":
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
	// Let the textarea scroll its viewport to the repositioned cursor.
	m.editor, _ = m.editor.Update(nil)
	return cmd
}

func (m *presentationModel) moveEditorToLine(line int) {
	for guard := m.editor.LineCount() * 64; m.editor.Line() > line && guard > 0; guard-- {
		m.editor.CursorUp()
	}
	for guard := m.editor.LineCount() * 64; m.editor.Line() < line && guard > 0; guard-- {
		m.editor.CursorDown()
	}
	m.editor.CursorStart()
}

// slideSourceLine finds the source line of a page's heading for cursor
// placement. Page 0 is the title page; it skips headings inside fences.
func slideSourceLine(source string, page int) int {
	lines := strings.Split(source, "\n")
	slide, fence := 0, ""
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = trimmed[:3]
			continue
		}
		if page == 0 && strings.HasPrefix(trimmed, "# ") {
			return index
		}
		if strings.HasPrefix(trimmed, "## ") {
			slide++
			if slide == page {
				return index
			}
		}
	}
	return 0
}

// pageForSourceLine is the inverse of slideSourceLine: it returns the page
// containing a source line. Lines before the first slide heading belong to
// the title page.
func pageForSourceLine(source string, line int) int {
	slide, fence := 0, ""
	for index, text := range strings.Split(source, "\n") {
		if index > line {
			break
		}
		trimmed := strings.TrimSpace(text)
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = trimmed[:3]
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			slide++
		}
	}
	return slide
}

// leaveEditor returns to the viewer on the slide containing the cursor.
func (m *presentationModel) leaveEditor() {
	m.goToPage(pageForSourceLine(m.editor.Value(), m.editor.Line()))
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
	width := max(1, m.width)
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
	accent := lipgloss.NewStyle().Foreground(m.theme.primary).Bold(true)
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
	return max(1, m.height-presentationChromeLines)
}

func (m *presentationModel) clampScroll() {
	limit := max(0, len(m.pageLines())-m.visibleLines())
	m.scroll = min(max(m.scroll, 0), limit)
}

func (m *presentationModel) View() string {
	if m.width < 20 || m.height < presentationChromeLines+2 {
		return "Terminal too small. Press q to quit."
	}
	accent := lipgloss.NewStyle().Foreground(m.theme.primary).Bold(true)
	muted := lipgloss.NewStyle().Foreground(m.theme.border)
	var b strings.Builder

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
	b.WriteString(accent.Render(runewidth.Truncate(header, m.width, "…")) + "\n")

	if m.editing {
		b.WriteString(m.editor.View() + "\n")
	} else {
		lines := m.pageLines()
		for i := 0; i < m.visibleLines(); i++ {
			if index := m.scroll + i; index < len(lines) {
				b.WriteString(lines[index])
			}
			b.WriteString("\n")
		}
	}

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
	b.WriteString(runewidth.Truncate(sanitizeTerminalLine(status), m.width, "…") + "\n")
	help := "←/→ slide • ↑/↓ scroll • e edit • s notes • u undo • ctrl+r redo • r reload • q quit"
	if m.editing {
		help = "ctrl+s save • esc cancel • ctrl+c quit"
	} else if !m.loaded {
		help = "r retry • q quit"
	}
	b.WriteString(muted.Render(runewidth.Truncate(help, m.width, "…")))
	return b.String()
}
