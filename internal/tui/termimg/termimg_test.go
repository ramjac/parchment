package termimg

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

func TestFallback(t *testing.T) {
	// Test nil image and invalid dimensions
	if Fallback(nil, 10, 10) != "" {
		t.Errorf("Expected empty string for nil image")
	}
	emptyImg := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if Fallback(emptyImg, 10, 10) != "" {
		t.Errorf("Expected empty string for zero-sized image")
	}
	if Fallback(image.NewRGBA(image.Rect(0, 0, 4, 4)), 0, 10) != "" {
		t.Errorf("Expected empty string for maxCols <= 0")
	}
	if Fallback(image.NewRGBA(image.Rect(0, 0, 4, 4)), 10, 0) != "" {
		t.Errorf("Expected empty string for maxRows <= 0")
	}

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

	// Test transparent blocks
	transImg := image.NewRGBA(image.Rect(0, 0, 2, 2))
	resTrans := Fallback(transImg, 2, 1)
	if !strings.Contains(resTrans, " ") {
		t.Errorf("Expected space for transparent pixel block, got: %q", resTrans)
	}

	// Test stubs fall back
	if RenderKitty(img, 2, 1) != res {
		t.Errorf("RenderKitty should fall back to Fallback")
	}
	if RenderITerm2(img, 2, 1) != res {
		t.Errorf("RenderITerm2 should fall back to Fallback")
	}
	if RenderSixel(img, 2, 1) != res {
		t.Errorf("RenderSixel should fall back to Fallback")
	}
}

func TestDetectProtocol(t *testing.T) {
	clearEnv := func() {
		t.Setenv("KITTY_WINDOW_ID", "")
		t.Setenv("TERM_PROGRAM", "")
		t.Setenv("LC_TERMINAL", "")
		t.Setenv("TERM", "")
	}

	clearEnv()
	t.Setenv("KITTY_WINDOW_ID", "1")
	if DetectProtocol() != ProtocolKitty {
		t.Errorf("Expected Kitty protocol")
	}

	clearEnv()
	t.Setenv("TERM", "xterm-kitty")
	if DetectProtocol() != ProtocolKitty {
		t.Errorf("Expected Kitty protocol via TERM=xterm-kitty")
	}

	clearEnv()
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	if DetectProtocol() != ProtocolITerm2 {
		t.Errorf("Expected iTerm2 protocol via TERM_PROGRAM")
	}

	clearEnv()
	t.Setenv("LC_TERMINAL", "iTerm2")
	if DetectProtocol() != ProtocolITerm2 {
		t.Errorf("Expected iTerm2 protocol via LC_TERMINAL")
	}

	clearEnv()
	t.Setenv("TERM_PROGRAM", "WezTerm")
	if DetectProtocol() != ProtocolITerm2 {
		t.Errorf("Expected iTerm2 protocol via WezTerm")
	}

	clearEnv()
	t.Setenv("TERM", "xterm")
	if DetectProtocol() != ProtocolSixel {
		t.Errorf("Expected Sixel protocol")
	}

	clearEnv()
	t.Setenv("TERM", "foot")
	if DetectProtocol() != ProtocolSixel {
		t.Errorf("Expected Sixel protocol via foot")
	}

	clearEnv()
	t.Setenv("TERM", "dumb")
	if DetectProtocol() != ProtocolNone {
		t.Errorf("Expected None protocol")
	}
}
