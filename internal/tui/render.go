package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"example.com/parchment/internal/tui/theme"
)

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
