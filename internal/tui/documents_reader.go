package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/document"
)

// The document reader shows the saved document as printed pages, with an
// outline of its headings beside them on wide terminals. Existing documents
// open here; e switches to the editor and leaving the editor returns here.

// startReading shows d, the saved document, in the reader.
func (s *documentsScreen) startReading(d document.Document) {
	s.stopDocumentAutosave()
	s.loadDocument(d)
	s.body.Blur()
	s.reading = true
	s.prompt, s.previewing, s.showChanges, s.reviewing = promptNone, false, false, false
	s.refreshReader()
}

// refreshReader paginates the saved document and rebuilds its outline,
// keeping the current page when it still exists.
func (s *documentsScreen) refreshReader() {
	s.readerPages, s.outline = nil, nil
	pages, err := document.Paginate(s.snapshot)
	if err != nil {
		s.errMessage = err.Error()
	} else {
		s.readerPages = pages
		s.outline, _ = document.Outline(s.snapshot)
	}
	s.readerPage = min(max(s.readerPage, 0), max(len(s.readerPages)-1, 0))
	s.resizeReader()
}

func (s *documentsScreen) resizeReader() {
	width, height := s.width, s.height-4
	if s.wideReader() {
		_, preview := s.readerPaneWidths()
		width, height = preview-2, s.height-6
	}
	s.reader.Width, s.reader.Height = max(width, 1), max(height, 1)
	s.showReaderPage()
}

func (s *documentsScreen) showReaderPage() {
	if s.readerPage >= len(s.readerPages) {
		s.reader.SetContent("")
		return
	}
	s.reader.SetContent(pageText(s.readerPages[s.readerPage], s.reader.Width))
	s.reader.GotoTop()
}

func (s *documentsScreen) wideReader() bool { return s.width >= 80 }

func (s *documentsScreen) readerPaneWidths() (outline, preview int) {
	outline = max(s.width/3, 24)
	return outline, max(s.width-outline-4, 30)
}

// changeReaderPage moves by step pages, wrapping around when wrap is set,
// and reports whether the page changed.
func (s *documentsScreen) changeReaderPage(step int, wrap bool) bool {
	if len(s.readerPages) == 0 {
		return false
	}
	next := s.readerPage + step
	if wrap {
		next = (next + len(s.readerPages)) % len(s.readerPages)
	}
	if next < 0 || next >= len(s.readerPages) || next == s.readerPage {
		return false
	}
	s.readerPage = next
	s.showReaderPage()
	return true
}

// scrollReader scrolls within the page, continuing onto the neighbouring
// page at either end.
func (s *documentsScreen) scrollReader(down bool, msg tea.Msg) {
	if down && s.reader.AtBottom() {
		s.changeReaderPage(1, false)
		return
	}
	if !down && s.reader.AtTop() {
		if s.changeReaderPage(-1, false) {
			s.reader.GotoBottom()
		}
		return
	}
	s.reader, _ = s.reader.Update(msg)
}

func (s *documentsScreen) updateReaderKey(msg tea.KeyMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "q", "esc", "ctrl+c":
		return s.close()
	case "e":
		cmd, ok := s.startEdit(s.snapshot)
		if !ok {
			return nil
		}
		s.reading = false
		return cmd
	case "v", "f4":
		return s.loadChanges()
	case "u", "ctrl+z":
		return s.history(true)
	case "ctrl+r":
		return s.history(false)
	case "right", "]":
		s.changeReaderPage(1, key == "]")
	case "left", "[":
		s.changeReaderPage(-1, key == "[")
	case "{":
		s.stepSection(-1)
	case "}":
		s.stepSection(1)
	case "down", "j", "pgdown", " ":
		s.scrollReader(true, msg)
	case "up", "k", "pgup":
		s.scrollReader(false, msg)
	case "home", "end", "g", "G":
		s.reader, _ = s.reader.Update(msg)
	}
	return nil
}

func (s *documentsScreen) updateReaderMouse(msg tea.MouseMsg) {
	if msg.Action != tea.MouseActionPress {
		return
	}
	switch msg.Button {
	case tea.MouseButtonLeft:
		s.clickOutline(msg.X, msg.Y)
	case tea.MouseButtonWheelDown:
		s.scrollReader(true, msg)
	case tea.MouseButtonWheelUp:
		s.scrollReader(false, msg)
	}
}

// currentSection is the last outline entry that starts on or before the page
// being read, or -1 before the first heading.
func (s *documentsScreen) currentSection() int {
	current := -1
	for i, section := range s.outline {
		if section.Page <= s.readerPage+1 {
			current = i
		}
	}
	return current
}

func (s *documentsScreen) stepSection(step int) {
	next := s.currentSection() + step
	if next >= 0 && next < len(s.outline) {
		s.gotoSection(next)
	}
}

func (s *documentsScreen) gotoSection(index int) {
	s.readerPage = min(max(s.outline[index].Page-1, 0), max(len(s.readerPages)-1, 0))
	s.showReaderPage()
}

// outlineLines renders the outline pane. targets maps each line to an
// outline index, or -1 when the line is not a section.
func (s *documentsScreen) outlineLines() (lines []string, targets []int) {
	add := func(line string, target int) {
		lines = append(lines, line)
		targets = append(targets, target)
	}
	add("Outline", -1)
	if len(s.outline) == 0 {
		add("  No headings", -1)
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
		indent := strings.Repeat("  ", min(max(section.Level-1, 0), 4))
		add(marker+indent+sanitizeTerminalLine(section.Title), i)
	}
	return lines, targets
}

// clickOutline jumps to the section under a click in the outline pane. Rows
// start below the header line and the pane's top border.
func (s *documentsScreen) clickOutline(x, y int) {
	if !s.wideReader() {
		return
	}
	outlineWidth, _ := s.readerPaneWidths()
	if x < 1 || x > outlineWidth+1 {
		return
	}
	_, targets := s.outlineLines()
	if row := y - 2; row >= 0 && row < len(targets) && targets[row] >= 0 {
		s.gotoSection(targets[row])
	}
}

func (s *documentsScreen) readerPageLabel() string {
	if len(s.readerPages) == 0 {
		return "–"
	}
	return fmt.Sprintf("%d/%d", s.readerPage+1, len(s.readerPages))
}

func (s *documentsScreen) readerView(header string) string {
	footer := "e edit  ↑/↓ scroll  ←/→ or [ ] page  { } section  v changes  u undo  Ctrl+R redo  q quit"
	if !s.wideReader() {
		return header + "\nPage " + s.readerPageLabel() + "\n" + s.reader.View() + "\n" +
			runewidth.Truncate(footer, max(s.width, 1), "…") + s.statusLine()
	}
	outlineWidth, previewWidth := s.readerPaneWidths()
	lines, _ := s.outlineLines()
	for i, line := range lines {
		lines[i] = runewidth.Truncate(line, max(outlineWidth-2, 1), "…")
	}
	pane := func(width int, content string) string {
		return lipgloss.NewStyle().Width(width).Height(max(s.height-5, 1)).MaxHeight(max(s.height-3, 1)).
			Border(lipgloss.NormalBorder()).BorderForeground(s.theme.border).Padding(0, 1).Render(content)
	}
	content := "Page " + s.readerPageLabel() + "\n" + s.reader.View()
	footer = "click outline to jump  " + footer
	return header + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, pane(outlineWidth, strings.Join(lines, "\n")), pane(previewWidth, content)) +
		"\n" + runewidth.Truncate(footer, max(s.width, 1), "…") + s.statusLine()
}

// leaveEditor returns from the editor to the reader, discarding unsaved edits
// and the autosaved draft, as closing the editor normally does.
func (s *documentsScreen) leaveEditor(status string) tea.Cmd {
	store, path := s.recoveryStore, s.path
	s.startReading(s.snapshot)
	s.status = status
	s.draftStored = false
	if store == nil {
		return nil
	}
	return func() tea.Msg {
		_ = store.DeleteRecovery(context.Background(), path)
		return nil
	}
}
