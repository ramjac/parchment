package theme

import (
	"github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"

	"example.com/parchment/internal/config"
)

// Theme contains semantic styles shared by TUI screens, editors, and Markdown.
type Theme struct {
	Accent      lipgloss.Style
	Muted       lipgloss.Style
	Error       lipgloss.Style
	Selection   lipgloss.Style
	Match       lipgloss.Style
	Heading     lipgloss.Style
	Code        lipgloss.Style
	Cursor      lipgloss.Style
	BorderColor lipgloss.TerminalColor
	Markdown    ansi.StyleConfig
}

// Resolve maps configured colors and the selected background mode to shared
// Lip Gloss and Glamour styles.
func Resolve(c config.Theme) Theme {
	dark := c.Mode == "dark"
	if c.Mode == "auto" || c.Mode == "" {
		dark = lipgloss.HasDarkBackground()
	}

	colors := palette{
		accent:    terminalColor(c.Primary, "#4b3f72", "#c4b5fd", c.Mode),
		border:    terminalColor(c.Border, "#b8b4c7", "#55516a", c.Mode),
		err:       terminalColor(c.Error, "#b42318", "#f87171", c.Mode),
		selection: terminalColor(c.Selection, "#dcd6f7", "#4c1d95", c.Mode),
		match:     terminalColor(c.Match, "#fde68a", "#854d0e", c.Mode),
		muted:     terminalColor("", "#62616a", "#b8b4c7", c.Mode),
		code:      terminalColor("", "#6d28d9", "#c4b5fd", c.Mode),
		codeBG:    terminalColor("", "#f2effa", "#252331", c.Mode),
		selectFG:  terminalColor("", "#241f35", "#ffffff", c.Mode),
		matchFG:   terminalColor("", "#241f35", "#ffffff", c.Mode),
	}

	accentHex := resolvedHex(c.Primary, "#4b3f72", "#c4b5fd", dark)
	borderHex := resolvedHex(c.Border, "#b8b4c7", "#55516a", dark)
	mutedHex := resolvedHex("", "#62616a", "#b8b4c7", dark)
	codeHex := resolvedHex("", "#6d28d9", "#c4b5fd", dark)
	codeBackgroundHex := resolvedHex("", "#f2effa", "#252331", dark)

	return Theme{
		Accent:      lipgloss.NewStyle().Foreground(colors.accent),
		Muted:       lipgloss.NewStyle().Foreground(colors.muted),
		Error:       lipgloss.NewStyle().Foreground(colors.err).Bold(true),
		Selection:   lipgloss.NewStyle().Foreground(colors.selectFG).Background(colors.selection).Bold(true).Underline(true),
		Match:       lipgloss.NewStyle().Foreground(colors.matchFG).Background(colors.match).Bold(true).Underline(true),
		Heading:     lipgloss.NewStyle().Foreground(colors.accent).Bold(true),
		Code:        lipgloss.NewStyle().Foreground(colors.code).Background(colors.codeBG),
		Cursor:      lipgloss.NewStyle().Reverse(true),
		BorderColor: colors.border,
		Markdown:    markdownStyles(dark, accentHex, borderHex, mutedHex, codeHex, codeBackgroundHex),
	}
}

type palette struct {
	accent, border, err, selection, match  lipgloss.TerminalColor
	muted, code, codeBG, selectFG, matchFG lipgloss.TerminalColor
}

func terminalColor(override, light, dark, mode string) lipgloss.TerminalColor {
	if override != "" {
		return lipgloss.Color(override)
	}
	switch mode {
	case "light":
		return lipgloss.Color(light)
	case "dark":
		return lipgloss.Color(dark)
	default:
		return lipgloss.AdaptiveColor{Light: light, Dark: dark}
	}
}

func resolvedHex(override, light, dark string, darkBackground bool) string {
	if override != "" {
		return override
	}
	if darkBackground {
		return dark
	}
	return light
}

func markdownStyles(dark bool, accent, border, muted, code, codeBackground string) ansi.StyleConfig {
	config := glamourstyles.LightStyleConfig
	if dark {
		config = glamourstyles.DarkStyleConfig
	}

	setColor(&config.Heading.StylePrimitive, accent)
	headingPrefixes := []string{"◆ ", "▌ ", "┃ ", "│ ", "┆ ", "┊ "}
	for i, heading := range []*ansi.StyleBlock{
		&config.H1, &config.H2, &config.H3, &config.H4, &config.H5, &config.H6,
	} {
		setColor(&heading.StylePrimitive, accent)
		bold := true
		heading.Bold = &bold
		heading.Prefix = headingPrefixes[i]
		heading.Suffix = ""
		heading.BackgroundColor = nil
	}
	setColor(&config.Link, accent)
	underline := true
	config.Link.Underline = &underline
	setColor(&config.LinkText, accent)
	setColor(&config.BlockQuote.StylePrimitive, muted)
	setColor(&config.Code.StylePrimitive, code)
	setBackground(&config.CodeBlock.StylePrimitive, codeBackground)
	setColor(&config.HorizontalRule, border)
	return config
}

func setColor(style *ansi.StylePrimitive, color string) {
	style.Color = &color
}

func setBackground(style *ansi.StylePrimitive, color string) {
	style.BackgroundColor = &color
}
