package docinterop

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

// DocxImporter imports a DOCX archive into a Parchment Document draft.
type DocxImporter struct{}

// NewDocxImporter returns a new DocxImporter.
func NewDocxImporter() *DocxImporter {
	return &DocxImporter{}
}

// SniffDOCX reports whether the zip reader contains a valid DOCX content type.
func SniffDOCX(zr *zip.Reader) bool {
	for _, f := range zr.File {
		if f.Name == "[Content_Types].xml" {
			rc, err := f.Open()
			if err != nil {
				return false
			}
			data, _ := io.ReadAll(rc)
			rc.Close()
			content := string(data)
			return strings.Contains(content, "wordprocessingml.document.main+xml") ||
				strings.Contains(content, "word/document.xml")
		}
	}
	return false
}

// Import reads a DOCX archive and converts it to a Parchment document Draft.
func (imp *DocxImporter) Import(r io.ReaderAt, size int64) (document.Draft, []error, error) {
	var warnings []error

	zr, err := ReadZip(r, size)
	if err != nil {
		return document.Draft{}, nil, err
	}

	if !SniffDOCX(zr) {
		return document.Draft{}, nil, errors.New("not a valid DOCX archive: missing document content type")
	}

	// Index files in zip
	zipFiles := make(map[string]*zip.File)
	for _, f := range zr.File {
		cleanName := path.Clean(f.Name)
		zipFiles[cleanName] = f
		zipFiles[f.Name] = f
	}

	// Read relationships in word/_rels/document.xml.rels
	rels := make(map[string]string)
	relTypes := make(map[string]string)
	if relsFile, ok := zipFiles["word/_rels/document.xml.rels"]; ok {
		parseRels(relsFile, rels, relTypes)
	}

	// Read styles.xml for default font
	defaultFont := "courier"
	if stylesFile, ok := zipFiles["word/styles.xml"]; ok {
		if font := parseDefaultFont(stylesFile); font != "" {
			defaultFont = MapFontFamily(font)
		}
	}

	// Read document.xml
	docFile, ok := zipFiles["word/document.xml"]
	if !ok {
		return document.Draft{}, nil, errors.New("missing word/document.xml in DOCX archive")
	}

	docRC, err := docFile.Open()
	if err != nil {
		return document.Draft{}, nil, fmt.Errorf("open word/document.xml: %w", err)
	}
	defer docRC.Close()

	// Parse document.xml
	dec := NewXMLDecoder(docRC)
	builder := &docxBodyBuilder{
		zipFiles: zipFiles,
		rels:     rels,
		relTypes: relTypes,
	}

	layout := document.DefaultLayout()
	layout.Font = defaultFont

	if err := builder.parse(dec, &layout, &warnings); err != nil {
		return document.Draft{}, warnings, fmt.Errorf("parse word/document.xml: %w", err)
	}

	// Parse header1.xml and footer1.xml if present
	for relID, target := range rels {
		relType := relTypes[relID]
		targetPath := path.Clean(path.Join("word", target))
		if strings.HasSuffix(relType, "/header") {
			if f, ok := zipFiles[targetPath]; ok {
				hdrText, isComplex := parseRunningXML(f)
				mapped, w := MapHeaderFooter(hdrText, isComplex)
				if w != nil {
					warnings = append(warnings, w)
				}
				layout.Header = mapped
			}
		} else if strings.HasSuffix(relType, "/footer") {
			if f, ok := zipFiles[targetPath]; ok {
				ftrText, isComplex := parseRunningXML(f)
				mapped, w := MapHeaderFooter(ftrText, isComplex)
				if w != nil {
					warnings = append(warnings, w)
				}
				layout.Footer = mapped
			}
		}
	}

	draft := document.Draft{
		Body:   strings.TrimSpace(builder.body.String()),
		Layout: layout,
		Images: builder.images,
	}

	return draft, warnings, nil
}

type docxBodyBuilder struct {
	zipFiles map[string]*zip.File
	rels     map[string]string
	relTypes map[string]string
	body     strings.Builder
	images   []artifact.Image
}

func (b *docxBodyBuilder) parse(dec *xml.Decoder, layout *document.Layout, warnings *[]error) error {
	inBody := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			if local == "body" {
				inBody = true
			} else if inBody {
				switch local {
				case "p":
					pText := b.parseParagraph(dec, t, layout, warnings)
					if pText != "" {
						if b.body.Len() > 0 {
							b.body.WriteString("\n\n")
						}
						b.body.WriteString(pText)
					}
				case "tbl":
					tblText := b.parseTable(dec, warnings)
					if tblText != "" {
						if b.body.Len() > 0 {
							b.body.WriteString("\n\n")
						}
						b.body.WriteString(tblText)
					}
				case "sectPr":
					b.parseSectPr(dec, t, layout, warnings)
				}
			}
		}
	}

	return nil
}

func (b *docxBodyBuilder) parseParagraph(dec *xml.Decoder, start xml.StartElement, layout *document.Layout, warnings *[]error) string {
	var pBuf strings.Builder
	var headingLevel int
	isBlockquote := false
	isListItem := false
	listIndent := 0
	listPrefix := "- "

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pPr":
				hLvl, bq, li, ind, pref := b.parsePPr(dec, t, layout, warnings)
				headingLevel = hLvl
				isBlockquote = bq
				isListItem = li
				listIndent = ind
				if pref != "" {
					listPrefix = pref
				}
			case "r":
				b.parseRun(dec, t, &pBuf, warnings)
			case "hyperlink":
				b.parseHyperlink(dec, t, &pBuf, warnings)
			case "bookmarkStart":
				name := getAttr(t, "name")
				if name != "" && !strings.HasPrefix(name, "_") {
					// Check if this bookmark is just a heading slug
					if headingLevel == 0 {
						pBuf.WriteString(fmt.Sprintf("<a id=\"%s\"></a>", name))
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				text := strings.TrimSpace(pBuf.String())
				if text == "" {
					return ""
				}
				if headingLevel > 0 {
					return fmt.Sprintf("%s %s", strings.Repeat("#", headingLevel), text)
				}
				if strings.HasPrefix(text, "• ") {
					indentStr := strings.Repeat("  ", listIndent)
					return fmt.Sprintf("%s- %s", indentStr, strings.TrimPrefix(text, "• "))
				}
				if isListItem {
					indentStr := strings.Repeat("  ", listIndent)
					return fmt.Sprintf("%s%s%s", indentStr, listPrefix, text)
				}
				if isBlockquote {
					return fmt.Sprintf("> %s", text)
				}
				return text
			}
		}
	}

	return strings.TrimSpace(pBuf.String())
}

func (b *docxBodyBuilder) parsePPr(dec *xml.Decoder, start xml.StartElement, layout *document.Layout, warnings *[]error) (headingLevel int, isBlockquote bool, isListItem bool, listIndent int, listPrefix string) {
	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pStyle":
				val := strings.ToLower(getAttr(t, "val"))
				if strings.HasPrefix(val, "heading") {
					numStr := strings.TrimSpace(strings.TrimPrefix(val, "heading"))
					if lvl, err := strconv.Atoi(numStr); err == nil && lvl >= 1 && lvl <= 6 {
						headingLevel = lvl
					}
				}
			case "ind":
				leftStr := getAttr(t, "left")
				hangingStr := getAttr(t, "hanging")
				if hangingStr != "" {
					isListItem = true
					listPrefix = "- "
				} else if left, err := strconv.Atoi(leftStr); err == nil && left >= 720 {
					isBlockquote = true
				}
			case "numPr":
				isListItem = true
				listPrefix = "- "
				for {
					ntok, _ := dec.Token()
					if ntok == nil {
						break
					}
					if se, ok := ntok.(xml.StartElement); ok {
						if se.Name.Local == "ilvl" {
							lvlStr := getAttr(se, "val")
							if l, err := strconv.Atoi(lvlStr); err == nil {
								listIndent = l
							}
						}
					} else if ee, ok := ntok.(xml.EndElement); ok && ee.Name.Local == "numPr" {
						break
					}
				}
			case "sectPr":
				// Inline section break
				cols := 1
				for {
					stok, _ := dec.Token()
					if stok == nil {
						break
					}
					if se, ok := stok.(xml.StartElement); ok && se.Name.Local == "cols" {
						numStr := getAttr(se, "num")
						if n, err := strconv.Atoi(numStr); err == nil {
							cols = n
						}
					} else if ee, ok := stok.(xml.EndElement); ok && ee.Name.Local == "sectPr" {
						break
					}
				}
				clampedCols, w := MapColumns(cols)
				if w != nil {
					*warnings = append(*warnings, w)
				}
				if b.body.Len() > 0 {
					b.body.WriteString("\n\n")
				}
				b.body.WriteString(fmt.Sprintf("<!-- parchment:section-break columns=%d continuous -->", clampedCols))
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return
			}
		}
	}
	return
}

func (b *docxBodyBuilder) parseRun(dec *xml.Decoder, start xml.StartElement, buf *strings.Builder, warnings *[]error) {
	bold := false
	italic := false
	strike := false
	code := false
	var textBuf strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "b":
				bold = true
			case "i":
				italic = true
			case "strike":
				strike = true
			case "rFonts":
				font := strings.ToLower(getAttr(t, "ascii"))
				if strings.Contains(font, "courier") || strings.Contains(font, "consolas") || strings.Contains(font, "mono") {
					code = true
				}
			case "t":
				for {
					contentTok, _ := dec.Token()
					if contentTok == nil {
						break
					}
					if ch, ok := contentTok.(xml.CharData); ok {
						textBuf.Write(ch)
					} else if ee, ok := contentTok.(xml.EndElement); ok && ee.Name.Local == "t" {
						break
					}
				}
			case "br":
				brType := getAttr(t, "type")
				if brType == "page" {
					if buf.Len() > 0 {
						buf.WriteString("\n\n")
					}
					buf.WriteString("<!-- parchment:page-break -->\n\n")
				} else {
					textBuf.WriteByte('\n')
				}
			case "drawing":
				b.parseDrawing(dec, buf, warnings)
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				raw := textBuf.String()
				if raw == "" {
					return
				}
				// Format inline text
				formatted := raw
				if code {
					formatted = fmt.Sprintf("`%s`", formatted)
				}
				if strike {
					formatted = fmt.Sprintf("~~%s~~", formatted)
				}
				if bold && italic {
					formatted = fmt.Sprintf("***%s***", formatted)
				} else if bold {
					formatted = fmt.Sprintf("**%s**", formatted)
				} else if italic {
					formatted = fmt.Sprintf("*%s*", formatted)
				}
				buf.WriteString(formatted)
				return
			}
		}
	}
}

func (b *docxBodyBuilder) parseDrawing(dec *xml.Decoder, buf *strings.Builder, warnings *[]error) {
	var blipEmbed string
	alt := "image"

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "docPr" {
				if d := getAttr(t, "descr"); d != "" {
					alt = d
				} else if n := getAttr(t, "name"); n != "" {
					alt = n
				}
			} else if t.Name.Local == "blip" {
				for _, attr := range t.Attr {
					if attr.Name.Local == "embed" {
						blipEmbed = attr.Value
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "drawing" {
				if blipEmbed != "" {
					target, ok := b.rels[blipEmbed]
					if ok {
						targetPath := path.Clean(path.Join("word", target))
						if zf, found := b.zipFiles[targetPath]; found {
							b.importImageFile(zf, alt, buf, warnings)
						} else {
							*warnings = append(*warnings, &Warning{Message: fmt.Sprintf("referenced image file %s missing", target)})
							buf.WriteString(fmt.Sprintf("[Image: %s]", alt))
						}
					}
				}
				return
			}
		}
	}
}

func (b *docxBodyBuilder) importImageFile(zf *zip.File, alt string, buf *strings.Builder, warnings *[]error) {
	if zf.UncompressedSize64 > artifact.MaxImageBytes {
		*warnings = append(*warnings, &Warning{Message: fmt.Sprintf("image %s exceeds 10 MiB limit, skipped", zf.Name)})
		buf.WriteString(fmt.Sprintf("[Image: %s]", alt))
		return
	}

	rc, err := zf.Open()
	if err != nil {
		*warnings = append(*warnings, &Warning{Message: fmt.Sprintf("cannot read image %s: %v", zf.Name, err)})
		buf.WriteString(fmt.Sprintf("[Image: %s]", alt))
		return
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		*warnings = append(*warnings, &Warning{Message: fmt.Sprintf("cannot read image %s: %v", zf.Name, err)})
		buf.WriteString(fmt.Sprintf("[Image: %s]", alt))
		return
	}

	img, err := artifact.NewImage(data)
	if err != nil {
		*warnings = append(*warnings, &Warning{Message: fmt.Sprintf("unsupported image %s (%v), skipped", zf.Name, err)})
		buf.WriteString(fmt.Sprintf("[Image: %s]", alt))
		return
	}

	// Add to images list if not already present
	exists := false
	for _, existing := range b.images {
		if existing.Name == img.Name {
			exists = true
			break
		}
	}
	if !exists {
		b.images = append(b.images, img)
	}

	buf.WriteString(fmt.Sprintf("![%s](%s)", alt, img.Name))
}

func (b *docxBodyBuilder) parseHyperlink(dec *xml.Decoder, start xml.StartElement, buf *strings.Builder, warnings *[]error) {
	relID := ""
	anchor := ""
	for _, attr := range start.Attr {
		if attr.Name.Local == "id" {
			relID = attr.Value
		} else if attr.Name.Local == "anchor" {
			anchor = attr.Value
		}
	}

	target := ""
	if relID != "" {
		if t, ok := b.rels[relID]; ok {
			target = t
		}
	} else if anchor != "" {
		target = "#" + anchor
	}

	var linkText strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "r" {
				b.parseRun(dec, t, &linkText, warnings)
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				lt := linkText.String()
				if target != "" {
					buf.WriteString(fmt.Sprintf("[%s](%s)", lt, target))
				} else {
					buf.WriteString(lt)
				}
				return
			}
		}
	}
}

func (b *docxBodyBuilder) parseTable(dec *xml.Decoder, warnings *[]error) string {
	var rows [][]string

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "tr" {
				isHeader := (len(rows) == 0)
				row := b.parseTableRow(dec, isHeader, warnings)
				if len(row) > 0 {
					rows = append(rows, row)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "tbl" {
				if len(rows) == 0 {
					return ""
				}
				var tblBuf strings.Builder
				colCount := 0
				for _, r := range rows {
					if len(r) > colCount {
						colCount = len(r)
					}
				}
				if colCount == 0 {
					return ""
				}

				// Header row
				header := rows[0]
				tblBuf.WriteString("|")
				for c := 0; c < colCount; c++ {
					val := ""
					if c < len(header) {
						val = escapeTableCell(header[c])
					}
					tblBuf.WriteString(" ")
					tblBuf.WriteString(val)
					tblBuf.WriteString(" |")
				}
				tblBuf.WriteString("\n|")
				for c := 0; c < colCount; c++ {
					tblBuf.WriteString("---|")
				}

				// Data rows
				for r := 1; r < len(rows); r++ {
					row := rows[r]
					tblBuf.WriteString("\n|")
					for c := 0; c < colCount; c++ {
						val := ""
						if c < len(row) {
							val = escapeTableCell(row[c])
						}
						tblBuf.WriteString(" ")
						tblBuf.WriteString(val)
						tblBuf.WriteString(" |")
					}
				}

				return tblBuf.String()
			}
		}
	}
	return ""
}

func (b *docxBodyBuilder) parseTableRow(dec *xml.Decoder, isHeader bool, warnings *[]error) []string {
	var cells []string

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "tc" {
				cellText := b.parseTableCell(dec, isHeader, warnings)
				cells = append(cells, cellText)
			}
		case xml.EndElement:
			if t.Name.Local == "tr" {
				return cells
			}
		}
	}
	return cells
}

func (b *docxBodyBuilder) parseTableCell(dec *xml.Decoder, isHeader bool, warnings *[]error) string {
	var cellBuf strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "p" {
				var pBuf strings.Builder
				for {
					ptok, _ := dec.Token()
					if ptok == nil {
						break
					}
					if se, ok := ptok.(xml.StartElement); ok && se.Name.Local == "r" {
						b.parseRun(dec, se, &pBuf, warnings)
					} else if ee, ok := ptok.(xml.EndElement); ok && ee.Name.Local == "p" {
						break
					}
				}
				if pBuf.Len() > 0 {
					if cellBuf.Len() > 0 {
						cellBuf.WriteString(" ")
					}
					text := pBuf.String()
					if isHeader {
						text = strings.ReplaceAll(text, "**", "")
					}
					cellBuf.WriteString(text)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "tc" {
				return cellBuf.String()
			}
		}
	}
	return cellBuf.String()
}

func (b *docxBodyBuilder) parseSectPr(dec *xml.Decoder, start xml.StartElement, layout *document.Layout, warnings *[]error) {
	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pgSz":
				wTwip, _ := strconv.Atoi(getAttr(t, "w"))
				hTwip, _ := strconv.Atoi(getAttr(t, "h"))
				if wTwip > 0 && hTwip > 0 {
					wMM := TwipToMM(wTwip)
					hMM := TwipToMM(hTwip)
					sz, or, w := MapPageSize(wMM, hMM)
					if w != nil {
						*warnings = append(*warnings, w)
					}
					layout.PageSize = sz
					layout.Orientation = or
				}
			case "pgMar":
				topTwip, _ := strconv.Atoi(getAttr(t, "top"))
				rightTwip, _ := strconv.Atoi(getAttr(t, "right"))
				bottomTwip, _ := strconv.Atoi(getAttr(t, "bottom"))
				leftTwip, _ := strconv.Atoi(getAttr(t, "left"))
				margins, w := MapMargins(TwipToMM(topTwip), TwipToMM(rightTwip), TwipToMM(bottomTwip), TwipToMM(leftTwip))
				if w != nil {
					*warnings = append(*warnings, w)
				}
				layout.Margins = margins
			case "cols":
				numStr := getAttr(t, "num")
				if num, err := strconv.Atoi(numStr); err == nil {
					cols, w := MapColumns(num)
					if w != nil {
						*warnings = append(*warnings, w)
					}
					layout.Columns = cols
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return
			}
		}
	}
}

func escapeTableCell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "|", `\|`)
}

func parseRels(f *zip.File, rels, relTypes map[string]string) {
	rc, err := f.Open()
	if err != nil {
		return
	}
	defer rc.Close()

	dec := NewXMLDecoder(rc)
	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
			id := getAttr(se, "Id")
			target := getAttr(se, "Target")
			relType := getAttr(se, "Type")
			if id != "" && target != "" {
				rels[id] = target
				relTypes[id] = relType
			}
		}
	}
}

func parseDefaultFont(f *zip.File) string {
	rc, err := f.Open()
	if err != nil {
		return ""
	}
	defer rc.Close()

	dec := NewXMLDecoder(rc)
	inDocDefaults := false
	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok {
			if se.Name.Local == "docDefaults" {
				inDocDefaults = true
			} else if inDocDefaults && se.Name.Local == "rFonts" {
				if font := getAttr(se, "ascii"); font != "" {
					return font
				}
			}
		} else if ee, ok := tok.(xml.EndElement); ok && ee.Name.Local == "docDefaults" {
			inDocDefaults = false
		}
	}
	return ""
}

func parseRunningXML(f *zip.File) (string, bool) {
	rc, err := f.Open()
	if err != nil {
		return "", false
	}
	defer rc.Close()

	dec := NewXMLDecoder(rc)
	var cells []string
	isComplex := false

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok {
			if se.Name.Local == "tc" {
				// Parse cell text
				var cellBuf strings.Builder
				for {
					ctok, _ := dec.Token()
					if ctok == nil {
						break
					}
					if cse, ok := ctok.(xml.StartElement); ok {
						if cse.Name.Local == "t" {
							for {
								ttok, _ := dec.Token()
								if ttok == nil {
									break
								}
								if cd, ok := ttok.(xml.CharData); ok {
									cellBuf.Write(cd)
								} else if cee, ok := ttok.(xml.EndElement); ok && cee.Name.Local == "t" {
									break
								}
							}
						} else if cse.Name.Local == "fldSimple" {
							instr := strings.TrimSpace(getAttr(cse, "instr"))
							if strings.EqualFold(instr, "PAGE") {
								cellBuf.WriteString("{page}")
							} else if strings.EqualFold(instr, "NUMPAGES") {
								cellBuf.WriteString("{pages}")
							} else {
								isComplex = true
							}
						}
					} else if cee, ok := ctok.(xml.EndElement); ok && cee.Name.Local == "tc" {
						break
					}
				}
				cells = append(cells, strings.TrimSpace(cellBuf.String()))
			}
		}
	}

	if len(cells) == 3 {
		return fmt.Sprintf("%s | %s | %s", cells[0], cells[1], cells[2]), isComplex
	} else if len(cells) > 0 {
		return strings.Join(cells, " | "), isComplex
	}
	return "", isComplex
}

func getAttr(se xml.StartElement, localName string) string {
	for _, a := range se.Attr {
		if a.Name.Local == localName {
			return a.Value
		}
	}
	return ""
}
