package tui

import (
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/document"
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
	word, _, ok := renderedWordHit(line, column)
	if !ok {
		return 0, 0, false
	}
	bestLine, bestColumn, bestDistance := 0, 0, int(^uint(0)>>1)
	for lineNumber, sourceLine := range strings.Split(source, "\n") {
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
				if distance < bestDistance {
					bestLine, bestColumn, bestDistance = lineNumber, start, distance
				}
			}
			start = end
		}
	}
	return bestLine, bestColumn, bestDistance != int(^uint(0)>>1)
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
	if index < 0 || index >= len(outline) {
		return 0
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
				return lineNumber
			}
			occurrence--
		}
	}
	return 0
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
