package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

func renderMarkdown(content string, width int) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithPreservedNewLines(),
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
