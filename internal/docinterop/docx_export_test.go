package docinterop

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

func createTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDocxExportComprehensive(t *testing.T) {
	pngData := createTestPNG(t)
	testImg, err := artifact.NewImage(pngData)
	if err != nil {
		t.Fatal(err)
	}

	body := `# Document Title

This is a paragraph with **bold**, *italic*, ~~strikethrough~~, ` + "`code`" + `, and [external link](https://example.com) and [internal link](#section-1).

<a id="explicit-anchor"></a>

## Section 1

Here is an image:
![Test Alt](` + testImg.Name + `)

Here is a missing image:
![Missing Alt](image-missing.png)

### Lists

- Bullet item 1
- Bullet item 2
  - Nested bullet 2a
  - Nested bullet 2b

1. Numbered 1
2. Numbered 2

### Table

| Header A | Header B | Header C |
|:---|:---:|---:|
| Val 1 | Val 2 | Val 3 |
| Val 4 | Val 5 | Val 6 |

### Code Blocks

` + "```go\nfunc main() {\n    println(\"hello\")\n}\n```" + `

` + "```parchment-meta\n{\"kind\":\"document\"}\n```" + `

---

<!-- parchment:page-break -->

Second page text.

<!-- parchment:section-break columns=2 continuous -->

Two column section text.
`

	doc := document.Document{
		Artifact: artifact.Artifact{
			Title: "Test Document",
		},
		Body: body,
		Layout: document.Layout{
			Version:     document.LayoutVersion,
			PageSize:    document.PageLetter,
			Orientation: document.Portrait,
			Margins:     document.Margins{Top: 20, Right: 20, Bottom: 20, Left: 20},
			Columns:     1,
			Header:      "Left Header | Center Header | Right Header",
			Footer:      "Left Footer | | Page {page} of {pages}",
			PageNumbers: document.NumbersRight,
			Font:        "times",
		},
		Images: []artifact.Image{testImg},
	}

	var buf bytes.Buffer
	exporter := NewDocxExporter()
	warnings, err := exporter.Export(&buf, doc)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Should have 1 warning for the missing image
	if len(warnings) != 1 {
		t.Errorf("Expected 1 warning for missing image, got %d: %v", len(warnings), warnings)
	} else if !strings.Contains(warnings[0].Error(), "missing image") {
		t.Errorf("Unexpected warning message: %v", warnings[0])
	}

	// Verify zip contents
	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("Failed to open exported zip: %v", err)
	}

	entries := make(map[string]string)
	for _, f := range zipReader.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("Failed to open zip entry %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("Failed to read zip entry %s: %v", f.Name, err)
		}
		entries[f.Name] = string(data)
	}

	requiredEntries := []string{
		"[Content_Types].xml",
		"_rels/.rels",
		"word/document.xml",
		"word/styles.xml",
		"word/_rels/document.xml.rels",
		"word/header1.xml",
		"word/footer1.xml",
	}
	for _, req := range requiredEntries {
		if _, ok := entries[req]; !ok {
			t.Errorf("Missing required zip entry: %s", req)
		}
	}

	// Check styles has Times New Roman
	styles := entries["word/styles.xml"]
	if !strings.Contains(styles, "Times New Roman") {
		t.Errorf("Expected styles.xml to specify Times New Roman, got: %s", styles)
	}

	// Check document.xml contents
	docXML := entries["word/document.xml"]
	if !strings.Contains(docXML, "document-title") {
		t.Error("Missing Document Title slug in document.xml")
	}
	if !strings.Contains(docXML, "<w:b/>") || !strings.Contains(docXML, "<w:i/>") || !strings.Contains(docXML, "<w:strike/>") {
		t.Error("Missing inline formatting in document.xml")
	}
	if !strings.Contains(docXML, "explicit-anchor") {
		t.Error("Missing bookmark for explicit anchor in document.xml")
	}
	if !strings.Contains(docXML, "<w:drawing>") {
		t.Error("Missing image drawing in document.xml")
	}
	if !strings.Contains(docXML, "[Image: Missing Alt]") {
		t.Error("Missing fallback text for missing image in document.xml")
	}
	if !strings.Contains(docXML, "<w:tbl>") {
		t.Error("Missing table in document.xml")
	}
	if strings.Contains(docXML, "parchment-meta") {
		t.Error("parchment-meta block should be hidden")
	}
	if !strings.Contains(docXML, "println") {
		t.Error("Regular code block should be included")
	}
	if !strings.Contains(docXML, "<w:br w:type=\"page\"/>") {
		t.Error("Missing page break in document.xml")
	}
	if !strings.Contains(docXML, "<w:cols w:num=\"2\"") {
		t.Error("Missing 2-column section break in document.xml")
	}

	// Check header1.xml
	hdrXML := entries["word/header1.xml"]
	if !strings.Contains(hdrXML, "Left Header") || !strings.Contains(hdrXML, "Center Header") || !strings.Contains(hdrXML, "Right Header") {
		t.Errorf("Header text missing in header1.xml: %s", hdrXML)
	}

	// Check footer1.xml
	ftrXML := entries["word/footer1.xml"]
	if !strings.Contains(ftrXML, "PAGE") || !strings.Contains(ftrXML, "NUMPAGES") {
		t.Errorf("Page fields missing in footer1.xml: %s", ftrXML)
	}
}
