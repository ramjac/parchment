package pdfrender

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strings"
	"testing"

	"example.com/parchment/internal/document"
)

func TestMarkdownPrinterRendersTightListsAndTables(t *testing.T) {
	lines := formattedTextLines(t, "- first item\n- second item\n\n| Name | Value |\n| --- | --- |\n| alpha | one |\n| beta | two |")
	text := strings.Join(lines, "\n")
	for _, want := range []string{"- first item", "- second item", "Name | Value", "alpha | one", "beta | two"} {
		if !strings.Contains(text, want) {
			t.Errorf("formatted Markdown is missing %q:\n%s", want, text)
		}
	}
}

func TestMarkdownPrinterPreservesCodeWhitespace(t *testing.T) {
	lines := formattedTextLines(t, "```python\nif ready:\n    nested = 1\n\treturn\n```\n\nUse `x  =  1` inline.")
	text := strings.Join(lines, "\n")
	for _, want := range []string{"if ready:", "    nested = 1", "    return", "x  =  1"} {
		if !strings.Contains(text, want) {
			t.Errorf("formatted code is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "if ready:\n\n") {
		t.Fatalf("code block contains an extra blank line between source lines:\n%s", text)
	}
}

func TestRenderMultilineCodeSpan(t *testing.T) {
	for _, lineEnding := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("%q", lineEnding), func(t *testing.T) {
			body := "Use `inline" + lineEnding + "code` here."
			lines := formattedTextLines(t, body)
			if text := strings.Join(lines, "\n"); !strings.Contains(text, "inline code") {
				t.Fatalf("formatted code span = %q, want normalized space", text)
			}
			var out bytes.Buffer
			if err := Render(&out, document.Document{Body: body, Layout: document.DefaultLayout()}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMarkdownWrapUsesSelectedFontMetrics(t *testing.T) {
	layout := document.DefaultLayout()
	layout.Font = "courier"
	lines := formattedTextLinesWithLayout(t, "# "+strings.Repeat("W", 60), layout)
	var headingLines []string
	for _, line := range lines {
		if strings.Contains(line, strings.Repeat("W", 10)) {
			headingLines = append(headingLines, line)
		}
	}
	if len(headingLines) < 2 {
		t.Fatalf("wide heading was not wrapped to the page's physical width: %q", headingLines)
	}
}

func TestFormattedPaginationRetainsColumnCells(t *testing.T) {
	layout := document.DefaultLayout()
	layout.Columns = 2
	var paragraphs []string
	for i := 0; i < 120; i++ {
		paragraphs = append(paragraphs, fmt.Sprintf("paragraph %03d", i))
	}
	d := document.Document{Body: strings.Join(paragraphs, "\n\n"), Layout: layout}
	width, height, err := pageSizeMM(layout)
	if err != nil {
		t.Fatal(err)
	}
	measure := newMeasurePDF(width, height, pdfFont(layout.Font))
	textWidth := width - float64(layout.Margins.Left+layout.Margins.Right)
	var refs []imageRef
	pages, err := document.PaginateWithFormatter(d, markdownFormatter(nil, &refs, measure, layout.Font, textWidth))
	if err != nil {
		t.Fatal(err)
	}
	foundColumns := false
	for _, page := range pages {
		for _, cells := range page.Cells {
			if len(cells) == 2 && cells[0] != "" && cells[1] != "" {
				foundColumns = true
				break
			}
		}
	}
	if !foundColumns {
		t.Fatal("paginated PDF lines lost their per-column cells")
	}
	if err := Render(&bytes.Buffer{}, d); err != nil {
		t.Fatal(err)
	}
}

func TestMarkdownWrapCountsSeparatorSpace(t *testing.T) {
	measure := newMeasurePDF(215.9, 279.4, "Courier")
	measure.SetFont("Courier", "", printerFontSize)
	width := measure.GetStringWidth(strings.Repeat("M", 65))
	printer := markdownPrinter{
		width:        65,
		widthMM:      width,
		font:         "courier",
		measure:      measure,
		spaceWidthMM: measure.GetStringWidth(" "),
	}
	printer.writeParts([]markdownPart{
		{text: strings.Repeat("a", 30)},
		{text: " "},
		{text: strings.Repeat("b", 35)},
	}, "", "")
	if len(printer.lines) != 2 {
		t.Fatalf("line count = %d, want the 66-character content split into two lines: %q", len(printer.lines), printer.lines)
	}
}

func TestBlockImageKeepsColumnOriginAndWidth(t *testing.T) {
	asset := imageAsset{width: 200, height: 100}
	elements, err := renderRichLine(
		newMeasurePDF(215.9, 279.4, "Courier"), encodeImage(0),
		40, 100, 20, "courier", []imageRef{{name: "large", block: true}},
		map[string]imageAsset{"large": asset}, 279.4, 25.4,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 1 || elements[0].X != 40 || elements[0].WidthMM > 20 {
		t.Fatalf("block image geometry = %#v; want origin 40mm and width at most 20mm", elements)
	}
}

func TestEncodeWinAnsiAndWriteCoreFontText(t *testing.T) {
	const text = "café € “quoted”"
	encoded, err := encodeWinAnsi(text)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("caf\xe9 \x80 \x93quoted\x94")
	if !bytes.Equal([]byte(encoded), want) {
		t.Fatalf("encoded text = % x, want % x", []byte(encoded), want)
	}

	var out bytes.Buffer
	if err := Write(&out, File{
		Pages: []PDFPage{{WidthMM: 215.9, HeightMM: 279.4, Elements: []Element{{
			Text: text, X: 10, Y: 20,
		}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if !pdfStreamContains(out.Bytes(), want) {
		t.Fatal("PDF content stream does not contain Windows-1252 text bytes")
	}
}

func formattedTextLines(t *testing.T, body string) []string {
	t.Helper()
	return formattedTextLinesWithLayout(t, body, document.DefaultLayout())
}

func formattedTextLinesWithLayout(t *testing.T, body string, layout document.Layout) []string {
	t.Helper()
	width, height, err := pageSizeMM(layout)
	if err != nil {
		t.Fatal(err)
	}
	textWidth := width - float64(layout.Margins.Left+layout.Margins.Right)
	measure := newMeasurePDF(width, height, pdfFont(layout.Font))
	var refs []imageRef
	pages, err := document.PaginateWithFormatter(document.Document{
		Body: body, Layout: layout,
	}, markdownFormatter(nil, &refs, measure, layout.Font, textWidth))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, page := range pages {
		for _, cells := range page.Cells {
			for _, cell := range cells {
				lines = append(lines, plainRichText(cell))
			}
		}
	}
	return lines
}

func plainRichText(line string) string {
	var out strings.Builder
	for i := 0; i < len(line); {
		if line[i] != markByte || i+1 >= len(line) {
			out.WriteByte(line[i])
			i++
			continue
		}
		if line[i+1] == imageMarker {
			i += 2
			for i < len(line) && line[i] != 0x03 {
				i++
			}
			if i < len(line) {
				i++
			}
			continue
		}
		i += 3
	}
	return out.String()
}

func pdfStreamContains(pdf, needle []byte) bool {
	for offset := 0; offset < len(pdf); {
		streamAt := bytes.Index(pdf[offset:], []byte("stream"))
		if streamAt < 0 {
			return false
		}
		streamAt += offset
		headerStart := bytes.LastIndex(pdf[:streamAt], []byte("<<"))
		if headerStart < 0 {
			return false
		}
		header := pdf[headerStart:streamAt]
		lengthStart := bytes.Index(header, []byte("/Length "))
		if lengthStart < 0 || !bytes.Contains(header, []byte("/FlateDecode")) {
			offset = streamAt + len("stream")
			continue
		}
		var length int
		if _, err := fmt.Sscanf(string(header[lengthStart:]), "/Length %d", &length); err != nil {
			return false
		}
		start := streamAt + len("stream")
		if start < len(pdf) && pdf[start] == '\r' {
			start++
		}
		if start < len(pdf) && pdf[start] == '\n' {
			start++
		}
		if length < 0 || start+length > len(pdf) {
			return false
		}
		reader, err := zlib.NewReader(bytes.NewReader(pdf[start : start+length]))
		if err == nil {
			content, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr == nil && closeErr == nil && bytes.Contains(content, needle) {
				return true
			}
		}
		offset = start + length
	}
	return false
}
