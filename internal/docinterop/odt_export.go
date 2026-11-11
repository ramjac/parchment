package docinterop

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"example.com/parchment/internal/document"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

// OdtExporter exports a Parchment Document to an ODF (.odt) archive.
type OdtExporter struct{}

// NewOdtExporter returns a new OdtExporter instance.
func NewOdtExporter() *OdtExporter {
	return &OdtExporter{}
}

type odtImageRef struct {
	zipPath   string
	mediaType string
	widthMM   float64
	heightMM  float64
	alt       string
}

// Export writes the document as a valid OpenDocument Text (.odt) archive.
func (e *OdtExporter) Export(w io.Writer, doc document.Document) ([]error, error) {
	var warnings []error

	zw := zip.NewWriter(w)
	defer zw.Close()

	// 1. mimetype MUST be stored first uncompressed
	mimeHeader := &zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store,
	}
	mimeEntry, err := zw.CreateHeader(mimeHeader)
	if err != nil {
		return warnings, fmt.Errorf("create mimetype entry: %w", err)
	}
	if _, err := io.WriteString(mimeEntry, "application/vnd.oasis.opendocument.text"); err != nil {
		return warnings, fmt.Errorf("write mimetype: %w", err)
	}

	// 2. Process images
	imageMap := make(map[string]odtImageRef)
	var imagesList []odtImageRef

	pgW, _, _ := pageSizeDimensions(doc.Layout.PageSize, doc.Layout.Orientation)
	maxTextWMM := pgW - float64(doc.Layout.Margins.Left+doc.Layout.Margins.Right)
	if maxTextWMM <= 20 {
		maxTextWMM = 150
	}

	for i, img := range doc.Images {
		ext := strings.ToLower(filepath.Ext(img.Name))
		mediaType := "image/png"
		if ext == ".jpg" || ext == ".jpeg" {
			mediaType = "image/jpeg"
		} else if ext == ".gif" {
			mediaType = "image/gif"
		}
		if ext == "" {
			ext = ".png"
		}

		targetPath := fmt.Sprintf("Pictures/image%d%s", i+1, ext)

		// Dimensions
		cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data))
		wPx := cfg.Width
		hPx := cfg.Height
		if err != nil || wPx <= 0 || hPx <= 0 {
			wPx = 400
			hPx = 300
		}

		// 96 DPI: 1 px ≈ 0.264583 mm
		wMM := float64(wPx) * 25.4 / 96.0
		hMM := float64(hPx) * 25.4 / 96.0
		if wMM > maxTextWMM && wMM > 0 {
			hMM = hMM * maxTextWMM / wMM
			wMM = maxTextWMM
		}

		ref := odtImageRef{
			zipPath:   targetPath,
			mediaType: mediaType,
			widthMM:   wMM,
			heightMM:  hMM,
			alt:       img.Name,
		}
		imageMap[img.Name] = ref
		imagesList = append(imagesList, ref)

		// Write picture to zip
		picEntry, err := zw.Create(targetPath)
		if err != nil {
			return warnings, fmt.Errorf("create picture %s: %w", targetPath, err)
		}
		if _, err := picEntry.Write(img.Data); err != nil {
			return warnings, fmt.Errorf("write picture %s: %w", targetPath, err)
		}
	}

	// 3. META-INF/manifest.xml
	manifestEntry, err := zw.Create("META-INF/manifest.xml")
	if err != nil {
		return warnings, err
	}
	var manifestXML strings.Builder
	manifestXML.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" manifest:version="1.2">
  <manifest:file-entry manifest:full-path="/" manifest:version="1.2" manifest:media-type="application/vnd.oasis.opendocument.text"/>
  <manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/>
  <manifest:file-entry manifest:full-path="styles.xml" manifest:media-type="text/xml"/>
`)
	for _, img := range imagesList {
		manifestXML.WriteString(fmt.Sprintf("  <manifest:file-entry manifest:full-path=\"%s\" manifest:media-type=\"%s\"/>\n", img.zipPath, img.mediaType))
	}
	manifestXML.WriteString("</manifest:manifest>")
	if _, err := io.WriteString(manifestEntry, manifestXML.String()); err != nil {
		return warnings, err
	}

	// 4. styles.xml (page layout, headers, footers, font)
	if err := e.writeStylesXML(zw, doc, &warnings); err != nil {
		return warnings, err
	}

	// 5. content.xml (Markdown body AST conversion)
	if err := e.writeContentXML(zw, doc, imageMap, &warnings); err != nil {
		return warnings, err
	}

	return warnings, nil
}

func (e *OdtExporter) writeStylesXML(zw *zip.Writer, doc document.Document, warnings *[]error) error {
	entry, err := zw.Create("styles.xml")
	if err != nil {
		return err
	}

	fontName := ExportFontFamily(doc.Layout.Font)
	fontFamilyGeneric := "monospace"
	if strings.Contains(strings.ToLower(fontName), "times") {
		fontFamilyGeneric = "serif"
	} else if strings.Contains(strings.ToLower(fontName), "arial") || strings.Contains(strings.ToLower(fontName), "helvetica") {
		fontFamilyGeneric = "sans-serif"
	}

	pgW, pgH, orient := pageSizeDimensions(doc.Layout.PageSize, doc.Layout.Orientation)
	cols := doc.Layout.Columns
	if cols < 1 {
		cols = 1
	} else if cols > document.MaxColumns {
		cols = document.MaxColumns
		*warnings = append(*warnings, &Warning{Message: "columns clamped to max"})
	}

	hasHeader := doc.Layout.Header != ""
	hasFooter := doc.Layout.Footer != "" || (doc.Layout.PageNumbers != "" && doc.Layout.PageNumbers != document.NumbersNone)

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<office:document-styles xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
                        xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0"
                        xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"
                        xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0"
                        xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0"
                        xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0"
                        xmlns:xlink="http://www.w3.org/1999/xlink"
                        xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0"
                        office:version="1.2">
  <office:font-face-decls>
`)
	b.WriteString(fmt.Sprintf("    <style:font-face style:name=\"%s\" svg:font-family=\"'%s', %s\"/>\n", fontName, fontName, fontFamilyGeneric))
	b.WriteString(`  </office:font-face-decls>
  <office:styles>
    <style:default-style style:family="paragraph">
`)
	b.WriteString(fmt.Sprintf("      <style:text-properties style:font-name=\"%s\" fo:font-size=\"11pt\"/>\n", fontName))
	b.WriteString(`    </style:default-style>
  </office:styles>
  <office:automatic-styles>
    <style:page-layout style:name="Mpm1">
`)
	b.WriteString(fmt.Sprintf("      <style:page-layout-properties fo:page-width=\"%.1fmm\" fo:page-height=\"%.1fmm\" style:print-orientation=\"%s\" fo:margin-top=\"%dmm\" fo:margin-bottom=\"%dmm\" fo:margin-left=\"%dmm\" fo:margin-right=\"%dmm\">\n",
		pgW, pgH, orient, doc.Layout.Margins.Top, doc.Layout.Margins.Bottom, doc.Layout.Margins.Left, doc.Layout.Margins.Right))
	if cols > 1 {
		b.WriteString(fmt.Sprintf("        <style:columns fo:column-count=\"%d\" fo:column-gap=\"5mm\"/>\n", cols))
	}
	if hasHeader {
		b.WriteString("        <style:header-style><style:header-footer-properties fo:min-height=\"5mm\" fo:margin-bottom=\"3mm\"/></style:header-style>\n")
	}
	if hasFooter {
		b.WriteString("        <style:footer-style><style:header-footer-properties fo:min-height=\"5mm\" fo:margin-top=\"3mm\"/></style:footer-style>\n")
	}
	b.WriteString(`      </style:page-layout-properties>
    </style:page-layout>
  </office:automatic-styles>
  <office:master-styles>
    <style:master-page style:name="Standard" style:page-layout-name="Mpm1">
`)
	if hasHeader {
		b.WriteString("      <style:header>\n")
		b.WriteString(odtRunningParagraph(doc.Layout.Header, doc.Title))
		b.WriteString("      </style:header>\n")
	}
	if hasFooter {
		footerText := doc.Layout.Footer
		if doc.Layout.PageNumbers != "" && doc.Layout.PageNumbers != document.NumbersNone {
			left, center, right := splitRunningParts(footerText)
			switch doc.Layout.PageNumbers {
			case document.NumbersLeft:
				if left == "" {
					left = "{page}"
				}
			case document.NumbersCenter:
				if center == "" {
					center = "{page}"
				}
			case document.NumbersRight:
				if right == "" {
					right = "{page}"
				}
			}
			footerText = fmt.Sprintf("%s | %s | %s", left, center, right)
		}
		b.WriteString("      <style:footer>\n")
		b.WriteString(odtRunningParagraph(footerText, doc.Title))
		b.WriteString("      </style:footer>\n")
	}
	b.WriteString(`    </style:master-page>
  </office:master-styles>
</office:document-styles>`)

	_, err = io.WriteString(entry, b.String())
	return err
}

func (e *OdtExporter) writeContentXML(zw *zip.Writer, doc document.Document, imageMap map[string]odtImageRef, warnings *[]error) error {
	entry, err := zw.Create("content.xml")
	if err != nil {
		return err
	}

	source := []byte(doc.Body)
	root := ParseMarkdown(doc.Body)

	var bodyXML strings.Builder

	// Helper for inlines
	var writeInlines func(b *strings.Builder, n ast.Node, bold, italic, strike, code bool)
	writeInlines = func(b *strings.Builder, n ast.Node, bold, italic, strike, code bool) {
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			switch c := child.(type) {
			case *ast.Text:
				val := c.Segment.Value(source)
				spanStyle := ""
				if bold && italic {
					spanStyle = "BoldItalic"
				} else if bold {
					spanStyle = "Bold"
				} else if italic {
					spanStyle = "Italic"
				} else if strike {
					spanStyle = "Strike"
				} else if code {
					spanStyle = "Code"
				}

				if spanStyle != "" {
					b.WriteString(fmt.Sprintf("<text:span text:style-name=\"%s\">", spanStyle))
				}
				b.WriteString(xmlEscape(string(val)))
				if spanStyle != "" {
					b.WriteString("</text:span>")
				}
				if c.SoftLineBreak() || c.HardLineBreak() {
					b.WriteString("<text:line-break/>")
				}

			case *ast.Emphasis:
				newB := bold
				newI := italic
				if c.Level == 2 {
					newB = true
				} else {
					newI = true
				}
				writeInlines(b, c, newB, newI, strike, code)

			case *east.Strikethrough:
				writeInlines(b, c, bold, italic, true, code)

			case *ast.CodeSpan:
				var codeBuf strings.Builder
				for cs := c.FirstChild(); cs != nil; cs = cs.NextSibling() {
					if t, ok := cs.(*ast.Text); ok {
						codeBuf.Write(t.Segment.Value(source))
					}
				}
				b.WriteString("<text:span text:style-name=\"Code\">")
				b.WriteString(xmlEscape(codeBuf.String()))
				b.WriteString("</text:span>")

			case *ast.Link:
				dest := string(c.Destination)
				b.WriteString(fmt.Sprintf("<text:a xlink:type=\"simple\" xlink:href=\"%s\">", xmlEscape(dest)))
				writeInlines(b, c, bold, italic, strike, code)
				b.WriteString("</text:a>")

			case *ast.AutoLink:
				dest := string(c.URL(source))
				b.WriteString(fmt.Sprintf("<text:a xlink:type=\"simple\" xlink:href=\"%s\">%s</text:a>", xmlEscape(dest), xmlEscape(dest)))

			case *ast.Image:
				dest := string(c.Destination)
				alt := string(c.Text(source))
				ref, found := imageMap[dest]
				if !found {
					base := filepath.Base(dest)
					ref, found = imageMap[base]
				}
				if found {
					b.WriteString(fmt.Sprintf("<draw:frame draw:name=\"%s\" svg:width=\"%.2fmm\" svg:height=\"%.2fmm\" text:anchor-type=\"as-char\">"+
						"<draw:image xlink:href=\"%s\" xlink:type=\"simple\" xlink:show=\"embed\" xlink:actuate=\"onLoad\"/>"+
						"<svg:desc>%s</svg:desc>"+
						"</draw:frame>",
						xmlEscape(ref.alt), ref.widthMM, ref.heightMM, ref.zipPath, xmlEscape(alt)))
				} else {
					*warnings = append(*warnings, &Warning{Message: fmt.Sprintf("unsupported or missing image %q skipped", dest)})
					b.WriteString(fmt.Sprintf("[Image: %s]", xmlEscape(alt)))
				}

			case *ast.RawHTML:
				var rawBuf bytes.Buffer
				for i := 0; i < c.Segments.Len(); i++ {
					seg := c.Segments.At(i)
					rawBuf.Write((&seg).Value(source))
				}
				raw := rawBuf.String()
				if match := anchorTagRE.FindStringSubmatch(raw); len(match) > 1 {
					b.WriteString(fmt.Sprintf("<text:bookmark text:name=\"%s\"/>", xmlEscape(match[1])))
				}

			default:
				writeInlines(b, child, bold, italic, strike, code)
			}
		}
	}

	sectionCounter := 0
	inSection := false

	// Walk block nodes
	for node := root.FirstChild(); node != nil; node = node.NextSibling() {
		switch n := node.(type) {
		case *ast.Heading:
			lvl := n.Level
			if lvl < 1 {
				lvl = 1
			}
			if lvl > 6 {
				lvl = 6
			}
			headingText := string(n.Text(source))
			slug := documentSlug(headingText)

			bodyXML.WriteString(fmt.Sprintf("<text:h text:outline-level=\"%d\">", lvl))
			if slug != "" {
				bodyXML.WriteString(fmt.Sprintf("<text:bookmark text:name=\"%s\"/>", xmlEscape(slug)))
			}
			writeInlines(&bodyXML, n, false, false, false, false)
			bodyXML.WriteString("</text:h>")

		case *ast.Paragraph:
			bodyXML.WriteString("<text:p>")
			writeInlines(&bodyXML, n, false, false, false, false)
			bodyXML.WriteString("</text:p>")

		case *ast.Blockquote:
			bodyXML.WriteString("<text:p text:style-name=\"Blockquote\">")
			writeInlines(&bodyXML, n, false, true, false, false)
			bodyXML.WriteString("</text:p>")

		case *ast.FencedCodeBlock:
			var info string
			if n.Info != nil {
				info = strings.TrimSpace(string(n.Info.Segment.Value(source)))
			}
			if strings.HasPrefix(info, "parchment-") {
				continue
			}
			lines := n.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				lineText := string((&seg).Value(source))
				bodyXML.WriteString("<text:p text:style-name=\"CodeBlock\">")
				bodyXML.WriteString(xmlEscape(lineText))
				bodyXML.WriteString("</text:p>")
			}

		case *ast.ThematicBreak:
			bodyXML.WriteString("<text:p text:style-name=\"HorizontalRule\"/>")

		case *ast.List:
			e.writeOdtList(&bodyXML, n, source, 0, writeInlines)

		case *east.Table:
			e.writeOdtTable(&bodyXML, n, source, writeInlines)

		case *ast.HTMLBlock:
			var rawBuf bytes.Buffer
			lines := n.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				rawBuf.Write((&seg).Value(source))
			}
			raw := rawBuf.String()
			if pageBreakRE.MatchString(raw) {
				bodyXML.WriteString("<text:p text:style-name=\"PageBreak\"/>")
			} else if match := sectionBreakRE.FindStringSubmatch(raw); len(match) > 1 {
				cols, _ := strconv.Atoi(match[1])
				if cols < 1 {
					cols = 1
				} else if cols > document.MaxColumns {
					cols = document.MaxColumns
					*warnings = append(*warnings, &Warning{Message: "columns clamped to max"})
				}
				if inSection {
					bodyXML.WriteString("</text:section>")
				}
				sectionCounter++
				inSection = true
				bodyXML.WriteString(fmt.Sprintf("<text:section text:name=\"Section%d\" text:style-name=\"SectionCols%d\">", sectionCounter, cols))
			} else if match := anchorTagRE.FindStringSubmatch(raw); len(match) > 1 {
				bodyXML.WriteString(fmt.Sprintf("<text:p><text:bookmark text:name=\"%s\"/></text:p>", xmlEscape(match[1])))
			}
		}
	}

	if inSection {
		bodyXML.WriteString("</text:section>")
	}

	fontName := ExportFontFamily(doc.Layout.Font)
	fullContentXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"
                         xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0"
                         xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0"
                         xmlns:table="urn:oasis:names:tc:opendocument:xmlns:table:1.0"
                         xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0"
                         xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0"
                         xmlns:xlink="http://www.w3.org/1999/xlink"
                         xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0"
                         office:version="1.2">
  <office:automatic-styles>
    <style:style style:name="Bold" style:family="text">
      <style:text-properties fo:font-weight="bold"/>
    </style:style>
    <style:style style:name="Italic" style:family="text">
      <style:text-properties fo:font-style="italic"/>
    </style:style>
    <style:style style:name="BoldItalic" style:family="text">
      <style:text-properties fo:font-weight="bold" fo:font-style="italic"/>
    </style:style>
    <style:style style:name="Strike" style:family="text">
      <style:text-properties style:text-line-through-style="solid"/>
    </style:style>
    <style:style style:name="Code" style:family="text">
      <style:text-properties style:font-name="Courier New" fo:font-family="'Courier New', monospace" fo:background-color="#f0f0f0"/>
    </style:style>
    <style:style style:name="Blockquote" style:family="paragraph">
      <style:paragraph-properties fo:margin-left="12.7mm" fo:border-left="0.5mm solid #888888" fo:padding-left="2mm"/>
    </style:style>
    <style:style style:name="CodeBlock" style:family="paragraph">
      <style:paragraph-properties fo:margin-left="6.35mm" fo:background-color="#f5f5f5" fo:padding="1mm"/>
      <style:text-properties style:font-name="Courier New" fo:font-family="'Courier New', monospace" fo:font-size="9.5pt"/>
    </style:style>
    <style:style style:name="HorizontalRule" style:family="paragraph">
      <style:paragraph-properties fo:border-bottom="0.5mm solid #cccccc" fo:margin-top="3mm" fo:margin-bottom="3mm"/>
    </style:style>
    <style:style style:name="PageBreak" style:family="paragraph">
      <style:paragraph-properties fo:break-before="page"/>
    </style:style>
    <style:style style:name="SectionCols2" style:family="section">
      <style:section-properties><style:columns fo:column-count="2" fo:column-gap="5mm"/></style:section-properties>
    </style:style>
    <style:style style:name="SectionCols3" style:family="section">
      <style:section-properties><style:columns fo:column-count="3" fo:column-gap="5mm"/></style:section-properties>
    </style:style>
    <style:style style:name="SectionCols4" style:family="section">
      <style:section-properties><style:columns fo:column-count="4" fo:column-gap="5mm"/></style:section-properties>
    </style:style>
  </office:automatic-styles>
  <office:body>
    <office:text>
%s
    </office:text>
  </office:body>
</office:document-content>`, bodyXML.String())
	_ = fontName

	_, err = io.WriteString(entry, fullContentXML)
	return err
}

func (e *OdtExporter) writeOdtList(b *strings.Builder, list *ast.List, source []byte, level int, writeInlines func(*strings.Builder, ast.Node, bool, bool, bool, bool)) {
	b.WriteString("<text:list>")
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		listItem, ok := item.(*ast.ListItem)
		if !ok {
			continue
		}
		b.WriteString("<text:list-item>")
		for child := listItem.FirstChild(); child != nil; child = child.NextSibling() {
			switch c := child.(type) {
			case *ast.List:
				e.writeOdtList(b, c, source, level+1, writeInlines)
			default:
				b.WriteString("<text:p>")
				writeInlines(b, c, false, false, false, false)
				b.WriteString("</text:p>")
			}
		}
		b.WriteString("</text:list-item>")
	}
	b.WriteString("</text:list>")
}

func (e *OdtExporter) writeOdtTable(b *strings.Builder, table *east.Table, source []byte, writeInlines func(*strings.Builder, ast.Node, bool, bool, bool, bool)) {
	b.WriteString("<table:table table:name=\"Table1\">")

	for rowNode := table.FirstChild(); rowNode != nil; rowNode = rowNode.NextSibling() {
		switch r := rowNode.(type) {
		case *east.TableHeader:
			b.WriteString("<table:table-header-rows><table:table-row>")
			for cellNode := r.FirstChild(); cellNode != nil; cellNode = cellNode.NextSibling() {
				cell, ok := cellNode.(*east.TableCell)
				if !ok {
					continue
				}
				b.WriteString("<table:table-cell office:value-type=\"string\"><text:p><text:span text:style-name=\"Bold\">")
				writeInlines(b, cell, true, false, false, false)
				b.WriteString("</text:span></text:p></table:table-cell>")
			}
			b.WriteString("</table:table-row></table:table-header-rows>")

		case *east.TableRow:
			b.WriteString("<table:table-row>")
			for cellNode := r.FirstChild(); cellNode != nil; cellNode = cellNode.NextSibling() {
				cell, ok := cellNode.(*east.TableCell)
				if !ok {
					continue
				}
				b.WriteString("<table:table-cell office:value-type=\"string\"><text:p>")
				writeInlines(b, cell, false, false, false, false)
				b.WriteString("</text:p></table:table-cell>")
			}
			b.WriteString("</table:table-row>")
		}
	}

	b.WriteString("</table:table>")
}

func odtRunningParagraph(text, title string) string {
	if text == "" {
		return "<text:p/>"
	}
	var b strings.Builder
	b.WriteString("<text:p>")
	parts := splitDynamicTokens(text)
	for _, part := range parts {
		switch part {
		case "{page}":
			b.WriteString("<text:page-number/>")
		case "{pages}":
			b.WriteString("<text:page-count/>")
		case "{title}":
			b.WriteString(fmt.Sprintf("<text:title>%s</text:title>", xmlEscape(title)))
		default:
			b.WriteString(xmlEscape(part))
		}
	}
	b.WriteString("</text:p>\n")
	return b.String()
}
