package tui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/glamour"
	"github.com/muesli/termenv"

	"example.com/parchment/internal/tui/theme"
)

func repaintImageFrame(view string, frame uint64) string {
	tag := "\x1b_PARCHMENT-REFRESH:" + strconv.FormatUint(frame, 10) + "\x1b\\"
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
		glamour.WithColorProfile(glamourColorProfile(compat.Profile)),
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

func glamourColorProfile(profile colorprofile.Profile) termenv.Profile {
	switch profile {
	case colorprofile.TrueColor:
		return termenv.TrueColor
	case colorprofile.ANSI256:
		return termenv.ANSI256
	case colorprofile.ANSI:
		return termenv.ANSI
	default:
		return termenv.Ascii
	}
}
