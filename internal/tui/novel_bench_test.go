package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/config"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/filerepo"
	"example.com/parchment/internal/notetext"
	"example.com/parchment/internal/printing"
)

func BenchmarkReaderUnsafeFallback(b *testing.B) {
	body := strings.Repeat("Source paragraph with several words.\n\n", 30000) + "\x1b"
	doc := document.Document{Body: body}
	b.Run("ParseBeforeRefusal", func(b *testing.B) {
		md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList), goldmark.WithParserOptions(parser.WithAutoHeadingID()))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			source := []byte(body)
			_ = md.Parser().Parse(text.NewReader(source), parser.WithContext(parser.NewContext()))
			if readerStreamSafe(context.Background(), source) {
				b.Fatal("unsafe source was accepted")
			}
		}
	})
	b.Run("PreflightRefusal", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			screen := &documentsScreen{}
			screen.imageState.markNavigation = true
			format := screen.documentMarkdownSourceFormatterContext(context.Background(), doc)
			if lines := format(body, 80); lines != nil || !errors.Is(screen.imageState.err, errReaderRenderLimit) {
				b.Fatalf("lines=%d, error=%v", len(lines), screen.imageState.err)
			}
		}
	})
}

func loadNovelFixture(t testing.TB) document.Document {
	t.Helper()
	repo, err := filerepo.New(t.TempDir(), filerepo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := repo.GetDocument(context.Background(), filepath.Join("testdata", "war-and-peace.md"))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func BenchmarkNovelReaderPipeline(b *testing.B) {
	doc := loadNovelFixture(b)
	withLink := doc.Body + "\n[Jump](#target)\n"
	b.Run("LinkRewriteSplit", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			lines, _, err := extractAndRewriteLinksContext(context.Background(), strings.Split(withLink, "\n"))
			if err != nil || len(strings.Join(lines, "\n")) == 0 {
				b.Fatal(err)
			}
		}
	})
	b.Run("LinkRewriteStreaming", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			text, _, err := extractDocumentLinksTextContext(context.Background(), withLink, true, false)
			if err != nil || len(text) == 0 {
				b.Fatal(err)
			}
		}
	})
	b.Run("StripPrivateBlocks", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = artifactfile.StripPrivateBlocks(doc.Body)
		}

	})
	b.Run("Outline", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := document.Outline(doc); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ReaderOutline", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := document.ReaderOutline(doc); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("EmbeddedExpansion", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _, err := printing.ExpandEmbeddedReferencesWithChartRendererContext(context.Background(), doc.Body, doc.Workbook, nil)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("FormatterPreparation", func(b *testing.B) {
		screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = screen.documentMarkdownFormatter(doc)
		}
	})
	b.Run("ReaderRender", func(b *testing.B) {
		screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, _, _, _, err := screen.renderReaderDocument(doc, 48); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ReaderRenderInlineLink", func(b *testing.B) {
		doc := doc
		doc.Body = withLink
		screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, _, _, _, err := screen.renderReaderDocument(doc, 48); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ReaderRenderCancellable", func(b *testing.B) {
		screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			ctx, cancel := context.WithCancel(context.Background())
			_, _, _, _, err := screen.renderReaderDocumentContext(ctx, doc, 48)
			cancel()
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkNovelStreamingCheckpoints(b *testing.B) {
	source := []byte(loadNovelFixture(b).Body)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList), goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	root := md.Parser().Parse(text.NewReader(source))
	options := glamouransi.Options{
		WordWrap: 80, PreserveNewLines: true,
		ColorProfile: glamourColorProfile(colorprofile.TrueColor),
		Styles:       resolveTheme(config.Theme{Mode: "dark"}).Markdown,
	}
	for _, enabled := range []bool{false, true} {
		name := "WithoutTracking"
		if enabled {
			name = "WithTracking"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				cursor := glamouransi.NewStreamingCursor(context.Background(), options, source, root, io.Discard)
				if enabled {
					cursor.EnableCheckpoints()
				}
				for step := 0; ; step++ {
					done, err := cursor.Step()
					if err != nil {
						b.Fatal(err)
					}
					if done {
						break
					}
					if enabled && step%128 == 0 {
						cursor.Checkpoint()
					}
				}
			}
		})
	}
}

func BenchmarkNovelNavigationMarkers(b *testing.B) {
	body := loadNovelFixture(b).Body
	b.Run("RepeatedScan", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for marker := rune(0xf0000); marker < 0xf0000+382; marker++ {
				if strings.ContainsRune(body, marker) {
					b.Fatal("novel unexpectedly contains marker")
				}
			}
		}
	})
	b.Run("Indexed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			screen := &documentsScreen{}
			screen.imageState.source = body
			for j := 0; j < 382; j++ {
				screen.nextDocumentImageMarker()
			}
		}
	})
}

func BenchmarkReaderSectionLookup(b *testing.B) {
	for _, size := range []int{382, 100000} {
		b.Run(fmt.Sprintf("%d", size), func(b *testing.B) {
			rows := make([]int, size)
			for i := range rows {
				rows[i] = i
			}
			index, err := indexReaderSectionRows(context.Background(), rows)
			if err != nil {
				b.Fatal(err)
			}
			screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
			screen.readerRows, screen.readerRowIndex = rows, index
			screen.readerSection = -1
			screen.reader.SetHeight(1)
			screen.reader.SetContentLines(make([]string, size+2))
			screen.reader.SetYOffset(size / 2)
			want := size/2 + 1
			b.Run("Linear", func(b *testing.B) {
				var current int
				for n := 0; n < b.N; n++ {
					current = -1
					for i, row := range screen.readerRows {
						if row >= 0 && row <= screen.reader.YOffset()+1 {
							current = i
						}
					}
				}
				if current != want {
					b.Fatalf("section = %d, want %d", current, want)
				}
			})
			b.Run("Indexed", func(b *testing.B) {
				var current int
				for n := 0; n < b.N; n++ {
					current = screen.currentSection()
				}
				if current != want {
					b.Fatalf("section = %d, want %d", current, want)
				}
			})
		})
	}
}

func BenchmarkNovelReaderViewport(b *testing.B) {
	doc := loadNovelFixture(b)
	screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
	screen.resize(80, 24)
	screen.startReading(doc)
	if screen.errMessage != "" || len(screen.readerLines) <= readerPreviewWindowLines {
		b.Fatalf("reader fixture is unavailable or too short: %s", screen.errMessage)
	}
	offset := len(screen.readerLines) / 2
	b.Run("FullContent", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			screen.reader.SetContentLines(screen.readerLines)
		}
	})
	b.Run("Window", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			screen.setReaderWindow(offset, offset, false)
		}
	})
}

func BenchmarkNovelCheckpointWindows(b *testing.B) {
	doc := loadNovelFixture(b)
	screen := &documentsScreen{theme: resolveTheme(config.Theme{Mode: "dark"})}
	screen.readerProgress = func([]string) bool { return true }
	lines, _, _, _, err := screen.renderReaderDocumentContext(context.Background(), doc, 80)
	if err != nil || screen.readerSource == nil {
		b.Fatalf("checkpoint reader setup: %v", err)
	}
	source := screen.readerSource
	for _, sample := range []struct {
		name  string
		start int
	}{
		{"Middle", len(lines) / 2},
		{"End", max(len(lines)-1024, 0)},
	} {
		b.Run(sample.name, func(b *testing.B) {
			end := min(sample.start+1024, len(lines))
			got, err := source.lines(context.Background(), sample.start, end)
			if err != nil || !slices.Equal(got, lines[sample.start:end]) {
				b.Fatalf("checkpoint window differs: %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := source.lines(context.Background(), sample.start, end); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("Search", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			matches, err := source.matches(context.Background(), "Pierre", len(lines), maxReaderSearchMatches)
			if err != nil || len(matches) == 0 {
				b.Fatalf("checkpoint search failed: %v", err)
			}
		}
	})
}

func BenchmarkNovelDocumentScreen(b *testing.B) {
	doc := loadNovelFixture(b)
	source := doc.Body
	lines := strings.Split(strings.ReplaceAll(strings.TrimPrefix(doc.Body, "\ufeff"), "\r\n", "\n"), "\n")
	if len(lines) < 4500 {
		b.Fatal("novel is shorter than the excerpt")
	}
	excerpt := doc
	excerpt.Body = strings.Join(lines[:4500], "\n")
	if err := notetext.ValidateText(excerpt.Body); err != nil {
		b.Fatal(err)
	}
	b.Run("EditorRefusal", func(b *testing.B) {
		screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, ok := screen.startEdit(doc); ok {
				b.Fatal("oversized novel unexpectedly entered the editor")
			}
		}
		b.StopTimer()
		if screen.errMessage == "" || doc.Body != source {
			b.Fatal("editor refusal did not preserve the source and report an error")
		}
	})
	b.Run("ScrolledReaderEditorRefusal", func(b *testing.B) {
		screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
		screen.reading = true
		screen.snapshot = doc
		screen.readerLines = []string{"first", "Long paragraph of text.", "last"}
		screen.reader.SetContentLines(screen.readerLines)
		screen.reader.SetHeight(1)
		screen.reader.SetYOffset(1)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			screen.updateReaderKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
		}
		if !screen.reading || screen.reader.YOffset() != 1 || screen.errMessage == "" {
			b.Fatal("oversized document was not refused at its reader position")
		}
	})
	b.Run("ScrolledReaderEditorRefusalPrevious", func(b *testing.B) {
		screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
		screen.snapshot = doc
		screen.readerLines = []string{"first", "Long paragraph of text.", "last"}
		screen.reader.SetContentLines(screen.readerLines)
		screen.reader.SetHeight(1)
		screen.reader.SetYOffset(1)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _, _, _ = sourcePositionAtViewport(screen.readerLines, 1, 1, doc.Body, 0)
			_, _ = screen.startEdit(doc)
		}
	})
	b.Run("OpenReader80x24", func(b *testing.B) {
		repo, err := filerepo.New(b.TempDir(), filerepo.Options{})
		if err != nil {
			b.Fatal(err)
		}
		service := document.NewService(repo, 10)
		path := filepath.Join("testdata", "war-and-peace.md")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			m := NewModel(Config{Path: path, Kind: artifact.DocumentKind, Documents: service, UndoLimit: 10})
			_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			_, cmd := m.Update(m.Init()())
			if cmd != nil {
				_, _ = m.Update(cmd())
			}
			_ = m.View().Content
			if m.stage != stageDocument || m.documents.errMessage != "" || !m.documents.reading {
				b.Fatalf("document did not open in its reader: %s", m.errMessage)
			}
		}
	})
	b.Run("ExcerptEditor80x24", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
			screen.resize(80, 24)
			if _, ok := screen.startEdit(excerpt); !ok {
				b.Fatalf("excerpt editor failed to open: %s", screen.errMessage)
			}
			b.StopTimer()
			if screen.body.Value() != excerpt.Body {
				b.Fatal("editor did not preserve the excerpt")
			}
			b.StartTimer()
		}
	})
	for _, size := range []struct{ width, height int }{{60, 24}, {80, 24}, {120, 40}} {
		b.Run(fmt.Sprintf("Reader%dx%d", size.width, size.height), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
				screen.resize(size.width, size.height)
				screen.startReading(doc)
				if screen.errMessage != "" || !screen.reading || len(screen.readerLines) == 0 {
					b.Fatalf("reader failed to open: %s", screen.errMessage)
				}
			}
		})
		b.Run(fmt.Sprintf("ReaderScroll%dx%d", size.width, size.height), func(b *testing.B) {
			screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
			screen.resize(size.width, size.height)
			screen.startReading(doc)
			if screen.errMessage != "" {
				b.Fatal(screen.errMessage)
			}
			initialOffset := screen.reader.YOffset()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = screen.update(tea.KeyPressMsg{Code: tea.KeyPgDown})
				_ = screen.view("")
				_ = screen.update(tea.KeyPressMsg{Code: tea.KeyPgUp})
			}
			b.StopTimer()
			if screen.reader.YOffset() != initialOffset || screen.snapshot.Body != doc.Body {
				b.Fatal("reader scrolling changed content or lost its position")
			}
		})
	}
	b.Run("ReaderDark80x24", func(b *testing.B) {
		profile := terminalColorProfile
		terminalColorProfile = colorprofile.TrueColor
		b.Cleanup(func() { terminalColorProfile = profile })
		theme := resolveTheme(config.Theme{Mode: "dark"})
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			screen := newDocumentsScreen(nil, "", nil, theme, 10)
			screen.resize(80, 24)
			screen.startReading(doc)
			if screen.errMessage != "" || !screen.reading || len(screen.readerLines) == 0 {
				b.Fatalf("reader failed to open: %s", screen.errMessage)
			}
		}
	})
	b.Run("ModelOpenBeforeSize", func(b *testing.B) {
		repo, err := filerepo.New(b.TempDir(), filerepo.Options{})
		if err != nil {
			b.Fatal(err)
		}
		service := document.NewService(repo, 10)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			model := NewModel(Config{Kind: artifact.DocumentKind, Documents: service})
			_, cmd := model.Update(fileOpenedMsg{kind: artifact.DocumentKind, document: doc})
			if cmd != nil {
				_, _ = model.Update(cmd())
			}
			if model.documents.errMessage != "" || !model.documents.reading || model.documents.reader.Width() < 2 {
				b.Fatalf("reader failed to open: %s", model.documents.errMessage)
			}
		}
	})
	b.Run("ModelReaderFirstFrame", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			model := NewModel(Config{Kind: artifact.DocumentKind, Documents: document.NewService(nil, 10)})
			_, _ = model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			_, cmd := model.Update(fileOpenedMsg{kind: artifact.DocumentKind, document: doc})
			if cmd == nil || !strings.Contains(model.View().Content, "Gutenberg") {
				b.Fatal("novel did not expose a first frame while rendering")
			}
		}
	})
	b.Run("ReaderSearch80x24", func(b *testing.B) {
		screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
		screen.resize(80, 24)
		screen.startReading(doc)
		screen.find.visible = true
		screen.find.input.SetValue("Pierre")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if cmd := screen.searchReader(screen.find.query()); cmd != nil {
				screen.update(cmd())
			}
			_ = screen.view("")
		}
		b.StopTimer()
		if len(screen.readerMatches) == 0 || screen.snapshot.Body != doc.Body {
			b.Fatal("reader search failed or changed the source")
		}
	})
	b.Run("ReaderSearchDispatch80x24", func(b *testing.B) {
		screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
		screen.resize(80, 24)
		screen.startReading(doc)
		screen.find.visible = true
		screen.find.input.SetValue("Pierre")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if screen.searchReader(screen.find.query()) == nil {
				b.Fatal("novel search was not dispatched asynchronously")
			}
			_ = screen.view("")
		}
		b.StopTimer()
		screen.cancelReaderSearch()
	})
	b.Run("ReaderResize80x24To120x40", func(b *testing.B) {
		screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
		screen.resize(80, 24)
		screen.startReading(doc)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			screen.resize(120, 40)
			_ = screen.view("")
			screen.resize(80, 24)
		}
		b.StopTimer()
		if screen.errMessage != "" || screen.snapshot.Body != doc.Body {
			b.Fatalf("reader resize failed or changed the source: %s", screen.errMessage)
		}
	})
	b.Run("OutlineNavigation120x40", func(b *testing.B) {
		screen := newDocumentsScreen(nil, "", nil, Theme{}, 10)
		screen.resize(120, 40)
		screen.startReading(doc)
		if screen.errMessage != "" || len(screen.outline) != 382 {
			b.Fatalf("novel outline failed to load: %s", screen.errMessage)
		}
		targets := []int{0, len(screen.outline) / 2, len(screen.outline) - 1}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, target := range targets {
				screen.gotoSection(target)
				_ = screen.view("")
			}
		}
		b.StopTimer()
		if screen.currentSection() != targets[len(targets)-1] || screen.snapshot.Body != doc.Body {
			b.Fatal("outline navigation changed content or lost its position")
		}
	})
	b.Run("ExcerptEditSave", func(b *testing.B) {
		ctx := context.Background()
		repo, err := filerepo.New(b.TempDir(), filerepo.Options{})
		if err != nil {
			b.Fatal(err)
		}
		service := document.NewService(repo, 10)
		path := filepath.Join(b.TempDir(), "excerpt.md")
		saved, err := service.Create(ctx, path, document.Draft{Body: excerpt.Body})
		if err != nil {
			b.Fatal(err)
		}
		screen := newDocumentsScreen(service, path, func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		}, Theme{}, 10)
		screen.resize(80, 24)
		if _, ok := screen.startEdit(saved); !ok {
			b.Fatalf("excerpt editor failed to open: %s", screen.errMessage)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if i%2 == 0 {
				_ = screen.update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			} else {
				_ = screen.update(tea.KeyPressMsg{Code: tea.KeyBackspace})
			}
			cmd := screen.update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			if cmd == nil {
				b.Fatal("document save did not return a command")
			}
			_ = screen.update(cmd())
			if screen.pending || screen.errMessage != "" {
				b.Fatalf("document save failed: %s", screen.errMessage)
			}
		}
		b.StopTimer()
		loaded, err := service.Get(ctx, path)
		if err != nil || loaded.Body != screen.body.Value() {
			b.Fatalf("saved excerpt did not match editor: %v", err)
		}
	})
}
