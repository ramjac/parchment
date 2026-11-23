package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/printing"
	"example.com/parchment/internal/spreadsheet"
	"example.com/parchment/internal/tui/termimg"
)

// Image rows are represented by single-cell private-use characters until the
// viewport has clipped and positioned the text. Graphics never enter pagination,
// search highlighting, or the viewport's ANSI truncation machinery.
type documentImageRow struct {
	pixels *image.NRGBA
	width  int
	row    int
}

const documentImagePixelScale = 8

const (
	maxReaderRenderedBytes = 256 << 20
	maxReaderRenderedLines = 1 << 20
	maxReaderLineBytes     = 4 << 20
	maxReaderImageBytes    = 64 << 20
	maxReaderImages        = 4096
	maxReaderBlockBytes    = 4 << 20
	maxReaderFallbackBytes = 1 << 20
	maxReaderChartBytes    = 64 << 20
	maxReaderCharts        = 4096
)

var errReaderRenderLimit = errors.New("document reader render exceeds resource limits")

type readerRenderLimits struct {
	bytes, lines, lineBytes, imageBytes, images, blockBytes, fallbackBytes, chartBytes, charts int
}

func defaultReaderRenderLimits() readerRenderLimits {
	return readerRenderLimits{
		bytes: maxReaderRenderedBytes, lines: maxReaderRenderedLines, lineBytes: maxReaderLineBytes,
		imageBytes: maxReaderImageBytes, images: maxReaderImages,
		blockBytes: maxReaderBlockBytes, fallbackBytes: maxReaderFallbackBytes,
		chartBytes: maxReaderChartBytes, charts: maxReaderCharts,
	}
}

type documentImagePlacement struct {
	pixels              *image.NRGBA
	x, y, width, height int
	sourceRow           int
}

type documentImageState struct {
	rows           map[rune]documentImageRow
	pendingRows    map[rune]documentImageRow
	captureRows    bool
	nextMarker     rune
	graphicsUsed   bool
	prepared       map[string][]string
	outline        []document.Section
	navigation     map[rune]int
	usedMarkers    map[rune]struct{}
	err            error
	markNavigation bool
	earlyPreview   bool
	source         string
	frame          uint64
	imageBytes     int
	limits         readerRenderLimits
}

var documentImageAnchorRE = regexp.MustCompile(`(?i)<a\s+(?:id|name)=["']([^"']+)["'][^>]*>`)

type cancellableNodeRenderer struct {
	ctx      context.Context
	renderer renderer.NodeRenderer
}

type documentLineWriter struct {
	ctx     context.Context
	lines   []string
	pending bytes.Buffer
	visit   func(string) error
	maxLine int
	blanks  int
	emitted bool
}

type documentRenderLimiter struct {
	writer io.Writer
	limit  int
	total  int
	err    error
}

type documentImageReader struct {
	ctx    context.Context
	reader *bytes.Reader
}

func (r documentImageReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type readerLineBudget struct {
	bytes, lines                   int
	maxBytes, maxLines, maxLineLen int
}

func (b *readerLineBudget) accept(line string) error {
	if len(line) > b.maxLineLen || b.lines >= b.maxLines || len(line) > b.maxBytes-b.bytes {
		return fmt.Errorf("%w: too many rendered lines or bytes", errReaderRenderLimit)
	}
	b.lines++
	b.bytes += len(line)
	return nil
}

func (w *documentRenderLimiter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if len(data) > w.limit-w.total {
		w.err = fmt.Errorf("%w: rendered output exceeds %d bytes", errReaderRenderLimit, w.limit)
		return 0, w.err
	}
	n, err := w.writer.Write(data)
	w.total += n
	w.err = err
	return n, err
}

func (w *documentLineWriter) send(line string) error {
	if w.visit != nil {
		if err := w.visit(line); err != nil {
			return err
		}
	} else {
		w.lines = append(w.lines, line)
	}
	w.emitted = true
	return nil
}

func (w *documentLineWriter) line(line string) error {
	if line == "" {
		w.blanks++
		return nil
	}
	for w.blanks > 0 {
		if err := w.send(""); err != nil {
			return err
		}
		w.blanks--
	}
	return w.send(line)
}

func (w *documentLineWriter) Write(data []byte) (int, error) {
	n := len(data)
	consumed := 0
	nextCheck := 0
	for {
		if w.ctx != nil && consumed >= nextCheck {
			if err := w.ctx.Err(); err != nil {
				return consumed, err
			}
			nextCheck = consumed + 4096
		}
		line, rest, found := bytes.Cut(data, []byte{'\n'})
		if w.maxLine > 0 && len(line) > w.maxLine-w.pending.Len() {
			return consumed, fmt.Errorf("%w: a rendered line exceeds %d bytes", errReaderRenderLimit, w.maxLine)
		}
		_, _ = w.pending.Write(line)
		consumed += len(line)
		if !found {
			return n, nil
		}
		consumed++
		if err := w.line(w.pending.String()); err != nil {
			return consumed, err
		}
		w.pending.Reset()
		data = rest
	}
}

func (w *documentLineWriter) finish() error {
	if w.pending.Len() > 0 {
		if err := w.line(w.pending.String()); err != nil {
			return err
		}
	}
	if !w.emitted {
		return w.send("")
	}
	return nil
}

func readerStreamSafe(ctx context.Context, source []byte) bool {
	if ctx.Err() != nil {
		return false
	}
	nextCheck := 0
	for i := 0; i < len(source); {
		if i >= nextCheck && ctx.Err() != nil {
			return false
		}
		if i >= nextCheck {
			nextCheck = i + 65536
		}
		r, size := utf8.DecodeRune(source[i:])
		if r == utf8.RuneError && size == 1 || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
		i += size
	}
	return true
}

func updateReaderNodeSpan(node ast.Node, sourceLen int, start, end *int) {
	update := func(segment text.Segment) {
		if segment.Start >= 0 && segment.Stop <= sourceLen && segment.Start <= segment.Stop {
			*start = min(*start, segment.Start)
			*end = max(*end, segment.Stop)
		}
	}
	if node.Type() == ast.TypeBlock {
		segments := node.Lines()
		if segments.Len() > 0 {
			update(segments.At(0))
			update(segments.At(segments.Len() - 1))
		}
	}
	switch n := node.(type) {
	case *ast.Text:
		update(n.Segment)
	case *ast.RawHTML:
		if n.Segments.Len() > 0 {
			update(n.Segments.At(0))
			update(n.Segments.At(n.Segments.Len() - 1))
		}
	case *ast.HTMLBlock:
		if n.HasClosure() {
			update(n.ClosureLine)
		}
	}
}

func readerBlockSourceEnd(ctx context.Context, root ast.Node, sourceLen int) (int, error) {
	start, end, visited := sourceLen, 0, 0
	err := ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		visited++
		if visited%256 == 0 {
			if err := ctx.Err(); err != nil {
				return ast.WalkStop, err
			}
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		updateReaderNodeSpan(node, sourceLen, &start, &end)
		return ast.WalkContinue, nil
	})
	return end, err
}

func (r cancellableNodeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	r.renderer.RegisterFuncs(cancellableNodeRegisterer{ctx: r.ctx, target: reg})
}

type cancellableNodeRegisterer struct {
	ctx    context.Context
	target renderer.NodeRendererFuncRegisterer
}

func (r cancellableNodeRegisterer) Register(kind ast.NodeKind, fn renderer.NodeRendererFunc) {
	r.target.Register(kind, func(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if err := r.ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		return fn(w, source, node, entering)
	})
}

func (s *documentsScreen) documentMarkdownFormatter(doc document.Document) document.Formatter {
	return s.documentMarkdownFormatterContext(context.Background(), doc)
}

func (s *documentsScreen) documentMarkdownFormatterContext(ctx context.Context, doc document.Document) document.Formatter {
	format := s.documentMarkdownSourceFormatterContext(ctx, doc)
	return func(lines []string, width, _ int, _ string) []string {
		return format(strings.Join(lines, "\n"), width)
	}
}

func (s *documentsScreen) documentMarkdownSourceFormatterContext(ctx context.Context, doc document.Document) func(string, int) []string {
	return s.documentMarkdownSourceFormatterWithLimits(ctx, doc, defaultReaderRenderLimits())
}

func (s *documentsScreen) documentMarkdownSourceFormatterWithLimits(ctx context.Context, doc document.Document, limits readerRenderLimits) func(string, int) []string {
	if ctx.Err() != nil {
		return func(string, int) []string { return nil }
	}
	if limits.blockBytes == 0 {
		limits.blockBytes = maxReaderBlockBytes
	}
	if limits.fallbackBytes == 0 {
		limits.fallbackBytes = maxReaderFallbackBytes
	}
	if limits.chartBytes == 0 {
		limits.chartBytes = maxReaderChartBytes
	}
	if limits.charts == 0 {
		limits.charts = maxReaderCharts
	}
	s.imageState.imageBytes = 0
	s.imageState.limits = limits
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	var originalSource []byte
	var originalRoot ast.Node
	var references []parser.Reference
	originalParsed := false
	assets := make(map[string]artifact.Image, len(doc.Images))
	for i, img := range doc.Images {
		if i%256 == 0 && ctx.Err() != nil {
			return func(string, int) []string { return nil }
		}
		assets[img.Name] = img
	}
	headingTargets := make(map[int][]int)
	anchorTargets := make(map[string][]int)
	for index, section := range s.imageState.outline {
		if index%256 == 0 && ctx.Err() != nil {
			return func(string, int) []string { return nil }
		}
		if section.Anchor {
			anchorTargets[section.Slug] = append(anchorTargets[section.Slug], index)
		} else {
			headingTargets[section.Level] = append(headingTargets[section.Level], index)
		}
	}
	originalRootUsed := false
	return func(sourceText string, width int) []string {
		if ctx.Err() != nil {
			return nil
		}
		if s.imageState.markNavigation && sourceText == doc.Body && len(doc.Body) > limits.fallbackBytes &&
			!strings.Contains(doc.Body, "<") && !strings.Contains(doc.Body, "parchment-sheet:") &&
			!strings.Contains(doc.Body, "parchment-chart:") {
			preflight := doc.Body
			if strings.Contains(preflight, "\r\n") {
				preflight = strings.ReplaceAll(preflight, "\r\n", "\n")
			}
			safe := readerStreamSafe(ctx, []byte(preflight))
			if !safe && strings.Contains(preflight, "](") {
				rewritten, _, err := extractDocumentLinksTextContext(ctx, preflight, true, false)
				if err != nil {
					return nil
				}
				preflight = rewritten
				safe = readerStreamSafe(ctx, []byte(preflight))
			}
			if len(preflight) > limits.fallbackBytes &&
				(len(doc.Body) < asyncReaderSearchBytes || !safe) {
				if ctx.Err() == nil {
					s.imageState.err = fmt.Errorf("%w: non-streamable Markdown exceeds %d bytes", errReaderRenderLimit, limits.fallbackBytes)
				}
				return nil
			}
		}
		// Goldmark reference definitions require a colon immediately after ']'.
		deferOriginalParse := s.imageState.markNavigation && sourceText == doc.Body &&
			strings.Contains(doc.Body, "](") && !strings.Contains(doc.Body, "]:")
		if !originalParsed && !deferOriginalParse {
			parseContext := parser.NewContext()
			originalSource = []byte(doc.Body)
			originalRoot = md.Parser().Parse(text.NewReader(originalSource), parser.WithContext(parseContext))
			if ctx.Err() != nil {
				return nil
			}
			references = parseContext.References()
			originalParsed = true
		}
		terminalCharts := make(map[string][]string)
		var expanded string
		if s.imageState.earlyPreview {
			expanded = sourceText
		} else if termimg.DetectProtocol() == termimg.ProtocolNone {
			var err error
			expandCtx, stopExpand := context.WithCancel(ctx)
			defer stopExpand()
			chartBytes := 0
			renderChart := func(data spreadsheet.ChartData) string {
				if s.imageState.markNavigation && len(terminalCharts) >= limits.charts {
					s.imageState.err = fmt.Errorf("%w: too many embedded charts", errReaderRenderLimit)
					stopExpand()
					return ""
				}
				rendered := renderEmbeddedTerminalChart(data, max(width-4, 1), 16, s.theme)
				if s.imageState.markNavigation && len(rendered) > limits.chartBytes-chartBytes {
					s.imageState.err = fmt.Errorf("%w: embedded chart output exceeds %d bytes", errReaderRenderLimit, limits.chartBytes)
					stopExpand()
					return ""
				}
				marker := s.nextDocumentImageMarkerContext(ctx)
				if marker == 0 {
					stopExpand()
					return ""
				}
				slot := string(marker)
				terminalCharts[slot] = strings.Split(rendered, "\n")
				chartBytes += len(rendered)
				return "![Chart](" + slot + ")"
			}
			if s.imageState.markNavigation {
				expanded, _, err = printing.ExpandEmbeddedReferencesWithChartRendererLimitedContext(expandCtx, sourceText, doc.Workbook,
					renderChart, printing.EmbeddedChartLimits{ExpandedBytes: limits.bytes, ReferenceCount: limits.charts})
			} else {
				expanded, _, err = printing.ExpandEmbeddedReferencesWithChartRendererContext(expandCtx, sourceText, doc.Workbook, renderChart)
			}
			if err != nil {
				if errors.Is(err, printing.ErrEmbeddedExpansionLimit) {
					s.imageState.err = fmt.Errorf("%w: %v", errReaderRenderLimit, err)
				}
				return nil
			}
		} else {
			var chartImages []artifact.Image
			var err error
			if s.imageState.markNavigation {
				expanded, chartImages, _, err = printing.ExpandEmbeddedReferencesWithImagesLimitedContext(ctx, sourceText, doc.Workbook,
					printing.EmbeddedChartLimits{Count: limits.charts, Bytes: limits.chartBytes, ExpandedBytes: limits.bytes, ReferenceCount: limits.charts})
			} else {
				expanded, chartImages, _, err = printing.ExpandEmbeddedReferencesWithImagesContext(ctx, sourceText, doc.Workbook)
			}
			if err != nil {
				if errors.Is(err, printing.ErrEmbeddedChartLimit) || errors.Is(err, printing.ErrEmbeddedExpansionLimit) {
					s.imageState.err = fmt.Errorf("%w: %v", errReaderRenderLimit, err)
				}
				return nil
			}
			for _, img := range chartImages {
				assets[img.Name] = img
			}
			if ctx.Err() != nil || s.imageState.err != nil {
				return nil
			}
		}
		rewrittenText := expanded
		if strings.Contains(expanded, "](") {
			rewritten, _, err := extractDocumentLinksTextContext(ctx, expanded, true, false)
			if err != nil {
				return nil
			}
			rewrittenText = rewritten
		}
		var source []byte
		var root ast.Node
		if originalParsed && !originalRootUsed && rewrittenText == doc.Body {
			originalRootUsed = true
			source, root = originalSource, originalRoot
		} else {
			source = []byte(rewrittenText)
			context := parser.NewContext()
			for _, reference := range references {
				context.AddReference(reference)
			}
			root = md.Parser().Parse(text.NewReader(source), parser.WithContext(context))
		}
		if ctx.Err() != nil {
			return nil
		}
		progressSourceLen := len(source)
		slots := map[rune][]string{}
		var images []*ast.Image
		var headings []*ast.Heading
		var anchors []*ast.RawHTML
		var htmlBlocks []*ast.HTMLBlock
		nodes, blockStart, blockEnd := 0, progressSourceLen, 0
		err := ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
			nodes++
			if nodes%256 == 0 {
				if err := ctx.Err(); err != nil {
					return ast.WalkStop, err
				}
			}
			if entering {
				if node.Parent() == root {
					blockStart, blockEnd = progressSourceLen, 0
				}
				if node != root && s.imageState.markNavigation {
					updateReaderNodeSpan(node, progressSourceLen, &blockStart, &blockEnd)
				}
				switch node := node.(type) {
				case *ast.Image:
					images = append(images, node)
				case *ast.Heading:
					headings = append(headings, node)
				case *ast.RawHTML:
					anchors = append(anchors, node)
				case *ast.HTMLBlock:
					htmlBlocks = append(htmlBlocks, node)
				}
			} else if node.Parent() == root && s.imageState.markNavigation && blockEnd-blockStart > limits.blockBytes {
				return ast.WalkStop, fmt.Errorf("%w: a Markdown block exceeds %d bytes", errReaderRenderLimit, limits.blockBytes)
			}
			return ast.WalkContinue, nil
		})
		if err != nil {
			if ctx.Err() == nil && errors.Is(err, errReaderRenderLimit) {
				s.imageState.err = err
			}
			return nil
		}
		if len(assets) > 0 || s.imageState.markNavigation {
			for _, heading := range headings {
				if ctx.Err() != nil {
					return nil
				}
				if heading.Parent() != root {
					continue
				}
				targets := headingTargets[heading.Level]
				if len(targets) > 0 {
					index := targets[0]
					headingTargets[heading.Level] = targets[1:]
					marker := s.nextDocumentImageMarkerContext(ctx)
					if marker == 0 {
						return nil
					}
					s.imageState.navigation[marker] = index
					start := len(source)
					source = append(source, []byte(string(marker))...)
					heading.InsertBefore(heading, heading.FirstChild(), ast.NewTextSegment(text.NewSegment(start, len(source))))
				}
			}
			if s.imageState.markNavigation {
				for _, block := range htmlBlocks {
					if ctx.Err() != nil {
						return nil
					}
					for _, match := range documentImageAnchorRE.FindAllStringSubmatch(string(block.Text(source)), -1) {
						targets := anchorTargets[match[1]]
						if len(targets) > 0 {
							index := targets[0]
							anchorTargets[match[1]] = targets[1:]
							marker := s.nextDocumentImageMarkerContext(ctx)
							if marker == 0 {
								return nil
							}
							s.imageState.navigation[marker] = index
							start := len(source)
							source = append(source, []byte(string(marker))...)
							paragraph := ast.NewParagraph()
							paragraph.AppendChild(paragraph, ast.NewTextSegment(text.NewSegment(start, len(source))))
							block.Parent().InsertBefore(block.Parent(), block, paragraph)
						}
					}
				}
			}
			for _, anchor := range anchors {
				if ctx.Err() != nil {
					return nil
				}
				var raw strings.Builder
				for i := 0; i < anchor.Segments.Len(); i++ {
					segment := anchor.Segments.At(i)
					raw.Write(segment.Value(source))
				}
				match := documentImageAnchorRE.FindStringSubmatch(raw.String())
				if len(match) == 0 {
					continue
				}
				targets := anchorTargets[match[1]]
				if len(targets) > 0 {
					index := targets[0]
					anchorTargets[match[1]] = targets[1:]
					marker := s.nextDocumentImageMarkerContext(ctx)
					if marker == 0 {
						return nil
					}
					s.imageState.navigation[marker] = index
					start := len(source)
					source = append(source, []byte(string(marker))...)
					anchor.Parent().ReplaceChild(anchor.Parent(), anchor, ast.NewTextSegment(text.NewSegment(start, len(source))))
				}
			}
		}
		stream := s.imageState.markNavigation && len(doc.Body) >= asyncReaderSearchBytes && readerStreamSafe(ctx, source)
		if s.imageState.markNavigation && !stream && len(source) > limits.fallbackBytes {
			s.imageState.err = fmt.Errorf("%w: non-streamable Markdown exceeds %d bytes", errReaderRenderLimit, limits.fallbackBytes)
			return nil
		}
		s.imageState.captureRows = stream && s.readerProgress != nil
		prepareImages := func(images []*ast.Image) error {
			for _, img := range images {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				replaceImage := func(label string) {
					start := len(source)
					source = append(source, []byte(label)...)
					parent := img.Parent()
					parent.ReplaceChild(parent, img, ast.NewTextSegment(text.NewSegment(start, len(source))))
				}
				if s.imageState.earlyPreview {
					label := sanitizeTerminalLine(string(img.Text(source)))
					if label == "" {
						label = "image"
					}
					replaceImage("[image: " + label + "]")
					continue
				}
				name := strings.TrimPrefix(string(img.Destination), "./")
				if rows, ok := terminalCharts[name]; ok {
					slot := s.nextDocumentImageMarkerContext(ctx)
					if slot == 0 {
						if err := ctx.Err(); err != nil {
							return err
						}
						return s.imageState.err
					}
					slots[slot] = rows
					replaceImage(string(slot))
					continue
				}
				if path.Base(name) != name || !artifact.IsImageName(name) {
					continue
				}
				asset, found := assets[name]
				label := sanitizeTerminalLine(string(img.Text(source)))
				if label == "" {
					label = name
				}
				if !found {
					replaceImage("[missing image: " + label + "]")
					continue
				}
				rows, err := s.prepareDocumentImage(ctx, asset, max(width-4, 1))
				if err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if errors.Is(err, errReaderRenderLimit) {
						return err
					}
					replaceImage("[unavailable image: " + label + "]")
					continue
				}
				slot := s.nextDocumentImageMarkerContext(ctx)
				if slot == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
					return s.imageState.err
				}
				caption := ansi.Hardwrap(s.theme.Image.Render("["+label+"]"), max(width-4, 1), true)
				slots[slot] = append(strings.Split(caption, "\n"), rows...)
				replaceImage(string(slot))
			}
			return nil
		}
		if !stream {
			if err := prepareImages(images); err != nil {
				if ctx.Err() == nil {
					s.imageState.err = err
				}
				return nil
			}
		}
		options := glamouransi.Options{
			WordWrap: width, PreserveNewLines: true,
			ColorProfile: glamourColorProfile(terminalColorProfile), Styles: s.theme.Markdown,
		}
		if s.imageState.markNavigation && !stream && len(source) > limits.fallbackBytes {
			s.imageState.err = fmt.Errorf("%w: non-streamable Markdown exceeds %d bytes", errReaderRenderLimit, limits.fallbackBytes)
			return nil
		}
		var result []string
		progress := s.readerProgress
		s.readerProgressApproximate = strings.Contains(doc.Body, "](")
		if !stream {
			progress = nil
		}
		budget := readerLineBudget{
			maxBytes: limits.bytes, maxLines: limits.lines, maxLineLen: limits.lineBytes,
		}
		appendLine := func(line string) error {
			if s.imageState.markNavigation {
				if err := budget.accept(line); err != nil {
					return err
				}
			}
			result = append(result, line)
			return nil
		}
		processLine := func(line string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if s.imageState.markNavigation && len(line) > limits.lineBytes {
				return fmt.Errorf("%w: a rendered line exceeds %d bytes", errReaderRenderLimit, limits.lineBytes)
			}
			for _, marker := range line {
				if rows, ok := slots[marker]; ok {
					before, after, _ := strings.Cut(line, string(marker))
					if strings.TrimSpace(ansi.Strip(before)) != "" {
						if err := appendLine(before); err != nil {
							return err
						}
					}
					for _, row := range rows {
						if err := appendLine("  " + row); err != nil {
							return err
						}
					}
					line = after
				}
			}
			return appendLine(line)
		}
		var out bytes.Buffer
		lines := documentLineWriter{ctx: ctx, visit: processLine}
		var writer io.Writer = &out
		if stream {
			writer = &lines
		}
		var limiter *documentRenderLimiter
		if s.imageState.markNavigation {
			lines.maxLine = limits.lineBytes
			limiter = &documentRenderLimiter{writer: writer, limit: limits.bytes}
			writer = limiter
		}
		var renderErr error
		var windowSource *readerWindowSource
		if stream {
			cacheable := progress != nil && len(source) <= maxReaderCheckpointSource && nodes <= maxReaderCheckpointNodeVisits
			if cacheable {
				source = bytes.Clone(source)
			}
			cursor := glamouransi.NewStreamingCursor(ctx, options, source, root, writer)
			if cacheable {
				windowSource = &readerWindowSource{
					source: source, root: root, options: options, navigation: s.imageState.navigation,
					nextRow: readerCheckpointRows,
				}
				cursor.EnableCheckpoints()
			}
			sourceOffset, sourceLines, nextProgress := 0, 0, readerProgressSourceLines
			firstProgress := false
			imageIndex := 0
			imageBlock := func(img *ast.Image) ast.Node {
				node := ast.Node(img)
				for node.Parent() != nil && node.Parent() != root {
					node = node.Parent()
				}
				return node
			}
			var nextImageBlock ast.Node
			if len(images) > 0 {
				nextImageBlock = imageBlock(images[0])
			}
			for {
				node := cursor.NextNode()
				if node != nil && node == nextImageBlock {
					end := imageIndex
					for end < len(images) && imageBlock(images[end]) == node {
						end++
					}
					if err := prepareImages(images[imageIndex:end]); err != nil {
						renderErr = err
						break
					}
					imageIndex = end
					nextImageBlock = nil
					if imageIndex < len(images) {
						nextImageBlock = imageBlock(images[imageIndex])
					}
					cursor.SetSource(source)
				}
				done, err := cursor.Step()
				if err != nil {
					renderErr = err
					break
				}
				if done {
					break
				}
				if windowSource != nil && len(result) >= windowSource.nextRow {
					if err := cursor.Flush(); err != nil {
						renderErr = err
						break
					}
					if lines.pending.Len() == 0 {
						if point, ok := cursor.Checkpoint(); ok {
							windowSource.add(len(result), lines.blanks, point)
						}
					}
				}
				if progress != nil {
					end, err := readerBlockSourceEnd(ctx, node, progressSourceLen)
					if err != nil {
						renderErr = err
						break
					}
					if end > sourceOffset {
						sourceLines += bytes.Count(source[sourceOffset:end], []byte{'\n'})
						sourceOffset = end
					}
					if !firstProgress || sourceLines >= nextProgress {
						if err := cursor.Flush(); err != nil {
							renderErr = err
							break
						}
						if progress(result) {
							firstProgress = true
						}
						if sourceLines >= nextProgress {
							nextProgress = (sourceLines/readerProgressSourceLines + 1) * readerProgressSourceLines
						}
					}
				}
			}
		} else {
			var nodeRenderer renderer.NodeRenderer = glamouransi.NewRenderer(options)
			if ctx.Done() != nil {
				nodeRenderer = cancellableNodeRenderer{ctx: ctx, renderer: nodeRenderer}
			}
			md.SetRenderer(renderer.NewRenderer(renderer.WithNodeRenderers(util.Prioritized(nodeRenderer, 1000))))
			renderErr = md.Renderer().Render(writer, source, root)
		}
		if renderErr == nil && limiter != nil {
			renderErr = limiter.err
		}
		if renderErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			if s.imageState.markNavigation {
				s.imageState.err = fmt.Errorf("document reader render: %w", renderErr)
				return nil
			}
			return strings.Split(renderMarkdown(string(source), width, s.theme), "\n")
		}
		if !stream {
			if _, err := lines.Write(out.Bytes()); err != nil {
				if ctx.Err() == nil {
					s.imageState.err = err
				}
				return nil
			}
		}
		if err := lines.finish(); err != nil {
			if ctx.Err() == nil {
				s.imageState.err = err
			}
			return nil
		}
		if progress != nil && ctx.Err() == nil {
			progress(result)
		}
		if ctx.Err() == nil && windowSource != nil && len(windowSource.points) > 0 && windowSource.setSource(ctx, source, slots) {
			s.readerSource = windowSource
		}
		return result
	}
}

func (s *documentsScreen) nextDocumentImageMarker() rune {
	return s.nextDocumentImageMarkerContext(context.Background())
}

func (s *documentsScreen) nextDocumentImageMarkerContext(ctx context.Context) rune {
	if ctx.Err() != nil {
		return 0
	}
	if s.imageState.nextMarker < 0xf0000 {
		s.imageState.nextMarker = 0xf0000
	}
	if s.imageState.usedMarkers == nil {
		used := make(map[rune]struct{})
		nextCheck := 0
		for i, r := range s.imageState.source {
			if i >= nextCheck {
				if ctx.Err() != nil {
					return 0
				}
				nextCheck = i + 65536
			}
			if r >= 0xf0000 {
				used[r] = struct{}{}
			}
		}
		s.imageState.usedMarkers = used
	}
	for {
		marker := s.imageState.nextMarker
		if marker&4095 == 0 && ctx.Err() != nil {
			return 0
		}
		if marker > utf8.MaxRune {
			s.imageState.err = fmt.Errorf("document has too many image or navigation markers")
			return 0
		}
		s.imageState.nextMarker++
		if _, used := s.imageState.usedMarkers[marker]; !used {
			return marker
		}
	}
}

func (s *documentsScreen) prepareDocumentImage(ctx context.Context, asset artifact.Image, maxCols int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s:%d", asset.Name, maxCols)
	if rows, ok := s.imageState.prepared[key]; ok {
		return rows, nil
	}
	config, _, err := image.DecodeConfig(documentImageReader{ctx: ctx, reader: bytes.NewReader(asset.Data)})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width) > (16<<20)/int64(config.Height) {
		return nil, fmt.Errorf("image dimensions exceed terminal limits")
	}
	rows := min(8, max(1, int(math.Ceil(float64(config.Height)*float64(maxCols)/float64(config.Width)/2))))
	cols := min(maxCols, max(1, int(float64(rows)*2*float64(config.Width)/float64(config.Height))))
	pixelBytesPerColumn := rows * 2 * documentImagePixelScale * documentImagePixelScale * 4
	if s.imageState.markNavigation {
		limits := s.imageState.limits
		if len(s.imageState.prepared) >= limits.images ||
			cols > (limits.imageBytes-s.imageState.imageBytes)/pixelBytesPerColumn {
			return nil, fmt.Errorf("%w: too many decoded image bytes or images", errReaderRenderLimit)
		}
	}
	img, _, err := image.Decode(documentImageReader{ctx: ctx, reader: bytes.NewReader(asset.Data)})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Retain pixel detail for graphics protocols; the ANSI fallback samples
	// these same strips at two pixels per terminal cell.
	scaled := image.NewNRGBA(image.Rect(0, 0, cols*documentImagePixelScale, rows*2*documentImagePixelScale))
	for y := 0; y < scaled.Bounds().Dy(); y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < scaled.Bounds().Dx(); x++ {
			scaled.Set(x, y, img.At(x*config.Width/scaled.Bounds().Dx(), y*config.Height/scaled.Bounds().Dy()))
		}
	}
	if s.imageState.rows == nil {
		s.imageState.rows = map[rune]documentImageRow{}
	}
	var result []string
	for row := 0; row < rows; row++ {
		marker := s.nextDocumentImageMarkerContext(ctx)
		if marker == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, s.imageState.err
		}
		s.imageState.rows[marker] = documentImageRow{
			pixels: scaled,
			width:  cols,
			row:    row,
		}
		if s.imageState.captureRows {
			if s.imageState.pendingRows == nil {
				s.imageState.pendingRows = make(map[rune]documentImageRow)
			}
			s.imageState.pendingRows[marker] = s.imageState.rows[marker]
		}
		result = append(result, strings.Repeat(string(marker), cols))
	}
	s.imageState.graphicsUsed = true
	if s.imageState.prepared == nil {
		s.imageState.prepared = make(map[string][]string)
	}
	s.imageState.prepared[key] = result
	if s.imageState.markNavigation {
		s.imageState.imageBytes += cols * pixelBytesPerColumn
	}
	return result, nil
}

func (s *documentsScreen) renderDocumentImages(content string, width int) string {
	content, placements := s.documentImagePlacements(content, width)
	lines := strings.Split(content, "\n")
	protocol := termimg.DetectProtocol()
	for _, placement := range placements {
		top := placement.sourceRow * 2 * documentImagePixelScale
		cropped := placement.pixels.SubImage(image.Rect(0, top,
			placement.width*documentImagePixelScale, top+placement.height*2*documentImagePixelScale))
		var output string
		switch protocol {
		case termimg.ProtocolKitty:
			output = termimg.RenderKitty(cropped, placement.width, placement.height)
		case termimg.ProtocolITerm2:
			output = termimg.RenderITerm2(cropped, placement.width, placement.height)
		case termimg.ProtocolSixel:
			output = termimg.RenderSixel(cropped, placement.width, placement.height)
		default:
			for row, pixels := range strings.Split(termimg.Fallback(cropped, placement.width, placement.height), "\n") {
				line := lines[placement.y+row]
				lines[placement.y+row] = ansi.Truncate(line, placement.x, "") + pixels +
					ansi.TruncateLeft(line, placement.x+placement.width, "")
			}
			continue
		}
		// Paint once, after all text rows covered by the image. Drawing one
		// strip per cell row introduces seams and lets later text erase pixels.
		last := placement.y + placement.height - 1
		position := "\r"
		if placement.height > 1 {
			position += fmt.Sprintf("\x1b[%dA", placement.height-1)
		}
		if placement.x > 0 {
			position += fmt.Sprintf("\x1b[%dC", placement.x)
		}
		lines[last] += "\x1b[s" + position + output + "\x1b[u"
	}
	return strings.Join(lines, "\n")
}

func (s *documentsScreen) documentImagePlacements(content string, width int) (string, []documentImagePlacement) {
	lines := strings.Split(content, "\n")
	var placements []documentImagePlacement
	for i, line := range lines {
		for offset := 0; offset < len(line); {
			marker, size := utf8.DecodeRuneInString(line[offset:])
			row, ok := s.imageState.rows[marker]
			if !ok {
				offset += size
				continue
			}
			end := offset + size
			for strings.HasPrefix(line[end:], string(marker)) {
				end += size
			}
			x := ansi.StringWidth(line[:offset])
			cells := (end - offset) / size
			cols := min(cells, row.width, max(width-x, 0))
			line = line[:offset] + strings.Repeat(" ", cells) + line[end:]
			offset += cells
			if cols == 0 {
				continue
			}
			merged := false
			for p := range placements {
				previous := &placements[p]
				if previous.pixels == row.pixels && previous.x == x && previous.width == cols &&
					previous.y+previous.height == i && previous.sourceRow+previous.height == row.row {
					previous.height++
					merged = true
					break
				}
			}
			if !merged {
				placements = append(placements, documentImagePlacement{
					pixels: row.pixels, x: x, y: i, width: cols, height: 1, sourceRow: row.row,
				})
			}
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n"), placements
}

func (s *documentsScreen) clearDocumentImages() string {
	if s.imageState.graphicsUsed && termimg.DetectProtocol() == termimg.ProtocolKitty {
		return termimg.ClearKittyPlacements()
	}
	return ""
}

func (s *documentsScreen) documentImagesVisible() bool {
	return s.imageState.graphicsUsed && !s.showChanges && (s.reading || s.previewing)
}

func (s *documentsScreen) documentImageFrame() []documentImagePlacement {
	if !s.documentImagesVisible() || s.width < 40 || s.height < 10 ||
		termimg.DetectProtocol() == termimg.ProtocolNone {
		return nil
	}
	_, placements := s.documentImagePlacements(s.documentView(" "), s.width)
	return placements
}

func (s *documentsScreen) invalidateDocumentImages(previous []documentImagePlacement) {
	if !slices.Equal(previous, s.documentImageFrame()) {
		s.imageState.frame++
	}
}

func (s *documentsScreen) locateDocumentSections(pages []document.Page) {
	for p := range pages {
		for row, line := range pages[p].Lines {
			pages[p].Lines[row] = strings.Map(func(r rune) rune {
				if index, ok := s.imageState.navigation[r]; ok {
					s.imageState.outline[index].Page = p + 1
					return -1
				}
				return r
			}, line)
		}
	}
	for i := len(s.imageState.outline) - 2; i >= 0; i-- {
		if s.imageState.outline[i].SectionBreak {
			s.imageState.outline[i].Page = s.imageState.outline[i+1].Page
		}
	}
}
