package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

type findAction int

const (
	findChanged findAction = iota
	findUnchanged
	findNext
	findPrevious
	findClosed
)

type findBar struct {
	input   textinput.Model
	active  bool
	visible bool
}

type renderedMatch struct {
	page, row, startCol, endCol int
	inNotes                     bool
}

type textMatch struct {
	startByte, endByte int
	startCol, endCol   int
}

func newFindBar(theme Theme) findBar {
	input := textinput.New()
	input.Prompt = "Find: "
	input.Placeholder = "type to search"
	input.CharLimit = 4096
	input.PromptStyle = theme.Accent
	input.TextStyle = theme.Muted
	return findBar{input: input}
}

func (f *findBar) open(width int) tea.Cmd {
	f.active = true
	f.visible = true
	f.input.Width = max(width-32, 1)
	return f.input.Focus()
}

func (f *findBar) close() tea.Cmd {
	f.active = false
	f.visible = false
	f.input.SetValue("")
	f.input.Blur()
	return nil
}

func (f *findBar) blur() {
	f.active = false
	f.input.Blur()
}

func (f *findBar) update(msg tea.KeyMsg) (findAction, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return findClosed, f.close()
	case "enter", "down":
		return findNext, nil
	case "up", "shift+tab":
		return findPrevious, nil
	default:
		old := f.input.Value()
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		if old == f.input.Value() {
			return findUnchanged, cmd
		}
		return findChanged, cmd
	}
}

func (f *findBar) query() string { return f.input.Value() }

func (f *findBar) view(matches, current, width int) string {
	summary := "no matches"
	if matches > 0 {
		summary = fmt.Sprintf("%d/%d", current+1, matches)
	}
	help := "  Enter/↓ next · ↑/Shift+Tab prev · Esc close"
	suffix := "  " + summary + help
	f.input.Width = max(width-runewidth.StringWidth(suffix)-2, 1)
	return ansi.Truncate(f.input.View()+suffix, max(width, 1), "…")
}

func findTextMatches(text, query string) []textMatch {
	queryRunes := []rune(query)
	if len(queryRunes) == 0 {
		return nil
	}
	caseSensitive := false
	for _, r := range queryRunes {
		caseSensitive = caseSensitive || unicode.IsUpper(r)
	}

	runes := make([]rune, 0, len(text))
	offsets := make([]int, 0, len(text)+1)
	for byteOffset, r := range text {
		offsets = append(offsets, byteOffset)
		runes = append(runes, r)
	}
	offsets = append(offsets, len(text))

	var matches []textMatch
	for start := 0; start+len(queryRunes) <= len(runes); {
		matched := true
		for i, queryRune := range queryRunes {
			textRune := runes[start+i]
			if caseSensitive && textRune != queryRune ||
				!caseSensitive && unicode.ToLower(textRune) != unicode.ToLower(queryRune) {
				matched = false
				break
			}
		}
		if !matched {
			start++
			continue
		}
		startByte, endByte := offsets[start], offsets[start+len(queryRunes)]
		startCol := runewidth.StringWidth(text[:startByte])
		endCol := startCol + runewidth.StringWidth(text[startByte:endByte])
		if endCol > startCol {
			matches = append(matches, textMatch{
				startByte: startByte, endByte: endByte,
				startCol: startCol, endCol: endCol,
			})
		}
		start += len(queryRunes)
	}
	return matches
}

func renderedMatches(lines []string, query string, page int) []renderedMatch {
	var matches []renderedMatch
	for row, line := range lines {
		for _, match := range findTextMatches(ansi.Strip(line), query) {
			matches = append(matches, renderedMatch{
				page: page, row: row, startCol: match.startCol, endCol: match.endCol,
			})
		}
	}
	return matches
}

func highlightLine(line, query string, style Theme) string {
	matches := findTextMatches(ansi.Strip(line), query)
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		prefix := ansi.Truncate(line, match.startCol, "")
		middle := ansi.Truncate(ansi.TruncateLeft(line, match.startCol, ""), match.endCol-match.startCol, "")
		suffix := ansi.TruncateLeft(line, match.endCol, "")
		line = prefix + style.Match.Render(ansi.Strip(middle)) + suffix
	}
	return line
}

func highlightLines(lines []string, query string, style Theme) []string {
	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = highlightLine(line, query, style)
	}
	return result
}

func highlightRenderedText(content, query string, style Theme) string {
	return strings.Join(highlightLines(strings.Split(content, "\n"), query, style), "\n")
}
