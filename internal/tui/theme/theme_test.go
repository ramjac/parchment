package theme

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	if got.Heading.GetForeground() != lipgloss.Color("#123456") {
		t.Errorf("content heading color = %v, want #123456", got.Heading.GetForeground())
	}
	if got.Chrome.GetForeground() != lipgloss.Color("#72bfb0") {
		t.Errorf("TUI chrome color = %v, want #72bfb0", got.Chrome.GetForeground())
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
	if got.Markdown.H1.Color == nil || *got.Markdown.H1.Color != "#123456" {
		t.Errorf("heading color = %v, want #123456", got.Markdown.H1.Color)
	}
	if got.Markdown.Link.Color == nil || *got.Markdown.Link.Color != "#ae81ff" {
		t.Errorf("primary override changed the independent link color: %v", got.Markdown.Link.Color)
	}
}

func TestResolveSelectsSemanticPaletteForConfiguredMode(t *testing.T) {
	tests := []struct {
		name, mode, content, chrome, border, selection, code, codeBackground string
	}{
		{"light", "light", "#805500", "#3e796d", "#4f8277", "#b7d9cf", "#a63d00", "#f7f3e8"},
		{"dark", "dark", "#f2c66d", "#72bfb0", "#64a99a", "#15564d", "#fd971f", "#2d2b24"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(config.Theme{Mode: tt.mode})
			if got.Accent.GetForeground() != lipgloss.Color(tt.content) ||
				got.Heading.GetForeground() != lipgloss.Color(tt.content) {
				t.Fatalf("content accent/heading = %v/%v, want %s",
					got.Accent.GetForeground(), got.Heading.GetForeground(), tt.content)
			}
			if got.Chrome.GetForeground() != lipgloss.Color(tt.chrome) {
				t.Fatalf("TUI chrome = %v, want %s", got.Chrome.GetForeground(), tt.chrome)
			}
			if got.BorderColor != lipgloss.Color(tt.border) {
				t.Fatalf("chrome border = %v, want %s", got.BorderColor, tt.border)
			}
			if got.Selection.GetBackground() != lipgloss.Color(tt.selection) {
				t.Fatalf("selection background = %v, want %s", got.Selection.GetBackground(), tt.selection)
			}
			if got.Code.GetForeground() != lipgloss.Color(tt.code) {
				t.Fatalf("code foreground = %v, want %s", got.Code.GetForeground(), tt.code)
			}
			if got.Markdown.H1.Color == nil || *got.Markdown.H1.Color != tt.content {
				t.Fatalf("heading color = %v, want %s", got.Markdown.H1.Color, tt.content)
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

func TestResolveUsesExplicitColorsInAutoMode(t *testing.T) {
	got := Resolve(config.Theme{Mode: "auto"})
	accent := got.Accent.GetForeground()
	chrome := got.Chrome.GetForeground()
	if accent != lipgloss.Color("#805500") && accent != lipgloss.Color("#f2c66d") {
		t.Fatalf("auto accent color = %v, want an explicit light or dark palette color", accent)
	}
	if chrome != lipgloss.Color("#3e796d") && chrome != lipgloss.Color("#72bfb0") {
		t.Fatalf("auto chrome color = %v, want an explicit light or dark palette color", chrome)
	}
	if got.Markdown.H1.Color == nil ||
		(*got.Markdown.H1.Color != "#805500" && *got.Markdown.H1.Color != "#f2c66d") {
		t.Fatalf("auto Markdown heading color = %v, want a light or dark palette color", got.Markdown.H1.Color)
	}
}

func TestAutoDarkUsesEnvironmentWithoutTerminalIO(t *testing.T) {
	for _, tt := range []struct {
		value string
		dark  bool
	}{{"", true}, {"15;0", true}, {"0;15", false}, {"0;231", false}, {"15;16", true}, {"0;255", false}, {"bad", true}} {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("COLORFGBG", tt.value)
			if got := AutoDark(); got != tt.dark {
				t.Fatalf("AutoDark() = %t, want %t", got, tt.dark)
			}
		})
	}
}

func TestResolveBackgroundRespectsExplicitMode(t *testing.T) {
	got := ResolveBackground(config.Theme{Mode: "light"}, true)
	if got.Heading.GetForeground() != lipgloss.Color("#805500") {
		t.Fatal("terminal response overrode an explicit light theme")
	}
	got = ResolveBackground(config.Theme{Mode: "auto"}, false)
	if got.Heading.GetForeground() != lipgloss.Color("#805500") {
		t.Fatal("auto mode did not use the reported light background")
	}
}

func TestContentAndChromeStayDistinct(t *testing.T) {
	for _, mode := range []string{"light", "dark"} {
		styles := Resolve(config.Theme{Mode: mode})
		if styles.Heading.GetForeground() == styles.Chrome.GetForeground() {
			t.Errorf("mode %s: content and chrome share a foreground color", mode)
		}
	}
}

func TestContentKindsUseDistinctColors(t *testing.T) {
	for _, mode := range []string{"light", "dark"} {
		styles := Resolve(config.Theme{Mode: mode})
		seen := map[color.Color]string{}
		for name, style := range map[string]lipgloss.Style{
			"heading": styles.Heading, "link": styles.Link,
			"image": styles.Image, "code": styles.Code, "chrome": styles.Chrome,
		} {
			color := style.GetForeground()
			if other, ok := seen[color]; ok {
				t.Errorf("mode %s: %s and %s share a foreground color", mode, name, other)
			}
			seen[color] = name
		}
	}
}

func TestSemanticRolesRenderExpectedTruecolorANSI(t *testing.T) {
	styles := Resolve(config.Theme{Mode: "dark"})
	if styles.Selection.GetUnderline() {
		t.Fatal("selection style underlines cells")
	}
	tests := []struct {
		name, rendered, ansiColor string
	}{
		{"accent", styles.Accent.Render("title"), "38;2;242;198;109"},
		{"error", styles.Error.Render("error"), "38;2;248;113;113"},
		{"selection foreground", styles.Selection.Render("selected"), "38;2;255;255;255"},
		{"selection background", styles.Selection.Render("selected"), "48;2;21;86;77"},
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
	selection := Resolve(config.Theme{Mode: "dark"}).Selection
	if selection.GetForeground() != lipgloss.Color("#ffffff") ||
		selection.GetBackground() != lipgloss.Color("#15564d") {
		t.Fatalf("monochrome selection lost its explicit colors: %v", selection)
	}
}
