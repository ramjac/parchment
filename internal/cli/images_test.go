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

	"example.com/parchment/internal/imageedit"
)

func writeCLIImage(t *testing.T, path string) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 6, 4))
	img.SetNRGBA(1, 1, color.NRGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func assertCLIImage(t *testing.T, path, format string, width, height int) *imageedit.Image {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := imageedit.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if img.Format() != format || img.Bounds().Size() != image.Pt(width, height) {
		t.Fatalf("%s: format=%s size=%v", path, img.Format(), img.Bounds().Size())
	}
	return img
}

func TestImageCLI(t *testing.T) {
	c := newCLI(t)
	source := filepath.Join(c.dir, "input.bin")
	original := writeCLIImage(t, source)
	if out := c.must("image", "crop", source, "--x", "1", "--y", "1", "--width", "2", "--height", "2", "-o", "crop.png"); out != "" {
		t.Fatalf("unexpected output %q", out)
	}
	crop := assertCLIImage(t, "crop.png", "png", 2, 2)
	if color.NRGBAModel.Convert(crop.At(0, 0)).(color.NRGBA).R != 255 {
		t.Fatal("CLI crop used the wrong coordinates")
	}
	c.must("image", "resize", source, "--width", "3", "-o", "resized.JPEG", "--quality", "100")
	assertCLIImage(t, "resized.JPEG", "jpeg", 3, 2)
	c.must("image", "resize", "resized.JPEG", "--height", "4", "-o", "converted.png")
	assertCLIImage(t, "converted.png", "png", 6, 4)
	c.must("image", "resize", source, "--width", "2", "--height", "5", "-o", "exact.png", "--force")
	assertCLIImage(t, "exact.png", "png", 2, 5)
	if _, err := c.run("image", "resize", source, "--width", "1", "-o", "exact.png"); err == nil {
		t.Fatal("overwrote existing output")
	}
	c.must("image", "resize", source, "--width", "1", "-o", "exact.png", "--force")
	assertCLIImage(t, "exact.png", "png", 1, 1)
	if got, _ := os.ReadFile(source); !bytes.Equal(got, original) {
		t.Fatal("save-as changed the source")
	}
	c.must("image", "crop", source, "--x", "0", "--y", "0", "--width", "3", "--height", "2", "--in-place", "--yes")
	assertCLIImage(t, source, "png", 3, 2)
	c.must("image", "resize", "resized.JPEG", "--width", "6", "--in-place", "--yes")
	assertCLIImage(t, "resized.JPEG", "jpeg", 6, 4)
}

func TestImageCLIRejectsInvalidArguments(t *testing.T) {
	c := newCLI(t)
	original := writeCLIImage(t, "input.png")
	for _, args := range [][]string{
		{"resize", "input.png", "--width", "2"},
		{"resize", "input.png", "--width", "2", "-o", "out.png", "--in-place"},
		{"resize", "input.png", "--width", "2", "--in-place"},
		{"resize", "input.png", "--width", "2", "--in-place", "--force", "--yes"},
		{"resize", "input.png", "--width", "2", "-o", "out.png", "--yes"},
		{"resize", "input.png", "-o", "out.png"},
		{"resize", "input.png", "--width", "0", "-o", "out.png"},
		{"resize", "input.png", "--width", "-1", "-o", "out.png"},
		{"resize", "input.png", "--width", "2", "--height", "0", "-o", "out.png"},
		{"resize", "input.png", "--width", "2", "--quality", "0", "-o", "out.png"},
		{"resize", "input.png", "--width", "2", "--quality", "101", "-o", "out.png"},
		{"resize", "input.png", "--width", "16385", "-o", "out.png"},
		{"resize", "input.png", "--width", "2", "-o", "out.gif"},
		{"resize", "input.png", "--width", "2", "-o", "input.png", "--force"},
		{"crop", "input.png", "--width", "2", "--height", "2", "-o", "out.png"},
		{"crop", "input.png", "--x", "0", "--y", "0", "--width", "2", "-o", "out.png"},
		{"crop", "input.png", "--x", "-1", "--y", "0", "--width", "2", "--height", "2", "-o", "out.png"},
		{"crop", "input.png", "--x", "5", "--y", "0", "--width", "2", "--height", "2", "-o", "out.png"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := c.run(append([]string{"image"}, args...)...); err == nil {
				t.Fatalf("accepted %v", args)
			}
			if data, _ := os.ReadFile("input.png"); !bytes.Equal(data, original) {
				t.Fatal("invalid command changed input")
			}
			if _, err := os.Stat("out.png"); !os.IsNotExist(err) {
				t.Fatal("invalid command created output")
			}
		})
	}
}

func TestImageConfirmation(t *testing.T) {
	for _, tt := range []struct {
		answer string
		want   bool
	}{{"y\n", true}, {"YES\n", true}, {"n\n", false}, {"\n", false}, {"", false}, {"yes", true}, {"something\n", false}} {
		var out bytes.Buffer
		got, err := confirmImageEdit(strings.NewReader(tt.answer), &out, "photo.png")
		if err != nil || got != tt.want || !strings.Contains(out.String(), "[y/N]") {
			t.Fatalf("answer %q: %v, %v, %q", tt.answer, got, err, out.String())
		}
	}
}
