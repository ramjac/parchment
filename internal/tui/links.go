package tui

import (
	"context"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/ramjac/parchment/internal/artifactfile"
	"github.com/ramjac/parchment/internal/document"
)

var internalLinkRE = regexp.MustCompile(`\[([^\]]+)\]\(\s*#([^\)\s]+)\s*\)`)

type linkRef struct {
	label  string
	target string
}

type linkHitBox struct {
	page     int
	row      int
	startCol int
	endCol   int
	target   string
}

// extractAndRewriteLinks rewrites [text](#target) into bare text and extracts
// the links in document order, ignoring fenced code blocks.
func extractAndRewriteLinks(lines []string) ([]string, []linkRef) {
	rewritten, links, _ := extractAndRewriteLinksContext(context.Background(), lines)
	return rewritten, links
}

func extractAndRewriteLinksContext(ctx context.Context, lines []string) ([]string, []linkRef, error) {
	var links []linkRef
	inFence := false
	rewritten := make([]string, len(lines))
	for i, line := range lines {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		rewritten[i] = rewriteDocumentLinkLine(line, &inFence, &links)
	}
	return rewritten, links, nil
}

func rewriteDocumentLinkLine(line string, inFence *bool, links *[]linkRef) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
		*inFence = !*inFence
		return line
	}
	if *inFence || !strings.Contains(line, "](") {
		return line
	}
	return internalLinkRE.ReplaceAllStringFunc(line, func(match string) string {
		sub := internalLinkRE.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		if links != nil {
			*links = append(*links, linkRef{label: sub[1], target: strings.TrimPrefix(sub[2], "#")})
		}
		return sub[1]
	})
}

func extractDocumentLinksTextContext(ctx context.Context, source string, rewrite, collect bool) (string, []linkRef, error) {
	var links []linkRef
	var linkList *[]linkRef
	if collect {
		linkList = &links
	}
	var result strings.Builder
	inFence := false
	last, start, lineCount := 0, 0, 0
	for {
		if lineCount%256 == 0 {
			if err := ctx.Err(); err != nil {
				return "", nil, err
			}
		}
		end := strings.IndexByte(source[start:], '\n')
		if end < 0 {
			end = len(source)
		} else {
			end += start
		}
		line := source[start:end]
		rewritten := rewriteDocumentLinkLine(line, &inFence, linkList)
		if rewrite && rewritten != line {
			if result.Len() == 0 {
				result.Grow(len(source))
			}
			result.WriteString(source[last:start])
			result.WriteString(rewritten)
			last = end
		}
		if end == len(source) {
			break
		}
		start = end + 1
		lineCount++
	}
	if result.Len() == 0 {
		return source, links, nil
	}
	result.WriteString(source[last:])
	return result.String(), links, nil
}

// paginateDocumentWithLinks paginates a document using Glamour, rewrites internal links
// so raw URLs are not printed, styles links with the shared link role,
// and computes hit-test boxes for mouse navigation in reader and preview.
func (s *documentsScreen) paginateDocumentWithLinks(doc document.Document) ([]document.Page, []linkHitBox, error) {
	s.imageState.markNavigation = false
	s.imageState.rows = make(map[rune]documentImageRow)
	s.imageState.pendingRows = nil
	s.imageState.prepared = make(map[string][]string)
	s.imageState.nextMarker = 0
	s.imageState.usedMarkers = nil
	s.imageState.err = nil
	s.imageState.outline, _ = document.Outline(doc)
	s.imageState.navigation = make(map[rune]int)
	s.imageState.source = doc.Body
	_, allLinks := extractAndRewriteLinks(strings.Split(doc.Body, "\n"))

	pages, err := document.PaginateWithFormatter(doc, s.documentMarkdownFormatter(doc))
	if err != nil {
		return nil, nil, err
	}
	if s.imageState.err != nil {
		return nil, nil, s.imageState.err
	}
	s.locateDocumentSections(pages)
	return pages, s.styleDocumentLinks(pages, allLinks), nil
}

func (s *documentsScreen) renderReaderDocument(doc document.Document, width int) ([]string, []document.Section, []int, []linkHitBox, error) {
	return s.renderReaderDocumentContext(context.Background(), doc, width)
}

func (s *documentsScreen) renderReaderDocumentContext(ctx context.Context, doc document.Document, width int) ([]string, []document.Section, []int, []linkHitBox, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	var err error
	doc.Body, err = artifactfile.StripPrivateBlocksContext(ctx, doc.Body)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	outline, err := document.ReaderOutlineContext(ctx, doc)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return s.renderReaderIndexedContext(ctx, doc, width, outline)
}

func (s *documentsScreen) renderReaderIndexedContext(ctx context.Context, doc document.Document, width int, outline []document.Section) ([]string, []document.Section, []int, []linkHitBox, error) {
	s.readerSource = nil
	s.imageState.rows = make(map[rune]documentImageRow)
	s.imageState.pendingRows = nil
	s.imageState.prepared = make(map[string][]string)
	s.imageState.nextMarker = 0
	s.imageState.usedMarkers = nil
	s.imageState.err = nil
	s.imageState.outline = outline
	s.imageState.navigation = make(map[rune]int)
	s.imageState.source = doc.Body
	s.imageState.markNavigation = true
	formatter := s.documentMarkdownSourceFormatterContext(ctx, doc)
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	lines := formatter(doc.Body, width)
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	if s.imageState.err != nil {
		return nil, nil, nil, nil, s.imageState.err
	}
	rows := make([]int, len(outline))
	for i := range rows {
		rows[i] = -1
	}
	for row, line := range lines {
		if row%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, nil, nil, err
			}
		}
		lines[row] = strings.Map(func(r rune) rune {
			if index, ok := s.imageState.navigation[r]; ok {
				rows[index] = row
				return -1
			}
			return r
		}, line)
	}
	var links []linkRef
	var err error
	if strings.Contains(doc.Body, "](") {
		_, links, err = extractDocumentLinksTextContext(ctx, doc.Body, false, true)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	}
	pages := []document.Page{{Lines: lines}}
	boxes, err := s.styleDocumentLinksContext(ctx, pages, links)
	if err == nil && s.readerSource != nil && !s.readerSource.setLinks(boxes, s.theme.Link) {
		s.readerSource = nil
	}
	if err == nil && s.readerSource != nil {
		s.readerSource.setSearchIndex(ctx, lines)
	}
	return lines, outline, rows, boxes, err
}

func (s *documentsScreen) styleDocumentLinks(pages []document.Page, allLinks []linkRef) []linkHitBox {
	boxes, _ := s.styleDocumentLinksContext(context.Background(), pages, allLinks)
	return boxes
}

func (s *documentsScreen) styleDocumentLinksContext(ctx context.Context, pages []document.Page, allLinks []linkRef) ([]linkHitBox, error) {
	var hitBoxes []linkHitBox
	linkStyle := s.theme.Link

	linkIdx := 0
	for p := 0; p < len(pages) && linkIdx < len(allLinks); p++ {
		for r := 0; r < len(pages[p].Lines) && linkIdx < len(allLinks); r++ {
			if r%256 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			line := pages[p].Lines[r]
			clean := ansi.Strip(line)
			searchOffset := 0

			for linkIdx < len(allLinks) {
				curLink := allLinks[linkIdx]
				label := curLink.label
				idx := strings.Index(clean[searchOffset:], label)
				if idx >= 0 {
					startByte := searchOffset + idx
					startCol := runewidth.StringWidth(clean[:startByte])
					endCol := startCol + runewidth.StringWidth(label)

					hitBoxes = append(hitBoxes, linkHitBox{
						page:     p,
						row:      r,
						startCol: startCol,
						endCol:   endCol,
						target:   curLink.target,
					})

					prefix := ansi.Truncate(line, startCol, "")
					middle := ansi.Truncate(ansi.TruncateLeft(line, startCol, ""), endCol-startCol, "")
					suffix := ansi.TruncateLeft(line, endCol, "")
					line = prefix + linkStyle.Render(ansi.Strip(middle)) + suffix
					pages[p].Lines[r] = line

					searchOffset = startByte + len(label)
					linkIdx++
				} else {
					if r+1 < len(pages[p].Lines) {
						nextLine := pages[p].Lines[r+1]
						nextClean := ansi.Strip(nextLine)
						words := strings.Fields(label)
						matched := false
						if len(words) > 1 {
							for splitIdx := 1; splitIdx < len(words); splitIdx++ {
								part1 := strings.Join(words[:splitIdx], " ")
								part2 := strings.Join(words[splitIdx:], " ")
								idx1 := strings.Index(clean[searchOffset:], part1)
								idx2 := strings.Index(nextClean, part2)
								if idx1 >= 0 && idx2 >= 0 {
									startCol1 := runewidth.StringWidth(clean[:searchOffset+idx1])
									endCol1 := startCol1 + runewidth.StringWidth(part1)

									startCol2 := runewidth.StringWidth(nextClean[:idx2])
									endCol2 := startCol2 + runewidth.StringWidth(part2)

									hitBoxes = append(hitBoxes, linkHitBox{
										page:     p,
										row:      r,
										startCol: startCol1,
										endCol:   endCol1,
										target:   curLink.target,
									})
									hitBoxes = append(hitBoxes, linkHitBox{
										page:     p,
										row:      r + 1,
										startCol: startCol2,
										endCol:   endCol2,
										target:   curLink.target,
									})

									prefix1 := ansi.Truncate(line, startCol1, "")
									middle1 := ansi.Truncate(ansi.TruncateLeft(line, startCol1, ""), endCol1-startCol1, "")
									suffix1 := ansi.TruncateLeft(line, endCol1, "")
									line = prefix1 + linkStyle.Render(ansi.Strip(middle1)) + suffix1
									pages[p].Lines[r] = line

									prefix2 := ansi.Truncate(nextLine, startCol2, "")
									middle2 := ansi.Truncate(ansi.TruncateLeft(nextLine, startCol2, ""), endCol2-startCol2, "")
									suffix2 := ansi.TruncateLeft(nextLine, endCol2, "")
									pages[p].Lines[r+1] = prefix2 + linkStyle.Render(ansi.Strip(middle2)) + suffix2

									matched = true
									linkIdx++
									break
								}
							}
						}
						if matched {
							continue
						}
					}
					break
				}
			}
		}
	}

	return hitBoxes, ctx.Err()
}
