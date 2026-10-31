package filerepo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

func BenchmarkListDocumentChanges(b *testing.B) {
	dir := b.TempDir()
	repo, err := New(dir)
	if err != nil {
		b.Fatalf("failed to create repo: %v", err)
	}
	path := filepath.Join(dir, "bench_doc.md")

	validPNG := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
		0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}

	// Make images larger
	largeData := make([]byte, 1024*1024)
	copy(largeData, validPNG)

	img1, _ := document.NewImage(validPNG)

	validPNG2 := make([]byte, len(validPNG))
	copy(validPNG2, validPNG)
	validPNG2[45] = 0x02
	img2, _ := document.NewImage(validPNG2)

	bodyData := "Test body\n"
	for i := 0; i < 1000; i++ {
		bodyData += "More text here to simulate a very large document.\n"
	}

	doc := &document.Document{
		Artifact: artifact.Artifact{Path: path, Kind: artifact.DocumentKind, ID: pathID(artifact.DocumentKind, path), CreatedAt: time.Now(), ModifiedAt: time.Now(), FormatVersion: artifact.FormatVersion},
		Body:     bodyData,
		Layout:   document.DefaultLayout(),
		Images: []document.Image{
			img1,
			img2,
		},
	}

	err = repo.TransitionDocument(context.Background(), path, nil, doc)
	if err != nil {
		b.Fatalf("failed to create document: %v", err)
	}

	doc2, err := repo.GetDocument(context.Background(), path)
	if err != nil {
		b.Fatalf("failed to get document: %v", err)
	}

	afterDoc := doc2
	afterDoc.Body = doc2.Body + "\nEdited"

	err = repo.ProposeDocumentChange(context.Background(), path, doc2, document.Change{
		ID: "0123456789abcdef0123456789abcdef", DocumentID: doc2.ID, Status: document.ChangePending, Before: document.ChangeSnapshotOf(doc2), After: document.ChangeSnapshotOf(afterDoc),
		Description: "test change", CreatedAt: time.Now(),
	})
	if err != nil {
		b.Fatalf("failed to propose change: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := repo.ListDocumentChanges(context.Background(), path)
		if err != nil {
			b.Fatalf("failed to list changes: %v", err)
		}
	}
}
