package termimg

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

func TestFallback(t *testing.T) {
	// Create a 4x4 image
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))

	// Top half (2 rows) red
	red := color.RGBA{255, 0, 0, 255}
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, red)
		}
	}

	// Bottom half (2 rows) blue
	blue := color.RGBA{0, 0, 255, 255}
	for y := 2; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, blue)
		}
	}

	// Test basic Fallback scaling
	// Scale to 2x1 cells (should be 2 columns wide, 1 row tall)
	// A 1 cell high image shows 2 pixel rows. So outH=1 means 2 pixel rows.
	// Since the aspect ratio of the image is 4x(4/2) = 4x2 = 2.0
	// maxCols=2, maxRows=1 -> terminal aspect ratio is 2.0

	res := Fallback(img, 2, 1)
	if !strings.Contains(res, "▀") {
		t.Errorf("Expected half blocks in fallback output, got: %q", res)
	}
	if strings.Count(res, "\n") != 0 {
		t.Errorf("Expected 1 row, got more lines in: %q", res)
	}
}

func TestDetectProtocol(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	if DetectProtocol() != ProtocolKitty {
		t.Errorf("Expected Kitty protocol")
	}

	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	if DetectProtocol() != ProtocolITerm2 {
		t.Errorf("Expected iTerm2 protocol")
	}

	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm")
	if DetectProtocol() != ProtocolSixel {
		t.Errorf("Expected Sixel protocol")
	}

	t.Setenv("TERM", "dumb")
	if DetectProtocol() != ProtocolNone {
		t.Errorf("Expected None protocol")
	}
}
