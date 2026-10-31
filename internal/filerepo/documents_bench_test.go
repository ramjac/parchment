package filerepo

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

func BenchmarkListDocumentChanges(b *testing.B) {
	ctx := context.Background()
	repo, err := New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}

	path := filepath.Join(b.TempDir(), "test.md")

	id, err := artifact.NewID(artifact.DocumentKind)
	if err != nil {
		b.Fatal(err)
	}

	now := time.Now().UTC()
	doc := &document.Document{
		Artifact: artifact.Artifact{
			ID:            id,
			Kind:          artifact.DocumentKind,
			FormatVersion: artifact.FormatVersion,
			CreatedAt:     now,
			ModifiedAt:    now,
		},
		Layout: document.DefaultLayout(),
		Body: "Hello, World!\n",
	}

	var imageNames []string
	for i := 0; i < 5; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 100, 100))
		for x := 0; x < 100; x++ {
			for y := 0; y < 100; y++ {
				img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(i), 255})
			}
		}
		var buf bytes.Buffer
		png.Encode(&buf, img)

		docImage, err := document.NewImage(buf.Bytes())
		if err != nil {
			b.Fatal(err)
		}
		doc.Images = append(doc.Images, docImage)
		imageNames = append(imageNames, docImage.Name)
		doc.Body += document.ImageMarkdown(docImage.Name, docImage.Name) + "\n"
	}

	change := document.Change{
		ID:          "12345678901234567890123456789012",
		DocumentID:  id,
		Description: "test change",
		CreatedAt:   time.Now().UTC(),
		Status:      document.ChangePending,
		Before:      document.ChangeSnapshot{Body: "old", Layout: document.DefaultLayout()},
		After:       document.ChangeSnapshot{Body: "new", Layout: document.DefaultLayout()},
	}

	err = repo.TransitionDocument(ctx, path, nil, doc)
	if err != nil {
		b.Fatal(err)
	}

	d, err := repo.GetDocument(ctx, path)
	if err != nil {
		b.Fatal(err)
	}

	change.DocumentID = d.ID
	change.Before.Body = d.Body
	change.Before.Layout = d.Layout
	change.Before.ImageNames = imageNames

	err = repo.ProposeDocumentChange(ctx, path, d, change)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := repo.ListDocumentChanges(ctx, path)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListDocumentChangesHuge(b *testing.B) {
	ctx := context.Background()
	repo, err := New(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}

	path := filepath.Join(b.TempDir(), "test.md")

	id, err := artifact.NewID(artifact.DocumentKind)
	if err != nil {
		b.Fatal(err)
	}

	now := time.Now().UTC()
	doc := &document.Document{
		Artifact: artifact.Artifact{
			ID:            id,
			Kind:          artifact.DocumentKind,
			FormatVersion: artifact.FormatVersion,
			CreatedAt:     now,
			ModifiedAt:    now,
		},
		Layout: document.DefaultLayout(),
		Body: "Hello, World!\n",
	}

	var imageNames []string
	for i := 0; i < 50; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 100, 100))
		for x := 0; x < 100; x++ {
			for y := 0; y < 100; y++ {
				img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(i), 255})
			}
		}
		var buf bytes.Buffer
		png.Encode(&buf, img)

		docImage, err := document.NewImage(buf.Bytes())
		if err != nil {
			b.Fatal(err)
		}
		doc.Images = append(doc.Images, docImage)
		imageNames = append(imageNames, docImage.Name)
		doc.Body += document.ImageMarkdown(docImage.Name, docImage.Name) + "\n"
	}

	change := document.Change{
		ID:          "12345678901234567890123456789012",
		DocumentID:  id,
		Description: "test change",
		CreatedAt:   time.Now().UTC(),
		Status:      document.ChangePending,
		Before:      document.ChangeSnapshot{Body: "old", Layout: document.DefaultLayout()},
		After:       document.ChangeSnapshot{Body: "new", Layout: document.DefaultLayout()},
	}

	err = repo.TransitionDocument(ctx, path, nil, doc)
	if err != nil {
		b.Fatal(err)
	}

	d, err := repo.GetDocument(ctx, path)
	if err != nil {
		b.Fatal(err)
	}

	change.DocumentID = d.ID
	change.Before.Body = d.Body
	change.Before.Layout = d.Layout
	change.Before.ImageNames = imageNames

	err = repo.ProposeDocumentChange(ctx, path, d, change)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := repo.ListDocumentChanges(ctx, path)
		if err != nil {
			b.Fatal(err)
		}
	}
}
