package editor

import (
	"strings"
	"time"
	"unicode"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Point represents a line and column location in the line-based buffer.
type Point struct {
	Line int
	Col  int
}

// Selection tracks the anchor point and whether selection is active.
type Selection struct {
	Anchor Point
	Active bool
}

// Model is a line-based rune buffer text editor with micro-style visual selections.
type Model struct {
	Lines        [][]rune
	Cursor       Point
	Selection    Selection
	StickyCol    int
	TopLine      int
	LeftCol      int
	Width        int
	Height       int
	Focused      bool
	Placeholder  string
	ShowLineNums bool
	Style        lipgloss.Style
	SelectStyle  lipgloss.Style

	// Mouse click tracking for double/triple click detection
	lastClickTime time.Time
	lastClickPos  Point
	clickCount    int
	isDragging    bool
}

var internalClipboard string

// New creates a new Model with default settings.
func New() Model {
	return Model{
		Lines:       [][]rune{{}},
		Width:       80,
		Height:      20,
		Focused:     true,
		SelectStyle: lipgloss.NewStyle().Reverse(true),
	}
}

// SetValue loads text into the editor lines.
func (m *Model) SetValue(text string) {
	lines := strings.Split(text, "\n")
	m.Lines = make([][]rune, len(lines))
	for i, l := range lines {
		m.Lines[i] = []rune(l)
	}
	m.Cursor = Point{Line: 0, Col: 0}
	m.StickyCol = 0
	m.Selection = Selection{}
	m.TopLine = 0
	m.LeftCol = 0
}

// Value returns the canonical string content of the buffer.
func (m Model) Value() string {
	var sb strings.Builder
	for i, line := range m.Lines {
		sb.WriteString(string(line))
		if i < len(m.Lines)-1 {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
}

// LineCount returns the number of lines in the buffer.
func (m Model) LineCount() int {
	return len(m.Lines)
}

// Line returns the current cursor line.
func (m Model) Line() int {
	return m.Cursor.Line
}

// SetWidth updates the editor width.
func (m *Model) SetWidth(w int) {
	if w < 1 {
		w = 1
	}
	m.Width = w
}

// SetHeight updates the editor height.
func (m *Model) SetHeight(h int) {
	if h < 1 {
		h = 1
	}
	m.Height = h
}

// Focus sets focus to the editor.
func (m *Model) Focus() tea.Cmd {
	m.Focused = true
	return nil
}

// Blur removes focus.
func (m *Model) Blur() {
	m.Focused = false
}

// CursorUp moves the cursor up one line.
func (m *Model) CursorUp() {
	if m.Cursor.Line > 0 {
		m.Cursor.Line--
		lineLen := len(m.Lines[m.Cursor.Line])
		if m.StickyCol > lineLen {
			m.Cursor.Col = lineLen
		} else {
			m.Cursor.Col = m.StickyCol
		}
	}
	m.ensureCursorVisible()
}

// CursorDown moves the cursor down one line.
func (m *Model) CursorDown() {
	if m.Cursor.Line < len(m.Lines)-1 {
		m.Cursor.Line++
		lineLen := len(m.Lines[m.Cursor.Line])
		if m.StickyCol > lineLen {
			m.Cursor.Col = lineLen
		} else {
			m.Cursor.Col = m.StickyCol
		}
	}
	m.ensureCursorVisible()
}

// CursorStart moves the cursor to the beginning of the line.
func (m *Model) CursorStart() {
	m.Cursor.Col = 0
	m.StickyCol = 0
	m.ensureCursorVisible()
}

// pointLess reports whether a is strictly before b in the buffer.
func pointLess(a, b Point) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

// SelectedSpan returns min and max points of the active selection.
func (m Model) SelectedSpan() (start Point, end Point, ok bool) {
	if !m.Selection.Active {
		return Point{}, Point{}, false
	}
	if m.Selection.Anchor == m.Cursor {
		return Point{}, Point{}, false
	}
	if pointLess(m.Selection.Anchor, m.Cursor) {
		return m.Selection.Anchor, m.Cursor, true
	}
	return m.Cursor, m.Selection.Anchor, true
}

// SelectedText returns the text currently selected.
func (m Model) SelectedText() string {
	start, end, ok := m.SelectedSpan()
	if !ok {
		return ""
	}
	if start.Line == end.Line {
		return string(m.Lines[start.Line][start.Col:end.Col])
	}
	var sb strings.Builder
	sb.WriteString(string(m.Lines[start.Line][start.Col:]))
	sb.WriteRune('\n')
	for l := start.Line + 1; l < end.Line; l++ {
		sb.WriteString(string(m.Lines[l]))
		sb.WriteRune('\n')
	}
	sb.WriteString(string(m.Lines[end.Line][:end.Col]))
	return sb.String()
}

// DeleteSelection removes the selected range and collapses selection.
func (m *Model) DeleteSelection() bool {
	start, end, ok := m.SelectedSpan()
	if !ok {
		m.Selection.Active = false
		return false
	}
	if start.Line == end.Line {
		m.Lines[start.Line] = append(m.Lines[start.Line][:start.Col], m.Lines[start.Line][end.Col:]...)
	} else {
		prefix := append([]rune{}, m.Lines[start.Line][:start.Col]...)
		suffix := m.Lines[end.Line][end.Col:]
		merged := append(prefix, suffix...)
		newLines := make([][]rune, 0, len(m.Lines)-(end.Line-start.Line))
		newLines = append(newLines, m.Lines[:start.Line]...)
		newLines = append(newLines, merged)
		newLines = append(newLines, m.Lines[end.Line+1:]...)
		m.Lines = newLines
	}
	m.Cursor = start
	m.StickyCol = start.Col
	m.Selection = Selection{Anchor: start, Active: false}
	m.clampCursor()
	return true
}

// InsertRune inserts a rune at cursor position, deleting selection first if active.
func (m *Model) InsertRune(r rune) {
	m.DeleteSelection()
	line := m.Lines[m.Cursor.Line]
	col := m.Cursor.Col
	line = append(line[:col], append([]rune{r}, line[col:]...)...)
	m.Lines[m.Cursor.Line] = line
	m.Cursor.Col++
	m.StickyCol = m.Cursor.Col
	m.Selection = Selection{Anchor: m.Cursor, Active: false}
	m.ensureCursorVisible()
}

// InsertString inserts multiple lines/characters at cursor position.
func (m *Model) InsertString(s string) {
	m.DeleteSelection()
	runes := []rune(s)
	for _, r := range runes {
		if r == '\n' {
			m.insertNewline()
		} else {
			m.InsertRune(r)
		}
	}
}

func (m *Model) insertNewline() {
	m.DeleteSelection()
	current := m.Lines[m.Cursor.Line]
	left := append([]rune{}, current[:m.Cursor.Col]...)
	right := append([]rune{}, current[m.Cursor.Col:]...)

	m.Lines[m.Cursor.Line] = left
	newLines := make([][]rune, 0, len(m.Lines)+1)
	newLines = append(newLines, m.Lines[:m.Cursor.Line+1]...)
	newLines = append(newLines, right)
	newLines = append(newLines, m.Lines[m.Cursor.Line+1:]...)
	m.Lines = newLines

	m.Cursor.Line++
	m.Cursor.Col = 0
	m.StickyCol = 0
	m.Selection = Selection{Anchor: m.Cursor, Active: false}
	m.ensureCursorVisible()
}

func (m *Model) clampCursor() {
	if m.Cursor.Line < 0 {
		m.Cursor.Line = 0
	}
	if m.Cursor.Line >= len(m.Lines) {
		m.Cursor.Line = len(m.Lines) - 1
	}
	if m.Cursor.Col < 0 {
		m.Cursor.Col = 0
	}
	lineLen := len(m.Lines[m.Cursor.Line])
	if m.Cursor.Col > lineLen {
		m.Cursor.Col = lineLen
	}
}

func (m *Model) ensureCursorVisible() {
	m.clampCursor()
	if m.Cursor.Line < m.TopLine {
		m.TopLine = m.Cursor.Line
	}
	if m.Cursor.Line >= m.TopLine+m.Height {
		m.TopLine = m.Cursor.Line - m.Height + 1
	}
	if m.Cursor.Col < m.LeftCol {
		m.LeftCol = m.Cursor.Col
	}
	if m.Cursor.Col >= m.LeftCol+m.Width {
		m.LeftCol = m.Cursor.Col - m.Width + 1
	}
}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func (m *Model) moveWordLeft() {
	if m.Cursor.Col == 0 {
		if m.Cursor.Line > 0 {
			m.Cursor.Line--
			m.Cursor.Col = len(m.Lines[m.Cursor.Line])
		}
		m.StickyCol = m.Cursor.Col
		return
	}
	line := m.Lines[m.Cursor.Line]
	col := m.Cursor.Col - 1
	for col > 0 && unicode.IsSpace(line[col]) {
		col--
	}
	inWord := isWordChar(line[col])
	for col > 0 && isWordChar(line[col-1]) == inWord && !unicode.IsSpace(line[col-1]) {
		col--
	}
	m.Cursor.Col = col
	m.StickyCol = col
}

func (m *Model) moveWordRight() {
	line := m.Lines[m.Cursor.Line]
	if m.Cursor.Col >= len(line) {
		if m.Cursor.Line < len(m.Lines)-1 {
			m.Cursor.Line++
			m.Cursor.Col = 0
		}
		m.StickyCol = m.Cursor.Col
		return
	}
	col := m.Cursor.Col
	for col < len(line) && unicode.IsSpace(line[col]) {
		col++
	}
	if col < len(line) {
		inWord := isWordChar(line[col])
		for col < len(line) && isWordChar(line[col]) == inWord && !unicode.IsSpace(line[col]) {
			col++
		}
	}
	m.Cursor.Col = col
	m.StickyCol = col
}

// Update handles tea messages for navigation and editing.
func (m *Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.Focused {
		return *m, nil
	}
	switch k := msg.(type) {
	case tea.MouseMsg:
		m.handleMouse(k)
	case tea.KeyMsg:
		m.handleKey(k)
	}
	return *m, nil
}

func (m *Model) handleMouse(msg tea.MouseMsg) {
	if msg.Y < 0 {
		return
	}
	line := m.TopLine + msg.Y
	col := m.LeftCol + msg.X
	if line < 0 {
		line = 0
	}
	if line >= len(m.Lines) {
		line = len(m.Lines) - 1
	}
	if col < 0 {
		col = 0
	}
	if col > len(m.Lines[line]) {
		col = len(m.Lines[line])
	}
	pos := Point{Line: line, Col: col}

	switch msg.Action {
	case tea.MouseActionPress:
		now := time.Now()
		if now.Sub(m.lastClickTime) < 400*time.Millisecond && pos.Line == m.lastClickPos.Line {
			m.clickCount++
		} else {
			m.clickCount = 1
		}
		m.lastClickTime = now
		m.lastClickPos = pos

		m.Cursor = pos
		m.StickyCol = pos.Col
		m.isDragging = true

		if m.clickCount == 2 {
			l := m.Lines[pos.Line]
			startCol, endCol := pos.Col, pos.Col
			if len(l) > 0 {
				if startCol >= len(l) {
					startCol = len(l) - 1
				}
				endCol = startCol
				if isWordChar(l[startCol]) {
					for startCol > 0 && isWordChar(l[startCol-1]) {
						startCol--
					}
					for endCol < len(l) && isWordChar(l[endCol]) {
						endCol++
					}
				} else if unicode.IsSpace(l[startCol]) {
					for startCol > 0 && unicode.IsSpace(l[startCol-1]) {
						startCol--
					}
					for endCol < len(l) && unicode.IsSpace(l[endCol]) {
						endCol++
					}
				} else {
					for startCol > 0 && !isWordChar(l[startCol-1]) && !unicode.IsSpace(l[startCol-1]) {
						startCol--
					}
					for endCol < len(l) && !isWordChar(l[endCol]) && !unicode.IsSpace(l[endCol]) {
						endCol++
					}
				}
			} else {
				startCol, endCol = 0, 0
			}
			m.Selection = Selection{Anchor: Point{Line: pos.Line, Col: startCol}, Active: true}
			m.Cursor = Point{Line: pos.Line, Col: endCol}
			m.StickyCol = endCol
		} else if m.clickCount >= 3 {
			m.Selection = Selection{Anchor: Point{Line: pos.Line, Col: 0}, Active: true}
			m.Cursor = Point{Line: pos.Line, Col: len(m.Lines[pos.Line])}
			m.StickyCol = m.Cursor.Col
		} else {
			m.Selection = Selection{Anchor: pos, Active: true}
		}
		m.ensureCursorVisible()

	case tea.MouseActionMotion:
		if m.isDragging {
			m.Cursor = pos
			m.StickyCol = pos.Col
			m.Selection.Active = true
			m.ensureCursorVisible()
		}

	case tea.MouseActionRelease:
		m.isDragging = false
		if m.Selection.Anchor == m.Cursor && m.clickCount == 1 {
			m.Selection.Active = false
		}
	}
}

func isShiftKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyShiftLeft, tea.KeyShiftRight, tea.KeyShiftUp, tea.KeyShiftDown,
		tea.KeyShiftHome, tea.KeyShiftEnd,
		tea.KeyCtrlShiftLeft, tea.KeyCtrlShiftRight, tea.KeyCtrlShiftUp, tea.KeyCtrlShiftDown,
		tea.KeyCtrlShiftHome, tea.KeyCtrlShiftEnd:
		return true
	}
	keyStr := msg.String()
	return strings.HasPrefix(keyStr, "shift+") || strings.HasPrefix(keyStr, "ctrl+shift+")
}

func (m *Model) handleKey(msg tea.KeyMsg) {
	shift := isShiftKey(msg)
	keyStr := msg.String()

	if shift {
		if !m.Selection.Active {
			m.Selection = Selection{Anchor: m.Cursor, Active: true}
		}
	}

	switch keyStr {
	case "left", "shift+left":
		if !shift && m.Selection.Active {
			start, _, _ := m.SelectedSpan()
			m.Cursor = start
			m.Selection = Selection{Anchor: start, Active: false}
		} else {
			if m.Cursor.Col > 0 {
				m.Cursor.Col--
			} else if m.Cursor.Line > 0 {
				m.Cursor.Line--
				m.Cursor.Col = len(m.Lines[m.Cursor.Line])
			}
			if !shift {
				m.Selection = Selection{Anchor: m.Cursor, Active: false}
			}
		}
		m.StickyCol = m.Cursor.Col

	case "right", "shift+right":
		if !shift && m.Selection.Active {
			_, end, _ := m.SelectedSpan()
			m.Cursor = end
			m.Selection = Selection{Anchor: end, Active: false}
		} else {
			if m.Cursor.Col < len(m.Lines[m.Cursor.Line]) {
				m.Cursor.Col++
			} else if m.Cursor.Line < len(m.Lines)-1 {
				m.Cursor.Line++
				m.Cursor.Col = 0
			}
			if !shift {
				m.Selection = Selection{Anchor: m.Cursor, Active: false}
			}
		}
		m.StickyCol = m.Cursor.Col

	case "up", "shift+up":
		if m.Cursor.Line > 0 {
			m.Cursor.Line--
			lineLen := len(m.Lines[m.Cursor.Line])
			if m.StickyCol > lineLen {
				m.Cursor.Col = lineLen
			} else {
				m.Cursor.Col = m.StickyCol
			}
		}
		if !shift {
			m.Selection = Selection{Anchor: m.Cursor, Active: false}
		}

	case "down", "shift+down":
		if m.Cursor.Line < len(m.Lines)-1 {
			m.Cursor.Line++
			lineLen := len(m.Lines[m.Cursor.Line])
			if m.StickyCol > lineLen {
				m.Cursor.Col = lineLen
			} else {
				m.Cursor.Col = m.StickyCol
			}
		}
		if !shift {
			m.Selection = Selection{Anchor: m.Cursor, Active: false}
		}

	case "home", "shift+home":
		m.Cursor.Col = 0
		m.StickyCol = 0
		if !shift {
			m.Selection = Selection{Anchor: m.Cursor, Active: false}
		}

	case "end", "shift+end":
		m.Cursor.Col = len(m.Lines[m.Cursor.Line])
		m.StickyCol = m.Cursor.Col
		if !shift {
			m.Selection = Selection{Anchor: m.Cursor, Active: false}
		}

	case "ctrl+left", "ctrl+shift+left":
		m.moveWordLeft()
		if !shift {
			m.Selection = Selection{Anchor: m.Cursor, Active: false}
		}

	case "ctrl+right", "ctrl+shift+right":
		m.moveWordRight()
		if !shift {
			m.Selection = Selection{Anchor: m.Cursor, Active: false}
		}

	case "ctrl+c":
		if m.Selection.Active {
			text := m.SelectedText()
			internalClipboard = text
			_ = clipboard.WriteAll(text)
		}

	case "ctrl+x":
		if m.Selection.Active {
			text := m.SelectedText()
			internalClipboard = text
			_ = clipboard.WriteAll(text)
			m.DeleteSelection()
		}

	case "ctrl+v":
		text, err := clipboard.ReadAll()
		if err != nil || text == "" {
			text = internalClipboard
		}
		if text != "" {
			m.InsertString(text)
		}

	case "ctrl+backspace":
		if !m.DeleteSelection() {
			m.Selection = Selection{Anchor: m.Cursor, Active: true}
			m.moveWordLeft()
			m.DeleteSelection()
		}

	case "ctrl+delete":
		if !m.DeleteSelection() {
			m.Selection = Selection{Anchor: m.Cursor, Active: true}
			m.moveWordRight()
			m.DeleteSelection()
		}

	case "backspace":
		if !m.DeleteSelection() {
			if m.Cursor.Col > 0 {
				line := m.Lines[m.Cursor.Line]
				m.Lines[m.Cursor.Line] = append(line[:m.Cursor.Col-1], line[m.Cursor.Col:]...)
				m.Cursor.Col--
				m.StickyCol = m.Cursor.Col
			} else if m.Cursor.Line > 0 {
				prevLen := len(m.Lines[m.Cursor.Line-1])
				m.Lines[m.Cursor.Line-1] = append(m.Lines[m.Cursor.Line-1], m.Lines[m.Cursor.Line]...)
				m.Lines = append(m.Lines[:m.Cursor.Line], m.Lines[m.Cursor.Line+1:]...)
				m.Cursor.Line--
				m.Cursor.Col = prevLen
				m.StickyCol = prevLen
			}
		}

	case "delete":
		if !m.DeleteSelection() {
			line := m.Lines[m.Cursor.Line]
			if m.Cursor.Col < len(line) {
				m.Lines[m.Cursor.Line] = append(line[:m.Cursor.Col], line[m.Cursor.Col+1:]...)
			} else if m.Cursor.Line < len(m.Lines)-1 {
				m.Lines[m.Cursor.Line] = append(m.Lines[m.Cursor.Line], m.Lines[m.Cursor.Line+1]...)
				m.Lines = append(m.Lines[:m.Cursor.Line+1], m.Lines[m.Cursor.Line+2:]...)
			}
		}

	case "enter":
		m.insertNewline()

	case "tab":
		m.InsertString("  ")

	default:
		if msg.Type == tea.KeyRunes || (len(msg.Runes) > 0 && !unicode.IsControl(msg.Runes[0])) {
			for _, r := range msg.Runes {
				m.InsertRune(r)
			}
		}
	}

	m.ensureCursorVisible()
}

// View renders the visible lines within the viewport height and width.
func (m Model) View() string {
	if len(m.Lines) == 0 {
		return ""
	}
	start, end, hasSelection := m.SelectedSpan()
	var renderedLines []string

	visibleHeight := m.Height
	if visibleHeight < 1 {
		visibleHeight = 1
	}

	for i := 0; i < visibleHeight; i++ {
		lineIdx := m.TopLine + i
		if lineIdx >= len(m.Lines) {
			break
		}
		lineRunes := m.Lines[lineIdx]
		var sb strings.Builder

		for colIdx := 0; colIdx < m.Width; colIdx++ {
			realCol := m.LeftCol + colIdx
			pt := Point{Line: lineIdx, Col: realCol}

			if realCol >= len(lineRunes) {
				isCursor := m.Focused && lineIdx == m.Cursor.Line && realCol == m.Cursor.Col && !hasSelection
				isLineBreakSelected := hasSelection && (pointLess(start, pt) || start == pt) && pointLess(pt, end)

				if isCursor {
					sb.WriteString(lipgloss.NewStyle().Reverse(true).Render(" "))
				} else if isLineBreakSelected {
					sb.WriteString(m.SelectStyle.Render(" "))
				}
				break
			}

			r := lineRunes[realCol]
			isSelected := hasSelection && (pointLess(start, pt) || start == pt) && pointLess(pt, end)
			isCursor := m.Focused && pt == m.Cursor && !hasSelection

			charStr := string(r)
			if unicode.IsControl(r) && r != '\t' {
				charStr = ""
			}
			if isSelected {
				charStr = m.SelectStyle.Render(charStr)
			} else if isCursor {
				charStr = lipgloss.NewStyle().Reverse(true).Render(charStr)
			}

			sb.WriteString(charStr)
		}

		renderedLines = append(renderedLines, sb.String())
	}

	if len(renderedLines) == 0 && len(m.Lines) == 1 && len(m.Lines[0]) == 0 && m.Placeholder != "" {
		return m.Style.Render(m.Placeholder)
	}

	return strings.Join(renderedLines, "\n")
}
