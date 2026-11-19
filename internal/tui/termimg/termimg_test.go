package termimg

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
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

}

func TestRenderImageSelectsProtocolAndReturnsCellRows(t *testing.T) {
	var data bytes.Buffer
	source := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			source.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	if err := png.Encode(&data, source); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("LC_TERMINAL", "")
	t.Setenv("TERM", "dumb")

	got, rows, err := RenderImage(data.Bytes(), 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 3 || !strings.Contains(got, "▀") {
		t.Fatalf("fallback render has %d rows and output %q, want 3 rows of half-blocks", rows, got)
	}

	t.Setenv("KITTY_WINDOW_ID", "1")
	got, rows, err = RenderImage(data.Bytes(), 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 3 || !strings.HasPrefix(got, "\x1b[s\x1b_G") || !strings.HasSuffix(got, "\x1b[u") {
		t.Fatalf("Kitty render has %d rows and output prefix %q", rows, got[:min(len(got), 30)])
	}

	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	got, rows, err = RenderImage(data.Bytes(), 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 3 || !strings.HasPrefix(got, "\x1b[s\x1b]1337;File=") || !strings.HasSuffix(got, "\x1b[u") {
		t.Fatalf("iTerm2 render has %d rows and output prefix %q", rows, got[:min(len(got), 30)])
	}

	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm")
	got, rows, err = RenderImage(data.Bytes(), 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 3 || !strings.HasPrefix(got, "\x1b[s\x1bP0;1;0q") || !strings.HasSuffix(got, "\x1b[u") {
		t.Fatalf("Sixel render has %d rows and output prefix %q", rows, got[:min(len(got), 30)])
	}
}

func TestGraphicsProtocolsEncodePNGAtCellDimensions(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x + y), A: 255})
		}
	}

	kitty := RenderKitty(img, 80, 24)
	headers, kittyPayload := parseKitty(t, kitty)
	if len(headers) != 1 || headers[0] != "a=T,f=100,t=d,c=80,r=20,m=0" {
		t.Fatalf("Kitty command headers = %q, want scaled 80x20 single-chunk display", headers)
	}
	assertPNGPayload(t, kittyPayload, 400, 200)

	iterm := RenderITerm2(img, 80, 24)
	const prefix = "\x1b]1337;File=inline=1;width=80;height=20;preserveAspectRatio=1;size="
	if !strings.HasPrefix(iterm, prefix) || !strings.HasSuffix(iterm, "\a") {
		t.Fatalf("iTerm2 sequence has incorrect cell dimensions or framing: %q", iterm[:min(len(iterm), 100)])
	}
	encoded, ok := strings.CutPrefix(strings.TrimSuffix(iterm, "\a"), prefix)
	sizeText, encoded, ok := strings.Cut(encoded, ":")
	if !ok {
		t.Fatal("iTerm2 sequence is missing payload size")
	}
	pngPayload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode iTerm2 payload: %v", err)
	}
	if sizeText != fmt.Sprint(len(pngPayload)) {
		t.Fatalf("iTerm2 payload size = %s, want %d", sizeText, len(pngPayload))
	}
	assertPNGPayload(t, pngPayload, 400, 200)
}

func TestRenderKittyChunksLargePNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 13), G: uint8(y * 17), B: uint8(x*y + x), A: 255})
		}
	}

	headers, payload := parseKitty(t, RenderKitty(img, 40, 20))
	if len(headers) < 2 {
		t.Fatalf("Kitty transfer used %d chunks, want multiple", len(headers))
	}
	for i, header := range headers {
		wantMore := 1
		if i == len(headers)-1 {
			wantMore = 0
		}
		if !strings.HasSuffix(header, fmt.Sprintf("m=%d", wantMore)) {
			t.Errorf("chunk %d header = %q, want m=%d", i, header, wantMore)
		}
	}
	assertPNGPayload(t, payload, 256, 256)
}

func TestRenderSixelQuantizesPaletteAndKeepsTransparency(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{B: 255, A: 255})
	img.SetNRGBA(0, 1, color.NRGBA{G: 255, A: 255})

	got := RenderSixel(img, 2, 1)
	for _, want := range []string{
		"\x1bP0;1;0q",
		`"1;1;16;16`,
		"#0;2;0;0;100",
		"#1;2;0;100;0",
		"#2;2;100;0;0",
		"\x1b\\",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Sixel output missing %q:\n%q", want, got)
		}
	}
}

func TestRenderSixelRunLengthEncodesSolidBands(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	got := RenderSixel(img, 2, 1)
	if !strings.Contains(got, "!16~") {
		t.Fatalf("solid Sixel bands were not run-length encoded: %q", got)
	}
}

func TestGraphicsProtocolRenderersIgnoreInvalidSizes(t *testing.T) {
	for _, render := range []func(image.Image, int, int) string{RenderKitty, RenderITerm2, RenderSixel} {
		if got := render(nil, 10, 10); got != "" {
			t.Errorf("renderer(nil) = %q, want empty", got)
		}
		if got := render(image.NewRGBA(image.Rect(0, 0, 1, 1)), 0, 10); got != "" {
			t.Errorf("renderer with invalid dimensions = %q, want empty", got)
		}
	}
}

func parseKitty(t *testing.T, rendered string) ([]string, []byte) {
	t.Helper()
	var headers []string
	var encoded strings.Builder
	for rendered != "" {
		if !strings.HasPrefix(rendered, "\x1b_G") {
			t.Fatalf("Kitty sequence does not start with APC: %q", rendered[:min(len(rendered), 40)])
		}
		rendered = strings.TrimPrefix(rendered, "\x1b_G")
		end := strings.Index(rendered, "\x1b\\")
		if end < 0 {
			t.Fatal("Kitty sequence is missing ST terminator")
		}
		command := rendered[:end]
		header, payload, ok := strings.Cut(command, ";")
		if !ok {
			t.Fatalf("Kitty chunk is missing header separator: %q", command)
		}
		headers = append(headers, header)
		encoded.WriteString(payload)
		rendered = rendered[end+2:]
	}
	payload, err := base64.StdEncoding.DecodeString(encoded.String())
	if err != nil {
		t.Fatalf("decode Kitty payload: %v", err)
	}
	return headers, payload
}

func assertPNGPayload(t *testing.T, payload []byte, width, height int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("decode protocol PNG: %v", err)
	}
	if bounds := img.Bounds(); bounds.Dx() != width || bounds.Dy() != height {
		t.Fatalf("PNG bounds = %v, want %dx%d", bounds, width, height)
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
