package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ramjac/parchment/internal/artifact"
	"github.com/ramjac/parchment/internal/artifactfile"
	"github.com/ramjac/parchment/internal/document"
	"github.com/ramjac/parchment/internal/filerepo"
	"github.com/ramjac/parchment/internal/presentation"
)

func benchmarkMediaImages(b *testing.B) []artifact.Image {
	b.Helper()
	var images []artifact.Image
	state := uint32(0x12345678)
	for i := 0; i < 8; i++ {
		pixels := image.NewNRGBA(image.Rect(0, 0, 512, 512))
		for p := 0; p < len(pixels.Pix); p += 4 {
			for c := 0; c < 3; c++ {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				pixels.Pix[p+c] = byte(state)
			}
			pixels.Pix[p+3] = 255
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, pixels); err != nil {
			b.Fatal(err)
		}
		img, err := artifact.NewImage(encoded.Bytes())
		if err != nil {
			b.Fatal(err)
		}
		images = append(images, img)
	}
	return images
}

func BenchmarkEmbeddedMedia(b *testing.B) {
	if os.Getenv("PARCHMENT_BENCH_MEDIA") != "1" {
		b.Skip("set PARCHMENT_BENCH_MEDIA=1 to generate the image workload")
	}
	for _, name := range []string{"KITTY_WINDOW_ID", "TERM_PROGRAM", "LC_TERMINAL"} {
		b.Setenv(name, "")
	}
	b.Setenv("TERM", "dumb")
	images := benchmarkMediaImages(b)
	var documentBody, deckSource strings.Builder
	documentBody.WriteString("# Illustrated document\n\n")
	deckSource.WriteString("# Illustrated deck\n\n")
	for i, img := range images {
		fmt.Fprintf(&documentBody, "## Image %d\n\n![Illustration](%s)\n\n", i+1, img.Name)
		fmt.Fprintf(&deckSource, "## Image %d\n\n.image %s 8 24\n\n", i+1, img.Name)
	}
	ctx := context.Background()
	repo, err := filerepo.New(b.TempDir(), filerepo.Options{})
	if err != nil {
		b.Fatal(err)
	}
	docPath := filepath.Join(b.TempDir(), "illustrated-document.md")
	deckPath := filepath.Join(b.TempDir(), "illustrated-deck.md")
	doc, err := document.NewService(repo, 10).Create(ctx, docPath, document.Draft{
		Body: documentBody.String(), Images: images,
	})
	if err != nil {
		b.Fatal(err)
	}
	deck, err := presentation.NewService(repo, 10).Create(ctx, deckPath, "# Illustrated deck\n\n## Start\n")
	if err != nil {
		b.Fatal(err)
	}
	withImages := deck
	withImages.Images = images
	withImages.Source = deckSource.String()
	if err := repo.TransitionPresentation(ctx, deckPath, &deck, &withImages); err != nil {
		b.Fatal(err)
	}
	deck, err = repo.GetPresentation(ctx, deckPath)
	if err != nil {
		b.Fatal(err)
	}
	if doc.Body != documentBody.String() || deck.Source != deckSource.String() ||
		len(doc.Images) != len(images) || len(deck.Images) != len(images) {
		b.Fatal("media artifacts did not round-trip")
	}
	expected := make(map[string][]byte, len(images))
	for _, img := range images {
		expected[img.Name] = img.Data
	}
	for _, collection := range [][]artifact.Image{doc.Images, deck.Images} {
		for _, img := range collection {
			if !bytes.Equal(img.Data, expected[img.Name]) {
				b.Fatal("embedded image bytes did not round-trip")
			}
		}
	}
	for _, path := range []string{docPath, deckPath} {
		info, err := os.Stat(path)
		if err != nil || info.Size() >= artifactfile.MaxFileSize {
			b.Fatalf("invalid media artifact size for %s: %v", path, err)
		}
		b.Logf("%s: %d bytes; %d embedded 512x512 images", filepath.Base(path), info.Size(), len(images))
	}

	for _, size := range []struct{ width, height int }{{60, 24}, {80, 24}, {120, 40}} {
		b.Run(fmt.Sprintf("DocumentReader%dx%d", size.width, size.height), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
				screen.resize(size.width, size.height)
				screen.startReading(doc)
				view := screen.view("")
				if screen.errMessage != "" || !screen.reading {
					b.Fatalf("document reader failed: %s", screen.errMessage)
				}
				if !strings.Contains(ansi.Strip(view), "▀") {
					b.Fatal("document reader did not render the first image")
				}
			}
		})
	}
	b.Run("PresentationSlide80x24", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			model := newPresentationModel(nil, deckPath, Theme{}, 10)
			_, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			model.setItem(deck)
			model.goToPage(1)
			view := model.View().Content
			if !strings.Contains(ansi.Strip(view), "▀") {
				b.Fatal("presentation did not render the first image")
			}
			model.goToPage(2)
			_ = model.View().Content
		}
	})
}
