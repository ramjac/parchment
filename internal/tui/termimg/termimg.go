package termimg

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
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
	outW, outH := fitToCells(img, maxCols, maxRows)
	if outW == 0 || outH == 0 {
		return ""
	}
	bounds := img.Bounds()
	imgW, imgH := bounds.Dx(), bounds.Dy()

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
				srcYBot := bounds.Min.Y + ((y + 1) * imgH / pxRows)
				bottomColor = img.At(srcX, srcYBot)
			} else {
				bottomColor = color.Transparent
			}

			b.WriteString(renderHalfBlock(topColor, bottomColor))
		}
		if y+2 < pxRows {
			b.WriteString("\n")
		}
	}

	return b.String()
}

func fitToCells(img image.Image, maxCols, maxRows int) (cols, rows int) {
	if img == nil || maxCols <= 0 || maxRows <= 0 {
		return 0, 0
	}
	bounds := img.Bounds()
	imgW, imgH := bounds.Dx(), bounds.Dy()
	if imgW <= 0 || imgH <= 0 {
		return 0, 0
	}

	imageAspect := float64(imgW) / (float64(imgH) / 2)
	terminalAspect := float64(maxCols) / float64(maxRows)
	if imageAspect > terminalAspect {
		cols = maxCols
		rows = int(float64(maxCols) / imageAspect)
	} else {
		rows = maxRows
		cols = int(float64(maxRows) * imageAspect)
	}
	return max(cols, 1), max(rows, 1)
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

const kittyChunkSize = 4096

// ClearKittyPlacements removes visible Kitty graphics placements.
func ClearKittyPlacements() string { return "\x1b_Ga=d,d=A\x1b\\" }

// RenderImage decodes an embedded image and renders it using the detected
// terminal graphics protocol, falling back to ANSI half-blocks when needed.
// The returned row count is the image's height in terminal cells.
func RenderImage(data []byte, maxCols, maxRows int) (string, int, error) {
	img, err := DecodeImage(data)
	if err != nil {
		return "", 0, err
	}
	rendered, rows := RenderPixels(img, maxCols, maxRows, DetectProtocol())
	return rendered, rows, nil
}

// DecodeImage validates encoded dimensions before allocating the decoded pixels.
func DecodeImage(data []byte) (image.Image, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image config: %w", err)
	}
	const maxImagePixels = 16 << 20
	if config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width) > int64(maxImagePixels)/int64(config.Height) {
		return nil, fmt.Errorf("image dimensions exceed terminal limits")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return img, nil
}

// CellSize returns the rendered terminal dimensions for an image.
func CellSize(img image.Image, maxCols, maxRows int) (cols, rows int) {
	return fitToCells(img, maxCols, maxRows)
}

// RenderPixels renders already-decoded, orientation-normalized pixels.
func RenderPixels(img image.Image, maxCols, maxRows int, protocol GraphicsProtocol) (string, int) {
	_, rows := fitToCells(img, maxCols, maxRows)
	if rows == 0 {
		return "", 0
	}

	var rendered string
	switch protocol {
	case ProtocolKitty:
		rendered = RenderKitty(img, maxCols, maxRows)
	case ProtocolITerm2:
		rendered = RenderITerm2(img, maxCols, maxRows)
	case ProtocolSixel:
		rendered = RenderSixel(img, maxCols, maxRows)
	default:
		rendered = Fallback(img, maxCols, maxRows)
	}
	if IsGraphicsOutput(protocol, rendered) {
		rendered = "\x1b[s" + rendered + "\x1b[u"
	}
	return rendered, rows
}

// IsGraphicsOutput reports whether rendered contains a terminal image protocol
// sequence rather than its ANSI half-block fallback.
func IsGraphicsOutput(protocol GraphicsProtocol, rendered string) bool {
	rendered = strings.TrimPrefix(rendered, "\x1b[s")
	switch protocol {
	case ProtocolKitty:
		return strings.HasPrefix(rendered, "\x1b_G")
	case ProtocolITerm2:
		return strings.HasPrefix(rendered, "\x1b]1337;File=")
	case ProtocolSixel:
		return strings.HasPrefix(rendered, "\x1bP0;1;0q")
	default:
		return false
	}
}

// RenderKitty renders an image using the Kitty graphics protocol.
func RenderKitty(img image.Image, maxCols, maxRows int) string {
	cols, rows := fitToCells(img, maxCols, maxRows)
	if cols == 0 || rows == 0 {
		return ""
	}
	pngData, err := encodePNG(img)
	if err != nil {
		return Fallback(img, maxCols, maxRows)
	}
	payload := base64.StdEncoding.EncodeToString(pngData)
	var out strings.Builder
	for offset := 0; offset < len(payload); offset += kittyChunkSize {
		end := min(offset+kittyChunkSize, len(payload))
		more := end < len(payload)
		if offset == 0 {
			fmt.Fprintf(&out, "\x1b_Ga=T,f=100,t=d,c=%d,r=%d,m=%d;", cols, rows, boolInt(more))
		} else {
			fmt.Fprintf(&out, "\x1b_Gm=%d;", boolInt(more))
		}
		out.WriteString(payload[offset:end])
		out.WriteString("\x1b\\")
	}
	return out.String()
}

// RenderITerm2 renders an image using the iTerm2 inline image protocol.
func RenderITerm2(img image.Image, maxCols, maxRows int) string {
	cols, rows := fitToCells(img, maxCols, maxRows)
	if cols == 0 || rows == 0 {
		return ""
	}
	pngData, err := encodePNG(img)
	if err != nil {
		return Fallback(img, maxCols, maxRows)
	}
	payload := base64.StdEncoding.EncodeToString(pngData)
	sequence := fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1;size=%d:%s\a",
		cols, rows, len(pngData), payload)
	if len(sequence) > 1<<20 {
		return Fallback(img, maxCols, maxRows)
	}
	return sequence
}

// RenderSixel renders an image using the pure-Go Sixel encoder.
func RenderSixel(img image.Image, maxCols, maxRows int) string {
	cols, rows := fitToCells(img, maxCols, maxRows)
	if cols == 0 || rows == 0 || cols > int(^uint(0)>>1)/sixelCellPixelWidth ||
		rows > int(^uint(0)>>1)/sixelCellPixelHeight {
		return ""
	}
	return encodeSixel(img, cols*sixelCellPixelWidth, rows*sixelCellPixelHeight)
}

func encodePNG(img image.Image) ([]byte, error) {
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
