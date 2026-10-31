package filerepo_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"testing"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/filerepo"
)

func createBenchmarkPNG(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode benchmark png: %v", err)
	}
	return buf.Bytes()
}

func BenchmarkListDocumentChanges(b *testing.B) {
	ctx := context.Background()
	stateDir := b.TempDir()
	repo, err := filerepo.New(stateDir)
	if err != nil {
		b.Fatal(err)
	}

	docPath := filepath.Join(b.TempDir(), "bench_doc.md")
	docs := document.NewService(repo, 10)

	doc, err := docs.Create(ctx, docPath, document.Draft{
		Body: "# Benchmark Document\n\nTesting proposal listing performance.\n",
	})
	if err != nil {
		b.Fatal(err)
	}

	// Add an embedded image to simulate realistic document payload with images
	pngBytes := createBenchmarkPNG(b, 200, 200)
	doc, _, err = docs.AddImage(ctx, docPath, "Sample Image", pngBytes)
	if err != nil {
		b.Fatal(err)
	}

	// Add a proposal
	draft := document.Draft{
		Body:   doc.Body + "\n\nProposed new section with edits.\n",
		Layout: doc.Layout,
		Images: doc.Images,
	}
	_, err = docs.Propose(ctx, doc, "Bench proposal", draft)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		changes, err := repo.ListDocumentChanges(ctx, docPath)
		if err != nil {
			b.Fatalf("ListDocumentChanges: %v", err)
		}
		if len(changes) != 1 {
			b.Fatalf("expected 1 change, got %d", len(changes))
		}
	}
}
