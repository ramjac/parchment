package document

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// Page is one printed page: PageRows lines of PageCols characters, including
// margins, header, and footer.
type Page struct {
	Number int
	Lines  []string
}

// Paginate lays a document out as printer-friendly monospaced pages, applying
// its margins, columns, page and section breaks, header, footer, and page
// numbers. Markdown is converted to plain text suitable for any printer;
// images print as bracketed placeholders.
func Paginate(d Document) ([]Page, error) {
	if err := d.Layout.Validate(); err != nil {
		return nil, err
	}
	g, err := d.Layout.Geometry()
	if err != nil {
		return nil, err
	}
	bodies := paginateBody(d.Body, d.Layout.Columns, g.TextCols, g.BodyRows(), nil)
	pages := make([]Page, len(bodies))
	for i, rows := range bodies {
		pages[i] = Page{Number: i + 1, Lines: composePage(d, g, rows, i+1, len(bodies))}
	}
	return pages, nil
}

// Section is one heading in a document and the page it is printed on.
type Section struct {
	Level int
	Title string
	Page  int
}

// Outline lists the document's headings with the page each one lands on, in
// the same layout Paginate produces.
func Outline(d Document) ([]Section, error) {
	if err := d.Layout.Validate(); err != nil {
		return nil, err
	}
	g, err := d.Layout.Geometry()
	if err != nil {
		return nil, err
	}
	var sections []Section
	paginateBody(d.Body, d.Layout.Columns, g.TextCols, g.BodyRows(), &sections)
	return sections, nil
}

// Print renders every page as text with a form feed between pages, ready to
// send to a printer.
func Print(d Document) (string, error) {
	pages, err := Paginate(d)
	if err != nil {
		return "", err
	}
	rendered := make([]string, len(pages))
	for i, page := range pages {
		rendered[i] = strings.Join(page.Lines, "\n") + "\n"
	}
	return strings.Join(rendered, "\f"), nil
}

func clampColumns(columns, textCols int) int {
	columns = min(max(columns, 1), MaxColumns)
	for columns > 1 && ColumnWidth(textCols, columns) < MinColumnWidth {
		columns--
	}
	return columns
}

type headMark struct {
	line  int
	level int
	title string
}

func paginateBody(body string, defaultColumns, textCols, bodyRows int, sections *[]Section) [][]string {
	var pages [][]string
	var current []string
	pendingBreaks := 0
	newPage := func() {
		pages = append(pages, current)
		current = nil
	}
	for _, r := range splitRuns(body, defaultColumns) {
		columns := clampColumns(r.columns, textCols)
		width := ColumnWidth(textCols, columns)
		var marks []headMark
		raw := formatBlocks(r.lines, width, &marks)
		flat := trimBlankEnds(raw)
		pos := 0 // index of flat[0] within raw
		for pos < len(raw) && raw[pos] == "" {
			pos++
		}
		record := func(consumed int) {
			for len(marks) > 0 && marks[0].line < pos+consumed {
				if sections != nil {
					*sections = append(*sections, Section{Level: marks[0].level, Title: marks[0].title, Page: len(pages) + 1})
				}
				marks = marks[1:]
			}
		}
		if len(flat) > 0 {
			for ; pendingBreaks > 0; pendingBreaks-- {
				newPage()
			}
			balance := columns > 1 && r.end == sectionBreak && r.continuous
			if len(current) > 0 && current[len(current)-1] != "" && bodyRows-len(current) > 1 {
				current = append(current, "")
			}
			for len(flat) > 0 {
				if len(current) >= bodyRows {
					newPage()
				}
				if len(current) == 0 {
					pos += leadingBlanks(flat)
					flat = trimBlankEnds(flat)
					if len(flat) == 0 {
						break
					}
				}
				remaining := bodyRows - len(current)
				n := min(len(flat), remaining*columns)
				chunk := flat[:n]
				flat = flat[n:]
				height := min(remaining, n)
				if balance && len(flat) == 0 {
					height = (n + columns - 1) / columns
				}
				current = append(current, columnRows(chunk, columns, width, height)...)
				pos += n
				record(0)
			}
		}
		if r.end == pageBreak || (r.end == sectionBreak && !r.continuous) {
			pendingBreaks++
		}
	}
	if len(current) > 0 || len(pages) == 0 {
		pages = append(pages, current)
	}
	return pages
}

func leadingBlanks(lines []string) int {
	n := 0
	for n < len(lines) && lines[n] == "" {
		n++
	}
	return n
}

func trimBlankEnds(lines []string) []string {
	start, end := 0, len(lines)
	for start < end && lines[start] == "" {
		start++
	}
	for end > start && lines[end-1] == "" {
		end--
	}
	return lines[start:end]
}

// columnRows arranges lines into columns of the given height, filling each
// column top to bottom before starting the next.
func columnRows(lines []string, columns, width, height int) []string {
	if columns == 1 {
		return lines
	}
	rows := make([]string, height)
	for r := range rows {
		cells := make([]string, 0, columns)
		for c := 0; c < columns; c++ {
			cell := ""
			if i := c*height + r; i < len(lines) {
				cell = lines[i]
			}
			cells = append(cells, runewidth.FillRight(cell, width))
		}
		rows[r] = strings.TrimRight(strings.Join(cells, strings.Repeat(" ", ColumnGutter)), " ")
	}
	return rows
}

func composePage(d Document, g Geometry, body []string, number, total int) []string {
	lines := make([]string, 0, g.PageRows)
	pad := strings.Repeat(" ", g.LeftCols)
	add := func(text string) { lines = append(lines, strings.TrimRight(pad+text, " ")) }
	tokens := strings.NewReplacer(
		"{title}", strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, d.Title),
		"{page}", strconv.Itoa(number), "{pages}", strconv.Itoa(total),
	)
	for i := 0; i < g.TopRows; i++ {
		lines = append(lines, "")
	}
	if d.Layout.Header != "" {
		add(runningLine(tokens.Replace(d.Layout.Header), g.TextCols))
		add("")
	}
	for _, row := range body {
		add(row)
	}
	for i := len(body); i < g.BodyRows(); i++ {
		add("")
	}
	if g.FooterRows > 0 {
		add("")
		if d.Layout.Footer != "" {
			add(runningLine(tokens.Replace(d.Layout.Footer), g.TextCols))
		}
		if d.Layout.numbered() {
			text := fmt.Sprintf("Page %d of %d", number, total)
			gap := 0
			switch d.Layout.PageNumbers {
			case NumbersCenter:
				gap = (g.TextCols - runewidth.StringWidth(text)) / 2
			case NumbersRight:
				gap = g.TextCols - runewidth.StringWidth(text)
			}
			add(strings.Repeat(" ", max(gap, 0)) + text)
		}
	}
	for len(lines) < g.PageRows {
		lines = append(lines, "")
	}
	return lines
}

// runningLine places up to three "|"-separated parts across a line.
func runningLine(spec string, width int) string {
	parts := strings.SplitN(spec, "|", 3)
	var left, center, right string
	switch len(parts) {
	case 1:
		left = parts[0]
	case 2:
		left, right = parts[0], parts[1]
	default:
		left, center, right = parts[0], parts[1], parts[2]
	}
	cells := make([]string, width)
	for i := range cells {
		cells[i] = " "
	}
	put := func(x int, text string) {
		for _, r := range text {
			w := runewidth.RuneWidth(r)
			if w == 0 {
				continue
			}
			if x < 0 || x+w > width {
				return
			}
			cells[x] = string(r)
			for k := 1; k < w; k++ {
				cells[x+k] = ""
			}
			x += w
		}
	}
	left = runewidth.Truncate(left, width, "")
	put(0, left)
	if right != "" {
		right = runewidth.Truncate(right, width-runewidth.StringWidth(left), "")
		put(width-runewidth.StringWidth(right), right)
	}
	if center != "" {
		leftEnd := runewidth.StringWidth(left)
		if leftEnd > 0 {
			leftEnd++
		}
		rightStart := width - runewidth.StringWidth(right)
		if right != "" {
			rightStart--
		}
		center = runewidth.Truncate(center, max(rightStart-leftEnd, 0), "")
		x := max((width-runewidth.StringWidth(center))/2, leftEnd)
		if x+runewidth.StringWidth(center) > rightStart {
			x = rightStart - runewidth.StringWidth(center)
		}
		put(x, center)
	}
	return strings.TrimRight(strings.Join(cells, ""), " ")
}

var (
	headingRE   = regexp.MustCompile(`^(#{1,6})\s+(.*?)(?:\s+#+)?$`)
	ruleRE      = regexp.MustCompile(`^(?:\*\s*){3,}$|^(?:-\s*){3,}$|^(?:_\s*){3,}$`)
	listItemRE  = regexp.MustCompile(`^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$`)
	quoteRE     = regexp.MustCompile(`^>\s?(.*)$`)
	commentRE   = regexp.MustCompile(`^<!--.*-->$`)
	imageRE     = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	linkRE      = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	boldRE      = regexp.MustCompile(`\*\*(.+?)\*\*`)
	boldUnderRE = regexp.MustCompile(`__(.+?)__`)
	italicRE    = regexp.MustCompile(`\*([^*\s](?:[^*]*[^*\s])?)\*`)
	strikeRE    = regexp.MustCompile(`~~(.+?)~~`)
	codeRE      = regexp.MustCompile("`([^`]+)`")
)

func inlineText(text string) string {
	text = imageRE.ReplaceAllStringFunc(text, func(match string) string {
		alt := strings.TrimSpace(imageRE.FindStringSubmatch(match)[1])
		if alt == "" {
			return "[Image]"
		}
		return "[Image: " + alt + "]"
	})
	text = linkRE.ReplaceAllString(text, "$1 ($2)")
	text = boldRE.ReplaceAllString(text, "$1")
	text = boldUnderRE.ReplaceAllString(text, "$1")
	text = italicRE.ReplaceAllString(text, "$1")
	text = strikeRE.ReplaceAllString(text, "$1")
	return codeRE.ReplaceAllString(text, "$1")
}

type paragraph struct {
	active      bool
	quote       bool
	first, rest string
	text        []string
}

// formatBlocks converts Markdown lines to plain text wrapped to width.
func formatBlocks(lines []string, width int, marks *[]headMark) []string {
	var out []string
	var p paragraph
	flush := func() {
		if p.active {
			out = append(out, wrap(inlineText(strings.Join(p.text, " ")), width, p.first, p.rest)...)
		}
		p = paragraph{}
	}
	blank := func() {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}
	fence := ""
	for _, raw := range lines {
		line := strings.TrimRight(strings.ReplaceAll(raw, "\t", "    "), " \r")
		if fence != "" {
			if strings.HasPrefix(strings.TrimSpace(line), fence) {
				fence = ""
				blank()
				continue
			}
			out = append(out, hardWrap("    "+line, width)...)
			continue
		}
		trimmed := strings.TrimSpace(line)
		if marker, ok := fenceOpener(line); ok {
			flush()
			blank()
			fence = marker
			continue
		}
		switch {
		case trimmed == "":
			flush()
			blank()
		case commentRE.MatchString(trimmed):
		case headingRE.MatchString(trimmed):
			flush()
			blank()
			match := headingRE.FindStringSubmatch(trimmed)
			text := inlineText(match[2])
			if marks != nil {
				*marks = append(*marks, headMark{line: len(out), level: len(match[1]), title: text})
			}
			if len(match[1]) == 1 {
				text = strings.ToUpper(text)
			}
			wrapped := wrap(text, width, "", "")
			out = append(out, wrapped...)
			if len(match[1]) <= 2 {
				longest := 0
				for _, w := range wrapped {
					longest = max(longest, runewidth.StringWidth(w))
				}
				underline := "="
				if len(match[1]) == 2 {
					underline = "-"
				}
				out = append(out, strings.Repeat(underline, longest))
			}
			blank()
		case ruleRE.MatchString(trimmed):
			flush()
			blank()
			out = append(out, strings.Repeat("-", width))
			blank()
		case strings.HasPrefix(trimmed, "|"):
			flush()
			out = append(out, hardWrap(line, width)...)
		case listItemRE.MatchString(line):
			flush()
			match := listItemRE.FindStringSubmatch(line)
			prefix := match[1] + match[2] + " "
			p = paragraph{active: true, first: prefix, rest: strings.Repeat(" ", runewidth.StringWidth(prefix)), text: []string{match[3]}}
		case quoteRE.MatchString(trimmed):
			text := quoteRE.FindStringSubmatch(trimmed)[1]
			if !p.active || !p.quote {
				flush()
				p = paragraph{active: true, quote: true, first: "> ", rest: "> "}
			}
			p.text = append(p.text, text)
		default:
			if !p.active {
				p = paragraph{active: true}
			}
			p.text = append(p.text, trimmed)
		}
	}
	flush()
	return out
}

// fitPrefix bounds a line prefix to half the width so the text keeps room to
// be readable. Indentation is dropped before the marker itself is truncated.
func fitPrefix(prefix string, width int) string {
	limit := max(width/2, 0)
	if runewidth.StringWidth(prefix) <= limit {
		return prefix
	}
	trimmed := strings.TrimLeft(prefix, " ")
	if runewidth.StringWidth(trimmed) <= limit {
		return strings.Repeat(" ", limit-runewidth.StringWidth(trimmed)) + trimmed
	}
	return runewidth.Truncate(trimmed, limit, "")
}

// wrap word-wraps text to width, using first and rest as line prefixes.
func wrap(text string, width int, first, rest string) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	first, rest = fitPrefix(first, width), fitPrefix(rest, width)
	prefix := first
	line := ""
	room := func() int { return max(width-runewidth.StringWidth(prefix), 1) }
	emit := func() {
		lines = append(lines, prefix+line)
		prefix, line = rest, ""
	}
	for _, word := range words {
		for runewidth.StringWidth(word) > room() {
			if line != "" {
				emit()
				continue
			}
			head := runewidth.Truncate(word, room(), "")
			if head == "" {
				head = string([]rune(word)[:1])
			}
			line = head
			word = strings.TrimPrefix(word, head)
			emit()
		}
		switch {
		case word == "":
		case line == "":
			line = word
		case runewidth.StringWidth(line)+1+runewidth.StringWidth(word) <= room():
			line += " " + word
		default:
			emit()
			line = word
		}
	}
	if line != "" {
		emit()
	}
	return lines
}

// hardWrap splits a preformatted line without reflowing it.
func hardWrap(line string, width int) []string {
	var lines []string
	for runewidth.StringWidth(line) > width {
		head := runewidth.Truncate(line, width, "")
		if head == "" {
			head = string([]rune(line)[:1])
		}
		lines = append(lines, head)
		line = strings.TrimPrefix(line, head)
	}
	return append(lines, line)
}
