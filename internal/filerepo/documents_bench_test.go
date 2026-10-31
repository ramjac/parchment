package filerepo

import (
	"context"
	"path/filepath"
	"testing"
    "time"

	"example.com/parchment/internal/document"
    "example.com/parchment/internal/artifact"
)

// A tiny valid 1x1 png image
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0xda, 0x63, 0x64, 0x60, 0x60, 0xf8,
	0xcf, 0xc0, 0x00, 0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xdd, 0x8d, 0xb0,
	0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func BenchmarkListDocumentChanges(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	repo, err := New(dir)
	if err != nil {
		b.Fatal(err)
	}

	docPath := filepath.Join(dir, "benchdoc.md")

	doc := &document.Document{
        Artifact: artifact.Artifact{
            Kind: artifact.DocumentKind,
            ID: pathID(artifact.DocumentKind, docPath),
            CreatedAt: time.Now(),
            ModifiedAt: time.Now(),
            FormatVersion: artifact.FormatVersion,
        },
		Layout: document.DefaultLayout(),
	}
	// Add some images and some large body to simulate real world
	for i := 0; i < 5; i++ {
        // Change color to make hash different
        data := append([]byte(nil), tinyPNG...)
        data[42] = byte(i)

        // Pad data to make it larger (100KB each)
        for j := 0; j < 100000; j++ {
           data = append(data, 0)
        }

        img, err := document.NewImage(data)
        if err != nil {
            b.Fatal(err)
        }
		doc.Images = append(doc.Images, img)
	}

	err = repo.TransitionDocument(ctx, docPath, nil, doc)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := repo.ListDocumentChanges(ctx, docPath)
		if err != nil {
			b.Fatal(err)
		}
	}
}
