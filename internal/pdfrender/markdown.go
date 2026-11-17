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
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"

	"example.com/parchment/internal/docinterop"
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
	runs  []markdownRun
	width int
	image bool
}

type markdownPrinter struct {
	source []byte
	width  int
	assets map[string]imageAsset
	refs   *[]imageRef
	lines  []string
}

func markdownFormatter(assets map[string]imageAsset, refs *[]imageRef) document.Formatter {
	return func(lines []string, width int) []string {
		body := strings.Join(lines, "\n")
		source := []byte(body)
		printer := markdownPrinter{
			source: source,
			width:  max(width, 1),
			assets: assets,
			refs:   refs,
		}
		root := docinterop.ParseMarkdown(body)
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
	case *ast.Heading:
		p.blank()
		style := styleBold | n.Level<<headingShift
		p.paragraph(n, firstPrefix, restPrefix, style)
		p.blank()
	case *ast.List:
		p.list(n, 0, firstPrefix, restPrefix)
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
			_, height := blockImageSize(asset, maxBlockImageMM)
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
	for i, line := range lines {
		p.writeParts([]markdownPart{{text: line, style: styleCode}}, firstPrefix, restPrefix)
		if i < len(lines)-1 {
			p.lines = append(p.lines, "")
		}
	}
	p.blank()
}

func (p *markdownPrinter) writeText(text, firstPrefix, restPrefix string, style int) {
	p.writeParts([]markdownPart{{text: text, style: style}}, firstPrefix, restPrefix)
	p.blank()
}

func (p *markdownPrinter) writeParts(parts []markdownPart, firstPrefix, restPrefix string) {
	var words []markdownWord
	var word markdownWord
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
			hardBreaks[len(words)] = true
			continue
		}
		if part.image {
			flushWord()
			ref := (*p.refs)[part.imageRef]
			if ref.name != "" {
				word = markdownWord{
					runs:  []markdownRun{{text: encodeImageWithSpace(part.imageRef), style: part.style}},
					width: 10,
					image: true,
				}
			} else {
				word = markdownWord{
					runs:  []markdownRun{{text: encodeImage(part.imageRef) + part.text, style: part.style}},
					width: runewidth.StringWidth(part.text),
					image: true,
				}
			}
			flushWord()
			continue
		}
		for _, r := range part.text {
			if unicode.IsSpace(r) {
				flushWord()
				continue
			}
			text := string(r)
			if strings.ContainsRune(text, rune(markByte)) {
				text = " "
			}
			word.runs = appendMarkdownRun(word.runs, text, part.style)
			word.width += runewidth.RuneWidth(r)
		}
	}
	flushWord()

	first := true
	var line []markdownRun
	lineWidth := 0
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
		lineWidth = runewidth.StringWidth(prefix)
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
		if next.width > p.width-lineWidth {
			if hasWord {
				flushLine()
			}
			if next.width > p.width-lineWidth && !next.image {
				pieces := splitMarkdownWord(next, max(p.width-lineWidth, 1))
				for _, piece := range pieces {
					if hasWord && lineWidth+1+piece.width > p.width {
						flushLine()
					}
					if hasWord {
						line = appendMarkdownRun(line, " ", 0)
						lineWidth++
					}
					line = append(line, piece.runs...)
					lineWidth += piece.width
					hasWord = true
				}
				continue
			}
		}
		if hasWord {
			line = appendMarkdownRun(line, " ", 0)
			lineWidth++
		}
		line = append(line, next.runs...)
		lineWidth += next.width
		hasWord = true
	}
	if len(line) > 0 && (hasWord || firstPrefix != "" || restPrefix != "") {
		p.lines = append(p.lines, encodeRuns(line))
	}
}

func splitMarkdownWord(word markdownWord, width int) []markdownWord {
	var result []markdownWord
	part := markdownWord{}
	for _, run := range word.runs {
		for _, r := range run.text {
			runeText := string(r)
			runeWidth := runewidth.RuneWidth(r)
			if part.width+runeWidth > width && part.width > 0 {
				result = append(result, part)
				part = markdownWord{}
			}
			part.runs = appendMarkdownRun(part.runs, runeText, run.style)
			part.width += runeWidth
		}
	}
	if len(part.runs) > 0 {
		result = append(result, part)
	}
	if len(result) == 0 {
		return []markdownWord{word}
	}
	return result
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

func renderRichLine(pdf *fpdf.Fpdf, line string, left, baseline float64, font string, refs []imageRef, assets map[string]imageAsset, pageHeight, bottomMargin float64) ([]Element, error) {
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
					y := baseline - height
					if ref.block {
						width, height = blockImageSize(asset, max(pageHeight-bottomMargin-baseline+lineHeightMM, lineHeightMM))
						x = left
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
		x += pdf.GetStringWidth(text)
	}
	return elements, nil
}

func blockImageSize(asset imageAsset, maxHeight float64) (float64, float64) {
	if asset.width <= 0 || asset.height <= 0 {
		return 0, 0
	}
	scale := min(maxBlockImageMM/float64(asset.width), maxBlockImageMM/float64(asset.height), maxHeight/float64(asset.height))
	if scale > 1 {
		scale = 1
	}
	return float64(asset.width) * scale, float64(asset.height) * scale
}
