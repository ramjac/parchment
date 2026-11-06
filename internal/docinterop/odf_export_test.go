package docinterop

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"

	"example.com/parchment/internal/document"
)

func TestODFExporter(t *testing.T) {
	doc := document.Document{
		Body: "## Heading 2\n\nParagraph text\n\n- List item 1\n- List item 2\n",
	}

	var buf bytes.Buffer
	exporter := ODFExporter{}
	warnings, err := exporter.Export(&buf, doc)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	if len(warnings) != 0 {
		t.Errorf("Expected 0 warnings, got %d", len(warnings))
	}

	// Read the written zip
	reader := bytes.NewReader(buf.Bytes())
	zr, err := zip.NewReader(reader, int64(buf.Len()))
	if err != nil {
		t.Fatalf("Failed to read zip: %v", err)
	}

	// 1. mimetype must be the first file and uncompressed
	if len(zr.File) == 0 || zr.File[0].Name != "mimetype" {
		t.Fatalf("mimetype is not the first file")
	}
	if zr.File[0].Method != zip.Store {
		t.Errorf("mimetype is not uncompressed (Method: %d)", zr.File[0].Method)
	}

	// Read mimetype content
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatalf("Failed to open mimetype: %v", err)
	}
	defer rc.Close()
	mimetypeContent, _ := io.ReadAll(rc)
	if string(mimetypeContent) != "application/vnd.oasis.opendocument.text" {
		t.Errorf("Unexpected mimetype content: %q", string(mimetypeContent))
	}

	// Validate other files
	expectedFiles := map[string]bool{
		"META-INF/manifest.xml": false,
		"styles.xml":            false,
		"content.xml":           false,
	}

	for _, f := range zr.File {
		if _, ok := expectedFiles[f.Name]; ok {
			expectedFiles[f.Name] = true

			// Validate content.xml snippet
			if f.Name == "content.xml" {
				rc, _ := f.Open()
				content, _ := io.ReadAll(rc)
				rc.Close()
				sContent := string(content)
				if !bytes.Contains(content, []byte(`<text:h text:outline-level="2">Heading 2</text:h>`)) {
					t.Errorf("content.xml missing heading: %s", sContent)
				}
				if !bytes.Contains(content, []byte(`<text:p>Paragraph text</text:p>`)) {
					t.Errorf("content.xml missing paragraph: %s", sContent)
				}
				if !bytes.Contains(content, []byte(`<text:list-item>`)) {
					t.Errorf("content.xml missing list item: %s", sContent)
				}
			}
		}
	}

	for k, found := range expectedFiles {
		if !found {
			t.Errorf("Missing expected file: %s", k)
		}
	}
}
