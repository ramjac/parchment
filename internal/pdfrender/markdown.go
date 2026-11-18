package pdfrender

import (
	"fmt"
	"html"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-pdf/fpdf"
	"github.com/mattn/go-runewidth"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"

	"example.com/parchment/internal/document"
)

const (
	markByte    = byte(1)
	imageMarker = byte(2)

	styleBold    = 1 << 0
	styleItalic  = 1 << 1
	styleCode    = 1 << 2
	styleLink    = 1 << 3
	styleStrike  = 1 << 4
	headingShift = 5
	headingMask  = 7 << headingShift
)

var htmlTagRE = regexp.MustCompile(`(?s)<[^>]*>`)

type markdownPart struct {
	text      string
	style     int
	hardBreak bool
	image     bool
	imageRef  int
}

type markdownRun struct {
	text  string
	style int
}

type markdownWord struct {
	runs          []markdownRun
	widthMM       float64
	image         bool
	spaceBefore   bool
	preserveSpace bool
}

type markdownPrinter struct {
	source       []byte
	width        int
	widthMM      float64
	font         string
	measure      *fpdf.Fpdf
	spaceWidthMM float64
	assets       map[string]imageAsset
	refs         *[]imageRef
	lines        []string
}

func markdownFormatter(assets map[string]imageAsset, refs *[]imageRef, measure *fpdf.Fpdf, font string, textWidthMM float64) document.Formatter {
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	var references []parser.Reference
	referencesReady := false
	return func(lines []string, width, columns int, fullBody string) []string {
		if !referencesReady {
			context := parser.NewContext()
			md.Parser().Parse(text.NewReader([]byte(fullBody)), parser.WithContext(context))
			references = context.References()
			referencesReady = true
		}
		runBody := strings.Join(lines, "\n")
		source := []byte(runBody)
		context := parser.NewContext()
		for _, reference := range references {
			context.AddReference(reference)
		}
		measure.SetFont(pdfFont(font), "", printerFontSize)
		gutterWidth := measure.GetStringWidth(strings.Repeat(" ", document.ColumnGutter))
		columnWidthMM := (textWidthMM - float64(columns-1)*gutterWidth) / float64(columns)
		printer := markdownPrinter{
			source:       source,
			width:        max(width, 1),
			widthMM:      columnWidthMM,
			font:         font,
			measure:      measure,
			spaceWidthMM: measure.GetStringWidth(" "),
			assets:       assets,
			refs:         refs,
		}
		root := md.Parser().Parse(text.NewReader(source), parser.WithContext(context))
		for node := root.FirstChild(); node != nil; node = node.NextSibling() {
			printer.block(node, "", "")
		}
		return printer.lines
	}
}

func (p *markdownPrinter) block(node ast.Node, firstPrefix, restPrefix string) {
	switch n := node.(type) {
	case *ast.Paragraph:
		p.paragraph(n, firstPrefix, restPrefix, 0)
	case *ast.TextBlock:
		p.paragraph(n, firstPrefix, restPrefix, 0)
	case *ast.Heading:
		p.blank()
		style := styleBold | n.Level<<headingShift
		p.paragraph(n, firstPrefix, restPrefix, style)
		p.blank()
	case *ast.List:
		p.list(n, 0, firstPrefix, restPrefix)
	case *east.Table:
		p.table(n, firstPrefix, restPrefix)
	case *ast.Blockquote:
		prefix := firstPrefix + "> "
		rest := restPrefix + "> "
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			p.block(child, prefix, rest)
		}
	case *ast.FencedCodeBlock:
		p.codeBlock(n.Text(p.source), firstPrefix, restPrefix)
	case *ast.CodeBlock:
		p.codeBlock(n.Text(p.source), firstPrefix, restPrefix)
	case *ast.ThematicBreak:
		p.writeText(strings.Repeat("-", min(p.width, 40)), firstPrefix, restPrefix, 0)
	case *ast.HTMLBlock:
		raw := html.UnescapeString(htmlTagRE.ReplaceAllString(string(n.Text(p.source)), ""))
		if text := strings.TrimSpace(raw); text != "" {
			p.writeText(text, firstPrefix, restPrefix, 0)
		}
	default:
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			p.block(child, firstPrefix, restPrefix)
		}
	}
}

func (p *markdownPrinter) table(table *east.Table, firstPrefix, restPrefix string) {
	p.blank()
	for row := table.FirstChild(); row != nil; row = row.NextSibling() {
		var parts []markdownPart
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			if len(parts) > 0 {
				parts = append(parts, markdownPart{text: " | "})
			}
			parts = append(parts, p.inline(cell, 0)...)
		}
		p.writeParts(parts, firstPrefix, restPrefix)
	}
	p.blank()
}

func (p *markdownPrinter) list(list *ast.List, depth int, baseFirst, baseRest string) {
	index := list.Start
	if index < 1 {
		index = 1
	}
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "- "
		if list.IsOrdered() {
			marker = fmt.Sprintf("%d. ", index)
			index++
		}
		indent := strings.Repeat("  ", depth)
		first := baseFirst + indent + marker
		rest := baseRest + strings.Repeat(" ", runewidth.StringWidth(indent+marker))
		firstParagraph := true
		for child := item.FirstChild(); child != nil; child = child.NextSibling() {
			switch child.(type) {
			case *ast.List:
				p.blank()
				p.list(child.(*ast.List), depth+1, baseRest, baseRest)
			default:
				prefix, continuation := rest, rest
				if firstParagraph {
					prefix, continuation = first, rest
					firstParagraph = false
				}
				p.block(child, prefix, continuation)
			}
		}
	}
}

func (p *markdownPrinter) paragraph(node ast.Node, firstPrefix, restPrefix string, baseStyle int) {
	parts := p.inline(node, baseStyle)
	if len(parts) == 1 && parts[0].image {
		refIndex := parts[0].imageRef
		if refIndex >= 0 && refIndex < len(*p.refs) && (*p.refs)[refIndex].name != "" {
			(*p.refs)[refIndex].block = true
			p.lines = append(p.lines, encodeImage(refIndex))
			asset := p.assets[(*p.refs)[refIndex].name]
			_, height := blockImageSize(asset, p.widthMM, maxBlockImageMM)
			for i := 0; i < int((height+lineHeightMM-1)/lineHeightMM)+1; i++ {
				p.lines = append(p.lines, "")
			}
			return
		}
	}
	p.writeParts(parts, firstPrefix, restPrefix)
	p.blank()
}

func (p *markdownPrinter) inline(node ast.Node, style int) []markdownPart {
	var parts []markdownPart
	var visit func(ast.Node, int)
	visit = func(parent ast.Node, current int) {
		for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
			switch n := child.(type) {
			case *ast.Text:
				parts = append(parts, markdownPart{text: string(n.Segment.Value(p.source)), style: current})
				if n.HardLineBreak() {
					parts = append(parts, markdownPart{hardBreak: true})
				} else if n.SoftLineBreak() {
					parts = append(parts, markdownPart{text: " ", style: current})
				}
			case *ast.String:
				value := pdfCompatibleEntityText(string(n.Value))
				stringStyle := current
				if n.IsCode() {
					stringStyle |= styleCode
				}
				parts = append(parts, markdownPart{text: value, style: stringStyle})
			case *ast.Emphasis:
				next := current
				if n.Level == 2 {
					next |= styleBold
				} else {
					next |= styleItalic
				}
				visit(n, next)
			case *east.Strikethrough:
				visit(n, current|styleStrike)
			case *ast.CodeSpan:
				visit(n, current|styleCode)
			case *ast.Link:
				visit(n, current|styleLink)
			case *ast.AutoLink:
				parts = append(parts, markdownPart{text: string(n.Label(p.source)), style: current | styleLink})
			case *ast.Image:
				destination := string(n.Destination)
				name := destination
				if _, ok := p.assets[name]; !ok {
					name = path.Base(destination)
				}
				if _, ok := p.assets[name]; !ok {
					name = ""
				}
				*p.refs = append(*p.refs, imageRef{name: name})
				index := len(*p.refs) - 1
				parts = append(parts, markdownPart{
					text:     fmt.Sprintf("[Image: %s]", string(n.Text(p.source))),
					style:    current,
					image:    true,
					imageRef: index,
				})
			case *ast.RawHTML:
				continue
			default:
				visit(child, current)
			}
		}
	}
	visit(node, style)
	return parts
}

func (p *markdownPrinter) codeBlock(content []byte, firstPrefix, restPrefix string) {
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	for _, line := range lines {
		p.codeLine(expandCodeTabs(line), firstPrefix, restPrefix)
	}
	p.blank()
}

func expandCodeTabs(line string) string {
	var out strings.Builder
	column := 0
	for _, r := range line {
		if r == '\t' {
			spaces := 4 - column%4
			out.WriteString(strings.Repeat(" ", spaces))
			column += spaces
			continue
		}
		out.WriteRune(r)
		column += max(runewidth.RuneWidth(r), 0)
	}
	return out.String()
}

func (p *markdownPrinter) codeLine(line, firstPrefix, restPrefix string) {
	runes := []rune(line)
	first := true
	for start := 0; start < len(runes); {
		prefix := restPrefix
		if first {
			prefix = firstPrefix
			first = false
		}
		prefixWidth := p.textWidthMM(prefix, 0)
		available := max(p.widthMM-prefixWidth, p.spaceWidthMM)
		end, width := start, 0.0
		for end < len(runes) {
			nextWidth := p.textWidthMM(string(runes[end]), styleCode)
			if end > start && width+nextWidth > available {
				break
			}
			width += nextWidth
			end++
		}
		if end == start {
			end++
		}
		runs := appendMarkdownRun(nil, prefix, 0)
		runs = appendMarkdownRun(runs, string(runes[start:end]), styleCode)
		p.lines = append(p.lines, encodeRuns(runs))
		start = end
	}
	if len(runes) == 0 {
		p.lines = append(p.lines, encodeRuns(appendMarkdownRun(nil, firstPrefix, 0)))
	}
}

func (p *markdownPrinter) writeText(text, firstPrefix, restPrefix string, style int) {
	p.writeParts([]markdownPart{{text: text, style: style}}, firstPrefix, restPrefix)
	p.blank()
}

func (p *markdownPrinter) writeParts(parts []markdownPart, firstPrefix, restPrefix string) {
	var words []markdownWord
	var word markdownWord
	pendingSpace := false
	flushWord := func() {
		if len(word.runs) > 0 {
			words = append(words, word)
			word = markdownWord{}
		}
	}
	hardBreaks := make(map[int]bool)
	for _, part := range parts {
		if part.hardBreak {
			flushWord()
			pendingSpace = false
			hardBreaks[len(words)] = true
			continue
		}
		if part.image {
			flushWord()
			ref := (*p.refs)[part.imageRef]
			if ref.name != "" {
				width, _ := inlineImageSize(p.assets[ref.name])
				word = markdownWord{
					runs:        []markdownRun{{text: encodeImageWithSpace(part.imageRef), style: part.style}},
					widthMM:     width,
					image:       true,
					spaceBefore: pendingSpace,
				}
			} else {
				word = markdownWord{
					runs:        []markdownRun{{text: encodeImage(part.imageRef) + part.text, style: part.style}},
					widthMM:     p.textWidthMM(part.text, part.style),
					image:       true,
					spaceBefore: pendingSpace,
				}
			}
			pendingSpace = false
			flushWord()
			continue
		}
		partText := part.text
		if part.style&styleCode != 0 && strings.ContainsRune(partText, '\t') {
			partText = expandCodeTabs(partText)
		}
		for _, r := range partText {
			if part.style&styleCode != 0 && (r == '\n' || r == '\r') {
				r = ' '
			}
			if unicode.IsSpace(r) {
				flushWord()
				if part.style&styleCode != 0 {
					words = append(words, markdownWord{
						runs:          []markdownRun{{text: string(r), style: part.style}},
						widthMM:       p.textWidthMM(string(r), part.style),
						spaceBefore:   pendingSpace,
						preserveSpace: true,
					})
					pendingSpace = false
				} else {
					pendingSpace = true
				}
				continue
			}
			text := string(r)
			if strings.ContainsRune(text, rune(markByte)) {
				text = " "
			}
			if len(word.runs) == 0 {
				word.spaceBefore = pendingSpace
				pendingSpace = false
			}
			word.runs = appendMarkdownRun(word.runs, text, part.style)
			word.widthMM += p.textWidthMM(text, part.style)
		}
	}
	flushWord()

	first := true
	var line []markdownRun
	lineWidthMM := 0.0
	hasWord := false
	startLine := func() {
		prefix := restPrefix
		if first {
			prefix = firstPrefix
			first = false
		}
		line = nil
		if prefix != "" {
			line = appendMarkdownRun(line, prefix, 0)
		}
		lineWidthMM = p.textWidthMM(prefix, 0)
		hasWord = false
	}
	flushLine := func() {
		p.lines = append(p.lines, encodeRuns(line))
		startLine()
	}
	startLine()
	for i, next := range words {
		if hardBreaks[i] && hasWord {
			flushLine()
		}
		separatorWidth := 0.0
		if next.spaceBefore && hasWord {
			separatorWidth = p.spaceWidthMM
		}
		if next.preserveSpace {
			spaceWidth := 0.0
			if next.spaceBefore && hasWord {
				spaceWidth = p.spaceWidthMM
			}
			if lineWidthMM+spaceWidth+next.widthMM > p.widthMM && hasWord {
				flushLine()
				spaceWidth = 0
			}
			if spaceWidth > 0 {
				line = appendMarkdownRun(line, " ", 0)
				lineWidthMM += spaceWidth
			}
			line = append(line, next.runs...)
			lineWidthMM += next.widthMM
			continue
		}
		if next.widthMM+separatorWidth > p.widthMM-lineWidthMM {
			if hasWord {
				flushLine()
			}
			if next.widthMM > p.widthMM-lineWidthMM && !next.image {
				pieces := splitMarkdownWord(next, max(p.widthMM-lineWidthMM, p.spaceWidthMM), p)
				for _, piece := range pieces {
					spaceWidth := 0.0
					if piece.spaceBefore && hasWord {
						spaceWidth = p.spaceWidthMM
					}
					if hasWord && lineWidthMM+spaceWidth+piece.widthMM > p.widthMM {
						flushLine()
					}
					if spaceWidth > 0 && hasWord {
						line = appendMarkdownRun(line, " ", 0)
						lineWidthMM += p.spaceWidthMM
					}
					line = append(line, piece.runs...)
					lineWidthMM += piece.widthMM
					hasWord = true
					piece.spaceBefore = false
				}
				continue
			}
		}
		if separatorWidth > 0 && hasWord {
			line = appendMarkdownRun(line, " ", 0)
			lineWidthMM += p.spaceWidthMM
		}
		line = append(line, next.runs...)
		lineWidthMM += next.widthMM
		if next.widthMM > 0 {
			hasWord = true
		}
	}
	if len(line) > 0 && (hasWord || firstPrefix != "" || restPrefix != "") {
		p.lines = append(p.lines, encodeRuns(line))
	}
}

func splitMarkdownWord(word markdownWord, widthMM float64, p *markdownPrinter) []markdownWord {
	var result []markdownWord
	part := markdownWord{}
	for _, run := range word.runs {
		for _, r := range run.text {
			runeText := string(r)
			runeWidth := p.textWidthMM(runeText, run.style)
			if part.widthMM+runeWidth > widthMM && part.widthMM > 0 {
				result = append(result, part)
				part = markdownWord{}
			}
			part.runs = appendMarkdownRun(part.runs, runeText, run.style)
			part.widthMM += runeWidth
		}
	}
	if len(part.runs) > 0 {
		result = append(result, part)
	}
	if len(result) == 0 {
		return []markdownWord{word}
	}
	result[0].spaceBefore = word.spaceBefore
	return result
}

func (p *markdownPrinter) textWidthMM(text string, style int) float64 {
	font, fontStyle, size := richFont(p.font, style)
	p.measure.SetFont(font, fontStyle, size)
	encoded, err := encodeWinAnsi(text)
	if err == nil {
		return p.measure.GetStringWidth(encoded)
	}
	return float64(runewidth.StringWidth(text)) * p.spaceWidthMM
}

func appendMarkdownRun(runs []markdownRun, text string, style int) []markdownRun {
	if text == "" {
		return runs
	}
	if len(runs) > 0 && runs[len(runs)-1].style == style {
		runs[len(runs)-1].text += text
		return runs
	}
	return append(runs, markdownRun{text: text, style: style})
}

func pdfCompatibleEntityText(value string) string {
	var out strings.Builder
	for _, r := range value {
		if winAnsiRune(r) {
			out.WriteRune(r)
		} else {
			fmt.Fprintf(&out, "&#x%X;", r)
		}
	}
	return out.String()
}

func (p *markdownPrinter) blank() {
	if len(p.lines) > 0 && p.lines[len(p.lines)-1] != "" {
		p.lines = append(p.lines, "")
	}
}

func encodeRuns(runs []markdownRun) string {
	var out strings.Builder
	style := -1
	for _, run := range runs {
		if run.style != style {
			out.WriteByte(markByte)
			out.WriteByte(byte(0x10 | run.style&0x0f))
			out.WriteByte(byte(0x10 | (run.style>>4)&0x0f))
			style = run.style
		}
		out.WriteString(run.text)
	}
	return out.String()
}

func encodeImage(index int) string {
	var out strings.Builder
	out.WriteByte(markByte)
	out.WriteByte(imageMarker)
	for _, digit := range strconv.FormatInt(int64(index), 16) {
		value := int(digit - '0')
		if digit >= 'a' {
			value = int(digit-'a') + 10
		}
		out.WriteByte(byte(0x10 + value))
	}
	out.WriteByte(0x03)
	return out.String()
}

func encodeImageWithSpace(index int) string {
	return encodeImage(index)
}

func renderRichLine(pdf *fpdf.Fpdf, line string, left, baseline, maxWidthMM float64, font string, refs []imageRef, assets map[string]imageAsset, pageHeight, bottomMargin float64) ([]Element, error) {
	var elements []Element
	x := left
	style := 0
	for i := 0; i < len(line); {
		if line[i] == markByte && i+1 < len(line) {
			if line[i+1] == imageMarker {
				j := i + 2
				index := 0
				digits := 0
				for j < len(line) && line[j] != 0x03 {
					if line[j] < 0x10 || line[j] > 0x1f {
						return nil, fmt.Errorf("invalid PDF image reference")
					}
					index = index*16 + int(line[j]-0x10)
					digits++
					j++
				}
				if j >= len(line) || digits == 0 || index >= len(refs) {
					return nil, fmt.Errorf("PDF image reference is missing")
				}
				ref := refs[index]
				asset, ok := assets[ref.name]
				if ref.name != "" && !ok {
					return nil, fmt.Errorf("PDF image %q is not available", ref.name)
				}
				if ref.name != "" {
					width, height := inlineImageSize(asset)
					if available := max(maxWidthMM-(x-left), 0); width > available {
						scale := available / width
						width, height = width*scale, height*scale
					}
					y := baseline - height
					if ref.block {
						width, height = blockImageSize(asset, maxWidthMM, max(pageHeight-bottomMargin-baseline+lineHeightMM, lineHeightMM))
						y = baseline - lineHeightMM
					}
					elements = append(elements, Element{
						ImageName: ref.name, X: x, Y: y, WidthMM: width, HeightMM: height,
					})
					if !ref.block {
						x += width
					}
				}
				i = j + 1
				continue
			}
			if i+2 >= len(line) || line[i+1] < 0x10 || line[i+1] > 0x1f || line[i+2] < 0x10 || line[i+2] > 0x1f {
				return nil, fmt.Errorf("invalid PDF formatting marker")
			}
			style = int(line[i+1]-0x10) | int(line[i+2]-0x10)<<4
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, fmt.Errorf("invalid UTF-8 in formatted Markdown")
		}
		if r < 0x20 || r == 0x7f {
			return nil, fmt.Errorf("control character in formatted Markdown")
		}
		start := i
		i += size
		for i < len(line) && line[i] != markByte {
			r, size = utf8.DecodeRuneInString(line[i:])
			if r == utf8.RuneError && size == 1 || r < 0x20 || r == 0x7f {
				return nil, fmt.Errorf("control character in formatted Markdown")
			}
			i += size
		}
		text := line[start:i]
		if err := validateWinAnsi(text); err != nil {
			return nil, err
		}
		code := style
		fontName, fontStyle, sizePt := richFont(font, code)
		encoded, err := encodeWinAnsi(text)
		if err != nil {
			return nil, err
		}
		pdf.SetFont(fontName, fontStyle, sizePt)
		if code&styleLink != 0 {
			pdf.SetTextColor(0, 76, 153)
		} else {
			pdf.SetTextColor(0, 0, 0)
		}
		elements = append(elements, Element{
			Text: text, FontStyle: fontStyle, FontFamily: fontName, FontSize: sizePt,
			Color: [3]uint8{0, 76, 153}, HasColor: code&styleLink != 0,
			Strikethrough: code&styleStrike != 0, X: x, Y: baseline,
		})
		x += pdf.GetStringWidth(encoded)
	}
	return elements, nil
}

func richFont(font string, code int) (string, string, float64) {
	fontName := pdfFont(font)
	if code&styleCode != 0 {
		fontName = "Courier"
	}
	fontStyle := ""
	if code&styleBold != 0 {
		fontStyle += "B"
	}
	if code&styleItalic != 0 {
		fontStyle += "I"
	}
	if code&styleLink != 0 {
		fontStyle += "U"
	}
	sizePt := printerFontSize
	switch (code & headingMask) >> headingShift {
	case 1:
		sizePt = 16
	case 2:
		sizePt = 14
	case 3:
		sizePt = 13
	case 5:
		sizePt = 11
	case 6:
		sizePt = 10
	}
	if code&styleCode != 0 {
		sizePt = 10
	}
	return fontName, fontStyle, sizePt
}

func blockImageSize(asset imageAsset, maxWidth, maxHeight float64) (float64, float64) {
	if asset.width <= 0 || asset.height <= 0 {
		return 0, 0
	}
	scale := min(maxWidth/float64(asset.width), maxBlockImageMM/float64(asset.height), maxHeight/float64(asset.height))
	if scale > 1 {
		scale = 1
	}
	return float64(asset.width) * scale, float64(asset.height) * scale
}
