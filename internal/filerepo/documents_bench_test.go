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
	repo, err := New(dir, Options{})
	if err != nil {
		b.Fatal(err)
	}

	docPath := filepath.Join(dir, "benchdoc.md")

	doc := &document.Document{
		Artifact: artifact.Artifact{
			Kind:          artifact.DocumentKind,
			ID:            pathID(artifact.DocumentKind, docPath),
			CreatedAt:     time.Now(),
			ModifiedAt:    time.Now(),
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
func BenchmarkListDocumentChangesHuge(b *testing.B) {
	ctx := context.Background()
	repo, err := New(b.TempDir(), Options{})
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
		Body:   "Hello, World!\n",
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

func BenchmarkGetDocumentChangeHuge(b *testing.B) {
	ctx := context.Background()
	repo, err := New(b.TempDir(), Options{})
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
		Body:   "Hello, World!\n",
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

	changes := make([]document.Change, 100)
	for i := 0; i < 100; i++ {
		c := change
		c.ID = c.ID[:32-3] + string(rune('0'+(i/100))) + string(rune('0'+((i/10)%10))) + string(rune('0'+(i%10)))
		if i < 99 {
			c.Status = document.ChangeAccepted
			now := time.Now().UTC()
			c.ResolvedAt = &now
		}
		changes[i] = c
	}

	err = repo.updateDocument(ctx, path, false, func(current document.Document, _ []document.Change) (document.Document, []document.Change, error) {
		return current, changes, nil
	})
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := repo.GetDocumentChange(ctx, path, "12345678901234567890123456789099")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListDocumentChangesHugeMany(b *testing.B) {
	ctx := context.Background()
	repo, err := New(b.TempDir(), Options{})
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
		Body:   "Hello, World!\n",
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

	changes := make([]document.Change, 100)
	for i := 0; i < 100; i++ {
		c := change
		c.ID = c.ID[:32-3] + string(rune('0'+(i/100))) + string(rune('0'+((i/10)%10))) + string(rune('0'+(i%10)))
		if i < 99 {
			c.Status = document.ChangeAccepted
			now := time.Now().UTC()
			c.ResolvedAt = &now
		}
		changes[i] = c
	}

	err = repo.updateDocument(ctx, path, false, func(current document.Document, _ []document.Change) (document.Document, []document.Change, error) {
		return current, changes, nil
	})
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		changes, err := repo.ListDocumentChanges(ctx, path)
		if err != nil {
			b.Fatal(err)
		}
		found := false
		for _, c := range changes {
			if c.ID == "12345678901234567890123456789099" {
				found = true
				break
			}
		}
		if !found {
			b.Fatal("not found")
		}
	}
}
