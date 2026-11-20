package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
	s.readerPages, s.outline, s.readerLinks = nil, nil, nil
	pages, links, err := s.paginateDocumentWithLinks(s.snapshot)
	if err != nil {
		s.errMessage = err.Error()
	} else {
		s.readerPages = pages
		s.readerLinks = links
		s.outline = append([]document.Section(nil), s.imageState.outline...)
	}
	s.readerPage = min(max(s.readerPage, 0), max(len(s.readerPages)-1, 0))
	s.resizeReader()
}

func (s *documentsScreen) resizeReader() {
	offset, current := s.reader.YOffset(), s.readerMatch
	width, height := s.width-4, s.height-6
	if s.wideReader() {
		_, preview := s.readerPaneWidths()
		width, height = preview-2, s.height-6
	}
	s.reader.SetWidth(max(width, 1))
	s.reader.SetHeight(max(height, 1))
	if s.find.visible && s.find.query() != "" {
		s.rebuildReaderMatches(s.find.query())
		s.readerMatch = min(max(current, 0), len(s.readerMatches)-1)
	}
	s.showReaderPage()
	s.reader.SetYOffset(offset)
}

func (s *documentsScreen) showReaderPage() {
	if s.readerPage >= len(s.readerPages) {
		s.reader.SetContent("")
		return
	}
	content := pageText(s.readerPages[s.readerPage], s.reader.Width())
	if s.find.visible && s.find.query() != "" {
		content = highlightRenderedText(content, s.find.query(), s.theme)
	}
	s.reader.SetContent(content)
	s.reader.GotoTop()
}

func (s *documentsScreen) searchReader(query string) {
	s.rebuildReaderMatches(query)
	if len(s.readerMatches) > 0 {
		s.readerMatch = 0
		s.gotoReaderMatch()
		return
	}
	s.showReaderPage()
}

func (s *documentsScreen) rebuildReaderMatches(query string) {
	s.readerMatches = nil
	for page, content := range s.readerPages {
		s.readerMatches = append(s.readerMatches, renderedMatches(strings.Split(pageText(content, s.reader.Width()), "\n"), query, page)...)
	}
	s.readerMatch = -1
}

func (s *documentsScreen) gotoReaderMatch() {
	if s.readerMatch < 0 || s.readerMatch >= len(s.readerMatches) {
		return
	}
	match := s.readerMatches[s.readerMatch]
	s.readerPage = match.page
	s.showReaderPage()
	s.reader.SetYOffset(match.row)
}

func (s *documentsScreen) moveReaderMatch(step int) {
	if len(s.readerMatches) == 0 {
		return
	}
	s.readerMatch = (s.readerMatch + step) % len(s.readerMatches)
	if s.readerMatch < 0 {
		s.readerMatch += len(s.readerMatches)
	}
	s.gotoReaderMatch()
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

func (s *documentsScreen) updateReaderKey(msg tea.KeyPressMsg) tea.Cmd {
	if s.prompt != promptNone {
		return s.updatePrompt(msg)
	}
	key := msg.String()
	if s.find.active {
		return s.updateReaderFindKey(msg)
	}
	if s.find.visible {
		switch key {
		case "n":
			s.moveReaderMatch(1)
			return nil
		case "N":
			s.moveReaderMatch(-1)
			return nil
		case "esc":
			s.find.close()
			s.searchReader("")
			return nil
		}
	}
	if key == "/" || key == "ctrl+f" {
		s.searchReader(s.find.query())
		return s.find.open(s.width)
	}
	switch key {
	case "q", "esc", "ctrl+c":
		if s.showChanges {
			return s.updateChangesKey(msg)
		}
		return s.close()
	case "e":
		cmd, ok := s.startEdit(s.snapshot)
		if !ok {
			return nil
		}
		s.reading = false
		return cmd
	case "ctrl+p":
		return s.openPDFPrompt()
	case "v", "f4":
		return s.loadChanges()
	case "u", "ctrl+z":
		return s.history(true)
	case "ctrl+r":
		return s.history(false)
	case "ctrl+o":
		s.outlineVisible = !s.outlineVisible
	case "right", "]":
		s.changeReaderPage(1, key == "]")
	case "left", "[":
		s.changeReaderPage(-1, key == "[")
	case "{":
		s.stepSection(-1)
	case "}":
		s.stepSection(1)
	case "down", "j", "pgdown", "space":
		s.scrollReader(true, msg)
	case "up", "k", "pgup":
		s.scrollReader(false, msg)
	case "home", "end", "g", "G":
		s.reader, _ = s.reader.Update(msg)
	}
	return nil
}

func (s *documentsScreen) updateReaderMouse(msg mouseEvent) {
	if s.prompt != promptNone || msg.Action != mousePress {
		return
	}
	switch msg.Button {
	case mouseLeft:
		if s.wideReader() {
			outlineWidth, _ := s.readerPaneWidths()
			if msg.X >= 1 && msg.X <= outlineWidth+1 {
				s.clickOutline(msg.X, msg.Y)
				return
			}
		}
		s.clickReaderLink(msg.X, msg.Y)
	case mouseWheelDown:
		s.scrollReader(true, msg)
	case mouseWheelUp:
		s.scrollReader(false, msg)
	}
}

func (s *documentsScreen) clickReaderLink(x, y int) {
	if s.readerPage >= len(s.readerPages) {
		return
	}
	pageRow := (y - 4) + s.reader.YOffset()
	pageCol := x - 1
	if s.wideReader() {
		outlineWidth, _ := s.readerPaneWidths()
		pageCol = x - (outlineWidth + 2)
	}
	for _, box := range s.readerLinks {
		if box.page == s.readerPage && box.row == pageRow && pageCol >= box.startCol && pageCol < box.endCol {
			s.navigateToTarget(box.target)
			return
		}
	}
}

func (s *documentsScreen) navigateToTarget(target string) {
	clean := strings.TrimPrefix(target, "#")
	if clean == "" {
		return
	}
	targetPage := -1
	targetTitle := ""
	for _, sec := range s.outline {
		if strings.EqualFold(sec.Slug, clean) {
			targetPage = sec.Page - 1
			targetTitle = sec.Title
			break
		}
	}
	if targetPage < 0 || targetPage >= len(s.readerPages) {
		return
	}
	s.readerPage = targetPage
	s.showReaderPage()
	if targetTitle != "" {
		for r, line := range s.readerPages[targetPage].Lines {
			if strings.Contains(ansi.Strip(line), targetTitle) {
				s.reader.SetYOffset(max(0, r-1))
				break
			}
		}
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
	shortcuts := "e edit  Ctrl+P PDF  ↑/↓ scroll  ←/→ or [ ] page  { } section  v changes  u undo  Ctrl+R redo  esc/q quit"
	if s.prompt != promptNone {
		shortcuts = "Enter export PDF  ·  Esc cancel"
		if s.prompt == promptPDFOverwrite {
			shortcuts = "y overwrite  ·  n/Esc cancel"
		}
	}
	readerContent := s.reader.View()
	if s.prompt != promptNone {
		readerContent = s.promptInput.View() + "\n" + readerContent
	}
	if !s.wideReader() {
		content := readerContent
		if s.prompt == promptNone {
			content = "Page " + s.readerPageLabel() + "\n" + content
		}
		shortcuts = runewidth.Truncate(shortcuts, max(s.width, 1), "…")
		return chromeLayout(s.width, s.height, header, shortcuts, content, s.readerStatus(), s.theme.BorderColor)
	}
	outlineWidth, previewWidth := s.readerPaneWidths()
	lines, _ := s.outlineLines()
	for i, line := range lines {
		lines[i] = runewidth.Truncate(line, max(outlineWidth-2, 1), "…")
	}

	left := lipgloss.NewStyle().
		Width(outlineWidth-2).
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(s.theme.BorderColor).
		PaddingRight(1).
		Render(strings.Join(lines, "\n"))

	pageContent := readerContent
	if s.prompt == promptNone {
		pageContent = "Page " + s.readerPageLabel() + "\n" + pageContent
	}
	right := lipgloss.NewStyle().
		Width(previewWidth - 2).
		PaddingLeft(1).
		Render(pageContent)

	content := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	if s.prompt == promptNone {
		shortcuts = "click outline to jump  " + shortcuts
	}
	shortcuts = runewidth.Truncate(shortcuts, max(s.width, 1), "…")
	return chromeLayout(s.width, s.height, header, shortcuts, content, s.readerStatus(), s.theme.BorderColor)
}

func (s *documentsScreen) readerStatus() string {
	if s.find.visible {
		return s.find.view(len(s.readerMatches), s.readerMatch, s.width)
	}
	return s.statusLine()
}

func (s *documentsScreen) updateReaderFindKey(msg tea.Msg) tea.Cmd {
	action, cmd := s.find.update(msg)
	switch action {
	case findChanged:
		s.searchReader(s.find.query())
	case findNext:
		s.moveReaderMatch(1)
		s.find.blur()
	case findPrevious:
		s.moveReaderMatch(-1)
		s.find.blur()
	case findClosed:
		s.searchReader("")
	}
	return cmd
}

// leaveEditor returns from the editor to the reader, discarding unsaved edits
// and the autosaved draft, as closing the editor normally does.
func (s *documentsScreen) leaveEditor(status string) tea.Cmd {
	store, path := s.recoveryStore, s.path
	s.startReading(s.snapshot)
	s.status = status
	if store == nil {
		s.draftStored = false
		return nil
	}
	return func() tea.Msg {
		return documentDraftDiscardedMsg{err: store.DeleteRecovery(context.Background(), path)}
	}
}

// draftDiscarded records the result of deleting the draft on leaving the
// editor. On failure the draft is still known to exist, so the next clean
// autosave retries the deletion.
func (s *documentsScreen) draftDiscarded(msg documentDraftDiscardedMsg) {
	if msg.err == nil {
		s.draftStored = false
		return
	}
	s.draftStored = true
	s.errMessage = "Could not remove recovery draft: " + msg.err.Error()
}
