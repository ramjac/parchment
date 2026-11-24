package tui

import (
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/ramjac/parchment/internal/document"
)

type mouseClickTracker struct {
	x, y    int
	at      time.Time
	pending bool
}

func (t *mouseClickTracker) click(x, y int, at time.Time) bool {
	if t.pending && t.x == x && t.y == y && !at.Before(t.at) &&
		at.Sub(t.at) <= 400*time.Millisecond {
		t.pending = false
		return true
	}
	t.x, t.y, t.at, t.pending = x, y, at, true
	return false
}

func (t *mouseClickTracker) reset() {
	t.pending = false
}

func sourcePositionForRenderedWord(line string, column int, source string, preferredLine int) (int, int, bool) {
	start, end := sourceSectionLineRange(source, preferredLine, 0)
	return sourcePositionForRenderedWordInRange(line, column, source, preferredLine, start, end)
}

func sourcePositionForRenderedWordInRange(
	line string, column int, source string, preferredLine, startLine, endLine int,
) (int, int, bool) {
	word, before, after, ok := renderedWordContext(line, column)
	if !ok {
		return 0, 0, false
	}
	bestLine, bestColumn, bestDistance := 0, 0, int(^uint(0)>>1)
	bestContext := -1
	lines := strings.Split(source, "\n")
	startLine = max(0, min(startLine, len(lines)))
	endLine = max(startLine, min(endLine, len(lines)))
	for lineNumber := startLine; lineNumber < endLine; lineNumber++ {
		sourceLine := lines[lineNumber]
		runes := []rune(sourceLine)
		for start := 0; start < len(runes); {
			if !wordRune(runes[start]) {
				start++
				continue
			}
			end := start + 1
			for end < len(runes) && wordRune(runes[end]) {
				end++
			}
			if strings.EqualFold(string(runes[start:end]), word) {
				distance := abs(lineNumber - preferredLine)
				contextScore := sourceWordContextScore(runes, start, before, after)
				if contextScore > bestContext ||
					contextScore == bestContext && distance < bestDistance {
					bestLine, bestColumn, bestContext, bestDistance = lineNumber, start, contextScore, distance
				}
			}
			start = end
		}
	}
	return bestLine, bestColumn, bestContext >= 0
}

func sourceWordContextScore(source []rune, start int, before, after []string) int {
	words := make([]string, 0)
	clicked := -1
	for i := 0; i < len(source); {
		if !wordRune(source[i]) {
			i++
			continue
		}
		wordStart := i
		for i < len(source) && wordRune(source[i]) {
			i++
		}
		if wordStart == start {
			clicked = len(words)
		}
		words = append(words, string(source[wordStart:i]))
	}
	if clicked < 0 {
		return 0
	}
	score := 0
	for distance := 1; distance <= len(before) && clicked-distance >= 0; distance++ {
		if !strings.EqualFold(words[clicked-distance], before[len(before)-distance]) {
			break
		}
		score++
	}
	for distance := 0; distance < len(after) && clicked+distance+1 < len(words); distance++ {
		if !strings.EqualFold(words[clicked+distance+1], after[distance]) {
			break
		}
		score++
	}
	return score
}

func renderedWordContext(line string, column int) (string, []string, []string, bool) {
	word, startCell, ok := renderedWordHit(line, column)
	if !ok {
		return "", nil, nil, false
	}
	runes := []rune(ansi.Strip(line))
	var words []string
	clicked := -1
	cell := 0
	for i := 0; i < len(runes); {
		if !wordRune(runes[i]) {
			cell += runewidth.RuneWidth(runes[i])
			i++
			continue
		}
		start := i
		startColumn := cell
		for i < len(runes) && wordRune(runes[i]) {
			cell += runewidth.RuneWidth(runes[i])
			i++
		}
		words = append(words, string(runes[start:i]))
		if startColumn == startCell {
			clicked = len(words) - 1
		}
	}
	if clicked < 0 {
		return "", nil, nil, false
	}
	return word, words[:clicked], words[clicked+1:], true
}

func sourceSectionLineRange(source string, startLine, boundaryLevel int) (int, int) {
	lines := strings.Split(source, "\n")
	if startLine < 0 || startLine >= len(lines) {
		return 0, len(lines)
	}
	level := sourceHeadingLevel(lines[startLine])
	if level == 0 {
		return 0, len(lines)
	}
	if boundaryLevel == 0 {
		boundaryLevel = level
	}
	endLine := len(lines)
	fence := ""
	for lineNumber := startLine + 1; lineNumber < len(lines); lineNumber++ {
		trimmed := trimLeftSpaces(lines[lineNumber])
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
		if nextLevel := sourceHeadingLevel(trimmed); nextLevel > 0 && nextLevel <= boundaryLevel {
			endLine = lineNumber
			break
		}
	}
	return startLine, endLine
}

func sourceHeadingLevel(line string) int {
	trimmed := trimLeftSpaces(line)
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || len(trimmed) <= level || trimmed[level] != ' ' {
		return 0
	}
	return level
}

// sourcePositionAtViewport finds visible text near the top of a rendered view.
// Rendered Markdown can reflow and hide syntax, so source and display row
// numbers cannot be used interchangeably.
func sourcePositionAtViewport(lines []string, offset, height int, source string, preferredLine int) (int, int, int, bool) {
	for row := max(offset, 0); row < min(len(lines), offset+height); row++ {
		text := []rune(ansi.Strip(lines[row]))
		bestStart, bestLength := -1, 0
		for i := 0; i < len(text); {
			if !wordRune(text[i]) {
				i++
				continue
			}
			start := i
			for i < len(text) && wordRune(text[i]) {
				i++
			}
			if i-start > bestLength {
				bestStart, bestLength = start, i-start
			}
		}
		if bestStart < 0 {
			continue
		}
		column := runewidth.StringWidth(string(text[:bestStart]))
		if line, col, found := sourcePositionForRenderedWord(lines[row], column, source, preferredLine); found {
			return line, col, row - offset, true
		}
	}
	return 0, 0, 0, false
}

func renderedWordHit(line string, column int) (string, int, bool) {
	runes := []rune(ansi.Strip(line))
	cell, clicked := 0, -1
	startCell := 0
	for i, r := range runes {
		width := runewidth.RuneWidth(r)
		if width > 0 && column >= cell && column < cell+width {
			clicked = i
			break
		}
		cell += width
	}
	if clicked < 0 || !wordRune(runes[clicked]) {
		return "", 0, false
	}
	start, end := clicked, clicked+1
	for start > 0 && wordRune(runes[start-1]) {
		start--
	}
	for _, r := range runes[:start] {
		startCell += runewidth.RuneWidth(r)
	}
	for end < len(runes) && wordRune(runes[end]) {
		end++
	}
	return string(runes[start:end]), startCell, true
}

func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_'
}

func sourceLineForSection(source string, outline []document.Section, index int) int {
	line, _ := findSourceLineForSection(source, outline, index)
	return line
}

func findSourceLineForSection(source string, outline []document.Section, index int) (int, bool) {
	if index < 0 || index >= len(outline) {
		return 0, false
	}
	target := outline[index]
	occurrence := 0
	for i := 0; i < index; i++ {
		if outline[i].Level == target.Level && strings.EqualFold(outline[i].Title, target.Title) {
			occurrence++
		}
	}
	fence := ""
	for lineNumber, line := range strings.Split(source, "\n") {
		trimmed := trimLeftSpaces(line)
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
		level := 0
		for level < len(trimmed) && trimmed[level] == '#' {
			level++
		}
		if level != target.Level || level == 0 || len(trimmed) <= level || trimmed[level] != ' ' {
			continue
		}
		title := strings.TrimSpace(trimmed[level:])
		title = strings.TrimRight(title, "# ")
		title = normalizeSourceHeading(title)
		if strings.EqualFold(title, target.Title) {
			if occurrence == 0 {
				return lineNumber, true
			}
			occurrence--
		}
	}
	return 0, false
}

func normalizeSourceHeading(title string) string {
	runes := []rune(title)
	var normalized strings.Builder
	for i, r := range runes {
		if r == '*' || r == '`' || r == '~' {
			continue
		}
		if r == '_' && (i == 0 || i == len(runes)-1 ||
			!wordRune(runes[i-1]) || !wordRune(runes[i+1])) {
			continue
		}
		normalized.WriteRune(r)
	}
	return normalized.String()
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
