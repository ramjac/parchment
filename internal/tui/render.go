package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"example.com/parchment/internal/tui/theme"
)

func repaintImageFrame(view string, frame uint64) string {
	// Equivalent SGR resets invalidate Bubble Tea's line cache without
	// clearing the terminal.
	bits := strconv.FormatUint(frame, 2)
	tag := "\x1b[" + strings.NewReplacer("0", "0;", "1", "00;").Replace(bits) + "0m"
	lines := strings.Split(view, "\n")
	for i := range lines {
		lines[i] += tag
	}
	return strings.Join(lines, "\n")
}

func renderMarkdown(content string, width int, theme theme.Theme) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(theme.Markdown),
		glamour.WithPreservedNewLines(),
		glamour.WithColorProfile(lipgloss.ColorProfile()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return content
	}

	out, err := r.Render(content)
	if err != nil {
		return content
	}

	return strings.TrimRight(out, "\n")
}
