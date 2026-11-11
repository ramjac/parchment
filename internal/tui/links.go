package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/document"
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
	var links []linkRef
	inFence := false
	rewritten := make([]string, len(lines))
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			rewritten[i] = line
			continue
		}
		if inFence {
			rewritten[i] = line
			continue
		}
		rewritten[i] = internalLinkRE.ReplaceAllStringFunc(line, func(match string) string {
			sub := internalLinkRE.FindStringSubmatch(match)
			if len(sub) == 3 {
				label := sub[1]
				target := strings.TrimPrefix(sub[2], "#")
				links = append(links, linkRef{label: label, target: target})
				return label
			}
			return match
		})
	}
	return rewritten, links
}

// paginateDocumentWithLinks paginates a document using Glamour, rewrites internal links
// so raw URLs are not printed, styles links with the primary theme color and underline,
// and computes hit-test boxes for mouse navigation in reader and preview.
func (s *documentsScreen) paginateDocumentWithLinks(doc document.Document) ([]document.Page, []linkHitBox, error) {
	_, allLinks := extractAndRewriteLinks(strings.Split(doc.Body, "\n"))

	pages, err := document.PaginateWithFormatter(doc, func(lines []string, width int) []string {
		rewritten, _ := extractAndRewriteLinks(lines)
		return strings.Split(renderMarkdown(strings.Join(rewritten, "\n"), width), "\n")
	})
	if err != nil {
		return nil, nil, err
	}

	if len(allLinks) == 0 {
		return pages, nil, nil
	}

	var hitBoxes []linkHitBox
	linkStyle := lipgloss.NewStyle().Underline(true).Foreground(s.theme.Primary)

	linkIdx := 0
	for p := 0; p < len(pages) && linkIdx < len(allLinks); p++ {
		for r := 0; r < len(pages[p].Lines) && linkIdx < len(allLinks); r++ {
			line := pages[p].Lines[r]
			clean := ansi.Strip(line)
			searchOffset := 0

			for linkIdx < len(allLinks) {
				curLink := allLinks[linkIdx]
				label := curLink.label
				idx := strings.Index(clean[searchOffset:], label)
				if idx >= 0 {
					startCol := searchOffset + idx
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

					searchOffset = endCol
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
									startCol1 := searchOffset + idx1
									endCol1 := startCol1 + runewidth.StringWidth(part1)

									startCol2 := idx2
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

	return pages, hitBoxes, nil
}
