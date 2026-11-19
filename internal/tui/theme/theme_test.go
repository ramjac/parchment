package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"example.com/parchment/internal/config"
)

func TestResolveAppliesConfiguredColorsToSemanticRoles(t *testing.T) {
	got := Resolve(config.Theme{
		Mode:      "dark",
		Primary:   "#123456",
		Border:    "#234567",
		Error:     "#345678",
		Selection: "#456789",
		Match:     "#56789a",
	})

	if got.Accent.GetForeground() != lipgloss.Color("#123456") {
		t.Errorf("accent color = %v, want #123456", got.Accent.GetForeground())
	}
	if got.BorderColor != lipgloss.Color("#234567") {
		t.Errorf("border color = %v, want #234567", got.BorderColor)
	}
	if got.Error.GetForeground() != lipgloss.Color("#345678") {
		t.Errorf("error color = %v, want #345678", got.Error.GetForeground())
	}
	if got.Selection.GetBackground() != lipgloss.Color("#456789") {
		t.Errorf("selection background = %v, want #456789", got.Selection.GetBackground())
	}
	if got.Match.GetBackground() != lipgloss.Color("#56789a") {
		t.Errorf("match background = %v, want #56789a", got.Match.GetBackground())
	}
	for name, color := range map[string]*string{
		"heading": got.Markdown.H1.Color,
		"link":    got.Markdown.Link.Color,
	} {
		if color == nil || *color != "#123456" {
			t.Errorf("%s color = %v, want #123456", name, color)
		}
	}
}

func TestResolveSelectsMarkdownPaletteForConfiguredMode(t *testing.T) {
	tests := []struct {
		name, mode, heading, codeBackground string
	}{
		{"light", "light", "#4b3f72", "#f2effa"},
		{"dark", "dark", "#c4b5fd", "#252331"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(config.Theme{Mode: tt.mode})
			if got.Markdown.H1.Color == nil || *got.Markdown.H1.Color != tt.heading {
				t.Fatalf("heading color = %v, want %s", got.Markdown.H1.Color, tt.heading)
			}
			if got.Markdown.CodeBlock.BackgroundColor == nil || *got.Markdown.CodeBlock.BackgroundColor != tt.codeBackground {
				t.Fatalf("code block background = %v, want %s", got.Markdown.CodeBlock.BackgroundColor, tt.codeBackground)
			}
		})
	}
}

func TestMarkdownHeadingsHideSourceMarkers(t *testing.T) {
	got := Resolve(config.Theme{Mode: "dark"}).Markdown
	for name, heading := range map[string]struct {
		prefix, suffix string
	}{
		"H1": {got.H1.Prefix, got.H1.Suffix},
		"H2": {got.H2.Prefix, got.H2.Suffix},
		"H3": {got.H3.Prefix, got.H3.Suffix},
		"H4": {got.H4.Prefix, got.H4.Suffix},
		"H5": {got.H5.Prefix, got.H5.Suffix},
		"H6": {got.H6.Prefix, got.H6.Suffix},
	} {
		if strings.Contains(heading.prefix, "#") || strings.Contains(heading.suffix, "#") {
			t.Errorf("%s exposes Markdown heading markers: prefix=%q suffix=%q", name, heading.prefix, heading.suffix)
		}
	}
}

func TestResolveUsesAdaptiveColorsInAutoMode(t *testing.T) {
	got := Resolve(config.Theme{Mode: "auto"})
	if _, ok := got.Accent.GetForeground().(lipgloss.AdaptiveColor); !ok {
		t.Fatalf("auto accent color = %T, want lipgloss.AdaptiveColor", got.Accent.GetForeground())
	}
	if got.Markdown.H1.Color == nil ||
		(*got.Markdown.H1.Color != "#4b3f72" && *got.Markdown.H1.Color != "#c4b5fd") {
		t.Fatalf("auto Markdown heading color = %v, want a light or dark palette color", got.Markdown.H1.Color)
	}
}

func TestSemanticRolesRenderExpectedTruecolorANSI(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
	})

	styles := Resolve(config.Theme{Mode: "dark"})
	tests := []struct {
		name, rendered, ansiColor string
	}{
		{"accent", styles.Accent.Render("title"), "38;2;195;181;253"},
		{"error", styles.Error.Render("error"), "38;2;248;113;113"},
		{"selection foreground", styles.Selection.Render("selected"), "38;2;255;255;255"},
		{"selection background", styles.Selection.Render("selected"), "48;2;76;29;149"},
		{"match background", styles.Match.Render("match"), "48;2;133;77;14"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ansi.Strip(tt.rendered) == tt.rendered || !strings.Contains(tt.rendered, tt.ansiColor) {
				t.Fatalf("rendered role %q lacks expected ANSI color %q: %q", tt.name, tt.ansiColor, tt.rendered)
			}
		})
	}
}

func TestSemanticSelectionFallsBackToPlainTextWithoutANSI(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
	})

	rendered := Resolve(config.Theme{Mode: "dark"}).Selection.Render("selected")
	if rendered != "selected" {
		t.Fatalf("monochrome selection = %q, want plain text without unsupported ANSI", rendered)
	}
}
