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

// OdtImporter imports an OpenDocument Text (.odt) archive into a Parchment Document draft.
type OdtImporter struct{}

// NewOdtImporter returns a new OdtImporter instance.
func NewOdtImporter() *OdtImporter {
	return &OdtImporter{}
}

// SniffODT checks if the archive contains the OpenDocument Text mimetype.
func SniffODT(zr *zip.Reader) bool {
	for _, f := range zr.File {
		if f.Name == "mimetype" {
			rc, err := f.Open()
			if err != nil {
				return false
			}
			data, _ := io.ReadAll(rc)
			rc.Close()
			return strings.TrimSpace(string(data)) == "application/vnd.oasis.opendocument.text"
		}
	}
	return false
}

// Import reads an ODT archive and converts it to a Parchment document Draft.
func (imp *OdtImporter) Import(r io.ReaderAt, size int64) (document.Draft, []error, error) {
	var warnings []error

	zr, err := ReadZip(r, size)
	if err != nil {
		return document.Draft{}, nil, err
	}

	if !SniffODT(zr) {
		return document.Draft{}, nil, errors.New("not a valid ODF archive: missing or incorrect mimetype")
	}

	zipFiles := make(map[string]*zip.File)
	for _, f := range zr.File {
		cleanName := path.Clean(f.Name)
		zipFiles[cleanName] = f
		zipFiles[f.Name] = f
	}

	layout := document.DefaultLayout()

	// 1. Parse styles.xml
	if stylesFile, ok := zipFiles["styles.xml"]; ok {
		if err := parseOdtStyles(stylesFile, &layout, &warnings); err != nil {
			return document.Draft{}, warnings, fmt.Errorf("parse styles.xml: %w", err)
		}
	}

	// 2. Parse content.xml
	contentFile, ok := zipFiles["content.xml"]
	if !ok {
		return document.Draft{}, warnings, errors.New("missing content.xml in ODF archive")
	}

	contentRC, err := contentFile.Open()
	if err != nil {
		return document.Draft{}, warnings, fmt.Errorf("open content.xml: %w", err)
	}
	defer contentRC.Close()

	builder := &odtBodyBuilder{
		zipFiles: zipFiles,
	}

	dec := NewXMLDecoder(contentRC)
	if err := builder.parse(dec, &warnings); err != nil {
		return document.Draft{}, warnings, fmt.Errorf("parse content.xml: %w", err)
	}

	draft := document.Draft{
		Body:   strings.TrimSpace(builder.body.String()),
		Layout: layout,
		Images: builder.images,
	}

	return draft, warnings, nil
}

func parseOdtStyles(f *zip.File, layout *document.Layout, warnings *[]error) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	dec := NewXMLDecoder(rc)
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
			switch t.Name.Local {
			case "font-face":
				name := getAttr(t, "name")
				if name != "" && layout.Font == "courier" {
					layout.Font = MapFontFamily(name)
				}
			case "page-layout-properties":
				wMM := parseDimMM(getAttr(t, "page-width"))
				hMM := parseDimMM(getAttr(t, "page-height"))
				if wMM > 0 && hMM > 0 {
					sz, orient, w := MapPageSize(wMM, hMM)
					if w != nil {
						*warnings = append(*warnings, w)
					}
					layout.PageSize = sz
					layout.Orientation = orient
				}
				topMM := parseDimMM(getAttr(t, "margin-top"))
				rightMM := parseDimMM(getAttr(t, "margin-right"))
				bottomMM := parseDimMM(getAttr(t, "margin-bottom"))
				leftMM := parseDimMM(getAttr(t, "margin-left"))
				margins, w := MapMargins(topMM, rightMM, bottomMM, leftMM)
				if w != nil {
					*warnings = append(*warnings, w)
				}
				layout.Margins = margins
			case "columns":
				cntStr := getAttr(t, "column-count")
				if cnt, err := strconv.Atoi(cntStr); err == nil && cnt > 0 {
					cols, w := MapColumns(cnt)
					if w != nil {
						*warnings = append(*warnings, w)
					}
					layout.Columns = cols
				}
			case "header":
				hdrText, isComplex := parseOdtRunningElem(dec, "header")
				mapped, w := MapHeaderFooter(hdrText, isComplex)
				if w != nil {
					*warnings = append(*warnings, w)
				}
				layout.Header = mapped
			case "footer":
				ftrText, isComplex := parseOdtRunningElem(dec, "footer")
				mapped, w := MapHeaderFooter(ftrText, isComplex)
				if w != nil {
					*warnings = append(*warnings, w)
				}
				layout.Footer = mapped
			}
		}
	}
	return nil
}

func parseOdtRunningElem(dec *xml.Decoder, tag string) (string, bool) {
	var b strings.Builder
	isComplex := false

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "page-number":
				b.WriteString("{page}")
			case "page-count":
				b.WriteString("{pages}")
			case "title":
				b.WriteString("{title}")
			}
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			if t.Name.Local == tag {
				return strings.TrimSpace(b.String()), isComplex
			}
		}
	}
	return strings.TrimSpace(b.String()), isComplex
}

func parseDimMM(dim string) float64 {
	dim = strings.TrimSpace(dim)
	if strings.HasSuffix(dim, "mm") {
		val, _ := strconv.ParseFloat(strings.TrimSuffix(dim, "mm"), 64)
		return val
	}
	if strings.HasSuffix(dim, "cm") {
		val, _ := strconv.ParseFloat(strings.TrimSuffix(dim, "cm"), 64)
		return val * 10.0
	}
	if strings.HasSuffix(dim, "in") || strings.HasSuffix(dim, "inch") {
		numStr := strings.TrimSuffix(strings.TrimSuffix(dim, "inch"), "in")
		val, _ := strconv.ParseFloat(numStr, 64)
		return val * 25.4
	}
	if strings.HasSuffix(dim, "pt") {
		val, _ := strconv.ParseFloat(strings.TrimSuffix(dim, "pt"), 64)
		return val * 25.4 / 72.0
	}
	val, _ := strconv.ParseFloat(dim, 64)
	return val
}

type odtBodyBuilder struct {
	zipFiles map[string]*zip.File
	body     strings.Builder
	images   []artifact.Image
	styles   map[string]string // styleName -> classification: "Bold", "Italic", "Code", "Blockquote", "PageBreak", "SectionColsN"
}

func (b *odtBodyBuilder) parse(dec *xml.Decoder, warnings *[]error) error {
	b.styles = make(map[string]string)
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
			switch t.Name.Local {
			case "style":
				name := getAttr(t, "name")
				family := getAttr(t, "family")
				b.parseStyleDef(dec, name, family)
			case "body":
				inBody = true
			case "h":
				if inBody {
					lvlStr := getAttr(t, "outline-level")
					lvl, _ := strconv.Atoi(lvlStr)
					if lvl < 1 {
						lvl = 1
					} else if lvl > 6 {
						lvl = 6
					}
					hText := b.parseParagraphOrHeading(dec, "h", warnings)
					if hText != "" {
						if b.body.Len() > 0 {
							b.body.WriteString("\n\n")
						}
						b.body.WriteString(fmt.Sprintf("%s %s", strings.Repeat("#", lvl), hText))
					}
				}
			case "p":
				if inBody {
					styleName := getAttr(t, "style-name")
					if b.styles[styleName] == "PageBreak" {
						if b.body.Len() > 0 {
							b.body.WriteString("\n\n")
						}
						b.body.WriteString("<!-- parchment:page-break -->")
					} else {
						pText := b.parseParagraphOrHeading(dec, "p", warnings)
						if pText != "" {
							if b.styles[styleName] == "Blockquote" {
								pText = "> " + pText
							}
							if b.body.Len() > 0 {
								b.body.WriteString("\n\n")
							}
							b.body.WriteString(pText)
						}
					}
				}
			case "list":
				if inBody {
					lText := b.parseList(dec, 0, warnings)
					if lText != "" {
						if b.body.Len() > 0 {
							b.body.WriteString("\n\n")
						}
						b.body.WriteString(lText)
					}
				}
			case "table":
				if inBody {
					tblText := b.parseTable(dec, warnings)
					if tblText != "" {
						if b.body.Len() > 0 {
							b.body.WriteString("\n\n")
						}
						b.body.WriteString(tblText)
					}
				}
			case "section":
				if inBody {
					styleName := getAttr(t, "style-name")
					cols := 1
					if strings.HasPrefix(b.styles[styleName], "SectionCols") {
						numStr := strings.TrimPrefix(b.styles[styleName], "SectionCols")
						cols, _ = strconv.Atoi(numStr)
					}
					if cols > 1 {
						if b.body.Len() > 0 {
							b.body.WriteString("\n\n")
						}
						b.body.WriteString(fmt.Sprintf("<!-- parchment:section-break columns=%d continuous -->", cols))
					}
				}
			}
		}
	}
	return nil
}

func (b *odtBodyBuilder) parseStyleDef(dec *xml.Decoder, name, family string) {
	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "text-properties":
				weight := getAttr(t, "font-weight")
				fontStyle := getAttr(t, "font-style")
				strike := getAttr(t, "text-line-through-style")
				fontName := strings.ToLower(getAttr(t, "font-name"))
				if weight == "bold" && fontStyle == "italic" {
					b.styles[name] = "BoldItalic"
				} else if weight == "bold" {
					b.styles[name] = "Bold"
				} else if fontStyle == "italic" {
					b.styles[name] = "Italic"
				} else if strike != "" && strike != "none" {
					b.styles[name] = "Strike"
				} else if strings.Contains(fontName, "courier") || strings.Contains(fontName, "mono") {
					b.styles[name] = "Code"
				}
			case "paragraph-properties":
				marginL := parseDimMM(getAttr(t, "margin-left"))
				breakBefore := getAttr(t, "break-before")
				if breakBefore == "page" {
					b.styles[name] = "PageBreak"
				} else if marginL >= 10.0 {
					b.styles[name] = "Blockquote"
				}
			case "columns":
				cntStr := getAttr(t, "column-count")
				if cnt, err := strconv.Atoi(cntStr); err == nil && cnt > 1 {
					b.styles[name] = fmt.Sprintf("SectionCols%d", cnt)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "style" {
				return
			}
		}
	}
}

func (b *odtBodyBuilder) parseParagraphOrHeading(dec *xml.Decoder, endTag string, warnings *[]error) string {
	var buf strings.Builder

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "span":
				styleName := getAttr(t, "style-name")
				classification := b.styles[styleName]
				var spanBuf strings.Builder
				b.parseSpan(dec, &spanBuf, warnings)
				txt := spanBuf.String()
				switch classification {
				case "Bold":
					buf.WriteString(fmt.Sprintf("**%s**", txt))
				case "Italic":
					buf.WriteString(fmt.Sprintf("*%s*", txt))
				case "BoldItalic":
					buf.WriteString(fmt.Sprintf("***%s***", txt))
				case "Strike":
					buf.WriteString(fmt.Sprintf("~~%s~~", txt))
				case "Code":
					buf.WriteString(fmt.Sprintf("`%s`", txt))
				default:
					buf.WriteString(txt)
				}
			case "a":
				href := getAttr(t, "href")
				var linkBuf strings.Builder
				b.parseSpan(dec, &linkBuf, warnings)
				buf.WriteString(fmt.Sprintf("[%s](%s)", linkBuf.String(), href))
			case "bookmark":
				name := getAttr(t, "name")
				if name != "" && !strings.HasPrefix(name, "_") && endTag != "h" {
					buf.WriteString(fmt.Sprintf("<a id=\"%s\"></a>", name))
				}
			case "line-break":
				buf.WriteString("\n")
			case "frame":
				b.parseFrame(dec, &buf, warnings)
			}
		case xml.CharData:
			buf.Write(t)
		case xml.EndElement:
			if t.Name.Local == endTag {
				return strings.TrimSpace(buf.String())
			}
		}
	}
	return strings.TrimSpace(buf.String())
}

func (b *odtBodyBuilder) parseSpan(dec *xml.Decoder, buf *strings.Builder, warnings *[]error) {
	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "span" {
				b.parseSpan(dec, buf, warnings)
			} else if t.Name.Local == "line-break" {
				buf.WriteString("\n")
			}
		case xml.CharData:
			buf.Write(t)
		case xml.EndElement:
			if t.Name.Local == "span" || t.Name.Local == "a" {
				return
			}
		}
	}
}

func (b *odtBodyBuilder) parseFrame(dec *xml.Decoder, buf *strings.Builder, warnings *[]error) {
	alt := "image"
	var imgHref string

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "image":
				imgHref = getAttr(t, "href")
			case "desc":
				for {
					dtok, _ := dec.Token()
					if dtok == nil {
						break
					}
					if cd, ok := dtok.(xml.CharData); ok {
						alt = string(cd)
					} else if ee, ok := dtok.(xml.EndElement); ok && ee.Name.Local == "desc" {
						break
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "frame" {
				if imgHref != "" {
					cleanPath := path.Clean(imgHref)
					if zf, ok := b.zipFiles[cleanPath]; ok {
						b.importImage(zf, alt, buf, warnings)
					} else {
						*warnings = append(*warnings, &Warning{Message: fmt.Sprintf("referenced image %s missing", imgHref)})
						buf.WriteString(fmt.Sprintf("[Image: %s]", alt))
					}
				}
				return
			}
		}
	}
}

func (b *odtBodyBuilder) importImage(zf *zip.File, alt string, buf *strings.Builder, warnings *[]error) {
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

func (b *odtBodyBuilder) parseList(dec *xml.Decoder, level int, warnings *[]error) string {
	var lines []string

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "list-item" {
				var itemBuf strings.Builder
				for {
					itok, _ := dec.Token()
					if itok == nil {
						break
					}
					if se, ok := itok.(xml.StartElement); ok {
						if se.Name.Local == "p" {
							pText := b.parseParagraphOrHeading(dec, "p", warnings)
							itemBuf.WriteString(pText)
						} else if se.Name.Local == "list" {
							subList := b.parseList(dec, level+1, warnings)
							if subList != "" {
								itemBuf.WriteString("\n" + subList)
							}
						}
					} else if ee, ok := itok.(xml.EndElement); ok && ee.Name.Local == "list-item" {
						break
					}
				}
				itemText := itemBuf.String()
				if itemText != "" {
					indentStr := strings.Repeat("  ", level)
					lines = append(lines, fmt.Sprintf("%s- %s", indentStr, itemText))
				}
			}
		case xml.EndElement:
			if t.Name.Local == "list" {
				return strings.Join(lines, "\n")
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (b *odtBodyBuilder) parseTable(dec *xml.Decoder, warnings *[]error) string {
	var rows [][]string

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "table-row" {
				isHeader := len(rows) == 0
				row := b.parseTableRow(dec, isHeader, warnings)
				if len(row) > 0 {
					rows = append(rows, row)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "table" {
				if len(rows) == 0 {
					return ""
				}
				colCount := 0
				for _, r := range rows {
					if len(r) > colCount {
						colCount = len(r)
					}
				}
				if colCount == 0 {
					return ""
				}

				var tblBuf strings.Builder
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

func (b *odtBodyBuilder) parseTableRow(dec *xml.Decoder, isHeader bool, warnings *[]error) []string {
	var cells []string

	for {
		tok, err := dec.Token()
		if err != nil || tok == nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "table-cell" {
				cellText := b.parseParagraphOrHeading(dec, "table-cell", warnings)
				if isHeader {
					cellText = strings.ReplaceAll(cellText, "**", "")
				}
				cells = append(cells, cellText)
			}
		case xml.EndElement:
			if t.Name.Local == "table-row" {
				return cells
			}
		}
	}
	return cells
}
