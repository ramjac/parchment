package docinterop

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
)

func TestOdtImportRoundTrip(t *testing.T) {
	pngData := createTestPNG(t)
	testImg, err := artifact.NewImage(pngData)
	if err != nil {
		t.Fatal(err)
	}

	body := `# ODF Document Title

This is a paragraph with **bold**, *italic*, and [link](https://example.com).

## ODF Section 1

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
			Title: "Test ODF Document",
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
		t.Fatalf("ODT export failed: %v", err)
	}
	if len(warnings) > 0 {
		t.Logf("Export warnings: %v", warnings)
	}

	importer := NewOdtImporter()
	data := buf.Bytes()
	draft, importWarns, err := importer.Import(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("ODT import failed: %v", err)
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

	// Verify body elements
	if !strings.Contains(draft.Body, "# ODF Document Title") {
		t.Errorf("Missing # ODF Document Title in imported body:\n%s", draft.Body)
	}
	if !strings.Contains(draft.Body, "**bold**") || !strings.Contains(draft.Body, "*italic*") {
		t.Errorf("Missing bold/italic in imported body:\n%s", draft.Body)
	}
	if !strings.Contains(draft.Body, "[link](https://example.com)") {
		t.Errorf("Missing link in imported body:\n%s", draft.Body)
	}
	if !strings.Contains(draft.Body, "- Bullet item 1") {
		t.Errorf("Missing list in imported body:\n%s", draft.Body)
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
}

func TestCrossFormatRoundTrip(t *testing.T) {
	// .md -> .docx -> .md -> .odt -> .md
	initialMD := "# Cross Format Test\n\nTesting **bold** and *italic* formatting.\n\n- Point 1\n- Point 2\n\n| Col A | Col B |\n|---|---|\n| 1 | 2 |\n"
	initialDoc := document.Document{
		Artifact: artifact.Artifact{Title: "Cross Format"},
		Body:     initialMD,
		Layout:   document.DefaultLayout(),
	}

	// 1. Export to DOCX
	var docxBuf bytes.Buffer
	docxExp := NewDocxExporter()
	if _, err := docxExp.Export(&docxBuf, initialDoc); err != nil {
		t.Fatalf("Export to DOCX failed: %v", err)
	}

	// 2. Import from DOCX
	docxImp := NewDocxImporter()
	docxData := docxBuf.Bytes()
	draftFromDocx, _, err := docxImp.Import(bytes.NewReader(docxData), int64(len(docxData)))
	if err != nil {
		t.Fatalf("Import from DOCX failed: %v", err)
	}

	docFromDocx := document.Document{
		Artifact: artifact.Artifact{Title: "Cross Format 2"},
		Body:     draftFromDocx.Body,
		Layout:   draftFromDocx.Layout,
	}

	// 3. Export to ODT
	var odtBuf bytes.Buffer
	odtExp := NewOdtExporter()
	if _, err := odtExp.Export(&odtBuf, docFromDocx); err != nil {
		t.Fatalf("Export to ODT failed: %v", err)
	}

	// 4. Import from ODT
	odtImp := NewOdtImporter()
	odtData := odtBuf.Bytes()
	finalDraft, _, err := odtImp.Import(bytes.NewReader(odtData), int64(len(odtData)))
	if err != nil {
		t.Fatalf("Import from ODT failed: %v", err)
	}

	// Check final content preserves structure
	if !strings.Contains(finalDraft.Body, "# Cross Format Test") {
		t.Errorf("Cross format lost heading:\n%s", finalDraft.Body)
	}
	if !strings.Contains(finalDraft.Body, "**bold**") || !strings.Contains(finalDraft.Body, "*italic*") {
		t.Errorf("Cross format lost formatting:\n%s", finalDraft.Body)
	}
	if !strings.Contains(finalDraft.Body, "- Point 1") {
		t.Errorf("Cross format lost list:\n%s", finalDraft.Body)
	}
	if !strings.Contains(finalDraft.Body, "| Col A | Col B |") {
		t.Errorf("Cross format lost table:\n%s", finalDraft.Body)
	}
}

func TestOdtImportInvalidArchive(t *testing.T) {
	importer := NewOdtImporter()
	// Invalid zip
	_, _, err := importer.Import(bytes.NewReader([]byte("invalid")), 7)
	if err == nil {
		t.Fatal("expected error for invalid zip")
	}

	// Zip without mimetype
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("hello.txt")
	w.Write([]byte("hello"))
	zw.Close()

	data := buf.Bytes()
	_, _, err = importer.Import(bytes.NewReader(data), int64(len(data)))
	if err == nil || !strings.Contains(err.Error(), "not a valid ODF") {
		t.Fatalf("expected not a valid ODF error, got: %v", err)
	}
}
