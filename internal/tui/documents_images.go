package tui

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2/compat"
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

type documentImagePlacement struct {
	pixels              *image.NRGBA
	x, y, width, height int
	sourceRow           int
}

type documentImageState struct {
	rows           map[rune]documentImageRow
	nextMarker     rune
	graphicsUsed   bool
	prepared       map[string][]string
	outline        []document.Section
	navigation     map[rune]int
	markNavigation bool
	source         string
	frame          uint64
}

var documentImageAnchorRE = regexp.MustCompile(`(?i)<a\s+(?:id|name)=["']([^"']+)["'][^>]*>`)

func (s *documentsScreen) documentMarkdownFormatter(doc document.Document) document.Formatter {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	ctx := parser.NewContext()
	md.Parser().Parse(text.NewReader([]byte(doc.Body)), parser.WithContext(ctx))
	references := ctx.References()
	assets := make(map[string]artifact.Image, len(doc.Images))
	for _, img := range doc.Images {
		assets[img.Name] = img
	}
	return func(lines []string, width, _ int, _ string) []string {
		sourceText := strings.Join(lines, "\n")
		terminalCharts := make(map[string]spreadsheet.ChartData)
		var expanded string
		if termimg.DetectProtocol() == termimg.ProtocolNone {
			expanded, _ = printing.ExpandEmbeddedReferencesWithChartRenderer(sourceText, doc.Workbook, func(data spreadsheet.ChartData) string {
				slot := string(s.nextDocumentImageMarker())
				terminalCharts[slot] = data
				return "![Chart](" + slot + ")"
			})
		} else {
			var chartImages []artifact.Image
			expanded, chartImages, _ = printing.ExpandEmbeddedReferencesWithImages(sourceText, doc.Workbook)
			for _, img := range chartImages {
				assets[img.Name] = img
			}
		}
		lines = strings.Split(expanded, "\n")
		rewritten, _ := extractAndRewriteLinks(lines)
		source := []byte(strings.Join(rewritten, "\n"))
		context := parser.NewContext()
		for _, reference := range references {
			context.AddReference(reference)
		}
		root := md.Parser().Parse(text.NewReader(source), parser.WithContext(context))
		slots := map[rune][]string{}
		var images []*ast.Image
		var headings []*ast.Heading
		var anchors []*ast.RawHTML
		var htmlBlocks []*ast.HTMLBlock
		_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
			if entering {
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
			}
			return ast.WalkContinue, nil
		})
		if len(assets) > 0 || s.imageState.markNavigation {
			for _, heading := range headings {
				if heading.Parent() != root {
					continue
				}
				for index, section := range s.imageState.outline {
					if section.Level == heading.Level && !s.documentSectionMarked(index) {
						marker := s.nextDocumentImageMarker()
						s.imageState.navigation[marker] = index
						start := len(source)
						source = append(source, []byte(string(marker))...)
						heading.InsertBefore(heading, heading.FirstChild(), ast.NewTextSegment(text.NewSegment(start, len(source))))
						break
					}
				}
			}
			if s.imageState.markNavigation {
				for _, block := range htmlBlocks {
					for _, match := range documentImageAnchorRE.FindAllStringSubmatch(string(block.Text(source)), -1) {
						for index, section := range s.imageState.outline {
							if !section.Anchor || section.Slug != match[1] || s.documentSectionMarked(index) {
								continue
							}
							marker := s.nextDocumentImageMarker()
							s.imageState.navigation[marker] = index
							start := len(source)
							source = append(source, []byte(string(marker))...)
							paragraph := ast.NewParagraph()
							paragraph.AppendChild(paragraph, ast.NewTextSegment(text.NewSegment(start, len(source))))
							block.Parent().InsertBefore(block.Parent(), block, paragraph)
							break
						}
					}
				}
			}
			for _, anchor := range anchors {
				var raw strings.Builder
				for i := 0; i < anchor.Segments.Len(); i++ {
					segment := anchor.Segments.At(i)
					raw.Write(segment.Value(source))
				}
				match := documentImageAnchorRE.FindStringSubmatch(raw.String())
				if len(match) == 0 {
					continue
				}
				for index, section := range s.imageState.outline {
					if !section.Anchor || s.documentSectionMarked(index) {
						continue
					}
					if match[1] == section.Slug {
						marker := s.nextDocumentImageMarker()
						s.imageState.navigation[marker] = index
						start := len(source)
						source = append(source, []byte(string(marker))...)
						anchor.Parent().ReplaceChild(anchor.Parent(), anchor, ast.NewTextSegment(text.NewSegment(start, len(source))))
						break
					}
				}
			}
		}
		for _, img := range images {
			replaceImage := func(label string) {
				start := len(source)
				source = append(source, []byte(label)...)
				parent := img.Parent()
				parent.ReplaceChild(parent, img, ast.NewTextSegment(text.NewSegment(start, len(source))))
			}
			name := strings.TrimPrefix(string(img.Destination), "./")
			if data, ok := terminalCharts[name]; ok {
				slot := s.nextDocumentImageMarker()
				slots[slot] = strings.Split(renderEmbeddedTerminalChart(data, max(width-4, 1), 16, s.theme), "\n")
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
			rows, err := s.prepareDocumentImage(asset, max(width-4, 1))
			if err != nil {
				replaceImage("[unavailable image: " + label + "]")
				continue
			}
			slot := s.nextDocumentImageMarker()
			caption := ansi.Hardwrap(s.theme.Image.Render("["+label+"]"), max(width-4, 1), true)
			slots[slot] = append(strings.Split(caption, "\n"), rows...)
			replaceImage(string(slot))
		}
		ar := glamouransi.NewRenderer(glamouransi.Options{
			WordWrap: width, PreserveNewLines: true,
			ColorProfile: glamourColorProfile(compat.Profile), Styles: s.theme.Markdown,
		})
		md.SetRenderer(renderer.NewRenderer(renderer.WithNodeRenderers(util.Prioritized(ar, 1000))))
		var out bytes.Buffer
		if err := md.Renderer().Render(&out, source, root); err != nil {
			return strings.Split(renderMarkdown(string(source), width, s.theme), "\n")
		}
		var result []string
		for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
			for _, marker := range []rune(line) {
				if rows, ok := slots[marker]; ok {
					before, after, _ := strings.Cut(line, string(marker))
					if strings.TrimSpace(ansi.Strip(before)) != "" {
						result = append(result, before)
					}
					for _, row := range rows {
						result = append(result, "  "+row)
					}
					line = after
				}
			}
			result = append(result, line)
		}
		return result
	}
}

func (s *documentsScreen) nextDocumentImageMarker() rune {
	if s.imageState.nextMarker < 0xf0000 {
		s.imageState.nextMarker = 0xf0000
	}
	for {
		marker := s.imageState.nextMarker
		s.imageState.nextMarker++
		if !strings.ContainsRune(s.imageState.source, marker) {
			return marker
		}
	}
}

func (s *documentsScreen) prepareDocumentImage(asset artifact.Image, maxCols int) ([]string, error) {
	key := fmt.Sprintf("%s:%d", asset.Name, maxCols)
	if rows, ok := s.imageState.prepared[key]; ok {
		return rows, nil
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(asset.Data))
	if err != nil || config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width)*int64(config.Height) > 16<<20 {
		return nil, fmt.Errorf("image dimensions exceed terminal limits")
	}
	img, _, err := image.Decode(bytes.NewReader(asset.Data))
	if err != nil {
		return nil, err
	}
	rows := min(8, max(1, int(math.Ceil(float64(config.Height)*float64(maxCols)/float64(config.Width)/2))))
	cols := min(maxCols, max(1, int(float64(rows)*2*float64(config.Width)/float64(config.Height))))
	// Retain pixel detail for graphics protocols; the ANSI fallback samples
	// these same strips at two pixels per terminal cell.
	scaled := image.NewNRGBA(image.Rect(0, 0, cols*documentImagePixelScale, rows*2*documentImagePixelScale))
	for y := 0; y < scaled.Bounds().Dy(); y++ {
		for x := 0; x < scaled.Bounds().Dx(); x++ {
			scaled.Set(x, y, img.At(x*config.Width/scaled.Bounds().Dx(), y*config.Height/scaled.Bounds().Dy()))
		}
	}
	if s.imageState.rows == nil {
		s.imageState.rows = map[rune]documentImageRow{}
	}
	var result []string
	for row := 0; row < rows; row++ {
		marker := s.nextDocumentImageMarker()
		s.imageState.rows[marker] = documentImageRow{
			pixels: scaled,
			width:  cols,
			row:    row,
		}
		result = append(result, strings.Repeat(string(marker), cols))
	}
	s.imageState.graphicsUsed = true
	if s.imageState.prepared == nil {
		s.imageState.prepared = make(map[string][]string)
	}
	s.imageState.prepared[key] = result
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

func (s *documentsScreen) documentSectionMarked(index int) bool {
	for _, marked := range s.imageState.navigation {
		if index == marked {
			return true
		}
	}
	return false
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
