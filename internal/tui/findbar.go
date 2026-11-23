package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

var errReaderSearchLimit = errors.New("too many matches; refine the search")

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
	setTextInputStyles(&input, theme.Chrome, theme.Muted)
	return findBar{input: input}
}

func setTextInputStyles(input *textinput.Model, prompt, text lipgloss.Style) {
	styles := input.Styles()
	styles.Focused.Prompt, styles.Blurred.Prompt = prompt, prompt
	styles.Focused.Text, styles.Blurred.Text = text, text
	input.SetStyles(styles)
}

func (f *findBar) open(width int) tea.Cmd {
	f.active = true
	f.visible = true
	f.input.SetWidth(max(width-32, 1))
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

func (f *findBar) update(msg tea.Msg) (findAction, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			return findClosed, f.close()
		case "enter", "down":
			return findNext, nil
		case "up", "shift+tab":
			return findPrevious, nil
		}
	}
	old := f.input.Value()
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	if old == f.input.Value() {
		return findUnchanged, cmd
	}
	return findChanged, cmd
}

func (f *findBar) query() string { return f.input.Value() }

func (f *findBar) view(matches, current, width int) string {
	summary := "no matches"
	if matches > 0 {
		summary = fmt.Sprintf("%d/%d", current+1, matches)
	}
	help := "  Enter/↓ next · ↑/Shift+Tab prev · Esc close"
	suffix := "  " + summary + help
	f.input.SetWidth(max(width-runewidth.StringWidth(suffix)-2, 1))
	return ansi.Truncate(f.input.View()+suffix, max(width, 1), "…")
}

type textMatcher struct {
	query         []rune
	caseSensitive bool
}

func newTextMatcher(query string) textMatcher {
	m := textMatcher{query: []rune(query)}
	for _, r := range m.query {
		m.caseSensitive = m.caseSensitive || unicode.IsUpper(r)
	}
	return m
}

func (m textMatcher) matches(text string) []textMatch {
	matches, _ := m.matchesContext(nil, text)
	return matches
}

func (m textMatcher) matchesContext(ctx context.Context, text string) ([]textMatch, error) {
	var matches []textMatch
	err := m.eachMatchContext(ctx, text, func(start, end int) error {
		startCol := runewidth.StringWidth(text[:start])
		matches = append(matches, textMatch{
			startByte: start, endByte: end,
			startCol: startCol, endCol: startCol + runewidth.StringWidth(text[start:end]),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, err
}

func (m textMatcher) eachMatchContext(ctx context.Context, text string, visit func(int, int) error) error {
	if len(m.query) == 0 {
		return nil
	}
	nextCheck := 0
	for start := 0; start < len(text); {
		if ctx != nil && start >= nextCheck {
			if err := ctx.Err(); err != nil {
				return err
			}
			nextCheck = start + 4096
		}
		matched := true
		end := start
		for _, queryRune := range m.query {
			if end == len(text) {
				matched = false
				break
			}
			textRune, size := utf8.DecodeRuneInString(text[end:])
			if m.caseSensitive && textRune != queryRune ||
				!m.caseSensitive && unicode.ToLower(textRune) != unicode.ToLower(queryRune) {
				matched = false
				break
			}
			end += size
		}
		if !matched {
			_, size := utf8.DecodeRuneInString(text[start:])
			start += size
			continue
		}
		if runewidth.StringWidth(text[start:end]) > 0 {
			if err := visit(start, end); err != nil {
				return err
			}
		}
		start = end
	}
	return nil
}

// Reader navigation needs match rows, not their columns; highlighting computes
// columns only for the visible lines. The limit bounds retained search state.
func renderedReaderMatchesContext(ctx context.Context, lines []string, query string, limit int) ([]renderedMatch, error) {
	var matches []renderedMatch
	matcher := newTextMatcher(query)
	for row, line := range lines {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := matcher.eachMatchContext(ctx, ansi.Strip(line), func(_, _ int) error {
			if len(matches) >= limit {
				return errReaderSearchLimit
			}
			matches = append(matches, renderedMatch{row: row})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return matches, nil
}

func findTextMatches(text, query string) []textMatch {
	return newTextMatcher(query).matches(text)
}

func renderedMatches(lines []string, query string, page int) []renderedMatch {
	matches, _ := renderedMatchesContext(nil, lines, query, page)
	return matches
}

func renderedMatchesContext(ctx context.Context, lines []string, query string, page int) ([]renderedMatch, error) {
	var matches []renderedMatch
	matcher := newTextMatcher(query)
	for row, line := range lines {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		lineMatches, err := matcher.matchesContext(ctx, ansi.Strip(line))
		if err != nil {
			return nil, err
		}
		for _, match := range lineMatches {
			matches = append(matches, renderedMatch{
				page: page, row: row, startCol: match.startCol, endCol: match.endCol,
			})
		}
	}
	return matches, nil
}

func highlightLine(line, query string, style Theme) string {
	return highlightLineWithMatcher(line, newTextMatcher(query), style)
}

func highlightLineWithMatcher(line string, matcher textMatcher, style Theme) string {
	matches := matcher.matches(ansi.Strip(line))
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
	matcher := newTextMatcher(query)
	for i, line := range lines {
		result[i] = highlightLineWithMatcher(line, matcher, style)
	}
	return result
}

func highlightRenderedText(content, query string, style Theme) string {
	return strings.Join(highlightLines(strings.Split(content, "\n"), query, style), "\n")
}
