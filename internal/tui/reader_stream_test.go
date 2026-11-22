package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/charmbracelet/colorprofile"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/config"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/spreadsheet"
)

type failStreamWriter struct{ err error }

func (w failStreamWriter) Write([]byte) (int, error) { return 0, w.err }

type cancelAfterChecks struct {
	context.Context
	checks int
}

type cancelAfterFourChecks struct {
	context.Context
	checks int
}

func (c *cancelAfterFourChecks) Err() error {
	c.checks++
	if c.checks >= 4 {
		return context.Canceled
	}
	return nil
}

func (c *cancelAfterChecks) Err() error {
	c.checks++
	if c.checks >= 2 {
		return context.Canceled
	}
	return nil
}

type countingStreamRenderer struct {
	delegate renderer.NodeRenderer
	visited  *int
}

func (r countingStreamRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	r.delegate.RegisterFuncs(countingStreamRegisterer{target: reg, visited: r.visited})
}

type countingStreamRegisterer struct {
	target  renderer.NodeRendererFuncRegisterer
	visited *int
}

func (r countingStreamRegisterer) Register(kind ast.NodeKind, fn renderer.NodeRendererFunc) {
	r.target.Register(kind, func(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && node.Parent() != nil && node.Parent().Kind() == ast.KindDocument {
			*r.visited++
		}
		return fn(w, source, node, entering)
	})
}

func TestReaderStreamingRendererMatchesGlamour(t *testing.T) {
	bodies := []string{
		strings.Repeat("# Heading\n\nParagraph **bold** and [link](https://example.com).\n\n"+
			"Text before <a id=\"target\"></a> anchor.\n\n"+
			"> quoted\n> lines\n\n- one\n- two\n\n```go\nfunc main() {}\n```\n\n"+
			"| heading | title |\n| --- | --- |\n| a | b |\n\n<x>html</x>\n\n"+
			"A [reference][later] and Unicode 世界.\n\n", 12) + "[later]: https://example.com\n",
	}
	for _, mode := range []string{"dark", "light"} {
		for _, width := range []int{15, 40, 80, 120} {
			for i, body := range bodies {
				options := glamouransi.Options{
					WordWrap: width, PreserveNewLines: true,
					ColorProfile: glamourColorProfile(colorprofile.TrueColor),
					Styles:       resolveTheme(config.Theme{Mode: mode}).Markdown,
				}
				source := []byte(body)
				md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList), goldmark.WithParserOptions(parser.WithAutoHeadingID()))
				root := md.Parser().Parse(text.NewReader(source))
				render := func(mode int) []byte {
					r := glamouransi.NewRenderer(options)
					if mode == 1 {
						r = glamouransi.NewStreamingRenderer(options)
					}
					var out bytes.Buffer
					if mode == 2 {
						cursor := glamouransi.NewStreamingCursor(context.Background(), options, source, root, &out)
						for {
							done, err := cursor.Step()
							if err != nil {
								t.Fatal(err)
							}
							if err := cursor.Flush(); err != nil {
								t.Fatal(err)
							}
							if done {
								break
							}
						}
					} else {
						renderer := renderer.NewRenderer(renderer.WithNodeRenderers(util.Prioritized(cancellableNodeRenderer{ctx: context.Background(), renderer: r}, 1000)))
						if err := renderer.Render(&out, source, root); err != nil {
							t.Fatal(err)
						}
					}
					return out.Bytes()
				}
				want, got, resumed := render(0), render(1), render(2)
				if !bytes.Equal(got, want) || !bytes.Equal(resumed, want) {
					t.Fatalf("%s width %d body %d differs; streamed %d, cursor %d, full %d bytes",
						mode, width, i, len(got), len(resumed), len(want))
				}

				if !strings.HasSuffix(string(got), "\n") {
					t.Fatal("missing root suffix")
				}
			}
		}
	}
}

func TestReaderInlineLinkMatchesReferenceParse(t *testing.T) {
	body := "# Start\n\n[Jump](#target) and [external](https://example.com).\n\n" +
		"```md\n[code](#ignored)\n```\n\n# Target\n\nText.\n"
	for _, width := range []int{20, 80} {
		doc := document.Document{Body: body, Layout: document.DefaultLayout()}
		fast := &documentsScreen{}
		got, gotOutline, gotRows, gotLinks, err := fast.renderReaderDocumentContext(context.Background(), doc, width)
		if err != nil {
			t.Fatal(err)
		}
		doc.Body += "\n[unused]: https://example.com\n"
		original := &documentsScreen{}
		want, wantOutline, wantRows, wantLinks, err := original.renderReaderDocumentContext(context.Background(), doc, width)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) || !slices.Equal(gotRows, wantRows) ||
			!slices.Equal(gotLinks, wantLinks) || len(gotOutline) != len(wantOutline) {
			t.Fatalf("width %d: inline link render differs from reference-aware parse", width)
		}
	}
}

func TestReaderInlineLinkPreservesLateReference(t *testing.T) {
	body := "# Start\n\n[Jump](#target) and [late reference][late label].\n\n" +
		"# Target\n\n[late\nlabel]: https://example.com \"Reference title\"\n"
	doc := document.Document{Body: body, Layout: document.DefaultLayout()}
	screen := &documentsScreen{}
	lines, _, rows, links, err := screen.renderReaderDocumentContext(context.Background(), doc, 80)
	if err != nil {
		t.Fatal(err)
	}
	rendered := strings.Join(lines, "\n")
	if strings.Contains(rendered, "[late reference][late label]") ||
		!strings.Contains(rendered, "late reference") || len(links) != 1 ||
		links[0].target != "target" || len(rows) != 2 || rows[1] < 0 || doc.Body != body {
		t.Fatalf("late reference or navigation changed: rows=%v links=%v", rows, links)
	}
}

func TestReaderCheckpointWindowsMatchCompletedRender(t *testing.T) {
	body := strings.Repeat("# Heading\n\nParagraph **bold** and Unicode 世界.\n\n"+
		"> A quote\n> on two lines\n\n- one\n- two\n\n"+
		"```go\nfunc main() {}\n```\n\n", 200) +
		strings.Repeat(strings.Repeat("padding ", 160)+"\n\n", 100)
	doc := document.Document{Body: body, Layout: document.DefaultLayout()}
	for _, width := range []int{20, 80} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			screen := &documentsScreen{theme: resolveTheme(config.Theme{Mode: "dark"})}
			screen.readerProgress = func([]string) bool { return true }
			lines, _, _, _, err := screen.renderReaderDocumentContext(context.Background(), doc, width)
			if err != nil {
				t.Fatal(err)
			}
			source := screen.readerSource
			if source == nil || len(source.points) == 0 || len(source.points) > maxReaderCheckpoints {
				t.Fatal("reader did not produce bounded checkpoints")
			}
			for _, start := range []int{0, 1, 200, 300, len(lines) / 2, len(lines) - 25} {
				end := min(start+32, len(lines))
				got, err := source.lines(context.Background(), start, end)
				if err != nil || !slices.Equal(got, lines[start:end]) {
					t.Fatalf("window %d:%d differs from completed rendering: %v", start, end, err)
				}
				for _, query := range []string{"世界"} {
					got, err := source.matches(context.Background(), query, len(lines), maxReaderSearchMatches)
					want, wantErr := renderedReaderMatchesContext(context.Background(), lines, query, maxReaderSearchMatches)
					if err != nil || wantErr != nil || !slices.Equal(got, want) {
						t.Fatalf("search %q differs from completed rendering: %v / %v", query, err, wantErr)
					}
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := source.lines(ctx, 0, 10); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled window error = %v", err)
			}
		})
	}
}

func TestReaderCheckpointIndexRemainsBounded(t *testing.T) {
	source := &readerWindowSource{nextRow: readerCheckpointRows}
	point := &glamouransi.StreamingCheckpoint{}
	for row := readerCheckpointRows; row < readerCheckpointRows*20000; row += readerCheckpointRows {
		source.add(row, 0, point)
	}
	if len(source.points) == 0 || len(source.points) > maxReaderCheckpoints {
		t.Fatalf("retained %d checkpoints", len(source.points))
	}
	for i := 1; i < len(source.points); i++ {
		if source.points[i-1].row >= source.points[i].row {
			t.Fatal("checkpoint eviction lost row ordering")
		}
	}
	boxes := make([]linkHitBox, maxReaderCachedLinkBytes/int(unsafe.Sizeof(linkHitBox{}))+1)
	if source.setLinks(boxes, Theme{}.Link) || len(source.links) != 0 {
		t.Fatal("oversized link index was retained")
	}
	if source.setSource(context.Background(), nil, map[rune][]string{'x': {strings.Repeat("x", maxReaderCachedSlotBytes)}}) {
		t.Fatal("oversized media slot was retained")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if source.setSource(ctx, nil, map[rune][]string{'x': {"row"}}) {
		t.Fatal("canceled media slot indexing succeeded")
	}
	source.setSearchIndexLimit(context.Background(), []string{strings.Repeat("x", 128)}, 128)
	if source.search != nil {
		t.Fatal("oversized search index was retained")
	}
	source.setSearchIndex(ctx, []string{"text"})
	if source.search != nil {
		t.Fatal("canceled search index was retained")
	}
}

func TestNovelCheckpointWindows(t *testing.T) {
	doc := loadNovelFixture(t)
	screen := &documentsScreen{theme: resolveTheme(config.Theme{Mode: "dark"})}
	screen.readerProgress = func([]string) bool { return true }
	lines, _, _, _, err := screen.renderReaderDocumentContext(context.Background(), doc, 80)
	if err != nil {
		t.Fatal(err)
	}
	if screen.readerSource == nil {
		t.Fatal("novel did not use checkpoint windows")
	}
	for _, start := range []int{0, len(lines) / 2, len(lines) - 25} {
		end := min(start+25, len(lines))
		got, err := screen.readerSource.lines(context.Background(), start, end)
		if err != nil || !slices.Equal(got, lines[start:end]) {
			t.Fatalf("novel window %d:%d differs: %v", start, end, err)
		}
	}
}

func TestReaderCheckpointConcurrentWindowsAndSearch(t *testing.T) {
	body := strings.Repeat("# Repeated\n\nA [reference][later], [inline](#target) and Unicode 世界.\n\n"+
		"<a id=\"target\"></a>\n\n> Quoted text\n\n", 1800) +
		"[later]: https://example.com\n"
	doc := document.Document{Body: body, Layout: document.DefaultLayout()}
	screen := &documentsScreen{theme: resolveTheme(config.Theme{Mode: "light"})}
	screen.readerProgress = func([]string) bool { return true }
	lines, _, _, _, err := screen.renderReaderDocumentContext(context.Background(), doc, 45)
	if err != nil {
		t.Fatal(err)
	}
	source := screen.readerSource
	if source == nil || source.search == nil {
		t.Fatal("reference and anchor document did not produce checkpoints")
	}
	wantMatches, err := renderedReaderMatchesContext(context.Background(), lines, "世界", maxReaderSearchMatches)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for _, start := range []int{0, len(lines) / 3, 2 * len(lines) / 3, len(lines) - 25} {
		workers.Go(func() {
			end := min(start+25, len(lines))
			got, err := source.lines(context.Background(), start, end)
			if err != nil || !slices.Equal(got, lines[start:end]) {
				t.Errorf("window %d:%d differs: %v", start, end, err)
			}
		})
	}
	workers.Go(func() {
		got, err := source.matches(context.Background(), "世界", len(lines), maxReaderSearchMatches)
		if err != nil || !slices.Equal(got, wantMatches) {
			t.Errorf("concurrent search differs: %v", err)
		}
	})
	workers.Wait()
	if _, err := source.matches(context.Background(), "世界", len(lines), 1); !errors.Is(err, errReaderSearchLimit) {
		t.Fatalf("search limit error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.matches(ctx, "世界", len(lines), maxReaderSearchMatches); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled search error = %v", err)
	}
	source.search = nil
	if got, err := source.matches(context.Background(), "世界", len(lines), maxReaderSearchMatches); err != nil || !slices.Equal(got, wantMatches) {
		t.Fatalf("search fallback differs: %v", err)
	}
	if doc.Body != body {
		t.Fatal("rendering changed the source")
	}
}

func TestReaderCheckpointWindowsPreserveInlineLinks(t *testing.T) {
	body := strings.Repeat("# Target\n\nA [short](#target) link and a "+
		"[longer link that wraps across two lines](#target) beside [another](#target).\n\n", 1800)
	doc := document.Document{Body: body, Layout: document.DefaultLayout()}
	for _, width := range []int{20, 45, 80} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			screen := &documentsScreen{theme: resolveTheme(config.Theme{Mode: "dark"})}
			screen.readerProgress = func([]string) bool { return true }
			lines, _, _, boxes, err := screen.renderReaderDocumentContext(context.Background(), doc, width)
			if err != nil {
				t.Fatal(err)
			}
			if screen.readerSource == nil || len(boxes) == 0 {
				t.Fatal("inline links did not produce checkpoints and hitboxes")
			}
			for _, start := range []int{0, len(lines) / 3, 2 * len(lines) / 3, len(lines) - 25} {
				end := min(start+100, len(lines))
				got, err := screen.readerSource.lines(context.Background(), start, end)
				if err != nil || !slices.Equal(got, lines[start:end]) {
					t.Fatalf("linked window %d:%d differs: %v", start, end, err)
				}
			}
			got, err := screen.readerSource.matches(context.Background(), "link", len(lines), maxReaderSearchMatches)
			want, wantErr := renderedReaderMatchesContext(context.Background(), lines, "link", maxReaderSearchMatches)
			if err != nil || wantErr != nil || !slices.Equal(got, want) {
				t.Fatalf("linked search differs: %v / %v", err, wantErr)
			}
		})
	}
}

func TestReaderCheckpointWindowsPreserveMedia(t *testing.T) {
	img := documentTestImage(t)
	book, reference := embeddedChartTestWorkbook(t, "line")
	paragraphs := strings.Repeat("A paragraph with enough content to produce a rendered line.\n\n", 1200)
	body := "# Start\n\n![First](" + img.Name + ")\n\n" + paragraphs +
		"\n> ![Middle](" + img.Name + ") and ![Other](" + img.Name + ")\n\n" +
		reference + "\n\n" + paragraphs + "\n![Last](" + img.Name + ")\n"
	doc := document.Document{Body: body, Images: []artifact.Image{img}, Workbook: book, Layout: document.DefaultLayout()}
	for _, protocol := range []string{"none", "kitty"} {
		t.Run(protocol, func(t *testing.T) {
			documentImageEnv(t, protocol)
			screen := &documentsScreen{theme: resolveTheme(config.Theme{Mode: "dark"})}
			screen.readerProgress = func([]string) bool { return true }
			lines, _, _, _, err := screen.renderReaderDocumentContext(context.Background(), doc, 45)
			if err != nil {
				t.Fatal(err)
			}
			source := screen.readerSource
			if source == nil || len(source.slots) == 0 {
				t.Fatal("media document did not produce checkpoint windows")
			}
			starts := []int{0, len(lines) / 2, len(lines) - 25}
			for row, line := range lines {
				for marker := range screen.imageState.rows {
					if strings.ContainsRune(line, marker) {
						starts = append(starts, max(0, row-2))
						break
					}
				}
			}
			for _, start := range starts {
				end := min(start+25, len(lines))
				got, err := source.lines(context.Background(), start, end)
				if err != nil || !slices.Equal(got, lines[start:end]) {
					t.Fatalf("media window %d:%d differs: %v", start, end, err)
				}
			}
			got, err := source.matches(context.Background(), "Monthly", len(lines), maxReaderSearchMatches)
			want, wantErr := renderedReaderMatchesContext(context.Background(), lines, "Monthly", maxReaderSearchMatches)
			if err != nil || wantErr != nil || !slices.Equal(got, want) {
				t.Fatalf("media search differs: %v / %v", err, wantErr)
			}
			if doc.Body != body {
				t.Fatal("media rendering changed the source")
			}
		})
	}
}

func TestDocumentLineWriterMatchesTrimmedSplit(t *testing.T) {
	for _, input := range []string{"", "\n\n", "a", "a\n\nb\n\n", "\n\na\n\x1b[0m\n", "世界\nnext"} {
		var writer documentLineWriter
		for i := range len(input) {
			if _, err := writer.Write([]byte{input[i]}); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.finish(); err != nil {
			t.Fatal(err)
		}
		got := writer.lines
		want := strings.Split(strings.TrimRight(input, "\n"), "\n")
		if len(got) != len(want) {
			t.Fatalf("%q: got %q, want %q", input, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%q: got %q, want %q", input, got, want)
			}
		}
	}
}

func TestReaderRenderLimits(t *testing.T) {
	t.Run("default limit allows output above 64 MiB", func(t *testing.T) {
		writer := documentRenderLimiter{writer: io.Discard, limit: defaultReaderRenderLimits().bytes}
		block := make([]byte, 1<<20)
		for range 65 {
			if _, err := writer.Write(block); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("bytes", func(t *testing.T) {
		var output bytes.Buffer
		writer := documentRenderLimiter{writer: &output, limit: 5}
		if _, err := writer.Write([]byte("12345")); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte("6")); !errors.Is(err, errReaderRenderLimit) {
			t.Fatalf("overflow error = %v", err)
		}
		if _, err := writer.Write(nil); !errors.Is(err, errReaderRenderLimit) {
			t.Fatalf("sticky error = %v", err)
		}
		if got := output.String(); got != "12345" {
			t.Fatalf("output = %q", got)
		}
	})
	t.Run("line", func(t *testing.T) {
		writer := documentLineWriter{maxLine: 3}
		if _, err := writer.Write([]byte("ab")); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte("cd\n")); !errors.Is(err, errReaderRenderLimit) {
			t.Fatalf("overflow error = %v", err)
		}
	})
	t.Run("cancel blank lines", func(t *testing.T) {
		ctx := &cancelAfterChecks{Context: context.Background()}
		writer := documentLineWriter{ctx: ctx}
		n, err := writer.Write([]byte(strings.Repeat("\n", 8192)))
		if !errors.Is(err, context.Canceled) || n >= 8192 {
			t.Fatalf("write = %d, error = %v", n, err)
		}
	})
	t.Run("expanded lines", func(t *testing.T) {
		budget := readerLineBudget{maxBytes: 4, maxLines: 2, maxLineLen: 3}
		if err := budget.accept("abc"); err != nil {
			t.Fatal(err)
		}
		if err := budget.accept("d"); err != nil {
			t.Fatal(err)
		}
		if err := budget.accept(""); !errors.Is(err, errReaderRenderLimit) {
			t.Fatalf("line count error = %v", err)
		}
		budget = readerLineBudget{maxBytes: 3, maxLines: 2, maxLineLen: 3}
		if err := budget.accept("abcd"); !errors.Is(err, errReaderRenderLimit) {
			t.Fatalf("line size error = %v", err)
		}
		if err := budget.accept("ab"); err != nil {
			t.Fatal(err)
		}
		if err := budget.accept("cd"); !errors.Is(err, errReaderRenderLimit) {
			t.Fatalf("byte count error = %v", err)
		}
	})
	t.Run("stream propagation", func(t *testing.T) {
		source := []byte(strings.Repeat("Paragraph with several words.\n\n", 30))
		root := goldmark.New().Parser().Parse(text.NewReader(source))
		var output bytes.Buffer
		writer := &documentRenderLimiter{writer: &output, limit: 128}
		cursor := glamouransi.NewStreamingCursor(context.Background(), glamouransi.Options{WordWrap: 40}, source, root, writer)
		var err error
		for err == nil {
			var done bool
			done, err = cursor.Step()
			if done {
				t.Fatal("render completed despite exceeding its limit")
			}
		}
		if !errors.Is(err, errReaderRenderLimit) || output.Len() > writer.limit {
			t.Fatalf("render error = %v, output bytes = %d", err, output.Len())
		}
	})
	for _, streaming := range []bool{false, true} {
		name, repetitions := "fallback formatter", 20
		if streaming {
			name, repetitions = "streaming formatter", 5000
		}
		t.Run(name, func(t *testing.T) {
			body := strings.Repeat("Paragraph with several words.\n\n", repetitions)
			doc := document.Document{Body: body}
			screen := &documentsScreen{}
			screen.imageState.markNavigation = true
			screen.imageState.navigation = make(map[rune]int)
			format := screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, readerRenderLimits{
				bytes: 128, lines: 100, lineBytes: 1024,
			})
			if got := format(body, 40); got != nil {
				t.Fatalf("limit returned partial content: %q", got)
			}
			if !errors.Is(screen.imageState.err, errReaderRenderLimit) {
				t.Fatalf("formatter error = %v", screen.imageState.err)
			}
		})
	}
	t.Run("image budget", func(t *testing.T) {
		img := documentTestImage(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := (&documentsScreen{}).prepareDocumentImage(ctx, img, 40); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled image error = %v", err)
		}
		duringDecode := &cancelAfterChecks{Context: context.Background()}
		if _, err := (&documentsScreen{}).prepareDocumentImage(duringDecode, img, 40); !errors.Is(err, context.Canceled) {
			t.Fatalf("image read cancellation error = %v", err)
		}
		body := "![Picture](" + img.Name + ")\n\n![Picture](" + img.Name + ")\n"
		doc := document.Document{Body: body, Images: []artifact.Image{img}}
		for _, tc := range []struct {
			name               string
			imageBytes, images int
			wantError          bool
		}{
			{"bytes", 1, 10, true},
			{"entries", 1 << 20, 0, true},
			{"reused image", 1 << 20, 1, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				screen := &documentsScreen{}
				screen.imageState.markNavigation = true
				screen.imageState.navigation = make(map[rune]int)
				screen.imageState.source = body
				format := screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, readerRenderLimits{
					bytes: 1 << 20, lines: 1000, lineBytes: 1 << 20,
					imageBytes: tc.imageBytes, images: tc.images,
				})
				got := format(body, 40)
				if tc.wantError {
					if got != nil || !errors.Is(screen.imageState.err, errReaderRenderLimit) {
						t.Fatalf("content=%q error=%v", got, screen.imageState.err)
					}
				} else if len(got) == 0 || screen.imageState.err != nil || len(screen.imageState.prepared) != 1 {
					t.Fatalf("content=%q error=%v prepared=%d", got, screen.imageState.err, len(screen.imageState.prepared))
				}
			})
		}
	})
}

func TestReaderRejectsOversizedMarkdownBlock(t *testing.T) {
	for _, block := range []string{
		strings.Repeat("x", 256),
		"![" + strings.Repeat("x", 256) + "](missing.png)",
	} {
		body := block + "\n\n" + strings.Repeat("Normal text.\n\n", 10000)
		doc := document.Document{Body: body}
		screen := &documentsScreen{}
		screen.imageState.markNavigation = true
		screen.imageState.navigation = make(map[rune]int)
		limits := defaultReaderRenderLimits()
		limits.blockBytes = 128
		format := screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, limits)
		if got := format(body, 40); got != nil || !errors.Is(screen.imageState.err, errReaderRenderLimit) {
			t.Fatalf("oversized block returned %d lines, error %v", len(got), screen.imageState.err)
		}
		if !strings.Contains(screen.imageState.err.Error(), "Markdown block") || doc.Body != body {
			t.Fatal("block limit did not report the cause or preserve the source")
		}
	}
}

func TestReaderBlockLimitDoesNotCountBlankSpaceBetweenBlocks(t *testing.T) {
	body := "First.\n\n" + strings.Repeat("\n", asyncReaderSearchBytes) + "Second.\n"
	doc := document.Document{Body: body}
	screen := &documentsScreen{}
	screen.imageState.markNavigation = true
	screen.imageState.navigation = make(map[rune]int)
	limits := defaultReaderRenderLimits()
	limits.blockBytes = 16
	format := screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, limits)
	if got := format(body, 40); len(got) == 0 || screen.imageState.err != nil {
		t.Fatalf("small blocks rejected: %v", screen.imageState.err)
	}
}

func TestReaderBoundsNonStreamingFallback(t *testing.T) {
	body := strings.Repeat("Text.\n\n", 20000) + "\x1b"
	doc := document.Document{Body: body}
	screen := &documentsScreen{}
	screen.imageState.markNavigation = true
	limits := defaultReaderRenderLimits()
	limits.fallbackBytes = 128
	format := screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, limits)
	if got := format(body, 40); got != nil || !errors.Is(screen.imageState.err, errReaderRenderLimit) ||
		!strings.Contains(screen.imageState.err.Error(), "non-streamable Markdown") {
		t.Fatalf("fallback returned %d lines, error %v", len(got), screen.imageState.err)
	}
	safe := strings.TrimSuffix(body, "\x1b")
	doc.Body = safe
	screen = &documentsScreen{}
	screen.imageState.markNavigation = true
	format = screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, limits)
	if got := format(safe, 40); len(got) == 0 || screen.imageState.err != nil {
		t.Fatalf("streamable input returned %d lines, error %v", len(got), screen.imageState.err)
	}
}

func TestReaderFallbackPreflightAfterLinkRewrite(t *testing.T) {
	prefix := strings.Repeat("Text.\n\n", 20000)
	for _, tc := range []struct {
		name, suffix string
		wantError    bool
	}{
		{"unsafe external link", "[text](https://example.com/\x1b)\n", true},
		{"removed unsafe target", "[text](#target\x1b)\n", false},
		{"normalized line endings", "Text.\r\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := prefix + tc.suffix
			doc := document.Document{Body: body}
			screen := &documentsScreen{}
			screen.imageState.markNavigation = true
			limits := defaultReaderRenderLimits()
			limits.fallbackBytes = 128
			got := screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, limits)(body, 40)
			if tc.wantError {
				if got != nil || !errors.Is(screen.imageState.err, errReaderRenderLimit) {
					t.Fatalf("unsafe fallback returned %d lines, error %v", len(got), screen.imageState.err)
				}
			} else if len(got) == 0 || screen.imageState.err != nil {
				t.Fatalf("safe rewrite returned %d lines, error %v", len(got), screen.imageState.err)
			}
			if doc.Body != body {
				t.Fatal("preflight changed the source")
			}
		})
	}
}

func TestReaderBoundsChartOutput(t *testing.T) {
	book, ref := embeddedChartTestWorkbook(t, "line")
	for _, protocol := range []string{"none", "kitty"} {
		t.Run(protocol, func(t *testing.T) {
			documentImageEnv(t, protocol)
			for _, tc := range []struct {
				name        string
				body        string
				bytes, max  int
				wantMessage string
			}{
				{"bytes", ref, 1, 10, "chart"},
				{"count", ref + "\n\n" + ref, 1 << 20, 1, "too many"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					doc := document.Document{Body: tc.body, Workbook: book}
					screen := &documentsScreen{}
					screen.imageState.markNavigation = true
					screen.imageState.navigation = make(map[rune]int)
					limits := defaultReaderRenderLimits()
					limits.chartBytes, limits.charts = tc.bytes, tc.max
					format := screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, limits)
					if got := format(tc.body, 40); got != nil || !errors.Is(screen.imageState.err, errReaderRenderLimit) ||
						!strings.Contains(screen.imageState.err.Error(), tc.wantMessage) {
						t.Fatalf("chart output returned %d lines, error %v", len(got), screen.imageState.err)
					}
				})
			}
		})
	}
}

func TestReaderBoundsExpandedSheetOutput(t *testing.T) {
	sheet, err := spreadsheet.NewEmbeddedSheet("Data")
	if err != nil {
		t.Fatal(err)
	}
	sheet.Rows = [][]spreadsheet.Cell{{{Value: strings.Repeat("x", 200)}}}
	ref, err := spreadsheet.FormatReference(spreadsheet.Reference{Kind: spreadsheet.SheetReferenceKind, ID: sheet.ID})
	if err != nil {
		t.Fatal(err)
	}
	doc := document.Document{Body: ref, Workbook: spreadsheet.Workbook{Sheets: []spreadsheet.Sheet{sheet}}}
	for _, protocol := range []string{"none", "kitty"} {
		t.Run(protocol, func(t *testing.T) {
			documentImageEnv(t, protocol)
			screen := &documentsScreen{}
			screen.imageState.markNavigation = true
			limits := defaultReaderRenderLimits()
			limits.bytes = 150
			format := screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, limits)
			if got := format(doc.Body, 40); got != nil || !errors.Is(screen.imageState.err, errReaderRenderLimit) ||
				!strings.Contains(screen.imageState.err.Error(), "expansion") {
				t.Fatalf("expanded sheet returned %d lines, error %v", len(got), screen.imageState.err)
			}
		})
	}
}

func TestReaderStreamsBeforePreparingLaterImages(t *testing.T) {
	documentImageEnv(t, "none")
	img := documentTestImage(t)
	body := strings.Repeat("Visible paragraph with several words.\n\n", 4000) +
		"\n![Later image](" + img.Name + ")\n"
	doc := document.Document{Body: body, Images: []artifact.Image{img}}
	screen := &documentsScreen{}
	screen.imageState.markNavigation = true
	screen.imageState.navigation = make(map[rune]int)
	screen.imageState.source = body
	published := false
	screen.readerProgress = func(lines []string) bool {
		if len(lines) >= 20 {
			published = true
			if len(screen.imageState.prepared) != 0 {
				t.Fatal("later image was prepared before the first viewport")
			}
			return true
		}
		return false
	}
	limits := defaultReaderRenderLimits()
	limits.images = 0
	format := screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, limits)
	if got := format(body, 40); got != nil || !published || !errors.Is(screen.imageState.err, errReaderRenderLimit) {
		t.Fatalf("published=%t, lines=%d, error=%v", published, len(got), screen.imageState.err)
	}
}

func TestReaderCancelsBeforePreparingLaterImages(t *testing.T) {
	documentImageEnv(t, "none")
	img := documentTestImage(t)
	body := strings.Repeat("Visible paragraph with several words.\n\n", 4000) +
		"\n![Later image](" + img.Name + ")\n"
	doc := document.Document{Body: body, Images: []artifact.Image{img}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	screen := &documentsScreen{}
	screen.imageState.markNavigation = true
	screen.imageState.navigation = make(map[rune]int)
	screen.imageState.source = body
	published := false
	screen.readerProgress = func(lines []string) bool {
		if len(lines) < 20 {
			return false
		}
		published = true
		cancel()
		return true
	}
	format := screen.documentMarkdownSourceFormatterContext(ctx, doc)
	if got := format(body, 40); got != nil || !published || len(screen.imageState.prepared) != 0 {
		t.Fatalf("published=%t, lines=%d, prepared images=%d", published, len(got), len(screen.imageState.prepared))
	}
}

func TestReaderRejectsOversizedFallbackBeforePreparingImages(t *testing.T) {
	documentImageEnv(t, "none")
	img := documentTestImage(t)
	body := "![Image](" + img.Name + ")\n\n" + strings.Repeat("Paragraph.\n\n", 40) + "\x1b"
	doc := document.Document{Body: body, Images: []artifact.Image{img}}
	screen := &documentsScreen{}
	screen.imageState.markNavigation = true
	screen.imageState.navigation = make(map[rune]int)
	screen.imageState.source = body
	limits := defaultReaderRenderLimits()
	limits.fallbackBytes = 100
	limits.images = 0
	format := screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, limits)
	if got := format(body, 40); got != nil || !errors.Is(screen.imageState.err, errReaderRenderLimit) ||
		!strings.Contains(screen.imageState.err.Error(), "non-streamable Markdown") {
		t.Fatalf("lines=%d, error=%v", len(got), screen.imageState.err)
	}
}

func TestReaderFallbackPreflightPreservesRewrittenLinks(t *testing.T) {
	body := strings.Repeat("Paragraph with enough words to fill a row.\n\n", 4000) +
		"[Link](#target\x1b)\n"
	doc := document.Document{Body: body}
	screen := &documentsScreen{}
	screen.imageState.markNavigation = true
	screen.imageState.navigation = make(map[rune]int)
	screen.imageState.source = body
	limits := defaultReaderRenderLimits()
	limits.fallbackBytes = 100
	format := screen.documentMarkdownSourceFormatterWithLimits(context.Background(), doc, limits)
	if got := format(body, 40); len(got) == 0 || screen.imageState.err != nil {
		t.Fatalf("rewritten stream returned %d lines, error %v", len(got), screen.imageState.err)
	}
}

func TestReaderStreamRejectsControlsAndMalformedUTF8(t *testing.T) {
	for _, source := range []string{"text\x1b[31m", "text\r\n", "text\x7f", "text\u009b", "text\xff"} {
		if readerStreamSafe(context.Background(), []byte(source)) {
			t.Errorf("streaming accepted unsafe source %q", source)
		}

	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if readerStreamSafe(ctx, []byte("text")) {
		t.Fatal("streaming accepted a canceled context")
	}
}

func TestReaderCancelsMarkerScanWithoutKeepingPartialState(t *testing.T) {
	screen := &documentsScreen{}
	screen.imageState.source = "\U000f0000" + strings.Repeat("x", 200000) + "\U000f0001"
	ctx := &cancelAfterFourChecks{Context: context.Background()}
	if marker := screen.nextDocumentImageMarkerContext(ctx); marker != 0 || screen.imageState.usedMarkers != nil {
		t.Fatal("canceled scan retained incomplete marker state")
	}
	if marker := screen.nextDocumentImageMarker(); marker != 0xf0002 {
		t.Fatalf("marker after retry = %U, want U+F0002", marker)
	}
}

func TestReaderCancelsPreparingLargeOutline(t *testing.T) {
	screen := &documentsScreen{}
	screen.imageState.outline = make([]document.Section, 1024)
	ctx := &cancelAfterFourChecks{Context: context.Background()}
	format := screen.documentMarkdownSourceFormatterContext(ctx, document.Document{Body: "text"})
	if !errors.Is(ctx.Err(), context.Canceled) || format("text", 40) != nil {
		t.Fatal("canceled outline preparation continued to parse or render")
	}
}

func TestReaderStreamingRendererStopsOnWriterError(t *testing.T) {
	source := []byte(strings.Repeat("Paragraph with enough words to fill terminal output.\n\n", 1000))
	md := goldmark.New()
	root := md.Parser().Parse(text.NewReader(source))
	options := glamouransi.Options{WordWrap: 40, Styles: resolveTheme(config.Theme{Mode: "dark"}).Markdown}
	visited := 0
	nodeRenderer := countingStreamRenderer{delegate: glamouransi.NewStreamingRenderer(options), visited: &visited}
	r := renderer.NewRenderer(renderer.WithNodeRenderers(util.Prioritized(nodeRenderer, 1000)))
	want := errors.New("stop")
	if err := r.Render(failStreamWriter{err: want}, source, root); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if visited >= 1000 {
		t.Fatalf("writer failure was not observed until all %d blocks were rendered", visited)
	}
}

func TestReaderStreamingCursorCancellation(t *testing.T) {
	source := []byte("First paragraph.\n\nSecond paragraph.")
	root := goldmark.New().Parser().Parse(text.NewReader(source))
	ctx, cancel := context.WithCancel(context.Background())
	var output bytes.Buffer
	cursor := glamouransi.NewStreamingCursor(ctx, glamouransi.Options{WordWrap: 40}, source, root, &output)
	if done, err := cursor.Step(); done || err != nil {
		t.Fatalf("first step: done=%t err=%v", done, err)
	}
	cancel()
	if _, err := cursor.Step(); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled step error = %v", err)
	}
	if cursor.AtLineBoundary() {
		t.Fatal("canceled cursor reported a usable boundary")
	}
	if _, err := cursor.Step(); !errors.Is(err, context.Canceled) {
		t.Fatalf("subsequent step error = %v", err)
	}
}

func TestReaderStreamingCheckpointMatchesFullSuffix(t *testing.T) {
	body := strings.Repeat("# Heading\n\nParagraph **bold** and [reference][later] 世界.\n\n"+
		"> A quote\n> on two lines\n\n- one\n- two\n\n"+
		"```go\nfunc main() {}\n```\n\n"+
		"| A | B |\n| --- | --- |\n| one | two |\n\n<x>HTML</x>\n\n", 3) +
		"[later]: https://example.com\n"
	for _, mode := range []string{"dark", "light", "custom"} {
		for _, width := range []int{15, 40, 80} {
			t.Run(mode+"/"+strconv.Itoa(width), func(t *testing.T) {
				themeMode := mode
				if mode == "custom" {
					themeMode = "dark"
				}
				options := glamouransi.Options{
					WordWrap: width, PreserveNewLines: true,
					ColorProfile: glamourColorProfile(colorprofile.TrueColor),
					Styles:       resolveTheme(config.Theme{Mode: themeMode}).Markdown,
				}
				if mode == "custom" {
					options.Styles.Document.BlockPrefix = "START\n"
					options.Styles.Document.Prefix = "PREFIX\n"
					options.Styles.Document.Suffix = "\nSUFFIX"
					options.Styles.Document.BlockSuffix = "\nEND\n"
				}
				source := []byte(body)
				md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList), goldmark.WithParserOptions(parser.WithAutoHeadingID()))
				root := md.Parser().Parse(text.NewReader(source))
				var output bytes.Buffer
				cursor := glamouransi.NewStreamingCursor(context.Background(), options, source, root, &output)
				if !cursor.EnableCheckpoints() {
					t.Fatal("could not enable checkpoints")
				}
				type point struct {
					checkpoint *glamouransi.StreamingCheckpoint
					offset     int
				}
				var points []point
				for {
					done, err := cursor.Step()
					if err != nil {
						t.Fatal(err)
					}
					if err := cursor.Flush(); err != nil {
						t.Fatal(err)
					}
					if done {
						break
					}
					if checkpoint, ok := cursor.Checkpoint(); ok {
						points = append(points, point{checkpoint, output.Len()})
					}
				}

				if len(points) == 0 {
					t.Fatal("no safe checkpoints")
				}
				full := output.Bytes()
				for _, p := range points {
					var resumed bytes.Buffer
					restart, err := glamouransi.ResumeStreamingCursor(context.Background(), p.checkpoint, &resumed)
					if err != nil {
						t.Fatal(err)
					}
					for {
						done, err := restart.Step()
						if err != nil {
							t.Fatal(err)
						}
						if done {
							break
						}
					}

					if !bytes.Equal(resumed.Bytes(), full[p.offset:]) {
						t.Fatalf("checkpoint at byte %d: resumed output differs", p.offset)
					}
				}
			})
		}
	}
}

func TestReaderStreamingCheckpointRejectsActiveANSI(t *testing.T) {
	source := []byte("First paragraph.\n\nSecond paragraph.\n\nThird paragraph.")
	root := goldmark.New().Parser().Parse(text.NewReader(source))
	options := glamouransi.Options{WordWrap: 40}
	options.Styles.Document.Prefix = "\x1b[31m"
	var output bytes.Buffer
	cursor := glamouransi.NewStreamingCursor(context.Background(), options, source, root, &output)
	if !cursor.EnableCheckpoints() {
		t.Fatal("could not enable checkpoints")
	}
	for {
		done, err := cursor.Step()
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if _, ok := cursor.Checkpoint(); ok {
			t.Fatal("checkpoint accepted active ANSI state")
		}
	}
}

func TestReaderStreamingCheckpointLifecycle(t *testing.T) {
	source := []byte("First paragraph.\n\nSecond paragraph.")
	root := goldmark.New().Parser().Parse(text.NewReader(source))
	cursor := glamouransi.NewStreamingCursor(context.Background(), glamouransi.Options{WordWrap: 40}, source, root, &bytes.Buffer{})
	if !cursor.EnableCheckpoints() {
		t.Fatal("could not enable checkpoints")
	}
	if _, err := cursor.Step(); err != nil {
		t.Fatal(err)
	}
	if cursor.EnableCheckpoints() {
		t.Fatal("enabled tracking after rendering began")
	}
	checkpoint, ok := cursor.Checkpoint()
	if !ok {
		t.Fatal("missing checkpoint")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := glamouransi.ResumeStreamingCursor(ctx, checkpoint, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("resume error = %v, want cancellation", err)
	}
	want := errors.New("write failed")
	failing, err := glamouransi.ResumeStreamingCursor(context.Background(), checkpoint, failStreamWriter{err: want})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failing.Step(); err == nil {
		err = failing.Flush()
		if !errors.Is(err, want) {
			t.Fatalf("flush error = %v, want %v", err, want)
		}
	} else if !errors.Is(err, want) {
		t.Fatalf("step error = %v, want %v", err, want)
	}
	if _, ok := failing.Checkpoint(); ok {
		t.Fatal("failed cursor returned a checkpoint")
	}
	if _, err := failing.Step(); !errors.Is(err, want) {
		t.Fatalf("subsequent step error = %v, want %v", err, want)
	}
}

func TestNovelStreamingCheckpoints(t *testing.T) {
	source := []byte(loadNovelFixture(t).Body)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList), goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	root := md.Parser().Parse(text.NewReader(source))
	options := glamouransi.Options{
		WordWrap: 80, PreserveNewLines: true,
		ColorProfile: glamourColorProfile(colorprofile.TrueColor),
		Styles:       resolveTheme(config.Theme{Mode: "dark"}).Markdown,
	}
	var full bytes.Buffer
	cursor := glamouransi.NewStreamingCursor(context.Background(), options, source, root, &full)
	cursor.EnableCheckpoints()
	type sample struct {
		point *glamouransi.StreamingCheckpoint
		step  int
	}
	var samples []sample
	offsets := []int{0}
	nextSample, safe, largestGap, previous := 1000, 0, 0, 0
	for step := 1; ; step++ {
		done, err := cursor.Step()
		if err != nil {
			t.Fatal(err)
		}
		if err := cursor.Flush(); err != nil {
			t.Fatal(err)
		}
		offsets = append(offsets, full.Len())
		if done {
			break
		}
		if point, ok := cursor.Checkpoint(); ok {
			safe++
			largestGap = max(largestGap, step-previous)
			previous = step
			if step >= nextSample {
				samples = append(samples, sample{point, step})
				nextSample += 1000
			}
		}
	}
	if len(samples) < 3 {
		t.Fatalf("only %d sampled checkpoints in novel", len(samples))
	}
	for _, sample := range samples {
		end := min(sample.step+32, len(offsets)-2)
		var output bytes.Buffer
		restarted, err := glamouransi.ResumeStreamingCursor(context.Background(), sample.point, &output)
		if err != nil {
			t.Fatal(err)
		}
		for step := sample.step; step < end; step++ {
			done, err := restarted.Step()
			if done || err != nil {
				t.Fatalf("restart at %d: done=%t err=%v", sample.step, done, err)
			}
		}
		if err := restarted.Flush(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(output.Bytes(), full.Bytes()[offsets[sample.step]:offsets[end]]) {
			t.Fatalf("restart at block %d differs", sample.step)
		}
	}
	t.Logf("%d checkpoints in %d blocks; largest internal gap %d blocks", safe, len(offsets)-2, largestGap)
}
