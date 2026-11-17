package cli

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPresentationCLI(t *testing.T) {
	c := newCLI(t)
	must, run := c.must, c.run
	sourcePath := filepath.Join(t.TempDir(), "talk.md")
	source := "# A Talk\n\n## Welcome\n\nHello **everyone**.\n// private comment\n: speaker note\n\n## Wrap Up\n\nThanks.\n"
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	id := "talk.md"
	if out := strings.TrimSpace(must("presentation", "create", id, "--body-file", sourcePath)); !strings.HasSuffix(out, id) {
		t.Fatalf("create printed %q", out)
	}
	if got := must("presentation", "show", id); got != source {
		t.Fatalf("show source = %q, want %q", got, source)
	}
	preview := must("presentation", "preview", id)
	for _, want := range []string{"Welcome", "Hello **everyone**.", "Wrap Up", "Thanks."} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview missing %q:\n%s", want, preview)
		}
	}
	if strings.Contains(preview, "speaker note") || strings.Contains(preview, "private comment") {
		t.Fatalf("preview exposed notes or comments:\n%s", preview)
	}
	editedPath := filepath.Join(t.TempDir(), "edited.md")
	editedSource := "# Renamed Talk\n\n## Welcome\n\nUpdated text.\n"
	if err := os.WriteFile(editedPath, []byte(editedSource), 0o600); err != nil {
		t.Fatal(err)
	}
	must("presentation", "edit", id, "--body-file", editedPath)
	if out := must("presentation", "show", id); out != editedSource {
		t.Fatalf("edited source = %q", out)
	}
	if _, err := run("presentation", "create", id, "--body-file", sourcePath); err == nil {
		t.Fatal("create overwrote an existing presentation")
	}
	must("presentation", "create", "blank.md")
	if out := must("presentation", "show", "blank.md"); !strings.HasPrefix(out, "# blank\n") {
		t.Fatalf("default presentation source = %q", out)
	}
}

func TestPresentationImageCLI(t *testing.T) {
	c := newCLI(t)
	must, run := c.must, c.run

	// Create a valid image file
	imgRect := image.Rect(0, 0, 10, 10)
	img := image.NewRGBA(imgRect)
	for x := 0; x < 10; x++ {
		for y := 0; y < 10; y++ {
			img.Set(x, y, color.Black)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	imgData := buf.Bytes()
	imgPath := filepath.Join(t.TempDir(), "test.png")
	if err := os.WriteFile(imgPath, imgData, 0o600); err != nil {
		t.Fatal(err)
	}

	must("presentation", "create", "pres.md")

	out := must("presentation", "image", "pres.md", imgPath)
	name := strings.TrimSpace(out)
	if !strings.HasPrefix(name, "image-") {
		t.Fatalf("expected image name to start with image-, got %q", name)
	}

	out = must("presentation", "images", "pres.md")
	if !strings.Contains(out, name) || !strings.Contains(out, "10x10") {
		t.Fatalf("images output missing name or size: %q", out)
	}

	extractPath := filepath.Join(t.TempDir(), "extracted.png")
	must("presentation", "image-extract", "pres.md", name, "-o", extractPath)
	extracted, err := os.ReadFile(extractPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(extracted, imgData) {
		t.Fatal("extracted image data does not match original")
	}

	// Try removing without strip references
	if _, err := run("presentation", "image-remove", "pres.md", name); err == nil {
		t.Fatal("expected remove to fail without strip references")
	}

	// Remove with strip references
	must("presentation", "image-remove", "pres.md", name, "--strip-references")
	out = must("presentation", "images", "pres.md")
	if strings.Contains(out, name) {
		t.Fatalf("image still present after remove: %q", out)
	}
}
