package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// placeTextareaCursor moves within the currently visible textarea viewport.
// Rendering a copy with a visible cursor lets the textarea itself account for
// soft wrapping and scrolling rather than reimplementing those rules here.
func placeTextareaCursor(editor *textarea.Model, x, y int) bool {
	if y < 0 || y >= editor.Height() {
		return false
	}
	copy := *editor
	copy.Cursor.Blink = false
	lines := strings.Split(copy.View(), "\n")
	currentY := -1
	for row, line := range lines {
		if marker := strings.Index(line, "\x1b[7m"); marker >= 0 {
			currentY = row
			break
		}
	}
	if currentY < 0 {
		before := ansi.Strip(copy.View())
		for moved := 0; moved <= editor.Height(); moved++ {
			if copy.Line() == 0 && copy.LineInfo().RowOffset == 0 {
				currentY = moved
				break
			}
			copy.CursorUp()
			copy, _ = copy.Update(nil)
			after := ansi.Strip(copy.View())
			if after != before {
				currentY = moved
				break
			}
		}
		if currentY < 0 {
			return false
		}
	}
	for currentY < y {
		editor.CursorDown()
		currentY++
	}
	for currentY > y {
		editor.CursorUp()
		currentY--
	}
	info := editor.LineInfo()
	gutter := runewidth.StringWidth(editor.Prompt)
	if editor.ShowLineNumbers {
		gutter += len(strconv.Itoa(editor.MaxHeight)) + 2
	}
	column := max(x-gutter, 0)
	source := []rune(strings.Split(editor.Value(), "\n")[editor.Line()])
	index := min(info.StartColumn, len(source))
	end := min(index+info.Width, len(source))
	for index < end {
		width := max(runewidth.RuneWidth(source[index]), 0)
		if column < width {
			break
		}
		column -= width
		index++
	}
	editor.SetCursor(index)
	return true
}
