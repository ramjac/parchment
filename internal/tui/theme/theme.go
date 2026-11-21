package theme

import (
	"image/color"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"

	"example.com/parchment/internal/config"
)

// Theme contains semantic styles shared by TUI screens, editors, and Markdown.
type Theme struct {
	Accent      lipgloss.Style
	Chrome      lipgloss.Style
	Muted       lipgloss.Style
	Error       lipgloss.Style
	Selection   lipgloss.Style
	Match       lipgloss.Style
	Heading     lipgloss.Style
	Link        lipgloss.Style
	Image       lipgloss.Style
	Code        lipgloss.Style
	Cursor      lipgloss.Style
	BorderColor color.Color
	Markdown    ansi.StyleConfig
}

// Resolve maps configured colors and the selected background mode to shared
// Lip Gloss and Glamour styles.
func Resolve(c config.Theme) Theme {
	dark := c.Mode == "dark"
	if c.Mode == "auto" || c.Mode == "" {
		dark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	}

	colors := palette{
		accent:    terminalColor(c.Primary, "#805500", "#f2c66d", dark),
		chrome:    terminalColor("", "#3e796d", "#72bfb0", dark),
		border:    terminalColor(c.Border, "#4f8277", "#64a99a", dark),
		err:       terminalColor(c.Error, "#b42318", "#f87171", dark),
		selection: terminalColor(c.Selection, "#b7d9cf", "#15564d", dark),
		match:     terminalColor(c.Match, "#fde68a", "#854d0e", dark),
		muted:     terminalColor("", "#626a67", "#adb8b3", dark),
		link:      terminalColor("", "#6941a5", "#ae81ff", dark),
		image:     terminalColor("", "#b31961", "#f779b0", dark),
		code:      terminalColor("", "#a63d00", "#fd971f", dark),
		codeBG:    terminalColor("", "#f7f3e8", "#2d2b24", dark),
		selectFG:  terminalColor("", "#17332d", "#ffffff", dark),
		matchFG:   terminalColor("", "#241f35", "#ffffff", dark),
	}

	accentHex := resolvedHex(c.Primary, "#805500", "#f2c66d", dark)
	borderHex := resolvedHex(c.Border, "#4f8277", "#64a99a", dark)
	mutedHex := resolvedHex("", "#626a67", "#adb8b3", dark)
	codeHex := resolvedHex("", "#a63d00", "#fd971f", dark)
	codeBackgroundHex := resolvedHex("", "#f7f3e8", "#2d2b24", dark)
	linkHex := resolvedHex("", "#6941a5", "#ae81ff", dark)
	imageHex := resolvedHex("", "#b31961", "#f779b0", dark)
	listHex := resolvedHex("", "#446c10", "#a6e22e", dark)

	return Theme{
		Accent:      lipgloss.NewStyle().Foreground(colors.accent),
		Chrome:      lipgloss.NewStyle().Foreground(colors.chrome).Bold(true),
		Muted:       lipgloss.NewStyle().Foreground(colors.muted),
		Error:       lipgloss.NewStyle().Foreground(colors.err).Bold(true),
		Selection:   lipgloss.NewStyle().Foreground(colors.selectFG).Background(colors.selection).Bold(true),
		Match:       lipgloss.NewStyle().Foreground(colors.matchFG).Background(colors.match).Bold(true).Underline(true),
		Heading:     lipgloss.NewStyle().Foreground(colors.accent).Bold(true),
		Link:        lipgloss.NewStyle().Foreground(colors.link).Underline(true),
		Image:       lipgloss.NewStyle().Foreground(colors.image),
		Code:        lipgloss.NewStyle().Foreground(colors.code).Background(colors.codeBG),
		Cursor:      lipgloss.NewStyle().Reverse(true),
		BorderColor: colors.border,
		Markdown: markdownStyles(dark, markdownPalette{
			heading: accentHex, border: borderHex, muted: mutedHex,
			code: codeHex, codeBackground: codeBackgroundHex,
			link: linkHex, image: imageHex, list: listHex,
		}),
	}
}

type palette struct {
	accent, border, err, selection, match          color.Color
	chrome, muted, code, codeBG, selectFG, matchFG color.Color
	link, image                                    color.Color
}

func terminalColor(override, light, dark string, darkBackground bool) color.Color {
	if override != "" {
		return lipgloss.Color(override)
	}
	if darkBackground {
		return lipgloss.Color(dark)
	}
	return lipgloss.Color(light)
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

type markdownPalette struct {
	heading, border, muted, code, codeBackground, link, image, list string
}

func markdownStyles(dark bool, colors markdownPalette) ansi.StyleConfig {
	config := glamourstyles.LightStyleConfig
	if dark {
		config = glamourstyles.DarkStyleConfig
	}

	setColor(&config.Heading.StylePrimitive, colors.heading)
	headingPrefixes := []string{"◆ ", "▌ ", "┃ ", "│ ", "┆ ", "┊ "}
	for i, heading := range []*ansi.StyleBlock{
		&config.H1, &config.H2, &config.H3, &config.H4, &config.H5, &config.H6,
	} {
		setColor(&heading.StylePrimitive, colors.heading)
		bold := true
		heading.Bold = &bold
		heading.Prefix = headingPrefixes[i]
		heading.Suffix = ""
		heading.BackgroundColor = nil
	}
	setColor(&config.Link, colors.link)
	underline := true
	config.Link.Underline = &underline
	setColor(&config.LinkText, colors.link)
	config.LinkText.Underline = &underline
	setColor(&config.Image, colors.image)
	setColor(&config.ImageText, colors.image)
	for _, marker := range []*ansi.StylePrimitive{&config.Item, &config.Enumeration} {
		setColor(marker, colors.list)
		// Glamour colors Prefix with the item style, but BlockPrefix with its parent.
		marker.Prefix = marker.BlockPrefix + marker.Prefix
		marker.BlockPrefix = ""
	}
	setColor(&config.BlockQuote.StylePrimitive, colors.muted)
	setColor(&config.Code.StylePrimitive, colors.code)
	setBackground(&config.Code.StylePrimitive, colors.codeBackground)
	setBackground(&config.CodeBlock.StylePrimitive, colors.codeBackground)
	config.CodeBlock.Chroma = nil
	config.CodeBlock.Theme = "github"
	if dark {
		config.CodeBlock.Theme = "monokai"
	}
	setColor(&config.HorizontalRule, colors.border)
	return config
}

func setColor(style *ansi.StylePrimitive, color string) {
	style.Color = &color
}

func setBackground(style *ansi.StylePrimitive, color string) {
	style.BackgroundColor = &color
}
