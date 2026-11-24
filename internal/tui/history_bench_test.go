package tui

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ramjac/parchment/internal/artifact"
	"github.com/ramjac/parchment/internal/filerepo"
	"github.com/ramjac/parchment/internal/presentation"
	"github.com/ramjac/parchment/internal/recovery"
	"github.com/ramjac/parchment/internal/spreadsheet"
	"github.com/ramjac/parchment/internal/tui/editor"
)

//go:embed testdata/us-history.md
var historyArtifact []byte

func TestHistoryPresentationFixture(t *testing.T) {
	item, err := presentation.Decode(historyArtifact)
	if err != nil {
		t.Fatal(err)
	}
	deck, err := presentation.Parse(item.Source)
	if err != nil {
		t.Fatal(err)
	}
	if len(deck.Slides) != 251 {
		t.Fatalf("got %d slides, want 251", len(deck.Slides))
	}
	if len(item.Images) != 25 || len(item.Workbook.Charts) != 25 {
		t.Fatalf("got %d embedded images and %d charts", len(item.Images), len(item.Workbook.Charts))
	}
	images := make(map[string]bool)
	for _, img := range item.Images {
		if err := img.Validate(); err != nil {
			t.Fatal(err)
		}
		images[img.Name] = true
	}
	charts := make(map[string]spreadsheet.Chart)
	for _, chart := range item.Workbook.Charts {
		data, err := spreadsheet.EvaluateChartInWorkbook(&item.Workbook, chart)
		if err != nil || len(data.Labels) != 2 || len(data.Series) != 1 || len(data.Series[0].Values) != 2 {
			t.Fatalf("chart %q did not evaluate: %+v, %v", chart.ID, data, err)
		}
		charts[chart.ID] = chart
	}
	for i, slide := range deck.Slides {
		body := strings.SplitN(strings.TrimSpace(slide.Body), "\n\n", 2)
		year := strconv.Itoa(1776 + i)
		prefix := "Source: https://en.wikipedia.org/w/index.php?title=" + year + "_in_the_United_States&oldid="
		note := strings.SplitN(strings.TrimSpace(slide.Notes), "\n", 2)[0]
		revision, ok := strings.CutPrefix(note, prefix)
		revisionNumber, err := strconv.ParseUint(revision, 10, 64)
		if slide.Title != year || len(body) != 2 ||
			!strings.HasPrefix(body[0], "**") || !strings.HasSuffix(body[0], "**") ||
			len(strings.Fields(body[1])) < 20 || !ok || err != nil || revisionNumber == 0 {
			t.Fatalf("invalid slide %d: %+v", i, slide)
		}
		number := i + 1
		bulletCount := strings.Count("\n"+slide.Body, "\n- ")
		if (number%3 == 0 && bulletCount != 3) || (number%3 != 0 && bulletCount != 0) {
			t.Fatalf("slide %s has %d bullets", year, bulletCount)
		}
		imageCount := strings.Count(slide.Body, ".image ")
		chartRef := "![Population context](parchment-chart:population-" + year + ")"
		if number%10 == 0 {
			if imageCount != 0 || !strings.Contains(slide.Body, chartRef) || charts["population-"+year].ID == "" {
				t.Fatalf("slide %s is missing its graph or has an image", year)
			}
		} else if number%5 == 0 {
			if imageCount != 1 || strings.Contains(slide.Body, "parchment-chart:") {
				t.Fatalf("slide %s is missing its image or has a graph", year)
			}
			line := strings.SplitN(strings.SplitN(slide.Body, ".image ", 2)[1], "\n", 2)[0]
			if fields := strings.Fields(line); len(fields) == 0 || !images[fields[0]] {
				t.Fatalf("slide %s references a missing image", year)
			}
		} else if imageCount != 0 || strings.Contains(slide.Body, "parchment-chart:") {
			t.Fatalf("unexpected media on slide %s", year)
		}
	}
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	service := presentation.NewService(repo, 10)
	fixture, err := service.Get(context.Background(), filepath.Join("testdata", "us-history.md"))
	if err != nil || fixture.Source != item.Source || !reflect.DeepEqual(fixture.Images, item.Images) ||
		!reflect.DeepEqual(fixture.Workbook, item.Workbook) {
		t.Fatalf("repository history presentation did not load: %v", err)
	}
	path := filepath.Join(t.TempDir(), "history.md")
	if err := os.WriteFile(path, historyArtifact, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Get(context.Background(), path)
	if err != nil || loaded.Source != item.Source || !reflect.DeepEqual(loaded.Images, item.Images) ||
		!reflect.DeepEqual(loaded.Workbook, item.Workbook) {
		t.Fatalf("history presentation did not round-trip: %v", err)
	}
	updated, err := service.Update(context.Background(), loaded, loaded.Source+"\n")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = service.Get(context.Background(), path)
	if err != nil || loaded.Source != updated.Source || !reflect.DeepEqual(loaded.Images, item.Images) ||
		!reflect.DeepEqual(loaded.Workbook, item.Workbook) {
		t.Fatalf("history presentation lost media after save: %v", err)
	}
	for _, name := range []string{"KITTY_WINDOW_ID", "TERM_PROGRAM", "LC_TERMINAL"} {
		t.Setenv(name, "")
	}
	t.Setenv("TERM", "dumb")
	model := newPresentationModel(service, path, Theme{}, 10)
	_, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model.setItem(loaded)
	model.goToPage(5)
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, "▀") {
		t.Fatalf("illustration did not render on slide 5: %q", view)
	}
	model.goToPage(10)
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, "1780") || !strings.Contains(view, "1790") ||
		strings.Contains(view, "parchment-chart:") {
		t.Fatalf("chart did not render on slide 10: %q", view)
	}
	model.goToPage(15)
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, "federal government assumed") || !strings.Contains(view, "▀") {
		t.Fatalf("bullet list and illustration did not render on slide 15: %q", view)
	}
	model.goToPage(30)
	if view := ansi.Strip(model.View().Content); !strings.Contains(view, "expedition crossed") || !strings.Contains(view, "1800") ||
		!strings.Contains(view, "1810") {
		t.Fatalf("bullet list and chart did not render on slide 30: %q", view)
	}
	for number := 5; number <= len(deck.Slides); number += 5 {
		model.goToPage(number)
		view := ansi.Strip(model.View().Content)
		if number%10 != 0 {
			if !strings.Contains(view, "▀") {
				t.Fatalf("illustration did not render on slide %d", number)
			}
			continue
		}
		year := strconv.Itoa(1775 + number)
		data, err := spreadsheet.EvaluateChartInWorkbook(&loaded.Workbook, charts["population-"+year])
		if err != nil || !strings.Contains(view, data.Labels[0]) || !strings.Contains(view, data.Labels[1]) {
			t.Fatalf("chart did not render on slide %d: %v", number, err)
		}
	}
}

func BenchmarkHistoryPresentation(b *testing.B) {
	item, err := presentation.Decode(historyArtifact)
	if err != nil {
		b.Fatal(err)
	}
	source := item.Source
	repo, err := filerepo.New(b.TempDir(), filerepo.Options{})
	if err != nil {
		b.Fatal(err)
	}
	service := presentation.NewService(repo, 10)
	ctx := context.Background()
	path := filepath.Join(b.TempDir(), "history.md")
	if err := os.WriteFile(path, historyArtifact, 0600); err != nil {
		b.Fatal(err)
	}
	item, err = service.Get(ctx, path)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("251 slides, %d source bytes", len(source))
	noMedia := presentationWithoutEmbeddedMedia(item)
	if err := presentation.Validate(noMedia); err != nil {
		b.Fatal(err)
	}
	withMediaData, err := presentation.Encode(item)
	if err != nil {
		b.Fatal(err)
	}
	withoutMediaData, err := presentation.Encode(noMedia)
	if err != nil {
		b.Fatal(err)
	}
	for _, workload := range []struct {
		name string
		item presentation.Presentation
		data []byte
	}{
		{name: "WithMedia", item: item, data: withMediaData},
		{name: "WithoutMedia", item: noMedia, data: withoutMediaData},
	} {
		b.Run("Encode"+workload.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := presentation.Encode(workload.item); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("Decode"+workload.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := presentation.Decode(workload.data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("Load", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := service.Get(ctx, path); err != nil {
				b.Fatal(err)
			}
		}
	})
	for _, size := range []struct{ width, height int }{{80, 24}, {120, 40}} {
		b.Run(fmt.Sprintf("Navigate/%dx%d", size.width, size.height), func(b *testing.B) {
			model := newPresentationModel(service, path, Theme{}, 10)
			_, _ = model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			model.setItem(item)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				model.goToPage(1)
				_ = model.View().Content
				model.goToPage(126)
				_ = model.View().Content
				model.goToPage(251)
				_ = model.View().Content
			}
			b.StopTimer()
			if model.page != 251 || model.item.Source != source {
				b.Fatal("slide navigation changed the presentation or lost its position")
			}
		})
	}
	b.Run("MixedContent/80x24", func(b *testing.B) {
		model := newPresentationModel(service, path, Theme{}, 10)
		_, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		model.setItem(item)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, page := range []int{3, 5, 10, 15, 30} {
				model.goToPage(page)
				_ = model.View().Content
			}
		}
		b.StopTimer()
		if model.page != 30 || model.item.Source != source {
			b.Fatal("mixed-content navigation changed the presentation or lost its position")
		}
	})
	b.Run("OpenSourceEditor", func(b *testing.B) {
		model := newPresentationModel(service, path, Theme{}, 10)
		_, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		model.setItem(item)
		model.goToPage(126)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = model.beginEdit()
		}
		b.StopTimer()
		if model.editor.Value() != source {
			b.Fatal("source editor did not preserve the presentation")
		}
	})
	b.Run("EditSave", func(b *testing.B) {
		item, err = service.Get(ctx, path)
		if err != nil {
			b.Fatal(err)
		}
		if item.Source != source {
			item, err = service.Update(ctx, item, source)
			if err != nil {
				b.Fatal(err)
			}
		}
		model := newPresentationModel(service, path, Theme{}, 10)
		_, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		model.setItem(item)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			model.goToPage(126)
			_ = model.beginEdit()
			model.editor.SetCursorAtRow(editor.Point{Line: 0, Col: 2}, 0)
			if i%2 == 0 {
				_, _ = model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			} else {
				_, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDelete})
			}
			_, cmd := model.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			if cmd == nil {
				b.Fatalf("presentation save did not start: %s", model.errMessage)
			}
			_, _ = model.Update(cmd())
			if model.errMessage != "" || model.editing {
				b.Fatalf("presentation save failed: %s", model.errMessage)
			}
		}
		b.StopTimer()
		loaded, err := service.Get(ctx, path)
		if err != nil || loaded.Source != model.item.Source ||
			!reflect.DeepEqual(loaded.Images, model.item.Images) || !reflect.DeepEqual(loaded.Workbook, model.item.Workbook) {
			b.Fatalf("saved presentation did not round-trip: %v", err)
		}
		item = model.item
	})
	b.Run("Save", func(b *testing.B) {
		current, err := service.Get(ctx, path)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			next := source
			if current.Source == source {
				next += "\n: benchmark edit\n"
			}
			current, err = service.Update(ctx, current, next)
			if err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		loaded, err := service.Get(ctx, path)
		if err != nil || loaded.Source != current.Source ||
			!reflect.DeepEqual(loaded.Images, current.Images) || !reflect.DeepEqual(loaded.Workbook, current.Workbook) {
			b.Fatalf("saved source did not round-trip: %v", err)
		}
	})
	noMediaPath := filepath.Join(b.TempDir(), "history-no-media.md")
	noMediaItem, err := service.Create(ctx, noMediaPath, noMedia.Source)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("SaveNoMedia", func(b *testing.B) {
		current, err := service.Get(ctx, noMediaPath)
		if err != nil {
			b.Fatal(err)
		}
		initialSource := current.Source
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			next := initialSource
			if current.Source == initialSource {
				next += "\n: benchmark edit\n"
			}
			current, err = service.Update(ctx, current, next)
			if err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		loaded, err := service.Get(ctx, noMediaPath)
		if err != nil || loaded.Source != current.Source || len(loaded.Images) != 0 ||
			len(loaded.Workbook.Charts) != 0 || !noMediaItem.Workbook.Equal(loaded.Workbook) {
			b.Fatalf("no-media presentation did not round-trip: %v", err)
		}
	})
	for _, workload := range []struct {
		name string
		path string
		item presentation.Presentation
	}{
		{name: "WithMedia", path: path, item: item},
		{name: "WithoutMedia", path: noMediaPath, item: noMediaItem},
	} {
		b.Run("RecoveryDraft"+workload.name, func(b *testing.B) {
			recoveryData := presentationRecoveryData{
				Baseline: workload.item,
				Source:   workload.item.Source + "\n: recovery edit\n",
				Workbook: workload.item.Workbook,
			}
			draft := recovery.Draft{
				Path: workload.path, Kind: string(artifact.PresentationKind),
				UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				encoded, err := json.Marshal(recoveryData)
				if err != nil {
					b.Fatal(err)
				}
				draft.Data = encoded
				if err := repo.SaveRecovery(ctx, draft); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func presentationWithoutEmbeddedMedia(item presentation.Presentation) presentation.Presentation {
	lines := strings.Split(item.Source, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.Contains(line, ".image ") || strings.Contains(line, "(parchment-chart:") {
			continue
		}
		kept = append(kept, line)
	}
	item.Source = strings.Join(kept, "\n")
	item.Images = nil
	item.Workbook = spreadsheet.Workbook{}
	return item
}
