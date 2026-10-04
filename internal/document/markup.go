package document

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"example.com/parchment/internal/artifactfile"
)

// PageBreakMarkup starts a new page. Layout directives are HTML comments so
// ordinary Markdown tools ignore them and the body stays readable.
const PageBreakMarkup = "<!-- parchment:page-break -->"

// SectionBreakMarkup returns a section break that sets the column count for
// the text that follows. A continuous break stays on the current page.
func SectionBreakMarkup(columns int, continuous bool) string {
	markup := fmt.Sprintf("<!-- parchment:section-break columns=%d", columns)
	if continuous {
		markup += " continuous"
	}
	return markup + " -->"
}

var directiveRE = regexp.MustCompile(`^\s*<!--\s*parchment:(page-break|section-break)((?:\s+[a-z]+(?:=[0-9]+)?)*)\s*-->\s*$`)

type directiveKind int

const (
	pageBreak directiveKind = iota + 1
	sectionBreak
)

type directive struct {
	kind       directiveKind
	columns    int // zero means the layout default
	continuous bool
}

func parseDirective(line string) (directive, bool) {
	match := directiveRE.FindStringSubmatch(line)
	if match == nil {
		return directive{}, false
	}
	d := directive{kind: pageBreak}
	if match[1] == "section-break" {
		d.kind = sectionBreak
	}
	for _, attribute := range strings.Fields(match[2]) {
		name, value, _ := strings.Cut(attribute, "=")
		switch name {
		case "columns":
			if n, err := strconv.Atoi(value); err == nil {
				d.columns = min(max(n, 1), MaxColumns)
			}
		case "continuous":
			d.continuous = true
		}
	}
	return d, true
}

// fenceOpener returns the code fence marker that a line opens, if any.
func fenceOpener(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	for _, marker := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, marker) {
			return marker, true
		}
	}
	return "", false
}

// run is consecutive text laid out with one column count, followed by the
// break that ends it.
type run struct {
	columns int
	lines   []string
	// end is zero at the end of the document.
	end directiveKind
	// continuous reports that the following section starts on the same page.
	continuous bool
}

// splitRuns divides a Markdown body at layout directives. Directives inside
// fenced code blocks are ordinary text.
func splitRuns(body string, defaultColumns int) []run {
	body = artifactfile.StripPrivateBlocks(body)
	columns := defaultColumns
	current := run{columns: columns}
	var runs []run
	fence := ""
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if fence != "" {
			if strings.HasPrefix(strings.TrimSpace(line), fence) {
				fence = ""
			}
			current.lines = append(current.lines, line)
			continue
		}
		if marker, ok := fenceOpener(line); ok {
			fence = marker
			current.lines = append(current.lines, line)
			continue
		}
		d, ok := parseDirective(line)
		if !ok {
			current.lines = append(current.lines, line)
			continue
		}
		current.end, current.continuous = d.kind, d.kind == sectionBreak && d.continuous
		runs = append(runs, current)
		if d.kind == sectionBreak {
			columns = defaultColumns
			if d.columns > 0 {
				columns = d.columns
			}
		}
		current = run{columns: columns}
	}
	return append(runs, current)
}
