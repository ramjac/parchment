package cli

import (
	"bytes"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/ramjac/parchment/internal/artifact"
)

func TestResolveImageTUITarget(t *testing.T) {
	c := newCLI(t)
	writeCLIImage(t, "photo.txt")
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, assertCLIImage(t, "photo.txt", "png", 6, 4), nil); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		path string
		data []byte
	}{
		{"jpeg-without-extension", jpegData.Bytes()},
		{"truncated.png", []byte("\x89PNG\r\n\x1a\n")},
		{"truncated-jpeg", []byte{0xff, 0xd8, 0xff}},
	} {
		if err := os.WriteFile(tt.path, tt.data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"photo.txt", "jpeg-without-extension", "truncated.png", "truncated-jpeg"} {
		resolved, kind, err := resolveTUITarget(path, "")
		if err != nil || kind != artifact.ImageKind || resolved != filepath.Join(c.dir, path) {
			t.Fatalf("image routing %s: %q, %q, %v", path, resolved, kind, err)
		}
		if _, _, err := resolveTUITarget(path, artifact.NoteKind); err == nil {
			t.Fatal("image accepted as an explicit note")
		}
	}
	if err := os.Symlink("photo.txt", "linked.png"); err == nil {
		if _, kind, err := resolveTUITarget("linked.png", ""); err != nil || kind != artifact.ImageKind {
			t.Fatal("symlink image routing", kind, err)
		}
	}
	if _, kind, err := resolveTUITarget("missing.png", ""); err != nil || kind != "" {
		t.Fatal("missing image must retain kind picker", kind, err)
	}
	if _, _, err := resolveTUITarget("missing.png", artifact.ImageKind); err == nil {
		t.Fatal("allowed creating an image with --kind")
	}
	if _, err := os.Stat("missing.png"); !os.IsNotExist(err) {
		t.Fatal("routing created an image")
	}
	if _, _, err := resolveTUITarget(c.dir, ""); err == nil {
		t.Fatal("directory accepted as image")
	}
}
