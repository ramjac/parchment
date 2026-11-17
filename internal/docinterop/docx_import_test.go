package docinterop

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

func TestDocxImportRoundTrip(t *testing.T) {
	pngData := createTestPNG(t)
	testImg, err := artifact.NewImage(pngData)
	if err != nil {
		t.Fatal(err)
	}

	body := `# Document Title

This is a paragraph with **bold**, *italic*, and [link](https://example.com).

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

<!-- parchment:page-break -->

Page 2 text.

<!-- parchment:section-break columns=2 continuous -->

Section text.`

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
	if len(warnings) > 0 {
		t.Logf("Export warnings: %v", warnings)
	}

	importer := NewDocxImporter()
	data := buf.Bytes()
	draft, importWarns, err := importer.Import(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}
	if len(importWarns) > 0 {
		t.Logf("Import warnings: %v", importWarns)
	}

	// Verify imported layout
	if draft.Layout.PageSize != document.PageLetter {
		t.Errorf("Expected Letter page size, got %s", draft.Layout.PageSize)
	}
	if draft.Layout.Orientation != document.Portrait {
		t.Errorf("Expected Portrait orientation, got %s", draft.Layout.Orientation)
	}
	if draft.Layout.Font != "times" {
		t.Errorf("Expected times font, got %s", draft.Layout.Font)
	}
	if draft.Layout.Margins.Top != 20 || draft.Layout.Margins.Left != 20 {
		t.Errorf("Margins not preserved: %+v", draft.Layout.Margins)
	}

	// Verify body elements
	if !strings.Contains(draft.Body, "# Document Title") {
		t.Errorf("Missing # Document Title in imported body:\n%s", draft.Body)
	}
	if !strings.Contains(draft.Body, "**bold**") || !strings.Contains(draft.Body, "*italic*") {
		t.Errorf("Missing bold/italic in imported body:\n%s", draft.Body)
	}
	if !strings.Contains(draft.Body, "[link](https://example.com)") {
		t.Errorf("Missing link in imported body:\n%s", draft.Body)
	}
	if !strings.Contains(draft.Body, "- Bullet item 1") {
		t.Errorf("Missing bullet list in imported body:\n%s", draft.Body)
	}
	if !strings.Contains(draft.Body, "| Header A | Header B |") {
		t.Errorf("Missing table in imported body:\n%s", draft.Body)
	}
	if !strings.Contains(draft.Body, "<!-- parchment:page-break -->") {
		t.Errorf("Missing page break in imported body:\n%s", draft.Body)
	}
	if !strings.Contains(draft.Body, "<!-- parchment:section-break columns=2 continuous -->") {
		t.Errorf("Missing section break in imported body:\n%s", draft.Body)
	}

	// Verify imported images
	if len(draft.Images) != 1 {
		t.Fatalf("Expected 1 imported image, got %d", len(draft.Images))
	}
	if draft.Images[0].Name != testImg.Name {
		t.Errorf("Image name mismatch: got %s, want %s", draft.Images[0].Name, testImg.Name)
	}
	if !bytes.Equal(draft.Images[0].Data, testImg.Data) {
		t.Errorf("Image data mismatch")
	}
}

func TestDocxImportInvalidArchive(t *testing.T) {
	importer := NewDocxImporter()
	// Empty data
	_, _, err := importer.Import(bytes.NewReader([]byte("not-a-zip")), 9)
	if err == nil {
		t.Fatal("expected error on invalid zip data")
	}

	// Valid zip without [Content_Types].xml
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("hello.txt")
	w.Write([]byte("hello"))
	zw.Close()

	data := buf.Bytes()
	_, _, err = importer.Import(bytes.NewReader(data), int64(len(data)))
	if err == nil || !strings.Contains(err.Error(), "not a valid DOCX") {
		t.Fatalf("expected not valid DOCX error, got: %v", err)
	}
}
