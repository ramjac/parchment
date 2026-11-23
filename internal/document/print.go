package document

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
)

// Page is one printed page: PageRows lines of PageCols characters, including
// margins, header, and footer.
type Page struct {
	Number int
	Lines  []string
	// Cells preserves the source text of each column for font-aware renderers.
	Cells [][]string
}

// Formatter formats one layout run to lines, given its width, columns, and full
// Markdown body for document-wide references.
type Formatter func(lines []string, width, columns int, body string) []string

// Paginate lays a document out as printer-friendly monospaced pages, applying
// its margins, columns, page and section breaks, header, footer, and page
// numbers. Markdown is converted to plain text suitable for any printer;
// images print as bracketed placeholders.
func Paginate(d Document) ([]Page, error) {
	return PaginateWithFormatter(d, nil)
}

func PaginateWithFormatter(d Document, formatter Formatter) ([]Page, error) {
	return PaginateWithFormatterContext(context.Background(), d, formatter)
}

// PaginateWithFormatterContext paginates a document while honoring cancellation.
func PaginateWithFormatterContext(ctx context.Context, d Document, formatter Formatter) ([]Page, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := d.Layout.Validate(); err != nil {
		return nil, err
	}
	g, err := d.Layout.Geometry()
	if err != nil {
		return nil, err
	}
	var bodyCells [][][]string
	directives := d.Kind != artifact.NoteKind && d.Kind != artifact.PresentationKind
	bodies, err := paginateBodyContext(ctx, d.Body, d.Layout.Columns, g.TextCols, g.BodyRows(), nil, formatter, &bodyCells, directives)
	if err != nil {
		return nil, err
	}
	pages := make([]Page, len(bodies))
	for i, rows := range bodies {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var cells [][]string
		if i < len(bodyCells) {
			cells = bodyCells[i]
		}
		pages[i] = composePage(d, g, rows, cells, i+1, len(bodies))
	}
	return pages, nil
}

// Section is one heading in a document and the page it is printed on.
type Section struct {
	Level        int
	Title        string
	Page         int
	Slug         string
	Anchor       bool
	SectionBreak bool
	Columns      int
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
	paginateBody(d.Body, d.Layout.Columns, g.TextCols, g.BodyRows(), &sections, nil, nil, true)
	return sections, nil
}

// ReaderOutline lists navigation targets without computing print page numbers.
func ReaderOutline(d Document) ([]Section, error) {
	return ReaderOutlineContext(context.Background(), d)
}

// ReaderOutlineContext builds reader navigation targets and honors cancellation.
func ReaderOutlineContext(ctx context.Context, d Document) ([]Section, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := d.Layout.Validate(); err != nil {
		return nil, err
	}
	if _, err := d.Layout.Geometry(); err != nil {
		return nil, err
	}
	var sections []Section
	seen := make(map[string]int)
	var marks []headMark
	f := blockFormatter{marks: &marks, seenSlugs: seen, indexOnly: true}
	finishRun := func() {
		f.flush()
		for _, mark := range marks {
			if mark.line < f.indexEnd {
				sections = append(sections, Section{
					Level: mark.level, Title: mark.title, Slug: mark.slug, Anchor: mark.anchor,
				})
			}
		}
		marks = nil
		f = blockFormatter{marks: &marks, seenSlugs: seen, indexOnly: true}
	}
	body, err := artifactfile.StripPrivateBlocksContext(ctx, d.Body)
	if err != nil {
		return nil, err
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	fence := ""
	for i := 0; ; i++ {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		line, rest, more := strings.Cut(body, "\n")
		if fence != "" {
			if strings.HasPrefix(strings.TrimSpace(line), fence) {
				fence = ""
			}
			f.processLine(line)
		} else if marker, ok := fenceOpener(line); ok {
			fence = marker
			f.processLine(line)
		} else if _, ok := parseDirective(line); ok {
			finishRun()
		} else {
			f.processLine(line)
		}
		if !more {
			break
		}
		body = rest
	}
	finishRun()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	line         int
	level        int
	title        string
	slug         string
	anchor       bool
	sectionBreak bool
	columns      int
}

type bodyPaginator struct {
	ctx           context.Context
	err           error
	textCols      int
	bodyRows      int
	sections      *[]Section
	pages         [][]string
	current       []string
	pageCells     [][][]string
	currentCells  [][]string
	pendingBreaks int
	seenSlugs     map[string]int
	formatter     Formatter
	sourceBody    string
}

func paginateBody(body string, defaultColumns, textCols, bodyRows int, sections *[]Section, formatter Formatter, cells *[][][]string, directives bool) [][]string {
	pages, _ := paginateBodyContext(context.Background(), body, defaultColumns, textCols, bodyRows, sections, formatter, cells, directives)
	return pages
}

func paginateBodyContext(ctx context.Context, body string, defaultColumns, textCols, bodyRows int, sections *[]Section, formatter Formatter, cells *[][][]string, directives bool) ([][]string, error) {
	p := bodyPaginator{
		textCols:   textCols,
		bodyRows:   bodyRows,
		ctx:        ctx,
		sections:   sections,
		seenSlugs:  make(map[string]int),
		formatter:  formatter,
		sourceBody: strings.ReplaceAll(artifactfile.StripPrivateBlocks(body), "\r\n", "\n"),
	}
	pages := p.paginate(body, defaultColumns, directives)
	if cells != nil {
		*cells = p.pageCells
	}
	return pages, p.err
}

func (p *bodyPaginator) paginate(body string, defaultColumns int, directives bool) [][]string {
	wasSectionBreak := false
	runs := []run{{columns: defaultColumns, lines: strings.Split(p.sourceBody, "\n")}}
	if directives {
		runs = splitRuns(body, defaultColumns)
	}
	for _, r := range runs {
		if p.err = p.ctx.Err(); p.err != nil {
			return nil
		}
		if wasSectionBreak && p.sections != nil {
			*p.sections = append(*p.sections, Section{
				Title:        fmt.Sprintf("── Section (%d cols)", clampColumns(r.columns, p.textCols)),
				Page:         len(p.pages) + 1,
				SectionBreak: true,
				Columns:      clampColumns(r.columns, p.textCols),
			})
		}
		wasSectionBreak = r.end == sectionBreak
		p.processRun(r)
		if p.err != nil {
			return nil
		}
		if p.err = p.ctx.Err(); p.err != nil {
			return nil
		}
	}
	if len(p.current) > 0 || len(p.pages) == 0 {
		p.newPage()
	}
	return p.pages
}

func (p *bodyPaginator) newPage() {
	p.pages = append(p.pages, p.current)
	p.current = nil
	if p.formatter != nil {
		p.pageCells = append(p.pageCells, p.currentCells)
		p.currentCells = nil
	}
}

func (p *bodyPaginator) processRun(r run) {
	columns := clampColumns(r.columns, p.textCols)
	width := ColumnWidth(p.textCols, columns)
	var marks []headMark
	var raw []string
	if p.formatter != nil {
		raw = p.formatter(r.lines, width, columns, p.sourceBody)
	} else {
		var headings *[]headMark
		if p.sections != nil {
			headings = &marks
		}
		raw = formatBlocks(r.lines, width, headings, p.seenSlugs)
	}
	flat := trimBlankEnds(raw)

	if len(flat) > 0 {
		for ; p.pendingBreaks > 0; p.pendingBreaks-- {
			p.newPage()
		}
		p.layoutRunLines(flat, &marks, raw, columns, width, r.end, r.continuous)
	}

	if r.end == pageBreak || (r.end == sectionBreak && !r.continuous) {
		p.pendingBreaks++
	}
}

func (p *bodyPaginator) layoutRunLines(flat []string, marks *[]headMark, raw []string, columns, width int, end directiveKind, continuous bool) {
	pos := leadingBlanks(raw)
	balance := columns > 1 && end == sectionBreak && continuous

	if len(p.current) > 0 && p.current[len(p.current)-1] != "" && p.bodyRows-len(p.current) > 1 {
		p.current = append(p.current, "")
		if p.formatter != nil {
			p.currentCells = append(p.currentCells, nil)
		}
	}

	for len(flat) > 0 {
		if p.err = p.ctx.Err(); p.err != nil {
			return
		}
		if len(p.current) >= p.bodyRows {
			p.newPage()
		}
		if len(p.current) == 0 {
			pos += leadingBlanks(flat)
			flat = trimBlankEnds(flat)
			if len(flat) == 0 {
				break
			}
		}
		remaining := p.bodyRows - len(p.current)
		n := min(len(flat), remaining*columns)
		chunk := flat[:n]
		flat = flat[n:]
		height := min(remaining, n)
		if balance && len(flat) == 0 {
			height = (n + columns - 1) / columns
		}
		p.current = append(p.current, columnRows(chunk, columns, width, height)...)
		if p.formatter != nil {
			p.currentCells = append(p.currentCells, columnCellRows(chunk, columns, height)...)
		}
		pos += n
		p.recordMarks(marks, pos)
	}
}

func (p *bodyPaginator) recordMarks(marks *[]headMark, pos int) {
	for len(*marks) > 0 && (*marks)[0].line < pos {
		if p.sections != nil {
			*p.sections = append(*p.sections, Section{
				Level:        (*marks)[0].level,
				Title:        (*marks)[0].title,
				Page:         len(p.pages) + 1,
				Slug:         (*marks)[0].slug,
				Anchor:       (*marks)[0].anchor,
				SectionBreak: (*marks)[0].sectionBreak,
				Columns:      (*marks)[0].columns,
			})
		}
		*marks = (*marks)[1:]
	}
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

func columnCellRows(lines []string, columns, height int) [][]string {
	rows := make([][]string, height)
	for r := range rows {
		rows[r] = make([]string, columns)
		for c := 0; c < columns; c++ {
			if i := c*height + r; i < len(lines) {
				rows[r][c] = lines[i]
			}
		}
	}
	return rows
}

func composePage(d Document, g Geometry, body []string, bodyCells [][]string, number, total int) Page {
	lines := make([]string, 0, g.PageRows)
	cells := make([][]string, 0, g.PageRows)
	pad := strings.Repeat(" ", g.LeftCols)
	add := func(text string, rowCells []string) {
		lines = append(lines, strings.TrimRight(pad+text, " "))
		cells = append(cells, rowCells)
	}
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
		add("", nil)
	}
	if d.Layout.Header != "" {
		add(runningLine(tokens.Replace(d.Layout.Header), g.TextCols), nil)
		add("", nil)
	}
	for i, row := range body {
		var rowCells []string
		if i < len(bodyCells) {
			rowCells = bodyCells[i]
		}
		add(row, rowCells)
	}
	for i := len(body); i < g.BodyRows(); i++ {
		add("", nil)
	}
	if g.FooterRows > 0 {
		add("", nil)
		if d.Layout.Footer != "" {
			add(runningLine(tokens.Replace(d.Layout.Footer), g.TextCols), nil)
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
			add(strings.Repeat(" ", max(gap, 0))+text, nil)
		}
	}
	for len(lines) < g.PageRows {
		add("", nil)
	}
	return Page{Number: number, Lines: lines, Cells: cells}
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
	setextRE    = regexp.MustCompile(`^(?:=+|-+)\s*$`)
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
	anchorRE    = regexp.MustCompile(`(?i)<a\s+(?:id|name)=["']([^"']+)["'][^>]*>`)
	slugPunctRE = regexp.MustCompile(`[^a-z0-9\s-]+`)
	slugSpaceRE = regexp.MustCompile(`\s+`)
)

func inlineText(text string) string {
	if !strings.ContainsAny(text, "![]*_~`") {
		return text
	}
	if strings.Contains(text, "![") {
		text = imageRE.ReplaceAllStringFunc(text, func(match string) string {
			alt := strings.TrimSpace(imageRE.FindStringSubmatch(match)[1])
			if alt == "" {
				return "[Image]"
			}
			return "[Image: " + alt + "]"
		})
	}
	if strings.Contains(text, "[") {
		text = linkRE.ReplaceAllString(text, "$1 ($2)")
	}
	if strings.Contains(text, "**") {
		text = boldRE.ReplaceAllString(text, "$1")
	}
	if strings.Contains(text, "__") {
		text = boldUnderRE.ReplaceAllString(text, "$1")
	}
	if strings.Contains(text, "*") {
		text = italicRE.ReplaceAllString(text, "$1")
	}
	if strings.Contains(text, "~~") {
		text = strikeRE.ReplaceAllString(text, "$1")
	}
	if strings.Contains(text, "`") {
		text = codeRE.ReplaceAllString(text, "$1")
	}
	return text
}

type paragraph struct {
	active      bool
	quote       bool
	first, rest string
	text        []string
}

type blockFormatter struct {
	seenSlugs  map[string]int
	width      int
	marks      *[]headMark
	out        []string
	p          paragraph
	fence      string
	indexOnly  bool
	indexRows  int
	indexEnd   int
	indexBlank bool
}

func (f *blockFormatter) position() int {
	if f.indexOnly {
		return f.indexRows
	}
	return len(f.out)
}

// The reader needs mark ordering and the last printable line, not wrapped rows.
func (f *blockFormatter) indexLine(nonempty bool) {
	f.indexRows++
	f.indexBlank = !nonempty
	if nonempty {
		f.indexEnd = f.indexRows
	}
}

// slugify converts a heading title into a GitHub-compatible anchor slug.
func slugify(title string, seen map[string]int) string {
	title = strings.ToLower(title)
	title = slugPunctRE.ReplaceAllString(title, "")
	title = slugSpaceRE.ReplaceAllString(strings.TrimSpace(title), "-")

	count := seen[title]
	seen[title] = count + 1
	if count > 0 {
		return fmt.Sprintf("%s-%d", title, count)
	}
	return title
}

func (f *blockFormatter) flush() {
	if f.p.active {
		if f.indexOnly {
			plain, nonempty := true, false
			for _, line := range f.p.text {
				if strings.ContainsAny(line, "[]*_~`") {
					plain = false
					break
				}
				nonempty = nonempty || strings.TrimSpace(line) != ""
			}
			if !plain {
				nonempty = strings.TrimSpace(inlineText(strings.Join(f.p.text, " "))) != ""
			}
			if nonempty {
				f.indexLine(true)
			}
		} else {
			text := inlineText(strings.Join(f.p.text, " "))
			f.out = append(f.out, wrap(text, f.width, f.p.first, f.p.rest)...)
		}
	}
	f.p = paragraph{}
}

func (f *blockFormatter) blank() {
	if f.indexOnly {
		if f.indexRows > 0 && !f.indexBlank {
			f.indexLine(false)
		}
		return
	}
	if len(f.out) > 0 && f.out[len(f.out)-1] != "" {
		f.out = append(f.out, "")
	}
}

func (f *blockFormatter) heading(level int, raw string) {
	f.blank()
	text := inlineText(raw)
	if f.marks != nil {
		*f.marks = append(*f.marks, headMark{line: f.position(), level: level, title: text, slug: slugify(text, f.seenSlugs)})
	}
	if f.indexOnly {
		hasText := len(strings.Fields(text)) > 0
		if hasText {
			f.indexLine(true)
		}
		if level <= 2 {
			f.indexLine(hasText)
		}
		f.blank()
		return
	}
	if level == 1 {
		text = strings.ToUpper(text)
	}
	wrapped := wrap(text, f.width, "", "")
	f.out = append(f.out, wrapped...)
	if level <= 2 {
		longest := 0
		for _, w := range wrapped {
			longest = max(longest, runewidth.StringWidth(w))
		}
		underline := "="
		if level == 2 {
			underline = "-"
		}
		f.out = append(f.out, strings.Repeat(underline, longest))
	}
	f.blank()
}

func (f *blockFormatter) processLine(raw string) {
	line := strings.TrimRight(strings.ReplaceAll(raw, "\t", "    "), " \r")
	if f.fence != "" {
		if strings.HasPrefix(strings.TrimSpace(line), f.fence) {
			f.fence = ""
			f.blank()
			return
		}
		if f.indexOnly {
			f.indexLine(true)
		} else {
			f.out = append(f.out, hardWrap("    "+line, f.width)...)
		}
		return
	}
	trimmed := strings.TrimSpace(line)
	if marker, ok := fenceOpener(line); ok {
		f.flush()
		f.blank()
		f.fence = marker
		return
	}

	if f.marks != nil && strings.Contains(line, "<") {
		for _, match := range anchorRE.FindAllStringSubmatch(line, -1) {
			*f.marks = append(*f.marks, headMark{line: f.position(), anchor: true, slug: match[1]})
		}
	}

	switch {
	case trimmed == "":
		f.flush()
		f.blank()
	case strings.HasPrefix(trimmed, "<!--") && commentRE.MatchString(trimmed):
	case f.p.active && !f.p.quote && f.p.first == "" && (trimmed[0] == '=' || trimmed[0] == '-') && setextRE.MatchString(trimmed):
		level := 1
		if trimmed[0] == '-' {
			level = 2
		}
		title := strings.Join(f.p.text, " ")
		f.p = paragraph{}
		f.heading(level, title)
	case trimmed[0] == '#' && headingRE.MatchString(trimmed):
		f.flush()
		match := headingRE.FindStringSubmatch(trimmed)
		f.heading(len(match[1]), match[2])
	case (trimmed[0] == '*' || trimmed[0] == '-' || trimmed[0] == '_') && ruleRE.MatchString(trimmed):
		f.flush()
		f.blank()
		if f.indexOnly {
			f.indexLine(true)
		} else {
			f.out = append(f.out, strings.Repeat("-", f.width))
		}
		f.blank()
	case strings.HasPrefix(trimmed, "|"):
		f.flush()
		if f.indexOnly {
			f.indexLine(true)
		} else {
			f.out = append(f.out, hardWrap(line, f.width)...)
		}
	case (trimmed[0] == '-' || trimmed[0] == '*' || trimmed[0] == '+' ||
		trimmed[0] >= '0' && trimmed[0] <= '9') && listItemRE.MatchString(line):
		f.flush()
		match := listItemRE.FindStringSubmatch(line)
		prefix := match[1] + match[2] + " "
		f.p = paragraph{active: true, first: prefix, rest: strings.Repeat(" ", runewidth.StringWidth(prefix)), text: []string{match[3]}}
	case trimmed[0] == '>' && quoteRE.MatchString(trimmed):
		text := quoteRE.FindStringSubmatch(trimmed)[1]
		if !f.p.active || !f.p.quote {
			f.flush()
			f.p = paragraph{active: true, quote: true, first: "> ", rest: "> "}
		}
		f.p.text = append(f.p.text, text)
	default:
		if !f.p.active {
			f.p = paragraph{active: true}
		}
		f.p.text = append(f.p.text, trimmed)
	}
}

// formatBlocks converts Markdown lines to plain text wrapped to width.
func formatBlocks(lines []string, width int, marks *[]headMark, seenSlugs map[string]int) []string {
	f := blockFormatter{width: width, marks: marks, seenSlugs: seenSlugs}
	for _, line := range lines {
		f.processLine(line)
	}
	f.flush()
	return f.out
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
	var line strings.Builder
	lineWidth := 0
	room := func() int { return max(width-runewidth.StringWidth(prefix), 1) }
	emit := func() {
		lines = append(lines, prefix+line.String())
		prefix = rest
		line.Reset()
		lineWidth = 0
	}
	for _, word := range words {
		wWidth := runewidth.StringWidth(word)
		for wWidth > room() {
			if line.Len() > 0 {
				emit()
				continue
			}
			head := runewidth.Truncate(word, room(), "")
			if head == "" {
				head = string([]rune(word)[:1])
			}
			line.WriteString(head)
			word = strings.TrimPrefix(word, head)
			emit()
			wWidth = runewidth.StringWidth(word)
		}
		switch {
		case word == "":
		case line.Len() == 0:
			line.WriteString(word)
			lineWidth = wWidth
		case lineWidth+1+wWidth <= room():
			line.WriteString(" ")
			line.WriteString(word)
			lineWidth += 1 + wWidth
		default:
			emit()
			line.WriteString(word)
			lineWidth = wWidth
		}
	}
	if line.Len() > 0 {
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
