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
	"regexp"
	"strconv"
	"strings"

	"example.com/parchment/internal/document"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

var (
	pageBreakRE    = regexp.MustCompile(`<!--\s*parchment:page-break\s*-->`)
	sectionBreakRE = regexp.MustCompile(`<!--\s*parchment:section-break\s+columns=(\d+)(?:\s+(continuous))?\s*-->`)
	anchorTagRE    = regexp.MustCompile(`<a\s+(?:id|name)="([^"]+)"(?:\s*>|\s*/>)`)
)

// DocxExporter exports a Parchment Document to a DOCX archive.
type DocxExporter struct{}

// NewDocxExporter returns a new DocxExporter instance.
func NewDocxExporter() *DocxExporter {
	return &DocxExporter{}
}

type imageRef struct {
	relID    string
	fileName string
	width    int64
	height   int64
	alt      string
}

type linkRef struct {
	relID string
	url   string
}

// Export writes the document as a valid OpenXML .docx archive.
func (e *DocxExporter) Export(w io.Writer, doc document.Document) ([]error, error) {
	var warnings []error

	zw := zip.NewWriter(w)
	defer zw.Close()

	// 1. Process images
	imageMap := make(map[string]imageRef)
	var imagesInDoc []imageRef
	nextRelID := 1

	// Compute max text width in EMU
	pageSizeW, _, _ := pageSizeDimensions(doc.Layout.PageSize, doc.Layout.Orientation)
	textWidthMM := pageSizeW - float64(doc.Layout.Margins.Left+doc.Layout.Margins.Right)
	if textWidthMM <= 20 {
		textWidthMM = 150
	}
	maxEMU := int64(textWidthMM * 36000)

	for i, img := range doc.Images {
		ext := strings.ToLower(filepath.Ext(img.Name))
		if ext == "" {
			ext = ".png"
		}
		targetName := fmt.Sprintf("image%d%s", i+1, ext)

		// Decode image config for dimensions
		cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data))
		wPx := cfg.Width
		hPx := cfg.Height
		if err != nil || wPx <= 0 || hPx <= 0 {
			wPx = 400
			hPx = 300
		}

		wEMU := int64(wPx) * 9525
		hEMU := int64(hPx) * 9525
		if wEMU > maxEMU && wEMU > 0 {
			hEMU = hEMU * maxEMU / wEMU
			wEMU = maxEMU
		}

		relID := fmt.Sprintf("rIdImg%d", nextRelID)
		nextRelID++

		ref := imageRef{
			relID:    relID,
			fileName: targetName,
			width:    wEMU,
			height:   hEMU,
			alt:      img.Name,
		}
		imageMap[img.Name] = ref
		imagesInDoc = append(imagesInDoc, ref)

		// Write image file to zip
		imgEntry, err := zw.Create("word/media/" + targetName)
		if err != nil {
			return warnings, fmt.Errorf("create image entry %s: %w", targetName, err)
		}
		if _, err := imgEntry.Write(img.Data); err != nil {
			return warnings, fmt.Errorf("write image entry %s: %w", targetName, err)
		}
	}

	// 2. Parse Markdown AST
	source := []byte(doc.Body)
	root := ParseMarkdown(doc.Body)

	// Collect hyperlinks and bookmarks
	var externalLinks []linkRef
	nextBookmarkID := 1
	var bodyXML strings.Builder

	// Helper to add external link relationship
	addLink := func(url string) string {
		relID := fmt.Sprintf("rIdLink%d", nextRelID)
		nextRelID++
		externalLinks = append(externalLinks, linkRef{
			relID: relID,
			url:   url,
		})
		return relID
	}

	// Helper to write inline nodes
	var writeInlines func(b *strings.Builder, n ast.Node, bold, italic, strike, code bool)
	writeInlines = func(b *strings.Builder, n ast.Node, bold, italic, strike, code bool) {
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			switch c := child.(type) {
			case *ast.Text:
				val := c.Segment.Value(source)
				b.WriteString("<w:r><w:rPr>")
				if bold {
					b.WriteString("<w:b/>")
				}
				if italic {
					b.WriteString("<w:i/>")
				}
				if strike {
					b.WriteString("<w:strike/>")
				}
				if code {
					b.WriteString("<w:rFonts w:ascii=\"Courier New\" w:hAnsi=\"Courier New\"/><w:shd w:val=\"clear\" w:color=\"auto\" w:fill=\"EAEAEA\"/>")
				}
				b.WriteString("</w:rPr>")
				b.WriteString("<w:t xml:space=\"preserve\">")
				b.WriteString(xmlEscape(string(val)))
				b.WriteString("</w:t></w:r>")
				if c.SoftLineBreak() || c.HardLineBreak() {
					b.WriteString("<w:r><w:br/></w:r>")
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
				// Collect code text
				var codeBuf strings.Builder
				for cs := c.FirstChild(); cs != nil; cs = cs.NextSibling() {
					if t, ok := cs.(*ast.Text); ok {
						codeBuf.Write(t.Segment.Value(source))
					}
				}
				b.WriteString("<w:r><w:rPr><w:rFonts w:ascii=\"Courier New\" w:hAnsi=\"Courier New\"/><w:shd w:val=\"clear\" w:color=\"auto\" w:fill=\"EAEAEA\"/></w:rPr><w:t xml:space=\"preserve\">")
				b.WriteString(xmlEscape(codeBuf.String()))
				b.WriteString("</w:t></w:r>")

			case *ast.Link:
				dest := string(c.Destination)
				if strings.HasPrefix(dest, "#") {
					anchor := strings.TrimPrefix(dest, "#")
					b.WriteString(fmt.Sprintf("<w:hyperlink w:anchor=\"%s\">", xmlEscape(anchor)))
					writeInlines(b, c, bold, italic, strike, code)
					b.WriteString("</w:hyperlink>")
				} else {
					relID := addLink(dest)
					b.WriteString(fmt.Sprintf("<w:hyperlink r:id=\"%s\">", relID))
					writeInlines(b, c, bold, italic, strike, code)
					b.WriteString("</w:hyperlink>")
				}

			case *ast.AutoLink:
				dest := string(c.URL(source))
				relID := addLink(dest)
				b.WriteString(fmt.Sprintf("<w:hyperlink r:id=\"%s\"><w:r><w:rPr><w:color w:val=\"0563C1\"/><w:u w:val=\"single\"/></w:rPr><w:t>", relID))
				b.WriteString(xmlEscape(dest))
				b.WriteString("</w:t></w:r></w:hyperlink>")

			case *ast.Image:
				dest := string(c.Destination)
				alt := string(c.Text(source))
				ref, found := imageMap[dest]
				if !found {
					// Check if destination without prefix or by basename
					base := filepath.Base(dest)
					ref, found = imageMap[base]
				}
				if found {
					bmID := nextBookmarkID
					nextBookmarkID++
					b.WriteString("<w:r>")
					b.WriteString(fmt.Sprintf("<w:drawing><wp:inline distT=\"0\" distB=\"0\" distL=\"0\" distR=\"0\">"+
						"<wp:extent cx=\"%d\" cy=\"%d\"/>"+
						"<wp:docPr id=\"%d\" name=\"%s\" descr=\"%s\"/>"+
						"<a:graphic xmlns:a=\"http://schemas.openxmlformats.org/drawingml/2006/main\">"+
						"<a:graphicData uri=\"http://schemas.openxmlformats.org/drawingml/2006/picture\">"+
						"<pic:pic xmlns:pic=\"http://schemas.openxmlformats.org/drawingml/2006/picture\">"+
						"<pic:nvPicPr>"+
						"<pic:cNvPr id=\"%d\" name=\"%s\"/>"+
						"<pic:cNvPicPr/>"+
						"</pic:nvPicPr>"+
						"<pic:blipFill>"+
						"<a:blip xmlns:r=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships\" r:embed=\"%s\"/>"+
						"<a:stretch><a:fillRect/></a:stretch>"+
						"</pic:blipFill>"+
						"<pic:spPr>"+
						"<a:xfrm>"+
						"<a:off x=\"0\" y=\"0\"/>"+
						"<a:ext cx=\"%d\" cy=\"%d\"/>"+
						"</a:xfrm>"+
						"<a:prstGeom prst=\"rect\"><a:avLst/></a:prstGeom>"+
						"</pic:spPr>"+
						"</pic:pic>"+
						"</a:graphicData>"+
						"</a:graphic>"+
						"</wp:inline></w:drawing>",
						ref.width, ref.height, bmID, xmlEscape(ref.fileName), xmlEscape(alt),
						bmID, xmlEscape(ref.fileName), ref.relID, ref.width, ref.height))
					b.WriteString("</w:r>")
				} else {
					warnings = append(warnings, &Warning{Message: fmt.Sprintf("unsupported or missing image %q skipped", dest)})
					b.WriteString("<w:r><w:rPr><w:i/></w:rPr><w:t>")
					b.WriteString(xmlEscape(fmt.Sprintf("[Image: %s]", alt)))
					b.WriteString("</w:t></w:r>")
				}

			case *ast.RawHTML:
				var rawBuf bytes.Buffer
				for i := 0; i < c.Segments.Len(); i++ {
					seg := c.Segments.At(i)
					rawBuf.Write((&seg).Value(source))
				}
				raw := rawBuf.String()
				if match := anchorTagRE.FindStringSubmatch(raw); len(match) > 1 {
					bmID := nextBookmarkID
					nextBookmarkID++
					b.WriteString(fmt.Sprintf("<w:bookmarkStart w:id=\"%d\" w:name=\"%s\"/><w:bookmarkEnd w:id=\"%d\"/>", bmID, xmlEscape(match[1]), bmID))
				}

			default:
				writeInlines(b, child, bold, italic, strike, code)
			}
		}
	}

	// 3. Walk block nodes
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

			bodyXML.WriteString(fmt.Sprintf("<w:p><w:pPr><w:pStyle w:val=\"Heading%d\"/></w:pPr>", lvl))
			if slug != "" {
				bmID := nextBookmarkID
				nextBookmarkID++
				bodyXML.WriteString(fmt.Sprintf("<w:bookmarkStart w:id=\"%d\" w:name=\"%s\"/><w:bookmarkEnd w:id=\"%d\"/>", bmID, xmlEscape(slug), bmID))
			}
			writeInlines(&bodyXML, n, false, false, false, false)
			bodyXML.WriteString("</w:p>")

		case *ast.Paragraph:
			bodyXML.WriteString("<w:p>")
			writeInlines(&bodyXML, n, false, false, false, false)
			bodyXML.WriteString("</w:p>")

		case *ast.Blockquote:
			bodyXML.WriteString("<w:p><w:pPr><w:ind w:left=\"720\"/><w:pBdr><w:left w:val=\"single\" w:sz=\"18\" w:space=\"8\" w:color=\"888888\"/></w:pBdr></w:pPr>")
			writeInlines(&bodyXML, n, false, true, false, false)
			bodyXML.WriteString("</w:p>")

		case *ast.FencedCodeBlock:
			var info string
			if n.Info != nil {
				info = strings.TrimSpace(string(n.Info.Segment.Value(source)))
			}
			if strings.HasPrefix(info, "parchment-") {
				// Hide reserved parchment-* fences
				continue
			}
			lines := n.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				lineText := string((&seg).Value(source))
				bodyXML.WriteString("<w:p><w:pPr><w:ind w:left=\"360\"/><w:shd w:val=\"clear\" w:color=\"auto\" w:fill=\"F5F5F5\"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii=\"Courier New\" w:hAnsi=\"Courier New\"/><w:sz w:val=\"20\"/></w:rPr><w:t xml:space=\"preserve\">")
				bodyXML.WriteString(xmlEscape(lineText))
				bodyXML.WriteString("</w:t></w:r></w:p>")
			}

		case *ast.ThematicBreak:
			bodyXML.WriteString("<w:p><w:pPr><w:pBdr><w:bottom w:val=\"single\" w:sz=\"6\" w:space=\"1\" w:color=\"auto\"/></w:pBdr></w:pPr></w:p>")

		case *ast.List:
			e.writeList(&bodyXML, n, source, 0, &nextBookmarkID, &addLink, writeInlines)

		case *east.Table:
			e.writeTable(&bodyXML, n, source, writeInlines)

		case *ast.HTMLBlock:
			var rawBuf bytes.Buffer
			lines := n.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				rawBuf.Write((&seg).Value(source))
			}
			raw := rawBuf.String()
			if pageBreakRE.MatchString(raw) {
				bodyXML.WriteString("<w:p><w:r><w:br w:type=\"page\"/></w:r></w:p>")
			} else if match := sectionBreakRE.FindStringSubmatch(raw); len(match) > 1 {
				cols, _ := strconv.Atoi(match[1])
				if cols < 1 {
					cols = 1
				} else if cols > document.MaxColumns {
					cols = document.MaxColumns
					warnings = append(warnings, &Warning{Message: "columns clamped to max"})
				}
				bodyXML.WriteString(fmt.Sprintf("<w:p><w:pPr><w:sectPr><w:cols w:num=\"%d\" w:space=\"720\"/><w:type w:val=\"continuous\"/></w:sectPr></w:pPr></w:p>", cols))
			} else if match := anchorTagRE.FindStringSubmatch(raw); len(match) > 1 {
				bmID := nextBookmarkID
				nextBookmarkID++
				bodyXML.WriteString(fmt.Sprintf("<w:p><w:bookmarkStart w:id=\"%d\" w:name=\"%s\"/><w:bookmarkEnd w:id=\"%d\"/></w:p>", bmID, xmlEscape(match[1]), bmID))
			}
		}
	}

	// 4. Headers and footers
	hasHeader := doc.Layout.Header != ""
	hasFooter := doc.Layout.Footer != "" || (doc.Layout.PageNumbers != "" && doc.Layout.PageNumbers != document.NumbersNone)

	var headerRelID, footerRelID string
	if hasHeader {
		headerRelID = fmt.Sprintf("rIdHdr%d", nextRelID)
		nextRelID++
		if err := e.writeHeader(zw, doc); err != nil {
			return warnings, err
		}
	}
	if hasFooter {
		footerRelID = fmt.Sprintf("rIdFtr%d", nextRelID)
		nextRelID++
		if err := e.writeFooter(zw, doc); err != nil {
			return warnings, err
		}
	}

	// 5. Final section properties at end of body
	pgW, pgH, orient := pageSizeDimensions(doc.Layout.PageSize, doc.Layout.Orientation)
	pgWTwips := MMToTwip(pgW)
	pgHTwips := MMToTwip(pgH)
	topTwips := MMToTwip(float64(doc.Layout.Margins.Top))
	rightTwips := MMToTwip(float64(doc.Layout.Margins.Right))
	bottomTwips := MMToTwip(float64(doc.Layout.Margins.Bottom))
	leftTwips := MMToTwip(float64(doc.Layout.Margins.Left))
	cols := doc.Layout.Columns
	if cols < 1 {
		cols = 1
	} else if cols > document.MaxColumns {
		cols = document.MaxColumns
		warnings = append(warnings, &Warning{Message: "columns clamped to max"})
	}

	bodyXML.WriteString("<w:sectPr>")
	if hasHeader {
		bodyXML.WriteString(fmt.Sprintf("<w:headerReference w:type=\"default\" r:id=\"%s\"/>", headerRelID))
	}
	if hasFooter {
		bodyXML.WriteString(fmt.Sprintf("<w:footerReference w:type=\"default\" r:id=\"%s\"/>", footerRelID))
	}
	bodyXML.WriteString(fmt.Sprintf("<w:pgSz w:w=\"%d\" w:h=\"%d\" w:orient=\"%s\"/>", pgWTwips, pgHTwips, orient))
	bodyXML.WriteString(fmt.Sprintf("<w:pgMar w:top=\"%d\" w:right=\"%d\" w:bottom=\"%d\" w:left=\"%d\" w:header=\"720\" w:footer=\"720\"/>", topTwips, rightTwips, bottomTwips, leftTwips))
	bodyXML.WriteString(fmt.Sprintf("<w:cols w:num=\"%d\" w:space=\"720\"/>", cols))
	bodyXML.WriteString("</w:sectPr>")

	// Write word/document.xml
	docXmlEntry, err := zw.Create("word/document.xml")
	if err != nil {
		return warnings, err
	}
	fullDocXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
            xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"
            xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
            xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">
  <w:body>
%s
  </w:body>
</w:document>`, bodyXML.String())
	if _, err := io.WriteString(docXmlEntry, fullDocXML); err != nil {
		return warnings, err
	}

	// 6. word/_rels/document.xml.rels
	relsEntry, err := zw.Create("word/_rels/document.xml.rels")
	if err != nil {
		return warnings, err
	}
	var relsXML strings.Builder
	relsXML.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rIdStyles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
`)
	if hasHeader {
		relsXML.WriteString(fmt.Sprintf("  <Relationship Id=\"%s\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/header\" Target=\"header1.xml\"/>\n", headerRelID))
	}
	if hasFooter {
		relsXML.WriteString(fmt.Sprintf("  <Relationship Id=\"%s\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer\" Target=\"footer1.xml\"/>\n", footerRelID))
	}
	for _, img := range imagesInDoc {
		relsXML.WriteString(fmt.Sprintf("  <Relationship Id=\"%s\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/image\" Target=\"media/%s\"/>\n", img.relID, img.fileName))
	}
	for _, l := range externalLinks {
		relsXML.WriteString(fmt.Sprintf("  <Relationship Id=\"%s\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink\" Target=\"%s\" TargetMode=\"External\"/>\n", l.relID, xmlEscape(l.url)))
	}
	relsXML.WriteString("</Relationships>")
	if _, err := io.WriteString(relsEntry, relsXML.String()); err != nil {
		return warnings, err
	}

	// 7. word/styles.xml
	stylesEntry, err := zw.Create("word/styles.xml")
	if err != nil {
		return warnings, err
	}
	fontName := ExportFontFamily(doc.Layout.Font)
	stylesXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:docDefaults>
    <w:rPrDefault>
      <w:rPr>
        <w:rFonts w:ascii="%s" w:hAnsi="%s" w:cs="%s"/>
        <w:sz w:val="22"/>
      </w:rPr>
    </w:rPrDefault>
  </w:docDefaults>
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal">
    <w:name w:val="Normal"/>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading1">
    <w:name w:val="heading 1"/>
    <w:basedOn w:val="Normal"/>
    <w:qFormat/>
    <w:rPr>
      <w:b/>
      <w:sz w:val="40"/>
    </w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading2">
    <w:name w:val="heading 2"/>
    <w:basedOn w:val="Normal"/>
    <w:qFormat/>
    <w:rPr>
      <w:b/>
      <w:sz w:val="32"/>
    </w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading3">
    <w:name w:val="heading 3"/>
    <w:basedOn w:val="Normal"/>
    <w:qFormat/>
    <w:rPr>
      <w:b/>
      <w:sz w:val="28"/>
    </w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading4">
    <w:name w:val="heading 4"/>
    <w:basedOn w:val="Normal"/>
    <w:qFormat/>
    <w:rPr>
      <w:b/>
      <w:sz w:val="24"/>
    </w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading5">
    <w:name w:val="heading 5"/>
    <w:basedOn w:val="Normal"/>
    <w:qFormat/>
    <w:rPr>
      <w:b/>
      <w:sz w:val="22"/>
    </w:rPr>
  </w:style>
  <w:style w:type="paragraph" w:styleId="Heading6">
    <w:name w:val="heading 6"/>
    <w:basedOn w:val="Normal"/>
    <w:qFormat/>
    <w:rPr>
      <w:b/>
      <w:sz w:val="20"/>
    </w:rPr>
  </w:style>
</w:styles>`, fontName, fontName, fontName)
	if _, err := io.WriteString(stylesEntry, stylesXML); err != nil {
		return warnings, err
	}

	// 8. _rels/.rels
	rootRelsEntry, err := zw.Create("_rels/.rels")
	if err != nil {
		return warnings, err
	}
	rootRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`
	if _, err := io.WriteString(rootRelsEntry, rootRels); err != nil {
		return warnings, err
	}

	// 9. [Content_Types].xml
	ctEntry, err := zw.Create("[Content_Types].xml")
	if err != nil {
		return warnings, err
	}
	var ctXML strings.Builder
	ctXML.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="png" ContentType="image/png"/>
  <Default Extension="jpeg" ContentType="image/jpeg"/>
  <Default Extension="jpg" ContentType="image/jpeg"/>
  <Default Extension="gif" ContentType="image/gif"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
`)
	if hasHeader {
		ctXML.WriteString("  <Override PartName=\"/word/header1.xml\" ContentType=\"application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml\"/>\n")
	}
	if hasFooter {
		ctXML.WriteString("  <Override PartName=\"/word/footer1.xml\" ContentType=\"application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml\"/>\n")
	}
	ctXML.WriteString("</Types>")
	if _, err := io.WriteString(ctEntry, ctXML.String()); err != nil {
		return warnings, err
	}

	return warnings, nil
}

func (e *DocxExporter) writeList(b *strings.Builder, list *ast.List, source []byte, level int, nextBM *int, addLink *func(string) string, writeInlines func(*strings.Builder, ast.Node, bool, bool, bool, bool)) {
	isOrdered := list.IsOrdered()
	startNum := list.Start
	if startNum <= 0 {
		startNum = 1
	}

	itemIdx := 0
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		listItem, ok := item.(*ast.ListItem)
		if !ok {
			continue
		}
		itemIdx++

		var marker string
		if isOrdered {
			marker = fmt.Sprintf("%d. ", startNum+itemIdx-1)
		} else {
			marker = "• "
		}

		indent := (level + 1) * 720
		b.WriteString(fmt.Sprintf("<w:p><w:pPr><w:ind w:left=\"%d\" w:hanging=\"360\"/></w:pPr>", indent))
		b.WriteString(fmt.Sprintf("<w:r><w:t xml:space=\"preserve\">%s</w:t></w:r>", xmlEscape(marker)))

		for child := listItem.FirstChild(); child != nil; child = child.NextSibling() {
			switch c := child.(type) {
			case *ast.List:
				b.WriteString("</w:p>")
				e.writeList(b, c, source, level+1, nextBM, addLink, writeInlines)
				// Re-open paragraph if subsequent siblings
				if child.NextSibling() != nil {
					b.WriteString(fmt.Sprintf("<w:p><w:pPr><w:ind w:left=\"%d\"/></w:pPr>", indent))
				}
			default:
				writeInlines(b, c, false, false, false, false)
			}
		}
		if !strings.HasSuffix(b.String(), "</w:p>") {
			b.WriteString("</w:p>")
		}
	}
}

func (e *DocxExporter) writeTable(b *strings.Builder, table *east.Table, source []byte, writeInlines func(*strings.Builder, ast.Node, bool, bool, bool, bool)) {
	b.WriteString("<w:tbl>")
	b.WriteString("<w:tblPr>")
	b.WriteString("<w:tblW w:w=\"0\" w:type=\"auto\"/>")
	b.WriteString("<w:tblBorders>")
	b.WriteString("<w:top w:val=\"single\" w:sz=\"4\" w:space=\"0\" w:color=\"CCCCCC\"/>")
	b.WriteString("<w:left w:val=\"single\" w:sz=\"4\" w:space=\"0\" w:color=\"CCCCCC\"/>")
	b.WriteString("<w:bottom w:val=\"single\" w:sz=\"4\" w:space=\"0\" w:color=\"CCCCCC\"/>")
	b.WriteString("<w:right w:val=\"single\" w:sz=\"4\" w:space=\"0\" w:color=\"CCCCCC\"/>")
	b.WriteString("<w:insideH w:val=\"single\" w:sz=\"4\" w:space=\"0\" w:color=\"CCCCCC\"/>")
	b.WriteString("<w:insideV w:val=\"single\" w:sz=\"4\" w:space=\"0\" w:color=\"CCCCCC\"/>")
	b.WriteString("</w:tblBorders>")
	b.WriteString("</w:tblPr>")

	for rowNode := table.FirstChild(); rowNode != nil; rowNode = rowNode.NextSibling() {
		switch r := rowNode.(type) {
		case *east.TableHeader:
			b.WriteString("<w:tr><w:trPr><w:tblHeader/></w:trPr>")
			for cellNode := r.FirstChild(); cellNode != nil; cellNode = cellNode.NextSibling() {
				cell, ok := cellNode.(*east.TableCell)
				if !ok {
					continue
				}
				b.WriteString("<w:tc><w:tcPr><w:shd w:val=\"clear\" w:color=\"auto\" w:fill=\"F0F0F0\"/></w:tcPr>")
				b.WriteString(fmt.Sprintf("<w:p><w:pPr><w:jc w:val=\"%s\"/></w:pPr>", tableAlign(cell.Alignment)))
				writeInlines(b, cell, true, false, false, false)
				b.WriteString("</w:p></w:tc>")
			}
			b.WriteString("</w:tr>")

		case *east.TableRow:
			b.WriteString("<w:tr>")
			for cellNode := r.FirstChild(); cellNode != nil; cellNode = cellNode.NextSibling() {
				cell, ok := cellNode.(*east.TableCell)
				if !ok {
					continue
				}
				b.WriteString("<w:tc>")
				b.WriteString(fmt.Sprintf("<w:p><w:pPr><w:jc w:val=\"%s\"/></w:pPr>", tableAlign(cell.Alignment)))
				writeInlines(b, cell, false, false, false, false)
				b.WriteString("</w:p></w:tc>")
			}
			b.WriteString("</w:tr>")
		}
	}

	b.WriteString("</w:tbl>")
}

func tableAlign(al east.Alignment) string {
	switch al {
	case east.AlignCenter:
		return "center"
	case east.AlignRight:
		return "right"
	default:
		return "left"
	}
}

func (e *DocxExporter) writeHeader(zw *zip.Writer, doc document.Document) error {
	entry, err := zw.Create("word/header1.xml")
	if err != nil {
		return err
	}
	left, center, right := splitRunningParts(doc.Layout.Header)

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
`)
	b.WriteString(runningTable(left, center, right, doc.Title))
	b.WriteString("</w:hdr>")

	_, err = io.WriteString(entry, b.String())
	return err
}

func (e *DocxExporter) writeFooter(zw *zip.Writer, doc document.Document) error {
	entry, err := zw.Create("word/footer1.xml")
	if err != nil {
		return err
	}
	left, center, right := splitRunningParts(doc.Layout.Footer)

	// If page numbers placement is configured and not yet in footer
	if doc.Layout.PageNumbers != "" && doc.Layout.PageNumbers != document.NumbersNone {
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
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
`)
	b.WriteString(runningTable(left, center, right, doc.Title))
	b.WriteString("</w:ftr>")

	_, err = io.WriteString(entry, b.String())
	return err
}

func runningTable(left, center, right, title string) string {
	var b strings.Builder
	b.WriteString("<w:tbl><w:tblPr><w:tblW w:w=\"0\" w:type=\"auto\"/><w:tblBorders><w:top w:val=\"none\"/><w:left w:val=\"none\"/><w:bottom w:val=\"none\"/><w:right w:val=\"none\"/><w:insideH w:val=\"none\"/><w:insideV w:val=\"none\"/></w:tblBorders></w:tblPr><w:tr>")

	writeCell := func(text, jc string) {
		b.WriteString(fmt.Sprintf("<w:tc><w:p><w:pPr><w:jc w:val=\"%s\"/></w:pPr>", jc))
		if text != "" {
			parts := splitDynamicTokens(text)
			for _, part := range parts {
				switch part {
				case "{page}":
					b.WriteString("<w:fldSimple w:instr=\"PAGE\"/>")
				case "{pages}":
					b.WriteString("<w:fldSimple w:instr=\"NUMPAGES\"/>")
				case "{title}":
					b.WriteString(fmt.Sprintf("<w:r><w:t>%s</w:t></w:r>", xmlEscape(title)))
				default:
					b.WriteString(fmt.Sprintf("<w:r><w:t xml:space=\"preserve\">%s</w:t></w:r>", xmlEscape(part)))
				}
			}
		}
		b.WriteString("</w:p></w:tc>")
	}

	writeCell(left, "left")
	writeCell(center, "center")
	writeCell(right, "right")

	b.WriteString("</w:tr></w:tbl>")
	return b.String()
}

func splitRunningParts(s string) (left, center, right string) {
	if s == "" {
		return "", "", ""
	}
	parts := strings.Split(s, "|")
	switch len(parts) {
	case 1:
		return strings.TrimSpace(parts[0]), "", ""
	case 2:
		return strings.TrimSpace(parts[0]), "", strings.TrimSpace(parts[1])
	default:
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
	}
}

func splitDynamicTokens(s string) []string {
	var result []string
	curr := s
	for len(curr) > 0 {
		idx1 := strings.Index(curr, "{page}")
		idx2 := strings.Index(curr, "{pages}")
		idx3 := strings.Index(curr, "{title}")

		bestIdx := -1
		bestToken := ""
		for _, candidate := range []struct {
			idx   int
			token string
		}{{idx1, "{page}"}, {idx2, "{pages}"}, {idx3, "{title}"}} {
			if candidate.idx != -1 && (bestIdx == -1 || candidate.idx < bestIdx) {
				bestIdx = candidate.idx
				bestToken = candidate.token
			}
		}

		if bestIdx == -1 {
			result = append(result, curr)
			break
		}

		if bestIdx > 0 {
			result = append(result, curr[:bestIdx])
		}
		result = append(result, bestToken)
		curr = curr[bestIdx+len(bestToken):]
	}
	return result
}

func pageSizeDimensions(size, orientation string) (float64, float64, string) {
	var w, h float64
	switch strings.ToLower(size) {
	case document.PageA4:
		w, h = 210.0, 297.0
	case document.PageLegal:
		w, h = 215.9, 355.6
	default: // letter
		w, h = 215.9, 279.4
	}

	orient := "portrait"
	if strings.ToLower(orientation) == document.Landscape {
		w, h = h, w
		orient = "landscape"
	}
	return w, h, orient
}

func documentSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteRune('-')
		}
	}
	return b.String()
}

func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
