package pdfrender

import (
	"bytes"
	"compress/zlib"
	"io"
	"strings"
	"testing"

	"example.com/parchment/internal/spreadsheet"
)

func TestRenderSpreadsheetPDF(t *testing.T) {
	book := spreadsheet.Spreadsheet{Sheets: []spreadsheet.Sheet{{Name: "Report", Rows: [][]spreadsheet.Cell{
		{{Value: "First cell"}, {}, {}, {}, {Value: "Fifth cell"}},
	}}}}
	var out bytes.Buffer
	if err := RenderSpreadsheet(&out, book); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out.Bytes(), []byte("%PDF-")) || !strings.Contains(out.String(), "/Count 2") ||
		!spreadsheetPDFContains(out.Bytes(), []byte("First cell")) || !spreadsheetPDFContains(out.Bytes(), []byte("Fifth cell")) {
		t.Fatalf("PDF missing data: header=%v pages=%v first=%v fifth=%v",
			bytes.HasPrefix(out.Bytes(), []byte("%PDF-")), strings.Contains(out.String(), "/Count 2"),
			spreadsheetPDFContains(out.Bytes(), []byte("First cell")), spreadsheetPDFContains(out.Bytes(), []byte("Fifth cell")))
	}
}

func spreadsheetPDFContains(pdf, text []byte) bool {
	for _, stream := range bytes.Split(pdf, []byte("stream\n"))[1:] {
		reader, err := zlib.NewReader(bytes.NewReader(stream))
		if err != nil {
			continue
		}
		content, err := io.ReadAll(reader)
		_ = reader.Close()
		if err == nil && bytes.Contains(content, text) {
			return true
		}
	}
	return false
}
