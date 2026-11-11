package docinterop

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

func TestOdtExportComprehensive(t *testing.T) {
	pngData := createTestPNG(t)
	testImg, err := artifact.NewImage(pngData)
	if err != nil {
		t.Fatal(err)
	}

	body := `# Document Title

This is a paragraph with **bold**, *italic*, ~~strikethrough~~, ` + "`code`" + `, and [link](https://example.com).

<a id="explicit-anchor"></a>

## Section 1

Here is an image:
![Test Alt](` + testImg.Name + `)

### Lists

- Bullet item 1
- Bullet item 2

### Table

| Header A | Header B |
|---|---|
| Val 1 | Val 2 |

### Code Block

` + "```go\nfunc main() {}\n```" + `

---

<!-- parchment:page-break -->

Page 2 text.

<!-- parchment:section-break columns=2 continuous -->

Section text.
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
	exporter := NewOdtExporter()
	warnings, err := exporter.Export(&buf, doc)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	if len(warnings) > 0 {
		t.Logf("Export warnings: %v", warnings)
	}

	// Verify zip contents
	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("Failed to open exported zip: %v", err)
	}

	if len(zipReader.File) == 0 {
		t.Fatal("empty zip archive")
	}

	// Check mimetype is first and uncompressed (zip.Store)
	first := zipReader.File[0]
	if first.Name != "mimetype" {
		t.Errorf("Expected first entry to be mimetype, got %s", first.Name)
	}
	if first.Method != zip.Store {
		t.Errorf("Expected mimetype to be uncompressed (zip.Store), got method %d", first.Method)
	}

	entries := make(map[string]string)
	for _, f := range zipReader.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("Failed to open entry %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("Failed to read entry %s: %v", f.Name, err)
		}
		entries[f.Name] = string(data)
	}

	if entries["mimetype"] != "application/vnd.oasis.opendocument.text" {
		t.Errorf("Unexpected mimetype content: %s", entries["mimetype"])
	}

	manifest := entries["META-INF/manifest.xml"]
	if !strings.Contains(manifest, "Pictures/image1.png") {
		t.Errorf("Expected picture in manifest, got: %s", manifest)
	}

	styles := entries["styles.xml"]
	if !strings.Contains(styles, "Times New Roman") {
		t.Errorf("Expected Times New Roman in styles.xml, got: %s", styles)
	}
	if !strings.Contains(styles, "text:page-number") {
		t.Errorf("Expected page-number field in styles footer, got: %s", styles)
	}

	content := entries["content.xml"]
	if !strings.Contains(content, "Document Title") {
		t.Errorf("Expected Document Title in content.xml")
	}
	if !strings.Contains(content, "text:bookmark") {
		t.Errorf("Expected bookmark in content.xml")
	}
	if !strings.Contains(content, "draw:image") {
		t.Errorf("Expected draw:image in content.xml")
	}
	if !strings.Contains(content, "table:table") {
		t.Errorf("Expected table:table in content.xml")
	}
	if !strings.Contains(content, "SectionCols2") {
		t.Errorf("Expected 2-column section in content.xml")
	}
}
