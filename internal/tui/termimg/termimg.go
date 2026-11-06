package termimg

import (
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Fallback renders the image as a string of truecolor ANSI half-blocks.
// The image is scaled proportionally to fit within maxCols and maxRows
// (measured in terminal cells). Because a terminal cell is approximately
// twice as tall as it is wide, and each half-block represents two vertical pixels,
// one cell can render a 1x2 block of pixels without distortion.
func Fallback(img image.Image, maxCols, maxRows int) string {
    // If the image is nil or dimensions are non-positive, return an empty string.
    if img == nil || maxCols <= 0 || maxRows <= 0 {
        return ""
    }

    bounds := img.Bounds()
    imgW, imgH := bounds.Dx(), bounds.Dy()
    if imgW == 0 || imgH == 0 {
        return ""
    }

    // Calculate aspect ratios
    imgAspect := float64(imgW) / (float64(imgH) / 2.0)
    termAspect := float64(maxCols) / float64(maxRows)

    var outW, outH int
    if imgAspect > termAspect {
        // Image is wider than the terminal view
        outW = maxCols
        outH = int(float64(maxCols) / imgAspect)
    } else {
        // Image is taller than the terminal view
        outH = maxRows
        outW = int(float64(maxRows) * imgAspect)
    }

    if outW <= 0 { outW = 1 }
    if outH <= 0 { outH = 1 }

    // The number of pixel rows we need is outH * 2
    pxRows := outH * 2
    pxCols := outW

    var b strings.Builder

    for y := 0; y < pxRows; y += 2 {
        for x := 0; x < pxCols; x++ {
            srcX := bounds.Min.X + (x * imgW / pxCols)
            srcYTop := bounds.Min.Y + (y * imgH / pxRows)

            topColor := img.At(srcX, srcYTop)

            var bottomColor color.Color
            if y+1 < pxRows {
                srcYBot := bounds.Min.Y + ((y+1) * imgH / pxRows)
                bottomColor = img.At(srcX, srcYBot)
            } else {
                bottomColor = color.Transparent
            }

            b.WriteString(renderHalfBlock(topColor, bottomColor))
        }
        if y + 2 < pxRows {
            b.WriteString("\n")
        }
    }

    return b.String()
}

func renderHalfBlock(top, bottom color.Color) string {
    _, _, _, aT := top.RGBA()
    _, _, _, aB := bottom.RGBA()

    if aT == 0 && aB == 0 {
        return " "
    }

    style := lipgloss.NewStyle()
    char := "▀"

    if aT > 0 {
        style = style.Foreground(lipgloss.Color(colorToHex(top)))
    } else {
        char = "▄"
        if aB > 0 {
            style = style.Foreground(lipgloss.Color(colorToHex(bottom)))
        }
        return style.Render(char)
    }

    if aB > 0 {
        style = style.Background(lipgloss.Color(colorToHex(bottom)))
    }

    return style.Render(char)
}

func colorToHex(c color.Color) string {
    r, g, b, _ := c.RGBA()
    return fmt.Sprintf("#%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

// GraphicsProtocol describes the terminal image rendering capability
type GraphicsProtocol int

const (
	ProtocolNone GraphicsProtocol = iota
	ProtocolKitty
	ProtocolITerm2
	ProtocolSixel
)

// DetectProtocol checks environment variables to determine terminal support for graphics
func DetectProtocol() GraphicsProtocol {
	// The no-terminal-query rule from Section 5.1 means we rely entirely on environment variables.

	term := os.Getenv("TERM")
	termProgram := os.Getenv("TERM_PROGRAM")
	kittyWinID := os.Getenv("KITTY_WINDOW_ID")
	lcTerminal := os.Getenv("LC_TERMINAL")

	// Kitty protocol
	if kittyWinID != "" || strings.Contains(term, "kitty") {
		return ProtocolKitty
	}

	// iTerm2 protocol
	if termProgram == "iTerm.app" || lcTerminal == "iTerm2" || termProgram == "WezTerm" {
		return ProtocolITerm2
	}

	// Sixel protocol
	// E.g., Xterm, mlterm, yaft, foot, libsixel, etc.
	// We'll approximate this by checking TERM or checking well-known sixel terminals.
	// In the exact instructions: "Detect Kitty, Sixel, and iTerm2 graphics support strictly via environment variables (KITTY_WINDOW_ID, TERM, TERM_PROGRAM, LC_TERMINAL..."

	// E.g. xterm can support sixel, mlterm, foot
	if strings.Contains(term, "xterm") ||
	   strings.Contains(term, "mlterm") ||
	   strings.Contains(term, "foot") {
		// This is a naive detection but fits the "detect ... strictly via environment variables" rule
		// Without terminal queries, accurate sixel detection is hard, but we do our best based on TERM.
		// For the sake of the PR, we'll implement the shell of the renderers and just let the PR implement the details.
		return ProtocolSixel
	}

	return ProtocolNone
}

// RenderKitty renders the image using the Kitty graphics protocol
func RenderKitty(img image.Image, maxCols, maxRows int) string {
	// ... Need to implement 15b
	return Fallback(img, maxCols, maxRows)
}

// RenderITerm2 renders the image using the iTerm2 graphics protocol
func RenderITerm2(img image.Image, maxCols, maxRows int) string {
	// ... Need to implement 15b
	return Fallback(img, maxCols, maxRows)
}

// RenderSixel renders the image using the pure-Go Sixel encoder
func RenderSixel(img image.Image, maxCols, maxRows int) string {
	// ... Need to implement 15c
	return Fallback(img, maxCols, maxRows)
}
